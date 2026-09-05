package syncbundlebuild

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func planChangedFiles(ctx context.Context, root, head string, matcher ignoreMatcher, changeMode string, includeUntracked bool) ([]string, error) {
	var paths []string
	appendGitPaths := func(args ...string) error {
		out, err := gitOutput(ctx, root, args...)
		if err != nil {
			return err
		}
		paths = append(paths, splitNUL(out)...)
		return nil
	}

	if head == "" {
		switch changeMode {
		case syncbundle.ChangeModeStaged:
			if err := appendGitPaths("ls-files", "-z", "--cached"); err != nil {
				return nil, err
			}
		case syncbundle.ChangeModeWorking, syncbundle.ChangeModeAll:
			args := []string{"ls-files", "-z", "--cached"}
			if includeUntracked {
				args = append(args, "--others", "--exclude-standard")
			}
			if err := appendGitPaths(args...); err != nil {
				return nil, err
			}
		}
	} else {
		// Disable git rename detection (--no-renames) so a rename surfaces as a
		// delete of the source plus an add of the destination. Otherwise a rename
		// that moves a file into a different rule domain collapses to the
		// destination path only, hiding the deleted source from affected-rule
		// matching and skipping the checks its old location required.
		switch changeMode {
		case syncbundle.ChangeModeStaged:
			if err := appendGitPaths("diff", "-z", "--name-only", "--no-renames", "--cached", head, "--"); err != nil {
				return nil, err
			}
		case syncbundle.ChangeModeWorking:
			if err := appendGitPaths("diff", "-z", "--name-only", "--no-renames", "--"); err != nil {
				return nil, err
			}
			if err := appendGitPaths("diff", "-z", "--name-only", "--no-renames", "--cached", head, "--"); err != nil {
				return nil, err
			}
			if includeUntracked {
				if err := appendGitPaths("ls-files", "-z", "--others", "--exclude-standard"); err != nil {
					return nil, err
				}
			}
		case syncbundle.ChangeModeAll:
			if err := appendGitPaths("diff", "-z", "--name-only", "--no-renames", head, "--"); err != nil {
				return nil, err
			}
			if includeUntracked {
				if err := appendGitPaths("ls-files", "-z", "--others", "--exclude-standard"); err != nil {
					return nil, err
				}
			}
		}
	}

	seen := make(map[string]struct{})
	changed := make([]string, 0, len(paths))
	for _, raw := range paths {
		rel, err := syncbundle.ValidateRelativePath(raw)
		if err != nil {
			return nil, err
		}
		if matcher.ignored(rel) {
			continue
		}
		if syncbundle.IsGitControlPath(rel) {
			return nil, fmt.Errorf("changed path %q targets git control metadata", rel)
		}
		if _, ok := seen[rel]; ok {
			continue
		}
		seen[rel] = struct{}{}
		changed = append(changed, rel)
	}
	sort.Strings(changed)
	return changed, nil
}

func appendWorkspacePath(root, rel string, matcher ignoreMatcher, seen map[string]struct{}, entries []plannedEntry) ([]plannedEntry, error) {
	if _, ok, err := symlinkAncestor(root, rel); err != nil || ok {
		return entries, err
	}
	source := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Lstat(source)
	if err != nil {
		if os.IsNotExist(err) {
			return entries, nil
		}
		return nil, err
	}
	if info.IsDir() {
		err := filepath.WalkDir(source, func(path string, dirEntry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if path == source {
				return nil
			}
			nested, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			nested = filepath.ToSlash(nested)
			if matcher.ignored(nested) {
				if dirEntry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if dirEntry.IsDir() {
				return nil
			}
			if _, ok := seen[nested]; ok {
				return nil
			}
			seen[nested] = struct{}{}
			info, err := os.Lstat(path)
			if err != nil {
				return err
			}
			entry, ok, err := entryFromInfo(nested, path, info)
			if err != nil || !ok {
				return err
			}
			entries = append(entries, entry)
			return nil
		})
		return entries, err
	}
	entry, ok, err := entryFromInfo(rel, source, info)
	if err != nil || !ok {
		return entries, err
	}
	return append(entries, entry), nil
}

func entryFromInfo(rel, source string, info os.FileInfo) (plannedEntry, bool, error) {
	entry := syncbundle.Entry{Path: rel, Mode: int64(info.Mode().Perm())}
	if info.Mode()&os.ModeSymlink != 0 {
		linkname, err := os.Readlink(source)
		if err != nil {
			return plannedEntry{}, false, err
		}
		if err := syncbundle.ValidateSymlinkTarget(linkname); err != nil {
			return plannedEntry{}, false, fmt.Errorf("symlink %q has invalid target: %w", rel, err)
		}
		entry.Type = syncbundle.EntryTypeSymlink
		entry.Linkname = linkname
		return plannedEntry{entry: entry, source: source}, true, nil
	}
	if !info.Mode().IsRegular() {
		return plannedEntry{}, false, nil
	}
	entry.Type = syncbundle.EntryTypeFile
	entry.Size = info.Size()
	return plannedEntry{entry: entry, source: source}, true, nil
}
