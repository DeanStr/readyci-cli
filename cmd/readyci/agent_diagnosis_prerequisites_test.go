package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

const kubernetesClusterPrerequisiteOutput = "unable to load in-cluster configuration, KUBERNETES_SERVICE_HOST and KUBERNETES_SERVICE_PORT must be defined"
const kubernetesServerPrerequisiteOutput = "KUBERNETES_SERVER_BIN_DIR must be set"

func TestKubernetesPrerequisiteAdvisoriesRequireFailedCommandOutput(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		event        preflightapi.RunEvent
		want         int
	}{
		{"cluster stdout", "failed", preflightapi.RunEvent{Type: "stdout", Phase: "command", Data: kubernetesClusterPrerequisiteOutput}, 1},
		{"binary stderr", "failed", preflightapi.RunEvent{Type: "stderr", Phase: "command", Data: kubernetesServerPrerequisiteOutput}, 1},
		{"binary encoded", "failed", preflightapi.RunEvent{Type: "stderr", Phase: "command", DataBase64: base64.StdEncoding.EncodeToString([]byte(kubernetesServerPrerequisiteOutput))}, 1},
		{"success", "succeeded", preflightapi.RunEvent{Type: "stdout", Phase: "command", Data: kubernetesClusterPrerequisiteOutput}, 0},
		{"pending", "running", preflightapi.RunEvent{Type: "stdout", Phase: "command", Data: kubernetesClusterPrerequisiteOutput}, 0},
		{"cancelled", "cancelled", preflightapi.RunEvent{Type: "stdout", Phase: "command", Data: kubernetesClusterPrerequisiteOutput}, 0},
		{"preparation", "failed", preflightapi.RunEvent{Type: "stdout", Phase: "prepare", Data: kubernetesClusterPrerequisiteOutput}, 0},
		{"phase unavailable", "failed", preflightapi.RunEvent{Type: "stdout", Data: kubernetesClusterPrerequisiteOutput}, 0},
		{"lifecycle data", "failed", preflightapi.RunEvent{Type: preflightapi.EventRunFailed, Phase: "command", Data: kubernetesClusterPrerequisiteOutput}, 0},
		{"message only", "failed", preflightapi.RunEvent{Type: "stdout", Phase: "command", Message: kubernetesClusterPrerequisiteOutput}, 0},
		{"service", "failed", preflightapi.RunEvent{Type: "stdout", Phase: "command", Service: "database", Data: kubernetesClusterPrerequisiteOutput}, 0},
		{"malformed encoding", "failed", preflightapi.RunEvent{Type: "stdout", Phase: "command", Data: kubernetesClusterPrerequisiteOutput, DataBase64: "!invalid"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := kubernetesPrerequisiteAdvisories(tc.status, []preflightapi.RunEvent{tc.event})
			if len(got) != tc.want {
				t.Fatalf("advisories = %+v, want %d", got, tc.want)
			}
		})
	}
}

func TestKubernetesPrerequisiteAdvisoriesPreserveChunkSourcesAndBounds(t *testing.T) {
	for _, marker := range []string{kubernetesClusterPrerequisiteOutput, kubernetesServerPrerequisiteOutput} {
		for split := 1; split < len(marker); split++ {
			events := []preflightapi.RunEvent{
				{Type: "stdout", Phase: "command", Step: "tests", Data: marker[:split]},
				{Type: "stdout", Phase: "command", Step: "tests", DataBase64: base64.StdEncoding.EncodeToString([]byte(marker[split:]))},
			}
			if got := kubernetesPrerequisiteAdvisories("failed", events); len(got) != 1 {
				t.Fatalf("split %d: advisories = %+v, want observed prerequisite", split, got)
			}
		}
	}
	marker := kubernetesClusterPrerequisiteOutput
	split := len(marker) / 2
	first := preflightapi.RunEvent{Type: "stdout", Phase: "command", Step: "tests", Data: marker[:split]}
	for name, events := range map[string][]preflightapi.RunEvent{
		"different stream":       {first, {Type: "stderr", Phase: "command", Step: "tests", Data: marker[split:]}},
		"different phase":        {first, {Type: "stdout", Phase: "prepare", Step: "tests", Data: marker[split:]}},
		"different step":         {first, {Type: "stdout", Phase: "command", Step: "other", Data: marker[split:]}},
		"malformed interruption": {first, {Type: "stdout", Phase: "command", Step: "tests", DataBase64: "!invalid"}, {Type: "stdout", Phase: "command", Step: "tests", Data: marker[split:]}},
		"byte limit":             {{Type: "stdout", Phase: "command", Data: strings.Repeat("x", sigstoreAdvisoryLogLimit)}, {Type: "stdout", Phase: "command", Data: marker}},
		"partial at byte limit":  {{Type: "stdout", Phase: "command", Data: strings.Repeat("x", sigstoreAdvisoryLogLimit-len(marker)+1) + marker}},
	} {
		t.Run(name, func(t *testing.T) {
			if got := kubernetesPrerequisiteAdvisories("failed", events); len(got) != 0 {
				t.Fatalf("unrelated or out-of-bound output created advisory: %+v", got)
			}
		})
	}
	for _, malformed := range []bool{false, true} {
		var events []preflightapi.RunEvent
		for index := 0; index < 16; index++ {
			event := preflightapi.RunEvent{Type: "stdout", Phase: "command", Step: fmt.Sprintf("step-%d", index), Data: "no prerequisite failure"}
			if malformed {
				event.DataBase64 = "!invalid"
			}
			events = append(events, event)
		}
		events = append(events, preflightapi.RunEvent{Type: "stdout", Phase: "command", Step: "step-17", Data: marker})
		if got := kubernetesPrerequisiteAdvisories("failed", events); len(got) != 0 {
			t.Fatalf("advisory exceeded source bound (malformed=%t): %+v", malformed, got)
		}
	}
}

