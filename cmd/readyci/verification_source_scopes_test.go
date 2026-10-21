package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
