package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/internal/syncbundlebuild"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestExecutionFailureOutputRedactsSummaryCredentials(t *testing.T) {
	chdirCLITestRepo(t, "failure-summary-redaction")
	runGitForCLITest(t, "init")
	const token = "pfl_summary_fixture_token"
	const summary = "download failed: https://summary-user:summary-password@example.invalid/path?token=summary-query Bearer summary-bearer; " + token
	var apiURL string
	child := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs":
			writeCLITestJSON(w, 201, preflightapi.CreateRunResponse{RunID: "run_failure", UploadURL: apiURL + "/upload", Status: preflightapi.StatusAwaitingUpload})
		case r.Method == http.MethodPut:
			w.WriteHeader(202)
		case r.URL.Path == "/v1/runs/run_failure":
			writeCLITestJSON(w, 200, preflightapi.RunDetail{Summary: preflightapi.RunSummary{RunID: "run_failure", Status: preflightapi.StatusFailed, FailureSummary: summary}})
		default:
			http.NotFound(w, r)
		}
	})
	fixture := successfulExecutionTestHandler(t, child)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/runs/run_failure/events" {
			writeCLITestJSON(w, 200, preflightapi.RunEventsResponse{RunID: "run_failure", NextCursor: 1, Events: []preflightapi.RunEvent{{ID: 1, Type: preflightapi.EventStderr, Data: summary + "\n"}}})
			return
		}
		if (r.Method == http.MethodGet || strings.HasSuffix(r.URL.Path, "/stale")) && strings.HasPrefix(r.URL.Path, "/v1/verification-sessions/") {
			recorder := httptest.NewRecorder()
			fixture.ServeHTTP(recorder, r)
			var session preflightapi.VerificationSession
			if err := json.Unmarshal(recorder.Body.Bytes(), &session); err != nil {
				t.Error(err)
				http.Error(w, "bad fixture", 500)
				return
			}
			session.Verdict = preflightapi.VerificationVerdictFailed
			for i := range session.Nodes {
				session.Nodes[i].Required = true
				session.Nodes[i].Status = preflightapi.StatusFailed
				for j := range session.Nodes[i].Attempts {
					session.Nodes[i].Attempts[j].Status = preflightapi.StatusFailed
					code := 1
					session.Nodes[i].Attempts[j].ExitCode = &code
				}
			}
			writeCLITestJSON(w, 200, session)
			return
		}
		fixture.ServeHTTP(w, r)
	}))
	defer server.Close()
	apiURL = server.URL
	var stdout string
	stderr, err := captureStderrForCLITest(t, func() error {
		var runErr error
		stdout, runErr = captureStdoutForCLITest(t, func() error {
			return runCommand([]string{"--api-url", apiURL, "--project", "review", "--token", token, "false"})
		})
		return runErr
	})
	if err == nil {
		t.Fatal("failed execution returned success")
	}
	for _, secret := range []string{"summary-user", "summary-password", "summary-query", "summary-bearer", token} {
		if strings.Contains(stdout+stderr, secret) {
			t.Errorf("failure output exposed %q", secret)
		}
	}
	if !strings.Contains(stderr, "readyci: download failed: https://[REDACTED]@example.invalid/path?token=[REDACTED] Bearer [REDACTED]; [REDACTED]") {
		t.Errorf("missing redacted final summary: %s", stderr)
	}
}

func TestExecutionRerunPreservesRetryResolution(t *testing.T) {
	for _, test := range []struct {
		name, command, check      string
		flags                     []string
		attempts, changedAttempts int
	}{
		{name: "literal default", command: "run", check: "true", attempts: 1, changedAttempts: 1},
		{name: "literal explicit zero", command: "run", check: "true", flags: []string{"--retry", "0"}, attempts: 1, changedAttempts: 1},
		{name: "literal explicit retries", command: "run", check: "true", flags: []string{"--retry", "3"}, attempts: 4, changedAttempts: 4},
		{name: "named project default", command: "check", check: "test", attempts: 3, changedAttempts: 5},
		{name: "named check default", command: "check", check: "own", attempts: 2, changedAttempts: 2},
		{name: "named explicit retries", command: "check", check: "test", flags: []string{"--retry", "0"}, attempts: 1, changedAttempts: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			chdirCLITestRepo(t, "rerun-retries")
			runGitForCLITest(t, "init")
			writeConfig := func(retries int) {
				t.Helper()
				if err := os.WriteFile(".readyci.yml", []byte(fmt.Sprintf("version: 1\nproject_id: review\nretries: %d\nchecks:\n  test:\n    command: true\n  own:\n    command: true\n    retries: 1\n", retries)), 0600); err != nil {
					t.Fatal(err)
				}
			}
			writeConfig(2)
			var requests []preflightapi.CreateVerificationSessionRequest
			server := captureExecutionRequests(t, &requests)
			args := append([]string{test.command, "--api-url", server.URL}, test.flags...)
			args = append(args, test.check)
			if err := run(args); err == nil {
				t.Fatal("fixture did not stop submission")
			}
			if len(requests) != 1 {
				t.Fatalf("requests=%d", len(requests))
			}
			original := requests[0]
			if len(original.Plan.Nodes) != 1 || original.Plan.Nodes[0].Retry.MaxAttempts != test.attempts {
				t.Fatalf("original retry policy: %+v", original.Plan.Nodes)
			}
			for _, configRetries := range []int{2, 4} {
				writeConfig(configRetries)
				prepared, err := prepareExecutionRerun(preflightapi.VerificationSession{ProjectID: "review", Recipe: original.Recipe}, server.URL)
				if err != nil {
					t.Fatal(err)
				}
				nodes, _, err := verificationNodesForPlans(prepared.WarmPlans, prepared.Plans, syncbundlebuild.BuildResult{})
				if err != nil {
					t.Fatal(err)
				}
				expected := test.attempts
				if configRetries == 4 {
					expected = test.changedAttempts
				}
				if len(nodes) != 1 || nodes[0].Retry.MaxAttempts != expected {
					t.Errorf("project retries=%d: rerun attempts=%d, expected=%d", configRetries, nodes[0].Retry.MaxAttempts, expected)
				}
				if prepared.Plans[0].RetryProvided != original.Recipe.RetryProvided {
					t.Error("rerun changed whether retries were explicitly requested")
				}
			}
		})
	}
}
