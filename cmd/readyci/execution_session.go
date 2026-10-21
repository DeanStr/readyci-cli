package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/internal/syncbundlebuild"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

// These durations are caller observations, never verification evidence.
type executionTimings struct {
	SourceCompletion   *preflightapi.PhaseTimings `json:"source_completion"`
	PackageMS          int64                      `json:"package_ms"`
	UploadMS           int64                      `json:"upload_and_validation_ms"`
	RemoteMS           int64                      `json:"remote_wait_ms"`
	TotalMS            int64                      `json:"total_ms"`
	UploadBytes        int64                      `json:"upload_bytes"`
	SessionCreateMS    *int64                     `json:"session_create_ms"`
	SourcePrepareMS    *int64                     `json:"source_prepare_ms"`
	SourceTransferMS   *int64                     `json:"source_transfer_ms"`
	SourceValidationMS *int64                     `json:"source_validation_ms"`
}

func observedDurationMS(start time.Time) *int64 {
	value := time.Since(start).Milliseconds()
	return &value
}

type executionSessionReport struct {
	Session preflightapi.VerificationSession `json:"session"`
	Timings executionTimings                 `json:"timings"`
	Plan    *verificationPlan                `json:"plan,omitempty"`
}

// Literal commands opt into retries through --retry. Project and check retry
// defaults apply only to named checks, including when rerunning a session.
func literalCommandRetries(retries int, provided bool) int {
	if provided {
		return retries
	}
	return 0
}

func executeSessionPlans(plans, warm []checkRunPlan, token string, jsonOutput bool, selection ...verificationPlan) error {
	var selectedPlan *verificationPlan
	if len(selection) > 0 {
		selectedPlan = &selection[0]
	}
	if len(plans) == 0 && len(warm) == 0 {
		return cliConfigError("no checks selected", nil)
	}
	all := append(append([]checkRunPlan{}, warm...), plans...)
	firstPlan := all[0]
	first := firstPlan.Options
	if len(plans) > 0 {
		firstPlan = plans[0]
		first = firstPlan.Options
	}
	requestSecrets := false
	selected := make([]affectedCheckPlan, 0, len(all))
	for i := range all {
		if i < len(warm) || all[i].Options.SecretsRequested == nil {
			all[i].Options.SecretsRequested = boolPtr(false)
		}
		requestSecrets = requestSecrets || *all[i].Options.SecretsRequested
		selected = append(selected, affectedCheckPlan{Plan: all[i]})
	}
	// Snapshot packaging is one shared contract across every node.
	build, err := compatibleAffectedSessionBuildOptions(selected, syncbundlebuild.BuildOptions{ChangeMode: first.ChangeMode, ExcludePatterns: first.SyncExclude, ReviewedFixtures: append([]syncbundle.ReviewedFixture(nil), first.SyncReviewedFixtures...), IncludeUntracked: first.IncludeUntracked})
	if err != nil {
		return cliConfigError(err.Error(), err)
	}

	project, err := executionProjectID(first.APIURL, first.ProjectID)
	if err != nil {
		return err
	}
	for i := range all {
		all[i].Options.ProjectID = project
	}
	warm = all[:len(warm)]
	plans = all[len(warm):]
	prepared := preparedVerificationSession{ProjectID: project, Plans: plans, WarmPlans: warm, BuildOptions: build, CoverageMode: "check"}
	if len(plans) == 1 {
		prepared.Plans[0].Options.APIToken = token
	}
	progress, err := newAgentProgressReporter("status", jsonOutput, os.Stderr, stderrIsInteractive())
	if err != nil {
		return err
	}
	timings := executionTimings{}
	began := time.Now()
	checkName, command := "", ""
	if len(plans) == 1 && (selectedPlan == nil || selectedPlan.Mode == "check" || selectedPlan.Mode == "explicit_check" || selectedPlan.Mode == "inferred_fallback") {
		checkName = first.CommandAlias
		if checkName == "" {
			command = first.Command
		}
	}
	var warmNames []string
	if selectedPlan != nil && selectedPlan.Mode == "warm" {
		for _, p := range warm {
			warmNames = appendUniqueString(warmNames, p.Name)
		}
	}
	logs := executionLogsForOutput(jsonOutput, token)
	session, runErr := createAndWatchVerificationSession(verificationSessionOptions{
		APIURL: first.APIURL, APIToken: token, ProjectID: project, Purpose: preflightapi.ExecutionPurpose,
		SkipWarm: firstPlan.SkipWarm, RetryCount: firstPlan.Retries, RetryProvided: firstPlan.RetryProvided, RequestSecrets: requestSecrets,
		Logs:       logs,
		WarmChecks: warmNames, SkipConfirm: len(warmNames) > 0 && len(plans) == 0,
		Prepared: &prepared, Timings: &timings, IdempotencyKey: first.IdempotencyKey,
		CheckName: checkName, Command: command, PredecessorID: first.RunGroupPredecessorID, Changes: first.ChangeMode, Detach: first.Detach, Progress: progress,
	})
	timings.TotalMS = time.Since(began).Milliseconds()
	if session.SchemaVersion == "" {
		return runErr
	}
	exit := executionSessionExit(session)
	if runErr != nil {
		code := applyVerificationCommandError(&session, runErr)
		exit = exitCodeError{code: code}
		if session.ClientError != nil {
			session.ClientError.Message = newCLISecretRedactor([]string{"--token", token}).Redact(session.ClientError.Message)
		}
	}
	if jsonOutput {
		if err := writeJSONOutput(executionSessionReport{Session: session, Timings: timings, Plan: selectedPlan}); err != nil {
			return err
		}
	} else {
		fmt.Printf("job: %s\nresult: %s\n", session.SessionID, session.Verdict)
		for _, node := range session.Nodes {
			fmt.Printf("%s: %s\n", node.Name, node.Status)
			for _, attempt := range node.Attempts {
				fmt.Printf("  run: %s (attempt %d)\n", attempt.RunID, attempt.Attempt)
				if attempt.QueuedAt != nil && attempt.ClaimedAt != nil {
					fmt.Printf("    queue: %dms\n", attempt.ClaimedAt.Sub(*attempt.QueuedAt).Milliseconds())
				}
				if attempt.ClaimedAt != nil && attempt.FinishedAt != nil {
					fmt.Printf("    worker: %dms (preparation, tests, and finalization)\n", attempt.FinishedAt.Sub(*attempt.ClaimedAt).Milliseconds())
				}
				if runErr == nil && attempt.Status != preflightapi.StatusSucceeded && preflightapi.IsTerminalRunStatus(attempt.Status) {
					printExecutionFailure(first.APIURL, token, attempt.RunID, logs.retainedWorkspace(attempt.RunID))
				}
			}
		}
		fmt.Printf("timing: package %dms, upload/validate %dms, remote %dms, total %dms; uploaded %d bytes\n", timings.PackageMS, timings.UploadMS, timings.RemoteMS, timings.TotalMS, timings.UploadBytes)
		printAgentWarnings(session.Warnings)
		if session.Currentness == preflightapi.VerificationCurrentnessStale {
			fmt.Fprintln(os.Stderr, "readyci: workspace changed while tests ran; these results describe the uploaded snapshot")
		}
		if runErr != nil && session.ClientError != nil {
			fmt.Fprintln(os.Stderr, "readyci: "+session.ClientError.Message)
		}
		if runErr != nil || first.Detach {
			fmt.Printf("inspect: readyci sessions get%s %s\n", apiURLFlag(first.APIURL), shellQuoteArg(session.SessionID))
		}
	}
	if first.Open {
		base := normalizeDashboardURLBase(first.DashboardURL)
		if base == "" {
			fmt.Fprintln(os.Stderr, "readyci: set READYCI_DASHBOARD_URL to use --open")
		} else {
			target := base + "/app/verification-sessions/" + url.PathEscape(session.SessionID)
			if err := openBrowser(target); err != nil {
				fmt.Fprintln(os.Stderr, "readyci: open "+target)
			}
		}
	}
	if first.Detach && runErr == nil {
		return nil
	}
	return exit
}

