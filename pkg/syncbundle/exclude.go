package syncbundle

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

var ErrInvalidExcludePattern = errors.New("invalid source exclusion")

// ValidateExcludePatterns rejects unsupported or malformed confidentiality
// rules rather than treating them as patterns that silently match nothing.
func ValidateExcludePatterns(patterns []string) error {
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" || strings.HasPrefix(pattern, "#") {
			continue
		}
		if strings.HasPrefix(pattern, "!") {
			return fmt.Errorf("%w: pattern %q: negation is unsupported; exclusions cannot re-include files", ErrInvalidExcludePattern, pattern)
		}
		normalized := normalizeExcludePattern(pattern)
		if strings.ContainsAny(pattern, "\x00\r\n") || normalized == "." || normalized == ".." || strings.HasPrefix(normalized, "../") || !doublestar.ValidatePattern(normalized) {
			return fmt.Errorf("%w: invalid exclude pattern %q", ErrInvalidExcludePattern, pattern)
		}
	}
	return nil
}

func normalizeExcludePattern(pattern string) string {
	// Preserve glob escapes. CleanSlashPath is for filesystem paths and would
	// turn an escaped metacharacter into a directory separator.
	return path.Clean(strings.TrimPrefix(strings.Trim(pattern, "/"), "./"))
}

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
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" || strings.HasPrefix(pattern, "#") {
			continue
		}
		pattern = normalizeExcludePattern(pattern)
		if pattern == "." {
			continue
		}
		// Match ancestors too: excluding a directory, including one selected
		// by a glob, excludes all of its descendants. ** spans path segments.
		for candidate := rel; candidate != "."; candidate = path.Dir(candidate) {
			if ok, _ := doublestar.Match(pattern, candidate); ok {
				return true
			}
			if ok, _ := doublestar.Match(pattern, path.Base(candidate)); ok {
				return true
			}
		}
	}
	return false
}
