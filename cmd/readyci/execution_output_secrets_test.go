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

// Secret-bearing runs cannot use prepared caches, so a secret request reaches
// the service only when preparation is skipped. Every other path reports the
// service's guidance locally: previews, submissions and reruns alike.
func TestExecutionSecretSelectionSkipsPreparationOrFailsLocally(t *testing.T) {
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
		withCheck := func(args ...string) []string {
			if command == "check" {
				args = append(args, "test")
			}
			return args
		}
		requests = nil
		if err := run(withCheck(command, "--api-url", server.URL, "--request-secrets", "--skip-warm")); err == nil {
			t.Fatal("fixture did not stop creation")
		}
		if len(requests) != 1 {
			t.Fatalf("%s: requests=%d", command, len(requests))
		}
		request := requests[0]
		if !request.Recipe.RequestSecrets || !request.Recipe.SkipWarm {
			t.Fatalf("recipe lost opt-in or skip: %+v", request.Recipe)
		}
		for _, node := range request.Plan.Nodes {
			if node.Phase == preflightapi.VerificationNodePhaseWarm || node.Run.SecretsRequested == nil || !*node.Run.SecretsRequested {
				t.Fatalf("incorrect secret selection: %+v", node)
			}
		}
		prepared, err := prepareExecutionRerun(preflightapi.VerificationSession{ProjectID: "review", Recipe: request.Recipe}, server.URL)
		if err != nil {
			t.Fatal(err)
		}
		if len(prepared.WarmPlans) != 0 || len(prepared.Plans) == 0 {
			t.Fatalf("rerun changed preparation: %+v", prepared)
		}
		for _, plan := range prepared.Plans {
			if plan.Options.SecretsRequested == nil || !*plan.Options.SecretsRequested {
				t.Fatal("rerun lost secret opt-in")
			}
		}
		withoutSkip := request.Recipe
		withoutSkip.SkipWarm = false
		if _, err := prepareExecutionRerun(preflightapi.VerificationSession{ProjectID: "review", Recipe: withoutSkip}, server.URL); err == nil || !strings.Contains(err.Error(), preflightapi.SecretPreparationGuidance) {
			t.Fatalf("%s: rerun with preparation err=%v, want local rejection with guidance", command, err)
		}

		for _, dryRun := range []bool{true, false} {
			args := withCheck(command, "--api-url", server.URL, "--request-secrets")
			if dryRun {
				args = withCheck(command, "--api-url", server.URL, "--request-secrets", "--dry-run", "--json")
			}
			requests = nil
			_, err := captureStdoutForCLITest(t, func() error { return run(args) })
			if err == nil || !strings.Contains(err.Error(), preflightapi.SecretPreparationGuidance) || len(requests) != 0 {
				t.Fatalf("%v: err=%v requests=%d, want local rejection with guidance", args, err, len(requests))
			}
		}
	}
	// Agent planning resolves preparation as readyci verify does, so it never
	// reports ready for a verify command that fails before submission.
	report, err := buildAgentPlanReport(server.URL, "", "", "", "", "", false)
	if err != nil || report.Status != "ready" || report.Mode != "affected_rules" {
		t.Fatalf("agent plan = %+v, %v; want a ready affected-rules plan", report, err)
	}
	for name, attempt := range map[string]func() error{
		"agent plan": func() error { _, err := buildAgentPlanReport(server.URL, "", "", "", "", "", true); return err },
		"verify":     func() error { return run([]string{"verify", "--api-url", server.URL, "--request-secrets"}) },
	} {
		requests = nil
		if _, err := captureStdoutForCLITest(t, attempt); err == nil || !strings.Contains(err.Error(), preflightapi.SecretPreparationGuidance) || len(requests) != 0 {
			t.Fatalf("%s with secrets: err=%v requests=%d, want local rejection with guidance", name, err, len(requests))
		}
	}
	output, err := captureStdoutForCLITest(t, func() error {
		return checkCommand([]string{"--request-secrets", "--skip-warm", "--dry-run", "--json", "test"})
	})
	if err != nil || !strings.Contains(output, `"secrets_requested": true`) {
		t.Fatalf("dry-run omitted opt-in: %s %v", output, err)
	}
	requests = nil
	if err := checkCommand([]string{"--api-url", server.URL, "--request-secrets", "dependencies"}); err == nil || len(requests) != 0 {
		t.Fatal("secret-bearing dependency preparation did not fail before submission")
	}
}
