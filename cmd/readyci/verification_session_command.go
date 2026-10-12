package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func verificationCommand(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	projectID := fs.String("project", envDefault("READYCI_PROJECT_ID", ""), "project id")
	checkName := fs.String("check", "", "configured check to run; defaults to affected verification or an inferred check")
	goal := fs.String("goal", "", "short verification objective")
	intentFlag := fs.String("intent", "", "verification intent")
	idempotencyKey := fs.String("idempotency-key", "", "stable key for resumable verification")
	taskID := fs.String("task-id", "", "stable task identifier shared by successor sessions")
	predecessorID := fs.String("predecessor", "", "prior verification session for this task")
	changes := fs.String("changes", "", "changed-file mode: working, staged, or all")
	requestSecrets := fs.Bool("request-secrets", false, "request authorized project secrets")
	retryCount := fs.Int("retry", -1, "maximum retries per node")
	jsonOutput := fs.Bool("json", false, "print one machine-readable JSON document")
	progressMode := fs.String("progress", envDefault("READYCI_PROGRESS", "auto"), "progress output: auto, status, or quiet")
	detach := fs.Bool("detach", false, "create and upload the sealed session without waiting for completion")
	probeDirectSourceUploadInvariants := fs.Bool("probe-direct-source-upload-invariants", false, "verify conditional PUT, Content-MD5, and descriptor enforcement on the advertised direct source upload")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci verify [--json] [--detach] [--check NAME] [--intent INTENT] [--goal TEXT] [--task-id ID] [--idempotency-key KEY] [--predecessor SESSION_ID]", nil)
	}
	if flagProvided(fs, "retry") && *retryCount < 0 {
		return cliConfigError("retry count must be non-negative", nil)
	}
	progress, err := newAgentProgressReporter(*progressMode, *jsonOutput, os.Stderr, stderrIsInteractive())
	if err != nil {
		return cliConfigError(err.Error(), err)
	}
	if progress.mode == agentProgressLogs {
		return cliConfigError("progress mode logs is not available for verification sessions; use readyci runs logs for a node run", nil)
	}
	session, runErr := createAndWatchVerificationSession(verificationSessionOptions{
		APIURL: *apiURL, APIToken: *apiToken, ProjectID: *projectID, CheckName: *checkName, Goal: *goal, Intent: *intentFlag,
		IdempotencyKey: *idempotencyKey, TaskID: *taskID, PredecessorID: *predecessorID, Changes: *changes,
		RequestSecrets: *requestSecrets, RetryCount: *retryCount, RetryProvided: flagProvided(fs, "retry"), Detach: *detach, Progress: progress,
		ProbeDirectSourceUploadInvariants: *probeDirectSourceUploadInvariants,
	})
	runErr = newCLISecretRedactor(args).RedactError(runErr)
	var interrupted cliInterruptedError
	if errors.As(runErr, &interrupted) {
		if session.SchemaVersion == "" {
			return runErr
		}
		if *jsonOutput {
			if err := writeJSONOutput(session); err != nil {
				return err
			}
		} else {
			printVerificationSession(session, *apiURL)
			fmt.Fprintln(os.Stderr, "readyci:", newCLISecretRedactor(args).Redact(interrupted.Error()))
		}
		return exitCodeError{code: interruptionExitCode(interrupted.Signal)}
	}
	commandErrorExitCode := 0
	if session.SchemaVersion != "" && runErr != nil {
		commandErrorExitCode = applyVerificationCommandError(&session, runErr)
	}
	if session.SchemaVersion != "" {
		if *jsonOutput {
			if err := writeJSONOutput(session); err != nil {
				return err
			}
		} else {
			printVerificationSession(session, *apiURL)
		}
	}
	if runErr != nil {
		if session.SchemaVersion == "" {
			return runErr
		}
		if !*jsonOutput {
			fmt.Fprintln(os.Stderr, "readyci:", runErr)
		}
		return exitCodeError{code: commandErrorExitCode}
	}
	if *detach {
		return nil
	}
	return verificationVerdictExit(session)
}

func applyVerificationCommandError(session *preflightapi.VerificationSession, err error) int {
	classification := classifyVerificationCommandError(err)
	normalized := normalizeCLIError(err)
	if session != nil {
		session.ClientError = &preflightapi.VerificationClientError{
			Code:              normalized.Code,
			Classification:    classification,
			Retryable:         normalized.Retryable,
			RetryAfterSeconds: normalized.RetryAfterSeconds,
			Message:           normalized.Message,
		}
	}
	return normalized.ExitCode
}

func classifyVerificationCommandError(err error) string {
	if errors.Is(err, errVerificationTaskMismatch) {
		return preflightapi.ClassificationConfigInvalid
	}
	return classifyCommandError(err)
}

func verificationVerdictExit(session preflightapi.VerificationSession) error {
	if session.Currentness == preflightapi.VerificationCurrentnessStale {
		return exitCodeError{code: cliExitNonAuthoritative}
	}
	switch session.Verdict {
	case preflightapi.VerificationVerdictVerified, preflightapi.VerificationVerdictNoChangeRequired, preflightapi.VerificationVerdictNoVerificationNeeded:
		return nil
	case preflightapi.VerificationVerdictChecksPassed, preflightapi.VerificationVerdictInsufficientEvidence, preflightapi.VerificationVerdictStale:
		return exitCodeError{code: cliExitNonAuthoritative}
	default:
		return exitCodeError{code: 1}
	}
}

