package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func executeAgentCheckMatrix(plan checkRunPlan) ([]agentVerifyRunReport, error) {
	return executeAgentCheckMatrixWithRunner(plan, executeAgentRunWithRetries)
}

type agentMatrixRunFunc func(remoteRunOptions, int) (remoteRunResult, error)

func executeAgentCheckMatrixWithRunner(plan checkRunPlan, run agentMatrixRunFunc) ([]agentVerifyRunReport, error) {
	variants, err := expandMatrix(plan.Matrix)
	if err != nil {
		return nil, err
	}
	maxParallel := plan.Matrix.MaxParallel
	if maxParallel < 1 {
		maxParallel = 1
	}
	if maxParallel > 1 && len(variants) > 1 {
		return executeAgentCheckMatrixParallelWithRunner(plan, variants, maxParallel, run)
	}
	var reports []agentVerifyRunReport
	var lastErr error
	var successErr error
	for _, variant := range variants {
		options := optionsForMatrixVariant(plan.Options, variant)
		result, err := run(options, plan.Retries)
		runReport := agentRunReportFromResult(plan.Name, variant.Name, options, result, err)
		reports = append(reports, runReport)
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
			return reports, err
		}
		if plan.Matrix.FailFast {
			break
		}
	}
	if lastErr != nil {
		return reports, lastErr
	}
	return reports, successErr
}

func executeAgentCheckMatrixParallelWithRunner(plan checkRunPlan, variants []matrixVariant, maxParallel int, run agentMatrixRunFunc) ([]agentVerifyRunReport, error) {
	type result struct {
		report agentVerifyRunReport
		err    error
	}
	results := make(chan result, len(variants))
	scheduled := 0
	completed := 0
	active := 0
	stopScheduling := false
	var reports []agentVerifyRunReport
	var lastErr error
	var successErr error

	for (!stopScheduling && scheduled < len(variants)) || completed < scheduled {
		for !stopScheduling && scheduled < len(variants) && active < maxParallel {
			variant := variants[scheduled]
			scheduled++
			options := optionsForMatrixVariant(plan.Options, variant)
			active++
			go func(variant matrixVariant, options remoteRunOptions) {
				runResult, err := run(options, plan.Retries)
				report := agentRunReportFromResult(plan.Name, variant.Name, options, runResult, err)
				results <- result{report: report, err: err}
			}(variant, options)
		}
		if completed >= scheduled {
			continue
		}
		runResult := <-results
		completed++
		active--
		reports = append(reports, runResult.report)
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
		if !exitFailure || plan.Matrix.FailFast {
			stopScheduling = true
		}
	}
	if lastErr != nil {
		return reports, lastErr
	}
	return reports, successErr
}

func executeAgentRunWithRetries(options remoteRunOptions, retries int) (remoteRunResult, error) {
	if retries < 0 {
		return remoteRunResult{}, fmt.Errorf("retry count must be non-negative")
	}
	attempts := retries + 1
	var result remoteRunResult
	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		attemptOptions := optionsForAgentRetryAttempt(options, attempt)
		result, err = executeRemoteRunInternal(attemptOptions, remoteRunOutputCollect)
		if err == nil || isSuccessfulExit(err) {
			return result, err
		}
		exitCode, retryable := retryableRemoteRunResult(result, err, remoteDisplayCommand(attemptOptions))
		if !retryable || attempt == attempts {
			return result, err
		}
		if options.Progress != nil {
			options.Progress.Emit(agentProgressEvent{
				Kind:         agentProgressRetrying,
				RunName:      displayRunName(attemptOptions),
				Attempt:      attempt + 1,
				AttemptCount: attempts,
				ExitCode:     intPtr(exitCode),
			})
		} else {
			fmt.Fprintf(os.Stderr, "readyci: retrying %s after exit code %d (attempt %d/%d)\n", displayRunName(attemptOptions), exitCode, attempt+1, attempts)
		}
	}
	return result, err
}

func optionsForAgentRetryAttempt(options remoteRunOptions, attempt int) remoteRunOptions {
	options.Attempt = attempt
	if attempt <= 1 || strings.TrimSpace(options.IdempotencyKey) == "" {
		return options
	}
	next := options
	next.Attempt = attempt
	next.IdempotencyKey = preflightapi.AgentRetryIdempotencyKey(options.IdempotencyKey, attempt)
	return next
}
