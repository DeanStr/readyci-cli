package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestAgentVerifyReturnsPendingWaitReportWhenStreamEndsBeforeTerminalEvent(t *testing.T) {
	cliTestWorkingDirectoryMu.Lock()
	defer cliTestWorkingDirectoryMu.Unlock()

	repo := t.TempDir()
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore wd: %v", err)
		}
	}()
	runGitForCLITest(t, "init")
	runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
	runGitForCLITest(t, "config", "user.name", "Preflight")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("pending verify\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
project_id: proj_story
checks:
  touched:
    command: go test ./...
    changes: all
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")

	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer pfl_story" {
			t.Fatalf("authorization = %q, want bearer token", got)
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs":
			writeCLITestJSON(w, http.StatusCreated, preflightapi.CreateRunResponse{
				RunID:     "run_pending",
				UploadURL: serverURL + "/upload",
				EventsURL: serverURL + "/events",
				Status:    preflightapi.StatusAwaitingUpload,
			})
		case r.Method == http.MethodPut && r.URL.Path == "/upload":
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte("{}\n"))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs/run_pending/upload-complete":
			writeCLITestJSON(w, http.StatusOK, preflightapi.CompleteUploadResponse{RunID: "run_pending", Status: preflightapi.StatusQueued})
		case r.Method == http.MethodGet && r.URL.Path == "/events":
			event := preflightapi.RunEvent{Type: preflightapi.EventStdout, RunID: "run_pending", Data: "waiting for worker\n"}
			data, _ := json.Marshal(event)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write(append(append([]byte("data: "), data...), '\n', '\n'))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	output, err := captureStdoutForCLITest(t, func() error {
		return agentVerifyCommand([]string{"--api-url", server.URL, "--token", "pfl_story", "--project", "proj_story", "--check", "touched", "--json"})
	})
	if err != nil {
		t.Fatalf("agent verify pending returned error = %v, want nil", err)
	}
	var report agentVerifyReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode pending verify report: %v\n%s", err, output)
	}
	if report.RunID != "run_pending" || report.Status != preflightapi.StatusQueued || report.Terminal || report.ExitCode != nil || report.Classification != preflightapi.ClassificationPending || report.AgentAction != preflightapi.AgentActionWait {
		t.Fatalf("pending verify report = %+v, want queued pending wait report", report)
	}
}

func TestAgentVerifyDoesNotCoverStreamEndedAfterObservedCommandFailure(t *testing.T) {
	exitCode := 1
	streamErr := runStreamEndedError{runID: "run_failed_stream", status: preflightapi.StatusRunning}
	events := []preflightapi.RunEvent{
		{Type: preflightapi.EventStderr, RunID: "run_failed_stream", Data: "--- FAIL: TestStore\nstore_test.go:42: expected queued, got failed\n"},
		{Type: preflightapi.EventCommandExited, RunID: "run_failed_stream", ExitCode: &exitCode},
	}
	run := agentRunReportFromResult(
		"touched",
		"",
		remoteRunOptions{Command: "go test ./..."},
		remoteRunResult{
			RunID:           "run_failed_stream",
			Status:          preflightapi.StatusRunning,
			CommandExited:   true,
			CommandExitCode: exitCode,
			Events:          events,
		},
		streamErr,
	)
	report := summarizeAgentVerifyRuns(agentVerifyReport{
		Check: "touched",
		Runs:  []agentVerifyRunReport{run},
	})
	if report.Classification != preflightapi.ClassificationTestFailed || report.AgentAction != preflightapi.AgentActionFixCode {
		t.Fatalf("stream-ended failed command report = %+v, want test_failed/fix_code", report)
	}
	if report.ExitCode == nil || *report.ExitCode != 1 {
		t.Fatalf("stream-ended failed command exit_code = %v, want 1", report.ExitCode)
	}
	if agentRunErrorCoveredByReport(report, streamErr) {
		t.Fatalf("stream-ended failed command was covered as pending: %+v", report)
	}
	if code := agentReportProcessExitCode(report); code != 1 {
		t.Fatalf("process exit code = %d, want 1", code)
	}

	detailExitCode := exitCodeForRunDetail(preflightapi.RunDetail{
		Summary: preflightapi.RunSummary{RunID: "run_failed_stream", Status: preflightapi.StatusRunning},
	}, events)
	if detailExitCode == nil || *detailExitCode != 1 {
		t.Fatalf("non-terminal detail exit code = %v, want observed command exit 1", detailExitCode)
	}
}

func TestAgentSummaryPrioritizesTerminalFailureOverPendingRun(t *testing.T) {
	report := summarizeAgentVerifyRuns(agentVerifyReport{
		Check: "matrix",
		Runs: []agentVerifyRunReport{
			{
				RunID:          "run_pending",
				Status:         preflightapi.StatusRunning,
				Terminal:       false,
				Classification: preflightapi.ClassificationPending,
				AgentAction:    preflightapi.AgentActionWait,
				Summary:        "Run is still in progress.",
			},
			{
				RunID:          "run_failed",
				Status:         preflightapi.StatusFailed,
				Terminal:       true,
				ExitCode:       intPtr(1),
				Classification: preflightapi.ClassificationTestFailed,
				AgentAction:    preflightapi.AgentActionFixCode,
				Summary:        "Tests failed.",
			},
		},
	})
	if report.RunID != "run_failed" || report.Status != preflightapi.StatusFailed || !report.Terminal || report.ExitCode == nil || *report.ExitCode != 1 {
		t.Fatalf("summary = %+v, want failed terminal run selected over pending run", report)
	}
	if code := agentReportProcessExitCode(report); code != 1 {
		t.Fatalf("process exit code = %d, want 1", code)
	}
}

func TestAgentSuccessSummaryForGroupedRuns(t *testing.T) {
	t.Run("uses single child check when top-level check is empty", func(t *testing.T) {
		report := summarizeAgentVerifyRuns(agentVerifyReport{
			Mode: "affected_rules",
			Runs: []agentVerifyRunReport{
				{RunID: "run_tools", Check: "tools", Status: preflightapi.StatusSucceeded, Terminal: true, ExitCode: intPtr(0), Classification: preflightapi.ClassificationSuccess},
			},
		})
		if report.Summary != "Ready CI check tools passed." {
			t.Fatalf("summary = %q, want child check success summary", report.Summary)
		}
	})

	t.Run("uses grouped wording for multiple child checks", func(t *testing.T) {
		report := summarizeAgentVerifyRuns(agentVerifyReport{
			Mode: "affected_rules",
			Runs: []agentVerifyRunReport{
				{RunID: "run_cli", Check: "cli", Status: preflightapi.StatusSucceeded, Terminal: true, ExitCode: intPtr(0), Classification: preflightapi.ClassificationSuccess},
				{RunID: "run_web", Check: "web", Status: preflightapi.StatusSucceeded, Terminal: true, ExitCode: intPtr(0), Classification: preflightapi.ClassificationSuccess},
			},
		})
		if report.Summary != "Ready CI verification passed for 2 checks." {
			t.Fatalf("summary = %q, want grouped success summary", report.Summary)
		}
	})
}
