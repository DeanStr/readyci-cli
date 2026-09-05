package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestAgentVerifyProgressKeepsStdoutAsOneFinalJSONDocument(t *testing.T) {
	t.Setenv("READYCI_API_TOKEN", "")
	t.Setenv("READYCI_PROGRESS", "")
	cliTestWorkingDirectoryMu.Lock()
	defer cliTestWorkingDirectoryMu.Unlock()
	repo := t.TempDir()
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	}()
	runGitForCLITest(t, "init")
	runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
affected:
  rules:
    - paths: ["apps/**"]
      checks: ["web"]
checks:
  web:
    command: pnpm test
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("initial\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "initial")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatalf("change readme: %v", err)
	}

	var stdout string
	var commandErr error
	stderr, captureErr := captureStderrForCLITest(t, func() error {
		stdout, commandErr = captureStdoutForCLITest(t, func() error {
			return agentVerifyCommand([]string{"--json", "--progress=status"})
		})
		return commandErr
	})
	if captureErr != nil || commandErr != nil {
		t.Fatalf("agent verify progress: capture=%v command=%v\nstdout=%s\nstderr=%s", captureErr, commandErr, stdout, stderr)
	}
	var report agentVerifyReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if report.Status != "skipped" || !report.Terminal {
		t.Fatalf("agent report = %+v, want terminal skipped", report)
	}
	if !strings.Contains(stderr, "readyci: verification planned · affected checks: none") {
		t.Fatalf("stderr missing semantic progress: %q", stderr)
	}
	if strings.Contains(stdout, "readyci:") || strings.Contains(stderr, `"schema_version"`) {
		t.Fatalf("progress/final output streams mixed:\nstdout=%s\nstderr=%s", stdout, stderr)
	}

	stdout = ""
	stderr, captureErr = captureStderrForCLITest(t, func() error {
		stdout, commandErr = captureStdoutForCLITest(t, func() error {
			return agentVerifyCommand([]string{"--json"})
		})
		return commandErr
	})
	if captureErr != nil || commandErr != nil {
		t.Fatalf("agent verify quiet auto: capture=%v command=%v", captureErr, commandErr)
	}
	if strings.TrimSpace(stderr) != "" {
		t.Fatalf("default JSON progress stderr = %q, want quiet", stderr)
	}
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("default JSON stdout invalid: %v\n%s", err, stdout)
	}
}

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

func TestRemoteRunObserverSendsSemanticProgressInCollectMode(t *testing.T) {
	var output bytes.Buffer
	reporter, err := newAgentProgressReporter("status", true, &output, false)
	if err != nil {
		t.Fatalf("new progress reporter: %v", err)
	}
	result := &remoteRunResult{RunID: "run_cli"}
	observer := remoteRunEventObserver{
		result:     result,
		outputMode: remoteRunOutputCollect,
		progress:   reporter,
		runName:    "cli",
	}
	if err := observer.handle(preflightapi.RunEvent{Type: preflightapi.EventCommandStarted}); err != nil {
		t.Fatalf("handle command started: %v", err)
	}
	if err := observer.handle(preflightapi.RunEvent{Type: preflightapi.EventStdout, Step: "unit", Data: "raw output\n"}); err != nil {
		t.Fatalf("handle stdout: %v", err)
	}
	got := output.String()
	if !strings.Contains(got, "cli · command running") || !strings.Contains(got, "cli · step: unit") {
		t.Fatalf("observer progress = %q", got)
	}
	if strings.Contains(got, "raw output") {
		t.Fatalf("observer status progress leaked stdout: %q", got)
	}
}
