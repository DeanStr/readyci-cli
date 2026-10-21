package syncbundlebuild

import (
	"context"
	"fmt"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func planStagedWorkspace(ctx context.Context, root, head string, matcher ignoreMatcher, contentMode sourceContentMode) ([]plannedEntry, error) {
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
	return planIndexPaths(ctx, root, paths, matcher, contentMode)
}

func planIndexPaths(ctx context.Context, root string, paths []string, matcher ignoreMatcher, contentMode sourceContentMode) ([]plannedEntry, error) {
	seenPaths := make(map[string]struct{})
	filtered := make([]string, 0, len(paths))
	for _, raw := range paths {
		rel, err := syncbundle.ValidateRelativePath(raw)
		if err != nil {
			return nil, err
		}
		if matcher.ignoredTracked(rel) {
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
	seenEntries := make(map[string]struct{})
	var blobs []gitSourceEntry
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
		blobs = append(blobs, gitSourceEntry{mode: mode, oid: oid, rel: rel})
	}
	return planGitEntries(ctx, root, blobs, contentMode)
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
