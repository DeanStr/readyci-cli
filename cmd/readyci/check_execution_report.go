package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

type checkExecutionReport struct {
	Plan        *verificationPlan          `json:"plan,omitempty"`
	CreatedRuns []checkExecutionRunReport  `json:"created_runs"`
	Result      checkExecutionResultReport `json:"result"`
}

type checkExecutionRunReport struct {
	RunID         string `json:"run_id"`
	RunGroupID    string `json:"run_group_id,omitempty"`
	Phase         string `json:"phase,omitempty"`
	Check         string `json:"check,omitempty"`
	MatrixVariant string `json:"matrix_variant,omitempty"`
	Attempt       int    `json:"attempt,omitempty"`
	Status        string `json:"status,omitempty"`
	DashboardURL  string `json:"dashboard_url,omitempty"`
}

type checkExecutionResultReport struct {
	Status         string `json:"status"`
	Classification string `json:"classification,omitempty"`
	ExitCode       int    `json:"exit_code"`
	Retryable      bool   `json:"retryable,omitempty"`
	Summary        string `json:"summary,omitempty"`
}

func executeCheckPlanJSONReport(plan checkRunPlan, cfgPlan verificationPlan, token string) (checkExecutionReport, error) {
	plan.Options.APIToken = token
	runs, result, err := executeCheckMatrixForReport(plan.Options, plan.Matrix, plan.Retries)
	return checkExecutionReport{
		Plan:        &cfgPlan,
		CreatedRuns: runs,
		Result:      result,
	}, err
}

func executeCheckMatrixForReport(options remoteRunOptions, matrix config.MatrixConfig, retries int) ([]checkExecutionRunReport, checkExecutionResultReport, error) {
	variants, err := expandMatrix(matrix)
	if err != nil {
		return nil, checkExecutionResultReport{}, err
	}
	options.IdempotencyKey, err = normalizeIdempotencyKey(options.IdempotencyKey)
	if err != nil {
		return nil, checkExecutionResultReport{}, err
	}
	if options.IdempotencyKey == "" {
		options.IdempotencyKey, err = newIdempotencyKey()
		if err != nil {
			return nil, checkExecutionResultReport{}, err
		}
	}
	options = withDefaultRunGroup(options)
	maxParallel := matrix.MaxParallel
	if maxParallel < 1 {
		maxParallel = 1
	}
	if maxParallel > 1 && len(variants) > 1 {
		return executeCheckMatrixForReportParallel(options, variants, retries, matrix.FailFast, maxParallel)
	}
	var runs []checkExecutionRunReport
	var lastResult remoteRunResult
	var lastFailureResult remoteRunResult
	var lastErr error
	var successErr error
	baseCheck := strings.TrimSpace(options.CommandAlias)
	for _, variant := range variants {
		runOptions := optionsForMatrixVariant(options, variant)
		variantRuns, result, err := executeRemoteRunWithRetriesForReport(runOptions, retries, baseCheck)
		runs = append(runs, variantRuns...)
		lastResult = result
		if err == nil {
			continue
		}
		if isSuccessfulExit(err) {
			successErr = err
			continue
		}
		lastErr = err
		lastFailureResult = result
		var exitErr exitCodeError
		if !errors.As(err, &exitErr) {
			return runs, checkExecutionResultFromRun(result, err), err
		}
		if matrix.FailFast {
			break
		}
	}
	if lastErr != nil {
		return runs, checkExecutionResultFromRun(lastFailureResult, lastErr), lastErr
	}
	return runs, checkExecutionResultFromRun(lastResult, successErr), successErr
}

func executeCheckMatrixForReportParallel(options remoteRunOptions, variants []matrixVariant, retries int, failFast bool, maxParallel int) ([]checkExecutionRunReport, checkExecutionResultReport, error) {
	return executeCheckMatrixForReportParallelWithRunner(options, variants, retries, failFast, maxParallel, executeRemoteRunWithRetriesForReport)
}

type matrixReportRunFunc func(remoteRunOptions, int, string) ([]checkExecutionRunReport, remoteRunResult, error)

