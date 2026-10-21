package main

import (
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestRunLogsSummaryRefinesIncidentalWarningWithoutChangingOutcome(t *testing.T) {
	exitCode := 2
	stored := "/workspace/src/requests/auth.py:45: DeprecationWarning: Non-string usernames will no longer be supported."
	detail := preflightapi.RunDetail{
		Summary: preflightapi.RunSummary{RunID: "run_pytest", Status: "failed", ExitCode: &exitCode, Classification: "test_failed", AgentAction: "fix_code", FailureSummary: stored},
		Spec:    preflightapi.RunSpec{Command: "python -m pytest tests"},
	}
	events := []preflightapi.RunEvent{{Type: "stdout", Data: stored + "\nE requests.exceptions.ConnectionError: Connection refused\nFAILED tests/test_requests.py::TestTimeout::test_connect_timeout[timeout0]\n"}}
	report := buildRunLogsFailureSummaryReport(detail, events, false)
	if report.Classification != "test_failed" || report.AgentAction != "fix_code" || report.Status != "failed" || report.Retryable {
		t.Fatalf("native outcome changed: %+v", report)
	}
	if !strings.Contains(report.FailureSummary, "FAILED tests/test_requests.py::TestTimeout") || !strings.Contains(report.FailureSummary, "ConnectionError") || strings.Contains(report.FailureSummary, "DeprecationWarning") {
		t.Fatalf("diagnostic summary = %q", report.FailureSummary)
	}
	if detail.Summary.FailureSummary != stored {
		t.Fatal("retained server summary was mutated")
	}
}

func TestLocalPytestSummaryRetainsLaterFailureAfterTraceback(t *testing.T) {
	exitCode := 2
	logs := strings.Repeat("/workspace/src/requests/auth.py:45: DeprecationWarning: Non-string usernames are deprecated.\n", 20) +
		"E requests.exceptions.ConnectionError: Connection refused\nFAILED tests/test_requests.py::TestTimeout::test_connect_timeout[timeout0]\n"
	analysis := localAnalyzeRunFailure("failed", "python -m pytest tests", &exitCode, []preflightapi.RunEvent{{Type: "stdout", Data: logs}}, "")
	if !strings.Contains(analysis.Summary, "FAILED tests/test_requests.py::TestTimeout") || !strings.Contains(analysis.Summary, "ConnectionError") || analysis.PrimaryFile != "tests/test_requests.py" {
		t.Fatalf("later actual failure lost: %+v", analysis)
	}
	if len(analysis.Evidence) > 12 || len(analysis.Summary) > 240 || len(strings.Split(analysis.Summary, "\n")) > 2 {
		t.Fatalf("unbounded diagnostic: %+v", analysis)
	}
}

func TestLocalPytestSummaryRetainsFailureBeforeTrailingWarnings(t *testing.T) {
	exitCode := 1
	logs := "FAILED tests/test_failure.py::test_case - AssertionError: expected 2, received 1\n" +
		strings.Repeat("src/legacy.py:12: DeprecationWarning: deprecated API\n", 20)
	analysis := localAnalyzeRunFailure("failed", "pytest tests", &exitCode, []preflightapi.RunEvent{{Type: "stdout", Data: logs}}, "")
	if !strings.Contains(analysis.Summary, "FAILED tests/test_failure.py::test_case") || analysis.PrimaryFile != "tests/test_failure.py" || len(analysis.Evidence) > 12 {
		t.Fatalf("actual failure lost to trailing warning noise: %+v", analysis)
	}
}

func TestLocalBrowserDownloadPreservesAuthoritativeFailurePrecedence(t *testing.T) {
	exitCode := 1
	events := []preflightapi.RunEvent{
		{Type: "stderr", Phase: "command", Data: "Error: getaddrinfo ENOTFOUND cdn.playwright.dev\n"},
		{Type: "stdout", Phase: "command", Data: "Error: Failed to download Chrome for Testing 151.0.7922.34 (playwright chromium v1234), caused by\n"},
	}
	for _, tc := range []struct{ name, mode, reason, want string }{
		{"allowlist is not proven denial", "allowlist", "", "dependency_download_failed"},
		{"required network disabled", "none", "", "network_not_allowed"},
		{"containment wins", "allowlist", "Firecracker shutdown failed: remove Firecracker cgroup: device or resource busy", "worker_internal_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			analysis := localAnalyzeRunFailureWithNetworkMode("failed", "pnpm exec playwright install chromium && pnpm test", &exitCode, events, tc.reason, tc.mode)
			if analysis.Classification != tc.want {
				t.Fatalf("analysis=%+v", analysis)
			}
		})
	}
	assertion := append(append([]preflightapi.RunEvent{}, events...), preflightapi.RunEvent{Type: "stdout", Data: "FAILED tests/test_download.py::test_fallback - AssertionError: expected fallback, received failure\n"})
	if analysis := localAnalyzeRunFailure("failed", "pytest", &exitCode, assertion, ""); analysis.Classification != "test_failed" {
		t.Fatalf("concrete assertion was replaced by download inference: %+v", analysis)
	}
}

