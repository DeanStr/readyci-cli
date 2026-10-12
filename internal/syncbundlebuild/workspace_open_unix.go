//go:build unix

package syncbundlebuild

import (
	"golang.org/x/sys/unix"
	"os"
)

func openWorkspaceFile(parent *os.Root, name string) (*os.File, error) {
	// A replaced symlink must never be followed, and a replacement FIFO must not
	// block before we can verify the opened file's type and identity.
	return parent.OpenFile(name, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
}
