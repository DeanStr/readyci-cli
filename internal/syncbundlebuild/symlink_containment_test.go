package syncbundlebuild

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func TestSourceModesPreserveInternalParentSymlinks(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)
	writeFile(t, repo, "target.txt", "baseline\n")
	writeFile(t, repo, "nested/placeholder", "")
	if err := os.Symlink("../target.txt", filepath.Join(repo, "nested", "baseline-link")); err != nil {
		t.Skip(err)
	}
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "baseline")
	if err := os.Symlink("../target.txt", filepath.Join(repo, "nested", "staged-link")); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, repo, "add", "nested/staged-link")
	if err := os.Symlink("../target.txt", filepath.Join(repo, "nested", "untracked-link")); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{syncbundle.ChangeModeWorking, syncbundle.ChangeModeStaged, syncbundle.ChangeModeAll} {
		t.Run(mode, func(t *testing.T) {
			options := BuildOptions{ChangeMode: mode}
			if _, err := ChangedFiles(context.Background(), repo, options); err != nil {
				t.Fatalf("preview rejected safe link: %v", err)
			}
			var bundle bytes.Buffer
			if _, err := BuildWithOptions(context.Background(), repo, &bundle, options); err != nil {
				t.Fatalf("build rejected safe link: %v", err)
			}
			if err := syncbundle.Validate(bytes.NewReader(bundle.Bytes())); err != nil {
				t.Fatalf("admission rejected safe link: %v", err)
			}
		})
	}
}

func TestUnsafeSymlinksAreUnsupportedSource(t *testing.T) {
	for _, state := range []string{"baseline-absolute", "working-absolute", "symlink-hop-escape"} {
		t.Run(state, func(t *testing.T) {
			repo := t.TempDir()
			initRepo(t, repo)
			writeFile(t, repo, "target", "data")
			if state == "baseline-absolute" {
				if err := os.Symlink("/etc/passwd", filepath.Join(repo, "link")); err != nil {
					t.Skip(err)
				}
			}
			if state == "symlink-hop-escape" {
				if err := os.Symlink(".", filepath.Join(repo, "alias")); err != nil {
					t.Skip(err)
				}
			}
			runGitTest(t, repo, "add", ".")
			runGitTest(t, repo, "commit", "-m", "baseline")
			if state != "baseline-absolute" {
				target := "/etc/passwd"
				if state == "symlink-hop-escape" {
					target = "alias/../outside"
				}
				if err := os.Symlink(target, filepath.Join(repo, "link")); err != nil {
					t.Skip(err)
				}
			}
			for _, operation := range []struct {
				name string
				run  func() error
			}{
				{"preview", func() error { _, err := ChangedFiles(context.Background(), repo, BuildOptions{}); return err }},
				{"snapshot", func() error { _, err := SnapshotWithOptions(context.Background(), repo, BuildOptions{}); return err }},
				{"bundle", func() error {
					var bundle bytes.Buffer
					_, err := Build(context.Background(), repo, &bundle)
					return err
				}},
			} {
				if err := operation.run(); !errors.Is(err, ErrUnsupportedRepository) {
					t.Fatalf("%s error = %v; want unsupported-source classification", operation.name, err)
				}
			}
		})
	}
}
