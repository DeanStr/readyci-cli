package main

import (
	"encoding/json"
	"errors"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newSuccessfulRunCommandServer(t *testing.T, created *preflightapi.CreateRunRequest, createBody *[]byte) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(successfulExecutionTestHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read create request: %v", err)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if createBody != nil {
				*createBody = body
			}
			if err := json.Unmarshal(body, created); err != nil {
				t.Errorf("decode create request: %v", err)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeCLITestJSON(w, http.StatusCreated, preflightapi.CreateRunResponse{
				RunID:     "run_test",
				UploadURL: server.URL + "/upload",
				EventsURL: server.URL + "/events",
				Status:    preflightapi.StatusAwaitingUpload,
			})
		case r.Method == http.MethodPut && r.URL.Path == "/upload":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte("{}\n"))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs/run_test/upload-complete":
			writeCLITestJSON(w, http.StatusOK, preflightapi.CompleteUploadResponse{RunID: "run_test", Status: preflightapi.StatusQueued})
		case r.Method == http.MethodGet && r.URL.Path == "/events":
			exitCode := 0
			event := preflightapi.RunEvent{Type: preflightapi.EventRunFinished, ExitCode: &exitCode}
			data, _ := json.Marshal(event)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: run_finished\n"))
			_, _ = w.Write(append(append([]byte("data: "), data...), '\n', '\n'))
		default:
			http.NotFound(w, r)
		}
	})))
	t.Cleanup(server.Close)
	return server
}

func TestValidateNetworkMode(t *testing.T) {
	for _, mode := range []string{"none", "default", "allowlist"} {
		if err := validateNetworkMode(mode); err != nil {
			t.Fatalf("validateNetworkMode(%q): %v", mode, err)
		}
	}
	if err := validateNetworkMode("host"); err == nil {
		t.Fatal("validateNetworkMode accepted invalid mode")
	}
}

func TestRunCommandRunnerSizeAndTimeoutFlagsOverrideConfig(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("runner size test\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
runner_size: small
timeout: 25m
resources:
  default:
    size: large
    timeout: 30m
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")

	var created preflightapi.CreateRunRequest
	var createBody []byte
	server := newSuccessfulRunCommandServer(t, &created, &createBody)

	err = runCommand([]string{"--api-url", server.URL, "--runner-size", "xlarge", "--timeout", "90m", "true"})
	var exitErr exitCodeError
	if err != nil && (!errors.As(err, &exitErr) || exitErr.code != 0) {
		t.Fatalf("runCommand error = %v, want exit code 0", err)
	}
	if created.RunnerSize != "xlarge" {
		t.Fatalf("create request runner size = %q, want xlarge", created.RunnerSize)
	}
	if created.TimeoutSeconds != 5400 {
		t.Fatalf("create request timeout seconds = %d, want 5400", created.TimeoutSeconds)
	}
	if strings.Contains(string(createBody), `"region"`) {
		t.Fatalf("create request sent region despite no CLI/config region: %s", createBody)
	}
}

func TestCreateRunRequestRegionOnlyDefaultsWhenExplicit(t *testing.T) {
	tests := []struct {
		name   string
		region string
		want   string
	}{
		{name: "omitted", region: "", want: ""},
		{name: "whitespace", region: "  ", want: ""},
		{name: "explicit default", region: "default", want: "default"},
		{name: "normalizes explicit", region: " US-East_1 ", want: "us-east_1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := createRunRequestRegion(tc.region); got != tc.want {
				t.Fatalf("createRunRequestRegion(%q) = %q, want %q", tc.region, got, tc.want)
			}
		})
	}
}

func TestRunCommandRejectsInvalidRunnerSizeFlag(t *testing.T) {
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

	err = runCommand([]string{"--api-url", "http://127.0.0.1:1", "--runner-size", "huge", "true"})
	if err == nil || !strings.Contains(err.Error(), `unsupported runner_size "huge"`) {
		t.Fatalf("runCommand invalid runner size error = %v", err)
	}
}

func TestRunCommandRejectsInvalidTimeoutFlag(t *testing.T) {
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

	err = runCommand([]string{"--api-url", "http://127.0.0.1:1", "--timeout", "not-a-duration", "true"})
	if err == nil || !strings.Contains(err.Error(), `parse timeout "not-a-duration"`) {
		t.Fatalf("runCommand invalid timeout error = %v", err)
	}
}

func TestRunCommandDefaultsDockerNetworkToNone(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("docker test\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")

	var created preflightapi.CreateRunRequest
	server := newSuccessfulRunCommandServer(t, &created, nil)

	err = runCommand([]string{"--api-url", server.URL, "--docker", "docker version"})
	var exitErr exitCodeError
	if err != nil && (!errors.As(err, &exitErr) || exitErr.code != 0) {
		t.Fatalf("runCommand error = %v, want exit code 0", err)
	}
	if !created.Docker {
		t.Fatalf("create request Docker = false, want true: %+v", created)
	}
	if created.CacheMode != "cache-only" {
		t.Fatalf("create request cache mode = %q, want cache-only", created.CacheMode)
	}
	if created.NetworkMode != "none" {
		t.Fatalf("create request network mode = %q, want none", created.NetworkMode)
	}
}

