package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAffectedExplicitCheckHonorsProjectSourcePolicy(t *testing.T) {
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
project_id: airline
sync:
  include_untracked: false
checks:
  custom:
    command: make custom
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runGitForCLITest(t, "add", ".readyci.yml")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, "new.ts"), []byte("export {}\n"), 0o644); err != nil {
		t.Fatalf("write untracked file: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return affectedCommand([]string{"--check", "custom", "--dry-run", "--json"})
	})
	if err != nil {
		t.Fatalf("affected explicit dry-run: %v", err)
	}
	var report checkDryRunReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode affected explicit report: %v\n%s", err, output)
	}
	if report.Check != "custom" || report.Sync.IncludeUntracked || contains(report.Sync.ChangedFiles, "new.ts") {
		t.Fatalf("affected explicit sync = check %q include %t changed %v, want custom with untracked files excluded", report.Check, report.Sync.IncludeUntracked, report.Sync.ChangedFiles)
	}
}

func TestSetupMakefileScaffoldIncludesAffectedAudit(t *testing.T) {
	output := setupMakefileScaffold("https://api.readyci.test", "airline")
	requireCLITestOutputContains(t, output,
		"READYCI_API_URL ?= https://api.readyci.test",
		"READYCI_PROJECT_ID ?= airline",
		"remote-check-touched:",
		"$(READYCI) affected audit --strict --duplicates",
	)
}
