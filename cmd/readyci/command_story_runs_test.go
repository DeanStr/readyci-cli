package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestRunLookupLogsAndArtifactCommandsExerciseHTTPStories(t *testing.T) {
	fixed := time.Date(2026, 6, 22, 2, 3, 4, 0, time.UTC)
	exitCode := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/runs/run_story/artifacts/archive" {
			if got := r.Header.Get("Authorization"); got != "Bearer pfl_story" {
				t.Fatalf("authorization = %q, want bearer token", got)
			}
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs":
			if r.URL.Query().Get("project") != "proj_story" || r.URL.Query().Get("status") != "succeeded" || r.URL.Query().Get("limit") != "1" {
				t.Fatalf("runs query = %s, want project/status/limit", r.URL.RawQuery)
			}
			writeCLITestJSON(w, http.StatusOK, preflightapi.ListRunsResponse{Runs: []preflightapi.RunSummary{{
				RunID:      "run_story",
				ProjectID:  "proj_story",
				Command:    "go test ./...",
				Status:     preflightapi.StatusSucceeded,
				RunnerSize: preflightapi.DefaultRunnerSize,
				CacheMode:  "cache-only",
				ExitCode:   &exitCode,
				CreatedAt:  fixed,
				EventCount: 3,
			}}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/by-idempotency-key":
			if r.URL.Query().Get("project") != "proj_story" || r.URL.Query().Get("key") != "task-1" {
				t.Fatalf("idempotency query = %s, want project/key", r.URL.RawQuery)
			}
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunDetail{
				Summary: preflightapi.RunSummary{
					RunID:     "run_story",
					ProjectID: "proj_story",
					Command:   "go test ./...",
					Status:    preflightapi.StatusSucceeded,
					ExitCode:  &exitCode,
					CreatedAt: fixed,
				},
				Spec: preflightapi.RunSpec{
					RunID:     "run_story",
					ProjectID: "proj_story",
					Command:   "go test ./...",
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_story/events":
			failedExit := 1
			events := []preflightapi.RunEvent{
				{ID: 11, Type: preflightapi.EventStdout, RunID: "run_story", Timestamp: fixed, Phase: "test", Step: "go-test", Data: "hello from run\n"},
				{ID: 12, Type: preflightapi.EventStderr, RunID: "run_story", Timestamp: fixed.Add(time.Second), Phase: "test", Step: "go-test", Data: "--- FAIL: TestStory\npkg/story/story_test.go:42: expected ok\n"},
				{ID: 13, Type: preflightapi.EventCommandExited, RunID: "run_story", Timestamp: fixed.Add(2 * time.Second), Phase: "test", Step: "go-test", ExitCode: &failedExit},
				{ID: 14, Type: preflightapi.EventLogTruncated, RunID: "run_story", Timestamp: fixed.Add(3 * time.Second), Message: "log limit reached"},
				{ID: 15, Type: preflightapi.EventStdout, RunID: "run_story", Timestamp: fixed.Add(4 * time.Second), Phase: "services", Step: "db", Service: "postgres", Data: "postgres ready\n"},
			}
			if r.URL.Query().Get("format") == "json" {
				if tail := r.URL.Query().Get("tail"); tail != "" {
					if r.URL.Query().Get("cursor") != "" {
						t.Fatalf("tail events query = %s, want no cursor", r.URL.RawQuery)
					}
					if tail == "2" {
						if r.URL.Query().Get("logs_only") != "true" || r.URL.Query().Get("include_lifecycle") != "true" {
							t.Fatalf("tail events query = %s, want logs_only/include_lifecycle true", r.URL.RawQuery)
						}
						writeCLITestJSON(w, http.StatusOK, preflightapi.RunEventsResponse{RunID: "run_story", Events: events[2:4]})
						return
					}
					if r.URL.Query().Get("logs_only") != "true" {
						t.Fatalf("tail events query = %s, want logs_only=true", r.URL.RawQuery)
					}
					switch tail {
					case "1":
						writeCLITestJSON(w, http.StatusOK, preflightapi.RunEventsResponse{RunID: "run_story", Events: events[3:4]})
					case "4":
						writeCLITestJSON(w, http.StatusOK, preflightapi.RunEventsResponse{RunID: "run_story", Events: events[:4]})
					default:
						t.Fatalf("tail events query = %s, want supported tail", r.URL.RawQuery)
					}
					return
				}
				writeCLITestJSON(w, http.StatusOK, preflightapi.RunEventsResponse{RunID: "run_story", Events: events})
				return
			}
			event := events[0]
			data, _ := json.Marshal(event)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: stdout\n"))
			_, _ = w.Write(append(append([]byte("data: "), data...), '\n', '\n'))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_story/artifacts":
			writeCLITestJSON(w, http.StatusOK, preflightapi.ListArtifactsResponse{
				RunID: "run_story",
				Artifacts: []preflightapi.ArtifactSummary{{
					ID:        "artifact_story",
					RunID:     "run_story",
					Name:      "coverage/lcov.info",
					Path:      "coverage/lcov.info",
					SizeBytes: 42,
					ExpiresAt: fixed.Add(24 * time.Hour),
				}},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_story/artifacts/archive":
			_, _ = w.Write([]byte("artifact-bytes"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	output, err := captureStdoutForCLITest(t, func() error {
		return runsLatestCommand([]string{"--api-url", server.URL, "--token", "pfl_story", "--project", "proj_story", "--status", "succeeded"})
	})
	if err != nil {
		t.Fatalf("runs latest: %v", err)
	}
	requireCLITestOutputContains(t, output, "run_id: run_story", "status: succeeded", "command: go test ./...")

	output, err = captureStdoutForCLITest(t, func() error {
		return runsLatestCommand([]string{"--api-url", server.URL, "--token", "pfl_story", "--project", "proj_story", "--by-idempotency-key", "task-1", "--json"})
	})
	if err != nil {
		t.Fatalf("runs latest by idempotency key: %v", err)
	}
	var latest preflightapi.RunSummary
	if err := json.Unmarshal([]byte(output), &latest); err != nil {
		t.Fatalf("decode latest by idempotency: %v\n%s", err, output)
	}
	if latest.RunID != "run_story" || latest.Status != preflightapi.StatusSucceeded {
		t.Fatalf("latest by idempotency = %+v, want run_story succeeded", latest)
	}

	output, err = captureStdoutForCLITest(t, func() error {
		return runsLogsCommand([]string{"--api-url", server.URL, "--token", "pfl_story", "run_story"})
	})
	if err != nil {
		t.Fatalf("runs logs: %v", err)
	}
	requireCLITestOutputContains(t, output, "hello from run")

	output, err = captureStdoutForCLITest(t, func() error {
		return runsLogsCommand([]string{"--api-url", server.URL, "--token", "pfl_story", "--tail", "4", "run_story"})
	})
	if err != nil {
		t.Fatalf("runs logs human tail: %v", err)
	}
	requireCLITestOutputContains(t, output, "hello from run")
	if strings.Contains(output, `"events"`) || strings.Contains(output, "{") {
		t.Fatalf("human tail output looked like JSON:\n%s", output)
	}

	stderrOutput, err := captureStderrForCLITest(t, func() error {
		return runsLogsCommand([]string{"--api-url", server.URL, "--token", "pfl_story", "--errors-only", "run_story"})
	})
	if err != nil {
		t.Fatalf("runs logs human errors-only: %v", err)
	}
	requireCLITestOutputContains(t, stderrOutput, "TestStory", "readyci: command exited with code 1", "log limit reached")

	output, err = captureStdoutForCLITest(t, func() error {
		return runsLogsCommand([]string{"--api-url", server.URL, "--token", "pfl_story", "--jsonl", "--errors-only", "run_story"})
	})
	if err != nil {
		t.Fatalf("runs logs jsonl: %v", err)
	}
	requireCLITestOutputContains(t, output, "TestStory", "log_truncated")
	if strings.Contains(output, "hello from run") {
		t.Fatalf("jsonl errors-only output included stdout:\n%s", output)
	}

	output, err = captureStdoutForCLITest(t, func() error {
		return runsLogsCommand([]string{"--api-url", server.URL, "--token", "pfl_story", "--json", "--tail", "1", "run_story"})
	})
	if err != nil {
		t.Fatalf("runs logs json tail: %v", err)
	}
	var logsReport runLogsJSONReport
	if err := json.Unmarshal([]byte(output), &logsReport); err != nil {
		t.Fatalf("decode logs json: %v\n%s", err, output)
	}
	if logsReport.RunID != "run_story" || !logsReport.Truncated || len(logsReport.Events) != 1 || logsReport.Events[0].Event != preflightapi.EventLogTruncated {
		t.Fatalf("logs json report = %+v, want truncated tail log event", logsReport)
	}
	if logsReport.Events[0].EventID != 14 || logsReport.Events[0].Seq != 14 {
		t.Fatalf("logs json event identifiers = event_id %d seq %d, want stable id 14", logsReport.Events[0].EventID, logsReport.Events[0].Seq)
	}

	output, err = captureStdoutForCLITest(t, func() error {
		return runsLogsCommand([]string{"--api-url", server.URL, "--token", "pfl_story", "--json", "--tail", "2", "--include-lifecycle", "run_story"})
	})
	if err != nil {
		t.Fatalf("runs logs json lifecycle tail: %v", err)
	}
	if err := json.Unmarshal([]byte(output), &logsReport); err != nil {
		t.Fatalf("decode lifecycle logs json: %v\n%s", err, output)
	}
	if !logsReport.Lifecycle || len(logsReport.Events) != 2 || logsReport.Events[0].Event != preflightapi.EventCommandExited {
		t.Fatalf("lifecycle logs json report = %+v, want command_exited included", logsReport)
	}

	output, err = captureStdoutForCLITest(t, func() error {
		return runsLogsCommand([]string{"--api-url", server.URL, "--token", "pfl_story", "--json", "--phase", "services", "--step", "db", "--service", "postgres", "run_story"})
	})
	if err != nil {
		t.Fatalf("runs logs json phase/step/service filter: %v", err)
	}
	if err := json.Unmarshal([]byte(output), &logsReport); err != nil {
		t.Fatalf("decode phase/step/service logs json: %v\n%s", err, output)
	}
	if len(logsReport.Events) != 1 || logsReport.Events[0].Text != "postgres ready\n" || logsReport.Events[0].Phase != "services" || logsReport.Events[0].Step != "db" || logsReport.Events[0].Service != "postgres" {
		t.Fatalf("phase/step/service logs report = %+v, want postgres service log", logsReport)
	}
	if err := runsLogsCommand([]string{"--api-url", server.URL, "--token", "pfl_story", "--phase", "", "run_story"}); err == nil {
		t.Fatal("runs logs accepted empty --phase")
	}
	if err := runsLogsCommand([]string{"--api-url", server.URL, "--token", "pfl_story", "--step", "", "run_story"}); err == nil {
		t.Fatal("runs logs accepted empty --step")
	}

	output, err = captureStdoutForCLITest(t, func() error {
		return runsArtifactsCommand([]string{"--api-url", server.URL, "--token", "pfl_story", "run_story"})
	})
	if err != nil {
		t.Fatalf("runs artifacts: %v", err)
	}
	requireCLITestOutputContains(t, output, "artifacts for run run_story", "artifact_story coverage/lcov.info", "download: readyci runs artifacts --download run_story")

	output, err = captureStdoutForCLITest(t, func() error {
		return artifactsListCommand([]string{"--api-url", server.URL, "--token", "pfl_story", "run_story"})
	})
	if err != nil {
		t.Fatalf("artifacts ls: %v", err)
	}
	requireCLITestOutputContains(t, output, "artifacts for run run_story", "artifact_story coverage/lcov.info")

	outputPath := filepath.Join(t.TempDir(), "artifacts.tar.gz")
	output, err = captureStdoutForCLITest(t, func() error {
		return artifactsDownloadCommand([]string{"--api-url", server.URL, "--token", "pfl_story", "--output", outputPath, "run_story"})
	})
	if err != nil {
		t.Fatalf("artifacts download: %v", err)
	}
	requireCLITestOutputContains(t, output, "downloaded artifacts to "+outputPath)
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read artifact download: %v", err)
	}
	if string(data) != "artifact-bytes" {
		t.Fatalf("artifact download = %q, want archive bytes", data)
	}
}

func TestArtifactsApplyCommandDownloadsAndAppliesFilteredPaths(t *testing.T) {
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "tests", "fixtures"), 0o755); err != nil {
		t.Fatalf("mkdir fixtures: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(source, "docs", "calibration-artifacts"), 0o755); err != nil {
		t.Fatalf("mkdir docs artifacts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "tests", "fixtures", "summary.json"), []byte("remote fixture"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "docs", "calibration-artifacts", "report.md"), []byte("remote report"), 0o644); err != nil {
		t.Fatalf("write report: %v", err)
	}
	var archive bytes.Buffer
	buildArtifactArchiveForTest(t, source, []string{"tests/fixtures/**", "docs/calibration-artifacts/**"}, &archive)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_apply/artifacts/archive" {
			_, _ = w.Write(archive.Bytes())
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	cliTestWorkingDirectoryMu.Lock()
	defer cliTestWorkingDirectoryMu.Unlock()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "tests", "fixtures"), 0o755); err != nil {
		t.Fatalf("mkdir repo fixtures: %v", err)
	}
	fixturePath := filepath.Join(repo, "tests", "fixtures", "summary.json")
	if err := os.WriteFile(fixturePath, []byte("local fixture"), 0o644); err != nil {
		t.Fatalf("write local fixture: %v", err)
	}
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}
	runGitForCLITest(t, "init")
	nestedDir := filepath.Join(repo, "packages", "app")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatalf("mkdir nested dir: %v", err)
	}
	if err := os.Chdir(nestedDir); err != nil {
		t.Fatalf("chdir nested dir: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	}()

	output, err := captureStdoutForCLITest(t, func() error {
		return artifactsApplyCommand([]string{"run_apply", "--api-url", server.URL, "--token", "pfl_apply", "--paths", "tests/fixtures/**"})
	})
	if err != nil {
		t.Fatalf("artifacts apply: %v", err)
	}
	requireCLITestOutputContains(t, output, "applied 1 artifact file(s) from run run_apply", "tests/fixtures/summary.json size_bytes=14")
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read applied fixture: %v", err)
	}
	if string(data) != "remote fixture" {
		t.Fatalf("fixture content = %q, want remote fixture", data)
	}
	if _, err := os.Stat(filepath.Join(repo, "docs", "calibration-artifacts", "report.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unmatched report stat err = %v, want not exists", err)
	}

	if err := os.WriteFile(fixturePath, []byte("local fixture"), 0o644); err != nil {
		t.Fatalf("restore local fixture: %v", err)
	}
	output, err = captureStdoutForCLITest(t, func() error {
		return artifactsApplyCommand([]string{"run_apply", "--api-url=" + server.URL, "--token=pfl_apply", "--paths=tests/fixtures/**", "--dry-run"})
	})
	if err != nil {
		t.Fatalf("artifacts apply dry-run: %v", err)
	}
	requireCLITestOutputContains(t, output, "would apply 1 artifact file(s) from run run_apply")
	data, err = os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read dry-run fixture: %v", err)
	}
	if string(data) != "local fixture" {
		t.Fatalf("dry-run fixture content = %q, want local fixture", data)
	}

	jsonOutput, err := captureStdoutForCLITest(t, func() error {
		return artifactsApplyCommand([]string{"run_apply", "--api-url=" + server.URL, "--token=pfl_apply", "--paths=tests/fixtures/**", "--dry-run", "--json"})
	})
	if err != nil {
		t.Fatalf("artifacts apply json dry-run: %v", err)
	}
	var report artifactApplyResult
	if err := json.Unmarshal([]byte(jsonOutput), &report); err != nil {
		t.Fatalf("decode artifacts apply json: %v\n%s", err, jsonOutput)
	}
	if report.RunID != "run_apply" || !report.DryRun || report.MatchedCount != 1 || report.AppliedCount != 0 || len(report.Files) != 1 || report.Files[0].Path != "tests/fixtures/summary.json" {
		t.Fatalf("artifacts apply json report = %+v, want one dry-run fixture match", report)
	}

	emptyOutput, err := captureStdoutForCLITest(t, func() error {
		return artifactsApplyCommand([]string{"run_apply", "--api-url=" + server.URL, "--token=pfl_apply", "--paths=tests/missing/**", "--dry-run"})
	})
	if err == nil || !strings.Contains(err.Error(), "no artifact files matched --paths") {
		t.Fatalf("artifacts apply empty error = %v, output=%s; want no-match error", err, emptyOutput)
	}
	if strings.TrimSpace(emptyOutput) != "" {
		t.Fatalf("artifacts apply empty output = %q, want no stdout on error", emptyOutput)
	}

	emptyOutput, err = captureStdoutForCLITest(t, func() error {
		return artifactsApplyCommand([]string{"run_apply", "--api-url=" + server.URL, "--token=pfl_apply", "--paths=tests/missing/**", "--dry-run", "--allow-empty"})
	})
	if err != nil {
		t.Fatalf("artifacts apply allow-empty: %v", err)
	}
	requireCLITestOutputContains(t, emptyOutput, "would apply 0 artifact files from run run_apply")

	emptyJSONOutput, err := captureStdoutForCLITest(t, func() error {
		return artifactsApplyCommand([]string{"run_apply", "--api-url=" + server.URL, "--token=pfl_apply", "--paths=tests/missing/**", "--dry-run", "--allow-empty", "--json"})
	})
	if err != nil {
		t.Fatalf("artifacts apply allow-empty json: %v", err)
	}
	var emptyReport artifactApplyResult
	if err := json.Unmarshal([]byte(emptyJSONOutput), &emptyReport); err != nil {
		t.Fatalf("decode empty artifacts apply json: %v\n%s", err, emptyJSONOutput)
	}
	if emptyReport.MatchedCount != 0 || emptyReport.AppliedCount != 0 || len(emptyReport.Files) != 0 {
		t.Fatalf("empty artifacts apply json report = %+v, want zero counts and files", emptyReport)
	}
	requireCLITestOutputContains(t, emptyJSONOutput, `"files": []`)
}
