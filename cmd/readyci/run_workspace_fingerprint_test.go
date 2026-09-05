package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestWorkspaceFingerprintIncludesDirtyAndUntrackedContent(t *testing.T) {
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
		t.Fatalf("write tracked base: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")

	if err := os.WriteFile("tracked.txt", []byte("dirty one\n"), 0o644); err != nil {
		t.Fatalf("write tracked dirty one: %v", err)
	}
	if err := os.WriteFile("untracked.txt", []byte("untracked one\n"), 0o644); err != nil {
		t.Fatalf("write untracked one: %v", err)
	}
	first, err := workspaceFingerprint(context.Background())
	if err != nil {
		t.Fatalf("initial fingerprint: %v", err)
	}

	if err := os.WriteFile("tracked.txt", []byte("dirty two\n"), 0o644); err != nil {
		t.Fatalf("write tracked dirty two: %v", err)
	}
	second, err := workspaceFingerprint(context.Background())
	if err != nil {
		t.Fatalf("tracked fingerprint: %v", err)
	}
	if second == first {
		t.Fatal("fingerprint did not change after editing an already-dirty tracked file")
	}

	if err := os.WriteFile("untracked.txt", []byte("untracked two\n"), 0o644); err != nil {
		t.Fatalf("write untracked two: %v", err)
	}
	third, err := workspaceFingerprint(context.Background())
	if err != nil {
		t.Fatalf("untracked fingerprint: %v", err)
	}
	if third == second {
		t.Fatal("fingerprint did not change after editing an already-untracked file")
	}

	if err := os.Mkdir("subdir", 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	if err := os.WriteFile("root-untracked.txt", []byte("root untracked one\n"), 0o644); err != nil {
		t.Fatalf("write root untracked one: %v", err)
	}
	if err := os.Chdir(filepath.Join(repo, "subdir")); err != nil {
		t.Fatalf("chdir subdir: %v", err)
	}
	fourth, err := workspaceFingerprint(context.Background())
	if err != nil {
		t.Fatalf("subdir fingerprint: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "root-untracked.txt"), []byte("root untracked two\n"), 0o644); err != nil {
		t.Fatalf("write root untracked two: %v", err)
	}
	fifth, err := workspaceFingerprint(context.Background())
	if err != nil {
		t.Fatalf("subdir edited fingerprint: %v", err)
	}
	if fifth == fourth {
		t.Fatal("fingerprint did not change after editing a repo-root untracked file from a subdirectory")
	}
}

func TestShouldPrintFailedWorkspaceHint(t *testing.T) {
	if !shouldPrintFailedWorkspaceHint(true, preflightapi.EventRunFailed, true, 7, true, true, false) {
		t.Fatal("complete failed-workspace artifact signals did not print hint")
	}
	for name, tt := range map[string]struct {
		keepFailed       bool
		terminalEvent    string
		commandExited    bool
		commandExitCode  int
		artifactUploaded bool
		retained         bool
		skipped          bool
	}{
		"successful command with artifact failure": {
			keepFailed:       true,
			terminalEvent:    preflightapi.EventRunFailed,
			commandExited:    true,
			commandExitCode:  0,
			artifactUploaded: false,
			retained:         false,
		},
		"startup failure": {
			keepFailed:       true,
			terminalEvent:    preflightapi.EventRunFailed,
			commandExited:    false,
			commandExitCode:  127,
			artifactUploaded: false,
			retained:         false,
		},
		"artifact upload missing": {
			keepFailed:       true,
			terminalEvent:    preflightapi.EventRunFailed,
			commandExited:    true,
			commandExitCode:  7,
			artifactUploaded: false,
			retained:         true,
		},
		"failed workspace skipped": {
			keepFailed:       true,
			terminalEvent:    preflightapi.EventRunFailed,
			commandExited:    true,
			commandExitCode:  7,
			artifactUploaded: true,
			retained:         true,
			skipped:          true,
		},
		"timeout": {
			keepFailed:       true,
			terminalEvent:    preflightapi.EventRunTimedOut,
			commandExited:    true,
			commandExitCode:  124,
			artifactUploaded: true,
			retained:         true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if shouldPrintFailedWorkspaceHint(tt.keepFailed, tt.terminalEvent, tt.commandExited, tt.commandExitCode, tt.artifactUploaded, tt.retained, tt.skipped) {
				t.Fatal("hint printed unexpectedly")
			}
		})
	}
}
