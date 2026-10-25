package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func setupInferredScopeRepo(t *testing.T, changed bool) {
	t.Helper()
	repo := setupCleanInferredGoRepo(t)
	writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/ci.yml"), "jobs:\n  unit:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test ./...\n  integration:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test -tags integration ./...\n")
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "upstream checks")
	if changed {
		writeFileForRepoMapTest(t, filepath.Join(repo, "main.go"), "package magic\n\nfunc Changed() {}\n")
	}
}

func requireInferredScopeWarning(t *testing.T, warnings string) {
	t.Helper()
	for _, want := range []string{"upstream jobs not imported or covered", "github-actions:.github/workflows/ci.yml:unit", "github-actions:.github/workflows/ci.yml:integration"} {
		if !strings.Contains(warnings, want) {
			t.Fatalf("scope warning lost %q: %s", want, warnings)
		}
	}
}

func TestInferredScopeWarningsSurvivePreviewAndPlanning(t *testing.T) {
	for _, tc := range []struct {
		name    string
		changed bool
		args    []string
	}{
		{"affected check", true, []string{"check", "--dry-run", "--json"}},
		{"fallback check", false, []string{"check", "--dry-run", "--json"}},
		{"explicit check", true, []string{"check", "--dry-run", "--json", "go"}},
		{"affected command", true, []string{"affected", "--dry-run", "--json"}},
		{"agent affected plan", true, []string{"agent", "plan", "--json"}},
		{"agent explicit plan", true, []string{"agent", "plan", "--json", "--check", "go"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupInferredScopeRepo(t, tc.changed)
			output, err := captureStdoutForCLITest(t, func() error { return run(tc.args) })
			if err != nil {
				t.Fatal(err)
			}
			var report struct {
				Warnings json.RawMessage `json:"warnings"`
			}
			if err := json.Unmarshal([]byte(output), &report); err != nil {
				t.Fatalf("decode preview: %v\n%s", err, output)
			}
			requireInferredScopeWarning(t, string(report.Warnings))
		})
	}
	t.Run("human summary", func(t *testing.T) {
		setupInferredScopeRepo(t, true)
		output, err := captureStdoutForCLITest(t, func() error { return checkCommand([]string{"--dry-run", "--summary"}) })
		if err != nil {
			t.Fatal(err)
		}
		requireInferredScopeWarning(t, output)
	})
}

func TestInferredScopeWarningsSurviveSuccessfulExecution(t *testing.T) {
	for _, changed := range []bool{true, false} {
		t.Run(map[bool]string{true: "affected", false: "fallback"}[changed], func(t *testing.T) {
			setupInferredScopeRepo(t, changed)
			var apiURL string
			server := httptest.NewServer(successfulExecutionTestHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/v1/runs":
					writeCLITestJSON(w, http.StatusCreated, preflightapi.CreateRunResponse{RunID: "run_scope", UploadURL: apiURL + "/upload/run_scope", Status: preflightapi.StatusAwaitingUpload})
				case r.Method == http.MethodPut:
					w.WriteHeader(http.StatusAccepted)
				default:
					http.NotFound(w, r)
				}
			})))
			defer server.Close()
			apiURL = server.URL
			var output string
			stderr, err := captureStderrForCLITest(t, func() error {
				var runErr error
				output, runErr = captureStdoutForCLITest(t, func() error {
					return checkCommand([]string{"--api-url", apiURL, "--project", "scope", "--skip-warm", "--json"})
				})
				return runErr
			})
			if err != nil {
				t.Fatalf("execution: %v\n%s\n%s", err, output, stderr)
			}
			var report struct {
				Session preflightapi.VerificationSession `json:"session"`
				Plan    struct {
					Warnings []string `json:"warnings"`
				} `json:"plan"`
			}
			if err := json.Unmarshal([]byte(output), &report); err != nil {
				t.Fatal(err)
			}
			if report.Session.Verdict != preflightapi.VerificationVerdictChecksPassed {
				t.Fatalf("unexpected execution: %s", output)
			}
			requireInferredScopeWarning(t, strings.Join(report.Plan.Warnings, "\n"))
			requireInferredScopeWarning(t, stderr)
		})
	}
}
