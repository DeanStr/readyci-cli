package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestRemoteRunEventObserverCollectsStateAndTerminalExit(t *testing.T) {
	exitCode := 7
	result := &remoteRunResult{RunID: "run_stream"}
	observer := &remoteRunEventObserver{
		result:     result,
		outputMode: remoteRunOutputCollect,
		keepFailed: true,
	}
	if err := observer.handle(preflightapi.RunEvent{ID: 2, Type: preflightapi.EventStdout, Data: "ordinary stdout\n"}); err != nil {
		t.Fatalf("handle stdout: %v", err)
	}
	if err := observer.handle(preflightapi.RunEvent{ID: 3, Type: preflightapi.EventStderr, Data: preflightapi.FailedWorkspaceArtifactRetainedMessage + "\n--- FAIL: TestStore\n"}); err != nil {
		t.Fatalf("handle stderr: %v", err)
	}
	if err := observer.handle(preflightapi.RunEvent{ID: 4, Type: preflightapi.EventLogTruncated, Message: "logs truncated"}); err != nil {
		t.Fatalf("handle log truncated: %v", err)
	}
	if err := observer.handle(preflightapi.RunEvent{ID: 5, Type: preflightapi.EventArtifactUploaded}); err != nil {
		t.Fatalf("handle artifact uploaded: %v", err)
	}
	if err := observer.handle(preflightapi.RunEvent{ID: 6, Type: preflightapi.EventCommandExited, ExitCode: &exitCode}); err != nil {
		t.Fatalf("handle command exited: %v", err)
	}
	err := observer.handle(preflightapi.RunEvent{ID: 7, Type: preflightapi.EventRunFailed, Message: "test failed"})
	var terminal terminalRunError
	if !errors.As(err, &terminal) || terminal.exitCode != exitCode || terminal.eventType != preflightapi.EventRunFailed {
		t.Fatalf("terminal err = %v, want run_failed exit 7", err)
	}
	if result.Status != preflightapi.StatusFailed || result.ExitCode != exitCode || result.FailureReason != "test failed" {
		t.Fatalf("result = %+v, want failed exit 7 with reason", result)
	}
	if !result.FailedWorkspaceArtifactRetained || !result.LogTruncated || !result.ArtifactUploaded || !result.CommandExited {
		t.Fatalf("result flags = %+v, want retained/log-truncated/artifact/command-exited", result)
	}
	if observer.cursor != 7 || observer.seen != 6 {
		t.Fatalf("cursor/seen = %d/%d, want 7/6", observer.cursor, observer.seen)
	}
	if len(result.Events) != 6 {
		t.Fatalf("collected events = %d, want 6", len(result.Events))
	}
	if len(result.AnalysisEvents) == 0 {
		t.Fatal("analysis events empty, want stderr/truncation/terminal context")
	}
}

func TestRemoteRunEventObserverFinishFromRunDetailDefaultsTerminalExit(t *testing.T) {
	tests := []struct {
		status   string
		wantType string
		wantExit int
		terminal bool
		exitCode *int
	}{
		{status: preflightapi.StatusRunning, terminal: false},
		{status: preflightapi.StatusSucceeded, wantType: preflightapi.EventRunFinished, wantExit: 0, terminal: true},
		{status: preflightapi.StatusTimedOut, wantType: preflightapi.EventRunTimedOut, wantExit: 124, terminal: true},
		{status: preflightapi.StatusCancelled, wantType: preflightapi.EventRunCancelled, wantExit: 130, terminal: true},
		{status: preflightapi.StatusFailed, wantType: preflightapi.EventRunFailed, wantExit: 1, terminal: true},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			result := &remoteRunResult{}
			observer := &remoteRunEventObserver{result: result}
			terminal, err := observer.finishFromRunDetail(preflightapi.RunDetail{Summary: preflightapi.RunSummary{Status: tt.status, ExitCode: tt.exitCode}})
			if err != nil {
				t.Fatalf("finishFromRunDetail: %v", err)
			}
			if terminal != tt.terminal {
				t.Fatalf("terminal = %t, want %t", terminal, tt.terminal)
			}
			if !tt.terminal {
				return
			}
			if result.TerminalEventType != tt.wantType || result.ExitCode != tt.wantExit {
				t.Fatalf("result = %+v, want type %s exit %d", result, tt.wantType, tt.wantExit)
			}
		})
	}
}

func TestRemoteRunEventObserverFinishFromRunDetailCopiesDiagnosis(t *testing.T) {
	result := &remoteRunResult{}
	observer := &remoteRunEventObserver{result: result}
	exitCode := 1
	terminal, err := observer.finishFromRunDetail(preflightapi.RunDetail{Summary: preflightapi.RunSummary{
		Status:         preflightapi.StatusFailed,
		ExitCode:       &exitCode,
		Classification: preflightapi.ClassificationDependencyDownloadFailed,
		Retryable:      true,
		AgentAction:    preflightapi.AgentActionRetry,
		FailureSummary: "Dependency download failed.",
		PrimaryFile:    "package.json",
	}})
	if err != nil {
		t.Fatalf("finishFromRunDetail: %v", err)
	}
	if !terminal {
		t.Fatal("terminal = false, want true")
	}
	if result.Classification != preflightapi.ClassificationDependencyDownloadFailed ||
		!result.Retryable ||
		result.AgentAction != preflightapi.AgentActionRetry ||
		result.FailureSummary != "Dependency download failed." ||
		result.PrimaryFile != "package.json" {
		t.Fatalf("result diagnosis = %+v", result)
	}
}

func TestRemoteRunAnalysisKeepsUsefulOutputAndHints(t *testing.T) {
	if !runFailureAnalysisTextLooksUseful("AssertionError: expected 1 got 2") {
		t.Fatal("assertion output should be useful for run-failure analysis")
	}
	if runFailureAnalysisTextLooksUseful("downloaded 10 packages successfully") {
		t.Fatal("ordinary progress output should not be useful for run-failure analysis")
	}
	if hint := runFailureDiagnosisHint(preflightapi.ClassificationNetworkNotAllowed); !strings.Contains(hint, "network access") {
		t.Fatalf("network hint = %q, want actionable network access guidance", hint)
	}
	if hint := runFailureDiagnosisHint(preflightapi.ClassificationTestFailed); hint != "" {
		t.Fatalf("test failure hint = %q, want empty because test output should carry details", hint)
	}
}

func TestRemoteRunObserverCapsAnalysisEventsAndDecodesBase64(t *testing.T) {
	result := &remoteRunResult{}
	observer := &remoteRunEventObserver{result: result}
	for i := 0; i < runFailureAnalysisEventLimit+5; i++ {
		observer.observeAnalysisEvent(preflightapi.RunEvent{
			ID:         int64(i + 1),
			Type:       preflightapi.EventStderr,
			DataBase64: "ZXJyb3IgbGluZQ==",
		})
	}
	if len(result.AnalysisEvents) != runFailureAnalysisEventLimit {
		t.Fatalf("analysis events = %d, want cap %d", len(result.AnalysisEvents), runFailureAnalysisEventLimit)
	}
	first := result.AnalysisEvents[0]
	if first.ID != 6 || first.Data != "error line" || first.DataBase64 != "" {
		t.Fatalf("first retained event = %+v, want decoded event after cap", first)
	}
}
