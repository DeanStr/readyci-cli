package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func TestAffectedCommandPrefersAffectedAlias(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("readyci\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
project_id: airline
checks:
  affected:
    command: make check-affected
    changes: working
  affected-staged:
    command: make check-affected-staged
    changes: staged
  touched:
    command: make check-touched
    changes: working
  staged:
    command: make check-staged
    changes: staged
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return affectedCommand([]string{"--dry-run", "--json"})
	})
	if err != nil {
		t.Fatalf("affected dry-run: %v", err)
	}
	var report checkDryRunReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode affected report: %v\n%s", err, output)
	}
	if report.Check != "affected" {
		t.Fatalf("affected selected check %q, want affected", report.Check)
	}

	output, err = captureStdoutForCLITest(t, func() error {
		return affectedCommand([]string{"--staged", "--dry-run", "--json"})
	})
	if err != nil {
		t.Fatalf("affected staged dry-run: %v", err)
	}
	report = checkDryRunReport{}
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode staged affected report: %v\n%s", err, output)
	}
	if report.Check != "affected-staged" {
		t.Fatalf("affected --staged selected check %q, want affected-staged", report.Check)
	}

	output, err = captureStdoutForCLITest(t, func() error {
		return touchedCommand([]string{"--dry-run", "--json"})
	})
	if err != nil {
		t.Fatalf("touched dry-run: %v", err)
	}
	report = checkDryRunReport{}
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode touched report: %v\n%s", err, output)
	}
	if report.Check != "touched" {
		t.Fatalf("touched selected check %q, want touched", report.Check)
	}

	output, err = captureStdoutForCLITest(t, func() error {
		return touchedCommand([]string{"--staged", "--dry-run", "--json"})
	})
	if err != nil {
		t.Fatalf("touched staged dry-run: %v", err)
	}
	report = checkDryRunReport{}
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode touched staged report: %v\n%s", err, output)
	}
	if report.Check != "staged" {
		t.Fatalf("touched --staged selected check %q, want staged", report.Check)
	}
}

func TestTouchedCommandStagedFallsBackToLegacyTouched(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("readyci\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
project_id: airline
checks:
  touched:
    command: make check-touched
    changes: working
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return touchedCommand([]string{"--staged", "--dry-run", "--json"})
	})
	if err != nil {
		t.Fatalf("touched staged dry-run: %v", err)
	}
	var report checkDryRunReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode touched staged report: %v\n%s", err, output)
	}
	if report.Check != "touched" || report.ChangeMode != syncbundle.ChangeModeStaged {
		t.Fatalf("touched --staged fallback = check %q change %q, want touched/staged", report.Check, report.ChangeMode)
	}
}

func TestAffectedCommandFallsBackToStandardNarrowCheck(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("readyci\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
project_id: airline
checks:
  go:
    command: go test ./...
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return affectedCommand([]string{"--dry-run", "--json"})
	})
	if err != nil {
		t.Fatalf("affected dry-run: %v", err)
	}
	var report checkDryRunReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode affected fallback report: %v\n%s", err, output)
	}
	if report.Check != "go" || report.ChangeMode != syncbundle.ChangeModeWorking {
		t.Fatalf("affected fallback report = check %q change %q, want go/working", report.Check, report.ChangeMode)
	}
}

func TestAffectedFallbackIncludesUntrackedWhenProjectDefaultExcludesThem(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("readyci\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
project_id: airline
sync:
  include_untracked: false
checks:
  touched:
    command: make check-touched
    changes: working
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, "new.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write untracked file: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return affectedCommand([]string{"--dry-run", "--json"})
	})
	if err != nil {
		t.Fatalf("affected dry-run: %v", err)
	}
	var report checkDryRunReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode affected report: %v\n%s", err, output)
	}
	if report.Check != "touched" || !report.Sync.IncludeUntracked || !contains(report.Sync.ChangedFiles, "new.go") {
		t.Fatalf("affected fallback sync = check %q include %t changed %v, want touched/include untracked/new.go", report.Check, report.Sync.IncludeUntracked, report.Sync.ChangedFiles)
	}
}

func TestAffectedExplicitCheckIncludesUntrackedWhenProjectDefaultExcludesThem(t *testing.T) {
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
	if report.Check != "custom" || !report.Sync.IncludeUntracked || !contains(report.Sync.ChangedFiles, "new.ts") {
		t.Fatalf("affected explicit sync = check %q include %t changed %v, want custom/include untracked/new.ts", report.Check, report.Sync.IncludeUntracked, report.Sync.ChangedFiles)
	}
}

func TestAffectedFallbackStagedStillExcludesUntracked(t *testing.T) {
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
  staged:
    command: make check-staged
    changes: staged
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runGitForCLITest(t, "add", ".readyci.yml")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, "new.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write untracked file: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return affectedCommand([]string{"--staged", "--dry-run", "--json"})
	})
	if err != nil {
		t.Fatalf("affected staged dry-run: %v", err)
	}
	var report checkDryRunReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode affected staged report: %v\n%s", err, output)
	}
	if report.Sync.IncludeUntracked || contains(report.Sync.ChangedFiles, "new.go") {
		t.Fatalf("staged affected sync = include %t changed %v, want untracked excluded", report.Sync.IncludeUntracked, report.Sync.ChangedFiles)
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
