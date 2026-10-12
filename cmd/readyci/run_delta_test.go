package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestRetryableRemoteRunErrorSkipsCancelledRuns(t *testing.T) {
	code, retryable := retryableRemoteRunError(terminalRunError{eventType: preflightapi.EventRunCancelled, exitCode: 130})
	if retryable {
		t.Fatal("cancelled terminal run was retryable")
	}
	if code != 130 {
		t.Fatalf("cancelled terminal code = %d, want 130", code)
	}
	var exitErr exitCodeError
	if !errors.As(terminalRunError{eventType: preflightapi.EventRunCancelled, exitCode: 130}, &exitErr) || exitErr.code != 130 {
		t.Fatalf("terminal run error did not unwrap to exit code: %#v", exitErr)
	}
}

func TestRetryableRemoteRunErrorSkipsUnknownTerminalState(t *testing.T) {
	err := runStreamEndedError{runID: "run_pending", status: preflightapi.StatusRunning}
	code, retryable := retryableRemoteRunError(err)
	if retryable {
		t.Fatal("run with unknown terminal state was retryable")
	}
	if code != 1 {
		t.Fatalf("unknown terminal state code = %d, want 1", code)
	}

	code, retryable = retryableRemoteRunResult(remoteRunResult{
		RunID:  "run_pending",
		Status: preflightapi.StatusRunning,
	}, err, "go test ./...")
	if retryable {
		t.Fatal("run result with unknown terminal state was retryable")
	}
	if code != 1 {
		t.Fatalf("unknown terminal result code = %d, want 1", code)
	}
}

func TestRetryableRemoteRunErrorSkipsDeterministicCommandFailures(t *testing.T) {
	err := remoteRunAttemptError{
		command: "go test ./...",
		result: remoteRunResult{
			Status:            preflightapi.StatusFailed,
			TerminalEventType: preflightapi.EventRunFailed,
			ExitCode:          7,
			AnalysisEvents: []preflightapi.RunEvent{{
				Type: preflightapi.EventStderr,
				Data: "--- FAIL: TestStore\n",
			}},
		},
		err: terminalRunError{eventType: preflightapi.EventRunFailed, exitCode: 7},
	}
	code, retryable := retryableRemoteRunError(err)
	if retryable {
		t.Fatal("deterministic command failure was retryable")
	}
	if code != 7 {
		t.Fatalf("failed terminal code = %d, want 7", code)
	}
}

func TestRetryableRemoteRunErrorAllowsRetryableClassifications(t *testing.T) {
	err := remoteRunAttemptError{
		command: "pnpm install",
		result: remoteRunResult{
			Status:            preflightapi.StatusFailed,
			TerminalEventType: preflightapi.EventRunFailed,
			ExitCode:          7,
			Classification:    preflightapi.ClassificationDependencyDownloadFailed,
			Retryable:         true,
			AgentAction:       preflightapi.AgentActionRetry,
			FailureSummary:    "Dependency download failed.",
		},
		err: terminalRunError{eventType: preflightapi.EventRunFailed, exitCode: 7},
	}
	code, retryable := retryableRemoteRunError(err)
	if !retryable {
		t.Fatal("dependency failure was not retryable")
	}
	if code != 7 {
		t.Fatalf("failed terminal code = %d, want 7", code)
	}
}

func TestRetryableRemoteRunUsesServerAnalysis(t *testing.T) {
	result := remoteRunResult{
		Status:            preflightapi.StatusFailed,
		TerminalEventType: preflightapi.EventRunFailed,
		ExitCode:          7,
		Classification:    preflightapi.ClassificationDependencyDownloadFailed,
		Retryable:         true,
		AgentAction:       preflightapi.AgentActionRetry,
		FailureSummary:    "Dependency download failed.",
		Events: []preflightapi.RunEvent{{
			Type: preflightapi.EventStderr,
			Data: "--- FAIL: TestStore\n",
		}},
		AnalysisEvents: []preflightapi.RunEvent{{
			Type: preflightapi.EventStderr,
			Data: "ERR_PNPM_FETCH GET https://registry.npmjs.org/pkg: connection timed out\n",
		}},
	}
	code, retryable := retryableRemoteRunResult(result, terminalRunError{eventType: preflightapi.EventRunFailed, exitCode: 7}, "pnpm install")
	if !retryable {
		t.Fatal("retry result did not use server retryable analysis")
	}
	if code != 7 {
		t.Fatalf("failed terminal code = %d, want 7", code)
	}

	err := remoteRunAttemptError{
		command: "pnpm install",
		result:  result,
		err:     terminalRunError{eventType: preflightapi.EventRunFailed, exitCode: 7},
	}
	_, retryable = retryableRemoteRunError(err)
	if !retryable {
		t.Fatal("retry error did not use server retryable analysis")
	}
}

