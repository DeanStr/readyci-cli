package main

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/internal/api"
)

func TestRequestIdentifiersRejectControlCharacters(t *testing.T) {
	if _, err := executionProjectID("http://127.0.0.1:8080", "proj\nother"); err == nil {
		t.Fatal("project id with a newline was accepted")
	}
	if _, err := normalizeIdempotencyKey("key\x00other"); err == nil {
		t.Fatal("idempotency key with a NUL byte was accepted")
	}
	if got, err := normalizeIdempotencyKey(" task-1 "); err != nil || got != "task-1" {
		t.Fatalf("normalized idempotency key = %q, %v", got, err)
	}
}

func TestExecutionProjectIDFailsClosedForRemoteAPI(t *testing.T) {
	_, err := executionProjectID("https://api.readyci.com", "")
	if err == nil {
		t.Fatal("expected missing remote project id to fail")
	}
	var typed cliError
	if !errors.As(err, &typed) || typed.Code != "config_invalid" || typed.ExitCode != cliExitUsageOrConfig {
		t.Fatalf("error = %#v, want typed config error", err)
	}
}

func TestExecutionProjectIDRetainsLocalDevelopmentDefault(t *testing.T) {
	for _, endpoint := range []string{"http://localhost:8080", "http://127.0.0.1:8080", "http://[::1]:8080"} {
		got, err := executionProjectID(endpoint, "")
		if err != nil || got != "proj_dev" {
			t.Fatalf("executionProjectID(%q) = %q, %v", endpoint, got, err)
		}
	}
}

func TestExecutionProjectIDUsesExplicitRemoteIdentity(t *testing.T) {
	got, err := executionProjectID("https://api.readyci.com", " owner/repo ")
	if err != nil || got != "owner/repo" {
		t.Fatalf("executionProjectID = %q, %v", got, err)
	}
}

func TestEffectiveProjectIDDoesNotGuessRemoteProject(t *testing.T) {
	_ = chdirCLITestRepo(t, "")
	t.Setenv("READYCI_PROJECT_ID", "")
	if _, err := effectiveProjectID("https://api.readyci.test", ""); err == nil {
		t.Fatal("expected missing remote project id to fail")
	}
	got, err := effectiveProjectID("http://127.0.0.1:8080", "")
	if err != nil || got != "proj_dev" {
		t.Fatalf("local effective project = %q, %v", got, err)
	}
}

func TestCachePurgeFailsBeforeRemoteMutationWithoutProject(t *testing.T) {
	_ = chdirCLITestRepo(t, "")
	t.Setenv("READYCI_PROJECT_ID", "")
	err := cachePurgeCommand([]string{"--api-url", "https://api.readyci.test", "--token", "pfl_test"})
	if err == nil || !strings.Contains(err.Error(), "project id is required") {
		t.Fatalf("cache purge error = %v, want missing project rejection", err)
	}
}

// The CLI treats an endpoint as local only when the transport will accept it
// over plain HTTP; other spellings fail before any request instead.
func TestLocalAPIEndpointMatchesTransportLoopbackRule(t *testing.T) {
	for endpoint, want := range map[string]bool{
		"http://localhost:8080":       true,
		"http://LOCALHOST:8080":       false,
		"http://127.0.0.1:8080":       true,
		"http://[::1]:8080":           true,
		"http://localhost.:8080":      false,
		"https://api.readyci.example": false,
	} {
		if got := localAPIEndpoint(endpoint); got != want {
			t.Fatalf("localAPIEndpoint(%q) = %t, want %t", endpoint, got, want)
		}
		parsed, err := url.Parse(endpoint)
		if err != nil {
			t.Fatal(err)
		}
		if want && api.ValidateEndpoint(parsed) != nil {
			t.Fatalf("transport rejects local endpoint %q", endpoint)
		}
	}
}
