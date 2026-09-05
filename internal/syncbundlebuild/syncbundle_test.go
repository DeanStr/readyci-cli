package syncbundlebuild

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	. "github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func TestBuildExtractMaterializesGitWorkspace(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	initRepo(t, repo)

	writeFile(t, repo, "changed.txt", "base\n")
	writeFile(t, repo, "deleted.txt", "delete me\n")
	writeFile(t, repo, "staged-delete.txt", "delete from index\n")
	writeFile(t, repo, "file-to-dir", "replace with directory\n")
	writeFile(t, repo, "space name .txt", "space\n")
	writeFile(t, repo, ".env", "secret\n")
	writeFile(t, repo, ".env.example", "DATABASE_URL=postgres://example\n")
	writeFile(t, repo, ".envrc", "secret\n")
	writeFile(t, repo, "nested/app.env.example", "APP_ENV=example\n")
	writeFile(t, repo, "nested/.envrc.local", "secret\n")
	writeFile(t, repo, ".gitignore", "*.ignored\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "baseline")

	writeFile(t, repo, "changed.txt", "updated\n")
	if err := os.Remove(filepath.Join(repo, "deleted.txt")); err != nil {
		t.Fatalf("remove deleted file: %v", err)
	}
	runGitTest(t, repo, "rm", "staged-delete.txt")
	if err := os.Remove(filepath.Join(repo, "file-to-dir")); err != nil {
		t.Fatalf("remove file-to-dir file: %v", err)
	}
	writeFile(t, repo, "file-to-dir/nested.txt", "nested\n")
	writeFile(t, repo, "untracked.txt", "new\n")
	writeFile(t, repo, "skip.ignored", "ignored\n")
	writeFile(t, repo, ".readyciignore", "local-secret.txt\n")
	writeFile(t, repo, "local-secret.txt", "secret\n")

	var bundle bytes.Buffer
	result, err := Build(ctx, repo, &bundle)
	if err != nil {
		t.Fatalf("build bundle: %v", err)
	}
	if result.Manifest.Git.Head == "" {
		t.Fatal("manifest missing git head")
	}
	if !result.Manifest.Git.Dirty {
		t.Fatal("manifest git dirty = false, want true")
	}
	if result.Manifest.ChangeMode != ChangeModeWorking {
		t.Fatalf("manifest change mode = %q, want %q", result.Manifest.ChangeMode, ChangeModeWorking)
	}
	if !result.IncludeUntracked {
		t.Fatal("result include_untracked = false, want true")
	}
	wantUntracked := []string{".readyciignore", "file-to-dir/nested.txt", "untracked.txt"}
	if !reflect.DeepEqual(result.UntrackedFiles, wantUntracked) {
		t.Fatalf("result untracked files = %v, want %v", result.UntrackedFiles, wantUntracked)
	}
	wantChanged := []string{".readyciignore", "changed.txt", "deleted.txt", "file-to-dir", "file-to-dir/nested.txt", "staged-delete.txt", "untracked.txt"}
	if !reflect.DeepEqual(result.Manifest.ChangedFiles, wantChanged) {
		t.Fatalf("manifest changed files = %v, want %v", result.Manifest.ChangedFiles, wantChanged)
	}

	paths := ManifestPaths(result.Manifest)
	for _, disallowed := range []string{
		"baseline/.env",
		"workspace/.env",
		"baseline/.envrc",
		"workspace/.envrc",
		"baseline/nested/.envrc.local",
		"workspace/nested/.envrc.local",
		"workspace/skip.ignored",
		"workspace/local-secret.txt",
	} {
		if contains(paths, disallowed) {
			t.Fatalf("bundle unexpectedly contains %s in %v", disallowed, paths)
		}
	}
	for _, disallowed := range []string{"workspace/.gitignore"} {
		if contains(paths, disallowed) {
			t.Fatalf("unchanged tracked file %s should not be in workspace overlay: %v", disallowed, paths)
		}
	}
	for _, required := range []string{
		"baseline/.env.example",
		"baseline/changed.txt",
		"baseline/nested/app.env.example",
		"workspace/changed.txt",
		"workspace/file-to-dir/nested.txt",
		"workspace/untracked.txt",
		"deleted/deleted.txt",
		"deleted/file-to-dir",
		"deleted/staged-delete.txt",
	} {
		if !contains(paths, required) {
			t.Fatalf("bundle missing %s in %v", required, paths)
		}
	}

	workspace := t.TempDir()
	if _, err := extractBundleForTest(ctx, bytes.NewReader(bundle.Bytes()), workspace); err != nil {
		t.Fatalf("extract bundle: %v", err)
	}

	assertFile(t, workspace, "changed.txt", "updated\n")
	assertFile(t, workspace, ".env.example", "DATABASE_URL=postgres://example\n")
	assertFile(t, workspace, ChangedFilesManifestPath, strings.Join(wantChanged, "\n")+"\n")
	assertFile(t, workspace, "file-to-dir/nested.txt", "nested\n")
	assertFile(t, workspace, "nested/app.env.example", "APP_ENV=example\n")
	assertFile(t, workspace, "space name .txt", "space\n")
	assertFile(t, workspace, "untracked.txt", "new\n")
	assertNotExists(t, workspace, "deleted.txt")
	assertNotExists(t, workspace, "staged-delete.txt")
	assertNotExists(t, workspace, ".env")
	assertNotExists(t, workspace, ".envrc")
	assertNotExists(t, workspace, "nested/.envrc.local")

	status := runGitTest(t, workspace, "status", "--short")
	for _, want := range []string{" M changed.txt", " D deleted.txt", " D file-to-dir", " D staged-delete.txt", "?? untracked.txt"} {
		if !strings.Contains(status, want) {
			t.Fatalf("git status = %q, missing %q", status, want)
		}
	}
	diff := strings.Fields(runGitTest(t, workspace, "diff", "--name-only", "HEAD"))
	if want := []string{"changed.txt", "deleted.txt", "file-to-dir", "staged-delete.txt"}; !reflect.DeepEqual(diff, want) {
		t.Fatalf("git diff --name-only HEAD = %v, want %v", diff, want)
	}
}

