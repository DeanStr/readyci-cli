package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/internal/syncbundlebuild"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestExplicitAffectedCheckUsesNamedCheckSourcePolicy(t *testing.T) {
	chdirCLITestRepo(t, "affected-named-rerun-source")
	runGitForCLITest(t, "init")
	var requests []preflightapi.CreateVerificationSessionRequest
	server := captureExecutionRequests(t, &requests)
	for _, include := range []bool{false, true} {
		value := "false"
		if include {
			value = "true"
		}
		if err := os.WriteFile(".readyci.yml", []byte("project_id: review\nsync:\n  include_untracked: "+value+"\nchecks:\n  test:\n    command: sh new_test.sh\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile("new_test.sh", []byte("echo test\n"), 0644); err != nil {
			t.Fatal(err)
		}
		requests = nil
		_ = affectedCommand([]string{"--api-url", server.URL, "--check", "test"})
		_ = checkCommand([]string{"--api-url", server.URL, "test"})
		if len(requests) != 2 {
			t.Fatalf("requests=%d", len(requests))
		}
		if requests[0].Snapshot.Digest != requests[1].Snapshot.Digest {
			t.Errorf("explicit affected check ignores configured include_untracked=%t", include)
		}
		prepared, err := prepareExecutionRerun(preflightapi.VerificationSession{ProjectID: "review", Recipe: requests[0].Recipe}, server.URL)
		if err != nil {
			t.Fatal(err)
		}
		manifest, err := syncbundlebuild.SnapshotWithOptions(context.Background(), ".", prepared.BuildOptions)
		if err != nil {
			t.Fatal(err)
		}
		if manifest.SnapshotDigest != requests[0].Snapshot.Digest {
			t.Errorf("unchanged named-check rerun changed snapshot for include_untracked=%t", include)
		}
	}
}

