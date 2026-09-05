package syncbundlebuild

import (
	"context"
	"fmt"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func planStagedWorkspace(ctx context.Context, root, head string, matcher ignoreMatcher) ([]plannedEntry, error) {
	var paths []string
	if head == "" {
		out, err := gitOutput(ctx, root, "ls-files", "-z", "--cached")
		if err != nil {
			return nil, err
		}
		paths = splitNUL(out)
	} else {
		out, err := gitOutput(ctx, root, "diff", "--cached", "--name-only", "--diff-filter=ACMRT", "-z", head, "--")
		if err != nil {
			return nil, err
		}
		paths = splitNUL(out)
	}
	return planIndexPaths(ctx, root, paths, matcher)
}

func planIndexPaths(ctx context.Context, root string, paths []string, matcher ignoreMatcher) ([]plannedEntry, error) {
	seenPaths := make(map[string]struct{})
	filtered := make([]string, 0, len(paths))
	for _, raw := range paths {
		rel, err := syncbundle.ValidateRelativePath(raw)
		if err != nil {
			return nil, err
		}
		if matcher.ignored(rel) {
			continue
		}
		if syncbundle.IsGitControlPath(rel) {
			return nil, fmt.Errorf("staged path %q targets git control metadata", rel)
		}
		if _, ok := seenPaths[rel]; ok {
			continue
		}
		seenPaths[rel] = struct{}{}
		filtered = append(filtered, rel)
	}
	if len(filtered) == 0 {
		return nil, nil
	}

	wanted := make(map[string]bool, len(filtered))
	for _, rel := range filtered {
		wanted[rel] = true
	}
	out, err := gitOutput(ctx, root, "ls-files", "-s", "-z", "--cached")
	if err != nil {
		return nil, err
	}
	type indexBlob struct {
		mode string
		oid  string
		rel  string
	}
	seenEntries := make(map[string]struct{})
	var blobs []indexBlob
	for _, record := range splitNUL(out) {
		mode, oid, stage, rel, err := parseIndexRecord(record)
		if err != nil {
			return nil, err
		}
		if stage != "0" {
			return nil, fmt.Errorf("staged path %q has unresolved merge stage %s", rel, stage)
		}
		if mode != "100644" && mode != "100755" && mode != "120000" {
			continue
		}
		rel, err = syncbundle.ValidateRelativePath(rel)
		if err != nil {
			return nil, err
		}
		if syncbundle.IsGitControlPath(rel) {
			return nil, fmt.Errorf("staged path %q targets git control metadata", rel)
		}
		if !wanted[rel] {
			continue
		}
		if _, seen := seenEntries[rel]; seen {
			continue
		}
		seenEntries[rel] = struct{}{}
		blobs = append(blobs, indexBlob{mode: mode, oid: oid, rel: rel})
	}
	objectIDs := make([]string, 0, len(blobs))
	for _, blob := range blobs {
		objectIDs = append(objectIDs, blob.oid)
	}
	objects, err := gitObjects(ctx, root, objectIDs)
	if err != nil {
		return nil, err
	}
	entries := make([]plannedEntry, 0, len(blobs))
	for index, blob := range blobs {
		data := objects[index]
		entry := syncbundle.Entry{Path: blob.rel, Mode: gitModeToFileMode(blob.mode)}
		if blob.mode == "120000" {
			if err := syncbundle.ValidateSymlinkTarget(string(data)); err != nil {
				return nil, fmt.Errorf("staged symlink %q has invalid target: %w", blob.rel, err)
			}
			entry.Type = syncbundle.EntryTypeSymlink
			entry.Linkname = string(data)
		} else {
			entry.Type = syncbundle.EntryTypeFile
			entry.Size = int64(len(data))
		}
		entries = append(entries, plannedEntry{entry: entry, data: data})
	}
	sortPlanned(entries)
	return entries, nil
}

func parseIndexRecord(record string) (mode string, oid string, stage string, rel string, err error) {
	meta, rel, ok := strings.Cut(record, "\t")
	if !ok {
		return "", "", "", "", fmt.Errorf("parse git index record %q", record)
	}
	fields := strings.Fields(meta)
	if len(fields) < 3 {
		return "", "", "", "", fmt.Errorf("parse git index metadata %q", meta)
	}
	return fields[0], fields[1], fields[2], rel, nil
}