func TestRunCommandDefaultsConfigDockerNetworkToNone(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("docker config test\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
resources:
  default:
    docker: true
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")

	var created preflightapi.CreateRunRequest
	server := newSuccessfulRunCommandServer(t, &created, nil)

	err = runCommand([]string{"--api-url", server.URL, "docker version"})
	var exitErr exitCodeError
	if err != nil && (!errors.As(err, &exitErr) || exitErr.code != 0) {
		t.Fatalf("runCommand error = %v, want exit code 0", err)
	}
	if !created.Docker {
		t.Fatalf("create request Docker = false, want true: %+v", created)
	}
	if created.NetworkMode != "none" {
		t.Fatalf("create request network mode = %q, want none", created.NetworkMode)
	}
}

func TestRunCommandCanDisableProjectConcurrency(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("concurrency test\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
concurrency_group: project-db
concurrency_limit: 3
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")

	var created preflightapi.CreateRunRequest
	server := newSuccessfulRunCommandServer(t, &created, nil)

	err = runCommand([]string{"--api-url", server.URL, "--concurrency-limit", "0", "true"})
	var exitErr exitCodeError
	if err != nil && (!errors.As(err, &exitErr) || exitErr.code != 0) {
		t.Fatalf("runCommand error = %v, want exit code 0", err)
	}
	if created.ConcurrencyGroup != "" || created.ConcurrencyLimit != 0 {
		t.Fatalf("create concurrency = %q/%d, want disabled", created.ConcurrencyGroup, created.ConcurrencyLimit)
	}
}

func TestRunCommandDoesNotInheritAllowlistWhenNetworkModeOverridden(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("allowlist override test\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
network:
  mode: allowlist
  allowlist:
    - api.github.com
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")

	var created preflightapi.CreateRunRequest
	server := newSuccessfulRunCommandServer(t, &created, nil)

	err = runCommand([]string{"--api-url", server.URL, "--network-mode", "none", "true"})
	var exitErr exitCodeError
	if err != nil && (!errors.As(err, &exitErr) || exitErr.code != 0) {
		t.Fatalf("runCommand error = %v, want exit code 0", err)
	}
	if created.NetworkMode != "none" {
		t.Fatalf("network mode = %q, want none", created.NetworkMode)
	}
	if len(created.NetworkAllowlist) != 0 {
		t.Fatalf("network allowlist = %v, want empty", created.NetworkAllowlist)
	}
}

func TestEffectiveRunNetworkModeHandlesDockerModes(t *testing.T) {
	if got, err := effectiveRunNetworkMode("default", true, false); err != nil || got != "none" {
		t.Fatalf("implicit docker network mode = %q, %v; want none, nil", got, err)
	}
	if got, err := effectiveRunNetworkMode("default", true, true); err != nil || got != "default" {
		t.Fatalf("explicit default docker network mode = %q, %v; want default, nil", got, err)
	}
	if got, err := effectiveRunNetworkMode("allowlist", true, true); err != nil || got != "allowlist" {
		t.Fatalf("explicit allowlist docker network mode = %q, %v; want allowlist, nil", got, err)
	}
	if got, err := effectiveRunNetworkMode("default", false, false); err != nil || got != "default" {
		t.Fatalf("non-docker network mode = %q, %v; want default, nil", got, err)
	}
}

func TestValidateNetworkAllowlistUse(t *testing.T) {
	if err := validateNetworkAllowlistUse("allowlist", []string{"api.github.com"}); err != nil {
		t.Fatalf("allowlist validation rejected explicit host: %v", err)
	}
	if err := validateNetworkAllowlistUse("allowlist", nil); err == nil {
		t.Fatal("allowlist validation accepted missing allowlist")
	}
	if err := validateNetworkAllowlistUse("default", []string{"api.github.com"}); err == nil {
		t.Fatal("allowlist validation accepted hosts outside allowlist mode")
	}
	if err := validateNetworkAllowlistUse("none", nil); err != nil {
		t.Fatalf("allowlist validation rejected none without hosts: %v", err)
	}
	if err := validateNetworkAllowlistUse("allowlist", []string{"10.0.0.1"}); err == nil {
		t.Fatal("allowlist validation accepted private IPv4")
	}
	for _, entry := range []string{"100.64.0.1", "192.0.2.1", "198.18.0.1"} {
		if err := validateNetworkAllowlistUse("allowlist", []string{entry}); err != nil {
			t.Fatalf("allowlist validation rejected platform-owned IPv4 policy case %s: %v", entry, err)
		}
	}
	if err := validateNetworkAllowlistUse("allowlist", []string{"*.example.com"}); err == nil {
		t.Fatal("allowlist validation accepted wildcard host")
	}
	if err := validateNetworkAllowlistUse("allowlist", []string{"93.184.216.0/24"}); err == nil {
		t.Fatal("allowlist validation accepted CIDR")
	}
}
