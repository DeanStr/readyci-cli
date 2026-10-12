package main

import (
	"errors"

	"strings"

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
