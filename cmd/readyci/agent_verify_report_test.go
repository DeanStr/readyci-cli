package main

import (
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestAgentSummaryPrioritizesTerminalFailureOverPendingRun(t *testing.T) {
	report := summarizeAgentVerifyRuns(agentVerifyReport{
		Check: "matrix",
		Runs: []agentVerifyRunReport{
			{
				RunID:          "run_pending",
				Status:         preflightapi.StatusRunning,
				Terminal:       false,
				Classification: preflightapi.ClassificationPending,
				AgentAction:    preflightapi.AgentActionWait,
				Summary:        "Run is still in progress.",
			},
			{
				RunID:          "run_failed",
				Status:         preflightapi.StatusFailed,
				Terminal:       true,
				ExitCode:       intPtr(1),
				Classification: preflightapi.ClassificationTestFailed,
				AgentAction:    preflightapi.AgentActionFixCode,
				Summary:        "Tests failed.",
			},
		},
	})
	if report.RunID != "run_failed" || report.Status != preflightapi.StatusFailed || !report.Terminal || report.ExitCode == nil || *report.ExitCode != 1 {
		t.Fatalf("summary = %+v, want failed terminal run selected over pending run", report)
	}
	if code := agentReportProcessExitCode(report); code != 1 {
		t.Fatalf("process exit code = %d, want 1", code)
	}
}

func TestAgentSuccessSummaryForGroupedRuns(t *testing.T) {
	t.Run("uses single child check when top-level check is empty", func(t *testing.T) {
		report := summarizeAgentVerifyRuns(agentVerifyReport{
			Mode: "affected_rules",
			Runs: []agentVerifyRunReport{
				{RunID: "run_tools", Check: "tools", Status: preflightapi.StatusSucceeded, Terminal: true, ExitCode: intPtr(0), Classification: preflightapi.ClassificationSuccess},
			},
		})
		if report.Summary != "Ready CI check tools passed." {
			t.Fatalf("summary = %q, want child check success summary", report.Summary)
		}
	})

	t.Run("uses grouped wording for multiple child checks", func(t *testing.T) {
		report := summarizeAgentVerifyRuns(agentVerifyReport{
			Mode: "affected_rules",
			Runs: []agentVerifyRunReport{
				{RunID: "run_cli", Check: "cli", Status: preflightapi.StatusSucceeded, Terminal: true, ExitCode: intPtr(0), Classification: preflightapi.ClassificationSuccess},
				{RunID: "run_web", Check: "web", Status: preflightapi.StatusSucceeded, Terminal: true, ExitCode: intPtr(0), Classification: preflightapi.ClassificationSuccess},
			},
		})
		if report.Summary != "Ready CI verification passed for 2 checks." {
			t.Fatalf("summary = %q, want grouped success summary", report.Summary)
		}
	})
}
