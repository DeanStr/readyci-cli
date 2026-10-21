package syncbundlebuild

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func planDeleted(ctx context.Context, root, head string, baseline []plannedEntry, matcher ignoreMatcher, changeMode string, eligiblePaths []string) ([]string, error) {
	if changeMode == syncbundle.ChangeModeStaged {
		return planStagedDeleted(ctx, root, head, baseline, matcher)
	}
	var deleted []string
	eligible := make(map[string]struct{}, len(eligiblePaths))
	for _, rel := range eligiblePaths {
		eligible[rel] = struct{}{}
	}
	for _, item := range baseline {
		if _, ok := eligible[item.entry.Path]; !ok {
			deleted = append(deleted, item.entry.Path)
			continue
		}
		if _, ok, err := symlinkAncestor(root, item.entry.Path); err != nil {
			return nil, err
		} else if ok {
			deleted = append(deleted, item.entry.Path)
			continue
		}
		source := filepath.Join(root, filepath.FromSlash(item.entry.Path))
		info, err := os.Lstat(source)
		if workspacePathMissing(err) {
			deleted = append(deleted, item.entry.Path)
			continue
		}
		if err != nil {
			return nil, err
		}
		if info.IsDir() || (info.Mode()&os.ModeSymlink == 0 && !info.Mode().IsRegular()) {
			deleted = append(deleted, item.entry.Path)
		}
	}
	sort.Strings(deleted)
	return deleted, nil
}

func planStagedDeleted(ctx context.Context, root, head string, baseline []plannedEntry, matcher ignoreMatcher) ([]string, error) {
	if head == "" {
		return nil, nil
	}
	indexPaths, err := stagedIndexPaths(ctx, root, matcher)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	var deleted []string
	for _, item := range baseline {
		if indexPaths[item.entry.Path] {
			continue
		}
		if _, ok := seen[item.entry.Path]; ok {
			continue
		}
		seen[item.entry.Path] = struct{}{}
		deleted = append(deleted, item.entry.Path)
	}
	sort.Strings(deleted)
	return deleted, nil
}

func stagedIndexPaths(ctx context.Context, root string, matcher ignoreMatcher) (map[string]bool, error) {
	out, err := gitOutput(ctx, root, "ls-files", "-s", "-z", "--cached")
	if err != nil {
		return nil, err
	}
	paths := make(map[string]bool)
	for _, record := range splitNUL(out) {
		mode, _, stage, rel, err := parseIndexRecord(record)
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
		if matcher.ignoredTracked(rel) {
			continue
		}
		if syncbundle.IsGitControlPath(rel) {
			return nil, fmt.Errorf("staged path %q targets git control metadata", rel)
		}
		paths[rel] = true
	}
	return paths, nil
}

func symlinkAncestor(root, rel string) (string, bool, error) {
	parts := strings.Split(rel, "/")
	current := root
	for i, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if workspacePathMissing(err) {
				return "", false, nil
			}
			return "", false, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return strings.Join(parts[:i+1], "/"), true, nil
		}
		if !info.IsDir() {
			return "", false, nil
		}
	}
	return "", false, nil
}

func workspacePathMissing(err error) bool {
	return os.IsNotExist(err) || errors.Is(err, syscall.ENOTDIR)
}
