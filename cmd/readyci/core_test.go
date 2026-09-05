package main

import (
	"errors"
	"flag"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestCommandFromArgsPreservesSingleShellString(t *testing.T) {
	got := commandFromArgs([]string{"go test ./... && echo done"})
	want := "go test ./... && echo done"
	if got != want {
		t.Fatalf("command = %q, want %q", got, want)
	}
}

func TestCommandFromArgsShellQuotesMultipleArgs(t *testing.T) {
	got := commandFromArgs([]string{"printf", "%s\\n", "a b", "can't"})
	want := "printf '%s\\n' 'a b' 'can'\\''t'"
	if got != want {
		t.Fatalf("command = %q, want %q", got, want)
	}
}

func TestWriteLogTruncationNoticePrintsMessage(t *testing.T) {
	output, err := captureStderrForCLITest(t, func() error {
		return writeLogTruncationNotice(preflightapi.RunEvent{
			Type:    preflightapi.EventLogTruncated,
			Message: "logs were truncated",
		})
	})
	if err != nil {
		t.Fatalf("write truncation notice: %v", err)
	}
	if !strings.Contains(output, "logs were truncated") {
		t.Fatalf("stderr = %q, want truncation notice", output)
	}
}

func TestShutdownSignalsIncludeSIGTERM(t *testing.T) {
	signals := shutdownSignals()
	if !hasSignal(signals, os.Interrupt) {
		t.Fatal("shutdown signals missing os.Interrupt")
	}
	if !hasSignal(signals, syscall.SIGTERM) {
		t.Fatal("shutdown signals missing SIGTERM")
	}
}

func TestEnvDefaultReadsReadyCIEnv(t *testing.T) {
	t.Setenv("READYCI_API_URL", "https://api.readyci.test")
	if got := envDefault("READYCI_API_URL", "fallback"); got != "https://api.readyci.test" {
		t.Fatalf("envDefault = %q", got)
	}
}

func TestEnvDefaultFallsBackWhenReadyCIEnvUnset(t *testing.T) {
	t.Setenv("READYCI_API_URL", "")

	if got := envDefault("READYCI_API_URL", "fallback"); got != "fallback" {
		t.Fatalf("envDefault fallback = %q", got)
	}
}

func TestDefaultAPIURLUsesLocalhostForDevBuilds(t *testing.T) {
	originalVersion := version
	originalReleaseDefault := releaseDefaultAPIURL
	t.Cleanup(func() {
		version = originalVersion
		releaseDefaultAPIURL = originalReleaseDefault
	})
	version = "0.1.0-dev"
	releaseDefaultAPIURL = ""

	if got := defaultAPIURL(); got != localDefaultAPIURL {
		t.Fatalf("default API URL for dev build = %q, want %q", got, localDefaultAPIURL)
	}
}

func TestDefaultAPIURLUsesPublicOriginForReleaseBuilds(t *testing.T) {
	originalVersion := version
	originalReleaseDefault := releaseDefaultAPIURL
	t.Cleanup(func() {
		version = originalVersion
		releaseDefaultAPIURL = originalReleaseDefault
	})
	version = "v0.1.0"
	releaseDefaultAPIURL = ""

	if got := defaultAPIURL(); got != publicDefaultAPIURL {
		t.Fatalf("default API URL for release build = %q, want %q", got, publicDefaultAPIURL)
	}
}

func TestDefaultAPIURLHonorsReleaseOverride(t *testing.T) {
	originalVersion := version
	originalReleaseDefault := releaseDefaultAPIURL
	t.Cleanup(func() {
		version = originalVersion
		releaseDefaultAPIURL = originalReleaseDefault
	})
	version = "0.1.0-dev"
	releaseDefaultAPIURL = " https://api.override.test "

	if got := defaultAPIURL(); got != "https://api.override.test" {
		t.Fatalf("default API URL with release override = %q", got)
	}
}

func TestRunDashboardURLTargetsOperatorRoute(t *testing.T) {
	got := runDashboardURL(" https://dash.example/ ", "run/with space")
	want := "https://dash.example/ops/runs?run=run%2Fwith+space"
	if got != want {
		t.Fatalf("runDashboardURL() = %q, want %q", got, want)
	}

	got = runDashboardURL(" https://user:pass@dash.example/app?token=secret#fragment ", "run_secret")
	want = "https://dash.example/app/ops/runs?run=run_secret"
	if got != want {
		t.Fatalf("runDashboardURL() with sensitive base = %q, want %q", got, want)
	}
	for _, notWant := range []string{"user:pass", "token=secret", "#fragment"} {
		if strings.Contains(got, notWant) {
			t.Fatalf("runDashboardURL() leaked %q in %q", notWant, got)
		}
	}
}

func TestCommandRoutersReturnUsageBeforeSideEffects(t *testing.T) {
	tests := []struct {
		name string
		run  func() error
		want string
	}{
		{name: "root unknown", run: func() error { return run([]string{"unknown"}) }, want: "unknown command"},
		{name: "project", run: func() error { return projectCommand(nil) }, want: "readyci project"},
		{name: "secrets", run: func() error { return secretsCommand(nil) }, want: "readyci secrets"},
		{name: "runs", run: func() error { return runsCommand(nil) }, want: "readyci runs"},
		{name: "groups", run: func() error { return groupsCommand(nil) }, want: "readyci groups"},
		{name: "artifacts", run: func() error { return artifactsCommand(nil) }, want: "readyci artifacts"},
		{name: "cache", run: func() error { return cacheCommand(nil) }, want: "readyci cache"},
		{name: "fixtures", run: func() error { return fixturesCommand(nil) }, want: "readyci fixtures"},
		{name: "admin", run: func() error { return adminCommand(nil) }, want: "readyci admin"},
		{name: "admin queue", run: func() error { return adminQueueCommand(nil) }, want: "readyci admin queue"},
		{name: "admin tokens", run: func() error { return adminTokensCommand(nil) }, want: "readyci admin tokens"},
		{name: "admin teams", run: func() error { return adminTeamsCommand(nil) }, want: "readyci admin teams"},
		{name: "admin workers", run: func() error { return adminWorkersCommand([]string{"unknown"}) }, want: "readyci admin workers"},
		{name: "admin storage", run: func() error { return adminStorageCommand(nil) }, want: "readyci admin storage"},
		{name: "agent token", run: func() error { return agentTokenCommand(nil) }, want: "readyci agent token"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := captureStdoutForCLITest(t, tc.run)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%s error = %v, want %q", tc.name, err, tc.want)
			}
		})
	}
}

