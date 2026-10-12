package main

import (
	"flag"
	"os"
	"strings"

	"github.com/DeanStr/readyci-cli/internal/syncbundlebuild"
	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func agentPlanCommand(args []string) error {
	fs := flag.NewFlagSet("agent plan", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL := fs.String("api-url", envDefault("READYCI_API_URL", defaultAPIURL()), "Ready CI API URL")
	projectID := fs.String("project", envDefault("READYCI_PROJECT_ID", ""), "project id")
	checkName := fs.String("check", "", "configured check to run")
	goal := fs.String("goal", "", "short verification goal for check recommendation")
	intentFlag := fs.String("intent", "", "verification intent: edit-loop, pre-commit, pre-pr, fixture-update, security-sensitive, or final-handoff")
	changes := fs.String("changes", "", "changed-file mode override: working, staged, or all")
	requestSecrets := fs.Bool("request-secrets", false, "explicitly request authorized project secrets for the submitted run")
	jsonOutput := fs.Bool("json", false, "print machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci agent plan [--api-url URL] [--project PROJECT] [--json] [--check NAME] [--intent INTENT] [--goal TEXT] [--changes MODE] [--request-secrets]", nil)
	}
	report, err := buildAgentPlanReport(*apiURL, *projectID, *checkName, *goal, *intentFlag, *changes, *requestSecrets)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSONOutput(report)
	}
	printAgentPlanReport(report)
	return nil
}

func buildAgentPlanReport(apiURL, projectID, checkName, goal, intentFlag, changes string, requestSecrets bool) (agentPlanReport, error) {
	intent, err := normalizeAgentIntent(intentFlag)
	if err != nil {
		return agentPlanReport{}, cliConfigError(err.Error(), err)
	}
	if err := validateAgentSecretRequestForIntent(intent, requestSecrets); err != nil {
		return agentPlanReport{}, cliConfigError(err.Error(), err)
	}
	if err := validateExplicitDependencyWarmSecretRequest(checkName, intent, requestSecrets); err != nil {
		return agentPlanReport{}, cliConfigError(err.Error(), err)
	}
	changes = agentIntentDefaultChanges(intent, changes)
	loaded, err := loadProjectConfigOrInfer(".")
	if err != nil {
		return agentPlanReport{}, err
	}
	cfg := loaded.Config
	path := loaded.Path
	if shouldUseAffectedRules(cfg, checkName) && !agentIntentRequestsBroadVerification(intent, goal) {
		report, err := buildAgentAffectedPlanReport(apiURL, projectID, changes, goal, intent, requestSecrets, cfg, path, loaded.Inferred, loaded.Signals)
		if err == nil && loaded.Inferred {
			report.Reason = strings.TrimSpace(report.Reason + " Inferred checks from repository signals because no Ready CI config was found.")
		}
		return report, err
	}
	recommendation, err := recommendAgentCheckWithIntent(cfg, checkName, goal, intent)
	if err != nil {
		return agentPlanReport{}, err
	}
	resolveOptions := agentResolveOptionsForIntent(intent, checkResolveOptions{
		APIURL:    apiURL,
		ProjectID: projectID,
		Changes:   changes,
	})
	plan, err := resolveCheckRunPlan(recommendation.Check, cfg, resolveOptions)
	if err != nil {
		return agentPlanReport{}, err
	}
	if explicitDependencyWarmRequested(checkName, intent) {
		ctx, stop := signalContext()
		defer stop()
		targets, err := dependencyWarmTargetsForExplicitCheck(ctx, ".", cfg, path, resolveOptions, plan)
		if err != nil {
			return agentPlanReport{}, err
		}
		warmPlans, err := dependencyWarmPlansForExplicitCheck(plan, targets, cfg, resolveOptions)
		if err != nil {
			return agentPlanReport{}, err
		}
		if _, err := explicitDependencyWarmSessionBuildOptions(warmPlans, targets, syncbundlebuild.BuildOptions{
			ChangeMode:       plan.Options.ChangeMode,
			ExcludePatterns:  append([]string(nil), plan.Options.SyncExclude...),
			IncludeUntracked: plan.Options.IncludeUntracked,
		}); err != nil {
			return agentPlanReport{}, err
		}
		recommendation.Reason = "Explicit dependency warm check requested."
		for index := range warmPlans {
			applyAgentRemoteRunOptionsForIntent(intent, false, &warmPlans[index].Options)
		}
		return explicitDependencyWarmAgentPlanReport(
			cfg, path, loaded, recommendation, warmPlans, goal, intent, intentFlag, changes,
		), nil
	}
	applyAgentRemoteRunOptionsForIntent(intent, requestSecrets, &plan.Options)
	warmPlans, err := agentDependencyWarmPlansForAffected(".", []affectedCheckPlan{{Plan: plan}}, cfg, resolveOptions, intent)
	if err != nil {
		return agentPlanReport{}, err
	}
	if _, err := compatibleAffectedSessionBuildOptions(append(warmPlans, affectedCheckPlan{Plan: plan}), syncbundlebuild.BuildOptions{
		ChangeMode: plan.Options.ChangeMode, ExcludePatterns: plan.Options.SyncExclude, IncludeUntracked: plan.Options.IncludeUntracked,
	}); err != nil {
		return agentPlanReport{}, err
	}
	var warnings []agentWarning
	for _, warm := range warmPlans {
		warnings = append(warnings, agentWarning{Section: "preparation", Message: "Automatic dependency preparation: " + warm.Plan.Name +
			"; network_mode=" + warm.Plan.Options.NetworkMode + "; network_allowlist=" + strings.Join(warm.Plan.Options.NetworkAllowlist, ",") +
			". Verification uses the prepared cache in this session and retains its configured network policy."})
	}
	variantNames := make([]string, 0, len(plan.Variants))
	for _, variant := range plan.Variants {
		if variant.Name != "" {
			variantNames = append(variantNames, variant.Name)
		}
	}
	estimated := &agentCheckEstimatedCost{
		RunnerSize:     defaultString(plan.Options.RunnerSize, preflightapi.DefaultRunnerSize),
		TimeoutSeconds: defaultInt(plan.Options.TimeoutSeconds, defaultRunTimeoutSeconds),
	}
	return agentPlanReport{
		Status:         "ready",
		SchemaVersion:  agentSchemaVersion,
		Mode:           "check",
		Intent:         intent,
		Goal:           strings.TrimSpace(goal),
		Check:          plan.Name,
		Reason:         recommendation.Reason,
		Command:        plan.Options.Command,
		ProjectID:      plan.Options.ProjectID,
		ChangeMode:     plan.Options.ChangeMode,
		RunnerSize:     estimated.RunnerSize,
		TimeoutSeconds: estimated.TimeoutSeconds,
		MatrixVariants: variantNames,
		Alternatives:   recommendation.Alternatives,
		Warnings:       warnings,
		Estimated:      estimated,
		Plan: ptrVerificationPlan(buildCheckVerificationPlan(cfg, path, plan, verificationPlanOptions{
			Goal:           goal,
			Intent:         intent,
			Mode:           "check",
			Inferred:       loaded.Inferred,
			Signals:        loaded.Signals,
			SelectedReason: recommendation.Reason,
		})),
		Policy:        agentPolicyForPlanForIntent(plan.Options.ChangeMode, intent, requestSecrets, plan),
		VerifyCommand: agentVerifyCommandForPlan(plan, intentFlag),
	}, nil
}