func TestAgentExplainKubernetesPrerequisiteAdvisoriesPreserveAuthoritativeFailure(t *testing.T) {
	const output = "[FAILED] Error loading client: error creating client: unable to load in-cluster configuration, KUBERNETES_SERVICE_HOST and KUBERNETES_SERVICE_PORT must be defined\n" +
		"--- FAIL: TestFeatureGateCycle (0.00s)\n    featuregatecycle_test.go:58: FATAL ERROR:\n        KUBERNETES_SERVER_BIN_DIR must be set\n"
	for _, tc := range []struct {
		name, status, classification, action, summary string
		exitCode                                      int
	}{
		{"test failure", "failed", "test_failed", "fix_code", "Kubernetes tests failed.", 1},
		{"deadline", "timed_out", "timeout", "reduce_scope", "The run timed out.", 124},
		{"cleanup error", "failed", "worker_internal_error", "wait_or_report_infra", "firecracker shutdown failed: remove firecracker cgroup: device or resource busy", 124},
	} {
		t.Run(tc.name, func(t *testing.T) {
			detail := preflightapi.RunDetail{
				Summary: preflightapi.RunSummary{RunID: "run_kubernetes", Status: tc.status, ExitCode: &tc.exitCode, Classification: tc.classification, AgentAction: tc.action, FailureSummary: tc.summary, Retryable: tc.classification == "worker_internal_error"},
				Spec:    preflightapi.RunSpec{RunID: "run_kubernetes", Command: "go test ./...", CommandAlias: "go", NetworkMode: "allowlist"},
			}
			events := []preflightapi.RunEvent{{Type: preflightapi.EventStdout, Phase: "command", Step: "tests", Data: output}}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/runs/run_kubernetes":
					writeCLITestJSON(w, http.StatusOK, detail)
				case "/v1/runs/run_kubernetes/events":
					writeCLITestJSON(w, http.StatusOK, preflightapi.RunEventsResponse{RunID: "run_kubernetes", Events: events})
				case "/v1/runs/run_kubernetes/artifacts":
					writeCLITestJSON(w, http.StatusOK, preflightapi.ListArtifactsResponse{RunID: "run_kubernetes"})
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			report, err := buildAgentExplainReport(context.Background(), api.NewClient(server.URL), server.URL, "run_kubernetes")
			if err != nil {
				t.Fatal(err)
			}
			if report.Status != tc.status || report.ExitCode == nil || *report.ExitCode != tc.exitCode || report.Diagnosis.Kind != tc.classification || report.Diagnosis.Action != tc.action || report.Diagnosis.Summary != tc.summary || report.Diagnosis.Retryable != detail.Summary.Retryable || report.Repair.Classification != tc.classification || report.Repair.AgentAction != tc.action {
				t.Fatalf("prerequisite advisory changed the authoritative outcome: %+v", report)
			}
			if len(report.Warnings) != 2 {
				t.Fatalf("warnings = %+v, want separate cluster configuration and server binary guidance", report.Warnings)
			}
			for _, warning := range report.Warnings {
				if warning.Section != "test_prerequisite" || !strings.Contains(warning.Message, "secondary guidance") || !strings.Contains(warning.Message, "terminal failure") || !strings.Contains(report.Repair.RootCause, warning.Message) {
					t.Fatalf("prerequisite guidance is missing or states an unproven terminal cause: %+v", report)
				}
			}
			if !strings.Contains(report.Warnings[0].Message, "cluster configuration") || !strings.Contains(report.Warnings[1].Message, "KUBERNETES_SERVER_BIN_DIR") {
				t.Fatalf("warnings do not describe the observed prerequisites: %+v", report.Warnings)
			}
		})
	}
}

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
