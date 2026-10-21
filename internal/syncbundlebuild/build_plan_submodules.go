package syncbundlebuild

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

type pinnedSubmoduleSource struct {
	entries []plannedEntry
	pins    []syncbundle.Submodule
}

type submoduleGitView struct {
	files []gitSourceEntry
	pins  map[string]string
}

// planPinnedSubmodules snapshots only locally initialized, unchanged Git pins.
// It never invokes Git submodule update, follows a URL, or downloads objects.
// Module files become ordinary prefixed source leaves, not native gitlinks.
func planPinnedSubmodules(ctx context.Context, root string, matcher ignoreMatcher, contentMode sourceContentMode) (pinnedSubmoduleSource, error) {
	before, err := submoduleView(ctx, root, "", gitHead(ctx, root), false, matcher)
	if err != nil {
		return pinnedSubmoduleSource{}, err
	}
	after, err := submoduleView(ctx, root, "", "", true, matcher)
	if err != nil {
		return pinnedSubmoduleSource{}, err
	}
	if err := identicalSubmodulePins(before.pins, after.pins); err != nil {
		return pinnedSubmoduleSource{}, err
	}
	if len(before.pins) == 0 {
		return pinnedSubmoduleSource{}, nil
	}
	workspace, err := os.OpenRoot(root)
	if err != nil {
		return pinnedSubmoduleSource{}, err
	}
	defer workspace.Close()
	planned := pinnedSubmoduleSource{}
	for _, rel := range sortedModulePaths(before.pins) {
		if err := collectPinnedSubmodule(ctx, root, workspace, rel, before.pins[rel], 1, matcher, contentMode, &planned); err != nil {
			return pinnedSubmoduleSource{}, err
		}
	}
	sortPlanned(planned.entries)
	sort.Slice(planned.pins, func(i, j int) bool { return planned.pins[i].Path < planned.pins[j].Path })
	return planned, nil
}

func collectPinnedSubmodule(ctx context.Context, root string, workspace *os.Root, rel, commit string, depth int, matcher ignoreMatcher, contentMode sourceContentMode, planned *pinnedSubmoduleSource) error {
	if depth > syncbundle.MaxSubmoduleDepth || len(planned.pins) >= syncbundle.MaxSubmodules {
		return fmt.Errorf("%w: submodule %q exceeds recursive source limits", ErrUnsupportedRepository, rel)
	}
	parent, name, closeParent, err := workspaceParent(workspace, rel)
	if err != nil {
		return submoduleStateError(rel, commit, "cannot open a contained initialized directory")
	}
	defer closeParent()
	info, err := parent.Lstat(name)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return submoduleStateError(rel, commit, "is uninitialized or is not a contained directory")
	}
	moduleRoot := filepath.Join(root, filepath.FromSlash(rel))
	top, err := repositoryRoot(ctx, moduleRoot)
	if err != nil || filepath.Clean(top) != filepath.Clean(moduleRoot) {
		return submoduleStateError(rel, commit, "is uninitialized or resolves to a different Git worktree")
	}
	if actual := gitHead(ctx, moduleRoot); actual != commit {
		return submoduleStateError(rel, commit, fmt.Sprintf("HEAD is %q rather than the parent gitlink pin", actual))
	}
	before, err := submoduleView(ctx, moduleRoot, rel, commit, false, matcher)
	if err != nil {
		return fmt.Errorf("submodule %q: %w", rel, err)
	}
	after, err := submoduleView(ctx, moduleRoot, rel, "", true, matcher)
	if err != nil {
		return fmt.Errorf("submodule %q: %w", rel, err)
	}
	if err := identicalSubmodulePins(before.pins, after.pins); err != nil {
		return err
	}
	if !sameModuleFiles(before.files, after.files) {
		return submoduleStateError(rel, commit, "has staged file changes; clean submodules are required")
	}
	untracked, err := gitOutput(ctx, moduleRoot, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return err
	}
	for _, candidate := range splitNUL(untracked) {
		full, err := prefixedModulePath(rel, candidate)
		if err != nil {
			return err
		}
		if !matcher.ignored(full) {
			return submoduleStateError(rel, commit, fmt.Sprintf("has untracked source %q; clean submodules are required", full))
		}
	}
	entries, err := planGitEntries(ctx, moduleRoot, before.files, contentMode)
	if err != nil {
		return fmt.Errorf("submodule %q cannot read local pinned objects: %w", rel, err)
	}
	for _, entry := range entries {
		working, ok, err := workspaceEntry(workspace, entry.entry.Path, entry.entry.Mode, contentMode)
		if err != nil {
			return fmt.Errorf("submodule %q: %w", rel, err)
		}
		if !ok || !plannedEntriesEqual(entry, working, contentMode) {
			return submoduleStateError(rel, commit, fmt.Sprintf("has modified source %q; clean submodules are required", entry.entry.Path))
		}
	}
	planned.pins = append(planned.pins, syncbundle.Submodule{Path: rel, Commit: commit})
	planned.entries = append(planned.entries, entries...)
	if len(planned.entries) >= syncbundle.MaxBundleEntries {
		return fmt.Errorf("%w: recursive submodule source exceeds %d archive entries", ErrUnsupportedRepository, syncbundle.MaxBundleEntries)
	}
	for _, child := range sortedModulePaths(before.pins) {
		if err := collectPinnedSubmodule(ctx, root, workspace, child, before.pins[child], depth+1, matcher, contentMode, planned); err != nil {
			return err
		}
	}
	finalInfo, err := parent.Lstat(name)
	if err != nil || !os.SameFile(info, finalInfo) || gitHead(ctx, moduleRoot) != commit {
		return submoduleStateError(rel, commit, "changed while its source was being selected")
	}
	return nil
}