func TestRetryableRemoteRunKeepsDeterministicAnalysisNonRetryableWhenBothStreamsExist(t *testing.T) {
	result := remoteRunResult{
		Status:            preflightapi.StatusFailed,
		TerminalEventType: preflightapi.EventRunFailed,
		ExitCode:          7,
		Events: []preflightapi.RunEvent{{
			Type: preflightapi.EventStderr,
			Data: "ERR_PNPM_FETCH GET https://registry.npmjs.org/pkg: connection timed out\n",
		}},
		AnalysisEvents: []preflightapi.RunEvent{{
			Type: preflightapi.EventStderr,
			Data: "--- FAIL: TestStore\nexpected ok, got error\n",
		}},
	}
	code, retryable := retryableRemoteRunResult(result, terminalRunError{eventType: preflightapi.EventRunFailed, exitCode: 7}, "go test ./...")
	if retryable {
		t.Fatal("deterministic curated analysis event was retryable")
	}
	if code != 7 {
		t.Fatalf("failed terminal code = %d, want 7", code)
	}
}

func TestTrimRunFailureAnalysisDataPreservesUTF8WithinByteLimit(t *testing.T) {
	trimmed := trimRunFailureAnalysisData(strings.Repeat("界", runFailureAnalysisDataLimit))
	if len(trimmed) > runFailureAnalysisDataLimit {
		t.Fatalf("trimmed byte length = %d, want at most %d", len(trimmed), runFailureAnalysisDataLimit)
	}
	if !utf8.ValidString(trimmed) {
		t.Fatalf("trimmed data is invalid UTF-8: %q", trimmed)
	}
	if !strings.Contains(trimmed, "diagnostic log chunk truncated") {
		t.Fatalf("trimmed data = %q, want truncation marker", trimmed)
	}
}

func TestTrimRunFailureAnalysisDataHandlesInvalidUTF8WithoutOverTruncating(t *testing.T) {
	text := "prefix" + string([]byte{0xff}) + strings.Repeat("x", runFailureAnalysisDataLimit)
	trimmed := trimRunFailureAnalysisData(text)
	if len(trimmed) > runFailureAnalysisDataLimit {
		t.Fatalf("trimmed byte length = %d, want at most %d", len(trimmed), runFailureAnalysisDataLimit)
	}
	if !utf8.ValidString(trimmed) {
		t.Fatalf("trimmed data is invalid UTF-8: %q", trimmed)
	}
	if !strings.Contains(trimmed, "xxx") {
		t.Fatalf("trimmed data = %q, want content after invalid byte to be preserved", trimmed)
	}
	if !strings.Contains(trimmed, "diagnostic log chunk truncated") {
		t.Fatalf("trimmed data = %q, want truncation marker", trimmed)
	}
}

func TestShouldPrintRunFailureDiagnosisSkipsLocalCancellation(t *testing.T) {
	if shouldPrintRunFailureDiagnosis(context.Canceled) {
		t.Fatal("local cancellation should not print run-failure diagnosis")
	}
	if !shouldPrintRunFailureDiagnosis(terminalRunError{eventType: preflightapi.EventRunCancelled, exitCode: 130}) {
		t.Fatal("remote cancellation should still be diagnosable")
	}
}

