package main

import (
	"bytes"
	"strings"
	"sync"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestNormalizeAgentProgressMode(t *testing.T) {
	for _, value := range []string{"", "auto", "status", "quiet", "logs", " STATUS "} {
		if _, err := normalizeAgentProgressMode(value); err != nil {
			t.Fatalf("normalize progress mode %q: %v", value, err)
		}
	}
	if _, err := normalizeAgentProgressMode("spinner"); err == nil {
		t.Fatal("normalize invalid progress mode returned nil error")
	}
}

func TestVerificationSessionCommandRejectsRawLogProgress(t *testing.T) {
	err := verificationCommand([]string{"--progress=logs"})
	if err == nil || !strings.Contains(err.Error(), "readyci runs logs") {
		t.Fatalf("logs progress error = %v", err)
	}
}

func TestAgentProgressAutoPreservesJSONSilence(t *testing.T) {
	var output bytes.Buffer
	reporter, err := newAgentProgressReporter("auto", true, &output, true)
	if err != nil {
		t.Fatalf("new progress reporter: %v", err)
	}
	reporter.Emit(agentProgressEvent{Kind: agentProgressPlanned, Mode: "check", Checks: []string{"cli"}})
	reporter.Log(preflightapi.RunEvent{RunID: "run_cli", Step: "unit"}, []byte("test output\n"))
	if output.Len() != 0 {
		t.Fatalf("auto JSON progress output = %q, want quiet", output.String())
	}
}

func TestAgentProgressStatusProjectsLifecycleWithoutRawLogs(t *testing.T) {
	var output bytes.Buffer
	reporter, err := newAgentProgressReporter("status", true, &output, false)
	if err != nil {
		t.Fatalf("new progress reporter: %v", err)
	}
	reporter.Emit(agentProgressEvent{Kind: agentProgressPlanned, Mode: "affected checks", Checks: []string{"web", "cli"}})
	reporter.Emit(agentProgressEvent{Kind: agentProgressWorkspacePackaged, SizeBytes: 18 * 1024 * 1024})
	reporter.Emit(agentProgressEvent{Kind: agentProgressRunAccepted, RunName: "cli", RunID: "run_cli"})
	reporter.Emit(agentProgressEvent{Kind: agentProgressLifecycle, RunName: "cli", RunID: "run_cli", EventType: preflightapi.EventVMStarting})
	reporter.Emit(agentProgressEvent{Kind: agentProgressLifecycle, RunName: "cli", RunID: "run_cli", EventType: preflightapi.EventVMStarting})
	reporter.Log(preflightapi.RunEvent{RunID: "run_cli", Step: "unit tests"}, []byte("secret raw log\n"))
	reporter.Log(preflightapi.RunEvent{RunID: "run_cli", Step: "unit tests"}, []byte("second raw log\n"))
	reporter.Emit(agentProgressEvent{Kind: agentProgressLifecycle, RunName: "cli", RunID: "run_cli", EventType: preflightapi.EventRunFinished})
	reporter.Emit(agentProgressEvent{Kind: agentProgressNodeStatus, RunName: "typecheck", RunID: "typecheck", Status: preflightapi.StatusRunning})
	reporter.Emit(agentProgressEvent{Kind: agentProgressNodeStatus, RunName: "typecheck", RunID: "typecheck", Status: preflightapi.StatusRunning})

	got := output.String()
	for _, want := range []string{
		"verification planned · affected checks: cli, web",
		"workspace packaged · 18.0 MiB",
		"cli · accepted · run_cli",
		"cli · VM starting",
		"cli · step: unit tests",
		"cli · succeeded",
		"typecheck · running",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("status progress missing %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "VM starting") != 1 || strings.Count(got, "step: unit tests") != 1 || strings.Count(got, "typecheck · running") != 1 {
		t.Fatalf("status progress did not suppress duplicate transitions:\n%s", got)
	}
	if strings.Contains(got, "secret raw log") || strings.Contains(got, "second raw log") {
		t.Fatalf("status progress leaked raw logs:\n%s", got)
	}
}

func TestAgentProgressLogsWritesLogDataToStderrProjection(t *testing.T) {
	var output bytes.Buffer
	reporter, err := newAgentProgressReporter("logs", true, &output, false)
	if err != nil {
		t.Fatalf("new progress reporter: %v", err)
	}
	reporter.Emit(agentProgressEvent{Kind: agentProgressRunAccepted, RunName: "platform", RunID: "run_platform"})
	reporter.Log(preflightapi.RunEvent{RunID: "run_platform", Step: "postgres"}, []byte("database test output"))
	got := output.String()
	if !strings.Contains(got, "platform · step: postgres") || !strings.Contains(got, "database test output\n") {
		t.Fatalf("logs progress output = %q", got)
	}
}

func TestAgentProgressStatusSanitizesSemanticFields(t *testing.T) {
	var output bytes.Buffer
	reporter, err := newAgentProgressReporter("status", true, &output, false)
	if err != nil {
		t.Fatalf("new progress reporter: %v", err)
	}
	reporter.Emit(agentProgressEvent{
		Kind:    agentProgressRunAccepted,
		RunName: "cli\nforged\x1b]0;title\a",
		RunID:   "run_cli\rforged",
	})
	reporter.Log(preflightapi.RunEvent{RunID: "run_cli\rforged", Step: "unit\nforged\x1b[31mred"}, nil)

	got := output.String()
	if strings.Contains(got, "\x1b") || strings.Contains(got, "\r") {
		t.Fatalf("status progress retained terminal controls: %q", got)
	}
	if strings.Count(strings.TrimSpace(got), "\n") != 1 {
		t.Fatalf("semantic fields forged progress lines: %q", got)
	}
	for _, want := range []string{"cli forged · accepted · run_cli forged", "cli forged · step: unit forgedred"} {
		if !strings.Contains(got, want) {
			t.Fatalf("sanitized status progress missing %q: %q", want, got)
		}
	}
}

func TestAgentProgressReporterSerializesParallelMatrixUpdates(t *testing.T) {
	var output bytes.Buffer
	reporter, err := newAgentProgressReporter("status", true, &output, false)
	if err != nil {
		t.Fatalf("new progress reporter: %v", err)
	}
	const count = 32
	var wg sync.WaitGroup
	for index := 0; index < count; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			runID := "run_" + string(rune('A'+index))
			reporter.Emit(agentProgressEvent{Kind: agentProgressRunAccepted, RunName: runID, RunID: runID})
		}(index)
	}
	wg.Wait()
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != count {
		t.Fatalf("parallel progress lines = %d, want %d:\n%s", len(lines), count, output.String())
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "readyci: ") || !strings.Contains(line, " · accepted · ") {
			t.Fatalf("interleaved progress line = %q", line)
		}
	}
}
