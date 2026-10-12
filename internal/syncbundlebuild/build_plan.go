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
	planned, err := planSource(ctx, cwd, options, retainSourceContent)
	if err != nil {
		return plannedBundle{}, err
	}
	planned.manifest.BaselineKey, err = baselineContentKey(planned.baseline)
	if err != nil {
		return plannedBundle{}, err
	}
	planned.manifest.SnapshotDigest, err = syncbundle.WorkspaceContentDigest(
		plannedArchiveEntries(planned.baseline), plannedArchiveEntries(planned.workspace), planned.manifest.Deleted,
	)
	if err != nil {
		return plannedBundle{}, err
	}
	return planned, nil
}

// planSource owns eligibility and delta selection for both packaging and path
// previews. Hash-only previews never produce an uploadable bundle or digest.
func planSource(ctx context.Context, cwd string, options BuildOptions, contentMode sourceContentMode) (plannedBundle, error) {
	changeMode, err := syncbundle.NormalizeChangeMode(options.ChangeMode)
	if err != nil {
		return plannedBundle{}, err
	}
	root, err := repositoryRoot(ctx, cwd)
	if err != nil {
		return plannedBundle{}, err
	}
	matcher, useProjectIgnore, err := sourceIgnoreMatcher(root, options)
	if err != nil {
		return plannedBundle{}, err
	}
	includeUntracked := true
	if options.IncludeUntracked != nil {
		includeUntracked = *options.IncludeUntracked
	}
	if changeMode == syncbundle.ChangeModeStaged {
		includeUntracked = false
	}
	trackedPaths, indexModes, err := eligibleIndexPaths(ctx, root, matcher, changeMode)
	if err != nil {
		return plannedBundle{}, err
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

	baseline, err := planBaseline(ctx, root, head, matcher, contentMode)
	if err != nil {
		return plannedBundle{}, err
	}
	eligiblePaths := append(append([]string(nil), trackedPaths...), untrackedFiles...)
	deleted, err := planDeleted(ctx, root, head, baseline, matcher, changeMode, eligiblePaths)
	if err != nil {
		return plannedBundle{}, err
	}
	workspace, err := planWorkspace(ctx, root, head, baseline, matcher, trackedPaths, untrackedFiles, indexModes, changeMode, contentMode)
	if err != nil {
		return plannedBundle{}, err
	}
	changedFiles := changedSourcePaths(workspace, deleted)

	for _, item := range baseline {
		manifest.Baseline = append(manifest.Baseline, item.entry)
	}
	for _, item := range workspace {
		manifest.Workspace = append(manifest.Workspace, item.entry)
	}
	manifest.Deleted = deleted
	manifest.ChangedFiles = changedFiles
	manifest.Git.Dirty = manifest.Git.Dirty || len(changedFiles) > 0

	return plannedBundle{
		root:             root,
		manifest:         manifest,
		baseline:         baseline,
		workspace:        workspace,
		includeUntracked: includeUntracked,
		untrackedFiles:   untrackedFiles,
	}, nil
}

func changedSourcePaths(workspace []plannedEntry, deleted []string) []string {
	// The Git baseline contains file/symlink leaves, never directory entries.
	// Workspace entries have already been compared with those baseline leaves;
	// only new or changed entries and deleted leaves contribute changed paths.
	seen := make(map[string]struct{}, len(workspace)+len(deleted))
	for _, item := range workspace {
		seen[item.entry.Path] = struct{}{}
	}
	for _, rel := range deleted {
		seen[rel] = struct{}{}
	}
	changed := make([]string, 0, len(seen))
	for rel := range seen {
		changed = append(changed, rel)
	}
	sort.Strings(changed)
	return changed
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
		// Git collapses an untracked nested repository into a directory record.
		// It does not enumerate an eligible set of files inside that checkout.
		if strings.HasSuffix(raw, "/") {
			return nil, fmt.Errorf("%w: untracked directory %q is unsupported; exclude nested repositories explicitly or use a checkout without them", ErrUnsupportedRepository, rel)
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

func planBaseline(ctx context.Context, root, head string, matcher ignoreMatcher, contentMode sourceContentMode) ([]plannedEntry, error) {
	if head == "" {
		return nil, nil
	}
	out, err := gitOutput(ctx, root, "ls-tree", "-rz", "--full-tree", "-r", head)
	if err != nil {
		return nil, err
	}

	var treeEntries []gitSourceEntry
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
		rel, err := syncbundle.ValidateRelativePath(rel)
		if err != nil {
			return nil, err
		}
		if matcher.ignored(rel) {
			continue
		}
		if err := validateSourceGitMode(mode, rel); err != nil {
			return nil, err
		}
		treeEntries = append(treeEntries, gitSourceEntry{mode: mode, oid: fields[2], rel: rel})
	}
	return planGitEntries(ctx, root, treeEntries, contentMode)
}

// eligibleIndexPaths defines file eligibility independently of Git's diff and
// stat cache. Tracked paths authorize themselves, never a directory's children.
func eligibleIndexPaths(ctx context.Context, root string, matcher ignoreMatcher, changeMode string) ([]string, map[string]int64, error) {
	out, err := gitOutput(ctx, root, "ls-files", "-v", "-s", "-z", "--cached")
	if err != nil {
		return nil, nil, err
	}
	var paths []string
	indexModes := make(map[string]int64)
	for _, record := range splitNUL(out) {
		if len(record) < 3 || record[1] != ' ' {
			return nil, nil, fmt.Errorf("parse git index flags %q", record)
		}
		mode, _, stage, rel, err := parseIndexRecord(record[2:])
		if err != nil {
			return nil, nil, err
		}
		rel, err = syncbundle.ValidateRelativePath(rel)
		if err != nil {
			return nil, nil, err
		}
		if matcher.ignored(rel) {
			continue
		}
		if err := validateSourceGitMode(mode, rel); err != nil {
			return nil, nil, err
		}
		if stage != "0" {
			return nil, nil, fmt.Errorf("path %q has unresolved merge stage %s", rel, stage)
		}
		if changeMode != syncbundle.ChangeModeStaged && (record[0] == 'S' || record[0] == 's') {
			return nil, nil, fmt.Errorf("%w: path %q has skip-worktree set; sparse or partial working checkouts are unsupported; use a full checkout or staged mode", ErrUnsupportedRepository, rel)
		}
		paths = append(paths, rel)
		indexModes[rel] = gitModeToFileMode(mode)
	}
	return paths, indexModes, nil
}

func validateSourceGitMode(mode, rel string) error {
	switch mode {
	case "100644", "100755", "120000":
		return nil
	case "160000":
		return fmt.Errorf("%w: submodule %q is unsupported; exclude it explicitly or use a checkout without submodules", ErrUnsupportedRepository, rel)
	default:
		return fmt.Errorf("%w: path %q has unsupported git mode %s", ErrUnsupportedRepository, rel, mode)
	}
}

func planWorkspace(ctx context.Context, root, head string, baseline []plannedEntry, matcher ignoreMatcher, trackedPaths, untrackedPaths []string, indexModes map[string]int64, changeMode string, contentMode sourceContentMode) ([]plannedEntry, error) {
	before := make(map[string]plannedEntry, len(baseline))
	for _, item := range baseline {
		before[item.entry.Path] = item
	}
	if changeMode == syncbundle.ChangeModeStaged {
		entries, err := planStagedWorkspace(ctx, root, head, matcher, contentMode)
		if err != nil {
			return nil, err
		}
		changed := entries[:0]
		for _, item := range entries {
			if prior, ok := before[item.entry.Path]; ok && plannedEntriesEqual(prior, item, contentMode) {
				continue
			}
			changed = append(changed, item)
		}
		clear(entries[len(changed):])
		return changed, nil
	}
	workspaceRoot, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer workspaceRoot.Close()
	paths := append(append([]string(nil), trackedPaths...), untrackedPaths...)
	seen := make(map[string]struct{})
	var entries []plannedEntry
	for _, rel := range paths {
		if ancestor, ok, err := symlinkAncestor(root, rel); err != nil {
			return nil, err
		} else if ok {
			rel = ancestor
		}
		if matcher.ignored(rel) {
			continue
		}
		if _, ok := seen[rel]; ok {
			continue
		}
		seen[rel] = struct{}{}
		item, ok, err := workspaceEntry(workspaceRoot, rel, indexModes[rel], contentMode)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		// Compare each eligible file immediately, including assume-unchanged
		// paths, so unchanged working copies do not accumulate in memory.
		if prior, ok := before[item.entry.Path]; ok && plannedEntriesEqual(prior, item, contentMode) {
			continue
		}
		entries = append(entries, item)
	}
	sortPlanned(entries)
	return entries, nil
}
