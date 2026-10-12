package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func TestAffectedCommandUsesChangedFileChecks(t *testing.T) {
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
  staged:
    command: make check-staged
    changes: staged
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return affectedCommand([]string{"--check", "touched", "--timeout", "45m", "--dry-run", "--json"})
	})
	if err != nil {
		t.Fatalf("affected dry-run: %v", err)
	}
	var report checkDryRunReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode affected report: %v\n%s", err, output)
	}
	if report.Check != "touched" || report.ChangeMode != syncbundle.ChangeModeWorking || report.TimeoutSeconds != 2700 {
		t.Fatalf("affected report = check %q change %q timeout %d, want touched/working/2700", report.Check, report.ChangeMode, report.TimeoutSeconds)
	}

	output, err = captureStdoutForCLITest(t, func() error {
		return affectedCommand([]string{"--check", "staged", "--staged", "--dry-run", "--json"})
	})
	if err != nil {
		t.Fatalf("affected staged dry-run: %v", err)
	}
	report = checkDryRunReport{}
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode staged affected report: %v\n%s", err, output)
	}
	if report.Check != "staged" || report.ChangeMode != syncbundle.ChangeModeStaged {
		t.Fatalf("staged affected report = check %q change %q, want staged/staged", report.Check, report.ChangeMode)
	}
}
