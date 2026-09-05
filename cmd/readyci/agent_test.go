package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestAgentDoesNotRetryWhenEventStreamEndsBeforeTerminalEvent(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("stream retry\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")

	var serverURL string
	createRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs":
			createRequests++
			runID := fmt.Sprintf("run_%d", createRequests)
			writeCLITestJSON(w, http.StatusCreated, preflightapi.CreateRunResponse{
				RunID:     runID,
				UploadURL: fmt.Sprintf("%s/upload/%d", serverURL, createRequests),
				EventsURL: fmt.Sprintf("%s/events/%d", serverURL, createRequests),
				Status:    preflightapi.StatusAwaitingUpload,
			})
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/upload/"):
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte("{}\n"))
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/runs/run_") && strings.HasSuffix(r.URL.Path, "/upload-complete"):
			runID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/runs/"), "/upload-complete")
			writeCLITestJSON(w, http.StatusOK, preflightapi.CompleteUploadResponse{RunID: runID, Status: preflightapi.StatusQueued})
		case r.Method == http.MethodGet && r.URL.Path == "/events/1":
			event := preflightapi.RunEvent{Type: preflightapi.EventStdout, Data: "partial output before disconnect\n"}
			data, _ := json.Marshal(event)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write(append(append([]byte("data: "), data...), '\n', '\n'))
		case r.Method == http.MethodGet && r.URL.Path == "/events/2":
			exitCode := 0
			event := preflightapi.RunEvent{Type: preflightapi.EventRunFinished, Status: preflightapi.StatusSucceeded, ExitCode: &exitCode}
			data, _ := json.Marshal(event)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write(append(append([]byte("data: "), data...), '\n', '\n'))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	result, err := executeAgentRunWithRetries(remoteRunOptions{
		APIURL:         server.URL,
		ProjectID:      "proj_dev",
		IdempotencyKey: "stream-task",
		Command:        "go test ./...",
		CacheMode:      "cache-only",
		NetworkMode:    "default",
	}, 1)
	if !errors.Is(err, errRunStreamEndedBeforeTerminal) {
		t.Fatalf("agent error = %v, want unknown terminal state", err)
	}
	if createRequests != 1 {
		t.Fatalf("create requests = %d, want no replacement run", createRequests)
	}
	if result.RunID != "run_1" || result.IdempotencyKey != "stream-task" || result.Status != preflightapi.StatusQueued {
		t.Fatalf("unknown result = %+v, want original queued run", result)
	}
}
