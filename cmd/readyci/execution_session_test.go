package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/DeanStr/readyci-cli/pkg/config"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

// Adapt existing successful-run fixtures to the new client boundary. The CLI
// must submit the complete graph and one source upload; only this simulated
// service creates child runs. Scheduler correctness is covered by store/API tests.
func successfulExecutionTestHandler(t *testing.T, childHandler http.Handler, acceptBaseline ...bool) http.Handler {
	t.Helper()
	t.Setenv("READYCI_API_TOKEN", "pfl_execution_fixture")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	var mu sync.Mutex
	var session preflightapi.VerificationSession
	var children []preflightapi.CreateRunResponse
	var uploaded []byte
	jobs := 0
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		// Legacy command fixtures model scheduling and uploads, without test output.
		if strings.HasSuffix(r.URL.Path, "/events") && r.URL.Query().Get("format") == "json" {
			runID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/runs/"), "/events")
			writeCLITestJSON(w, 200, preflightapi.RunEventsResponse{RunID: runID})
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/v1/verification-sessions") && r.URL.Path != "/execution-object" {
			childHandler.ServeHTTP(w, r)
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/verification-sessions":
			var request preflightapi.CreateVerificationSessionRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				http.Error(w, "bad request", 400)
				return
			}
			if request.Recipe.Purpose != preflightapi.ExecutionPurpose {
				t.Error("ordinary command did not request execution purpose")
			}
			normalized, err := preflightapi.NormalizeCreateVerificationSessionRequest(request)
			if err != nil {
				t.Error(err)
				http.Error(w, err.Error(), 400)
				return
			}
			jobs++
			session = preflightapi.VerificationSession{SchemaVersion: preflightapi.VerificationSchemaVersion, SessionID: fmt.Sprintf("group_execution_%d", jobs), ProjectID: request.ProjectID, Recipe: normalized.Recipe, Snapshot: request.Snapshot, Plan: normalized.Plan, State: preflightapi.VerificationStateSealed, Verdict: preflightapi.VerificationVerdictPending, Currentness: preflightapi.VerificationCurrentnessCurrent}
			children = nil
			uploaded = nil
			for _, node := range request.Plan.Nodes {
				encoded, _ := json.Marshal(node.Run)
				recorder := httptest.NewRecorder()
				childHandler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/runs", bytes.NewReader(encoded)))
				if recorder.Code != http.StatusCreated {
					t.Errorf("child fixture rejected node: %s", recorder.Body.String())
				}
				var child preflightapi.CreateRunResponse
				if err := json.Unmarshal(recorder.Body.Bytes(), &child); err != nil {
					t.Error(err)
				}
				children = append(children, child)
			}
			baselineID := ""
			if len(acceptBaseline) > 0 && acceptBaseline[0] {
				baselineID = request.Recipe.Source.UploadBaselineSessionID
			}
			writeCLITestJSON(w, 201, preflightapi.CreateVerificationSessionResponse{Session: session, SourceUploadMode: preflightapi.VerificationSourceUploadModeDirect, SourceUploadPrepareURL: "/v1/verification-sessions/" + session.SessionID + "/source-upload", SourceUploadBaselineSessionID: baselineID})
		case strings.HasSuffix(r.URL.Path, "/source-upload"):
			writeCLITestJSON(w, 200, preflightapi.PrepareVerificationSessionSourceUploadResponse{UploadURL: "http://" + r.Host + "/execution-object", UploadMethod: "PUT", UploadHeaders: map[string]string{"If-None-Match": "*"}, CompleteURL: "/v1/verification-sessions/" + session.SessionID + "/source-complete"})
		case r.Method == http.MethodPut && r.URL.Path == "/execution-object":
			if r.Header.Get("Authorization") != "" {
				t.Error("API token leaked to object storage")
			}
			var err error
			uploaded, err = io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			manifest, err := syncbundle.ValidateWithManifest(bytes.NewReader(uploaded))
			if err != nil {
				t.Error(err)
			}
			if manifest.SnapshotDigest != session.Snapshot.Digest {
				t.Error("uploaded a different snapshot")
			}
			for _, child := range children {
				parsed, _ := url.Parse(child.UploadURL)
				recorder := httptest.NewRecorder()
				childHandler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, parsed.RequestURI(), bytes.NewReader(uploaded)))
			}
			w.WriteHeader(200)
		case strings.HasSuffix(r.URL.Path, "/source-complete"):
			if len(uploaded) == 0 {
				t.Error("completed without source")
			}
			session.State = preflightapi.VerificationStateTerminal
			session.Verdict = preflightapi.VerificationVerdictChecksPassed
			session.Classification = "requested_checks"
			session.Nodes = nil
			for i, node := range session.Plan.Nodes {
				code := 0
				session.Nodes = append(session.Nodes, preflightapi.VerificationNode{ID: node.ID, Name: node.Name, Status: preflightapi.StatusSucceeded, Attempts: []preflightapi.VerificationAttempt{{RunID: children[i].RunID, Attempt: 1, Status: preflightapi.StatusSucceeded, ExitCode: &code}}})
			}
			writeCLITestJSON(w, 202, session)
		default:
			writeCLITestJSON(w, 200, session)
		}
	})
}

