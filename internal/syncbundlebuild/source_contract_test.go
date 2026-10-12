package syncbundlebuild

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func TestSourceSelectionExcludesRecursiveAndIneligibleDescendants(t *testing.T) {
	for _, include := range []bool{false, true} {
		t.Run(map[bool]string{false: "tracked-only", true: "with-untracked"}[include], func(t *testing.T) {
			repo := t.TempDir()
			initRepo(t, repo)
			writeFile(t, repo, "config", "base")
			writeFile(t, repo, "secrets/deep/token.txt", "dummy")
			writeFile(t, repo, ".gitignore", "*.private\n")
			runGitTest(t, repo, "add", ".")
			runGitTest(t, repo, "commit", "-m", "baseline")
			if err := os.Remove(filepath.Join(repo, "config")); err != nil {
				t.Fatal(err)
			}
			writeFile(t, repo, "config/token.private", "dummy")
			writeFile(t, repo, "config/public.txt", "public")
			options := BuildOptions{IncludeUntracked: &include, ExcludePatterns: []string{"secrets/**"}}
			var archive bytes.Buffer
			built, err := BuildWithOptions(context.Background(), repo, &archive, options)
			if err != nil {
				t.Fatal(err)
			}
			target := t.TempDir()
			if _, err := extractBundleForTest(context.Background(), bytes.NewReader(archive.Bytes()), target); err != nil {
				t.Fatal(err)
			}
			assertNotExists(t, target, "secrets/deep/token.txt")
			assertNotExists(t, target, "config/token.private")
			if include {
				assertFile(t, target, "config/public.txt", "public")
			} else {
				assertNotExists(t, target, "config/public.txt")
			}
			expected := []string{"config"}
			if include {
				expected = append(expected, "config/public.txt")
			}
			if !reflect.DeepEqual(built.Manifest.ChangedFiles, expected) {
				t.Fatalf("archive changed files=%v, want %v", built.Manifest.ChangedFiles, expected)
			}
			preview, err := ChangedFiles(context.Background(), repo, options)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(preview.ChangedFiles, expected) || !reflect.DeepEqual(preview.UntrackedFiles, built.UntrackedFiles) {
				t.Fatalf("preview differs from archive: %+v vs %+v", preview, built)
			}
		})
	}
}

