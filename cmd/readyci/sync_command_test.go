package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func TestBuildSyncDryRunReportUsesCheckChangeModeAndConfigSync(t *testing.T) {
	cliTestWorkingDirectoryMu.Lock()
	defer cliTestWorkingDirectoryMu.Unlock()

	repo := t.TempDir()
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore wd: %v", err)
		}
	}()

	runGitForCLITest(t, "init")
	runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	if err := os.WriteFile("tracked.txt", []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write tracked: %v", err)
	}
	if err := os.WriteFile("excluded.txt", []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write excluded: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.WriteFile("tracked.txt", []byte("changed\n"), 0o644); err != nil {
		t.Fatalf("change tracked: %v", err)
	}
	runGitForCLITest(t, "add", "tracked.txt")
	if err := os.WriteFile("excluded.txt", []byte("changed but excluded\n"), 0o644); err != nil {
		t.Fatalf("change excluded: %v", err)
	}
	if err := os.WriteFile("untracked.txt", []byte("not included\n"), 0o644); err != nil {
		t.Fatalf("write untracked: %v", err)
	}
	configPath := filepath.Join(repo, ".readyci.yml")
	if err := os.WriteFile(configPath, []byte(`
sync:
  include_untracked: true
  exclude:
    - excluded.txt
checks:
  staged:
    command: make staged
    changes: staged
    sync:
      include_untracked: false
      exclude:
        - check-excluded.txt
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile("check-excluded.txt", []byte("check excluded\n"), 0o644); err != nil {
		t.Fatalf("write check excluded: %v", err)
	}

	cfg, path, err := config.LoadProjectConfig(repo)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	plan, err := resolveCheckRunPlan("staged", cfg, checkResolveOptions{RetryCount: -1})
	if err != nil {
		t.Fatalf("resolve check: %v", err)
	}
	if plan.Options.IncludeUntracked == nil || *plan.Options.IncludeUntracked {
		t.Fatalf("plan include_untracked = %v, want explicit false", plan.Options.IncludeUntracked)
	}
	if !contains(plan.Options.SyncExclude, "excluded.txt") || !contains(plan.Options.SyncExclude, "check-excluded.txt") {
		t.Fatalf("plan sync excludes = %v, want project and check excludes", plan.Options.SyncExclude)
	}
	report, err := buildSyncDryRunReport(context.Background(), repo, cfg, path, syncDryRunOptions{CheckName: "staged"})
	if err != nil {
		t.Fatalf("build dry-run report: %v", err)
	}
	if report.ConfigPath != configPath || report.CheckName != "staged" || report.ChangeMode != syncbundle.ChangeModeStaged {
		t.Fatalf("report identity = %+v, want config/check/staged", report)
	}
	if report.IncludeUntracked {
		t.Fatal("include_untracked = true, want false")
	}
	if !contains(report.ExcludePatterns, "excluded.txt") || !contains(report.ExcludePatterns, "check-excluded.txt") {
		t.Fatalf("exclude patterns = %v, want project and check excludes", report.ExcludePatterns)
	}
	if !contains(report.ChangedFiles, "tracked.txt") {
		t.Fatalf("changed files = %v, want tracked.txt", report.ChangedFiles)
	}
	for _, disallowed := range []string{"excluded.txt", "check-excluded.txt", "untracked.txt"} {
		if contains(report.ChangedFiles, disallowed) || contains(report.WorkspaceFiles, disallowed) {
			t.Fatalf("report included %s: changed=%v workspace=%v", disallowed, report.ChangedFiles, report.WorkspaceFiles)
		}
	}
	if report.CompressedBytes <= 0 || report.ExpandedBytes <= 0 {
		t.Fatalf("report sizes compressed=%d expanded=%d, want positive", report.CompressedBytes, report.ExpandedBytes)
	}
}

func TestBuildSyncDryRunReportWarnsForIncludedUntrackedFiles(t *testing.T) {
	cliTestWorkingDirectoryMu.Lock()
	defer cliTestWorkingDirectoryMu.Unlock()

	repo := t.TempDir()
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore wd: %v", err)
		}
	}()

	runGitForCLITest(t, "init")
	runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	if err := os.WriteFile("tracked.txt", []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write tracked: %v", err)
	}
	if err := os.WriteFile(".gitignore", []byte("ignored.txt\n"), 0o644); err != nil {
		t.Fatalf("write gitignore: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.WriteFile("new-source.go", []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write untracked source: %v", err)
	}
	if err := os.WriteFile("excluded.txt", []byte("excluded\n"), 0o644); err != nil {
		t.Fatalf("write excluded: %v", err)
	}
	if err := os.WriteFile("ignored.txt", []byte("ignored\n"), 0o644); err != nil {
		t.Fatalf("write ignored: %v", err)
	}

	includeUntracked := true
	cfg := config.ProjectConfig{
		Sync: config.SyncConfig{
			IncludeUntracked: &includeUntracked,
			Exclude:          []string{"excluded.txt"},
		},
	}
	report, err := buildSyncDryRunReport(context.Background(), repo, cfg, "", syncDryRunOptions{})
	if err != nil {
		t.Fatalf("build dry-run report: %v", err)
	}
	if report.UntrackedCount != 1 || !contains(report.UntrackedFiles, "new-source.go") {
		t.Fatalf("untracked report = count %d files %v, want new-source.go only", report.UntrackedCount, report.UntrackedFiles)
	}
	for _, filtered := range []string{"excluded.txt", "ignored.txt"} {
		if contains(report.UntrackedFiles, filtered) {
			t.Fatalf("untracked files included filtered path %s: %v", filtered, report.UntrackedFiles)
		}
	}
	if len(report.Warnings) == 0 || !strings.Contains(strings.Join(report.Warnings, "\n"), "untracked files included in upload") {
		t.Fatalf("warnings = %v, want untracked upload warning", report.Warnings)
	}
}

func TestBuildSyncDryRunReportStagedOverrideDisablesUntracked(t *testing.T) {
	cliTestWorkingDirectoryMu.Lock()
	defer cliTestWorkingDirectoryMu.Unlock()

	repo := t.TempDir()
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore wd: %v", err)
		}
	}()

	runGitForCLITest(t, "init")
	runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	if err := os.WriteFile("tracked.txt", []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write tracked: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.WriteFile("tracked.txt", []byte("staged\n"), 0o644); err != nil {
		t.Fatalf("change tracked: %v", err)
	}
	runGitForCLITest(t, "add", "tracked.txt")
	if err := os.WriteFile("untracked.txt", []byte("new\n"), 0o644); err != nil {
		t.Fatalf("write untracked: %v", err)
	}
	includeUntracked := true
	cfg := config.ProjectConfig{Sync: config.SyncConfig{IncludeUntracked: &includeUntracked}}
	report, err := buildSyncDryRunReport(context.Background(), repo, cfg, "", syncDryRunOptions{ChangeMode: syncbundle.ChangeModeStaged})
	if err != nil {
		t.Fatalf("build dry-run report: %v", err)
	}
	if report.IncludeUntracked {
		t.Fatal("include_untracked = true, want staged override to report false")
	}
	if contains(report.ChangedFiles, "untracked.txt") || contains(report.WorkspaceFiles, "untracked.txt") {
		t.Fatalf("staged dry-run included untracked file: changed=%v workspace=%v", report.ChangedFiles, report.WorkspaceFiles)
	}
}

func TestSyncCommandDryRunIgnoresUnrelatedInvalidChecks(t *testing.T) {
	cliTestWorkingDirectoryMu.Lock()
	defer cliTestWorkingDirectoryMu.Unlock()

	repo := t.TempDir()
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore wd: %v", err)
		}
	}()

	runGitForCLITest(t, "init")
	runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	if err := os.WriteFile("tracked.txt", []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write tracked: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.WriteFile("tracked.txt", []byte("changed\n"), 0o644); err != nil {
		t.Fatalf("change tracked: %v", err)
	}
	runGitForCLITest(t, "add", "tracked.txt")
	if err := os.WriteFile(".readyci.yml", []byte(`
checks:
  good:
    command: make good
    changes: staged
  placeholder:
    changes: definitely-not-a-mode
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	for _, args := range [][]string{
		{"--dry-run", "--check", "good"},
		{"--dry-run"},
	} {
		if err := syncCommand(args); err != nil {
			t.Fatalf("syncCommand(%v) error = %v", args, err)
		}
	}
}
