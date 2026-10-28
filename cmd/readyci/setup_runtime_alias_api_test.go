package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestSetupAliasUsesServicePolicyAfterCompiledCatalogExpires(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/runtime-aliases/resolve" || r.Method != http.MethodPost || r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected alias request: %s %s", r.Method, r.URL.Path)
		}
		var request preflightapi.RuntimeAliasRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.Runtime != "node" || request.Selector != "lts/*" || request.ActionRef != "actions/setup-node@future-reviewed-revision" {
			t.Errorf("lost source policy: %+v", request)
		}
		json.NewEncoder(w).Encode(preflightapi.RuntimeResolution{Selector: "lts/*", Policy: preflightapi.RuntimeResolutionPolicySetupNodeLTSImport, Requirement: "26", CatalogDigest: "sha256:" + strings.Repeat("a", 64)})
	}))
	defer server.Close()
	withReviewedAliasClock(t, preflightapi.NodeRuntimeAliasCatalogMetadata().ValidBefore.Add(48*time.Hour))
	prior := setupRuntimeAliasResolve
	setupRuntimeAliasResolve = setupAPIRuntimeAliasResolver(server.URL)
	t.Cleanup(func() { setupRuntimeAliasResolve = prior })
	job := repoWorkflowJobYAML{Steps: []repoWorkflowStepYAML{{Uses: "actions/setup-node@future-reviewed-revision", With: map[string]any{"node-version": "lts/*"}}}}
	for range 2 {
		requirements, resolutions, problems := setupWorkflowRuntimeRequirements(".", job)
		if len(problems) > 0 || requirements["node"] != "26" || resolutions["node"].CatalogDigest == preflightapi.NodeRuntimeAliasCatalogDigest() {
			t.Fatalf("service policy not used: %v %v %v", requirements, resolutions, problems)
		}
	}
	if calls != 1 {
		t.Fatalf("detection requested alias policy %d times", calls)
	}
}

func TestSetupAliasServiceFailureNeverUsesCompiledFallback(t *testing.T) {
	for _, response := range []string{"unavailable", `{"selector":"lts/*","policy":"unknown","requirement":"24"}`} {
		t.Run(response, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if response == "unavailable" {
					w.WriteHeader(503)
				}
				w.Write([]byte(response))
			}))
			defer server.Close()
			resolver := setupAPIRuntimeAliasResolver(server.URL)
			_, err := resolver(preflightapi.RuntimeAliasRequest{Runtime: "node", Selector: "lts/*", ActionRef: preflightapi.NodeRuntimeAliasCatalogMetadata().ReviewedActionRef}, preflightapi.NodeRuntimeAliasCatalogMetadata().ReviewedAt)
			if err == nil {
				t.Fatal("unavailable or invalid service response used a fallback")
			}
		})
	}
}