func TestDeltaBundleRehydratesWithBaselineBundle(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	initRepo(t, repo)

	writeFile(t, repo, "base.txt", "base\n")
	writeFile(t, repo, "changed.txt", "base\n")
	writeFile(t, repo, "deleted.txt", "delete\n")
	writeFile(t, repo, "file-to-dir", "replace me\n")
	symlinksSupported := true
	if err := os.Symlink("base.txt", filepath.Join(repo, "base-link.txt")); err != nil {
		symlinksSupported = false
	}
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "baseline")

	writeFile(t, repo, "changed.txt", "updated\n")
	if err := os.Remove(filepath.Join(repo, "deleted.txt")); err != nil {
		t.Fatalf("remove deleted file: %v", err)
	}
	if err := os.Remove(filepath.Join(repo, "file-to-dir")); err != nil {
		t.Fatalf("remove file-to-dir file: %v", err)
	}
	writeFile(t, repo, "file-to-dir/nested.txt", "nested\n")
	writeFile(t, repo, "new.txt", "new\n")
	if symlinksSupported {
		if err := os.Symlink("new.txt", filepath.Join(repo, "new-link.txt")); err != nil {
			t.Fatalf("create workspace symlink: %v", err)
		}
	}

	fullPath, fullResult, deltaPath, deltaResult, err := BuildFullAndDeltaToTempWithOptions(ctx, repo, BuildOptions{})
	if err != nil {
		t.Fatalf("build full and delta bundle: %v", err)
	}
	defer os.Remove(fullPath)
	defer os.Remove(deltaPath)
	fullData, err := os.ReadFile(fullPath)
	if err != nil {
		t.Fatalf("read full bundle: %v", err)
	}
	deltaData, err := os.ReadFile(deltaPath)
	if err != nil {
		t.Fatalf("read delta bundle: %v", err)
	}
	if fullResult.Manifest.BaselineKey == "" {
		t.Fatal("full bundle missing baseline key")
	}
	if fullResult.Manifest.BaselineRef {
		t.Fatal("full bundle unexpectedly references baseline")
	}
	if !deltaResult.Manifest.BaselineRef {
		t.Fatal("delta bundle does not reference baseline")
	}
	if deltaResult.Manifest.BaselineKey != fullResult.Manifest.BaselineKey {
		t.Fatalf("delta baseline key = %q, want %q", deltaResult.Manifest.BaselineKey, fullResult.Manifest.BaselineKey)
	}
	wantChanged := []string{"changed.txt", "deleted.txt", "file-to-dir", "file-to-dir/nested.txt", "new.txt"}
	if symlinksSupported {
		wantChanged = []string{"changed.txt", "deleted.txt", "file-to-dir", "file-to-dir/nested.txt", "new-link.txt", "new.txt"}
	}
	if !reflect.DeepEqual(deltaResult.Manifest.ChangedFiles, wantChanged) {
		t.Fatalf("delta changed files = %v, want %v", deltaResult.Manifest.ChangedFiles, wantChanged)
	}
	_, deltaFiles, err := ReadBundleContents(bytes.NewReader(deltaData))
	if err != nil {
		t.Fatalf("read delta bundle: %v", err)
	}
	if _, ok := deltaFiles["baseline/base.txt"]; ok {
		t.Fatalf("delta bundle unexpectedly includes baseline/base.txt")
	}
	if _, ok := deltaFiles["baseline/base-link.txt"]; ok {
		t.Fatalf("delta bundle unexpectedly includes baseline/base-link.txt")
	}
	for _, required := range []string{"workspace/changed.txt", "workspace/file-to-dir/nested.txt", "workspace/new.txt"} {
		if _, ok := deltaFiles[required]; !ok {
			t.Fatalf("delta bundle missing %s", required)
		}
	}
	if wantDeleted := []string{"deleted.txt", "file-to-dir"}; !reflect.DeepEqual(deltaResult.Manifest.Deleted, wantDeleted) {
		t.Fatalf("delta deleted paths = %v, want %v", deltaResult.Manifest.Deleted, wantDeleted)
	}
	if symlinksSupported {
		if file, ok := deltaFiles["workspace/new-link.txt"]; !ok {
			t.Fatal("delta bundle missing workspace/new-link.txt")
		} else if file.EntryType != tar.TypeSymlink || file.Linkname != "new.txt" {
			t.Fatalf("delta new-link entry = type %v target %q, want symlink to new.txt", file.EntryType, file.Linkname)
		}
	}
	if err := Validate(bytes.NewReader(deltaData)); err != nil {
		t.Fatalf("validate delta bundle: %v", err)
	}

	var baselineOnly bytes.Buffer
	baselineManifest, err := WriteBaselineBundle(bytes.NewReader(fullData), &baselineOnly)
	if err != nil {
		t.Fatalf("write baseline-only bundle: %v", err)
	}
	if baselineManifest.BaselineKey != fullResult.Manifest.BaselineKey {
		t.Fatalf("baseline-only key = %q, want %q", baselineManifest.BaselineKey, fullResult.Manifest.BaselineKey)
	}
	_, baselineOnlyFiles, err := ReadBundleContents(bytes.NewReader(baselineOnly.Bytes()))
	if err != nil {
		t.Fatalf("read baseline-only bundle: %v", err)
	}
	if _, ok := baselineOnlyFiles["workspace/changed.txt"]; ok {
		t.Fatal("baseline-only bundle unexpectedly includes workspace/changed.txt")
	}
	if _, ok := baselineOnlyFiles["workspace/new.txt"]; ok {
		t.Fatal("baseline-only bundle unexpectedly includes workspace/new.txt")
	}

	var rehydrated bytes.Buffer
	manifest, err := RehydrateDeltaBundle(bytes.NewReader(baselineOnly.Bytes()), bytes.NewReader(deltaData), &rehydrated)
	if err != nil {
		t.Fatalf("rehydrate delta bundle: %v", err)
	}
	if manifest.BaselineRef {
		t.Fatal("rehydrated manifest still references baseline")
	}

	workspace := t.TempDir()
	if _, err := extractBundleForTest(ctx, bytes.NewReader(rehydrated.Bytes()), workspace); err != nil {
		t.Fatalf("extract rehydrated bundle: %v", err)
	}
	assertFile(t, workspace, "base.txt", "base\n")
	assertFile(t, workspace, "changed.txt", "updated\n")
	assertNotExists(t, workspace, "deleted.txt")
	assertFile(t, workspace, "file-to-dir/nested.txt", "nested\n")
	assertFile(t, workspace, "new.txt", "new\n")
	assertFile(t, workspace, ChangedFilesManifestPath, strings.Join(manifest.ChangedFiles, "\n")+"\n")
	if symlinksSupported {
		baseLinkTarget, err := os.Readlink(filepath.Join(workspace, "base-link.txt"))
		if err != nil {
			t.Fatalf("read baseline symlink: %v", err)
		}
		if baseLinkTarget != "base.txt" {
			t.Fatalf("baseline symlink target = %q, want base.txt", baseLinkTarget)
		}
		newLinkTarget, err := os.Readlink(filepath.Join(workspace, "new-link.txt"))
		if err != nil {
			t.Fatalf("read workspace symlink: %v", err)
		}
		if newLinkTarget != "new.txt" {
			t.Fatalf("workspace symlink target = %q, want new.txt", newLinkTarget)
		}
	}
}

