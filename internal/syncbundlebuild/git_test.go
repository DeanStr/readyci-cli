package syncbundlebuild

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitObjectsReadsOrderedBinaryBlobsInOneBatch(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)
	first := []byte("first\n")
	binary := []byte{0, 1, '\n', 255, 0}
	if err := os.WriteFile(filepath.Join(repo, "first.txt"), first, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "binary.bin"), binary, 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "baseline")

	firstID := strings.TrimSpace(runGitTest(t, repo, "rev-parse", "HEAD:first.txt"))
	binaryID := strings.TrimSpace(runGitTest(t, repo, "rev-parse", "HEAD:binary.bin"))
	objects, err := gitObjects(context.Background(), repo, []string{binaryID, firstID, binaryID})
	if err != nil {
		t.Fatal(err)
	}
	want := [][]byte{binary, first, binary}
	if len(objects) != len(want) {
		t.Fatalf("objects = %d, want %d", len(objects), len(want))
	}
	for index := range want {
		if !bytes.Equal(objects[index], want[index]) {
			t.Fatalf("object %d = %v, want %v", index, objects[index], want[index])
		}
	}
}

func TestPlanBaselineUsesRequestedHead(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)
	path := filepath.Join(repo, "version.txt")
	if err := os.WriteFile(path, []byte("first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, repo, "add", "version.txt")
	runGitTest(t, repo, "commit", "-m", "first")
	firstHead := strings.TrimSpace(runGitTest(t, repo, "rev-parse", "HEAD"))

	if err := os.WriteFile(path, []byte("second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, repo, "add", "version.txt")
	runGitTest(t, repo, "commit", "-m", "second")

	entries, err := planBaseline(context.Background(), repo, firstHead, newIgnoreMatcher(nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("baseline entries = %d, want 1", len(entries))
	}
	if entries[0].entry.Path != "version.txt" || string(entries[0].data) != "first\n" {
		t.Fatalf("baseline entry = path %q data %q, want version.txt from requested head", entries[0].entry.Path, entries[0].data)
	}
}
