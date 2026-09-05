package syncbundlebuild

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func planBundle(ctx context.Context, cwd string, options BuildOptions) (plannedBundle, error) {
	changeMode, err := syncbundle.NormalizeChangeMode(options.ChangeMode)
	if err != nil {
		return plannedBundle{}, err
	}
	root, err := repositoryRoot(ctx, cwd)
	if err != nil {
		return plannedBundle{}, err
	}
	useProjectIgnore := true
	if options.UseProjectIgnore != nil {
		useProjectIgnore = *options.UseProjectIgnore
	}
	var ignorePatterns []string
	if useProjectIgnore {
		ignorePatterns, err = readProjectIgnore(root)
		if err != nil {
			return plannedBundle{}, err
		}
	}
	ignorePatterns = append(ignorePatterns, options.ExcludePatterns...)
	defaultExcludePatterns := syncbundle.DefaultSourceExcludePatterns()
	if strings.TrimSpace(options.SourcePolicyDigest) != "" {
		defaultExcludePatterns = append([]string(nil), options.DefaultExcludePatterns...)
	}
	matcher := newIgnoreMatcherWithDefaults(ignorePatterns, defaultExcludePatterns)
	includeUntracked := true
	if options.IncludeUntracked != nil {
		includeUntracked = *options.IncludeUntracked
	}
	if changeMode == syncbundle.ChangeModeStaged {
		includeUntracked = false
	}
	untrackedFiles, err := planIncludedUntrackedFiles(ctx, root, matcher, includeUntracked, changeMode)
	if err != nil {
		return plannedBundle{}, err
	}

	head := gitHead(ctx, root)
	manifest := syncbundle.Manifest{
		Version:    syncbundle.FormatVersion,
		CreatedAt:  time.Unix(0, 0).UTC(),
		ChangeMode: changeMode,
		Git: syncbundle.GitMetadata{
			Branch: gitBranch(ctx, root),
			Head:   head,
			Dirty:  gitStatusDirty(ctx, root),
		},
	}
	if strings.TrimSpace(options.SourcePolicyDigest) != "" {
		binding, bindingErr := syncbundle.NormalizeSourcePolicyBinding(syncbundle.SourcePolicyBinding{
			Digest:          options.SourcePolicyDigest,
			ExcludePatterns: append([]string(nil), options.ExcludePatterns...),
			DefaultExcludePatterns: append(
				[]string(nil), options.DefaultExcludePatterns...,
			),
			IncludeUntracked:  includeUntracked,
			ProjectIgnoreUsed: useProjectIgnore,
		})
		if bindingErr != nil {
			return plannedBundle{}, bindingErr
		}
		manifest.SourcePolicy = &binding
	}

	baseline, err := planBaseline(ctx, root, head, matcher)
	if err != nil {
		return plannedBundle{}, err
	}
	baselineKey, err := baselineContentKey(baseline)
	if err != nil {
		return plannedBundle{}, err
	}
	manifest.BaselineKey = baselineKey
	deleted, err := planDeleted(ctx, root, head, baseline, matcher, changeMode)
	if err != nil {
		return plannedBundle{}, err
	}
	workspace, err := planWorkspace(ctx, root, head, baseline, matcher, includeUntracked, changeMode)
	if err != nil {
		return plannedBundle{}, err
	}
	changedFiles, err := syncbundle.WorkspaceChangedFiles(
		plannedArchiveEntries(baseline),
		plannedArchiveEntries(workspace),
		deleted,
	)
	if err != nil {
		return plannedBundle{}, err
	}

	for _, item := range baseline {
		manifest.Baseline = append(manifest.Baseline, item.entry)
	}
	for _, item := range workspace {
		manifest.Workspace = append(manifest.Workspace, item.entry)
	}
	manifest.Deleted = deleted
	manifest.ChangedFiles = changedFiles
	snapshotDigest, err := syncbundle.WorkspaceContentDigest(plannedArchiveEntries(baseline), plannedArchiveEntries(workspace), deleted)
	if err != nil {
		return plannedBundle{}, err
	}
	manifest.SnapshotDigest = snapshotDigest

	return plannedBundle{
		root:             root,
		manifest:         manifest,
		baseline:         baseline,
		workspace:        workspace,
		includeUntracked: includeUntracked,
		untrackedFiles:   untrackedFiles,
	}, nil
}