func TestBuildToTempReadManifestAndValidateWithManifest(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	initRepo(t, repo)

	writeFile(t, repo, "tracked.txt", "base\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "baseline")
	writeFile(t, repo, "tracked.txt", "updated\n")
	writeFile(t, repo, "untracked.txt", "new\n")

	path, result, err := BuildToTemp(ctx, repo)
	if err != nil {
		t.Fatalf("build to temp: %v", err)
	}
	defer os.Remove(path)
	if result.Size <= 0 {
		t.Fatalf("bundle size = %d, want positive", result.Size)
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open bundle: %v", err)
	}
	manifest, err := ReadManifest(file)
	file.Close()
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if manifest.BaselineKey != result.Manifest.BaselineKey {
		t.Fatalf("read manifest baseline key = %q, want %q", manifest.BaselineKey, result.Manifest.BaselineKey)
	}
	if want := []string{"tracked.txt", "untracked.txt"}; !reflect.DeepEqual(manifest.ChangedFiles, want) {
		t.Fatalf("read manifest changed files = %v, want %v", manifest.ChangedFiles, want)
	}

	file, err = os.Open(path)
	if err != nil {
		t.Fatalf("reopen bundle: %v", err)
	}
	validated, err := ValidateWithManifest(file)
	file.Close()
	if err != nil {
		t.Fatalf("validate with manifest: %v", err)
	}
	if validated.BaselineKey != result.Manifest.BaselineKey {
		t.Fatalf("validated manifest baseline key = %q, want %q", validated.BaselineKey, result.Manifest.BaselineKey)
	}

	workspace := t.TempDir()
	file, err = os.Open(path)
	if err != nil {
		t.Fatalf("open bundle for extract: %v", err)
	}
	if _, err := extractBundleForTest(ctx, file, workspace); err != nil {
		file.Close()
		t.Fatalf("extract temp bundle: %v", err)
	}
	file.Close()
	assertFile(t, workspace, "tracked.txt", "updated\n")
	assertFile(t, workspace, "untracked.txt", "new\n")
}

func TestBuildDoesNotUseLegacyPreflightIgnore(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	initRepo(t, repo)

	writeFile(t, repo, "keep.txt", "keep\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "baseline")

	writeFile(t, repo, ".preflightignore", "legacy-secret.txt\n")
	writeFile(t, repo, "legacy-secret.txt", "secret\n")
	writeFile(t, repo, "visible.txt", "visible\n")

	var bundle bytes.Buffer
	result, err := Build(ctx, repo, &bundle)
	if err != nil {
		t.Fatalf("build bundle: %v", err)
	}
	paths := ManifestPaths(result.Manifest)
	if !contains(paths, "workspace/legacy-secret.txt") {
		t.Fatalf("legacy ignore unexpectedly applied: %v", paths)
	}
	if !contains(paths, "workspace/visible.txt") {
		t.Fatalf("visible file missing from bundle: %v", paths)
	}
}

func TestBuildPrefersReadyCIIgnoreOverLegacyPreflightIgnore(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	initRepo(t, repo)

	writeFile(t, repo, "keep.txt", "keep\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "baseline")

	writeFile(t, repo, ".readyciignore", "ready-secret.txt\n")
	writeFile(t, repo, ".preflightignore", "legacy-secret.txt\n")
	writeFile(t, repo, "ready-secret.txt", "secret\n")
	writeFile(t, repo, "legacy-secret.txt", "legacy\n")

	var bundle bytes.Buffer
	result, err := Build(ctx, repo, &bundle)
	if err != nil {
		t.Fatalf("build bundle: %v", err)
	}
	paths := ManifestPaths(result.Manifest)
	if contains(paths, "workspace/ready-secret.txt") {
		t.Fatalf("Ready CI ignored file was bundled: %v", paths)
	}
	if !contains(paths, "workspace/legacy-secret.txt") {
		t.Fatalf("legacy ignore should not apply when .readyciignore exists: %v", paths)
	}
}

func TestBuildWithOptionsSupportsStagedChangeManifest(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	initRepo(t, repo)

	writeFile(t, repo, "staged.txt", "base\n")
	writeFile(t, repo, "working.txt", "base\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "baseline")

	writeFile(t, repo, "staged.txt", "staged\n")
	runGitTest(t, repo, "add", "staged.txt")
	writeFile(t, repo, "working.txt", "working\n")
	writeFile(t, repo, "untracked.txt", "new\n")

	var bundle bytes.Buffer
	result, err := BuildWithOptions(ctx, repo, &bundle, BuildOptions{ChangeMode: ChangeModeStaged})
	if err != nil {
		t.Fatalf("build staged bundle: %v", err)
	}
	if result.Manifest.ChangeMode != ChangeModeStaged {
		t.Fatalf("manifest change mode = %q, want %q", result.Manifest.ChangeMode, ChangeModeStaged)
	}
	if want := []string{"staged.txt"}; !reflect.DeepEqual(result.Manifest.ChangedFiles, want) {
		t.Fatalf("manifest changed files = %v, want %v", result.Manifest.ChangedFiles, want)
	}

	workspace := t.TempDir()
	if _, err := extractBundleForTest(ctx, bytes.NewReader(bundle.Bytes()), workspace); err != nil {
		t.Fatalf("extract bundle: %v", err)
	}
	assertFile(t, workspace, ChangedFilesManifestPath, "staged.txt\n")
}

func TestBuildWorkingModeDerivesChangesFromMaterializedWorkspace(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	initRepo(t, repo)

	writeFile(t, repo, "restored.txt", "base\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "baseline")

	writeFile(t, repo, "restored.txt", "staged\n")
	runGitTest(t, repo, "add", "restored.txt")
	writeFile(t, repo, "restored.txt", "base\n")

	var bundle bytes.Buffer
	result, err := Build(ctx, repo, &bundle)
	if err != nil {
		t.Fatalf("build working bundle: %v", err)
	}
	if len(result.Manifest.ChangedFiles) != 0 {
		t.Fatalf("manifest changed files = %v, want no materialized changes", result.Manifest.ChangedFiles)
	}

	workspace := t.TempDir()
	if _, err := extractBundleForTest(ctx, bytes.NewReader(bundle.Bytes()), workspace); err != nil {
		t.Fatalf("extract working bundle: %v", err)
	}
	assertFile(t, workspace, "restored.txt", "base\n")
	assertFile(t, workspace, ChangedFilesManifestPath, "")
}

func TestBuildWithOptionsStagesIndexSnapshot(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	initRepo(t, repo)

	writeFile(t, repo, "staged.txt", "base staged\n")
	writeFile(t, repo, "working.txt", "base working\n")
	writeFile(t, repo, "deleted.txt", "base delete\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "baseline")

	writeFile(t, repo, "staged.txt", "index staged\n")
	runGitTest(t, repo, "add", "staged.txt")
	writeFile(t, repo, "staged.txt", "unstaged after add\n")

	writeFile(t, repo, "new.txt", "index new\n")
	runGitTest(t, repo, "add", "new.txt")
	writeFile(t, repo, "new.txt", "unstaged new\n")

	runGitTest(t, repo, "rm", "deleted.txt")
	writeFile(t, repo, "deleted.txt", "unstaged restore\n")
	writeFile(t, repo, "working.txt", "unstaged only\n")
	writeFile(t, repo, "untracked.txt", "untracked\n")

	var bundle bytes.Buffer
	result, err := BuildWithOptions(ctx, repo, &bundle, BuildOptions{ChangeMode: ChangeModeStaged})
	if err != nil {
		t.Fatalf("build staged bundle: %v", err)
	}
	wantChanged := []string{"deleted.txt", "new.txt", "staged.txt"}
	if !reflect.DeepEqual(result.Manifest.ChangedFiles, wantChanged) {
		t.Fatalf("manifest changed files = %v, want %v", result.Manifest.ChangedFiles, wantChanged)
	}
	paths := ManifestPaths(result.Manifest)
	for _, disallowed := range []string{"workspace/working.txt", "workspace/untracked.txt", "workspace/deleted.txt"} {
		if contains(paths, disallowed) {
			t.Fatalf("staged bundle unexpectedly contains %s in %v", disallowed, paths)
		}
	}
	for _, required := range []string{"workspace/staged.txt", "workspace/new.txt", "deleted/deleted.txt"} {
		if !contains(paths, required) {
			t.Fatalf("staged bundle missing %s in %v", required, paths)
		}
	}

	workspace := t.TempDir()
	if _, err := extractBundleForTest(ctx, bytes.NewReader(bundle.Bytes()), workspace); err != nil {
		t.Fatalf("extract bundle: %v", err)
	}
	assertFile(t, workspace, "staged.txt", "index staged\n")
	assertFile(t, workspace, "new.txt", "index new\n")
	assertFile(t, workspace, "working.txt", "base working\n")
	assertFile(t, workspace, ChangedFilesManifestPath, strings.Join(wantChanged, "\n")+"\n")
	assertNotExists(t, workspace, "deleted.txt")
	assertNotExists(t, workspace, "untracked.txt")
}

func TestPlanIndexPathsAvoidsLargePathArgv(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	initRepo(t, repo)

	writeFile(t, repo, "kept.txt", "index\n")
	runGitTest(t, repo, "add", "kept.txt")

	longName := strings.Repeat("x", 180) + ".txt"
	paths := make([]string, 0, 12001)
	for i := 0; i < 12000; i++ {
		paths = append(paths, fmt.Sprintf("huge/%05d/%s", i, longName))
	}
	paths = append(paths, "kept.txt")

	entries, err := planIndexPaths(ctx, repo, paths, newIgnoreMatcher(nil))
	if err != nil {
		t.Fatalf("plan index paths: %v", err)
	}
	if len(entries) != 1 || entries[0].entry.Path != "kept.txt" {
		t.Fatalf("entries = %+v, want kept.txt only", entries)
	}
}

func TestBuildWithOptionsStagesIndexSnapshotBeforeFirstCommit(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	initRepo(t, repo)

	writeFile(t, repo, "staged.txt", "index staged\n")
	runGitTest(t, repo, "add", "staged.txt")
	writeFile(t, repo, "staged.txt", "unstaged after add\n")
	writeFile(t, repo, "untracked.txt", "untracked\n")

	var bundle bytes.Buffer
	result, err := BuildWithOptions(ctx, repo, &bundle, BuildOptions{ChangeMode: ChangeModeStaged})
	if err != nil {
		t.Fatalf("build staged bundle: %v", err)
	}
	if want := []string{"staged.txt"}; !reflect.DeepEqual(result.Manifest.ChangedFiles, want) {
		t.Fatalf("manifest changed files = %v, want %v", result.Manifest.ChangedFiles, want)
	}

	workspace := t.TempDir()
	if _, err := extractBundleForTest(ctx, bytes.NewReader(bundle.Bytes()), workspace); err != nil {
		t.Fatalf("extract bundle: %v", err)
	}
	assertFile(t, workspace, "staged.txt", "index staged\n")
	assertNotExists(t, workspace, "untracked.txt")
}

func TestBuildWithOptionsStagesRenamesAndReplacements(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	initRepo(t, repo)

	writeFile(t, repo, "renamed-old.txt", "rename me\n")
	writeFile(t, repo, "file-to-dir", "replace me\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "baseline")

	runGitTest(t, repo, "mv", "renamed-old.txt", "renamed-new.txt")
	writeFile(t, repo, "renamed-new.txt", "renamed staged\n")
	runGitTest(t, repo, "add", "renamed-new.txt")

	if err := os.Remove(filepath.Join(repo, "file-to-dir")); err != nil {
		t.Fatalf("remove file-to-dir: %v", err)
	}
	writeFile(t, repo, "file-to-dir/nested.txt", "nested staged\n")
	runGitTest(t, repo, "add", "-A", "file-to-dir")

	var bundle bytes.Buffer
	if _, err := BuildWithOptions(ctx, repo, &bundle, BuildOptions{ChangeMode: ChangeModeStaged}); err != nil {
		t.Fatalf("build staged bundle: %v", err)
	}
	workspace := t.TempDir()
	if _, err := extractBundleForTest(ctx, bytes.NewReader(bundle.Bytes()), workspace); err != nil {
		t.Fatalf("extract bundle: %v", err)
	}
	assertNotExists(t, workspace, "renamed-old.txt")
	assertFile(t, workspace, "renamed-new.txt", "renamed staged\n")
	assertFile(t, workspace, "file-to-dir/nested.txt", "nested staged\n")
}

func TestBuildWithOptionsAppliesSyncExclusionsAndUntrackedMode(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	initRepo(t, repo)

	writeFile(t, repo, "kept.txt", "base\n")
	writeFile(t, repo, "secret.txt", "secret\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "baseline")

	writeFile(t, repo, "kept.txt", "updated\n")
	writeFile(t, repo, "secret.txt", "new secret\n")
	writeFile(t, repo, "untracked.txt", "new\n")

	includeUntracked := false
	var bundle bytes.Buffer
	result, err := BuildWithOptions(ctx, repo, &bundle, BuildOptions{
		ChangeMode:       ChangeModeWorking,
		ExcludePatterns:  []string{"secret.txt"},
		IncludeUntracked: &includeUntracked,
	})
	if err != nil {
		t.Fatalf("build bundle: %v", err)
	}

	paths := ManifestPaths(result.Manifest)
	for _, disallowed := range []string{"baseline/secret.txt", "workspace/secret.txt", "workspace/untracked.txt"} {
		if contains(paths, disallowed) {
			t.Fatalf("bundle unexpectedly contains %s in %v", disallowed, paths)
		}
	}
	if want := []string{"kept.txt"}; !reflect.DeepEqual(result.Manifest.ChangedFiles, want) {
		t.Fatalf("changed files = %v, want %v", result.Manifest.ChangedFiles, want)
	}

	workspace := t.TempDir()
	if _, err := extractBundleForTest(ctx, bytes.NewReader(bundle.Bytes()), workspace); err != nil {
		t.Fatalf("extract bundle: %v", err)
	}
	assertFile(t, workspace, "kept.txt", "updated\n")
	assertNotExists(t, workspace, "secret.txt")
	assertNotExists(t, workspace, "untracked.txt")
}

func TestChangedFilesCollectsNamesWithoutBundlingContents(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	initRepo(t, repo)

	writeFile(t, repo, "kept.txt", "base\n")
	writeFile(t, repo, "ignored.txt", "base\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "baseline")

	writeFile(t, repo, "kept.txt", "updated\n")
	writeFile(t, repo, "ignored.txt", "ignored update\n")
	writeFile(t, repo, "untracked.txt", strings.Repeat("x", 1024*1024))

	result, err := ChangedFiles(ctx, repo, BuildOptions{
		ChangeMode:      ChangeModeWorking,
		ExcludePatterns: []string{"ignored.txt"},
	})
	if err != nil {
		t.Fatalf("changed files: %v", err)
	}
	if result.Root != repo {
		t.Fatalf("root = %q, want %q", result.Root, repo)
	}
	want := []string{"kept.txt", "untracked.txt"}
	if !reflect.DeepEqual(result.ChangedFiles, want) {
		t.Fatalf("changed files = %v, want %v", result.ChangedFiles, want)
	}
	if len(result.ChangedFiles) == 0 {
		t.Fatal("changed files empty")
	}
}

func TestRegisteredSourcePolicyDisablesCandidateReadyCIIgnoreAndBindsFilter(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	initRepo(t, repo)

	writeFile(t, repo, "kept.txt", "base\n")
	writeFile(t, repo, "candidate-hidden.txt", "base\n")
	writeFile(t, repo, "trusted-generated/output.txt", "base\n")
	writeFile(t, repo, "dist/tracked.js", "base\n")
	writeFile(t, repo, ".readyciignore", "candidate-hidden.txt\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "baseline")

	writeFile(t, repo, "kept.txt", "updated\n")
	writeFile(t, repo, "candidate-hidden.txt", "updated\n")
	writeFile(t, repo, "trusted-generated/output.txt", "updated\n")
	writeFile(t, repo, "dist/tracked.js", "updated\n")

	includeUntracked := true
	useProjectIgnore := false
	const digest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	var bundle bytes.Buffer
	result, err := BuildWithOptions(ctx, repo, &bundle, BuildOptions{
		ChangeMode:             ChangeModeWorking,
		ExcludePatterns:        []string{"trusted-generated/**"},
		DefaultExcludePatterns: DefaultSourceExcludePatterns(),
		IncludeUntracked:       &includeUntracked,
		UseProjectIgnore:       &useProjectIgnore,
		SourcePolicyDigest:     digest,
	})
	if err != nil {
		t.Fatalf("build registered-policy bundle: %v", err)
	}
	if got, want := result.Manifest.ChangedFiles, []string{"candidate-hidden.txt", "kept.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("changed files = %v, want candidate ignore disabled and trusted exclude applied: %v", got, want)
	}
	if result.Manifest.SourcePolicy == nil ||
		result.Manifest.SourcePolicy.Digest != digest ||
		!reflect.DeepEqual(result.Manifest.SourcePolicy.ExcludePatterns, []string{"trusted-generated/**"}) ||
		!reflect.DeepEqual(result.Manifest.SourcePolicy.DefaultExcludePatterns, DefaultSourceExcludePatterns()) ||
		!result.Manifest.SourcePolicy.IncludeUntracked ||
		result.Manifest.SourcePolicy.ProjectIgnoreUsed {
		t.Fatalf("source policy binding = %+v", result.Manifest.SourcePolicy)
	}
}

func TestChangedFilesStagedDisablesUntrackedEffectively(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	initRepo(t, repo)

	writeFile(t, repo, "tracked.txt", "base\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "baseline")
	writeFile(t, repo, "tracked.txt", "staged\n")
	runGitTest(t, repo, "add", "tracked.txt")
	writeFile(t, repo, "untracked.txt", "new\n")

	includeUntracked := true
	result, err := ChangedFiles(ctx, repo, BuildOptions{
		ChangeMode:       ChangeModeStaged,
		IncludeUntracked: &includeUntracked,
	})
	if err != nil {
		t.Fatalf("changed files: %v", err)
	}
	if result.IncludeUntracked {
		t.Fatal("staged changed files reported include_untracked=true, want effective false")
	}
	if want := []string{"tracked.txt"}; !reflect.DeepEqual(result.ChangedFiles, want) {
		t.Fatalf("changed files = %v, want %v", result.ChangedFiles, want)
	}
}

// A rename that moves a file into a different rule domain must report BOTH the
// deleted source and the added destination. Git's default rename detection
// collapses the pair to the destination only, which would hide the source path
// from affected-rule matching and skip the checks its old location required.
// planChangedFiles passes --no-renames so both sides surface. This exercises the
// default `affected` (working), `affected --staged`, and all-change paths.
func TestChangedFilesReportsBothSidesOfCrossDomainRename(t *testing.T) {
	for _, changeMode := range []string{ChangeModeWorking, ChangeModeStaged, ChangeModeAll} {
		t.Run(changeMode, func(t *testing.T) {
			ctx := context.Background()
			repo := t.TempDir()
			initRepo(t, repo)

			writeFile(t, repo, "apps/web/src/foo.ts", "export const foo = 1;\n")
			runGitTest(t, repo, "add", ".")
			runGitTest(t, repo, "commit", "-m", "baseline")

			// git mv stages the rename; default detection would collapse it.
			if err := os.MkdirAll(filepath.Join(repo, "docs"), 0o755); err != nil {
				t.Fatalf("mkdir docs: %v", err)
			}
			runGitTest(t, repo, "mv", "apps/web/src/foo.ts", "docs/foo.md")

			result, err := ChangedFiles(ctx, repo, BuildOptions{ChangeMode: changeMode})
			if err != nil {
				t.Fatalf("changed files: %v", err)
			}
			want := []string{"apps/web/src/foo.ts", "docs/foo.md"}
			if !reflect.DeepEqual(result.ChangedFiles, want) {
				t.Fatalf("changed files = %v, want %v", result.ChangedFiles, want)
			}
		})
	}
}

func TestBuildRejectsSymlinkedParentOutsideWorkspace(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	outside := t.TempDir()
	initRepo(t, repo)

	writeFile(t, repo, "dir/file.txt", "base\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "baseline")

	writeFile(t, outside, "file.txt", "outside secret\n")
	if err := os.RemoveAll(filepath.Join(repo, "dir")); err != nil {
		t.Fatalf("remove dir: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, "dir")); err != nil {
		t.Fatalf("symlink dir: %v", err)
	}

	var bundle bytes.Buffer
	if _, err := Build(ctx, repo, &bundle); err == nil || !strings.Contains(err.Error(), "invalid target") {
		t.Fatalf("build symlinked parent error = %v, want invalid target", err)
	}
}

func TestBuildRejectsRawBackslashPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("backslash is a path separator on Windows")
	}

	ctx := context.Background()
	repo := t.TempDir()
	initRepo(t, repo)
	if err := os.WriteFile(filepath.Join(repo, `dir\file.txt`), []byte("ambiguous\n"), 0o644); err != nil {
		t.Fatalf("write backslash path: %v", err)
	}

	var bundle bytes.Buffer
	if _, err := Build(ctx, repo, &bundle); err == nil || !strings.Contains(err.Error(), "contains backslash") {
		t.Fatalf("build backslash path error = %v, want backslash rejection", err)
	}
}

func initRepo(t *testing.T, repo string) {
	t.Helper()
	runGitTest(t, repo, "init")
	runGitTest(t, repo, "config", "user.name", "Preflight Test")
	runGitTest(t, repo, "config", "user.email", "preflight-test@example.invalid")
}

func runGitTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func writeFile(t *testing.T, root, rel, value string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func assertFile(t *testing.T, root, rel, want string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	if string(data) != want {
		t.Fatalf("%s = %q, want %q", rel, data, want)
	}
}

func assertNotExists(t *testing.T, root, rel string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
		t.Fatalf("%s exists, want absent", rel)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat %s: %v", rel, err)
	}
}

func extractBundleForTest(ctx context.Context, reader io.Reader, workspace string) (Manifest, error) {
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		return Manifest{}, err
	}
	manifest, files, err := ReadBundleContents(reader)
	if err != nil {
		return Manifest{}, err
	}
	entries, err := os.ReadDir(workspace)
	if err != nil {
		return Manifest{}, err
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(workspace, entry.Name())); err != nil {
			return Manifest{}, err
		}
	}
	if err := materializeBundleEntriesForTest(workspace, "baseline", manifest.Baseline, files); err != nil {
		return Manifest{}, err
	}
	if _, err := runGitForTest(ctx, workspace, "init"); err != nil {
		return Manifest{}, err
	}
	if _, err := runGitForTest(ctx, workspace, "config", "user.name", "Preflight"); err != nil {
		return Manifest{}, err
	}
	if _, err := runGitForTest(ctx, workspace, "config", "user.email", "preflight@example.invalid"); err != nil {
		return Manifest{}, err
	}
	if manifest.Git.Branch != "" {
		if _, err := runGitForTest(ctx, workspace, "checkout", "-B", manifest.Git.Branch); err != nil {
			return Manifest{}, err
		}
	}
	if _, err := runGitForTest(ctx, workspace, "add", "-A"); err != nil {
		return Manifest{}, err
	}
	if _, err := runGitForTest(ctx, workspace, "commit", "--allow-empty", "-m", "preflight baseline"); err != nil {
		return Manifest{}, err
	}
	for _, deleted := range manifest.Deleted {
		rel, err := ValidateRelativePath(deleted)
		if err != nil {
			return Manifest{}, err
		}
		if err := os.RemoveAll(filepath.Join(workspace, filepath.FromSlash(rel))); err != nil {
			return Manifest{}, err
		}
	}
	if err := materializeBundleEntriesForTest(workspace, "workspace", manifest.Workspace, files); err != nil {
		return Manifest{}, err
	}
	changedFilesPath := filepath.Join(workspace, filepath.FromSlash(ChangedFilesManifestPath))
	if err := os.MkdirAll(filepath.Dir(changedFilesPath), 0o755); err != nil {
		return Manifest{}, err
	}
	data := strings.Join(manifest.ChangedFiles, "\n")
	if data != "" {
		data += "\n"
	}
	if err := os.WriteFile(changedFilesPath, []byte(data), 0o644); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func materializeBundleEntriesForTest(workspace, prefix string, entries []Entry, files map[string]BundleFile) error {
	for _, entry := range entries {
		rel, err := ValidateRelativePath(entry.Path)
		if err != nil {
			return err
		}
		key := prefix + "/" + rel
		file, ok := files[key]
		if !ok {
			return fmt.Errorf("bundle is missing %s", key)
		}
		target := filepath.Join(workspace, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.RemoveAll(target); err != nil {
			return err
		}
		switch entry.Type {
		case EntryTypeFile:
			if file.EntryType != tar.TypeReg {
				return fmt.Errorf("%s is not a regular file in bundle", key)
			}
			if int64(len(file.Data)) != entry.Size {
				return fmt.Errorf("%s size mismatch", key)
			}
			if err := os.WriteFile(target, file.Data, os.FileMode(entry.Mode)); err != nil {
				return err
			}
		case EntryTypeSymlink:
			if file.EntryType != tar.TypeSymlink {
				return fmt.Errorf("%s is not a symlink in bundle", key)
			}
			if file.Linkname != entry.Linkname {
				return fmt.Errorf("%s symlink target mismatch", key)
			}
			if err := os.Symlink(entry.Linkname, target); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%s has unsupported type %q", key, entry.Type)
		}
	}
	return nil
}

func runGitForTest(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmdArgs := append([]string{"-c", "core.hooksPath=/dev/null"}, args...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s failed: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return out, nil
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func writeTestBundle(t *testing.T, writer *bytes.Buffer, manifest Manifest, files map[string]string) {
	t.Helper()
	gzipWriter := gzip.NewWriter(writer)
	tarWriter := tar.NewWriter(gzipWriter)
	writeManifestEntry(t, tarWriter, manifest)
	for name, data := range files {
		writeTarFileEntry(t, tarWriter, name, data)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
}

func writeTestBundleWithSymlinks(t *testing.T, writer *bytes.Buffer, manifest Manifest, files, symlinks map[string]string) {
	t.Helper()
	gzipWriter := gzip.NewWriter(writer)
	tarWriter := tar.NewWriter(gzipWriter)
	writeManifestEntry(t, tarWriter, manifest)
	for name, data := range files {
		writeTarFileEntry(t, tarWriter, name, data)
	}
	for name, target := range symlinks {
		if err := tarWriter.WriteHeader(&tar.Header{
			Name:     name,
			Typeflag: tar.TypeSymlink,
			Linkname: target,
			Mode:     0o777,
		}); err != nil {
			t.Fatalf("write %s symlink header: %v", name, err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
}

func writeManifestEntry(t *testing.T, writer *tar.Writer, manifest Manifest) {
	t.Helper()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	writeTarFileEntry(t, writer, "manifest.json", string(data))
}

func writeTarFileEntry(t *testing.T, writer *tar.Writer, name, data string) {
	t.Helper()
	if err := writer.WriteHeader(&tar.Header{
		Name:     name,
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     int64(len(data)),
	}); err != nil {
		t.Fatalf("write %s header: %v", name, err)
	}
	if _, err := writer.Write([]byte(data)); err != nil {
		t.Fatalf("write %s data: %v", name, err)
	}
}
