package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestAdminTokensCreateDefaultScopesIncludeFeedbackAndExcludeRetiredPrivacyScopes(t *testing.T) {
	var created preflightapi.CreateAPITokenRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/admin/tokens" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
			t.Fatalf("decode token create request: %v", err)
		}
		writeCLITestJSON(w, http.StatusCreated, preflightapi.CreateAPITokenResponse{
			Token: preflightapi.APITokenSummary{
				ID:        "token_admin",
				Name:      created.Name,
				Scopes:    created.Scopes,
				CreatedAt: time.Date(2026, 6, 22, 5, 0, 0, 0, time.UTC),
			},
			Secret: "pfl_admin_secret",
		})
	}))
	defer server.Close()

	if _, err := captureStdoutForCLITest(t, func() error {
		return adminTokensCreateCommand([]string{"--api-url", server.URL, "--token", "admin_test"})
	}); err != nil {
		t.Fatalf("admin tokens create: %v", err)
	}
	if !containsString(created.Scopes, "feedback:write") {
		t.Fatalf("default admin token scopes missing feedback:write: %v", created.Scopes)
	}
	for _, retired := range []string{"privacy:read", "privacy:write", "privacy:*"} {
		if containsString(created.Scopes, retired) {
			t.Fatalf("default admin token scopes include retired scope %q: %v", retired, created.Scopes)
		}
	}
}
