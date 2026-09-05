package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAffectedExplainWhyNotReportsRuleExclude(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
affected:
  rules:
    - paths:
        - apps/web/**
      exclude:
        - apps/web/generated/**
      checks:
        - web
checks:
  web:
    command: pnpm web-check
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")

	output, err := captureStdoutForCLITest(t, func() error {
		return affectedExplainCommand([]string{"--json", "--why-not", "apps/web/generated/client.ts"})
	})
	if err != nil {
		t.Fatalf("affected explain why-not exclude: %v\n%s", err, output)
	}
	var report affectedExplainReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode explain report: %v\n%s", err, output)
	}
	if len(report.Paths) != 1 || !strings.Contains(report.Paths[0].WhyNot, "rule excludes") {
		t.Fatalf("explain report = %+v, want rule-exclude why-not", report.Paths)
	}
	if len(report.Paths[0].FilterHits) == 0 || !strings.Contains(report.Paths[0].FilterHits[0], "exclude pattern") {
		t.Fatalf("filter hits = %+v, want exclude pattern detail", report.Paths[0].FilterHits)
	}
}

func TestAffectedExplainWhyNotReportsAuditIgnore(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
affected:
  audit_ignore:
    - legacy/**
  rules:
    - paths:
        - docs/**
      checks:
        - docs-lint
checks:
  docs-lint:
    command: make docs-lint
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")

	output, err := captureStdoutForCLITest(t, func() error {
		return affectedExplainCommand([]string{"--json", "--why-not", "legacy/schema.sql"})
	})
	if err != nil {
		t.Fatalf("affected explain why-not: %v\n%s", err, output)
	}
	var report affectedExplainReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode explain report: %v\n%s", err, output)
	}
	if len(report.Paths) != 1 || !report.Paths[0].Ignored || !strings.Contains(report.Paths[0].WhyNot, "affected.audit_ignore") {
		t.Fatalf("explain report = %+v, want ignored why-not", report.Paths)
	}
}

func TestAffectedExplainReportsMatchingChecks(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
affected:
  subsumes:
    web:
      - web-test
  rules:
    - paths:
        - apps/web/src/**
      checks:
        - web
    - paths:
        - apps/web/src/**
      checks:
        - web-test
checks:
  web:
    command: pnpm web
  web-test:
    command: pnpm test
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	output, err := captureStdoutForCLITest(t, func() error {
		return affectedExplainCommand([]string{"--json", "apps/web/src/app.tsx"})
	})
	if err != nil {
		t.Fatalf("affected explain: %v", err)
	}
	var report affectedExplainReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode explain report: %v\n%s", err, output)
	}
	if len(report.Paths) != 1 || !report.Paths[0].Covered {
		t.Fatalf("explain paths = %+v, want covered path", report.Paths)
	}
	if got := strings.Join(report.Paths[0].Checks, ","); got != "web" {
		t.Fatalf("checks = %q, want web after subsumption", got)
	}
	if len(report.Paths[0].Reasons) != 1 || report.Paths[0].Reasons[0].Pattern != "apps/web/src/**" {
		t.Fatalf("reasons = %+v, want web rule", report.Paths[0].Reasons)
	}
	if len(report.Paths[0].SkippedChecks) != 1 || report.Paths[0].SkippedChecks[0].Name != "web-test" || report.Paths[0].SkippedChecks[0].Reason != "subsumed" {
		t.Fatalf("skipped checks = %+v, want subsumed web-test", report.Paths[0].SkippedChecks)
	}
}

func TestAffectedExplainIncludesUntrackedForInheritedWorkingChecks(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
sync:
  include_untracked: false
affected:
  rules:
    - paths:
        - .github/**
      checks:
        - ci
checks:
  ci:
    command: make ci
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runGitForCLITest(t, "add", ".readyci.yml")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.MkdirAll(filepath.Join(repo, ".github", "workflows"), 0o755); err != nil {
		t.Fatalf("mkdir workflows: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".github", "workflows", "new.yml"), []byte("name: New\n"), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return affectedExplainCommand([]string{"--json", "--why-not", ".github/workflows/new.yml"})
	})
	if err != nil {
		t.Fatalf("affected explain: %v\n%s", err, output)
	}
	var report affectedExplainReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode explain report: %v\n%s", err, output)
	}
	if len(report.Paths) != 1 || !report.Paths[0].Covered || strings.Join(report.Paths[0].Checks, ",") != "ci" {
		t.Fatalf("explain path = %+v, want untracked workflow covered by ci", report.Paths)
	}
	if report.Paths[0].WhyNot != "" || len(report.Paths[0].FilterHits) != 0 {
		t.Fatalf("explain path = %+v, want no why-not/filter hits for covered untracked path", report.Paths[0])
	}
}

func TestAffectedExplainUsesRepoRelativePathsFromSubdirectory(t *testing.T) {
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
	if err := os.MkdirAll(filepath.Join(repo, "a", "b"), 0o755); err != nil {
		t.Fatalf("mkdir nested source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "a", "b", "file.ts"), []byte("export {}\n"), 0o644); err != nil {
		t.Fatalf("write nested source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
affected:
  rules:
    - paths:
        - a/b/**
      checks:
        - web-check
checks:
  web-check:
    command: pnpm test
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.Chdir(filepath.Join(repo, "a")); err != nil {
		t.Fatalf("chdir subdir: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return affectedExplainCommand([]string{"--json", "b/file.ts"})
	})
	if err != nil {
		t.Fatalf("affected explain from subdir: %v\n%s", err, output)
	}
	var report affectedExplainReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode explain report: %v\n%s", err, output)
	}
	if len(report.Paths) != 1 || report.Paths[0].Path != "a/b/file.ts" || !report.Paths[0].Covered {
		t.Fatalf("explain paths = %+v, want covered repo-relative a/b/file.ts", report.Paths)
	}
}
