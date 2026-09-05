package main

import (
	"encoding/json"
	"errors"
	"flag"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestAgentTokenCreateUsesPresetScopesAndTTL(t *testing.T) {
	var created preflightapi.CreateAPITokenRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer pfl_parent" {
			t.Fatalf("authorization = %q, want parent bearer token", got)
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1/account/tokens" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
			t.Fatalf("decode token create request: %v", err)
		}
		writeCLITestJSON(w, http.StatusCreated, preflightapi.CreateAPITokenResponse{
			Token: preflightapi.APITokenSummary{
				ID:        "token_agent",
				Name:      created.Name,
				Scopes:    created.Scopes,
				ExpiresAt: created.ExpiresAt,
				CreatedAt: time.Date(2026, 6, 22, 5, 0, 0, 0, time.UTC),
			},
			Secret: "pfl_agent_secret",
		})
	}))
	defer server.Close()

	output, err := captureStdoutForCLITest(t, func() error {
		return agentTokenCreateCommand([]string{
			"--api-url", server.URL,
			"--token", "pfl_parent",
			"--preset", "verify-only",
			"--name", "Codex agent",
			"--ttl", "30d",
			"--json",
		})
	})
	if err != nil {
		t.Fatalf("agent token create: %v", err)
	}
	var resp preflightapi.CreateAPITokenResponse
	if err := json.Unmarshal([]byte(output), &resp); err != nil {
		t.Fatalf("decode token create response: %v\n%s", err, output)
	}
	if resp.Secret != "pfl_agent_secret" || resp.Token.ID != "token_agent" {
		t.Fatalf("token response = %+v, want created agent token", resp)
	}
	if created.Name != "Codex agent" {
		t.Fatalf("token name = %q, want Codex agent", created.Name)
	}
	for _, forbidden := range []string{"runs:create_with_secrets", "cache:purge", "projects:write"} {
		if containsString(created.Scopes, forbidden) {
			t.Fatalf("verify-only scopes included %s: %v", forbidden, created.Scopes)
		}
	}
	for _, required := range []string{"runs:create", "runs:read", "cache:read", "projects:read", "account:read"} {
		if !containsString(created.Scopes, required) {
			t.Fatalf("verify-only scopes missing %s: %v", required, created.Scopes)
		}
	}
	if created.ExpiresAt == nil || time.Until(*created.ExpiresAt) < 29*24*time.Hour {
		t.Fatalf("expires_at = %v, want roughly 30 days from now", created.ExpiresAt)
	}
}

func TestAgentTokenRemovedCompatibilityPresetIsUnavailable(t *testing.T) {
	if _, ok := agentTokenPresets["verify-with-artifacts"]; ok {
		t.Fatal("removed verify-with-artifacts compatibility preset is still available")
	}
}

func TestAgentTokenPresetsCannotWriteVerificationPolicy(t *testing.T) {
	for name, scopes := range agentTokenPresets {
		if containsString(scopes, "policies:write") {
			t.Fatalf("agent token preset %q can mutate independently trusted verification policy: %v", name, scopes)
		}
	}
}

func TestAgentTokenVerifyWithFeedbackPresetAddsFeedbackScope(t *testing.T) {
	scopes, ok := agentTokenPresets["verify-with-feedback"]
	if !ok {
		t.Fatal("verify-with-feedback preset missing")
	}
	for _, required := range []string{"runs:create", "runs:read", "cache:read", "projects:read", "account:read", "feedback:write"} {
		if !containsString(scopes, required) {
			t.Fatalf("verify-with-feedback scopes missing %s: %v", required, scopes)
		}
	}
	for _, forbidden := range []string{"runs:create_with_secrets", "cache:purge", "projects:write", "secrets:write"} {
		if containsString(scopes, forbidden) {
			t.Fatalf("verify-with-feedback scopes included %s: %v", forbidden, scopes)
		}
	}
}