func executionSessionExit(session preflightapi.VerificationSession) error {
	if session.Recipe.Purpose == "" {
		return verificationVerdictExit(session)
	}
	if session.Recipe.Purpose != preflightapi.ExecutionPurpose {
		return fmt.Errorf("unsupported session recipe purpose %q", session.Recipe.Purpose)
	}
	if session.State != preflightapi.VerificationStateTerminal {
		return exitCodeError{code: cliExitVerificationUnknown}
	}
	if session.Verdict == preflightapi.VerificationVerdictChecksPassed {
		return nil
	}
	for _, node := range session.Nodes {
		if !node.Required || node.Status == preflightapi.StatusSucceeded || node.Status == preflightapi.VerificationNodeStatusSkipped || len(node.Attempts) == 0 {
			continue
		}
		attempt := node.Attempts[len(node.Attempts)-1]
		if preflightapi.IsTerminalRunStatus(attempt.Status) && attempt.ExitCode != nil && *attempt.ExitCode != 0 {
			return exitCodeError{code: *attempt.ExitCode}
		}
	}
	return exitCodeError{code: 1}
}

func printExecutionFailure(apiURL, token, runID string, retainedWorkspace bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := api.NewClient(apiURL, api.WithToken(token))
	redactor := newCLISecretRedactor([]string{"--token", token})
	detail, err := client.GetRun(ctx, runID)
	if err == nil {
		if detail.Summary.Classification != "" {
			fmt.Fprintf(os.Stderr, "readyci: classification: %s (retryable: %t)\n", redactor.Redact(detail.Summary.Classification), detail.Summary.Retryable)
		}
		if detail.Summary.FailureSummary != "" {
			fmt.Fprintf(os.Stderr, "readyci: %s\n", redactor.Redact(detail.Summary.FailureSummary))
		}
		if hint := runFailureDiagnosisHint(detail.Summary.Classification); hint != "" {
			fmt.Fprintf(os.Stderr, "readyci: hint: %s\n", redactor.Redact(hint))
		}
	} else if !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "readyci: failure details unavailable")
	}
	fmt.Fprintf(os.Stderr, "explain: %s\n", redactor.Redact(runRecoveryCommand(apiURL, "explain", runID)))
	fmt.Fprintf(os.Stderr, "logs: %s\n", redactor.Redact(runRecoveryCommand(apiURL, "logs", runID)))
	if retainedWorkspace {
		artifacts, err := client.ListArtifacts(ctx, runID)
		if err == nil && artifacts.RunID == runID {
			for _, artifact := range artifacts.Artifacts {
				if artifact.RunID == runID && artifact.SizeBytes > 0 && artifact.ExpiresAt.After(time.Now()) {
					fmt.Fprintf(os.Stderr, "readyci: inspect failed workspace: %s\n", redactor.Redact("readyci artifacts download"+apiURLFlag(apiURL)+" "+shellQuoteArg(runID)))
					break
				}
			}
		}
	}
}
