package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestSessionsRetryDetachUsesServerOwnedSuccessorEndpoint(t *testing.T) {
	recipe := preflightapi.VerificationRecipe{
		SchemaVersion: preflightapi.VerificationRecipeSchemaVersion,
		ChangeMode:    "working",
		Source:        &preflightapi.VerificationSourceRecipe{ExcludePatterns: []string{}, DefaultExcludePatterns: []string{}, IncludeUntracked: true, UseProjectIgnore: true},
	}
	recipeDigest, err := preflightapi.VerificationRecipeDigest(recipe)
	if err != nil {
		t.Fatalf("digest verification recipe: %v", err)
	}
	recipe.Digest = recipeDigest

	var request preflightapi.RetryVerificationSessionRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/verification-sessions/group_original/retry" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer pfl_retry" {
			t.Fatalf("authorization = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		writeCLITestJSON(w, http.StatusCreated, preflightapi.RetryVerificationSessionResponse{
			SchemaVersion:      preflightapi.VerificationSchemaVersion,
			RetryOfSessionID:   "group_original",
			PolicyReauthorized: true,
			Session: preflightapi.VerificationSession{
				SchemaVersion:  preflightapi.VerificationSchemaVersion,
				SessionID:      "group_successor",
				IdempotencyKey: "session-retry-key",
				ProjectID:      "proj_dev",
				PredecessorID:  "group_original",
				State:          preflightapi.VerificationStateSealed,
				Verdict:        preflightapi.VerificationVerdictPending,
				Currentness:    preflightapi.VerificationCurrentnessCurrent,
				Assurance: preflightapi.VerificationAssurance{
					Scope:     preflightapi.VerificationAssuranceScopeConfiguredChecks,
					Execution: preflightapi.VerificationExecutionAssurancePending,
					Evaluator: preflightapi.VerificationEvaluatorAssuranceUnassessed,
				},
				Recipe:    recipe,
				Snapshot:  preflightapi.VerificationSnapshot{Digest: "sha256:" + strings.Repeat("a", 64), ChangeMode: "working", ChangedFiles: []string{}},
				Coverage:  preflightapi.VerificationCoverage{MatchedFiles: []string{}, IgnoredFiles: []string{}, UncoveredFiles: []string{}},
				Plan:      preflightapi.VerificationPlan{Digest: "sha256:" + strings.Repeat("b", 64), Authority: preflightapi.VerificationPlanAuthorityClientProposed, Nodes: []preflightapi.VerificationNodePlan{}},
				Nodes:     []preflightapi.VerificationNode{},
				CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
			},
		})
	}))
	defer server.Close()
	output, err := captureStdoutForCLITest(t, func() error {
		return sessionsCommand([]string{"retry", "--api-url", server.URL, "--token", "pfl_retry", "--detach", "--json", "group_original"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, `"session_id": "group_successor"`) || !strings.Contains(output, `"predecessor_id": "group_original"`) {
		t.Fatalf("request=%+v output=%s", request, output)
	}
	validateAgentJSONSchema(t, []byte(output))
}

func TestInterruptedRetryReconcilesAndCancelsCreatedSuccessor(t *testing.T) {
	retried := false
	cancelled := false
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/verification-sessions/group_original/retry":
			retried = true
			writeCLITestJSON(w, http.StatusOK, preflightapi.RetryVerificationSessionResponse{
				SchemaVersion:    preflightapi.VerificationSchemaVersion,
				RetryOfSessionID: "group_original",
				Session: preflightapi.VerificationSession{
					SchemaVersion: preflightapi.VerificationSchemaVersion,
					SessionID:     "group_successor",
					ProjectID:     "proj_dev",
					State:         preflightapi.VerificationStateSealed,
					Verdict:       preflightapi.VerificationVerdictPending,
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/verification-sessions/group_successor/cancel":
			cancelled = true
			writeCLITestJSON(w, http.StatusOK, preflightapi.VerificationSession{
				SchemaVersion: preflightapi.VerificationSchemaVersion,
				SessionID:     "group_successor",
				ProjectID:     "proj_dev",
				State:         preflightapi.VerificationStateTerminal,
				Verdict:       preflightapi.VerificationVerdictCancelled,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := api.NewClient(server.URL, api.WithToken("pfl_retry"))
	session, err := reconcileVerificationRetryAfterInterrupt(client, "group_original")
	if err != nil {
		t.Fatal(err)
	}
	output, err := captureStdoutForCLITest(t, func() error {
		return outputInterruptedVerificationSession(server.URL, client, session, os.Interrupt, true)
	})
	var exitErr exitCodeError
	if !errors.As(err, &exitErr) || exitErr.code != 130 {
		t.Fatalf("interruption error = %v", err)
	}
	if !retried || !cancelled || !strings.Contains(output, `"resource_id": "group_successor"`) || !strings.Contains(output, `"cancellation_acknowledged": true`) {
		t.Fatalf("retried=%t cancelled=%t output=%s", retried, cancelled, output)
	}
}

func TestSessionsRerunFailsFastWithoutVersionedRecipe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/verification-sessions/group_old" {
			http.NotFound(w, r)
			return
		}
		writeCLITestJSON(w, http.StatusOK, preflightapi.VerificationSession{
			SchemaVersion: preflightapi.VerificationSchemaVersion,
			SessionID:     "group_old",
			ProjectID:     "proj_dev",
			State:         preflightapi.VerificationStateTerminal,
		})
	}))
	defer server.Close()
	err := sessionsCommand([]string{"rerun", "--api-url", server.URL, "--token", "pfl_retry", "group_old"})
	if err == nil || !strings.Contains(err.Error(), "supported rerun recipe") {
		t.Fatalf("rerun error = %v", err)
	}
}

func TestSessionsRerunReplaysVersionedRecipeAgainstCurrentWorkspace(t *testing.T) {
	_ = chdirCLITestRepo(t, "")
	if err := os.WriteFile(".readyci.yml", []byte(`version: 1
project_id: proj_dev
checks:
  cli:
    command: "go test ./..."
    network_mode: none
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("go.mod", []byte("module example.com/rerun\n\ngo 1.25.13\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitForCLITest(t, "init")
	runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "initial")
	if err := os.WriteFile(".readyciignore", []byte("project-ignored.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("project-ignored.txt", []byte("must follow retained recipe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("recipe-excluded.txt", []byte("must be excluded\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var createdRequest preflightapi.CreateVerificationSessionRequest
	uploaded := false
	recipe := preflightapi.VerificationRecipe{
		SchemaVersion: preflightapi.VerificationRecipeSchemaVersion,
		Rerunnable:    true,
		Check:         "cli",
		ChangeMode:    "working",
		Source:        &preflightapi.VerificationSourceRecipe{ExcludePatterns: []string{"recipe-excluded.txt"}, DefaultExcludePatterns: []string{}, IncludeUntracked: true, UseProjectIgnore: false},
	}
	recipe.Digest, _ = preflightapi.VerificationRecipeDigest(recipe)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/verification-sessions/group_original":
			writeCLITestJSON(w, http.StatusOK, preflightapi.VerificationSession{
				SchemaVersion:  preflightapi.VerificationSchemaVersion,
				SessionID:      "group_original",
				IdempotencyKey: "original-key",
				TaskID:         "task-stable",
				ProjectID:      "proj_dev",
				Intent:         "edit-loop",
				Goal:           "verify CLI",
				State:          preflightapi.VerificationStateTerminal,
				Recipe:         recipe,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/projects/proj_dev/verification-policy/source-binding":
			writeCLITestJSON(w, http.StatusOK, preflightapi.ProjectVerificationSourcePolicyResponse{
				SchemaVersion: preflightapi.ProjectVerificationSourcePolicySchemaVersion,
				ProjectID:     "proj_dev",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/verification-sessions":
			if err := json.NewDecoder(r.Body).Decode(&createdRequest); err != nil {
				t.Fatal(err)
			}
			writeCLITestJSON(w, http.StatusCreated, preflightapi.CreateVerificationSessionResponse{
				SchemaVersion:          preflightapi.VerificationSchemaVersion,
				SourceUploadMode:       preflightapi.VerificationSourceUploadModeDirect,
				SourceUploadPrepareURL: "/v1/verification-sessions/group_successor/source-upload",
				Session: preflightapi.VerificationSession{
					SchemaVersion:  preflightapi.VerificationSchemaVersion,
					SessionID:      "group_successor",
					IdempotencyKey: createdRequest.IdempotencyKey,
					TaskID:         createdRequest.TaskID,
					ProjectID:      createdRequest.ProjectID,
					PredecessorID:  createdRequest.PredecessorID,
					State:          preflightapi.VerificationStateSealed,
					Verdict:        preflightapi.VerificationVerdictPending,
					Currentness:    preflightapi.VerificationCurrentnessCurrent,
					Recipe:         createdRequest.Recipe,
					Snapshot:       createdRequest.Snapshot,
					Coverage:       createdRequest.Coverage,
					Plan:           createdRequest.Plan,
					Nodes:          []preflightapi.VerificationNode{},
					CreatedAt:      time.Now().UTC(), UpdatedAt: time.Now().UTC(),
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/verification-sessions/group_successor/source-upload":
			writeCLITestJSON(w, http.StatusOK, preflightapi.PrepareVerificationSessionSourceUploadResponse{
				UploadURL:    server.URL + "/verification-source-object",
				UploadMethod: http.MethodPut,
				CompleteURL:  "/v1/verification-sessions/group_successor/source-complete",
			})
		case r.Method == http.MethodPut && r.URL.Path == "/verification-source-object":
			uploaded = true
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/verification-sessions/group_successor/source-complete":
			writeCLITestJSON(w, http.StatusAccepted, preflightapi.VerificationSession{
				SchemaVersion:  preflightapi.VerificationSchemaVersion,
				SessionID:      "group_successor",
				IdempotencyKey: createdRequest.IdempotencyKey,
				TaskID:         createdRequest.TaskID,
				ProjectID:      createdRequest.ProjectID,
				PredecessorID:  createdRequest.PredecessorID,
				State:          preflightapi.VerificationStateTerminal,
				Verdict:        preflightapi.VerificationVerdictNoChangeRequired,
				Currentness:    preflightapi.VerificationCurrentnessCurrent,
				Recipe:         createdRequest.Recipe,
				Snapshot:       createdRequest.Snapshot,
				Coverage:       createdRequest.Coverage,
				Plan:           createdRequest.Plan,
				Nodes:          []preflightapi.VerificationNode{},
				CreatedAt:      time.Now().UTC(), UpdatedAt: time.Now().UTC(),
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	output, err := captureStdoutForCLITest(t, func() error {
		return sessionsCommand([]string{"rerun", "--api-url", server.URL, "--token", "pfl_rerun", "--detach", "--json", "group_original"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !uploaded || createdRequest.PredecessorID != "group_original" || createdRequest.TaskID != "task-stable" || createdRequest.Recipe.Check != "cli" {
		t.Fatalf("uploaded=%t request=%+v", uploaded, createdRequest)
	}
	if !slices.Contains(createdRequest.Snapshot.ChangedFiles, "recipe-excluded.txt") ||
		slices.Contains(createdRequest.Snapshot.ChangedFiles, "project-ignored.txt") {
		t.Fatalf("rerun did not use current source packaging: changed_files=%v", createdRequest.Snapshot.ChangedFiles)
	}
	if !strings.Contains(output, `"session_id": "group_successor"`) || !strings.Contains(output, `"state": "terminal"`) {
		t.Fatalf("output=%s", output)
	}
}

func TestSessionsRetryJSONWritesOneSessionDocumentWhenWatchFails(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/verification-sessions/group_original/retry":
			writeCLITestJSON(w, http.StatusCreated, preflightapi.RetryVerificationSessionResponse{
				SchemaVersion:    preflightapi.VerificationSchemaVersion,
				RetryOfSessionID: "group_original",
				Session: preflightapi.VerificationSession{
					SchemaVersion: preflightapi.VerificationSchemaVersion,
					SessionID:     "group_successor",
					ProjectID:     "proj_dev",
					State:         preflightapi.VerificationStateSealed,
					Verdict:       preflightapi.VerificationVerdictPending,
					Currentness:   preflightapi.VerificationCurrentnessCurrent,
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/verification-sessions/group_successor":
			requests++
			http.Error(w, "observation unavailable", http.StatusServiceUnavailable)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	output, err := captureStdoutForCLITest(t, func() error {
		return sessionsCommand([]string{"retry", "--api-url", server.URL, "--token", "pfl_retry", "--json", "group_original"})
	})
	if err == nil {
		t.Fatal("expected watch failure")
	}
	if requests != 1 {
		t.Fatalf("watch requests = %d, want 1", requests)
	}
	decoder := json.NewDecoder(strings.NewReader(output))
	var session preflightapi.VerificationSession
	if decodeErr := decoder.Decode(&session); decodeErr != nil {
		t.Fatalf("decode session: %v\n%s", decodeErr, output)
	}
	if session.SessionID != "group_successor" || session.ClientError == nil || session.ClientError.Code != "platform_internal" {
		t.Fatalf("session receipt = %+v", session)
	}
	var extra any
	if decodeErr := decoder.Decode(&extra); decodeErr != io.EOF {
		t.Fatalf("structured output contained trailing data (%v): %s", decodeErr, output)
	}
}

func TestSessionsRerunRejectsTamperedRecipeDigest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeCLITestJSON(w, http.StatusOK, preflightapi.VerificationSession{
			SchemaVersion: preflightapi.VerificationSchemaVersion,
			SessionID:     "group_original",
			ProjectID:     "proj_dev",
			State:         preflightapi.VerificationStateTerminal,
			Recipe: preflightapi.VerificationRecipe{
				SchemaVersion: preflightapi.VerificationRecipeSchemaVersion,
				Digest:        "sha256:" + strings.Repeat("a", 64),
				Rerunnable:    true,
				Check:         "cli",
				ChangeMode:    "working",
				Source:        &preflightapi.VerificationSourceRecipe{ExcludePatterns: []string{}, DefaultExcludePatterns: []string{}, IncludeUntracked: true, UseProjectIgnore: true},
			},
		})
	}))
	defer server.Close()
	err := sessionsCommand([]string{"rerun", "--api-url", server.URL, "--token", "pfl_retry", "group_original"})
	if err == nil || !strings.Contains(err.Error(), "digest does not match") {
		t.Fatalf("rerun error = %v, want recipe digest rejection", err)
	}
}
