package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func TestWorkspaceFingerprintTracksGitAndUntrackedChanges(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	runMatrixGitTest(t, repo, "init")
	runMatrixGitTest(t, repo, "config", "user.email", "test@example.test")
	runMatrixGitTest(t, repo, "config", "user.name", "Test User")
	writeMatrixFile(t, repo, "tracked.txt", "base\n")
	runMatrixGitTest(t, repo, "add", ".")
	runMatrixGitTest(t, repo, "commit", "-m", "baseline")
	t.Chdir(repo)

	clean, err := workspaceFingerprint(ctx)
	if err != nil {
		t.Fatalf("clean workspace fingerprint: %v", err)
	}
	writeMatrixFile(t, repo, "untracked.txt", "new\n")
	withUntracked, err := workspaceFingerprint(ctx)
	if err != nil {
		t.Fatalf("untracked workspace fingerprint: %v", err)
	}
	if withUntracked == clean {
		t.Fatal("fingerprint did not change after adding untracked file")
	}
	writeMatrixFile(t, repo, "untracked.txt", "changed\n")
	withUntrackedContentChange, err := workspaceFingerprint(ctx)
	if err != nil {
		t.Fatalf("changed untracked workspace fingerprint: %v", err)
	}
	if withUntrackedContentChange == withUntracked {
		t.Fatal("fingerprint did not change after changing untracked file contents")
	}
	writeMatrixFile(t, repo, "tracked.txt", "working\n")
	withUnstagedDiff, err := workspaceFingerprint(ctx)
	if err != nil {
		t.Fatalf("unstaged workspace fingerprint: %v", err)
	}
	if withUnstagedDiff == withUntrackedContentChange {
		t.Fatal("fingerprint did not change after unstaged tracked-file edit")
	}
	runMatrixGitTest(t, repo, "add", "tracked.txt")
	withStagedDiff, err := workspaceFingerprint(ctx)
	if err != nil {
		t.Fatalf("staged workspace fingerprint: %v", err)
	}
	if withStagedDiff == withUnstagedDiff {
		t.Fatal("fingerprint did not change after staging tracked-file edit")
	}
}

func TestWorkspaceFingerprintHonorsSyncExclusions(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	runMatrixGitTest(t, repo, "init")
	runMatrixGitTest(t, repo, "config", "user.email", "test@example.test")
	runMatrixGitTest(t, repo, "config", "user.name", "Test User")
	writeMatrixFile(t, repo, "kept.txt", "base\n")
	writeMatrixFile(t, repo, "dist/generated.txt", "base\n")
	runMatrixGitTest(t, repo, "add", ".")
	runMatrixGitTest(t, repo, "commit", "-m", "baseline")
	t.Chdir(repo)

	options := workspaceFingerprintOptions{ExcludePatterns: []string{"dist/**"}}
	clean, err := workspaceFingerprint(ctx, options)
	if err != nil {
		t.Fatalf("clean workspace fingerprint: %v", err)
	}
	writeMatrixFile(t, repo, "dist/generated.txt", "ignored change\n")
	ignored, err := workspaceFingerprint(ctx, options)
	if err != nil {
		t.Fatalf("excluded workspace fingerprint: %v", err)
	}
	if ignored != clean {
		t.Fatal("fingerprint changed after editing an excluded tracked file")
	}
	writeMatrixFile(t, repo, "kept.txt", "included change\n")
	included, err := workspaceFingerprint(ctx, options)
	if err != nil {
		t.Fatalf("included workspace fingerprint: %v", err)
	}
	if included == ignored {
		t.Fatal("fingerprint did not change after editing an included tracked file")
	}
}

func TestWorkspaceFingerprintHonorsIncludeUntrackedFalse(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	runMatrixGitTest(t, repo, "init")
	runMatrixGitTest(t, repo, "config", "user.email", "test@example.test")
	runMatrixGitTest(t, repo, "config", "user.name", "Test User")
	writeMatrixFile(t, repo, "tracked.txt", "base\n")
	runMatrixGitTest(t, repo, "add", ".")
	runMatrixGitTest(t, repo, "commit", "-m", "baseline")
	t.Chdir(repo)

	includeUntracked := false
	options := workspaceFingerprintOptions{IncludeUntracked: &includeUntracked}
	clean, err := workspaceFingerprint(ctx, options)
	if err != nil {
		t.Fatalf("clean workspace fingerprint: %v", err)
	}
	writeMatrixFile(t, repo, "untracked.txt", "new\n")
	ignored, err := workspaceFingerprint(ctx, options)
	if err != nil {
		t.Fatalf("untracked workspace fingerprint: %v", err)
	}
	if ignored != clean {
		t.Fatal("fingerprint changed after writing untracked file with include_untracked=false")
	}
	writeMatrixFile(t, repo, "tracked.txt", "changed\n")
	included, err := workspaceFingerprint(ctx, options)
	if err != nil {
		t.Fatalf("tracked workspace fingerprint: %v", err)
	}
	if included == ignored {
		t.Fatal("fingerprint did not change after editing tracked file")
	}
}

