package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestSyncCommandRequiresDryRun(t *testing.T) {
	if err := syncCommand(nil); err == nil || !strings.Contains(err.Error(), "sync --dry-run") {
		t.Fatalf("syncCommand without dry-run error = %v, want usage", err)
	}
}

func TestWarmCommandDryRunPreviewsWarmAndConfirmRuns(t *testing.T) {
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
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	if err := os.WriteFile("tracked.txt", []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write tracked: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.WriteFile(".readyci.yml", []byte(`
network:
  mode: none
  warm_mode: allowlist
  warm_allowlist: [packages.example.com]
checks:
  ci:
    command: make ci
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	output, err := captureStdoutForCLITest(t, func() error {
		return warmCommand([]string{"--dry-run", "--check", "ci"})
	})
	if err != nil {
		t.Fatalf("warm dry-run: %v\n%s", err, output)
	}
	if strings.Count(output, "check: dry-run") != 2 || !strings.Contains(output, "network_mode: allowlist") || !strings.Contains(output, "network_allowlist: packages.example.com") || !strings.Contains(output, "network_mode: none") {
		t.Fatalf("warm dry-run output = %s, want warm and confirm previews", output)
	}
}

func TestWarmCommandFailsFastWithoutExplicitWarmNetworkProfile(t *testing.T) {
	cliTestWorkingDirectoryMu.Lock()
	defer cliTestWorkingDirectoryMu.Unlock()

	repo := t.TempDir()
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(previousDir) }()
	if err := os.WriteFile(".readyci.yml", []byte("network:\n  mode: none\nchecks:\n  ci:\n    command: make ci\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = warmCommand([]string{"--dry-run", "--check", "ci"})
	if err == nil || !strings.Contains(err.Error(), "explicit network.warm_mode") {
		t.Fatalf("warm without profile error = %v, want fail-fast configuration error", err)
	}
}

func TestWarmProfileDependenciesSelectsWarmableChecks(t *testing.T) {
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
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	if err := os.WriteFile("tracked.txt", []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write tracked: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.WriteFile(".readyci.yml", []byte(`
network:
  mode: none
  warm_mode: default
cache:
  paths:
    - node_modules
checks:
  dependencies:
    command: go mod download
  unit:
    command: make unit
  ci:
    command: make check-ci
  web:
    command: make web
    prepare:
      - scripts/bootstrap-web-deps.sh
  db:
    command: make db
    docker: true
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	output, err := captureStdoutForCLITest(t, func() error {
		return warmCommand([]string{"--dry-run", "--profile", "dependencies"})
	})
	if err != nil {
		t.Fatalf("warm profile dependencies dry-run: %v\n%s", err, output)
	}
	for _, unwanted := range []string{"name: unit", "name: ci", "name: web", "name: db"} {
		if strings.Contains(output, unwanted) {
			t.Fatalf("warm profile selected %q instead of only dependencies:\n%s", unwanted, output)
		}
	}
	for _, want := range []string{"name: dependencies", "network_mode: default", "network_mode: none"} {
		if !strings.Contains(output, want) {
			t.Fatalf("warm profile output missing %q:\n%s", want, output)
		}
	}
}

func TestWarmCommandContinuesAfterSuccessfulWarmRun(t *testing.T) {
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
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	if err := os.WriteFile("tracked.txt", []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write tracked: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.WriteFile(".readyci.yml", []byte(`
network:
  mode: none
  warm_mode: allowlist
  warm_allowlist: [packages.example.com]
checks:
  ci:
    command: make ci
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var serverURL string
	var created []preflightapi.CreateRunRequest
	server := httptest.NewServer(successfulExecutionTestHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs":
			var req preflightapi.CreateRunRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode create request: %v", err)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			created = append(created, req)
			runID := fmt.Sprintf("run_warm_%d", len(created))
			writeCLITestJSON(w, http.StatusCreated, preflightapi.CreateRunResponse{
				RunID:     runID,
				UploadURL: serverURL + "/upload/" + runID,
				EventsURL: serverURL + "/events/" + runID,
				Status:    preflightapi.StatusAwaitingUpload,
			})
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/upload/"):
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte("{}\n"))
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/runs/") && strings.HasSuffix(r.URL.Path, "/upload-complete"):
			runID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/runs/"), "/upload-complete")
			writeCLITestJSON(w, http.StatusOK, preflightapi.CompleteUploadResponse{RunID: runID, Status: preflightapi.StatusQueued})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/events/"):
			writeCLITestFinishedEvent(w)
		default:
			http.NotFound(w, r)
		}
	})))
	defer server.Close()
	serverURL = server.URL

	err = warmCommand([]string{"--api-url", server.URL, "--token", "token_warm", "--check", "ci"})
	var exitErr exitCodeError
	if err != nil && (!errors.As(err, &exitErr) || exitErr.code != 0) {
		t.Fatalf("warmCommand error = %v, want successful exit sentinel", err)
	}
	if len(created) != 2 {
		t.Fatalf("created runs = %d, want warm and confirm", len(created))
	}
	if created[0].NetworkMode != "allowlist" || len(created[0].NetworkAllowlist) != 1 || created[0].NetworkAllowlist[0] != "packages.example.com" {
		t.Fatalf("warm network = %q %v, want explicit allowlist profile", created[0].NetworkMode, created[0].NetworkAllowlist)
	}
	if created[1].NetworkMode != "none" {
		t.Fatalf("confirm network mode = %q, want configured none", created[1].NetworkMode)
	}
}