func TestRootHelpAliasesPrintUsageSuccessfully(t *testing.T) {
	for _, alias := range []string{"help", "--help", "-h"} {
		t.Run(alias, func(t *testing.T) {
			output, err := captureStderrForCLITest(t, func() error {
				return run([]string{alias})
			})
			if err != nil {
				t.Fatalf("run(%q): %v", alias, err)
			}
			requireCLITestOutputContains(t, output, "usage:", "readyci verify", "readyci version")
		})
	}
}

func TestSubcommandHelpIsRecognizedAsSuccessfulHelp(t *testing.T) {
	output, err := captureStderrForCLITest(t, func() error {
		return checkCommand([]string{"-h"})
	})
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("check -h error = %v, want flag.ErrHelp", err)
	}
	requireCLITestOutputContains(t, output, "Usage of check:", "-dry-run", "-why")
}

func TestAdminCommandRoutersValidateSubcommandsBeforeNetwork(t *testing.T) {
	tests := []struct {
		name string
		run  func() error
		want string
	}{
		{name: "admin unknown", run: func() error { return adminCommand([]string{"mystery"}) }, want: `unknown admin command "mystery"`},
		{name: "admin runs unexpected arg", run: func() error { return adminCommand([]string{"runs", "extra"}) }, want: "readyci admin runs"},
		{name: "admin inspect missing run", run: func() error { return adminCommand([]string{"inspect"}) }, want: "readyci admin inspect"},
		{name: "admin events missing run", run: func() error { return adminCommand([]string{"events"}) }, want: "readyci admin events"},
		{name: "admin logs missing run", run: func() error { return adminCommand([]string{"logs"}) }, want: "readyci admin logs"},
		{name: "admin cancel missing run", run: func() error { return adminCommand([]string{"cancel"}) }, want: "readyci admin cancel"},
		{name: "admin cancel missing reason", run: func() error { return adminCommand([]string{"cancel", "run_test"}) }, want: "requires --reason"},
		{name: "queue unknown", run: func() error { return adminCommand([]string{"queue", "mystery"}) }, want: `unknown admin queue command "mystery"`},
		{name: "queue status unexpected arg", run: func() error { return adminCommand([]string{"queue", "status", "extra"}) }, want: "readyci admin queue status"},
		{name: "queue explain missing run", run: func() error { return adminCommand([]string{"queue", "explain"}) }, want: "readyci admin queue explain"},
		{name: "tokens unknown", run: func() error { return adminCommand([]string{"tokens", "mystery"}) }, want: `unknown admin tokens command "mystery"`},
		{name: "tokens list unexpected arg", run: func() error { return adminCommand([]string{"tokens", "list", "extra"}) }, want: "readyci admin tokens list"},
		{name: "tokens create unexpected arg", run: func() error { return adminCommand([]string{"tokens", "create", "extra"}) }, want: "readyci admin tokens create"},
		{name: "tokens revoke missing id", run: func() error { return adminCommand([]string{"tokens", "revoke"}) }, want: "readyci admin tokens revoke"},
		{name: "teams unknown", run: func() error { return adminCommand([]string{"teams", "mystery"}) }, want: `unknown admin teams command "mystery"`},
		{name: "teams list unexpected arg", run: func() error { return adminCommand([]string{"teams", "list", "extra"}) }, want: "readyci admin teams list"},
		{name: "teams disable missing id", run: func() error { return adminCommand([]string{"teams", "disable"}) }, want: "readyci admin teams disable"},
		{name: "teams disable missing reason", run: func() error { return adminCommand([]string{"teams", "disable", "team_test"}) }, want: "requires --reason"},
		{name: "teams cancel missing id", run: func() error { return adminCommand([]string{"teams", "cancel-runs"}) }, want: "readyci admin teams cancel-runs"},
		{name: "teams cancel missing reason", run: func() error { return adminCommand([]string{"teams", "cancel-runs", "team_test"}) }, want: "requires --reason"},
		{name: "workers unknown", run: func() error { return adminCommand([]string{"workers", "mystery"}) }, want: "readyci admin workers"},
		{name: "workers list unexpected arg", run: func() error { return adminCommand([]string{"workers", "list", "extra"}) }, want: "readyci admin workers"},
		{name: "workers drain missing id", run: func() error { return adminCommand([]string{"workers", "drain"}) }, want: "readyci admin workers drain"},
		{name: "workers drain missing reason", run: func() error { return adminCommand([]string{"workers", "drain", "worker_test"}) }, want: "requires --reason"},
		{name: "workers demote release missing id", run: func() error { return adminCommand([]string{"workers", "demote-release"}) }, want: "readyci admin workers demote-release"},
		{name: "workers demote release missing reason", run: func() error { return adminCommand([]string{"workers", "demote-release", "worker_test"}) }, want: "requires --reason"},
		{name: "workers undrain missing id", run: func() error { return adminCommand([]string{"workers", "undrain"}) }, want: "readyci admin workers undrain"},
		{name: "workers delete missing id", run: func() error { return adminCommand([]string{"workers", "delete"}) }, want: "readyci admin workers delete"},
		{name: "workers delete missing reason", run: func() error { return adminCommand([]string{"workers", "delete", "worker_test"}) }, want: "requires --reason"},
		{name: "storage unknown", run: func() error { return adminCommand([]string{"storage", "mystery"}) }, want: `unknown admin storage command "mystery"`},
		{name: "storage audit unexpected arg", run: func() error { return adminCommand([]string{"storage", "audit", "extra"}) }, want: "readyci admin storage audit"},
		{name: "storage cleanup unexpected arg", run: func() error { return adminCommand([]string{"storage", "cleanup-expired-artifacts", "extra"}) }, want: "readyci admin storage cleanup-expired-artifacts"},
		{name: "storage cache lane cleanup unexpected arg", run: func() error { return adminCommand([]string{"storage", "cleanup-cache-lanes", "extra"}) }, want: "readyci admin storage cleanup-cache-lanes"},
		{name: "storage status unexpected arg", run: func() error { return adminCommand([]string{"storage", "cleanup-status", "extra"}) }, want: "readyci admin storage cleanup-status"},
		{name: "storage cancel unexpected arg", run: func() error { return adminCommand([]string{"storage", "cleanup-cancel", "extra"}) }, want: "readyci admin storage cleanup-cancel"},
		{name: "storage cancel missing reason", run: func() error { return adminCommand([]string{"storage", "cleanup-cancel"}) }, want: "requires --reason"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := captureStdoutForCLITest(t, tc.run)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%s error = %v, want %q", tc.name, err, tc.want)
			}
		})
	}
}