func explicitDependencyWarmAgentPlanReport(
	cfg config.ProjectConfig,
	configPath string,
	loaded loadedReadyCIConfig,
	recommendation agentCheckRecommendation,
	warmPlans []checkRunPlan,
	goal string,
	intent string,
	intentFlag string,
	changes string,
) agentPlanReport {
	first := warmPlans[0]
	planReport := buildCheckVerificationPlan(cfg, configPath, first, verificationPlanOptions{
		Goal: goal, Intent: intent, Mode: "check", Inferred: loaded.Inferred, Signals: loaded.Signals,
		SelectedReason: recommendation.Reason,
	})
	confidence := confidenceForMode("explicit_check", loaded.Inferred, len(warmPlans), 0)
	planReport.Selected = make([]checkCandidate, 0, len(warmPlans))
	for _, warmPlan := range warmPlans {
		planReport.Selected = append(planReport.Selected, checkCandidateFromRunPlan(warmPlan, recommendation.Reason, confidence))
	}
	planReport.Confidence = confidence

	report := agentPlanReport{
		Status:        "ready",
		SchemaVersion: agentSchemaVersion,
		Mode:          "check",
		Intent:        intent,
		Goal:          strings.TrimSpace(goal),
		Check:         dependencyWarmCheckName,
		Reason:        recommendation.Reason,
		Command:       first.Options.Command,
		ProjectID:     first.Options.ProjectID,
		ChangeMode:    first.Options.ChangeMode,
		Alternatives:  recommendation.Alternatives,
		Plan:          &planReport,
		Policy:        agentPolicyForPlansForIntent(changes, intent, false, warmPlans),
		VerifyCommand: agentVerifyCommandForPlan(first, intentFlag),
	}
	if len(warmPlans) == 1 {
		report.RunnerSize = defaultString(first.Options.RunnerSize, preflightapi.DefaultRunnerSize)
		report.TimeoutSeconds = defaultInt(first.Options.TimeoutSeconds, defaultRunTimeoutSeconds)
		report.Estimated = &agentCheckEstimatedCost{RunnerSize: report.RunnerSize, TimeoutSeconds: report.TimeoutSeconds}
	}
	return report
}