func TestWorkspaceFingerprintStagedModeIgnoresUnstagedChanges(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	runMatrixGitTest(t, repo, "init")
	runMatrixGitTest(t, repo, "config", "user.email", "test@example.test")
	runMatrixGitTest(t, repo, "config", "user.name", "Test User")
	writeMatrixFile(t, repo, "tracked.txt", "base\n")
	runMatrixGitTest(t, repo, "add", ".")
	runMatrixGitTest(t, repo, "commit", "-m", "baseline")
	t.Chdir(repo)

	options := workspaceFingerprintOptions{ChangeMode: syncbundle.ChangeModeStaged}
	clean, err := workspaceFingerprint(ctx, options)
	if err != nil {
		t.Fatalf("clean staged workspace fingerprint: %v", err)
	}
	writeMatrixFile(t, repo, "tracked.txt", "unstaged\n")
	unstaged, err := workspaceFingerprint(ctx, options)
	if err != nil {
		t.Fatalf("unstaged workspace fingerprint: %v", err)
	}
	if unstaged != clean {
		t.Fatal("staged fingerprint changed after unstaged tracked-file edit")
	}
	runMatrixGitTest(t, repo, "add", "tracked.txt")
	staged, err := workspaceFingerprint(ctx, options)
	if err != nil {
		t.Fatalf("staged workspace fingerprint: %v", err)
	}
	if staged == clean {
		t.Fatal("staged fingerprint did not change after staging tracked-file edit")
	}
}

func TestWorkspaceFingerprintDistinguishesStagedRenameFromRestoredSource(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	runMatrixGitTest(t, repo, "init")
	runMatrixGitTest(t, repo, "config", "user.email", "test@example.test")
	runMatrixGitTest(t, repo, "config", "user.name", "Test User")
	writeMatrixFile(t, repo, "a.txt", "base\n")
	runMatrixGitTest(t, repo, "add", ".")
	runMatrixGitTest(t, repo, "commit", "-m", "baseline")
	t.Chdir(repo)

	runMatrixGitTest(t, repo, "mv", "a.txt", "b.txt")
	renameWorking, err := workspaceFingerprint(ctx)
	if err != nil {
		t.Fatalf("rename working fingerprint: %v", err)
	}
	stagedOptions := workspaceFingerprintOptions{ChangeMode: syncbundle.ChangeModeStaged}
	renameStaged, err := workspaceFingerprint(ctx, stagedOptions)
	if err != nil {
		t.Fatalf("rename staged fingerprint: %v", err)
	}

	runMatrixGitTest(t, repo, "checkout", "HEAD", "--", "a.txt")
	restoredWorking, err := workspaceFingerprint(ctx)
	if err != nil {
		t.Fatalf("restored working fingerprint: %v", err)
	}
	if restoredWorking == renameWorking {
		t.Fatal("working fingerprint collided between staged rename and restored source")
	}
	restoredStaged, err := workspaceFingerprint(ctx, stagedOptions)
	if err != nil {
		t.Fatalf("restored staged fingerprint: %v", err)
	}
	if restoredStaged == renameStaged {
		t.Fatal("staged fingerprint collided between staged rename and restored source")
	}
}