func submoduleView(ctx context.Context, root, prefix, commit string, index bool, matcher ignoreMatcher) (submoduleGitView, error) {
	view := submoduleGitView{pins: make(map[string]string)}
	var out []byte
	var err error
	if index {
		out, err = gitOutput(ctx, root, "ls-files", "-v", "-s", "-z", "--cached")
	} else if commit != "" {
		out, err = gitOutput(ctx, root, "ls-tree", "-rz", "--full-tree", "-r", commit)
	}
	if err != nil {
		return view, err
	}
	for _, record := range splitNUL(out) {
		flag := byte(0)
		if index {
			if len(record) < 3 || record[1] != ' ' {
				return view, fmt.Errorf("invalid submodule index record")
			}
			flag, record = record[0], record[2:]
		}
		mode, oid, stage, local, err := parseIndexRecord(record)
		if err != nil {
			return view, err
		}
		if !index {
			// Tree records are mode/type/object; index records are mode/object/stage.
			oid, stage = stage, "0"
		}
		rel, err := prefixedModulePath(prefix, local)
		if err != nil {
			return view, err
		}
		if matcher.ignoredTracked(rel) {
			continue
		}
		if prefix == "" && mode != "160000" {
			continue // Superproject files are handled by the ordinary planner.
		}
		if index && stage != "0" {
			return view, fmt.Errorf("%w: submodule source %q has unresolved merge stages", ErrUnsupportedRepository, rel)
		}
		if flag == 'S' || flag == 's' {
			return view, fmt.Errorf("%w: submodule source %q has skip-worktree set; a full initialized checkout is required", ErrUnsupportedRepository, rel)
		}
		if mode == "160000" {
			view.pins[rel] = oid
			continue
		}
		if err := validateSourceGitMode(mode, rel); err != nil {
			return view, err
		}
		view.files = append(view.files, gitSourceEntry{mode: mode, oid: oid, rel: rel})
	}
	sort.Slice(view.files, func(i, j int) bool { return view.files[i].rel < view.files[j].rel })
	return view, nil
}

func prefixedModulePath(prefix, local string) (string, error) {
	rel, err := syncbundle.ValidateRelativePath(local)
	if err != nil || rel != local || syncbundle.IsGitControlPath(rel) {
		return "", fmt.Errorf("invalid submodule source path %q", local)
	}
	if prefix != "" {
		rel = path.Join(prefix, rel)
	}
	return rel, nil
}

func sameModuleFiles(left, right []gitSourceEntry) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func identicalSubmodulePins(left, right map[string]string) error {
	for _, rel := range sortedModulePaths(left) {
		if right[rel] != left[rel] {
			return submoduleStateError(rel, left[rel], "the parent index changes or removes its gitlink; unchanged committed pins are required")
		}
	}
	for _, rel := range sortedModulePaths(right) {
		if left[rel] != right[rel] {
			return submoduleStateError(rel, right[rel], "the parent index adds or changes its gitlink; unchanged committed pins are required")
		}
	}
	return nil
}

func sortedModulePaths(pins map[string]string) []string {
	paths := make([]string, 0, len(pins))
	for rel := range pins {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	return paths
}

func submoduleStateError(rel, commit, state string) error {
	return fmt.Errorf("%w: submodule %q %s (required commit %s); initialize it locally with git submodule update --init --recursive -- %q and preserve or resolve local changes before retrying; Ready CI never fetches submodule source", ErrUnsupportedRepository, rel, strings.TrimSpace(state), commit, rel)
}
