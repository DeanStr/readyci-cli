package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/internal/syncbundlebuild"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

// Stop after creation so these probes inspect the actual public-command request
// without simulating the session scheduler or running commands locally.
func captureExecutionRequests(t *testing.T, requests *[]preflightapi.CreateVerificationSessionRequest) *httptest.Server {
	t.Helper()
	t.Setenv("READYCI_API_TOKEN", "pfl_execution_review")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/verification-sessions" {
			var req preflightapi.CreateVerificationSessionRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
			}
			*requests = append(*requests, req)
		}
		http.Error(w, "creation stopped by review fixture", http.StatusForbidden)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestExplicitExecutionCheckRetainsRerunSelection(t *testing.T) {
	repo := chdirCLITestRepo(t, "execution-recipe")
	runGitForCLITest(t, "init")
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte("project_id: review\nchecks:\n  test:\n    command: echo default\n  integration:\n    command: echo integration\n"), 0644); err != nil {
		t.Fatal(err)
	}
	var requests []preflightapi.CreateVerificationSessionRequest
	server := captureExecutionRequests(t, &requests)
	if err := checkCommand([]string{"--api-url", server.URL, "integration"}); err == nil {
		t.Fatal("fixture should stop creation")
	}
	if len(requests) != 1 || requests[0].Recipe.Check != "integration" {
		t.Fatalf("explicit check lost its rerun selection: %+v", requests)
	}
	prepared, err := prepareExecutionRerun(preflightapi.VerificationSession{ProjectID: "review", Recipe: requests[0].Recipe}, server.URL)
	if err != nil || len(prepared.Plans) != 1 || prepared.Plans[0].Name != "integration" {
		t.Fatalf("rerun selected another check: %+v, %v", prepared, err)
	}
}

func TestExecutionIdempotencyDoesNotDependOnLatestSourceCache(t *testing.T) {
	repo := chdirCLITestRepo(t, "execution-idempotency")
	runGitForCLITest(t, "init")
	runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("baseline"), 0644); err != nil {
		t.Fatal(err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "baseline")
	var requests []preflightapi.CreateVerificationSessionRequest
	server := captureExecutionRequests(t, &requests)
	build := syncbundlebuild.BuildOptions{ChangeMode: "working"}
	manifest, err := syncbundlebuild.SnapshotWithOptions(context.Background(), ".", build)
	if err != nil {
		t.Fatal(err)
	}
	for _, baseline := range []string{"group_first", "group_second"} {
		rememberExecutionSource(server.URL, "review", manifest.BaselineKey, preflightapi.VerificationSession{
			SessionID: baseline, State: preflightapi.VerificationStateTerminal,
			Nodes: []preflightapi.VerificationNode{{Attempts: []preflightapi.VerificationAttempt{{RunID: "run_baseline"}}}},
		})
		_, err := createAndWatchVerificationSession(verificationSessionOptions{
			APIURL: server.URL, APIToken: "pfl_execution_review", ProjectID: "review", Purpose: preflightapi.ExecutionPurpose,
			Command: "true", IdempotencyKey: "repeatable-request", Changes: "working",
			Prepared: &preparedVerificationSession{ProjectID: "review", BuildOptions: build, CoverageMode: "check",
				Plans: []checkRunPlan{{Name: "command", Options: remoteRunOptions{Command: "true", ChangeMode: "working"}}}},
		})
		if err == nil {
			t.Fatal("fixture should stop creation")
		}
	}
	if len(requests) != 2 {
		t.Fatalf("expected two captured requests, got %d", len(requests))
	}
	if !reflect.DeepEqual(requests[0], requests[1]) {
		t.Fatalf("same idempotency key and workspace changed request with source cache: %q -> %q", requests[0].Recipe.Source.UploadBaselineSessionID, requests[1].Recipe.Source.UploadBaselineSessionID)
	}
}

func TestExecutionDryRunDisclosesAutomaticDependencyNetwork(t *testing.T) {
	chdirCLITestRepo(t, "execution-preview")
	runGitForCLITest(t, "init")
	if err := os.WriteFile(".readyci.yml", []byte(`project_id: review
sync:
  include_untracked: true
network:
  mode: none
  warm_mode: allowlist
  warm_allowlist: [registry.npmjs.org]
checks:
  dependencies:
    command: npm ci
  test:
    command: npm test
affected:
  rules:
    - paths: ["**"]
      checks: [test]
`), 0644); err != nil {
		t.Fatal(err)
	}
	var requests []preflightapi.CreateVerificationSessionRequest
	server := captureExecutionRequests(t, &requests)
	for _, command := range []string{"check", "affected"} {
		t.Run(command, func(t *testing.T) {
			output, err := captureStdoutForCLITest(t, func() error {
				args := []string{command, "--api-url", server.URL, "--dry-run", "--json"}
				if command == "check" {
					args = append(args, "test")
				}
				return run(args)
			})
			if err != nil {
				t.Fatal(err)
			}
			var report struct {
				Warnings []string `json:"warnings"`
			}
			if err := json.Unmarshal([]byte(output), &report); err != nil {
				t.Fatal(err)
			}
			warnings := strings.Join(report.Warnings, "\n")
			for _, want := range []string{"dependency preparation", "npm ci", "allowlist", "registry.npmjs.org"} {
				if !strings.Contains(warnings, want) {
					t.Errorf("preview omitted %q: %s", want, output)
				}
			}
		})
	}
	if len(requests) != 0 {
		t.Fatal("dry-run submitted execution")
	}
}
