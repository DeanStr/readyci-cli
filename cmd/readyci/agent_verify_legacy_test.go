package main

import (
	"errors"
	"flag"
	"os"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func agentVerifyCommand(args []string) error {
	fs := flag.NewFlagSet("agent verify", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	projectID := fs.String("project", envDefault("READYCI_PROJECT_ID", ""), "project id")
	checkName := fs.String("check", "", "configured check to run")
	goal := fs.String("goal", "", "short verification goal for check recommendation")
	intentFlag := fs.String("intent", "", "verification intent: edit-loop, pre-commit, pre-pr, fixture-update, security-sensitive, or final-handoff")
	idempotencyKey := fs.String("idempotency-key", "", "stable key for resumable agent verification")
	changes := fs.String("changes", "", "changed-file mode override: working, staged, or all")
	requestSecrets := fs.Bool("request-secrets", false, "explicitly request authorized project secrets for the submitted run")
	retryCount := fs.Int("retry", -1, "retry failed commands this many times")
	jsonOutput := fs.Bool("json", false, "print machine-readable JSON")
	progressMode := fs.String("progress", envDefault("READYCI_PROGRESS", "auto"), "progress output: auto, status, quiet, or logs")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci verify [--json] [--check NAME] [--intent INTENT] [--goal TEXT] [--idempotency-key KEY] [--request-secrets]", nil)
	}
	if flagProvided(fs, "retry") && *retryCount < 0 {
		return cliConfigError("retry count must be non-negative", nil)
	}
	progress, err := newAgentProgressReporter(*progressMode, *jsonOutput, os.Stderr, stderrIsInteractive())
	if err != nil {
		return err
	}

	report, runErr := buildAgentVerifyReport(*apiURL, *apiToken, *projectID, *checkName, *goal, *intentFlag, *idempotencyKey, *changes, *requestSecrets, *retryCount, flagProvided(fs, "retry"), progress)
	if *jsonOutput {
		if err := writeJSONOutput(report); err != nil {
			return err
		}
	} else {
		printAgentVerifyReport(report)
	}
	if code := agentReportProcessExitCode(report); code != 0 {
		return exitCodeError{code: code}
	}
	if runErr != nil && !agentRunErrorCoveredByReport(report, runErr) {
		return agentExitError(runErr)
	}
	return nil
}

func buildAgentVerifyReport(apiURL, apiToken, projectID, checkName, goal, intentFlag, idempotencyKey, changes string, requestSecrets bool, retryCount int, retryProvided bool, progress agentProgressSink) (agentVerifyReport, error) {
	intent, err := normalizeAgentIntent(intentFlag)
	if err != nil {
		return agentCommandErrorReport(preflightapi.ClassificationConfigInvalid, err), err
	}
	if err := validateAgentSecretRequestForIntent(intent, requestSecrets); err != nil {
		return agentCommandErrorReport(preflightapi.ClassificationConfigInvalid, err), err
	}
	changes = agentIntentDefaultChanges(intent, changes)
	loaded, err := loadProjectConfigOrInfer(".")
	if err != nil {
		if errors.Is(err, errCouldNotInferSafeCheck) {
			return agentCommandErrorReport(preflightapi.ClassificationVerificationUnknown, err), err
		}
		return agentCommandErrorReport("config_invalid", err), err
	}
	cfg := loaded.Config
	path := loaded.Path
	if shouldUseAffectedRules(cfg, checkName) && !agentIntentRequestsBroadVerification(intent, goal) {
		return buildAgentAffectedVerifyReport(apiURL, apiToken, projectID, idempotencyKey, changes, goal, intent, requestSecrets, retryCount, retryProvided, cfg, path, progress)
	}
	recommendation, err := recommendAgentCheckWithIntent(cfg, checkName, goal, intent)
	if err != nil {
		return agentCommandErrorReport("config_invalid", err), err
	}
	resolveOptions := agentResolveOptionsForIntent(intent, checkResolveOptions{
		APIURL:        apiURL,
		ProjectID:     projectID,
		Changes:       changes,
		RetryCount:    retryCount,
		RetryProvided: retryProvided,
	})
	plan, err := resolveCheckRunPlan(recommendation.Check, cfg, resolveOptions)
	if err != nil {
		return agentCommandErrorReport("config_invalid", err), err
	}
	applyAgentRemoteRunOptionsForIntent(intent, requestSecrets, &plan.Options)
	token, err := effectiveAPIToken(apiURL, apiToken)
	if err != nil {
		return agentCommandErrorReport("auth_missing", err), err
	}
	if strings.TrimSpace(token) == "" {
		err := errors.New("no API token found; set READYCI_API_TOKEN")
		return agentCommandErrorReport("auth_missing", err), err
	}
	plan.Options.APIToken = token
	baseKey, err := normalizeIdempotencyKey(idempotencyKey)
	if err != nil {
		return agentCommandErrorReport(preflightapi.ClassificationConfigInvalid, err), err
	}
	if baseKey == "" {
		baseKey, err = newIdempotencyKey()
		if err != nil {
			return agentCommandErrorReport(preflightapi.ClassificationInternalError, err), err
		}
	}
	plan.Options.IdempotencyKey = baseKey
	plan.Options.Progress = progress
	plan.Options.RunGroupKey = baseKey
	plan.Options.RunGroupKind = "check"
	plan.Options.RunGroupName = agentRunGroupNameForIntent(intent, plan.Name)
	plan.Options.LogicalChildID = defaultString(plan.Options.LogicalChildID, plan.Name)
	plan.Options.LogicalChildName = defaultString(plan.Options.LogicalChildName, plan.Name)
	if recommendation.Estimated == nil {
		recommendation.Estimated = &agentCheckEstimatedCost{
			RunnerSize:     defaultString(plan.Options.RunnerSize, preflightapi.DefaultRunnerSize),
			TimeoutSeconds: defaultInt(plan.Options.TimeoutSeconds, defaultRunTimeoutSeconds),
		}
	}
	report := agentVerifyReport{
		SchemaVersion:  agentSchemaVersion,
		Status:         "running",
		Mode:           "check",
		Intent:         intent,
		Goal:           strings.TrimSpace(goal),
		GroupKind:      "check",
		Check:          plan.Name,
		Recommendation: &recommendation,
		Policy:         agentPolicyForPlanForIntent(plan.Options.ChangeMode, intent, requestSecrets, plan),
		ArtifactPaths:  append([]string(nil), plan.Options.ArtifactPaths...),
	}
	emitAgentProgress(progress, agentProgressEvent{Kind: agentProgressPlanned, Mode: "check", Checks: []string{plan.Name}})
	runs, runErr := executeAgentCheckMatrix(plan)
	report.Runs = runs
	report = summarizeAgentVerifyRuns(report)
	if intent == agentIntentFixtureUpdate {
		report.NextActions = append(report.NextActions, agentFixtureArtifactActions(apiURL, report.RunID, plan.Options.ArtifactPaths)...)
	}
	return report, runErr
}
