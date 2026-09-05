package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func sessionsCommand(args []string) error {
	if len(args) == 0 {
		return cliConfigError("usage: readyci sessions <get|watch|cancel|retry|rerun> SESSION_ID", nil)
	}
	switch args[0] {
	case "get", "watch", "cancel":
		return simpleSessionCommand(args[0], args[1:])
	case "retry":
		return retrySessionCommand(args[1:])
	case "rerun":
		return rerunSessionCommand(args[1:])
	default:
		return cliConfigError("usage: readyci sessions <get|watch|cancel|retry|rerun> SESSION_ID", nil)
	}
}

func simpleSessionCommand(command string, args []string) error {
	fs := flag.NewFlagSet("sessions "+command, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	jsonOutput := fs.Bool("json", false, "print machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 1 {
		return cliConfigError("usage: readyci sessions "+command+" SESSION_ID", nil)
	}
	token, err := effectiveAPIToken(*apiURL, *apiToken)
	if err != nil {
		return err
	}
	client := api.NewClient(*apiURL, api.WithToken(token))
	ctx, stop := signalContext()
	defer stop()
	var session preflightapi.VerificationSession
	switch command {
	case "get":
		session, err = client.VerificationSession(ctx, fs.Arg(0))
	case "watch":
		session, err = watchVerificationSession(ctx, client, fs.Arg(0), nil)
		if err == nil {
			session, err = refreshVerificationSessionCurrentness(ctx, client, session)
		}
	case "cancel":
		session, err = client.CancelVerificationSession(ctx, fs.Arg(0))
	}
	if err != nil {
		if command == "watch" && session.SchemaVersion != "" {
			exitCode := applyVerificationCommandError(&session, err)
			if outputErr := outputVerificationSession(session, *jsonOutput); outputErr != nil {
				return outputErr
			}
			return exitCodeError{code: exitCode}
		}
		return err
	}
	if *jsonOutput {
		if err := writeJSONOutput(session); err != nil {
			return err
		}
	} else {
		printVerificationSession(session)
	}
	if command == "watch" {
		return verificationVerdictExit(session)
	}
	return nil
}

func retrySessionCommand(args []string) error {
	fs := flag.NewFlagSet("sessions retry", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	jsonOutput := fs.Bool("json", false, "print machine-readable JSON")
	detach := fs.Bool("detach", false, "create the successor without waiting for completion")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 1 {
		return cliConfigError("usage: readyci sessions retry [--detach] [--json] SESSION_ID", nil)
	}
	token, err := effectiveAPIToken(*apiURL, *apiToken)
	if err != nil {
		return err
	}
	if strings.TrimSpace(token) == "" {
		return cliAuthMissingError("No Ready CI credential is available.", nil)
	}
	client := api.NewClient(*apiURL, api.WithToken(token))
	ctx, interruption, stop := interruptContext(context.Background())
	defer stop()
	response, err := client.RetryVerificationSession(ctx, fs.Arg(0), preflightapi.RetryVerificationSessionRequest{})
	if err != nil {
		if received := interruption.Signal(); received != nil {
			reconciled, reconcileErr := reconcileVerificationRetryAfterInterrupt(client, fs.Arg(0))
			if reconcileErr != nil {
				return cliInterruptedError{Signal: received, CancellationError: errors.Join(err, reconcileErr)}
			}
			return outputInterruptedVerificationSession(*apiURL, client, reconciled, received, *jsonOutput)
		}
		return err
	}
	session := response.Session
	if !*detach {
		watched, watchErr := watchVerificationSession(ctx, client, session.SessionID, nil)
		if watched.SchemaVersion != "" {
			session = watched
		}
		err = watchErr
		if err == nil {
			session, err = refreshVerificationSessionCurrentness(ctx, client, session)
		}
	}
	if received := interruption.Signal(); received != nil {
		return outputInterruptedVerificationSession(*apiURL, client, session, received, *jsonOutput)
	}
	if err != nil {
		if session.SchemaVersion == "" {
			return err
		}
		exitCode := applyVerificationCommandError(&session, err)
		if outputErr := outputVerificationSession(session, *jsonOutput); outputErr != nil {
			return outputErr
		}
		return exitCodeError{code: exitCode}
	}
	if err := outputVerificationSession(session, *jsonOutput); err != nil {
		return err
	}
	if *detach {
		return nil
	}
	return verificationVerdictExit(session)
}

func reconcileVerificationRetryAfterInterrupt(client *api.Client, predecessorID string) (preflightapi.VerificationSession, error) {
	reconcileCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	response, err := client.RetryVerificationSession(reconcileCtx, predecessorID, preflightapi.RetryVerificationSessionRequest{})
	if err != nil {
		return preflightapi.VerificationSession{}, err
	}
	if response.Session.SessionID == "" {
		return preflightapi.VerificationSession{}, errors.New("retry reconciliation returned no successor session")
	}
	return response.Session, nil
}

func outputInterruptedVerificationSession(apiURL string, client *api.Client, session preflightapi.VerificationSession, received os.Signal, jsonOutput bool) error {
	acknowledged := false
	cancelCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	cancelled, cancelErr := client.CancelVerificationSession(cancelCtx, session.SessionID)
	cancel()
	acknowledged = cancelErr == nil
	if cancelErr == nil {
		session = cancelled
	}
	session.ClientInterruption = &preflightapi.VerificationClientInterruption{
		Signal:                   received.String(),
		ResourceID:               session.SessionID,
		CancellationAcknowledged: acknowledged,
		StatusCommand:            verificationSessionRecoveryCommand(apiURL, "get", session.SessionID),
		CancelCommand:            verificationSessionRecoveryCommand(apiURL, "cancel", session.SessionID),
	}
	if outputErr := outputVerificationSession(session, jsonOutput); outputErr != nil {
		return outputErr
	}
	return exitCodeError{code: interruptionExitCode(received)}
}

func rerunSessionCommand(args []string) error {
	fs := flag.NewFlagSet("sessions rerun", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	jsonOutput := fs.Bool("json", false, "print machine-readable JSON")
	detach := fs.Bool("detach", false, "create and upload the successor without waiting for completion")
	idempotencyKey := fs.String("idempotency-key", "", "stable key for resumable successor creation")
	progressMode := fs.String("progress", envDefault("READYCI_PROGRESS", "auto"), "progress output: auto, status, or quiet")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 1 {
		return cliConfigError("usage: readyci sessions rerun [--detach] [--json] [--idempotency-key KEY] SESSION_ID", nil)
	}
	progress, err := newAgentProgressReporter(*progressMode, *jsonOutput, os.Stderr, stderrIsInteractive())
	if err != nil {
		return cliConfigError(err.Error(), err)
	}
	if progress.mode == agentProgressLogs {
		return cliConfigError("progress mode logs is not available for verification sessions; use readyci runs logs for a node run", nil)
	}
	token, err := effectiveAPIToken(*apiURL, *apiToken)
	if err != nil {
		return err
	}
	if strings.TrimSpace(token) == "" {
		return cliAuthMissingError("No Ready CI credential is available.", nil)
	}
	client := api.NewClient(*apiURL, api.WithToken(token))
	ctx, stop := signalContext()
	predecessor, err := client.VerificationSession(ctx, fs.Arg(0))
	stop()
	if err != nil {
		return err
	}
	if predecessor.State != preflightapi.VerificationStateTerminal {
		return cliConfigError("verification session must be terminal before rerun", nil)
	}
	if err := validateVerificationRerunRecipe(predecessor.Recipe); err != nil {
		return cliConfigError("verification session does not contain a supported rerun recipe: "+err.Error(), err)
	}
	session, runErr := createAndWatchVerificationSession(verificationSessionOptions{
		APIURL: *apiURL, APIToken: token, ProjectID: predecessor.ProjectID, CheckName: predecessor.Recipe.Check,
		Goal: predecessor.Goal, Intent: predecessor.Intent, IdempotencyKey: *idempotencyKey, TaskID: predecessor.TaskID,
		PredecessorID: predecessor.SessionID, Changes: predecessor.Recipe.ChangeMode,
		RequestSecrets: predecessor.Recipe.RequestSecrets, RetryCount: predecessor.Recipe.RetryCount,
		RetryProvided: predecessor.Recipe.RetryProvided, Detach: *detach, Progress: progress,
	})
	runErr = newCLISecretRedactor(args).RedactError(runErr)
	if session.SchemaVersion == "" {
		if runErr != nil {
			return runErr
		}
		return cliError{
			Code:           "platform_internal",
			ExitCode:       cliExitInternal,
			Classification: preflightapi.ClassificationPlatformInternalError,
			Message:        "verification rerun returned no successor session",
		}
	}
	exitCode := 0
	var interrupted cliInterruptedError
	if errors.As(runErr, &interrupted) {
		exitCode = interruptionExitCode(interrupted.Signal)
	} else if runErr != nil {
		exitCode = applyVerificationCommandError(&session, runErr)
	}
	if err := outputVerificationSession(session, *jsonOutput); err != nil {
		return err
	}
	if runErr != nil {
		return exitCodeError{code: exitCode}
	}
	if *detach {
		return nil
	}
	return verificationVerdictExit(session)
}

func validateVerificationRerunRecipe(recipe preflightapi.VerificationRecipe) error {
	return validateVerificationRecipe(recipe, true)
}

func validateVerificationRecipe(recipe preflightapi.VerificationRecipe, requireRerunnable bool) error {
	if recipe.SchemaVersion != preflightapi.VerificationRecipeSchemaVersion {
		return fmt.Errorf("unsupported schema version %q", recipe.SchemaVersion)
	}
	if requireRerunnable && !recipe.Rerunnable {
		return errors.New("recipe is marked non-rerunnable")
	}
	if recipe.Source == nil {
		return errors.New("source packaging contract is missing")
	}
	if strings.TrimSpace(recipe.Digest) == "" {
		return errors.New("recipe digest is missing")
	}
	digest, err := preflightapi.VerificationRecipeDigest(recipe)
	if err != nil {
		return err
	}
	if recipe.Digest != digest {
		return errors.New("recipe digest does not match its contents")
	}
	return nil
}

func outputVerificationSession(session preflightapi.VerificationSession, jsonOutput bool) error {
	if jsonOutput {
		return writeJSONOutput(session)
	}
	printVerificationSession(session)
	return nil
}
