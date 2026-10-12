package syncbundlebuild

import (
	"fmt"
	"os"
	"runtime"

	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func workspaceEntry(root *os.Root, rel string, indexMode int64, contentMode sourceContentMode) (plannedEntry, bool, error) {
	parent, name, closeParent, err := workspaceParent(root, rel)
	if err != nil {
		if workspacePathMissing(err) {
			return plannedEntry{}, false, nil
		}
		return plannedEntry{}, false, err
	}
	defer closeParent()
	info, err := parent.Lstat(name)
	if err != nil {
		if workspacePathMissing(err) {
			return plannedEntry{}, false, nil
		}
		return plannedEntry{}, false, err
	}
	// A formerly tracked file becoming a directory does not authorize walking it.
	// Its eligible descendants arrive separately from Git's exact file listing.
	if info.IsDir() {
		if indexMode == 0 {
			return plannedEntry{}, false, fmt.Errorf("%w: untracked path %q became a directory during source selection", ErrUnsupportedRepository, rel)
		}
		return plannedEntry{}, false, nil
	}
	return entryFromInfo(rel, parent, name, info, indexMode, contentMode)
}

func entryFromInfo(rel string, parent *os.Root, name string, info os.FileInfo, indexMode int64, contentMode sourceContentMode) (plannedEntry, bool, error) {
	entry := syncbundle.Entry{Path: rel, Mode: int64(info.Mode().Perm())}
	if info.Mode()&os.ModeSymlink != 0 {
		// Git records symlink targets, not the host's symlink permissions.
		entry.Mode = 0o644
		linkname, err := parent.Readlink(name)
		if err != nil {
			return plannedEntry{}, false, err
		}
		if err := syncbundle.ValidateSymlinkTarget(linkname); err != nil {
			return plannedEntry{}, false, fmt.Errorf("symlink %q has invalid target: %w", rel, err)
		}
		entry.Type = syncbundle.EntryTypeSymlink
		entry.Linkname = linkname
		return plannedEntry{entry: entry}, true, nil
	}
	if !info.Mode().IsRegular() {
		return plannedEntry{}, false, fmt.Errorf("%w: workspace path %q is not a regular file or symlink", ErrUnsupportedRepository, rel)
	}

	file, err := openWorkspaceFile(parent, name)
	if err != nil {
		return plannedEntry{}, false, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return plannedEntry{}, false, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return plannedEntry{}, false, fmt.Errorf("workspace path %q changed during source selection", rel)
	}
	content, err := readSourceContent(file, opened.Size(), contentMode)
	if err != nil {
		return plannedEntry{}, false, err
	}
	after, err := file.Stat()
	if err != nil {
		return plannedEntry{}, false, err
	}
	if after.Size() != opened.Size() || after.ModTime() != opened.ModTime() || after.Mode() != opened.Mode() {
		return plannedEntry{}, false, fmt.Errorf("workspace path %q changed while reading", rel)
	}
	entry.Mode = workspaceFileMode(opened.Mode(), indexMode, runtime.GOOS == "windows")
	entry.Type = syncbundle.EntryTypeFile
	entry.Size = content.size
	return plannedEntry{entry: entry, data: content.data, digest: content.digest}, true, nil
}

func workspaceFileMode(mode os.FileMode, indexMode int64, windows bool) int64 {
	// Windows has no POSIX execute bits; preserve explicit Git index modes.
	if windows && indexMode != 0 {
		return indexMode
	}
	// Git's executable flag follows the owner execute bit, not group/other bits.
	if !windows && mode.Perm()&0o100 != 0 {
		return 0o755
	}
	return 0o644
}