func executeCheckMatrixForReportParallelWithRunner(options remoteRunOptions, variants []matrixVariant, retries int, failFast bool, maxParallel int, run matrixReportRunFunc) ([]checkExecutionRunReport, checkExecutionResultReport, error) {
	type result struct {
		runs   []checkExecutionRunReport
		result remoteRunResult
		err    error
	}
	results := make(chan result, len(variants))
	scheduled := 0
	completed := 0
	active := 0
	stopScheduling := false
	var runs []checkExecutionRunReport
	var lastResult remoteRunResult
	var lastFailureResult remoteRunResult
	var lastErr error
	var successErr error
	baseCheck := strings.TrimSpace(options.CommandAlias)

	for (!stopScheduling && scheduled < len(variants)) || completed < scheduled {
		for !stopScheduling && scheduled < len(variants) && active < maxParallel {
			variant := variants[scheduled]
			scheduled++
			runOptions := optionsForMatrixVariant(options, variant)
			active++
			go func() {
				variantRuns, runResult, err := run(runOptions, retries, baseCheck)
				results <- result{runs: variantRuns, result: runResult, err: err}
			}()
		}
		if completed >= scheduled {
			continue
		}
		runResult := <-results
		completed++
		active--
		runs = append(runs, runResult.runs...)
		lastResult = runResult.result
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
			lastFailureResult = runResult.result
		}
		if !exitFailure || failFast {
			stopScheduling = true
		}
	}
	if lastErr != nil {
		return runs, checkExecutionResultFromRun(lastFailureResult, lastErr), lastErr
	}
	return runs, checkExecutionResultFromRun(lastResult, successErr), successErr
}

func executeRemoteRunWithRetriesForReport(options remoteRunOptions, retries int, checkName string) ([]checkExecutionRunReport, remoteRunResult, error) {
	if retries < 0 {
		return nil, remoteRunResult{}, fmt.Errorf("retry count must be non-negative")
	}
	attempts := retries + 1
	baseIdempotencyKey := options.IdempotencyKey
	var runs []checkExecutionRunReport
	var lastResult remoteRunResult
	for attempt := 1; attempt <= attempts; attempt++ {
		runOptions := options
		runOptions.Attempt = attempt
		if baseIdempotencyKey != "" && attempt > 1 {
			runOptions.IdempotencyKey = fmt.Sprintf("%s:__readyci_attempt=%d", baseIdempotencyKey, attempt)
		}
		result, err := executeRemoteRunInternal(runOptions, remoteRunOutputCollect)
		lastResult = result
		if result.RunID != "" {
			runs = append(runs, checkExecutionRunReport{
				RunID:         result.RunID,
				RunGroupID:    result.RunGroupID,
				Check:         defaultString(checkName, runOptions.CommandAlias),
				MatrixVariant: runOptions.MatrixVariant,
				Attempt:       attempt,
				Status:        result.Status,
				DashboardURL:  runDashboardURL(runOptions.DashboardURL, result.RunID),
			})
		}
		if err == nil || isSuccessfulExit(err) {
			return runs, result, err
		}
		_, retryable := retryableRemoteRunResult(result, err, remoteDisplayCommand(runOptions))
		if !retryable || attempt == attempts {
			return runs, result, err
		}
	}
	return runs, lastResult, nil
}

func checkExecutionResultFromRun(result remoteRunResult, err error) checkExecutionResultReport {
	if err == nil || isSuccessfulExit(err) {
		return checkExecutionResultReport{
			Status:         defaultString(result.Status, preflightapi.StatusSucceeded),
			Classification: preflightapi.ClassificationSuccess,
			ExitCode:       result.ExitCode,
		}
	}
	analysis := analyzeRemoteRunFailure(result, err, "")
	status := defaultString(result.Status, runFailureStatusFromError(err))
	if status == "" {
		status = "unknown"
	}
	exitCode := result.ExitCode
	if exitCode == 0 {
		var exitErr exitCodeError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.code
		} else {
			exitCode = 1
		}
	}
	return checkExecutionResultReport{
		Status:         status,
		Classification: analysis.Classification,
		ExitCode:       exitCode,
		Retryable:      analysis.Retryable,
		Summary:        strings.TrimSpace(oneLine(analysis.Summary)),
	}
}

func skippedCheckExecutionReport(plan verificationPlan, reason string) checkExecutionReport {
	return checkExecutionReport{
		Plan:        &plan,
		CreatedRuns: []checkExecutionRunReport{},
		Result: checkExecutionResultReport{
			Status:         "skipped",
			Classification: "skipped",
			ExitCode:       0,
			Summary:        reason,
		},
	}
}