func TestRemovedCommandsFailExplicitly(t *testing.T) {
	for _, command := range []string{"init", "start", "touched"} {
		if err := run([]string{command}); err == nil || !strings.Contains(err.Error(), "unknown command") {
			t.Fatalf("%s: %v", command, err)
		}
	}
}

func TestExecutionSuccessDoesNotUpgradeVerification(t *testing.T) {
	execution := preflightapi.VerificationSession{Recipe: preflightapi.VerificationRecipe{Purpose: preflightapi.ExecutionPurpose}, State: preflightapi.VerificationStateTerminal, Verdict: preflightapi.VerificationVerdictChecksPassed}
	if err := executionSessionExit(execution); err != nil {
		t.Fatal(err)
	}
	if err := verificationVerdictExit(execution); err == nil {
		t.Fatal("checks passed became authoritative verification")
	}
	execution.Verdict = preflightapi.VerificationVerdictInsufficientEvidence
	if err := executionSessionExit(execution); err == nil {
		t.Fatal("insufficient evidence became execution success")
	}
}

func TestSetupRejectsInvalidRunFlagsBeforeWriting(t *testing.T) {
	chdirCLITestRepo(t, "invalid-setup")
	for _, flags := range [][]string{{"--run", "--json", "--write-config"}, {"--run", "--helper", "shell-syntax", "--write-helper"}, {"--run", "--makefile"}} {
		if err := setupCommand(flags); err == nil {
			t.Fatalf("accepted invalid flags: %v", flags)
		}
	}
	if _, err := os.Stat(".readyci.yml"); !os.IsNotExist(err) {
		t.Fatalf("invalid setup wrote config: %v", err)
	}
}

func TestDetectedSetupUsesExplicitPublicEgress(t *testing.T) {
	cfg, err := config.ParseProjectConfig([]byte(renderDetectedProjectTemplate("project", "", "remote-ci", []detectedCheck{{Name: "go", Command: "go test ./..."}}, nil, nil, nil)), "generated")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Network.Mode != "allowlist" || !containsString(cfg.Network.Allowlist, "proxy.golang.org") {
		t.Fatalf("generated network: %+v", cfg.Network)
	}
}

func TestExecutionReusesBaselineAndFallsBackToFullSource(t *testing.T) {
	for _, accept := range []bool{false, true} {
		t.Run(fmt.Sprint(accept), func(t *testing.T) {
			repo := chdirCLITestRepo(t, "delta-workspace")
			runGitForCLITest(t, "init")
			runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
			runGitForCLITest(t, "config", "user.name", "Ready CI")
			if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("baseline"), 0644); err != nil {
				t.Fatal(err)
			}
			runGitForCLITest(t, "add", ".")
			runGitForCLITest(t, "commit", "-m", "baseline")
			var deltas []bool
			var apiURL string
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/v1/runs":
					writeCLITestJSON(w, 201, preflightapi.CreateRunResponse{RunID: "run_test", UploadURL: apiURL + "/upload/run_test", Status: preflightapi.StatusAwaitingUpload})
				case r.Method == http.MethodPut:
					manifest, err := syncbundle.ValidateWithManifest(r.Body)
					if err != nil {
						t.Error(err)
					}
					deltas = append(deltas, manifest.BaselineRef)
					w.WriteHeader(202)
				default:
					http.NotFound(w, r)
				}
			})
			server := httptest.NewServer(successfulExecutionTestHandler(t, handler, accept))
			defer server.Close()
			apiURL = server.URL
			invoke := func() {
				if err := runCommand([]string{"--api-url", apiURL, "--project", "delta-test", "true"}); err != nil {
					t.Fatal(err)
				}
			}
			invoke()
			if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("edited"), 0644); err != nil {
				t.Fatal(err)
			}
			invoke()
			if len(deltas) != 2 || deltas[0] || deltas[1] != accept {
				t.Fatalf("delta transfers: %v accept=%t", deltas, accept)
			}
		})
	}
}
