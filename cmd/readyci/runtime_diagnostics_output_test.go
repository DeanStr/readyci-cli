package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestAgentExplainIncludesRuntimeDiagnosticsWithoutInferringGuestOOM(t *testing.T) {
	one := 1
	detail := preflightapi.RunDetail{
		Summary: preflightapi.RunSummary{RunID: "run_crash", Status: preflightapi.StatusFailed, ExitCode: &one, Classification: preflightapi.ClassificationWorkerInternalError, FailureSummary: "agent protocol closed before command_exit: EOF"},
		Spec:    preflightapi.RunSpec{RunID: "run_crash", Command: "run-tests"},
	}
	diagnostic := `{"schema_version":"readyci.runtime_diagnostics.v1","instance_id":"instance-one","trigger":"unexpected_runtime_failure","captured_at":"2026-10-23T00:00:00Z","launcher":{"scope":"firecracker_vmm","exited":true,"observed_before_teardown":true,"exit_code":1},"logs":{"serial_stdout":{"data_base64":"S2VybmVsIGRpYWdub3N0aWNzCg==","observed_bytes":19,"retained_bytes":19,"truncated":false}},"vm_resources":{"basis":"host_vm_cgroup_v2","oom_kills":0},"limitations":["Host counters do not prove guest OOM."]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/runs/run_crash":
			writeCLITestJSON(w, http.StatusOK, detail)
		case "/v1/runs/run_crash/events":
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunEventsResponse{Events: []preflightapi.RunEvent{{Type: preflightapi.EventRuntimeDiagnostics, Data: diagnostic}}})
		case "/v1/runs/run_crash/artifacts":
			writeCLITestJSON(w, http.StatusOK, preflightapi.ListArtifactsResponse{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	report, err := buildAgentExplainReport(context.Background(), api.NewClient(server.URL), server.URL, detail.Summary.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if report.Diagnosis.Kind != preflightapi.ClassificationWorkerInternalError || !strings.Contains(report.Diagnosis.Summary, "EOF") {
		t.Fatalf("runtime diagnostics changed authoritative failure: %+v", report.Diagnosis)
	}
	if !strings.Contains(report.RecommendedNextStep, "Inspect runtime_diagnostics") {
		t.Fatalf("runtime evidence is not actionable: %q", report.RecommendedNextStep)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var output map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output["runtime_diagnostics"]), "instance-one") {
		t.Fatalf("runtime diagnostic snapshot missing: %s", encoded)
	}
	validateAgentJSONSchema(t, encoded)
	text, err := captureStdoutForCLITest(t, func() error { printAgentExplainReport(report); return nil })
	if err != nil || !strings.Contains(text, "runtime_diagnostics: instance-one") || !strings.Contains(text, "serial_stdout") {
		t.Fatalf("human runtime diagnostic availability missing: %s, %v", text, err)
	}
}