func TestWorkspaceFingerprintTracksRenameSourceWhenDestinationExcluded(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	runMatrixGitTest(t, repo, "init")
	runMatrixGitTest(t, repo, "config", "user.email", "test@example.test")
	runMatrixGitTest(t, repo, "config", "user.name", "Test User")
	writeMatrixFile(t, repo, "a.txt", "base\n")
	runMatrixGitTest(t, repo, "add", ".")
	runMatrixGitTest(t, repo, "commit", "-m", "baseline")
	t.Chdir(repo)

	options := workspaceFingerprintOptions{ExcludePatterns: []string{"dist/**"}}
	clean, err := workspaceFingerprint(ctx, options)
	if err != nil {
		t.Fatalf("clean fingerprint: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "dist"), 0o755); err != nil {
		t.Fatalf("mkdir dist: %v", err)
	}
	runMatrixGitTest(t, repo, "mv", "a.txt", "dist/b.txt")
	renamed, err := workspaceFingerprint(ctx, options)
	if err != nil {
		t.Fatalf("renamed fingerprint: %v", err)
	}
	if renamed == clean {
		t.Fatal("fingerprint did not change when an included source was renamed to an excluded destination")
	}
}

func TestWorkspaceFingerprintPreservesWhitespaceInGitPaths(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	runMatrixGitTest(t, repo, "init")
	runMatrixGitTest(t, repo, "config", "user.email", "test@example.test")
	runMatrixGitTest(t, repo, "config", "user.name", "Test User")
	writeMatrixFile(t, repo, " spaced.txt", "base\n")
	runMatrixGitTest(t, repo, "add", ".")
	runMatrixGitTest(t, repo, "commit", "-m", "baseline")
	t.Chdir(repo)

	clean, err := workspaceFingerprint(ctx)
	if err != nil {
		t.Fatalf("clean workspace fingerprint: %v", err)
	}
	writeMatrixFile(t, repo, " spaced.txt", "changed\n")
	changed, err := workspaceFingerprint(ctx)
	if err != nil {
		t.Fatalf("changed workspace fingerprint: %v", err)
	}
	if changed == clean {
		t.Fatal("fingerprint did not change after editing tracked file with leading whitespace")
	}
}

func TestWorkspaceFingerprintTreatsDiffPathsAsLiteralPathspecs(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	runMatrixGitTest(t, repo, "init")
	runMatrixGitTest(t, repo, "config", "user.email", "test@example.test")
	runMatrixGitTest(t, repo, "config", "user.name", "Test User")
	writeMatrixFile(t, repo, ":(foo).txt", "base\n")
	runMatrixGitTest(t, repo, "add", ".")
	runMatrixGitTest(t, repo, "commit", "-m", "baseline")
	t.Chdir(repo)

	clean, err := workspaceFingerprint(ctx)
	if err != nil {
		t.Fatalf("clean workspace fingerprint: %v", err)
	}
	writeMatrixFile(t, repo, ":(foo).txt", "changed\n")
	changed, err := workspaceFingerprint(ctx)
	if err != nil {
		t.Fatalf("changed workspace fingerprint: %v", err)
	}
	if changed == clean {
		t.Fatal("fingerprint did not change after editing tracked pathspec-like file")
	}
	runMatrixGitTest(t, repo, "add", ":(literal):(foo).txt")
	stagedOptions := workspaceFingerprintOptions{ChangeMode: syncbundle.ChangeModeStaged}
	staged, err := workspaceFingerprint(ctx, stagedOptions)
	if err != nil {
		t.Fatalf("staged workspace fingerprint: %v", err)
	}
	if staged == clean {
		t.Fatal("staged fingerprint did not change after staging pathspec-like file")
	}
}

func TestUntrackedPathHashingRejectsUnsafePathsAndHandlesMissing(t *testing.T) {
	repo := t.TempDir()
	for _, path := range []string{"../escape.txt", "/tmp/escape.txt"} {
		if err := hashUntrackedPath(sha256.New(), repo, path); err == nil {
			t.Fatalf("hashUntrackedPath(%q) succeeded, want validation error", path)
		}
	}
	digest := sha256.New()
	if err := hashUntrackedPath(digest, repo, "missing.txt"); err != nil {
		t.Fatalf("hash missing untracked path: %v", err)
	}
	if len(digest.Sum(nil)) != sha256.Size {
		t.Fatalf("digest size = %d, want %d", len(digest.Sum(nil)), sha256.Size)
	}
}

func TestExecuteCheckMatrixParallelFailFastStopsScheduling(t *testing.T) {
	variants := []matrixVariant{
		{Name: "A"},
		{Name: "B"},
		{Name: "C"},
	}
	started := make(chan string, len(variants))
	releaseB := make(chan struct{})
	var mu sync.Mutex
	var ran []string
	run := func(options remoteRunOptions, retries int) error {
		started <- options.MatrixVariant
		if options.MatrixVariant == "B" {
			<-releaseB
		}
		mu.Lock()
		ran = append(ran, options.MatrixVariant)
		mu.Unlock()
		if options.MatrixVariant == "A" {
			return exitCodeError{code: 2}
		}
		return nil
	}

	done := make(chan error, 1)
	go func() {
		done <- executeCheckMatrixParallel(remoteRunOptions{CommandAlias: "ci"}, variants, 0, true, 2, run)
	}()

	seenStarted := map[string]bool{}
	for len(seenStarted) < 2 {
		select {
		case variant := <-started:
			seenStarted[variant] = true
		case <-time.After(2 * time.Second):
			t.Fatalf("started variants = %v, want A and B", seenStarted)
		}
	}
	if !seenStarted["A"] || !seenStarted["B"] {
		t.Fatalf("started variants = %v, want A and B", seenStarted)
	}
	time.Sleep(20 * time.Millisecond)
	select {
	case variant := <-started:
		t.Fatalf("fail_fast scheduled %s before blocked sibling completed", variant)
	default:
	}
	close(releaseB)

	select {
	case err := <-done:
		var exitErr exitCodeError
		if !errors.As(err, &exitErr) || exitErr.code != 2 {
			t.Fatalf("parallel matrix error = %v, want exit code 2", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("parallel matrix fail_fast did not finish after started variants completed")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(ran) != 2 || ran[0] != "A" || ran[1] != "B" {
		t.Fatalf("ran variants = %v, want only already-started variants A and B", ran)
	}
}

func TestExecuteCheckMatrixParallelPreservesNonExitErrorWhileDraining(t *testing.T) {
	infraErr := errors.New("create run failed")
	variants := []matrixVariant{
		{Name: "A"},
		{Name: "B"},
	}
	started := make(chan string, len(variants))
	infraReturned := make(chan struct{})
	releaseB := make(chan struct{})
	run := func(options remoteRunOptions, retries int) error {
		started <- options.MatrixVariant
		switch options.MatrixVariant {
		case "A":
			close(infraReturned)
			return infraErr
		case "B":
			<-releaseB
			return exitCodeError{code: 2}
		default:
			return nil
		}
	}

	done := make(chan error, 1)
	go func() {
		done <- executeCheckMatrixParallel(remoteRunOptions{CommandAlias: "ci"}, variants, 0, false, 2, run)
	}()

	waitForStartedVariants(t, started, "A", "B")
	select {
	case <-infraReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("infra variant did not return")
	}
	close(releaseB)

	select {
	case err := <-done:
		if !errors.Is(err, infraErr) {
			t.Fatalf("parallel matrix error = %v, want infra error", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("parallel matrix did not finish")
	}
}

func TestExecuteCheckMatrixForReportParallelPreservesNonExitErrorWhileDraining(t *testing.T) {
	infraErr := errors.New("upload failed")
	variants := []matrixVariant{
		{Name: "A"},
		{Name: "B"},
	}
	started := make(chan string, len(variants))
	infraReturned := make(chan struct{})
	releaseB := make(chan struct{})
	run := func(options remoteRunOptions, retries int, checkName string) ([]checkExecutionRunReport, remoteRunResult, error) {
		started <- options.MatrixVariant
		switch options.MatrixVariant {
		case "A":
			close(infraReturned)
			return []checkExecutionRunReport{{RunID: "run-a", MatrixVariant: "A"}}, remoteRunResult{Status: "upload_failed"}, infraErr
		case "B":
			<-releaseB
			return []checkExecutionRunReport{{RunID: "run-b", MatrixVariant: "B"}}, remoteRunResult{Status: "failed", ExitCode: 2}, exitCodeError{code: 2}
		default:
			return nil, remoteRunResult{}, nil
		}
	}

	done := make(chan struct {
		runs   []checkExecutionRunReport
		result checkExecutionResultReport
		err    error
	}, 1)
	go func() {
		runs, result, err := executeCheckMatrixForReportParallelWithRunner(remoteRunOptions{CommandAlias: "ci"}, variants, 0, false, 2, run)
		done <- struct {
			runs   []checkExecutionRunReport
			result checkExecutionResultReport
			err    error
		}{runs: runs, result: result, err: err}
	}()

	waitForStartedVariants(t, started, "A", "B")
	select {
	case <-infraReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("infra variant did not return")
	}
	close(releaseB)

	select {
	case got := <-done:
		if !errors.Is(got.err, infraErr) {
			t.Fatalf("parallel report error = %v, want infra error", got.err)
		}
		if got.result.Status != "upload_failed" || got.result.ExitCode != 1 {
			t.Fatalf("parallel report result = %+v, want upload_failed exit 1", got.result)
		}
		if len(got.runs) != 2 {
			t.Fatalf("parallel report runs = %+v, want both drained runs", got.runs)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("parallel report matrix did not finish")
	}
}

func waitForStartedVariants(t *testing.T, started <-chan string, names ...string) {
	t.Helper()
	want := make(map[string]bool, len(names))
	for _, name := range names {
		want[name] = true
	}
	seen := make(map[string]bool, len(names))
	for len(seen) < len(want) {
		select {
		case variant := <-started:
			if want[variant] {
				seen[variant] = true
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("started variants = %v, want %v", seen, names)
		}
	}
}

func runMatrixGitTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return string(out)
}

func writeMatrixFile(t *testing.T, root, rel, contents string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}
