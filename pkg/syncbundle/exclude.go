package syncbundle

import (
	"errors"
	"fmt"
	"path"
	"sort"
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
	".env*", ".git",
	"id_rsa", "id_dsa", "id_ecdsa", "id_ed25519",
	".git-credentials", "**/.aws/credentials",
}

var defaultUntrackedExcludePatterns = []string{
	"apps/web/.next", "coverage", "dist", "node_modules", "target",
	".npmrc", ".npmrc.local", ".pypirc", ".netrc",
	"credentials.json", "service-account.json", "application_default_credentials.json",
	"**/.docker/config.json", ".ssh", "kubeconfig",
}

// RequiresReviewedSourcePath identifies private-key and credential-store paths
// that must fail packaging rather than silently remove tracked source.
func RequiresReviewedSourcePath(rel string) bool {
	for _, pattern := range defaultSourceExcludePatterns {
		if pattern != ".git" && pattern != ".env*" && PathMatchesExcludePatterns([]string{pattern}, rel) {
			return true
		}
	}
	return false
}

// DefaultSourceExcludePatterns returns confidentiality and VCS exclusions that
// apply to both tracked and untracked source. Registered policy bindings carry
// this exact list instead of relying on hidden client behavior.
func DefaultSourceExcludePatterns() []string {
	patterns := append([]string(nil), defaultSourceExcludePatterns...)
	sort.Strings(patterns)
	return patterns
}

// ReviewableSourceExclude reports whether an exact fixture may bypass this
// built-in confidentiality rule. Additional policy rules and Git metadata
// remain exclusions even when a fixture is reviewed.
func ReviewableSourceExclude(pattern string) bool {
	for _, candidate := range defaultSourceExcludePatterns {
		if candidate == pattern && candidate != ".git" {
			return true
		}
	}
	return false
}

// DefaultUntrackedExcludePatterns returns generated-output and credential-name
// exclusions for untracked files. Git
// tracked source is preserved even when a directory has a conventional build
// output name. Explicit exclusions apply to tracked source too.
func DefaultUntrackedExcludePatterns() []string {
	patterns := append([]string(nil), defaultUntrackedExcludePatterns...)
	sort.Strings(patterns)
	return patterns
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
		if ok, _ := doublestar.Match(pattern, rel); ok {
			return true
		}
		// Match directory descendants in one pass. Matching every ancestor
		// separately repeatedly rescans deep paths for patterns containing **.
		if ok, _ := doublestar.Match(pattern+"/**", rel); ok {
			return true
		}
		// Basename matching also applies to every directory component, even
		// when the full pattern only matches paths rooted at the repository.
		for component := range strings.SplitSeq(rel, "/") {
			if ok, _ := doublestar.Match(pattern, component); ok {
				return true
			}
		}
	}
	return false
}
