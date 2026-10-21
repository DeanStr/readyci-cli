package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func setupInferredGoRepo(t *testing.T) string {
	t.Helper()
	return setupInferredGoRepoWithChange(t, true)
}

func setupCleanInferredGoRepo(t *testing.T) string {
	t.Helper()
	return setupInferredGoRepoWithChange(t, false)
}

func setupInferredGoRepoWithChange(t *testing.T, changed bool) string {
	t.Helper()
	t.Setenv("READYCI_API_URL", "")
	t.Setenv("READYCI_API_TOKEN", "")
	t.Setenv("READYCI_PROJECT_ID", "")
	repo := chdirCLITestRepo(t, "magic-go")
	runGitForCLITest(t, "init")
	runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/magic\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package magic\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if !changed {
		return repo
	}
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package magic\n\nfunc Changed() {}\n"), 0o644); err != nil {
		t.Fatalf("change main.go: %v", err)
	}
	return repo
}

func TestCheckInfersAffectedGoCheckWithoutConfig(t *testing.T) {
	setupInferredGoRepo(t)

	output, err := captureStdoutForCLITest(t, func() error {
		return checkCommand([]string{"--dry-run"})
	})
	if err != nil {
		t.Fatalf("check dry-run inferred config: %v\n%s", err, output)
	}
	requireCLITestOutputContains(t, output,
		"affected: dry-run",
		"config: <inferred .readyci.yml>",
		"check: go",
		"command: go test ./...",
		"reason: main.go matched **/*.go",
	)
}

func TestCheckInfersFromRepoRootWhenRunFromSubdirectory(t *testing.T) {
	repo := setupInferredGoRepo(t)
	subdir := filepath.Join(repo, "cmd", "tool")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	if err := os.Chdir(subdir); err != nil {
		t.Fatalf("chdir subdir: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return checkCommand([]string{"--dry-run"})
	})
	if err != nil {
		t.Fatalf("check dry-run inferred from subdir: %v\n%s", err, output)
	}
	requireCLITestOutputContains(t, output,
		"affected: dry-run",
		"config: <inferred .readyci.yml>",
		"project_id: magic-go",
		"check: go",
		"command: go test ./...",
	)
}

func TestCheckRequiresConfigurationWithoutTestEntrypoint(t *testing.T) {
	t.Setenv("READYCI_API_URL", "")
	t.Setenv("READYCI_API_TOKEN", "")
	t.Setenv("READYCI_PROJECT_ID", "")
	repo := chdirCLITestRepo(t, "docs-only")
	if err := os.MkdirAll(filepath.Join(repo, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "docs", "guide.md"), []byte("# Guide\n"), 0o644); err != nil {
		t.Fatalf("write guide: %v", err)
	}
	runGitForCLITest(t, "init")
	runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")

	output, err := captureStdoutForCLITest(t, func() error {
		return checkCommand([]string{"--dry-run"})
	})
	if err == nil || normalizeCLIError(err).Classification != preflightapi.ClassificationConfigInvalid || !strings.Contains(err.Error(), "no supported root test entrypoint") {
		t.Fatalf("check must require a documented test command: error=%v output=%s", err, output)
	}
	if strings.Contains(output, "make check") {
		t.Fatalf("unsupported repository received an invented test command: %s", output)
	}
	if _, err := os.Stat(filepath.Join(repo, ".readyci.yml")); !os.IsNotExist(err) {
		t.Fatalf("inference wrote configuration for unsupported repository: %v", err)
	}
}

func TestCheckFallsBackToDetectedCheckWhenInferredAffectedHasNoChanges(t *testing.T) {
	setupCleanInferredGoRepo(t)

	output, err := captureStdoutForCLITest(t, func() error {
		return checkCommand([]string{"--dry-run"})
	})
	if err != nil {
		t.Fatalf("check dry-run clean inferred config: %v\n%s", err, output)
	}
	requireCLITestOutputContains(t, output,
		"check: dry-run",
		"name: go",
		"config: <inferred .readyci.yml>",
		"command: go test ./...",
	)
}

func TestAgentPlanInfersAffectedGoCheckWithoutConfig(t *testing.T) {
	setupInferredGoRepo(t)

	output, err := captureStdoutForCLITest(t, func() error {
		return agentPlanCommand([]string{"--json"})
	})
	if err != nil {
		t.Fatalf("agent plan inferred config: %v\n%s", err, output)
	}
	var report agentPlanReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode agent plan: %v\n%s", err, output)
	}
	if report.Mode != "affected_rules" || report.Status != "ready" || report.Check != "go" || len(report.Checks) != 1 || report.Checks[0].Check != "go" {
		t.Fatalf("agent inferred plan = %+v, want selected go affected check", report)
	}
	if !strings.Contains(report.Reason, "Inferred checks from repository signals") {
		t.Fatalf("agent inferred reason = %q, want inference note", report.Reason)
	}
}
