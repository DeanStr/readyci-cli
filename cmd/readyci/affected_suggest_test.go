package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAffectedSuggestGeneratesRulesTestsAndAuditIgnore(t *testing.T) {
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
	if err := os.MkdirAll(filepath.Join(repo, "scripts"), 0o755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "scripts", "check.sh"), []byte("#!/usr/bin/env bash\necho ok\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "legacy"), 0o755); err != nil {
		t.Fatalf("mkdir legacy: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "legacy", "old.sql"), []byte("select 1;\n"), 0o644); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
checks:
  shell-syntax:
    command: bash -n scripts/check.sh
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")

	output, err := captureStdoutForCLITest(t, func() error {
		return affectedSuggestCommand([]string{"--json"})
	})
	if err != nil {
		t.Fatalf("affected suggest: %v\n%s", err, output)
	}
	if strings.Contains(output, `"Paths"`) || strings.Contains(output, `"Checks"`) || strings.Contains(output, `"Path"`) {
		t.Fatalf("suggest JSON used Go field names: %s", output)
	}
	if !strings.Contains(output, `"paths"`) || !strings.Contains(output, `"checks"`) {
		t.Fatalf("suggest JSON missing lowercase rule/test fields: %s", output)
	}
	var report affectedSuggestReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode suggest report: %v\n%s", err, output)
	}
	if !contains(report.Profiles, "shell") || !contains(report.Profiles, "readyci") || !contains(report.AuditIgnore, "legacy/**") {
		t.Fatalf("suggest profiles=%v audit_ignore=%v, want shell/readyci and legacy/**", report.Profiles, report.AuditIgnore)
	}
	if len(report.Rules) == 0 || len(report.Tests) == 0 {
		t.Fatalf("suggest rules=%d tests=%d, want non-empty", len(report.Rules), len(report.Tests))
	}
	if len(report.Subsumes) != 0 {
		t.Fatalf("suggest subsumes = %+v, want none for baseline shell suggestion", report.Subsumes)
	}
}

func TestAffectedSuggestUsesRepositoryRootFromSubdirectory(t *testing.T) {
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
	if err := os.MkdirAll(filepath.Join(repo, "scripts"), 0o755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "scripts", "check.sh"), []byte("#!/usr/bin/env bash\necho ok\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "legacy"), 0o755); err != nil {
		t.Fatalf("mkdir legacy: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "legacy", "old.sql"), []byte("select 1;\n"), 0o644); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "nested", "child"), 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
checks:
  shell-syntax:
    command: bash -n scripts/check.sh
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.Chdir(filepath.Join(repo, "nested", "child")); err != nil {
		t.Fatalf("chdir nested: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return affectedSuggestCommand([]string{"--json"})
	})
	if err != nil {
		t.Fatalf("affected suggest from subdir: %v\n%s", err, output)
	}
	var report affectedSuggestReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode suggest report: %v\n%s", err, output)
	}
	if !contains(report.Profiles, "shell") || !contains(report.Profiles, "readyci") || !contains(report.AuditIgnore, "legacy/**") {
		t.Fatalf("suggest from subdir profiles=%v audit_ignore=%v, want root shell/readyci and legacy/**", report.Profiles, report.AuditIgnore)
	}
	if len(report.Rules) == 0 || len(report.Tests) == 0 {
		t.Fatalf("suggest from subdir rules=%d tests=%d, want root-derived suggestions", len(report.Rules), len(report.Tests))
	}
}
