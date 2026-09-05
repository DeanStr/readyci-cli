package main

import (
	"errors"
	"strings"
	"testing"
)

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
