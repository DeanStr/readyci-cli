package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/internal/syncbundlebuild"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestUnsupportedSessionPurposeIsNotVerification(t *testing.T) {
	recipe := preflightapi.VerificationRecipe{SchemaVersion: preflightapi.VerificationRecipeSchemaVersion, Purpose: "unsupported", Rerunnable: true, Source: &preflightapi.VerificationSourceRecipe{}}
	var err error
	recipe.Digest, err = preflightapi.VerificationRecipeDigest(recipe)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateVerificationRerunRecipe(recipe); err == nil {
		t.Error("unsupported purpose accepted as a verification rerun")
	}
	if err := executionSessionExit(preflightapi.VerificationSession{Recipe: recipe, State: preflightapi.VerificationStateTerminal, Verdict: preflightapi.VerificationVerdictVerified, Currentness: preflightapi.VerificationCurrentnessCurrent}); err == nil {
		t.Error("unsupported purpose accepted as successful verification")
	}
}

func TestAffectedExecutionRerunRequiresAffectedRules(t *testing.T) {
	chdirCLITestRepo(t, "removed-affected-rules")
	if err := os.WriteFile(".readyci.yml", []byte("project_id: review\nchecks:\n  test:\n    command: echo unrelated-default\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := prepareExecutionRerun(preflightapi.VerificationSession{ProjectID: "review", Recipe: preflightapi.VerificationRecipe{Purpose: preflightapi.ExecutionPurpose, ChangeMode: "working"}}, "http://localhost")
	if err == nil || !strings.Contains(err.Error(), "affected rules") {
		t.Fatalf("missing affected rules silently selected a default check: %v", err)
	}
}

func TestExecutionRejectsUnexpectedBaselineAcknowledgement(t *testing.T) {
	chdirCLITestRepo(t, "unexpected-baseline")
	runGitForCLITest(t, "init")
	runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	if err := os.WriteFile("file.txt", []byte("baseline"), 0644); err != nil {
		t.Fatal(err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "baseline")
	build := syncbundlebuild.BuildOptions{ChangeMode: "working"}
	manifest, err := syncbundlebuild.SnapshotWithOptions(context.Background(), ".", build)
	if err != nil {
		t.Fatal(err)
	}
	for _, cached := range []bool{false, true} {
		t.Run(map[bool]string{false: "full", true: "delta"}[cached], func(t *testing.T) {
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			uploadCalls, cancelCalls := 0, 0
			var session preflightapi.VerificationSession
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/v1/verification-sessions":
					var req preflightapi.CreateVerificationSessionRequest
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
					}
					session = preflightapi.VerificationSession{SchemaVersion: preflightapi.VerificationSchemaVersion, SessionID: "group_unexpected", State: preflightapi.VerificationStateSealed, Recipe: req.Recipe}
					writeCLITestJSON(w, 201, preflightapi.CreateVerificationSessionResponse{Session: session, SourceUploadMode: preflightapi.VerificationSourceUploadModeDirect, SourceUploadPrepareURL: "/v1/verification-sessions/group_unexpected/source-upload", SourceUploadBaselineSessionID: "group_not_requested"})
				case strings.HasSuffix(r.URL.Path, "/cancel"):
					cancelCalls++
					session.State = preflightapi.VerificationStateTerminal
					session.Verdict = preflightapi.VerificationVerdictCancelled
					writeCLITestJSON(w, 200, session)
				default:
					uploadCalls++
					http.Error(w, "upload must not be attempted", http.StatusBadRequest)
				}
			}))
			defer server.Close()
			if cached {
				rememberExecutionSource(server.URL, "review", manifest.BaselineKey, preflightapi.VerificationSession{SessionID: "group_requested", State: preflightapi.VerificationStateTerminal, Nodes: []preflightapi.VerificationNode{{Attempts: []preflightapi.VerificationAttempt{{RunID: "run_base"}}}}})
			}
			_, err := createAndWatchVerificationSession(verificationSessionOptions{APIURL: server.URL, APIToken: "pfl_fixture", ProjectID: "review", Purpose: preflightapi.ExecutionPurpose, Command: "true", Changes: "working", Prepared: &preparedVerificationSession{ProjectID: "review", BuildOptions: build, CoverageMode: "check", Plans: []checkRunPlan{{Name: "command", Options: remoteRunOptions{Command: "true", ChangeMode: "working"}}}}})
			if err == nil || !strings.Contains(err.Error(), "unexpected source upload baseline") || uploadCalls != 0 || cancelCalls != 1 {
				t.Fatalf("unexpected baseline did not fail before upload: error=%v uploads=%d cancellations=%d", err, uploadCalls, cancelCalls)
			}
		})
	}
}
