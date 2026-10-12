package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestExecutionDisplaysTestOutput(t *testing.T) {
	chdirCLITestRepo(t, "execution-output")
	runGitForCLITest(t, "init")
	var apiURL string
	eventReads := 0
	child := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs":
			writeCLITestJSON(w, 201, preflightapi.CreateRunResponse{RunID: "run_output", UploadURL: apiURL + "/upload", Status: preflightapi.StatusAwaitingUpload})
		case r.Method == http.MethodPut:
			w.WriteHeader(202)
		case r.URL.Path == "/v1/runs/run_output/events":
			eventReads++
			writeCLITestJSON(w, 200, preflightapi.RunEventsResponse{RunID: "run_output", NextCursor: 1, Events: []preflightapi.RunEvent{{ID: 1, Type: preflightapi.EventStdout, Data: "test output reached terminal\n"}}})
		default:
			http.NotFound(w, r)
		}
	})
	fixture := successfulExecutionTestHandler(t, child)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/events") {
			child.ServeHTTP(w, r)
			return
		}
		fixture.ServeHTTP(w, r)
	}))
	defer server.Close()
	apiURL = server.URL
	output, err := captureStdoutForCLITest(t, func() error { return runCommand([]string{"--api-url", apiURL, "--project", "review", "echo test"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "test output reached terminal\n") {
		t.Fatalf("test output missing: %s", output)
	}

	eventReads = 0
	output, err = captureStdoutForCLITest(t, func() error {
		return runCommand([]string{"--api-url", apiURL, "--project", "review", "--json", "echo test"})
	})
	if err != nil {
		t.Fatal(err)
	}
	var report executionSessionReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("invalid JSON: %s: %v", output, err)
	}
	if eventReads != 0 || report.Session.Verdict != preflightapi.VerificationVerdictChecksPassed {
		t.Fatalf("JSON fetched logs or lost result: reads=%d verdict=%s", eventReads, report.Session.Verdict)
	}
}

func TestExecutionExplicitlyRequestsProjectSecrets(t *testing.T) {
	chdirCLITestRepo(t, "execution-secrets")
	runGitForCLITest(t, "init")
	var requests []preflightapi.CreateVerificationSessionRequest
	server := captureExecutionRequests(t, &requests)
	_ = runCommand([]string{"--api-url", server.URL, "--project", "review", "--request-secrets", "true"})
	if len(requests) != 1 {
		t.Fatalf("secret opt-in did not reach session creation: requests=%d", len(requests))
	}
	request := requests[0]
	if !request.Recipe.RequestSecrets || request.Plan.Nodes[0].Run.SecretsRequested == nil || !*request.Plan.Nodes[0].Run.SecretsRequested {
		t.Fatal("execution lost explicit secret request")
	}

	prepared, err := prepareExecutionRerun(preflightapi.VerificationSession{ProjectID: "review", Recipe: request.Recipe}, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Plans) != 1 || prepared.Plans[0].Options.SecretsRequested == nil || !*prepared.Plans[0].Options.SecretsRequested {
		t.Fatal("literal-command rerun lost secret opt-in")
	}
}

func TestExecutionSecretSelectionWarmExclusionAndRerun(t *testing.T) {
	chdirCLITestRepo(t, "secret-selection")
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
		for _, requested := range []bool{false, true} {
			requests = nil
			args := []string{command, "--api-url", server.URL}
			if requested {
				args = append(args, "--request-secrets")
			}
			if err := run(args); err == nil {
				t.Fatal("fixture did not stop creation")
			}
			if len(requests) != 1 {
				t.Fatalf("%s: requests=%d", command, len(requests))
			}
			request := requests[0]
			if request.Recipe.RequestSecrets != requested {
				t.Fatalf("recipe lost opt-in: %+v", request.Recipe)
			}
			hasWarm := false
			for _, node := range request.Plan.Nodes {
				warm := node.Phase == "warm"
				hasWarm = hasWarm || warm
				if node.Run.SecretsRequested == nil || *node.Run.SecretsRequested != (requested && !warm) {
					t.Fatalf("incorrect secret selection: %+v", node)
				}
			}
			if !hasWarm {
				t.Fatal("fixture did not include dependency preparation")
			}
			prepared, err := prepareExecutionRerun(preflightapi.VerificationSession{ProjectID: "review", Recipe: request.Recipe}, server.URL)
			if err != nil {
				t.Fatal(err)
			}
			for _, plan := range prepared.Plans {
				if plan.Options.SecretsRequested == nil || *plan.Options.SecretsRequested != requested {
					t.Fatal("rerun lost secret opt-in")
				}
			}
			for _, plan := range prepared.WarmPlans {
				if plan.Options.SecretsRequested == nil || *plan.Options.SecretsRequested {
					t.Fatal("rerun enabled secrets during preparation")
				}
			}
		}
	}
	output, err := captureStdoutForCLITest(t, func() error { return checkCommand([]string{"--request-secrets", "--dry-run", "--json", "test"}) })
	if err != nil || !strings.Contains(output, `"secrets_requested": true`) {
		t.Fatalf("dry-run omitted opt-in: %s %v", output, err)
	}
	requests = nil
	if err := checkCommand([]string{"--api-url", server.URL, "--request-secrets", "dependencies"}); err == nil || len(requests) != 0 {
		t.Fatal("secret-bearing dependency preparation did not fail before submission")
	}
}
