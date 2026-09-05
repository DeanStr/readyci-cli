package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func executeRemoteRunWithRetries(options remoteRunOptions, retries int) error {
	if retries < 0 {
		return fmt.Errorf("retry count must be non-negative")
	}
	attempts := retries + 1
	baseIdempotencyKey := options.IdempotencyKey
	for attempt := 1; attempt <= attempts; attempt++ {
		runOptions := options
		runOptions.Attempt = attempt
		if baseIdempotencyKey != "" && attempt > 1 {
			runOptions.IdempotencyKey = fmt.Sprintf("%s:__readyci_attempt=%d", baseIdempotencyKey, attempt)
		}
		err := executeRemoteRun(runOptions)
		if err == nil {
			return nil
		}
		exitCode, retryable := retryableRemoteRunError(err)
		if !retryable || attempt == attempts {
			return err
		}
		fmt.Fprintf(os.Stderr, "readyci: retrying %s after exit code %d (attempt %d/%d)\n", displayRunName(options), exitCode, attempt+1, attempts)
	}
	return nil
}

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
	variants, err := expandMatrix(matrix)
	if err != nil {
		return err
	}
	options.IdempotencyKey, err = normalizeIdempotencyKey(options.IdempotencyKey)
	if err != nil {
		return err
	}
	if options.IdempotencyKey == "" {
		options.IdempotencyKey, err = newIdempotencyKey()
		if err != nil {
			return err
		}
	}
	options = withDefaultRunGroup(options)
	maxParallel := matrix.MaxParallel
	if maxParallel < 1 {
		maxParallel = 1
	}
	if maxParallel > 1 && len(variants) > 1 {
		return executeCheckMatrixParallel(options, variants, retries, matrix.FailFast, maxParallel, executeRemoteRunWithRetries)
	}
	var lastErr error
	var successErr error
	for _, variant := range variants {
		runOptions := optionsForMatrixVariant(options, variant)
		if len(variants) > 1 {
			fmt.Fprintf(os.Stderr, "readyci: running %s\n", displayRunName(runOptions))
		}
		err := executeRemoteRunWithRetries(runOptions, retries)
		if err == nil {
			continue
		}
		if isSuccessfulExit(err) {
			successErr = err
			continue
		}
		lastErr = err
		var exitErr exitCodeError
		if !errors.As(err, &exitErr) {
			return err
		}
		if matrix.FailFast {
			break
		}
	}
	if lastErr != nil {
		return lastErr
	}
	return successErr
}

type matrixRunFunc func(remoteRunOptions, int) error

func executeCheckMatrixParallel(options remoteRunOptions, variants []matrixVariant, retries int, failFast bool, maxParallel int, run matrixRunFunc) error {
	type result struct {
		err error
	}
	results := make(chan result, len(variants))
	scheduled := 0
	completed := 0
	active := 0
	stopScheduling := false
	var lastErr error
	var successErr error

	for (!stopScheduling && scheduled < len(variants)) || completed < scheduled {
		for !stopScheduling && scheduled < len(variants) && active < maxParallel {
			variant := variants[scheduled]
			scheduled++
			runOptions := optionsForMatrixVariant(options, variant)
			active++
			go func() {
				if len(variants) > 1 {
					fmt.Fprintf(os.Stderr, "readyci: running %s\n", displayRunName(runOptions))
				}
				err := run(runOptions, retries)
				results <- result{err: err}
			}()
		}
		if completed >= scheduled {
			continue
		}
		runResult := <-results
		completed++
		active--
		err := runResult.err
		if err == nil {
			continue
		}
		if isSuccessfulExit(err) {
			successErr = err
			continue
		}
		var exitErr exitCodeError
		exitFailure := errors.As(err, &exitErr)
		if shouldReplaceMatrixError(lastErr, err) {
			lastErr = err
		}
		if !exitFailure || failFast {
			stopScheduling = true
		}
	}
	if lastErr != nil {
		return lastErr
	}
	return successErr
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

func watchCheck(options remoteRunOptions, matrix config.MatrixConfig, retries int, interval time.Duration) error {
	if interval <= 0 {
		return cliConfigError("--watch-interval must be positive", nil)
	}
	ctx, stop := signalContext()
	defer stop()

	fingerprint, err := workspaceFingerprint(ctx, workspaceFingerprintOptions{
		ExcludePatterns:  options.SyncExclude,
		IncludeUntracked: options.IncludeUntracked,
		ChangeMode:       options.ChangeMode,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "readyci: watching %s every %s\n", displayRunName(options), interval)
	if err := executeCheckMatrix(options, matrix, retries); err != nil {
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
				ExcludePatterns:  options.SyncExclude,
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
			if err := executeCheckMatrix(options, matrix, retries); err != nil {
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