func readProjectIgnore(root string) ([]string, error) {
	for _, name := range []string{".readyciignore"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err == nil {
			return strings.Split(string(data), "\n"), nil
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
	}
	return nil, nil
}

func planIncludedUntrackedFiles(ctx context.Context, root string, matcher ignoreMatcher, includeUntracked bool, changeMode string) ([]string, error) {
	if !includeUntracked || changeMode == syncbundle.ChangeModeStaged {
		return nil, nil
	}
	out, err := gitOutput(ctx, root, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	var paths []string
	seen := make(map[string]struct{})
	for _, raw := range splitNUL(out) {
		rel, err := syncbundle.ValidateRelativePath(raw)
		if err != nil {
			return nil, err
		}
		if matcher.ignored(rel) {
			continue
		}
		if ancestor, ok, err := symlinkAncestor(root, rel); err != nil {
			return nil, err
		} else if ok && matcher.ignored(ancestor) {
			continue
		}
		if _, ok := seen[rel]; ok {
			continue
		}
		seen[rel] = struct{}{}
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	return paths, nil
}

func planBaseline(ctx context.Context, root, head string, matcher ignoreMatcher) ([]plannedEntry, error) {
	if head == "" {
		return nil, nil
	}
	out, err := gitOutput(ctx, root, "ls-tree", "-rz", "--full-tree", "-r", head)
	if err != nil {
		return nil, err
	}

	type treeEntry struct {
		mode string
		oid  string
		rel  string
	}
	var treeEntries []treeEntry
	for _, record := range splitNUL(out) {
		meta, rel, ok := strings.Cut(record, "\t")
		if !ok {
			return nil, fmt.Errorf("parse git tree record %q", record)
		}
		fields := strings.Fields(meta)
		if len(fields) < 3 {
			return nil, fmt.Errorf("parse git tree metadata %q", meta)
		}
		mode := fields[0]
		if mode != "100644" && mode != "100755" && mode != "120000" {
			continue
		}
		rel, err := syncbundle.ValidateRelativePath(rel)
		if err != nil {
			return nil, err
		}
		if matcher.ignored(rel) {
			continue
		}
		treeEntries = append(treeEntries, treeEntry{mode: mode, oid: fields[2], rel: rel})
	}
	objectIDs := make([]string, 0, len(treeEntries))
	for _, item := range treeEntries {
		objectIDs = append(objectIDs, item.oid)
	}
	objects, err := gitObjects(ctx, root, objectIDs)
	if err != nil {
		return nil, err
	}

	entries := make([]plannedEntry, 0, len(treeEntries))
	for index, item := range treeEntries {
		data := objects[index]
		mode := item.mode
		rel := item.rel
		entry := syncbundle.Entry{Path: rel, Mode: gitModeToFileMode(mode)}
		if mode == "120000" {
			if err := syncbundle.ValidateSymlinkTarget(string(data)); err != nil {
				return nil, fmt.Errorf("git symlink %q has invalid target: %w", rel, err)
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

func planWorkspace(ctx context.Context, root, head string, baseline []plannedEntry, matcher ignoreMatcher, includeUntracked bool, changeMode string) ([]plannedEntry, error) {
	if changeMode == syncbundle.ChangeModeStaged {
		return planStagedWorkspace(ctx, root, head, matcher)
	}
	var paths []string
	if head == "" {
		args := []string{"ls-files", "-z", "--cached"}
		if includeUntracked {
			args = append(args, "--others", "--exclude-standard")
		}
		out, err := gitOutput(ctx, root, args...)
		if err != nil {
			return nil, err
		}
		paths = splitNUL(out)
	} else {
		out, err := gitOutput(ctx, root, "diff", "-z", "--name-only", head, "--")
		if err != nil {
			return nil, err
		}
		paths = append(paths, splitNUL(out)...)
		if includeUntracked {
			out, err = gitOutput(ctx, root, "ls-files", "-z", "--others", "--exclude-standard")
			if err != nil {
				return nil, err
			}
			paths = append(paths, splitNUL(out)...)
		}
	}
	for _, item := range baseline {
		source := filepath.Join(root, filepath.FromSlash(item.entry.Path))
		info, err := os.Lstat(source)
		if err == nil && info.IsDir() {
			paths = append(paths, item.entry.Path)
		}
	}

	seen := make(map[string]struct{})
	var entries []plannedEntry
	for _, raw := range paths {
		rel, err := syncbundle.ValidateRelativePath(raw)
		if err != nil {
			return nil, err
		}
		if matcher.ignored(rel) {
			continue
		}
		if ancestor, ok, err := symlinkAncestor(root, rel); err != nil {
			return nil, err
		} else if ok {
			if matcher.ignored(ancestor) {
				continue
			}
			if _, ok := seen[ancestor]; !ok {
				seen[ancestor] = struct{}{}
				updated, err := appendWorkspacePath(root, ancestor, matcher, seen, entries)
				if err != nil {
					return nil, err
				}
				entries = updated
			}
			seen[rel] = struct{}{}
			continue
		}
		if _, ok := seen[rel]; ok {
			continue
		}
		seen[rel] = struct{}{}
		updated, err := appendWorkspacePath(root, rel, matcher, seen, entries)
		if err != nil {
			return nil, err
		}
		entries = updated
	}
	sortPlanned(entries)
	return entries, nil
}
