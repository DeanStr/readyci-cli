package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestAgentExplainRequestsWarningSummaryUsesObservedTestFailure(t *testing.T) {
	const warning = "/workspace/src/requests/auth.py:45: DeprecationWarning: Non-string usernames will no longer be supported in Requests 3.0.0. Please convert the object you've passed in (None) to a string or bytes object in the near future to avoid problems."
	exitCode := 2
	detail := preflightapi.RunDetail{
		Summary: preflightapi.RunSummary{RunID: "run_requests", Status: "failed", ExitCode: &exitCode, Classification: "test_failed", AgentAction: "fix_code", FailureSummary: warning, PrimaryFile: "/workspace/src/requests/auth.py"},
		Spec:    preflightapi.RunSpec{RunID: "run_requests", Command: "pytest", CommandAlias: "test", NetworkMode: "allowlist"},
	}
	events := []preflightapi.RunEvent{
		{Type: preflightapi.EventStdout, Phase: "command", Data: "E           requests.exceptions.ConnectionError: Failed to establish a new connection: [Errno 111] Connection refused\n"},
		{Type: preflightapi.EventStdout, Phase: "command", Data: warning + "\nFAILED tests/test_requests.py::TestTimeout::test_connect_timeout[timeout0] - ...\n"},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/runs/run_requests":
			writeCLITestJSON(w, http.StatusOK, detail)
		case "/v1/runs/run_requests/events":
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunEventsResponse{RunID: "run_requests", Events: events})
		case "/v1/runs/run_requests/artifacts":
			writeCLITestJSON(w, http.StatusOK, preflightapi.ListArtifactsResponse{RunID: "run_requests"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	report, err := buildAgentExplainReport(context.Background(), api.NewClient(server.URL), server.URL, "run_requests")
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "failed" || report.ExitCode == nil || *report.ExitCode != exitCode || report.Diagnosis.Kind != "test_failed" || report.Diagnosis.Action != "fix_code" || report.Diagnosis.Retryable || report.Repair.Classification != "test_failed" || report.Repair.AgentAction != "fix_code" {
		t.Fatalf("test evidence refinement changed the authoritative outcome: %+v", report)
	}
	if !strings.Contains(report.Diagnosis.Summary, "FAILED tests/test_requests.py::TestTimeout::test_connect_timeout[timeout0]") || strings.Contains(report.Diagnosis.Summary, "DeprecationWarning") || strings.Contains(report.Diagnosis.Summary, "ConnectionError") || report.Repair.Summary != report.Diagnosis.Summary {
		t.Fatalf("summary did not prefer the failed test, or attached an earlier event's exception: diagnosis=%+v repair=%+v", report.Diagnosis, report.Repair)
	}
	for _, target := range report.Repair.TargetFiles {
		if target.Path == detail.Summary.PrimaryFile {
			t.Fatal("incidental warning was proposed as a repair target")
		}
	}
	for _, item := range report.Evidence {
		if strings.Contains(item.Text, "requests.exceptions.ConnectionError") {
			return
		}
	}
	t.Fatal("observed exception disappeared from diagnostic evidence")
}