func TestAgentTokenCreatePrintsHumanOutputAndRequestDefaults(t *testing.T) {
	var created preflightapi.CreateAPITokenRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/account/tokens" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
			t.Fatalf("decode token create request: %v", err)
		}
		writeCLITestJSON(w, http.StatusCreated, preflightapi.CreateAPITokenResponse{
			Token: preflightapi.APITokenSummary{
				ID:        "token_human",
				Name:      created.Name,
				Scopes:    created.Scopes,
				CreatedAt: time.Date(2026, 6, 22, 5, 0, 0, 0, time.UTC),
			},
			Secret: "pfl_human_secret",
		})
	}))
	defer server.Close()

	output, err := captureStdoutForCLITest(t, func() error {
		return agentTokenCommand([]string{"create", "--api-url", server.URL, "--token", "pfl_parent"})
	})
	if err != nil {
		t.Fatalf("agent token create: %v", err)
	}
	requireCLITestOutputContains(t, output,
		"token_id: token_human",
		"preset: verify-only",
		"secret: pfl_human_secret",
	)
	if created.Name != "Ready CI verification agent" {
		t.Fatalf("token name = %q, want default agent name", created.Name)
	}
	if created.ExpiresAt != nil {
		t.Fatalf("expires_at = %v, want no default expiry", created.ExpiresAt)
	}
}

func TestAgentTokenCommandValidationErrors(t *testing.T) {
	if err := agentTokenCommand(nil); err == nil || !strings.Contains(err.Error(), "usage: readyci agent token") {
		t.Fatalf("agentTokenCommand(nil) error = %v, want usage", err)
	}
	if err := agentTokenCommand([]string{"rotate"}); err == nil || !strings.Contains(err.Error(), `unknown agent token command "rotate"`) {
		t.Fatalf("agentTokenCommand(rotate) error = %v, want unknown command", err)
	}
	err := agentTokenCreateCommand([]string{"--preset", "adminish"})
	if err == nil || !strings.Contains(err.Error(), `unknown agent token preset "adminish"`) || !strings.Contains(err.Error(), "verify-only") {
		t.Fatalf("unknown preset error = %v, want available presets", err)
	}
	if err := agentTokenCreateCommand([]string{"extra"}); err == nil || !strings.Contains(err.Error(), "usage: readyci agent token create") {
		t.Fatalf("extra arg error = %v, want usage", err)
	}
}

func TestAgentTokenCreateFlagUsageDoesNotPrintEnvToken(t *testing.T) {
	t.Setenv("READYCI_API_TOKEN", "pfl_secret_usage_token")
	stderr, err := captureStderrForCLITest(t, func() error {
		return agentTokenCreateCommand([]string{"--definitely-not-a-real-flag"})
	})
	if err == nil {
		t.Fatal("agent token create with invalid flag succeeded, want error")
	}
	if strings.Contains(stderr, "pfl_secret_usage_token") {
		t.Fatalf("stderr leaked READYCI_API_TOKEN:\n%s", stderr)
	}
	if !strings.Contains(stderr, "-token") {
		t.Fatalf("stderr = %q, want token flag usage", stderr)
	}
}

func TestParseAgentTTL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want time.Duration
	}{
		{name: "days", in: "14d", want: 14 * 24 * time.Hour},
		{name: "duration", in: "12h", want: 12 * time.Hour},
		{name: "trim", in: " 30m ", want: 30 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAgentTTL(tt.in)
			if err != nil {
				t.Fatalf("parseAgentTTL(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("parseAgentTTL(%q) = %s, want %s", tt.in, got, tt.want)
			}
		})
	}
	for _, value := range []string{"", "0d", "-1h", "soon"} {
		if _, err := parseAgentTTL(value); err == nil {
			t.Fatalf("parseAgentTTL(%q) succeeded, want error", value)
		}
	}
}

func TestSortedAgentTokenPresets(t *testing.T) {
	names := sortedAgentTokenPresets()
	if len(names) == 0 {
		t.Fatal("sortedAgentTokenPresets returned no presets")
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Fatalf("presets are not sorted: %v", names)
		}
	}
	if !containsString(names, "verify-only") {
		t.Fatalf("presets = %v, want verify-only", names)
	}
}

func TestAgentTokenCreateRequiresAuthenticatedClient(t *testing.T) {
	err := agentTokenCreateCommand([]string{"--api-url", "http://127.0.0.1:1", "--token", ""})
	if err == nil {
		t.Fatal("agent token create without token succeeded, want auth error")
	}
	if errors.Is(err, flag.ErrHelp) {
		t.Fatalf("agent token create returned flag help, want auth error")
	}
}

func TestAgentTokenCreateExplainsMissingTokenWriteScope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/account/tokens" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"bearer token missing required scope: tokens:write"}`))
	}))
	defer server.Close()

	err := agentTokenCreateCommand([]string{"--api-url", server.URL, "--token", "pfl_parent"})
	if err == nil {
		t.Fatal("agent token create succeeded, want missing-scope error")
	}
	for _, want := range []string{"tokens:write", "owner/admin", "dashboard"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("agent token error = %v, want %q", err, want)
		}
	}
}