func TestRunsAndGroupsRoutersValidateSubcommandsBeforeNetwork(t *testing.T) {
	tests := []struct {
		name string
		run  func() error
		want string
	}{
		{name: "runs unknown", run: func() error { return runsCommand([]string{"mystery"}) }, want: `unknown runs command "mystery"`},
		{name: "runs latest unexpected arg", run: func() error { return runsCommand([]string{"latest", "extra"}) }, want: "readyci runs latest"},
		{name: "runs status missing run", run: func() error { return runsCommand([]string{"status"}) }, want: "readyci runs status"},
		{name: "runs explain missing run", run: func() error { return runsCommand([]string{"explain"}) }, want: "readyci agent explain"},
		{name: "runs logs missing run", run: func() error { return runsCommand([]string{"logs"}) }, want: "readyci runs logs"},
		{name: "runs artifacts missing run", run: func() error { return runsCommand([]string{"artifacts"}) }, want: "readyci runs artifacts"},
		{name: "groups unknown", run: func() error { return groupsCommand([]string{"mystery"}) }, want: `unknown groups command "mystery"`},
		{name: "groups latest unexpected arg", run: func() error { return groupsCommand([]string{"latest", "extra"}) }, want: "readyci groups latest"},
		{name: "groups status missing group", run: func() error { return groupsCommand([]string{"status"}) }, want: "readyci groups status"},
		{name: "groups logs missing group", run: func() error { return groupsCommand([]string{"logs"}) }, want: "readyci groups logs"},
		{name: "groups logs bad tail", run: func() error { return groupsCommand([]string{"logs", "--tail", "0", "group"}) }, want: "--tail must be positive"},
		{name: "groups cancel missing group", run: func() error { return groupsCommand([]string{"cancel"}) }, want: "readyci groups cancel"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := captureStdoutForCLITest(t, tc.run)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%s error = %v, want %q", tc.name, err, tc.want)
			}
		})
	}
}

