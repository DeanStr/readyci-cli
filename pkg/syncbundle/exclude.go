package syncbundle

import (
	"path"
	"strings"
)

var defaultSourceExcludePatterns = []string{
	".env*",
	".git",
	"apps/web/.next",
	"coverage",
	"dist",
	"node_modules",
	"target",
}

// DefaultSourceExcludePatterns returns the built-in source filter. Registered
// policy bindings carry this exact list in the manifest, instead of relying on
// hidden client behavior.
func DefaultSourceExcludePatterns() []string {
	return append([]string(nil), defaultSourceExcludePatterns...)
}

// PathMatchesExcludePatterns reports whether a repository-relative path is
// excluded by the same additional-pattern semantics used while constructing a
// source bundle. Patterns are matched against the full path and basename, and
// a directory pattern excludes everything below that directory.
func PathMatchesExcludePatterns(patterns []string, rel string) bool {
	rel = CleanSlashPath(rel)
	if rel == "" {
		return true
	}
	base := path.Base(rel)
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" || strings.HasPrefix(pattern, "#") {
			continue
		}
		pattern = CleanSlashPath(strings.Trim(pattern, "/"))
		if pattern == "" {
			continue
		}
		if pattern == rel || pattern == base {
			return true
		}
		if strings.HasSuffix(pattern, "/") && strings.HasPrefix(rel, strings.TrimSuffix(pattern, "/")+"/") {
			return true
		}
		if strings.HasPrefix(rel, pattern+"/") {
			return true
		}
		if strings.Contains(rel, "/"+pattern+"/") {
			return true
		}
		if ok, _ := path.Match(pattern, rel); ok {
			return true
		}
		if ok, _ := path.Match(pattern, base); ok {
			return true
		}
	}
	return false
}
