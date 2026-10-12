//go:build !unix

package syncbundlebuild

import "os"

func openWorkspaceFile(parent *os.Root, name string) (*os.File, error) {
	// Root contains traversal; entryFromInfo verifies identity before reading.
	return parent.Open(name)
}
