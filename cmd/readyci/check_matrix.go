package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func retryableRemoteRunError(err error) (int, bool) {
	var terminalErr terminalRunError
	terminalEventType := ""
	if errors.As(err, &terminalErr) {
		terminalEventType = terminalErr.eventType
		if terminalEventType == preflightapi.EventRunCancelled {
			return terminalErr.exitCode, false
		}
	}
	var exitErr exitCodeError
	if !errors.As(err, &exitErr) || exitErr.code == 0 {
		return 0, false
	}
	if errors.Is(err, errRunStreamEndedBeforeTerminal) {
		return exitErr.code, false
	}
	var attemptErr remoteRunAttemptError
	if errors.As(err, &attemptErr) {
		analysis := analyzeRemoteRunFailureForRetry(attemptErr.result, attemptErr.err, attemptErr.command)
		return exitErr.code, analysis.Retryable
	}
	analysis := analyzeRemoteRunFailureForRetry(remoteRunResult{
		Status:            runFailureStatusFromError(err),
		TerminalEventType: terminalEventType,
		ExitCode:          exitErr.code,
	}, err, "")
	return exitErr.code, analysis.Retryable
}

func retryableRemoteRunResult(result remoteRunResult, err error, command string) (int, bool) {
	var terminalErr terminalRunError
	if errors.As(err, &terminalErr) && terminalErr.eventType == preflightapi.EventRunCancelled {
		return terminalErr.exitCode, false
	}
	var exitErr exitCodeError
	if !errors.As(err, &exitErr) || exitErr.code == 0 {
		return 0, false
	}
	if errors.Is(err, errRunStreamEndedBeforeTerminal) {
		return exitErr.code, false
	}
	analysis := analyzeRemoteRunFailureForRetry(result, err, command)
	return exitErr.code, analysis.Retryable
}

func executeCheckMatrix(options remoteRunOptions, matrix config.MatrixConfig, retries int) error {
	return executeSessionPlans([]checkRunPlan{{Name: defaultString(options.CommandAlias, "check"), Options: options, Matrix: matrix, Retries: retries}}, nil, options.APIToken, false)
}

func shouldReplaceMatrixError(current, candidate error) bool {
	if candidate == nil || isSuccessfulExit(candidate) {
		return false
	}
	if current == nil {
		return true
	}
	var currentExitErr exitCodeError
	currentIsExit := errors.As(current, &currentExitErr)
	var candidateExitErr exitCodeError
	candidateIsExit := errors.As(candidate, &candidateExitErr)
	if !candidateIsExit {
		return currentIsExit
	}
	return currentIsExit
}

func watchCheck(plan checkRunPlan, warm []checkRunPlan, token string, interval time.Duration) error {
	options := plan.Options
	if interval <= 0 {
		return cliConfigError("--watch-interval must be positive", nil)
	}
	ctx, stop := signalContext()
	defer stop()

	fingerprint, err := workspaceFingerprint(ctx, workspaceFingerprintOptions{
		ExcludePatterns: options.SyncExclude, ReviewedFixtures: append([]syncbundle.ReviewedFixture(nil), options.SyncReviewedFixtures...),
		IncludeUntracked: options.IncludeUntracked,
		ChangeMode:       options.ChangeMode,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "readyci: watching %s every %s\n", displayRunName(options), interval)
	if err := executeSessionPlans([]checkRunPlan{plan}, warm, token, false); err != nil {
		if !isSuccessfulExit(err) {
			var exitErr exitCodeError
			if !errors.As(err, &exitErr) {
				return err
			}
		}
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			next, err := workspaceFingerprint(ctx, workspaceFingerprintOptions{
				ExcludePatterns: options.SyncExclude, ReviewedFixtures: append([]syncbundle.ReviewedFixture(nil), options.SyncReviewedFixtures...),
				IncludeUntracked: options.IncludeUntracked,
				ChangeMode:       options.ChangeMode,
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "readyci: watch skipped: %v\n", err)
				continue
			}
			if next == fingerprint {
				continue
			}
			fingerprint = next
			fmt.Fprintln(os.Stderr, "readyci: change detected")
			if err := executeSessionPlans([]checkRunPlan{plan}, warm, token, false); err != nil {
				if !isSuccessfulExit(err) {
					var exitErr exitCodeError
					if errors.As(err, &exitErr) {
						continue
					}
					return err
				}
			}
		}
	}
}

func isSuccessfulExit(err error) bool {
	var exitErr exitCodeError
	return errors.As(err, &exitErr) && exitErr.code == 0
}
