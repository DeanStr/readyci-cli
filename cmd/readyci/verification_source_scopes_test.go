package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/internal/syncbundlebuild"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func TestVerifyAffectedComponentsUsesRegisteredScopesInOneRequest(t *testing.T) {
	for _, registered := range []bool{true, false} {
		t.Run(map[bool]string{true: "registered", false: "missing policy"}[registered], func(t *testing.T) {
			chdirCLITestRepo(t, "registered-component-scopes")
			runGitForCLITest(t, "init")
			cfg := strings.Replace(componentDependencyCLIConfig, "affected:\n", "  rust-test:\n    command: cargo test --offline\n    sync: {exclude: [apps/web]}\naffected:\n", 1)
			cfg += "    - paths: [apps/server/**]\n      checks: [rust-test]\n"
			writeFileForRepoMapTest(t, ".readyci.yml", cfg)
			writeFileForRepoMapTest(t, "apps/web/test.ts", "web source\n")
			writeFileForRepoMapTest(t, "apps/server/main.rs", "server source\n")
			t.Setenv("READYCI_API_TOKEN", "pfl_component_test")
			digest := "sha256:" + strings.Repeat("a", 64)
			var scopes []syncbundle.SourcePolicyBinding
			for _, excluded := range []string{"apps/server", "apps/web"} {
				scopes = append(scopes, syncbundle.SourcePolicyBinding{Digest: digest, ExcludePatterns: []string{excluded}, DefaultExcludePatterns: syncbundle.DefaultSourceExcludePatterns(), DefaultUntrackedExcludePatterns: syncbundle.DefaultUntrackedExcludePatterns(), IncludeUntracked: true})
			}
			binding, err := syncbundle.NormalizeSourcePolicyBinding(syncbundle.SourcePolicyBinding{Digest: digest, Scopes: scopes})
			if err != nil {
				t.Fatal(err)
			}
			var requests []preflightapi.CreateVerificationSessionRequest
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/verification-policy/source-binding"):
					if r.URL.Query().Get("check") != "" {
						t.Error("combined verification narrowed to an explicit check")
					}
					response := preflightapi.ProjectVerificationSourcePolicyResponse{SchemaVersion: preflightapi.ProjectVerificationSourcePolicySchemaVersion, ProjectID: "review"}
					if registered {
						response.Active = &binding
					}
					writeCLITestJSON(w, http.StatusOK, response)
				case r.Method == http.MethodPost && r.URL.Path == "/v1/verification-sessions":
					var request preflightapi.CreateVerificationSessionRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					requests = append(requests, request)
					http.Error(w, "creation captured", http.StatusForbidden)
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
			}))
			defer server.Close()
			err = verificationCommand([]string{"--api-url", server.URL, "--progress=quiet"})
			if !registered {
				if err == nil || !strings.Contains(err.Error(), "active trusted verification policy") || len(requests) != 0 {
					t.Fatalf("unregistered mixed scopes = %v, requests=%+v", err, requests)
				}
				return
			}
			if err == nil || len(requests) != 1 {
				t.Fatalf("one-command verification = %v, requests=%+v", err, requests)
			}
			request := requests[0]
			if len(request.Plan.Nodes) != 4 || request.Recipe.Check != "" || len(request.Recipe.Source.SourceScopes) != 2 || len(request.Snapshot.ChangedFiles) != 3 {
				t.Fatalf("component request = %+v", request)
			}
			request, err = preflightapi.NormalizeCreateVerificationSessionRequest(request)
			if err != nil {
				t.Fatal(err)
			}
			options, err := verificationSourceBuildOptions(request.Recipe)
			if err != nil {
				t.Fatal(err)
			}
			current, err := syncbundlebuild.SnapshotWithOptions(context.Background(), ".", options)
			if err != nil || current.SnapshotDigest != request.Snapshot.Digest {
				t.Fatalf("retained scope recipe changed currentness: %s %v", current.SnapshotDigest, err)
			}
		})
	}
}

