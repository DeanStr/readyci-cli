package syncbundlebuild

import (
	"path"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

const defaultEnvExcludePattern = ".env*"

type ignoreMatcher struct {
	defaultPatterns []string
	extraPatterns   []string
}

func newIgnoreMatcher(extra []string) ignoreMatcher {
	return newIgnoreMatcherWithDefaults(extra, syncbundle.DefaultSourceExcludePatterns())
}

func newIgnoreMatcherWithDefaults(extra, defaults []string) ignoreMatcher {
	defaultPatterns := append([]string(nil), defaults...)
	extraPatterns := make([]string, 0, len(extra))
	for _, pattern := range extra {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" || strings.HasPrefix(pattern, "#") {
			continue
		}
		extraPatterns = append(extraPatterns, strings.Trim(pattern, "/"))
	}
	return ignoreMatcher{
		defaultPatterns: defaultPatterns,
		extraPatterns:   extraPatterns,
	}
}

func (matcher ignoreMatcher) ignored(rel string) bool {
	rel = syncbundle.CleanSlashPath(rel)
	if rel == "" {
		return true
	}
	if matchIgnorePatterns(matcher.extraPatterns, rel) {
		return true
	}
	envExample := isEnvExamplePath(rel)
	return matchIgnorePatternsSkipping(matcher.defaultPatterns, rel, func(pattern string) bool {
		return envExample && pattern == defaultEnvExcludePattern
	})
}

func isEnvExamplePath(rel string) bool {
	base := path.Base(syncbundle.CleanSlashPath(rel))
	return base == ".env.example" || strings.HasSuffix(base, ".env.example")
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
	if strings.TrimSpace(options.SourcePolicyDigest) != "" {
		defaults = options.DefaultExcludePatterns
	}
	for _, patterns := range [][]string{extra, defaults} {
		if err := syncbundle.ValidateExcludePatterns(patterns); err != nil {
			return ignoreMatcher{}, false, err
		}
	}
	return newIgnoreMatcherWithDefaults(extra, defaults), useProjectIgnore, nil
}