func TestLocalBrowserDownloadDoesNotMaskLaterLintFailure(t *testing.T) {
	exitCode := 1
	for _, prior := range []preflightapi.RunEvent{
		{Type: "stderr", Phase: "prepare", Data: "Error: Failed to download Chromium (playwright chromium v1234)\n"},
		{Type: "stderr", Phase: "command", Step: "browser", Data: "Error: Failed to download Chromium (playwright chromium v1234)\n"},
	} {
		events := []preflightapi.RunEvent{
			prior,
			{Type: "stderr", Phase: "command", Step: "lint", Data: "src/app.js:3:1 error Unexpected console statement no-console\n"},
			{Type: preflightapi.EventCommandExited, ExitCode: &exitCode},
		}
		got := localAnalyzeRunFailureWithNetworkMode("failed", "npm run lint", &exitCode, events, "", "default")
		if got.Classification != "lint_failed" || got.Retryable || got.AgentAction != "fix_code" || !strings.Contains(got.Summary, "no-console") || got.PrimaryFile != "src/app.js" {
			t.Fatalf("recovered browser installation masked the failed lint command: %+v", got)
		}
	}
}

func TestLocalWarningAsErrorRemainsDiagnosticEvidence(t *testing.T) {
	exitCode := 1
	analysis := localAnalyzeRunFailure("failed", "pytest -W error", &exitCode, []preflightapi.RunEvent{{Type: "stdout", Data: "src/legacy.py:12: DeprecationWarning: deprecated API\n"}}, "")
	if analysis.Classification != "test_failed" || !strings.Contains(analysis.Summary, "DeprecationWarning") {
		t.Fatalf("warning-as-error evidence lost: %+v", analysis)
	}
}

func TestDependencyDownloadGuidanceReviewsPrerequisitesWithoutEditingSource(t *testing.T) {
	diagnosis := agentDiagnosis{Kind: "dependency_download_failed", Action: "retry"}
	packet := buildAgentRepairPacket(diagnosis, "pnpm test", "readyci verify --check test", "readyci runs logs run_fixture", []agentEvidence{{Path: "node_modules/playwright/lib/server/registry/index.js"}}, nil, nil)
	if len(packet.TargetFiles) != 0 || !strings.Contains(packet.RootCause, "does not prove policy denial") {
		t.Fatalf("download repair suggests editing a tool or asserts policy denial: %+v", packet)
	}
	next := recommendedNextStep(diagnosis, "", "test", false)
	if !strings.Contains(next, "explicit network requirements") || !strings.Contains(next, "same check") || strings.Contains(next, "reduce the check scope") {
		t.Fatalf("next step=%q", next)
	}
}
