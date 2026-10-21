package syncbundlebuild

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"

	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

type gitSourceEntry struct {
	mode string
	oid  string
	rel  string
}

func planGitEntries(ctx context.Context, root string, blobs []gitSourceEntry, contentMode sourceContentMode) ([]plannedEntry, error) {
	objectIDs := make([]string, 0, len(blobs))
	symlinkObjects := make(map[string]bool)
	for _, blob := range blobs {
		objectIDs = append(objectIDs, blob.oid)
		if blob.mode == "120000" {
			symlinkObjects[blob.oid] = true
		}
	}
	contents := make(map[string]sourceContent, len(blobs))
	err := gitObjects(ctx, root, objectIDs, func(oid string, size int64, reader io.Reader) error {
		mode := contentMode
		if symlinkObjects[oid] {
			// Symlink targets are metadata and must still be validated in previews.
			mode = retainSourceContent
		}
		content, err := readSourceContent(reader, size, mode)
		if err == nil {
			if contentMode == hashSourceContent && mode == retainSourceContent {
				// One blob can be both a regular file and a symlink target.
				content.digest = sha256.Sum256(content.data)
			}
			contents[oid] = content
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	entries := make([]plannedEntry, 0, len(blobs))
	for _, blob := range blobs {
		content := contents[blob.oid]
		entry := syncbundle.Entry{Path: blob.rel, Mode: gitModeToFileMode(blob.mode)}
		if blob.mode == "120000" {
			if err := syncbundle.ValidateSymlinkTargetSyntax(string(content.data)); err != nil {
				return nil, fmt.Errorf("%w: git symlink %q has invalid target: %w", ErrUnsupportedRepository, blob.rel, err)
			}
			entry.Type = syncbundle.EntryTypeSymlink
			entry.Linkname = string(content.data)
		} else {
			entry.Type = syncbundle.EntryTypeFile
			entry.Size = content.size
		}
		entries = append(entries, plannedEntry{entry: entry, data: content.data, digest: content.digest})
	}
	sortPlanned(entries)
	return entries, nil
}
