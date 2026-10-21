package main

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestSigstorePrerequisiteAdvisoryRequiresRestrictedFailedTestOutput(t *testing.T) {
	output := preflightapi.RunEvent{Type: preflightapi.EventStdout, Data: "Error: " + opaqueSigstoreInitializationFailure + "\n"}
	for _, tc := range []struct {
		name, status, classification, network string
		events                                []preflightapi.RunEvent
		want                                  bool
	}{
		{"allowlist", "failed", "test_failed", "allowlist", []preflightapi.RunEvent{output}, true},
		{"none", "failed", "test_failed", "none", []preflightapi.RunEvent{output}, true},
		{"default network", "failed", "test_failed", "default", []preflightapi.RunEvent{output}, false},
		{"unknown network", "failed", "test_failed", "", []preflightapi.RunEvent{output}, false},
		{"success", "succeeded", "success", "none", []preflightapi.RunEvent{output}, false},
		{"timeout", "timed_out", "timeout", "none", []preflightapi.RunEvent{output}, false},
		{"other classification", "failed", "build_failed", "none", []preflightapi.RunEvent{output}, false},
		{"lifecycle data", "failed", "test_failed", "none", []preflightapi.RunEvent{{Type: preflightapi.EventRunFailed, Data: output.Data, Message: output.Data}}, false},
		{"message only", "failed", "test_failed", "none", []preflightapi.RunEvent{{Type: preflightapi.EventStdout, Message: output.Data}}, false},
		{"service output", "failed", "test_failed", "none", []preflightapi.RunEvent{{Type: preflightapi.EventStderr, Service: "database", Data: output.Data}}, false},
		{"unrelated failure", "failed", "test_failed", "none", []preflightapi.RunEvent{{Type: preflightapi.EventStderr, Data: "expected 2, received 1"}}, false},
		{"malformed encoding", "failed", "test_failed", "none", []preflightapi.RunEvent{{Type: preflightapi.EventStdout, Data: output.Data, DataBase64: "not base64"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			advisory := sigstorePrerequisiteAdvisory(tc.status, tc.classification, tc.network, tc.events)
			if (advisory != "") != tc.want {
				t.Fatalf("advisory=%q want presence=%t", advisory, tc.want)
			}
			if tc.want && (!strings.Contains(advisory, "does not establish the cause") || strings.Contains(advisory, "https://") || len(advisory) > 240) {
				t.Fatalf("advisory asserted a cause, inferred a host, or exceeded its bound: %q", advisory)
			}
		})
	}
}

func TestSigstorePrerequisiteAdvisoryPreservesChunkSourcesAndBound(t *testing.T) {
	first, second := opaqueSigstoreInitializationFailure[:20], opaqueSigstoreInitializationFailure[20:]
	events := []preflightapi.RunEvent{{Type: "stdout", Step: "tests", Data: first}, {Type: "stdout", Step: "tests", DataBase64: base64.StdEncoding.EncodeToString([]byte(second))}}
	if !hasOpaqueSigstoreInitializationFailure(events) {
		t.Fatal("chunked command output did not retain the initialization diagnostic")
	}
	for _, other := range []preflightapi.RunEvent{{Type: "stderr", Step: "tests", Data: second}, {Type: "stdout", Step: "other", Data: second}, {Type: "stdout", Step: "tests", Phase: "prepare", Data: second}} {
		if hasOpaqueSigstoreInitializationFailure([]preflightapi.RunEvent{events[0], other}) {
			t.Fatal("diagnostic was invented across distinct output sources")
		}
	}
	if hasOpaqueSigstoreInitializationFailure([]preflightapi.RunEvent{{Type: "stdout", Data: strings.Repeat("x", sigstoreAdvisoryLogLimit)}, {Type: "stdout", Data: opaqueSigstoreInitializationFailure}}) {
		t.Fatal("advisory exceeded the bounded output inspection limit")
	}
}

func TestAgentExplainSigstoreAdvisoryPreservesAuthoritativeFailure(t *testing.T) {
	exitCode := 1
	detail := preflightapi.RunDetail{
		Summary: preflightapi.RunSummary{RunID: "run_sigstore", Status: "failed", ExitCode: &exitCode, Classification: "test_failed", AgentAction: "fix_code", FailureSummary: "Tests failed."},
		Spec:    preflightapi.RunSpec{RunID: "run_sigstore", Command: "go test -race -tags=integration ./...", CommandAlias: "build", NetworkMode: "allowlist"},
	}
	events := []preflightapi.RunEvent{{Type: "stdout", Data: "--- FAIL: TestLiveSigstoreVerifier\nError: " + opaqueSigstoreInitializationFailure + "\n"}, {Type: preflightapi.EventRunFailed, Status: "failed", ExitCode: &exitCode}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/runs/run_sigstore":
			writeCLITestJSON(w, http.StatusOK, detail)
		case "/v1/runs/run_sigstore/events":
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunEventsResponse{RunID: "run_sigstore", Events: events})
		case "/v1/runs/run_sigstore/artifacts":
			writeCLITestJSON(w, http.StatusOK, preflightapi.ListArtifactsResponse{RunID: "run_sigstore"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	report, err := buildAgentExplainReport(context.Background(), api.NewClient(server.URL), server.URL, "run_sigstore")
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "failed" || report.ExitCode == nil || *report.ExitCode != 1 || report.Diagnosis.Kind != "test_failed" || report.Repair.Classification != "test_failed" || report.Repair.AgentAction != "fix_code" || report.Diagnosis.Summary != "Tests failed." {
		t.Fatalf("advisory changed the authoritative outcome: %+v", report)
	}
	if len(report.Warnings) != 1 || report.Warnings[0].Section != "test_prerequisite" || !strings.Contains(report.Repair.RootCause, report.Warnings[0].Message) || len(report.Repair.Evidence) == 0 {
		t.Fatalf("bounded advisory or original evidence missing: repair=%+v warnings=%+v", report.Repair, report.Warnings)
	}
}
