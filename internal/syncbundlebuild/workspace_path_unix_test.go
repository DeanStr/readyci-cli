//go:build unix

package syncbundlebuild

import (
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWorkspaceReadRejectsReplacementFIFOWithoutBlocking(t *testing.T) {
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
	source := filepath.Join(root, "tracked.txt")
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(source, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := entryFromInfo("tracked.txt", parent, "tracked.txt", info, 0, retainSourceContent)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("replacement FIFO was accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("source reading blocked on replacement FIFO")
	}
}