func TestPrintRunFailureDiagnosisIncludesClassificationAndHint(t *testing.T) {
	result := remoteRunResult{
		RunID:             "run_dns",
		Status:            preflightapi.StatusFailed,
		TerminalEventType: preflightapi.EventRunFailed,
		ExitCode:          1,
		CommandExited:     true,
		CommandExitCode:   1,
		Classification:    preflightapi.ClassificationDependencyDownloadFailed,
		Retryable:         true,
		AgentAction:       preflightapi.AgentActionRetry,
		FailureSummary:    "Dependency download failed.",
		AnalysisEvents: []preflightapi.RunEvent{{
			Type: preflightapi.EventStderr,
			Data: "npm error code EAI_AGAIN\nnpm error syscall getaddrinfo\n",
		}},
	}
	var output strings.Builder
	printRunFailureDiagnosis(&output, result, terminalRunError{eventType: preflightapi.EventRunFailed, exitCode: 1}, "npx -y @stoplight/spectral-cli lint docs/api/openapi.yaml")
	got := output.String()
	for _, want := range []string{"run_dns failed", "dependency_download_failed", "retryable", "readyci warm --check dependencies with an explicit network allowlist", "readyci agent explain run_dns"} {
		if !strings.Contains(got, want) {
			t.Fatalf("diagnosis output %q does not contain %q", got, want)
		}
	}
}

func TestRemoteObserverDefaultsMissingTerminalExitCodes(t *testing.T) {
	tests := map[string]struct {
		eventType string
		status    string
		wantCode  int
	}{
		"failed": {
			eventType: preflightapi.EventRunFailed,
			status:    preflightapi.StatusFailed,
			wantCode:  1,
		},
		"timed out": {
			eventType: preflightapi.EventRunTimedOut,
			status:    preflightapi.StatusTimedOut,
			wantCode:  124,
		},
		"cancelled without status": {
			eventType: preflightapi.EventRunCancelled,
			wantCode:  130,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result := remoteRunResult{}
			observer := remoteRunEventObserver{result: &result}
			err := observer.handle(preflightapi.RunEvent{Type: tt.eventType, Status: tt.status})
			var exitErr exitCodeError
			if !errors.As(err, &exitErr) {
				t.Fatalf("handle error = %v, want exit code error", err)
			}
			if exitErr.code != tt.wantCode || result.ExitCode != tt.wantCode {
				t.Fatalf("exit code = %d result=%d, want %d", exitErr.code, result.ExitCode, tt.wantCode)
			}
			if result.Status != defaultString(tt.status, runStatusForTerminalEvent(tt.eventType)) {
				t.Fatalf("status = %q", result.Status)
			}
		})
	}
}

func TestRemoteObserverDoesNotReuseZeroCommandExitForFailedTerminal(t *testing.T) {
	tests := []struct {
		name            string
		commandExitCode int
		terminalEvent   string
		terminalStatus  string
		wantCode        int
	}{
		{
			name:            "failed terminal ignores zero command exit",
			commandExitCode: 0,
			terminalEvent:   preflightapi.EventRunFailed,
			terminalStatus:  preflightapi.StatusFailed,
			wantCode:        1,
		},
		{
			name:            "timed out terminal ignores zero command exit",
			commandExitCode: 0,
			terminalEvent:   preflightapi.EventRunTimedOut,
			terminalStatus:  preflightapi.StatusTimedOut,
			wantCode:        124,
		},
		{
			name:            "failed terminal preserves nonzero command exit",
			commandExitCode: 7,
			terminalEvent:   preflightapi.EventRunFailed,
			terminalStatus:  preflightapi.StatusFailed,
			wantCode:        7,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := remoteRunResult{}
			observer := remoteRunEventObserver{result: &result}
			commandExit := tt.commandExitCode
			if err := observer.handle(preflightapi.RunEvent{Type: preflightapi.EventCommandExited, ExitCode: &commandExit}); err != nil {
				t.Fatalf("command exit handle error = %v", err)
			}
			err := observer.handle(preflightapi.RunEvent{Type: tt.terminalEvent, Status: tt.terminalStatus})
			var exitErr exitCodeError
			if !errors.As(err, &exitErr) {
				t.Fatalf("terminal handle error = %v, want exit code error", err)
			}
			if exitErr.code != tt.wantCode || result.ExitCode != tt.wantCode {
				t.Fatalf("exit code = %d result=%d, want %d", exitErr.code, result.ExitCode, tt.wantCode)
			}
		})
	}
}