func TestAuthRouterValidatesSubcommandsBeforeNetwork(t *testing.T) {
	tests := []struct {
		name string
		run  func() error
		want string
	}{
		{name: "auth unknown", run: func() error { return authCommand([]string{"mystery"}) }, want: `unknown auth command "mystery"`},
		{name: "auth status unexpected arg", run: func() error { return authCommand([]string{"status", "extra"}) }, want: "readyci auth status"},
		{name: "auth logout unexpected arg", run: func() error { return authCommand([]string{"logout", "extra"}) }, want: "readyci auth logout"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := captureStdoutForCLITest(t, tc.run)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%s error = %v, want %q", tc.name, err, tc.want)
			}
		})
	}
}

func TestValidateExitCodeRejectsOutOfRangeValues(t *testing.T) {
	for _, code := range []int{-1, 256} {
		if _, err := validateExitCode(code); err == nil {
			t.Fatalf("validateExitCode(%d) succeeded, want error", code)
		}
	}
}

func TestValidateExitCodeAllowsShellExitRange(t *testing.T) {
	for _, code := range []int{0, 1, 124, 130, 143, 255} {
		got, err := validateExitCode(code)
		if err != nil {
			t.Fatalf("validateExitCode(%d): %v", code, err)
		}
		if got != code {
			t.Fatalf("validateExitCode(%d) = %d", code, got)
		}
	}
}

func TestArtifactsDownloadForcePreservesExistingFileOnFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "missing", http.StatusNotFound)
	}))
	defer server.Close()

	outputPath := filepath.Join(t.TempDir(), "artifacts.tar.gz")
	if err := os.WriteFile(outputPath, []byte("keep me"), 0o644); err != nil {
		t.Fatalf("write existing artifact: %v", err)
	}

	err := artifactsDownloadCommand([]string{
		"--api-url", server.URL,
		"--output", outputPath,
		"--force",
		"run_123",
	})
	if err == nil {
		t.Fatal("download unexpectedly succeeded")
	}
	got, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read existing artifact after failed download: %v", err)
	}
	if string(got) != "keep me" {
		t.Fatalf("existing artifact = %q, want preserved content", got)
	}
}