func printVerificationSession(session preflightapi.VerificationSession, apiURL string) {
	fmt.Printf("session: %s\nstate: %s\nverdict: %s\n", session.SessionID, session.State, session.Verdict)
	if session.Classification != "" {
		fmt.Printf("classification: %s\n", session.Classification)
	}
	if session.Currentness != "" {
		fmt.Printf("currentness: %s\n", session.Currentness)
	}
	if session.TerminalReason != "" {
		fmt.Printf("reason: %s\n", sanitizeRunLogsSummaryText(session.TerminalReason))
	}
	for _, node := range session.Nodes {
		fmt.Printf("%s: %s\n", node.Name, node.Status)
	}
	printVerificationRecovery(session, apiURL)
}

func printVerificationRecovery(session preflightapi.VerificationSession, apiURL string) {
	if session.State != preflightapi.VerificationStateTerminal || session.Recipe.Purpose == preflightapi.ExecutionPurpose {
		return
	}
	stale := session.Currentness == preflightapi.VerificationCurrentnessStale || session.Verdict == preflightapi.VerificationVerdictStale
	if stale {
		fmt.Println("incomplete: This recorded result does not describe your current workspace.")
	}
	for i, path := range session.Coverage.UncoveredFiles {
		if i == 10 {
			fmt.Printf("uncovered_file: ... %d more (use --json for the complete list)\n", len(session.Coverage.UncoveredFiles)-i)
			break
		}
		fmt.Printf("uncovered_file: %s\n", sanitizeRunLogsSummaryText(path))
	}
	// Policy and evidence gaps still need attention when the snapshot is stale.
	// A fresh snapshot alone cannot make these results authoritative.
	switch session.Classification {
	case preflightapi.ClassificationHarnessGap:
		fmt.Println("incomplete: The registered verification policy leaves changed files uncovered.")
		fmt.Println("next: Update affected.rules or affected.unmatched_checks to cover these files, then have an owner or admin review and activate the policy in Settings → Project. Rerunning alone cannot close this gap.")
		return
	case preflightapi.ClassificationClientProposedPlan:
		fmt.Println("incomplete: The service did not authorize this plan as sufficient verification.")
		fmt.Println("next: Have an owner or admin review and activate a registered verification policy in Settings → Project. Rerunning alone cannot authorize the plan.")
		return
	case preflightapi.ClassificationUntrustedEvidence:
		fmt.Println("incomplete: Passing checks lack service-validated execution evidence.")
		fmt.Println("next: Ask the operator to inspect worker approval and runner receipt bindings using this session ID before rerunning.")
		return
	case preflightapi.ClassificationPartialVerification:
		if session.Verdict == preflightapi.VerificationVerdictChecksPassed {
			fmt.Println("incomplete: Selected checks passed, but this scoped result did not establish workspace verification.")
		} else {
			fmt.Println("incomplete: This scoped request did not establish workspace verification.")
		}
		printFreshVerificationCommand(session, apiURL, true)
		return
	}
	if stale {
		switch session.Verdict {
		case preflightapi.VerificationVerdictVerified, preflightapi.VerificationVerdictNoChangeRequired,
			preflightapi.VerificationVerdictNoVerificationNeeded, preflightapi.VerificationVerdictStale:
			printFreshVerificationCommand(session, apiURL, false)
		default:
			fmt.Println("next: Inspect the recorded result and node evidence, and address any failures or setup problems before verifying the current workspace.")
		}
	} else if session.Verdict == preflightapi.VerificationVerdictChecksPassed || session.Verdict == preflightapi.VerificationVerdictInsufficientEvidence {
		fmt.Println("incomplete: The available evidence did not establish workspace verification.")
		fmt.Println("next: Inspect the terminal reason and node evidence before deciding whether to repair or rerun.")
	}
}

func printFreshVerificationCommand(session preflightapi.VerificationSession, apiURL string, affected bool) {
	// Apply the same recipe validation as sessions rerun before copying its
	// scope, retry, or secret settings into a recovery command.
	if err := validateVerificationRerunRecipe(session.Recipe); err != nil {
		fmt.Printf("recovery_unavailable: Session recipe cannot be replayed: %s\n", sanitizeRunLogsSummaryText(err.Error()))
		fmt.Println("next: Start a new verification with explicitly reviewed settings from the intended workspace.")
		return
	}
	if affected {
		fmt.Println("next: Run affected verification against the current workspace.")
		fmt.Printf("next_command: %s\n", affectedVerificationRecoveryCommand(session, apiURL))
	} else {
		fmt.Println("next: Submit the current workspace for a fresh verification result.")
		fmt.Printf("next_command: %s\n", verificationSessionRecoveryCommand(apiURL, "rerun", session.SessionID))
	}
}

func affectedVerificationRecoveryCommand(session preflightapi.VerificationSession, apiURL string) string {
	command := "readyci verify --json --progress=status" + apiURLFlag(apiURL)
	for _, flag := range []struct{ name, value string }{
		{"project", session.ProjectID}, {"intent", session.Intent},
		{"changes", session.Recipe.ChangeMode}, {"predecessor", session.SessionID},
		{"goal", session.Goal},
	} {
		if flag.value != "" {
			command += " --" + flag.name + " " + shellQuoteArg(flag.value)
		}
	}
	if session.Recipe.RetryProvided {
		command += fmt.Sprintf(" --retry %d", session.Recipe.RetryCount)
	}
	if session.Recipe.RequestSecrets {
		command += " --request-secrets"
	}
	return command
}
