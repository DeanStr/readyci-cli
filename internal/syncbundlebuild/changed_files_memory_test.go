package syncbundlebuild

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestChangedFilesBoundsLargeFileAllocations(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)
	file, err := os.Create(filepath.Join(repo, "large.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	const size = 64 << 20
	if err := file.Truncate(size); err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, "small.txt", "base")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "baseline")
	runGitTest(t, repo, "update-index", "--assume-unchanged", "large.bin")
	writeFile(t, repo, "small.txt", "edit")

	for _, edited := range []bool{false, true} {
		if edited {
			if _, err := file.WriteAt([]byte{1}, size-1); err != nil {
				t.Fatal(err)
			}
		}
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		result, err := ChangedFiles(context.Background(), repo, BuildOptions{})
		runtime.ReadMemStats(&after)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"small.txt"}
		if edited {
			want = []string{"large.bin", "small.txt"}
		}
		if !reflect.DeepEqual(result.ChangedFiles, want) {
			t.Fatalf("edited=%t: changes=%v, want %v", edited, result.ChangedFiles, want)
		}
		// Preview must read hidden edits too, without allocating file-sized
		// buffers for either the Git baseline or the working copy.
		if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 8<<20 {
			t.Fatalf("edited=%t: preview allocated %d bytes for a %d-byte file; limit is 8 MiB", edited, allocated, size)
		}
	}
}
