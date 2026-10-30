package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

type prerequisiteEvidenceFixture struct {
	name, command, summary, cause string
	events                        []preflightapi.RunEvent
	causeID, causeLine            int64
	capacity                      string
	capacityID, capacityLine      int64
}

func prerequisiteEvidenceFixtures() []prerequisiteEvidenceFixture {
	code := 1
	const gobin = "go: cannot install cross-compiled binaries when GOBIN is set"
	const storage = `e"mkdir /tmp/readyci-release-storage-control: no space left on device"`
	const capacity = "/dev/vdc 33501757440 33484980224 0 100% /tmp"
	return []prerequisiteEvidenceFixture{
		{name: "gobin", command: "go install .", summary: gobin, cause: gobin, causeID: 22, causeLine: 1,
			events: []preflightapi.RunEvent{
				{ID: 21, RunID: "run-prerequisite", Type: preflightapi.EventStdout, Phase: "command", Step: "check", Data: "invoke-rc.d: policy-rc.d denied execution of start.\nBuild for dragonfly\n"},
				{ID: 22, RunID: "run-prerequisite", Type: preflightapi.EventStderr, Phase: "command", Step: "check", Data: gobin + "\n"},
				{ID: 23, RunID: "run-prerequisite", Type: preflightapi.EventCommandExited, ExitCode: &code},
			}},
		{name: "storage", command: "go test ./...", summary: "Observed mkdir /tmp/readyci-release-storage-control: no space left on device; /tmp reported zero available bytes. Inspect filesystem capacity and temporary-file placement.", cause: storage, causeID: 11, causeLine: 6, capacity: capacity, capacityID: 11, capacityLine: 8,
			events: []preflightapi.RunEvent{
				{ID: 11, RunID: "run-prerequisite", Type: preflightapi.EventStdout, Phase: "command", Step: "check", Data: "--- FAIL: TestStorageAcceptanceControl (0.00s)\n    fixture_failure.go:1:\n        error:\n          got non-nil error\n        got:\n          " + storage + "\nFilesystem 1-blocks Used Available Capacity Mounted on\n" + capacity + "\n"},
				{ID: 12, RunID: "run-prerequisite", Type: preflightapi.EventStderr, Phase: "command", Step: "check", Message: preflightapi.CommandFailureScopeMessage, ExitCode: &code},
				{ID: 13, RunID: "run-prerequisite", Type: preflightapi.EventCommandExited, ExitCode: &code},
			}},
	}
}

func requirePrerequisiteEvidence(t *testing.T, fixture prerequisiteEvidenceFixture, evidence []agentEvidence) {
	t.Helper()
	if len(evidence) == 0 || evidence[0].Text != fixture.cause || evidence[0].EventID != fixture.causeID || int64(evidence[0].Line) != fixture.causeLine || evidence[0].Path != "" {
		t.Fatalf("primary prerequisite evidence lost its original location: %+v", evidence)
	}
	if fixture.capacity != "" && (len(evidence) < 2 || evidence[1].Text != fixture.capacity || evidence[1].EventID != fixture.capacityID || int64(evidence[1].Line) != fixture.capacityLine || evidence[1].Path != "") {
		t.Fatalf("supporting filesystem evidence lost its original location: %+v", evidence)
	}
}

