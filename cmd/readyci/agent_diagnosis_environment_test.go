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

func TestAgentExplainPreservesHostedEnvironmentFailure(t *testing.T) {
	exitCode := 78
	detail := preflightapi.RunDetail{
		Summary: preflightapi.RunSummary{
			RunID: "run_environment", Status: preflightapi.StatusFailed, ExitCode: &exitCode,
			Command: "xcodebuild test", Classification: preflightapi.ClassificationEnvironmentSetupFailed,
			AgentAction: preflightapi.AgentActionReportSetup, FailureSummary: "The check requires a macOS/Xcode runner.",
		},
		Spec: preflightapi.RunSpec{RunID: "run_environment", Command: "xcodebuild test", CommandAlias: "manual-tests"},
	}
	events := []preflightapi.RunEvent{{
		Type: preflightapi.EventStderr, Phase: "prepare", Data: "manual-tests requires a macOS/Xcode runner; ubuntu-24.04-ci cannot run this XCTest plan\n",
	}, {
		Type: preflightapi.EventRunFailed, Status: preflightapi.StatusFailed, ExitCode: &exitCode,
	}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/runs/run_environment":
			writeCLITestJSON(w, http.StatusOK, detail)
		case "/v1/runs/run_environment/events":
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunEventsResponse{RunID: "run_environment", Events: events})
		case "/v1/runs/run_environment/artifacts":
			writeCLITestJSON(w, http.StatusOK, preflightapi.ListArtifactsResponse{RunID: "run_environment"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	report, err := buildAgentExplainReport(context.Background(), api.NewClient(server.URL), server.URL, "run_environment")
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != preflightapi.StatusFailed || report.ExitCode == nil || *report.ExitCode != 78 {
		t.Fatalf("terminal outcome changed: %+v", report)
	}
	if report.Diagnosis.Kind != preflightapi.ClassificationEnvironmentSetupFailed || report.Repair.AgentAction != preflightapi.AgentActionReportSetup || report.Repair.RerunCommand != "" || len(report.Repair.TargetFiles) != 0 {
		t.Fatalf("environment repair=%+v diagnosis=%+v", report.Repair, report.Diagnosis)
	}
	if strings.Contains(report.Repair.RootCause, "at least one test failed") || !strings.Contains(report.Repair.RootCause, "prerequisite") {
		t.Fatalf("root cause = %q, want prerequisite category", report.Repair.RootCause)
	}
	if hint := runFailureDiagnosisHint(report.Diagnosis.Kind); !strings.Contains(hint, "filesystem") {
		t.Fatalf("hint = %q", hint)
	}
}

func TestTestFailureCategoryDoesNotInventAssertionEvidence(t *testing.T) {
	rootCause := rootCauseForClassification(preflightapi.ClassificationTestFailed)
	if strings.Contains(rootCause, "at least one test failed") || !strings.Contains(rootCause, "evidence") {
		t.Fatalf("root cause = %q, want evidence-qualified category", rootCause)
	}
	packet := buildAgentRepairPacket(agentDiagnosis{Kind: preflightapi.ClassificationEnvironmentSetupFailed}, "go test ./...", "", "", []agentEvidence{{Path: "tsdb/fileutil/direct_io_writer_test.go"}}, nil, nil)
	if len(packet.TargetFiles) != 0 {
		t.Fatalf("environment prerequisite failure nominated test source for editing: %+v", packet.TargetFiles)
	}
}

func TestLocalRuntimePreparationFailureDoesNotSuggestCodeRepair(t *testing.T) {
	code := 78
	events := []preflightapi.RunEvent{{Type: preflightapi.EventStderr, Phase: "prepare", Data: preflightapi.RuntimePreparationFailureMarker + "\n"}}
	analysis := localAnalyzeRunFailure(preflightapi.StatusFailed, "go test ./...", &code, events, "")
	if analysis.Classification != preflightapi.ClassificationEnvironmentSetupFailed || analysis.AgentAction != preflightapi.AgentActionReportSetup || analysis.PrimaryFile != "" {
		t.Fatalf("runtime failure suggested a code repair: %+v", analysis)
	}
	if got := localAnalyzeRunFailure(preflightapi.StatusSucceeded, "go test ./...", &code, events, ""); got.Classification != preflightapi.ClassificationSuccess {
		t.Fatalf("diagnostic changed terminal success: %+v", got)
	}
}
