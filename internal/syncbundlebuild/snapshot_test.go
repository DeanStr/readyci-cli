package syncbundlebuild

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSnapshotMatchesUploadedBundleForSourceModes(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)
	writeFile(t, repo, "tracked.txt", "base")
	writeFile(t, repo, "deleted.txt", "remove")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "baseline")
	writeFile(t, repo, "tracked.txt", "staged")
	runGitTest(t, repo, "add", "tracked.txt")
	writeFile(t, repo, "tracked.txt", "working")
	writeFile(t, repo, "untracked.txt", "new")
	writeFile(t, repo, "excluded.txt", "ignored")
	if err := os.Remove(filepath.Join(repo, "deleted.txt")); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"working", "staged", "all"} {
		for _, omit := range []bool{false, true} {
			options := BuildOptions{ChangeMode: mode, OmitBaseline: omit, ExcludePatterns: []string{"excluded.txt"}}
			var archive bytes.Buffer
			built, err := BuildWithOptions(context.Background(), repo, &archive, options)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := SnapshotWithOptions(context.Background(), repo, options)
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.SnapshotDigest != built.Manifest.SnapshotDigest {
				t.Fatalf("%s delta=%t: %s != %s", mode, omit, snapshot.SnapshotDigest, built.Manifest.SnapshotDigest)
			}
			preview, err := ChangedFiles(context.Background(), repo, options)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(preview.ChangedFiles, built.Manifest.ChangedFiles) || !reflect.DeepEqual(preview.UntrackedFiles, built.UntrackedFiles) {
				t.Fatalf("%s delta=%t: preview differs from packaged source: %+v", mode, omit, preview)
			}
		}
	}
}

func TestChangedFilesMatchesBundleWithSharedSymlinkBlobs(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)
	writeFile(t, repo, "target.txt", "base")
	writeFile(t, repo, "shared-blob.txt", "target.txt")
	writeFile(t, repo, "empty.txt", "")
	if err := os.Symlink("target.txt", filepath.Join(repo, "link")); err != nil {
		t.Skip(err)
	}
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "baseline")
	writeFile(t, repo, "target.txt", "staged")
	runGitTest(t, repo, "add", "target.txt")
	writeFile(t, repo, "target.txt", "working")
	for _, mode := range []string{"working", "staged", "all"} {
		options := BuildOptions{ChangeMode: mode}
		var archive bytes.Buffer
		built, err := BuildWithOptions(context.Background(), repo, &archive, options)
		if err != nil {
			t.Fatal(err)
		}
		preview, err := ChangedFiles(context.Background(), repo, options)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(preview.ChangedFiles, []string{"target.txt"}) || !reflect.DeepEqual(preview.ChangedFiles, built.Manifest.ChangedFiles) {
			t.Fatalf("%s: preview=%v, bundle=%v", mode, preview.ChangedFiles, built.Manifest.ChangedFiles)
		}
	}
}
