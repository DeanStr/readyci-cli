package main

import (
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestRunFailureStatusFromTerminalErrors(t *testing.T) {
	for event, want := range map[string]string{
		preflightapi.EventRunFinished:  preflightapi.StatusSucceeded,
		preflightapi.EventRunTimedOut:  preflightapi.StatusTimedOut,
		preflightapi.EventRunCancelled: preflightapi.StatusCancelled,
		preflightapi.EventRunFailed:    preflightapi.StatusFailed,
		"unknown":                      "",
	} {
		if got := runFailureStatusFromError(terminalRunError{eventType: event, exitCode: 1}); got != want {
			t.Fatalf("%s status = %q, want %q", event, got, want)
		}
	}
	if got := runFailureStatusFromError(exitCodeError{code: 1}); got != "" {
		t.Fatalf("non-terminal error status = %q, want empty", got)
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
