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

const evidenceBoundaryWarning = "/workspace/src/requests/auth.py:45: DeprecationWarning: Non-string usernames will no longer be supported."

func explainEvidenceBoundaryRun(t *testing.T, events []preflightapi.RunEvent) agentExplainReport {
	t.Helper()
	exitCode := 2
	detail := preflightapi.RunDetail{
		Summary: preflightapi.RunSummary{
			RunID: "run_evidence_boundaries", Status: "failed", ExitCode: &exitCode,
			Classification: "test_failed", AgentAction: "fix_code", FailureSummary: evidenceBoundaryWarning,
			PrimaryFile: "/workspace/src/requests/auth.py",
		},
		Spec: preflightapi.RunSpec{RunID: "run_evidence_boundaries", Command: "python -m pytest tests", CommandAlias: "test", NetworkMode: "allowlist"},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/runs/run_evidence_boundaries":
			writeCLITestJSON(w, http.StatusOK, detail)
		case "/v1/runs/run_evidence_boundaries/events":
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunEventsResponse{RunID: "run_evidence_boundaries", Events: events})
		case "/v1/runs/run_evidence_boundaries/artifacts":
			writeCLITestJSON(w, http.StatusOK, preflightapi.ListArtifactsResponse{RunID: "run_evidence_boundaries"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	report, err := buildAgentExplainReport(context.Background(), api.NewClient(server.URL), server.URL, "run_evidence_boundaries")
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "failed" || !report.Terminal || report.ExitCode == nil || *report.ExitCode != exitCode || report.Diagnosis.Kind != "test_failed" || report.Diagnosis.Action != "fix_code" || report.Diagnosis.Retryable || report.Repair.Classification != "test_failed" || report.Repair.AgentAction != "fix_code" {
		t.Fatalf("diagnostic evidence changed the authoritative outcome: %+v", report)
	}
	return report
}

func TestAgentExplainDoesNotRefineRepositoryFailureFromSidecar(t *testing.T) {
	report := explainEvidenceBoundaryRun(t, []preflightapi.RunEvent{
		{ID: 1, Type: preflightapi.EventStdout, Phase: "command", Data: evidenceBoundaryWarning + "\n"},
		{ID: 2, Type: preflightapi.EventStdout, Phase: "command", Service: "database", Data: "FAILED tests/test_sidecar.py::test_health\n"},
	})
	if report.Diagnosis.Summary != evidenceBoundaryWarning || report.Repair.Summary != evidenceBoundaryWarning || report.Diagnosis.PrimaryFile != "/workspace/src/requests/auth.py" || report.Repair.PrimaryFile != "/workspace/src/requests/auth.py" {
		t.Fatalf("sidecar output replaced the repository's retained failure: diagnosis=%+v repair=%+v", report.Diagnosis, report.Repair)
	}
}

func TestAgentExplainRetainsFailureEvidenceAfterWarningTail(t *testing.T) {
	const exception = "E requests.exceptions.ConnectionError: Connection refused"
	const failed = "FAILED tests/test_requests.py::TestTimeout::test_connect_timeout[timeout0]"
	logs := strings.Repeat(evidenceBoundaryWarning+"\n", 20) + exception + "\n" + failed + "\n"
	report := explainEvidenceBoundaryRun(t, []preflightapi.RunEvent{{ID: 1, Type: preflightapi.EventStdout, Phase: "command", Step: "tests", Data: logs}})
	if !strings.HasPrefix(report.Diagnosis.Summary, failed) || report.Diagnosis.PrimaryFile != "tests/test_requests.py" {
		t.Fatalf("summary did not select the observed failed test: %+v", report.Diagnosis)
	}
	for name, evidence := range map[string][]agentEvidence{"explanation": report.Evidence, "repair": report.Repair.Evidence} {
		if len(evidence) > 8 {
			t.Fatalf("%s evidence exceeded its bound: %+v", name, evidence)
		}
		foundException, foundFailed := false, false
		for _, item := range evidence {
			foundException = foundException || item.Text == exception
			foundFailed = foundFailed || item.Text == failed
		}
		if !foundException || !foundFailed {
			t.Fatalf("%s evidence lost the exception or named failure after warnings: %+v", name, evidence)
		}
	}
}
