package main

import (
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestLocalArtifactLimitsRetainArtifactScope(t *testing.T) {
	commandExit, terminalExit := 0, 1
	for _, reason := range []string{
		"artifact upload failed: artifact collection incomplete: retained 514 of 525 selected files; 11 omitted",
		"artifact upload failed: required artifacts omitted during bounded collection",
		"artifact upload failed: expanded bundle exceeds limit",
		"artifact upload failed: artifact scan exceeds its entry limit: artifact bundle too large",
		"artifact upload failed: PUT artifacts failed: HTTP 413 Request Entity Too Large",
	} {
		for _, events := range [][]preflightapi.RunEvent{nil, {{Type: preflightapi.EventCommandExited, ExitCode: &commandExit}}} {
			got := localAnalyzeRunFailure(preflightapi.StatusFailed, "mvn verify", &terminalExit, events, reason)
			if got.Classification != preflightapi.ClassificationArtifactLimitExceeded || got.Retryable || got.AgentAction != preflightapi.AgentActionReduceScope || got.Summary != reason {
				t.Fatalf("diagnosis=%+v, want artifact_limit_exceeded/reduce_scope with original limit detail", got)
			}
		}
	}
}

func TestLocalArtifactFixtureTextKeepsReportedTestFailure(t *testing.T) {
	commandExit := 1
	events := []preflightapi.RunEvent{
		{Type: preflightapi.EventStderr, Data: "--- FAIL: TestWidget (0.01s)\nwidget_test.go:42: AssertionError: expected artifact bundle too large, got success\n"},
		{Type: preflightapi.EventCommandExited, ExitCode: &commandExit},
	}
	got := localAnalyzeRunFailure(preflightapi.StatusFailed, "go test ./... # required artifacts were omitted during bounded collection", &commandExit, events, "")
	if got.Classification != preflightapi.ClassificationTestFailed || got.AgentAction != preflightapi.AgentActionFixCode {
		t.Fatalf("artifact fixture replaced the actual test failure: %+v", got)
	}
}