func TestExecutionRerunPreservesAdHocUntrackedSource(t *testing.T) {
	chdirCLITestRepo(t, "adhoc-rerun-source")
	runGitForCLITest(t, "init")
	if err := os.WriteFile("test.sh", []byte("echo hello\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var requests []preflightapi.CreateVerificationSessionRequest
	server := captureExecutionRequests(t, &requests)
	_ = runCommand([]string{"--api-url", server.URL, "--project", "review", "sh test.sh"})
	if len(requests) != 1 {
		t.Fatalf("expected one request, got %d", len(requests))
	}
	prepared, err := prepareExecutionRerun(preflightapi.VerificationSession{ProjectID: "review", Recipe: requests[0].Recipe}, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := syncbundlebuild.SnapshotWithOptions(context.Background(), ".", prepared.BuildOptions)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(requests[0].Snapshot.ChangedFiles, manifest.ChangedFiles) || manifest.SnapshotDigest != requests[0].Snapshot.Digest {
		t.Fatalf("unchanged ad hoc rerun changed snapshot: original=%v rerun=%v", requests[0].Snapshot.ChangedFiles, manifest.ChangedFiles)
	}
}

func TestExecutionRerunExpandsAffectedProfiles(t *testing.T) {
	chdirCLITestRepo(t, "rerun-affected-profiles")
	runGitForCLITest(t, "init")
	for name, content := range map[string]string{"main.go": "package main\n", "README.md": "docs\n"} {
		if err := os.WriteFile(name, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var requests []preflightapi.CreateVerificationSessionRequest
	server := captureExecutionRequests(t, &requests)
	for _, rules := range []string{"", "  rules:\n    - paths: ['**/*.md']\n      checks: [docs]\n"} {
		cfg := "project_id: review\nsync:\n  include_untracked: true\nchecks:\n  go:\n    command: go test ./...\n  docs:\n    command: echo docs\naffected:\n  profiles: [go]\n" + rules
		if err := os.WriteFile(".readyci.yml", []byte(cfg), 0600); err != nil {
			t.Fatal(err)
		}
		requests = nil
		_ = checkCommand([]string{"--api-url", server.URL})
		if len(requests) != 1 {
			t.Fatalf("expected one request, got %d", len(requests))
		}
		prepared, err := prepareExecutionRerun(preflightapi.VerificationSession{ProjectID: "review", Recipe: requests[0].Recipe}, server.URL)
		if err != nil {
			t.Errorf("profile rerun: %v", err)
			continue
		}
		var original, rerun []string
		for _, node := range requests[0].Plan.Nodes {
			original = append(original, node.Name)
		}
		for _, plan := range prepared.Plans {
			rerun = append(rerun, plan.Name)
		}
		if !reflect.DeepEqual(original, rerun) {
			t.Errorf("profile rerun changed selection: original=%v rerun=%v", original, rerun)
		}
	}
}

func TestRunJSONErrorsRespectCLIFlagBoundary(t *testing.T) {
	t.Chdir(t.TempDir())
	args := []string{"run", "--json", "--network-mode", "invalid", "true"}
	err := run(args)
	if err == nil {
		t.Fatal("expected invalid network mode")
	}
	output, _ := captureStdoutForCLITest(t, func() error { renderCLIError(args, err); return nil })
	var document cliErrorEnvelope
	if err := json.Unmarshal([]byte(output), &document); err != nil {
		t.Errorf("missing structured error: %v; stdout=%q", err, output)
	} else if document.Error.Code != "config_invalid" {
		t.Errorf("wrong error taxonomy: %+v", document.Error)
	}
	for _, args := range [][]string{
		{"run", "tool", "--json"},
		{"run", "--", "--json"},
		{"run", "--cache-path", "--json", "true"},
		{"run", "--json=false", "true", "--json"},
	} {
		if invocationRequestsStructuredOutput(args) {
			t.Errorf("remote argument or flag value enabled CLI JSON output: %v", args)
		}
	}
	if !invocationRequestsStructuredOutput([]string{"run", "--cache-path", "dir", "--json", "true"}) {
		t.Error("CLI JSON flag after a valued option was ignored")
	}
}

func TestWarmExecutionRejectsMixedChangeModes(t *testing.T) {
	chdirCLITestRepo(t, "warm-mixed-source")
	runGitForCLITest(t, "init")
	if err := os.WriteFile(".readyci.yml", []byte(`project_id: review
network:
  warm_mode: default
checks:
  index:
    command: echo index
    changes: staged
  workspace:
    command: echo workspace
    changes: working
`), 0644); err != nil {
		t.Fatal(err)
	}
	var requests []preflightapi.CreateVerificationSessionRequest
	server := captureExecutionRequests(t, &requests)
	err := warmCommand([]string{"--api-url", server.URL, "--profile", "all"})
	if err == nil || !strings.Contains(err.Error(), "different changes modes") || len(requests) != 0 {
		t.Fatalf("mixed source policy must fail before submission: err=%v, requests=%d", err, len(requests))
	}
	if err := warmCommand([]string{"--api-url", server.URL, "--profile", "all", "--dry-run"}); err == nil || !strings.Contains(err.Error(), "different changes modes") {
		t.Fatalf("dry-run must reject the same incompatible source policy: %v", err)
	}
	// An explicit mode resolves the checks to the same snapshot before validation.
	_ = warmCommand([]string{"--api-url", server.URL, "--profile", "all", "--changes", "working"})
	if len(requests) != 1 || requests[0].Snapshot.ChangeMode != "working" {
		t.Fatalf("explicit common mode did not reach session creation: %+v", requests)
	}
}

func TestExecutionLocalDevelopmentAllowsMissingCredential(t *testing.T) {
	chdirCLITestRepo(t, "execution-local-auth")
	runGitForCLITest(t, "init")
	var requests []preflightapi.CreateVerificationSessionRequest
	server := captureExecutionRequests(t, &requests)
	t.Setenv("READYCI_API_TOKEN", "")
	t.Setenv("READYCI_CONFIG_DIR", t.TempDir())
	if err := runCommand([]string{"--api-url", server.URL, "true"}); err == nil || !strings.Contains(err.Error(), "403 Forbidden") {
		t.Fatalf("server authentication failure was not preserved: %v", err)
	}
	if len(requests) != 1 {
		t.Fatalf("local execution did not reach the server: requests=%d", len(requests))
	}
	for _, operation := range []string{"retry", "rerun"} {
		t.Run(operation, func(t *testing.T) {
			contacted := false
			local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				contacted = true
				http.Error(w, "credential required by server", http.StatusForbidden)
			}))
			defer local.Close()
			err := sessionsCommand([]string{operation, "--api-url", local.URL, "group_dev"})
			if !contacted || err == nil || !strings.Contains(err.Error(), "403 Forbidden") {
				t.Fatalf("local %s did not preserve server authentication: contacted=%t err=%v", operation, contacted, err)
			}
		})
	}
	for _, endpoint := range []string{"http://localhost:8080", "http://127.0.0.1:8080", "http://[::1]:8080", "https://api.example.invalid"} {
		for _, purpose := range []string{"", preflightapi.ExecutionPurpose} {
			_, err := prepareVerificationSessionRemoteContext(context.Background(), verificationSessionOptions{APIURL: endpoint, Purpose: purpose})
			wantAllowed := purpose == preflightapi.ExecutionPurpose && endpoint != "https://api.example.invalid"
			if (err == nil) != wantAllowed {
				t.Errorf("endpoint=%s purpose=%q allowed=%t err=%v", endpoint, purpose, wantAllowed, err)
			}
		}
	}
}

func TestExecutionRetriesHonorServiceClassification(t *testing.T) {
	chdirCLITestRepo(t, "execution-retry-policy")
	runGitForCLITest(t, "init")
	if err := os.WriteFile("package.json", []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	var requests []preflightapi.CreateVerificationSessionRequest
	server := captureExecutionRequests(t, &requests)
	if err := runCommand([]string{"--api-url", server.URL, "--project", "review", "--retry", "1", "npm ci"}); err == nil {
		t.Fatal("fixture should stop creation")
	}
	if len(requests) != 1 || len(requests[0].Plan.Nodes) != 1 {
		t.Fatalf("captured requests: %+v", requests)
	}
	planned := requests[0].Plan.Nodes[0]
	for _, classification := range []string{preflightapi.ClassificationDependencyDownloadFailed, preflightapi.ClassificationTimeout} {
		for _, retryable := range []bool{false, true} {
			node := preflightapi.VerificationNode{Attempts: []preflightapi.VerificationAttempt{{Status: preflightapi.StatusFailed, Classification: classification, Retryable: retryable}}}
			if got := preflightapi.VerificationNodeRetryPending(planned, node); got != retryable {
				t.Errorf("classification=%s service_retryable=%t retry=%t", classification, retryable, got)
			}
			node.Attempts = append(node.Attempts, node.Attempts[0])
			if preflightapi.VerificationNodeRetryPending(planned, node) {
				t.Error("retry budget was exceeded")
			}
		}
	}
	if preflightapi.VerificationNodeRetryPending(planned, preflightapi.VerificationNode{Attempts: []preflightapi.VerificationAttempt{{Status: preflightapi.StatusFailed, Classification: preflightapi.ClassificationTestFailed, Retryable: true}}}) {
		t.Fatal("deterministic test failures became retryable")
	}
	strict, err := verificationSessionRequest(verificationSessionOptions{}, agentIntentEditLoop, verificationSessionRemoteContext{}, "review", syncbundlebuild.BuildResult{}, syncbundlebuild.BuildOptions{}, nil, "check", nil, []checkRunPlan{{Name: "test", Retries: 1, Options: remoteRunOptions{Command: "true"}}})
	if err != nil {
		t.Fatal(err)
	}
	if preflightapi.VerificationNodeRetryPending(strict.Plan.Nodes[0], preflightapi.VerificationNode{Attempts: []preflightapi.VerificationAttempt{{Status: preflightapi.StatusTimedOut, Classification: preflightapi.ClassificationTimeout, Retryable: true}}}) {
		t.Fatal("verification timeout policy was broadened")
	}
}

func TestExecutionExitIgnoresRecoveredAndOptionalFailures(t *testing.T) {
	attempt := func(code int) preflightapi.VerificationAttempt {
		status := preflightapi.StatusFailed
		if code == 0 {
			status = preflightapi.StatusSucceeded
		}
		return preflightapi.VerificationAttempt{Status: status, ExitCode: &code}
	}
	session := preflightapi.VerificationSession{State: preflightapi.VerificationStateTerminal, Recipe: preflightapi.VerificationRecipe{Purpose: preflightapi.ExecutionPurpose}, Verdict: preflightapi.VerificationVerdictFailed, Nodes: []preflightapi.VerificationNode{
		{ID: "recovered", Required: true, Status: preflightapi.StatusSucceeded, Attempts: []preflightapi.VerificationAttempt{attempt(137), attempt(0)}},
		{ID: "optional", Required: false, Status: preflightapi.StatusFailed, Attempts: []preflightapi.VerificationAttempt{attempt(2)}},
		{ID: "failing", Required: true, Status: preflightapi.StatusFailed, Attempts: []preflightapi.VerificationAttempt{attempt(124), attempt(1)}},
	}}
	var exit exitCodeError
	if err := executionSessionExit(session); !errors.As(err, &exit) || exit.code != 1 {
		t.Fatalf("exit=%v; want current required failure's exit 1", err)
	}
}
