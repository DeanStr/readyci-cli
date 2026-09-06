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
			printVerificationSession(session)
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
			printVerificationSession(session)
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

func printVerificationSession(session preflightapi.VerificationSession) {
	fmt.Printf("session: %s\nstate: %s\nverdict: %s\n", session.SessionID, session.State, session.Verdict)
	if session.Classification != "" {
		fmt.Printf("classification: %s\n", session.Classification)
	}
	for _, node := range session.Nodes {
		fmt.Printf("%s: %s\n", node.Name, node.Status)
	}
}
