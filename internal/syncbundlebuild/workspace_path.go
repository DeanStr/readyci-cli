package syncbundlebuild

import (
	"fmt"
	"os"
	"strings"
	"syscall"
)

// Pin each parent directory and reject symlink traversal. A pathname check
// followed by an ordinary open can otherwise read an ineligible replacement.
func workspaceParent(root *os.Root, rel string) (*os.Root, string, func(), error) {
	parts := strings.Split(rel, "/")
	current := root
	closeCurrent := func() {
		if current != root {
			_ = current.Close()
		}
	}
	for _, part := range parts[:len(parts)-1] {
		expected, err := current.Lstat(part)
		if err != nil {
			closeCurrent()
			return nil, "", func() {}, err
		}
		if !expected.IsDir() {
			closeCurrent()
			if expected.Mode()&os.ModeSymlink == 0 {
				return nil, "", func() {}, &os.PathError{Op: "source parent", Path: rel, Err: syscall.ENOTDIR}
			}
			return nil, "", func() {}, fmt.Errorf("workspace parent of %q changed during source selection", rel)
		}
		next, err := current.OpenRoot(part)
		if err != nil {
			closeCurrent()
			return nil, "", func() {}, err
		}
		actual, err := next.Stat(".")
		if err != nil || !os.SameFile(expected, actual) {
			_ = next.Close()
			closeCurrent()
			return nil, "", func() {}, fmt.Errorf("workspace parent of %q changed during source selection", rel)
		}
		closeCurrent()
		current = next
	}
	return current, parts[len(parts)-1], closeCurrent, nil
}