func TestWorkingSnapshotReadsAssumeUnchangedFiles(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)
	writeFile(t, repo, "tracked.txt", "base")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "baseline")
	runGitTest(t, repo, "update-index", "--assume-unchanged", "tracked.txt")
	before, err := SnapshotWithOptions(context.Background(), repo, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, "tracked.txt", "edit")
	after, err := SnapshotWithOptions(context.Background(), repo, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if before.SnapshotDigest == after.SnapshotDigest || !reflect.DeepEqual(after.ChangedFiles, []string{"tracked.txt"}) {
		t.Fatalf("actual edit not detected: %+v", after)
	}
	preview, err := ChangedFiles(context.Background(), repo, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(preview.ChangedFiles, after.ChangedFiles) {
		t.Fatalf("preview=%v snapshot=%v", preview.ChangedFiles, after.ChangedFiles)
	}
	var archive bytes.Buffer
	built, err := BuildWithOptions(context.Background(), repo, &archive, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if built.Manifest.SnapshotDigest != after.SnapshotDigest {
		t.Fatal("snapshot and archive differ")
	}
	target := t.TempDir()
	if _, err := extractBundleForTest(context.Background(), bytes.NewReader(archive.Bytes()), target); err != nil {
		t.Fatal(err)
	}
	assertFile(t, target, "tracked.txt", "edit")
	staged, err := SnapshotWithOptions(context.Background(), repo, BuildOptions{ChangeMode: syncbundle.ChangeModeStaged})
	if err != nil {
		t.Fatal(err)
	}
	if staged.SnapshotDigest != before.SnapshotDigest || len(staged.ChangedFiles) != 0 {
		t.Fatal("staged mode did not preserve the index snapshot")
	}
}

func TestSourceSelectionRejectsUnsupportedRepositoryContent(t *testing.T) {
	for _, state := range []string{"skip-worktree", "submodule-index", "submodule-head", "sparse-checkout"} {
		t.Run(state, func(t *testing.T) {
			repo := t.TempDir()
			initRepo(t, repo)
			writeFile(t, repo, "included/a.txt", "base")
			writeFile(t, repo, "omitted/b.txt", "base")
			runGitTest(t, repo, "add", ".")
			runGitTest(t, repo, "commit", "-m", "baseline")
			message := "skip-worktree"
			switch state {
			case "skip-worktree":
				runGitTest(t, repo, "update-index", "--skip-worktree", "included/a.txt")
			case "sparse-checkout":
				runGitTest(t, repo, "sparse-checkout", "set", "included")
			default:
				message = "submodule"
				head := strings.TrimSpace(runGitTest(t, repo, "rev-parse", "HEAD"))
				runGitTest(t, repo, "update-index", "--add", "--cacheinfo", "160000,"+head+",dependency")
				if state == "submodule-head" {
					runGitTest(t, repo, "commit", "-m", "gitlink")
					runGitTest(t, repo, "rm", "--cached", "dependency")
				}
			}
			for _, operation := range []string{"build", "snapshot", "preview"} {
				var err error
				switch operation {
				case "build":
					var b bytes.Buffer
					_, err = BuildWithOptions(context.Background(), repo, &b, BuildOptions{})
				case "snapshot":
					_, err = SnapshotWithOptions(context.Background(), repo, BuildOptions{})
				case "preview":
					_, err = ChangedFiles(context.Background(), repo, BuildOptions{})
				}
				if err == nil || !strings.Contains(err.Error(), message) {
					t.Fatalf("%s error=%v, want %s rejection", operation, err, message)
				}
			}
		})
	}
}

func TestSourceSelectionRejectsUntrackedNestedRepositories(t *testing.T) {
	for _, metadata := range []string{"directory", "worktree-file"} {
		t.Run(metadata, func(t *testing.T) {
			repo := t.TempDir()
			initRepo(t, repo)
			writeFile(t, repo, "parent.txt", "parent")
			writeFile(t, repo, ".gitignore", "")
			runGitTest(t, repo, "add", ".")
			runGitTest(t, repo, "commit", "-m", "parent baseline")
			nested := filepath.Join(repo, "nested")
			if metadata == "directory" {
				writeFile(t, repo, "nested/public.txt", "nested")
				initRepo(t, nested)
				runGitTest(t, nested, "add", ".")
				runGitTest(t, nested, "commit", "-m", "nested baseline")
			} else {
				origin := t.TempDir()
				initRepo(t, origin)
				writeFile(t, origin, "public.txt", "nested")
				runGitTest(t, origin, "add", ".")
				runGitTest(t, origin, "commit", "-m", "nested baseline")
				runGitTest(t, origin, "worktree", "add", "--detach", nested, "HEAD")
			}
			for _, operation := range []string{"build", "snapshot", "preview"} {
				t.Run(operation, func(t *testing.T) {
					var err error
					switch operation {
					case "build":
						var archive bytes.Buffer
						_, err = BuildWithOptions(context.Background(), repo, &archive, BuildOptions{})
						if archive.Len() != 0 {
							t.Fatal("unsupported repository produced an archive")
						}
					case "snapshot":
						_, err = SnapshotWithOptions(context.Background(), repo, BuildOptions{})
					case "preview":
						_, err = ChangedFiles(context.Background(), repo, BuildOptions{})
					}
					if !errors.Is(err, ErrUnsupportedRepository) || !strings.Contains(err.Error(), "untracked directory") {
						t.Fatalf("error=%v, want explicit untracked-directory rejection", err)
					}
				})
			}
			include := false
			for name, options := range map[string]BuildOptions{
				"tracked-only": {IncludeUntracked: &include},
				"excluded":     {ExcludePatterns: []string{"nested"}},
				"staged":       {ChangeMode: syncbundle.ChangeModeStaged},
			} {
				t.Run(name, func(t *testing.T) {
					var archive bytes.Buffer
					built, err := BuildWithOptions(context.Background(), repo, &archive, options)
					if err != nil {
						t.Fatal(err)
					}
					if len(built.UntrackedFiles) != 0 {
						t.Fatalf("nested repository reported as included: %v", built.UntrackedFiles)
					}
				})
			}
			writeFile(t, repo, ".gitignore", "nested/\n")
			if _, err := SnapshotWithOptions(context.Background(), repo, BuildOptions{}); err != nil {
				t.Fatalf("Git-ignored nested repository: %v", err)
			}
		})
	}
}

func TestFormerTrackedPathCannotAuthorizeIgnoredWorkingBytes(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)
	writeFile(t, repo, "config.private", "public baseline")
	writeFile(t, repo, ".gitignore", "*.private\n")
	runGitTest(t, repo, "add", "-f", "config.private", ".gitignore")
	runGitTest(t, repo, "commit", "-m", "baseline")
	runGitTest(t, repo, "rm", "--cached", "config.private")
	writeFile(t, repo, "config.private", "dummy private replacement")
	for _, include := range []bool{false, true} {
		options := BuildOptions{IncludeUntracked: &include}
		var archive bytes.Buffer
		built, err := BuildWithOptions(context.Background(), repo, &archive, options)
		if err != nil {
			t.Fatal(err)
		}
		_, files, err := syncbundle.ReadBundleContents(bytes.NewReader(archive.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := files["workspace/config.private"]; exists {
			t.Fatal("historical tracking authorized new ignored bytes")
		}
		if !reflect.DeepEqual(built.Manifest.Deleted, []string{"config.private"}) {
			t.Fatalf("deletions=%v", built.Manifest.Deleted)
		}
	}
}

func TestWorkspaceSelectionRejectsUntrackedDirectoryReplacement(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, "selected.txt", "selected untracked file")
	root, err := os.OpenRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := os.Remove(filepath.Join(repo, "selected.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(repo, "selected.txt"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := workspaceEntry(root, "selected.txt", 0, retainSourceContent); !errors.Is(err, ErrUnsupportedRepository) {
		t.Fatalf("selected untracked file replaced with a directory: %v", err)
	}
}

func TestWorkingSnapshotUsesGitModesAndDetectsHiddenExecutableChanges(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)
	writeFile(t, repo, "tracked.txt", "base")
	writeFile(t, repo, "script.sh", "echo ok")
	if err := os.Chmod(filepath.Join(repo, "script.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "baseline")
	before, err := SnapshotWithOptions(context.Background(), repo, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for name, mode := range map[string]os.FileMode{"tracked.txt": 0o664, "script.sh": 0o775} {
		if err := os.Chmod(filepath.Join(repo, name), mode); err != nil {
			t.Fatal(err)
		}
	}
	groupWritable, err := SnapshotWithOptions(context.Background(), repo, BuildOptions{})
	if err != nil || before.SnapshotDigest != groupWritable.SnapshotDigest || len(groupWritable.ChangedFiles) != 0 {
		t.Fatalf("local umask permissions changed the Git snapshot: %+v err=%v", groupWritable, err)
	}
	runGitTest(t, repo, "config", "core.fileMode", "false")
	if err := os.Chmod(filepath.Join(repo, "tracked.txt"), 0o755); err != nil {
		t.Fatal(err)
	}
	after, err := SnapshotWithOptions(context.Background(), repo, BuildOptions{})
	if err != nil || before.SnapshotDigest == after.SnapshotDigest || !reflect.DeepEqual(after.ChangedFiles, []string{"tracked.txt"}) {
		t.Fatalf("Git's fileMode hint hid an executable change: %+v err=%v", after, err)
	}
}

func TestWorkingSnapshotIgnoresNonOwnerExecuteBits(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)
	writeFile(t, repo, "tracked.txt", "base")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "baseline")
	before, err := SnapshotWithOptions(context.Background(), repo, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(repo, "tracked.txt"), 0o654); err != nil {
		t.Fatal(err)
	}
	if status := strings.TrimSpace(runGitTest(t, repo, "status", "--porcelain")); status != "" {
		t.Skip("filesystem does not preserve Git permission semantics")
	}
	after, err := SnapshotWithOptions(context.Background(), repo, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if before.SnapshotDigest != after.SnapshotDigest || len(after.ChangedFiles) != 0 {
		t.Fatalf("group execute bit produced a phantom workspace change: %v", after.ChangedFiles)
	}
}

func TestWorkspaceReadRejectsReplacementSymlink(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "tracked.txt", "public")
	source := filepath.Join(root, "tracked.txt")
	info, err := os.Lstat(source)
	if err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(t.TempDir(), "private.txt")
	if err := os.WriteFile(private, []byte("dummy private bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(private, source); err != nil {
		t.Skip(err)
	}
	parent, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	entry, _, err := entryFromInfo("tracked.txt", parent, "tracked.txt", info, 0, retainSourceContent)
	if err == nil {
		t.Fatalf("replacement symlink was read as a regular file: %q", entry.data)
	}
}

func TestWorkspaceFileModesAcrossFilesystems(t *testing.T) {
	for _, tc := range []struct {
		mode      os.FileMode
		indexMode int64
		windows   bool
		want      int64
	}{
		{0o644, 0, false, 0o644}, {0o654, 0, false, 0o644}, {0o645, 0, false, 0o644}, {0o744, 0, false, 0o755},
		{0o666, 0o755, true, 0o755}, {0o666, 0o644, true, 0o644}, {0o666, 0, true, 0o644},
	} {
		if got := workspaceFileMode(tc.mode, tc.indexMode, tc.windows); got != tc.want {
			t.Fatalf("mode %o, index %o, windows %v = %o, want %o", tc.mode, tc.indexMode, tc.windows, got, tc.want)
		}
	}
}

func TestWorkspaceReadRejectsChangedRegularFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "tracked.txt", "public")
	parent, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	info, err := parent.Lstat("tracked.txt")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, "replacement.txt", "dummy replacement")
	if err := os.Rename(filepath.Join(root, "replacement.txt"), filepath.Join(root, "tracked.txt")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := entryFromInfo("tracked.txt", parent, "tracked.txt", info, 0, retainSourceContent); err == nil {
		t.Fatal("replacement file identity was accepted")
	}
}

func TestWorkspaceReadPinsParentAndRejectsSymlinkParent(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "dir/tracked.txt", "public")
	parentRoot, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer parentRoot.Close()
	parent, name, closeParent, err := workspaceParent(parentRoot, "dir/tracked.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer closeParent()
	info, err := parent.Lstat(name)
	if err != nil {
		t.Fatal(err)
	}
	private := t.TempDir()
	writeFile(t, private, "tracked.txt", "dummy private bytes")
	if err := os.Rename(filepath.Join(root, "dir"), filepath.Join(root, "old-dir")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(private, filepath.Join(root, "dir")); err != nil {
		t.Skip(err)
	}
	entry, _, err := entryFromInfo("dir/tracked.txt", parent, name, info, 0, retainSourceContent)
	if err != nil || string(entry.data) != "public" {
		t.Fatal("pinned parent read replacement bytes")
	}
	if _, _, _, err := workspaceParent(parentRoot, "dir/tracked.txt"); err == nil {
		t.Fatal("parent symlink was followed")
	}
}

func TestSourceSelectionRejectsSpecialFiles(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	writeFile(t, root, "tracked.txt", "public")
	runGitTest(t, root, "add", ".")
	runGitTest(t, root, "commit", "-m", "baseline")
	path := filepath.Join(root, "tracked.txt")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Skip(err)
	}
	defer listener.Close()
	if _, err := SnapshotWithOptions(context.Background(), root, BuildOptions{}); err == nil || !strings.Contains(err.Error(), "not a regular file or symlink") {
		t.Fatalf("special file was silently omitted: %v", err)
	}
}

func TestWorkingSnapshotHandlesDirectoryReplacedByFile(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)
	writeFile(t, repo, "dir/tracked.txt", "base")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "baseline")
	if err := os.RemoveAll(filepath.Join(repo, "dir")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, "dir", "replacement")
	for _, include := range []bool{false, true} {
		var archive bytes.Buffer
		built, err := BuildWithOptions(context.Background(), repo, &archive, BuildOptions{IncludeUntracked: &include})
		if err != nil {
			t.Fatalf("directory replacement, include=%v: %v", include, err)
		}
		if !reflect.DeepEqual(built.Manifest.Deleted, []string{"dir/tracked.txt"}) {
			t.Fatalf("deletions = %v", built.Manifest.Deleted)
		}
		target := t.TempDir()
		if _, err := extractBundleForTest(context.Background(), bytes.NewReader(archive.Bytes()), target); err != nil {
			t.Fatal(err)
		}
		if include {
			assertFile(t, target, "dir", "replacement")
		} else {
			_, files, err := syncbundle.ReadBundleContents(bytes.NewReader(archive.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			if _, exists := files["workspace/dir"]; exists {
				t.Fatal("untracked replacement was uploaded")
			}
			assertNotExists(t, target, "dir/tracked.txt")
		}
	}
}