func TestAnalyzeRemoteRunFailureKeepsPendingStreamDropsPending(t *testing.T) {
	analysis := analyzeRemoteRunFailure(
		remoteRunResult{RunID: "run_stream", Status: preflightapi.StatusRunning},
		runStreamEndedError{runID: "run_stream", status: preflightapi.StatusRunning},
		"go test ./...",
	)
	if analysis.Classification != preflightapi.ClassificationPending || analysis.AgentAction != preflightapi.AgentActionWait {
		t.Fatalf("analysis = %+v, want pending wait", analysis)
	}

	commandExit := 1
	analysis = analyzeRemoteRunFailure(
		remoteRunResult{
			RunID:           "run_stream",
			Status:          preflightapi.StatusRunning,
			CommandExited:   true,
			CommandExitCode: commandExit,
			AnalysisEvents: []preflightapi.RunEvent{{
				Type: preflightapi.EventStderr,
				Data: "--- FAIL: TestStore\n",
			}},
		},
		runStreamEndedError{runID: "run_stream", status: preflightapi.StatusRunning},
		"go test ./...",
	)
	if analysis.Classification != preflightapi.ClassificationTestFailed || analysis.AgentAction != preflightapi.AgentActionFixCode {
		t.Fatalf("analysis = %+v, want observed command failure", analysis)
	}
}

func TestHumanModeDiagnosisKeepsAuthEvidence(t *testing.T) {
	result := remoteRunResult{
		RunID:         "run_auth",
		Status:        preflightapi.StatusFailed,
		ExitCode:      1,
		CommandExited: true,
	}
	observer := remoteRunEventObserver{result: &result, outputMode: remoteRunOutputHuman}
	observer.observeAnalysisStreamEvent(preflightapi.RunEvent{
		Type: preflightapi.EventStderr,
	}, []byte("403 forbidden: missing required scope runs:read\n"))
	if len(result.AnalysisEvents) == 0 {
		t.Fatal("auth stderr was not retained for diagnosis")
	}
	result.Classification = preflightapi.ClassificationAuthForbidden
	result.AgentAction = preflightapi.AgentActionAskForCredentials
	result.FailureSummary = "Missing required scope."
	analysis := analyzeRemoteRunFailure(result, terminalRunError{eventType: preflightapi.EventRunFailed, exitCode: 1}, "readyci runs latest")
	if analysis.Classification != preflightapi.ClassificationAuthForbidden || analysis.AgentAction != preflightapi.AgentActionAskForCredentials {
		t.Fatalf("analysis = %+v, want auth_forbidden ask_for_credentials", analysis)
	}
}

func TestHumanModeDiagnosisKeepsUnrecognizedStderr(t *testing.T) {
	result := remoteRunResult{}
	observer := remoteRunEventObserver{result: &result, outputMode: remoteRunOutputHuman}
	observer.observeAnalysisStreamEvent(preflightapi.RunEvent{
		Type: preflightapi.EventStderr,
	}, []byte("custom verifier says nope\n"))
	if len(result.AnalysisEvents) != 1 || !strings.Contains(result.AnalysisEvents[0].Data, "custom verifier") {
		t.Fatalf("analysis events = %+v, want unrecognized stderr retained", result.AnalysisEvents)
	}

	observer.observeAnalysisStreamEvent(preflightapi.RunEvent{
		Type: preflightapi.EventStdout,
	}, []byte("ordinary stdout progress\n"))
	if len(result.AnalysisEvents) != 1 {
		t.Fatalf("analysis events = %+v, ordinary stdout should still be filtered", result.AnalysisEvents)
	}
}

func TestHumanModeDiagnosisKeepsStdoutTestEvidence(t *testing.T) {
	result := remoteRunResult{
		RunID:         "run_test",
		Status:        preflightapi.StatusFailed,
		ExitCode:      1,
		CommandExited: true,
	}
	observer := remoteRunEventObserver{result: &result, outputMode: remoteRunOutputHuman}
	observer.observeAnalysisStreamEvent(preflightapi.RunEvent{
		Type: preflightapi.EventStdout,
	}, []byte("--- FAIL: TestWidget\nexpected 1, got 2\n"))
	if len(result.AnalysisEvents) == 0 {
		t.Fatal("stdout test evidence was not retained for diagnosis")
	}
	analysis := analyzeRemoteRunFailure(result, terminalRunError{eventType: preflightapi.EventRunFailed, exitCode: 1}, "make check")
	if analysis.Classification != preflightapi.ClassificationTestFailed || analysis.AgentAction != preflightapi.AgentActionFixCode {
		t.Fatalf("analysis = %+v, want test_failed fix_code", analysis)
	}
}
