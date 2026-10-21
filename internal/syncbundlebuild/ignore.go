package syncbundlebuild

import (
	"fmt"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

const defaultEnvExcludePattern = ".env*"

type ignoreMatcher struct {
	defaultPatterns   []string
	untrackedPatterns []string
	extraPatterns     []string
	reviewedFixtures  map[string]bool
}

func newIgnoreMatcher(extra []string) ignoreMatcher {
	return newIgnoreMatcherWithDefaults(extra, syncbundle.DefaultSourceExcludePatterns())
}

func newIgnoreMatcherWithDefaults(extra, defaults []string) ignoreMatcher {
	return newIgnoreMatcherWithFilters(extra, defaults, syncbundle.DefaultUntrackedExcludePatterns(), nil)
}

func newIgnoreMatcherWithFilters(extra, defaults, untracked []string, fixtures []syncbundle.ReviewedFixture) ignoreMatcher {
	// Version 3 has a confidentiality floor independent of a caller's recorded
	// additional defaults. Replaying an empty list must never include credentials
	// that source admission would reject. Exact reviewed fixtures are the only
	// exception for secret-shaped paths; Git control metadata is never allowed.
	defaultPatterns := append(syncbundle.DefaultSourceExcludePatterns(), defaults...)
	extraPatterns := make([]string, 0, len(extra))
	for _, pattern := range extra {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" || strings.HasPrefix(pattern, "#") {
			continue
		}
		extraPatterns = append(extraPatterns, strings.Trim(pattern, "/"))
	}
	return ignoreMatcher{
		defaultPatterns:   defaultPatterns,
		untrackedPatterns: append([]string(nil), untracked...),
		extraPatterns:     extraPatterns,
		reviewedFixtures:  reviewedFixturePaths(fixtures),
	}
}

func (matcher ignoreMatcher) ignored(rel string) bool {
	return matcher.ignoredSource(rel, false)
}

func (matcher ignoreMatcher) ignoredTracked(rel string) bool {
	return matcher.ignoredSource(rel, true)
}

func (matcher ignoreMatcher) ignoredSource(rel string, tracked bool) bool {
	rel = syncbundle.CleanSlashPath(rel)
	if rel == "" {
		return true
	}
	if matchIgnorePatterns(matcher.extraPatterns, rel) {
		return true
	}
	if !tracked && matchIgnorePatterns(matcher.untrackedPatterns, rel) {
		return true
	}
	envExample := isEnvExamplePath(rel)
	return matchIgnorePatternsSkipping(matcher.defaultPatterns, rel, func(pattern string) bool {
		return pattern == defaultEnvExcludePattern && (envExample || tracked && matcher.reviewedFixtures[rel])
	})
}

func reviewedFixturePaths(fixtures []syncbundle.ReviewedFixture) map[string]bool {
	paths := make(map[string]bool, len(fixtures))
	for _, fixture := range fixtures {
		paths[fixture.Path] = true
	}
	return paths
}

func isEnvExamplePath(rel string) bool {
	return syncbundle.IsPublicEnvExamplePath(rel)
}

func matchIgnorePatterns(patterns []string, rel string) bool {
	return syncbundle.PathMatchesExcludePatterns(patterns, rel)
}

func matchIgnorePatternsSkipping(patterns []string, rel string, skip func(string) bool) bool {
	for _, pattern := range patterns {
		if skip != nil && skip(pattern) {
			continue
		}
		if syncbundle.PathMatchesExcludePatterns([]string{pattern}, rel) {
			return true
		}
	}
	return false
}

// sourceIgnoreMatcher is shared by planning, packaging, currentness, and path
// previews, including the independent registered-policy filter.
func sourceIgnoreMatcher(root string, options BuildOptions) (ignoreMatcher, bool, error) {
	useProjectIgnore := options.UseProjectIgnore == nil || *options.UseProjectIgnore
	var extra []string
	if useProjectIgnore {
		var err error
		extra, err = readProjectIgnore(root)
		if err != nil {
			return ignoreMatcher{}, false, err
		}
	}
	extra = append(extra, options.ExcludePatterns...)
	defaults := syncbundle.DefaultSourceExcludePatterns()
	untrackedDefaults := syncbundle.DefaultUntrackedExcludePatterns()
	if strings.TrimSpace(options.SourcePolicyDigest) != "" || options.DefaultExcludePatterns != nil {
		defaults = options.DefaultExcludePatterns
	}
	if strings.TrimSpace(options.SourcePolicyDigest) != "" || options.DefaultUntrackedExcludePatterns != nil {
		untrackedDefaults = options.DefaultUntrackedExcludePatterns
	}
	for _, patterns := range [][]string{extra, defaults, untrackedDefaults} {
		if err := syncbundle.ValidateExcludePatterns(patterns); err != nil {
			return ignoreMatcher{}, false, err
		}
	}
	fixtures, err := syncbundle.NormalizeReviewedFixtures(options.ReviewedFixtures)
	if err != nil {
		return ignoreMatcher{}, false, fmt.Errorf("%w: %w", ErrUnsupportedRepository, err)
	}
	for _, fixture := range fixtures {
		if matchIgnorePatterns(extra, fixture.Path) {
			return ignoreMatcher{}, false, fmt.Errorf("%w: reviewed fixture %q is also explicitly excluded", ErrUnsupportedRepository, fixture.Path)
		}
	}
	return newIgnoreMatcherWithFilters(extra, defaults, untrackedDefaults, fixtures), useProjectIgnore, nil
}
