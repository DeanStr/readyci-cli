package syncbundlebuild

import (
	"context"
	"path"

	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

type PathFilter struct {
	matcher          ignoreMatcher
	includeUntracked bool
	knownPaths       map[string]struct{}
}

func NewPathFilter(ctx context.Context, cwd string, options BuildOptions) (PathFilter, error) {
	changeMode, err := syncbundle.NormalizeChangeMode(options.ChangeMode)
	if err != nil {
		return PathFilter{}, err
	}
	root, err := repositoryRoot(ctx, cwd)
	if err != nil {
		return PathFilter{}, err
	}
	ignorePatterns, err := readProjectIgnore(root)
	if err != nil {
		return PathFilter{}, err
	}
	ignorePatterns = append(ignorePatterns, options.ExcludePatterns...)
	includeUntracked := true
	if options.IncludeUntracked != nil {
		includeUntracked = *options.IncludeUntracked
	}
	if changeMode == syncbundle.ChangeModeStaged {
		includeUntracked = false
	}
	var knownPaths map[string]struct{}
	if !includeUntracked {
		knownPaths, err = knownGitPaths(ctx, root)
		if err != nil {
			return PathFilter{}, err
		}
	}
	return PathFilter{
		matcher:          newIgnoreMatcher(ignorePatterns),
		includeUntracked: includeUntracked,
		knownPaths:       knownPaths,
	}, nil
}

func (filter PathFilter) Includes(path string) (bool, error) {
	rel, err := syncbundle.ValidateRelativePath(path)
	if err != nil {
		return false, err
	}
	if syncbundle.IsGitControlPath(rel) {
		return false, nil
	}
	if filter.matcher.ignored(rel) {
		return false, nil
	}
	if !filter.includeUntracked && !filter.pathKnownToGit(rel) {
		return false, nil
	}
	return true, nil
}

func (filter PathFilter) pathKnownToGit(rel string) bool {
	_, ok := filter.knownPaths[rel]
	return ok
}

func knownGitPaths(ctx context.Context, root string) (map[string]struct{}, error) {
	known := make(map[string]struct{})
	indexPaths, err := gitOutput(ctx, root, "ls-files", "-z", "--cached")
	if err != nil {
		return nil, err
	}
	for _, rel := range splitNUL(indexPaths) {
		addKnownGitPath(known, rel)
	}
	head := gitHead(ctx, root)
	if head != "" {
		headPaths, err := gitOutput(ctx, root, "ls-tree", "-rz", "--name-only", head)
		if err != nil {
			return nil, err
		}
		for _, rel := range splitNUL(headPaths) {
			addKnownGitPath(known, rel)
		}
	}
	return known, nil
}

func addKnownGitPath(known map[string]struct{}, rel string) {
	for current := rel; current != "." && current != "/"; current = path.Dir(current) {
		known[current] = struct{}{}
	}
}