func TestAgentExplainAndRunReportRetainPrerequisiteEvidence(t *testing.T) {
	for _, fixture := range prerequisiteEvidenceFixtures() {
		t.Run(fixture.name, func(t *testing.T) {
			code := 1
			detail := preflightapi.RunDetail{
				Summary: preflightapi.RunSummary{RunID: "run-prerequisite", Status: preflightapi.StatusFailed, ExitCode: &code, Classification: preflightapi.ClassificationEnvironmentSetupFailed, AgentAction: preflightapi.AgentActionReportSetup, FailureSummary: fixture.summary},
				Spec:    preflightapi.RunSpec{RunID: "run-prerequisite", ProjectID: "fixture", Command: fixture.command},
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/v1/runs/run-prerequisite":
					_ = json.NewEncoder(w).Encode(detail)
				case "/v1/runs/run-prerequisite/events":
					_ = json.NewEncoder(w).Encode(preflightapi.RunEventsResponse{RunID: "run-prerequisite", Events: fixture.events})
				case "/v1/runs/run-prerequisite/artifacts":
					_ = json.NewEncoder(w).Encode(preflightapi.ListArtifactsResponse{RunID: "run-prerequisite", Artifacts: []preflightapi.ArtifactSummary{}})
				default:
					t.Errorf("unexpected read %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client := api.NewClient(server.URL)
			report, err := buildAgentExplainReport(context.Background(), client, server.URL, "run-prerequisite")
			if err != nil {
				t.Fatal(err)
			}
			if report.Status != preflightapi.StatusFailed || report.ExitCode == nil || *report.ExitCode != 1 || report.Diagnosis.Kind != preflightapi.ClassificationEnvironmentSetupFailed || report.Diagnosis.Action != preflightapi.AgentActionReportSetup || report.Diagnosis.Summary != fixture.summary || report.Diagnosis.PrimaryFile != "" || report.Repair.PrimaryFile != "" || len(report.Repair.TargetFiles) != 0 {
				t.Fatalf("authoritative outcome or source attribution changed: %+v", report)
			}
			requirePrerequisiteEvidence(t, fixture, report.Evidence)
			if !reflect.DeepEqual(report.Evidence, report.Repair.Evidence) {
				t.Fatal("repair projection lost prerequisite evidence")
			}
			runReport, err := agentRunReportFromDetail(context.Background(), client, server.URL, detail)
			if err != nil {
				t.Fatal(err)
			}
			if runReport.ExitCode == nil || *runReport.ExitCode != 1 || runReport.Classification != preflightapi.ClassificationEnvironmentSetupFailed {
				t.Fatalf("run report changed native outcome: %+v", runReport)
			}
			requirePrerequisiteEvidence(t, fixture, runReport.Evidence)
		})
	}
}

func TestAgentLocalPrerequisiteEvidenceSurvivesProjection(t *testing.T) {
	for _, fixture := range prerequisiteEvidenceFixtures() {
		t.Run(fixture.name, func(t *testing.T) {
			local := diagnoseRun(preflightapi.StatusFailed, fixture.command, fixture.events, nil, "")
			requirePrerequisiteEvidence(t, fixture, evidenceForDiagnosis(local, fixture.events, 8))
			result := remoteRunResult{Status: preflightapi.StatusFailed, ExitCode: 1, Events: fixture.events}
			fallback := agentDiagnosisFromAnalysis(analyzeRemoteRunFailure(result, nil, fixture.command))
			requirePrerequisiteEvidence(t, fixture, evidenceForDiagnosis(fallback, fixture.events, 8))
		})
	}
}

func TestAgentEvidenceProjectionPreservesBoundsCopiesAndJSON(t *testing.T) {
	items := []preflightapi.RunFailureEvidence{{EventID: 71, Stream: "stderr", Seq: 42, Line: 3, Event: "stderr", Text: "first"}, {EventID: 72, Stream: "stdout", Seq: 43, Line: 9, Text: "second"}}
	analysis := preflightapi.RunFailureAnalysis{Classification: preflightapi.ClassificationEnvironmentSetupFailed, Evidence: items}
	diagnosis := agentDiagnosisFromAnalysis(analysis)
	items[0].Text = "changed after construction"
	for _, limit := range []int{-1, 0, 1, 2} {
		got := evidenceForDiagnosis(diagnosis, nil, limit)
		want := max(0, limit)
		if len(got) != want {
			t.Fatalf("limit %d returned %d items", limit, len(got))
		}
		if len(got) > 0 && (got[0].Text != "first" || got[0].EventID != 71 || got[0].Seq != 42 || got[0].Line != 3 || got[0].Stream != "stderr") {
			t.Fatalf("projection reordered or aliased evidence: %+v", got)
		}
	}
	encoded, err := json.Marshal(diagnosis)
	if err != nil {
		t.Fatal(err)
	}
	var public map[string]any
	if err := json.Unmarshal(encoded, &public); err != nil {
		t.Fatal(err)
	}
	if _, present := public["Evidence"]; present {
		t.Fatal("internal typed evidence entered the diagnosis JSON")
	}
	if _, present := public["evidence"]; present {
		t.Fatal("internal typed evidence changed the diagnosis JSON contract")
	}
	for _, kind := range []string{preflightapi.ClassificationSuccess, preflightapi.ClassificationPending} {
		diagnosis.Kind = kind
		if got := evidenceForDiagnosis(diagnosis, nil, 8); got != nil {
			t.Fatalf("%s projection exposed failure evidence: %+v", kind, got)
		}
	}
	fallback := evidenceForDiagnosis(agentDiagnosis{Kind: preflightapi.ClassificationBuildFailed}, []preflightapi.RunEvent{{Type: preflightapi.EventStderr, Data: "source.c:4: error: unknown type\n"}}, 8)
	if len(fallback) == 0 {
		t.Fatal("event-only callers lost their raw-log fallback")
	}
}

func TestDetailDiagnosisUsesOnlyRecordedExits(t *testing.T) {
	zero, one, twentyThree := 0, 1, 23
	for _, tc := range []struct {
		name    string
		summary *int
		events  []preflightapi.RunEvent
		want    *int
	}{
		{"unknown failed exit", nil, nil, nil},
		{"explicit summary zero", &zero, nil, &zero},
		{"explicit summary failure", &twentyThree, nil, &twentyThree},
		{"event zero precedes summary failure", &one, []preflightapi.RunEvent{{Type: preflightapi.EventCommandExited, ExitCode: &zero}}, &zero},
		{"event failure precedes summary zero", &zero, []preflightapi.RunEvent{{Type: preflightapi.EventCommandExited, ExitCode: &one}}, &one},
	} {
		t.Run(tc.name, func(t *testing.T) {
			detail := preflightapi.RunDetail{Summary: preflightapi.RunSummary{Status: preflightapi.StatusFailed, ExitCode: tc.summary}}
			result := remoteRunResultFromDetailDiagnosis(detail, preflightapi.StatusFailed, "", tc.events)
			got := runFailureExitCode(result, preflightapi.StatusFailed)
			if tc.want == nil {
				if result.ExitCode != 0 || got == nil || *got != 0 {
					t.Fatalf("unknown detail exit enabled a synthetic failure: result=%+v, observed=%v", result, got)
				}
			} else if !reflect.DeepEqual(got, tc.want) || result.ExitCode != *tc.want {
				t.Fatalf("observed=%v result=%+v, want %v", got, result, tc.want)
			}
		})
	}
	fixture := prerequisiteEvidenceFixtures()[0]
	withoutExit := fixture.events[:len(fixture.events)-1]
	for _, summaryExit := range []*int{nil, &zero} {
		detail := preflightapi.RunDetail{Summary: preflightapi.RunSummary{Status: preflightapi.StatusFailed, ExitCode: summaryExit}}
		result := remoteRunResultFromDetailDiagnosis(detail, preflightapi.StatusFailed, "", withoutExit)
		analysis := analyzeRemoteRunFailure(result, nil, fixture.command)
		if analysis.Classification == preflightapi.ClassificationEnvironmentSetupFailed {
			t.Fatal("unknown or zero native exit enabled exit-sensitive prerequisite diagnosis")
		}
	}
}

func TestHostedEvidenceCannotReplaceAuthoritativeOutcomes(t *testing.T) {
	fixture := prerequisiteEvidenceFixtures()[1]
	for _, tc := range []struct{ status, kind, reason string }{
		{preflightapi.StatusSucceeded, preflightapi.ClassificationSuccess, ""},
		{preflightapi.StatusTimedOut, preflightapi.ClassificationTimeout, ""},
		{preflightapi.StatusCancelled, preflightapi.ClassificationCancelled, ""},
		{preflightapi.StatusDenied, preflightapi.ClassificationPolicyDenied, ""},
		{preflightapi.StatusFailed, preflightapi.ClassificationWorkerInternalError, "agent protocol closed before command_exit: EOF"},
	} {
		t.Run(tc.status+"/"+tc.kind, func(t *testing.T) {
			result := remoteRunResult{Status: tc.status, ExitCode: 1, Classification: tc.kind, AgentAction: preflightapi.AgentActionStop, Retryable: true, FailureSummary: "trusted hosted summary", PrimaryFile: "trusted.txt", FailureReason: tc.reason, Events: fixture.events}
			analysis := analyzeRemoteRunFailure(result, nil, fixture.command)
			if analysis.Classification != tc.kind || analysis.Summary != result.FailureSummary || analysis.AgentAction != result.AgentAction || analysis.Retryable != result.Retryable || analysis.PrimaryFile != result.PrimaryFile || result.ExitCode != 1 {
				t.Fatalf("local evidence displaced hosted authority: %+v", analysis)
			}
		})
	}
	result := remoteRunResult{Status: preflightapi.StatusFailed, ExitCode: 1, Classification: preflightapi.ClassificationBuildFailed, FailureSummary: "hosted compiler failure", Events: fixture.events}
	if analysis := analyzeRemoteRunFailure(result, nil, fixture.command); len(analysis.Evidence) != 0 {
		t.Fatalf("mismatched local prerequisite evidence entered hosted compiler diagnosis: %+v", analysis.Evidence)
	}

}

func TestAgentEnvironmentPreparationEvidenceAfterWarningTail(t *testing.T) {
	code := 1
	const diagnostic = "Error: preparation command could not initialize tool state"
	warning := "fixture_module.py:3: DeprecationWarning: deprecated API"
	events := []preflightapi.RunEvent{
		{ID: 51, Type: preflightapi.EventStderr, Phase: "prepare", Step: "dependencies", Data: strings.Repeat(warning+"\n", 20) + diagnostic + "\n" + strings.Repeat(warning+"\n", 20)},
		{ID: 52, Type: preflightapi.EventStderr, Phase: "prepare", Step: "dependencies", Message: preflightapi.CommandFailureScopeMessage, ExitCode: &code},
		{ID: 53, Type: preflightapi.EventCommandExited, ExitCode: &code},
	}
	diagnosis := diagnoseRun(preflightapi.StatusFailed, "initialize-tool", events, nil, "")
	if diagnosis.Kind != preflightapi.ClassificationEnvironmentSetupFailed {
		t.Fatalf("diagnosis=%+v", diagnosis)
	}
	evidence := evidenceForDiagnosis(diagnosis, events, 8)
	if len(evidence) == 0 || evidence[0].Text != diagnostic {
		t.Fatalf("preparation diagnostic lost behind warnings: %+v", evidence)
	}
}

func TestAgentRecoveredSummaryZeroKeepsRecordedPrecedence(t *testing.T) {
	one, zero := 1, 0
	result := remoteRunResult{Status: preflightapi.StatusRunning}
	observer := remoteRunEventObserver{result: &result, outputMode: remoteRunOutputCollect}
	for _, event := range []preflightapi.RunEvent{
		{Type: preflightapi.EventStderr, Phase: "command", Step: "cross", Data: "go: cannot install cross-compiled binaries when GOBIN is set\n"},
		{Type: preflightapi.EventCommandExited, ExitCode: &one},
	} {
		if err := observer.handle(event); err != nil {
			t.Fatal(err)
		}
	}
	terminal, err := observer.finishFromRunDetail(preflightapi.RunDetail{Summary: preflightapi.RunSummary{Status: preflightapi.StatusFailed, ExitCode: &zero}})
	if err != nil || !terminal || result.ExitCode != 0 {
		t.Fatalf("recovery=%+v terminal=%t err=%v", result, terminal, err)
	}
	code := runFailureExitCode(result, result.Status)
	if code == nil || *code != 0 {
		t.Fatalf("earlier command failure replaced recovered terminal zero: %+v", code)
	}
	if analysis := localAnalyzeRunFailure(result.Status, "go install .", code, result.Events, ""); analysis.Classification == preflightapi.ClassificationEnvironmentSetupFailed {
		t.Fatalf("recovered zero authorized prerequisite evidence: %+v", analysis)
	}
}
