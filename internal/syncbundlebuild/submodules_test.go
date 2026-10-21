package syncbundlebuild

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func pinnedSubmoduleFixture(t *testing.T) (string, string) {
	t.Helper()
	child := t.TempDir()
	initRepo(t, child)
	writeFile(t, child, "library.txt", "pinned dependency\n")
	runGitTest(t, child, "add", ".")
	runGitTest(t, child, "commit", "-m", "dependency")
	parent := t.TempDir()
	initRepo(t, parent)
	writeFile(t, parent, "main.txt", "parent\n")
	runGitTest(t, parent, "-c", "protocol.file.allow=always", "submodule", "add", child, "vendor/library")
	runGitTest(t, parent, "add", ".")
	runGitTest(t, parent, "commit", "-m", "pinned dependency")
	return parent, filepath.Join(parent, "vendor", "library")
}

func TestInitializedPinnedSubmoduleBuildsTrackedContents(t *testing.T) {
	parent, child := pinnedSubmoduleFixture(t)
	var archive bytes.Buffer
	result, err := BuildWithOptions(context.Background(), parent, &archive, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	manifest, files, err := syncbundle.ReadBundleContents(bytes.NewReader(archive.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(files["baseline/vendor/library/library.txt"].Data); got != "pinned dependency\n" {
		t.Fatalf("submodule content = %q", got)
	}
	if manifest.SnapshotDigest != result.Manifest.SnapshotDigest {
		t.Fatal("admitted source differs from selected snapshot")
	}
	want := []syncbundle.Submodule{{Path: "vendor/library", Commit: strings.TrimSpace(runGitTest(t, child, "rev-parse", "HEAD"))}}
	if !reflect.DeepEqual(manifest.Submodules, want) {
		t.Fatalf("pins=%+v want=%+v", manifest.Submodules, want)
	}
	for name := range files {
		if strings.Contains(name, "/.git/") || strings.HasSuffix(name, "/.git") {
			t.Fatalf("Git control metadata was uploaded: %s", name)
		}
	}
}

func TestPinnedSubmodulePreviewSnapshotAndChangeModesAgree(t *testing.T) {
	parent, _ := pinnedSubmoduleFixture(t)
	writeFile(t, parent, "main.txt", "parent edit\n")
	runGitTest(t, parent, "add", "main.txt")
	include := false
	for _, mode := range []string{syncbundle.ChangeModeWorking, syncbundle.ChangeModeStaged, syncbundle.ChangeModeAll} {
		t.Run(mode, func(t *testing.T) {
			options := BuildOptions{ChangeMode: mode, IncludeUntracked: &include}
			var archive bytes.Buffer
			built, err := BuildWithOptions(context.Background(), parent, &archive, options)
			if err != nil {
				t.Fatal(err)
			}
			preview, err := ChangedFiles(context.Background(), parent, options)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := SnapshotWithOptions(context.Background(), parent, options)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(preview.ChangedFiles, []string{"main.txt"}) || !reflect.DeepEqual(built.Manifest.ChangedFiles, preview.ChangedFiles) || snapshot.SnapshotDigest != built.Manifest.SnapshotDigest {
				t.Fatalf("preview=%+v built=%+v snapshot=%+v", preview, built.Manifest, snapshot)
			}
			filter, err := NewPathFilter(context.Background(), parent, options)
			if err != nil {
				t.Fatal(err)
			}
			if included, err := filter.Includes("vendor/library/library.txt"); err != nil || !included {
				t.Fatalf("tracked submodule path filter: included=%v err=%v", included, err)
			}
		})
	}
}

func TestPinnedSubmoduleRejectsIncompleteOrModifiedSource(t *testing.T) {
	for name, mutate := range map[string]func(*testing.T, string, string){
		"uninitialized": func(t *testing.T, parent, child string) {
			runGitTest(t, parent, "submodule", "deinit", "-f", "--", "vendor/library")
		},
		"modified": func(t *testing.T, parent, child string) { writeFile(t, child, "library.txt", "local edit\n") },
		"assume unchanged": func(t *testing.T, parent, child string) {
			runGitTest(t, child, "update-index", "--assume-unchanged", "library.txt")
			writeFile(t, child, "library.txt", "hidden local edit\n")
		},
		"staged": func(t *testing.T, parent, child string) {
			writeFile(t, child, "library.txt", "staged edit\n")
			runGitTest(t, child, "add", "library.txt")
		},
		"untracked": func(t *testing.T, parent, child string) { writeFile(t, child, "extra.txt", "untracked source\n") },
		"sparse": func(t *testing.T, parent, child string) {
			runGitTest(t, child, "update-index", "--skip-worktree", "library.txt")
		},
		"mode hidden by git": func(t *testing.T, parent, child string) {
			runGitTest(t, child, "config", "core.fileMode", "false")
			if err := os.Chmod(filepath.Join(child, "library.txt"), 0o755); err != nil {
				t.Fatal(err)
			}
		},
		"different HEAD": func(t *testing.T, parent, child string) {
			runGitTest(t, child, "config", "user.name", "Test")
			runGitTest(t, child, "config", "user.email", "test@example.invalid")
			runGitTest(t, child, "commit", "--allow-empty", "-m", "same tree, different pin")
		},
		"changed gitlink": func(t *testing.T, parent, child string) {
			head := strings.TrimSpace(runGitTest(t, parent, "rev-parse", "HEAD"))
			runGitTest(t, parent, "update-index", "--cacheinfo", "160000,"+head+",vendor/library")
		},
		"symlink directory": func(t *testing.T, parent, child string) {
			other := t.TempDir()
			if err := os.Rename(child, filepath.Join(other, "child")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(other, "child"), child); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			parent, child := pinnedSubmoduleFixture(t)
			mutate(t, parent, child)
			for _, operation := range []string{"build", "preview", "snapshot"} {
				var archive bytes.Buffer
				var err error
				switch operation {
				case "build":
					_, err = BuildWithOptions(context.Background(), parent, &archive, BuildOptions{})
				case "preview":
					_, err = ChangedFiles(context.Background(), parent, BuildOptions{})
				case "snapshot":
					_, err = SnapshotWithOptions(context.Background(), parent, BuildOptions{})
				}
				if !errors.Is(err, ErrUnsupportedRepository) || !strings.Contains(err.Error(), "submodule") || archive.Len() != 0 {
					t.Fatalf("%s accepted incomplete module: archive=%d error=%v", operation, archive.Len(), err)
				}
			}
		})
	}
}

func TestRecursiveSubmodulesRetainPinsAndTrackedGeneratedFiles(t *testing.T) {
	parent, child := pinnedSubmoduleFixture(t)
	leaf := t.TempDir()
	initRepo(t, leaf)
	writeFile(t, leaf, "target/fixture.txt", "required tracked fixture\n")
	runGitTest(t, leaf, "add", ".")
	runGitTest(t, leaf, "commit", "-m", "nested dependency")
	runGitTest(t, child, "config", "user.name", "Test")
	runGitTest(t, child, "config", "user.email", "test@example.invalid")
	runGitTest(t, child, "-c", "protocol.file.allow=always", "submodule", "add", leaf, "nested")
	runGitTest(t, child, "add", ".")
	runGitTest(t, child, "commit", "-m", "recursive dependency")
	runGitTest(t, parent, "add", "vendor/library")
	runGitTest(t, parent, "commit", "-m", "pin recursive dependency")
	var archive bytes.Buffer
	built, err := BuildWithOptions(context.Background(), parent, &archive, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(built.Manifest.Submodules) != 2 || built.Manifest.Submodules[1].Path != "vendor/library/nested" {
		t.Fatalf("recursive provenance=%+v", built.Manifest.Submodules)
	}
	_, files, err := syncbundle.ReadBundleContents(bytes.NewReader(archive.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(files["baseline/vendor/library/nested/target/fixture.txt"].Data); got != "required tracked fixture\n" {
		t.Fatalf("tracked generated-looking fixture=%q", got)
	}
	filter, err := NewPathFilter(context.Background(), parent, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if included, err := filter.Includes("vendor/library/nested/target/fixture.txt"); err != nil || !included {
		t.Fatalf("recursive tracked path filter=%v error=%v", included, err)
	}
}

func TestChangingPinnedCommitWithIdenticalFilesChangesSourceIdentity(t *testing.T) {
	parent, child := pinnedSubmoduleFixture(t)
	before, err := SnapshotWithOptions(context.Background(), parent, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	runGitTest(t, child, "config", "user.name", "Test")
	runGitTest(t, child, "config", "user.email", "test@example.invalid")
	runGitTest(t, child, "commit", "--allow-empty", "-m", "new identity, same files")
	runGitTest(t, parent, "add", "vendor/library")
	runGitTest(t, parent, "commit", "-m", "new dependency pin")
	after, err := SnapshotWithOptions(context.Background(), parent, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if before.SnapshotDigest == after.SnapshotDigest || before.BaselineKey == after.BaselineKey {
		t.Fatal("different pins with identical trees shared source identity")
	}
}