func TestSyncRegisteredPreviewUsesExactCheckView(t *testing.T) {
	chdirCLITestRepo(t, "registered-digest-preview")
	runGitForCLITest(t, "init")
	writeFileForRepoMapTest(t, ".readyciignore", "component/\n")
	writeFileForRepoMapTest(t, "component/source.go", "package component\n")
	writeFileForRepoMapTest(t, "installer/setup.sh", "true\n")
	digest := "sha256:" + strings.Repeat("a", 64)
	check := syncbundle.SourcePolicyBinding{Digest: digest, ExcludePatterns: []string{"installer"}, IncludeUntracked: true}
	installer := syncbundle.SourcePolicyBinding{Digest: digest, ExcludePatterns: []string{"component"}, IncludeUntracked: true}
	check, err := syncbundle.NormalizeSourcePolicyBinding(check)
	if err != nil {
		t.Fatal(err)
	}
	combined, err := syncbundle.NormalizeSourcePolicyBinding(syncbundle.SourcePolicyBinding{Digest: digest, Scopes: []syncbundle.SourcePolicyBinding{check, installer}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Query().Get("check") != "component" || !strings.HasSuffix(r.URL.Path, "/verification-policy/source-binding") {
			t.Errorf("unexpected preview request: %s %s", r.Method, r.URL.Path)
		}
		writeCLITestJSON(w, http.StatusOK, preflightapi.ProjectVerificationSourcePolicyResponse{Active: &combined, CheckSource: &check})
	}))
	defer server.Close()
	out := requireCLITestCommandOutputContains(t, "registered preview", func() error {
		return syncCommand([]string{"--dry-run", "--registered", "--check", "component", "--project", "review", "--api-url", server.URL, "--token", "pfl_test", "--json"})
	}, "snapshot_digest")
	var report syncDryRunReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err)
	}
	options, err := registeredPolicyBuildOptions(syncbundlebuild.BuildOptions{ChangeMode: "working"}, &combined)
	if err != nil {
		t.Fatal(err)
	}
	options.SealSourceViews = true
	built, err := syncbundlebuild.BuildWithOptions(t.Context(), ".", io.Discard, options)
	if err != nil {
		t.Fatal(err)
	}
	view := built.SourceViews[syncbundle.SourcePolicyScopeKey(check)]
	if report.SnapshotDigest == built.Manifest.SnapshotDigest || report.SnapshotDigest != view.SnapshotDigest || !slices.Contains(report.WorkspaceFiles, "component/source.go") || slices.Contains(report.WorkspaceFiles, "installer/setup.sh") {
		t.Fatalf("preview %s, check %s, combined %s, files %v", report.SnapshotDigest, view.SnapshotDigest, built.Manifest.SnapshotDigest, report.WorkspaceFiles)
	}
}

// A session uploads the active policy's combined source, so a preview must not
// print an approval digest for a workspace that upload rejects.
func TestSyncRegisteredPreviewFailsWhereTheSessionUploadFails(t *testing.T) {
	chdirCLITestRepo(t, "registered-digest-rejected")
	runGitForCLITest(t, "init")
	writeFileForRepoMapTest(t, "component/tracked.go", "package component\n")
	writeFileForRepoMapTest(t, "installer/setup.sh", "true\n")
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "-c", "user.email=test@example.invalid", "-c", "user.name=test", "commit", "-q", "-m", "files")
	runGitForCLITest(t, "rm", "--cached", "-q", "component/tracked.go")
	digest := "sha256:" + strings.Repeat("a", 64)
	check, err := syncbundle.NormalizeSourcePolicyBinding(syncbundle.SourcePolicyBinding{Digest: digest, ExcludePatterns: []string{"installer"}})
	if err != nil {
		t.Fatal(err)
	}
	installer, err := syncbundle.NormalizeSourcePolicyBinding(syncbundle.SourcePolicyBinding{Digest: digest, IncludeUntracked: true})
	if err != nil {
		t.Fatal(err)
	}
	combined, err := syncbundle.NormalizeSourcePolicyBinding(syncbundle.SourcePolicyBinding{Digest: digest, Scopes: []syncbundle.SourcePolicyBinding{check, installer}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeCLITestJSON(w, http.StatusOK, preflightapi.ProjectVerificationSourcePolicyResponse{Active: &combined, CheckSource: &check})
	}))
	defer server.Close()
	out, err := captureStdoutForCLITest(t, func() error {
		return syncCommand([]string{"--dry-run", "--registered", "--check", "component", "--project", "review", "--api-url", server.URL, "--token", "pfl_test", "--json"})
	})
	if err == nil || !strings.Contains(err.Error(), `untracked source path "component/tracked.go"`) || strings.Contains(out, "snapshot_digest") {
		t.Fatalf("preview err=%v out=%s, want the session upload's rejection and no digest", err, out)
	}
}
