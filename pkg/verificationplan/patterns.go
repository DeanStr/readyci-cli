package verificationplan

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// PatternMatches reports whether an affected-rule path pattern matches a path.
func PatternMatches(patternValue, pathValue string) (bool, error) {
	patternValue = NormalizePath(patternValue)
	pathValue = NormalizePath(pathValue)
	if patternValue == "" || pathValue == "" {
		return false, nil
	}
	if patternValue == "." || patternValue == "**" {
		return true, nil
	}
	patternParts := strings.Split(patternValue, "/")
	pathParts := strings.Split(pathValue, "/")
	return patternPartsMatch(patternParts, pathParts)
}

// ValidatePattern rejects patterns that are empty, absolute, or escape the repo.
func ValidatePattern(patternValue string) error {
	raw := strings.TrimSpace(patternValue)
	if raw == "" {
		return errors.New("path pattern cannot be empty")
	}
	if filepath.IsAbs(raw) {
		return fmt.Errorf("path pattern %q must be relative", patternValue)
	}
	for _, part := range strings.Split(filepath.ToSlash(raw), "/") {
		if part == ".." {
			return fmt.Errorf("path pattern %q must not escape the repository", patternValue)
		}
	}
	normalized := NormalizePath(raw)
	if normalized == "." || normalized == "**" {
		return nil
	}
	for _, part := range strings.Split(normalized, "/") {
		if part == "**" {
			continue
		}
		if _, err := path.Match(part, ""); err != nil {
			return fmt.Errorf("invalid path pattern %q: %w", patternValue, err)
		}
	}
	return nil
}

func patternPartsMatch(patternParts, pathParts []string) (bool, error) {
	if len(patternParts) == 0 {
		return len(pathParts) == 0, nil
	}
	head := patternParts[0]
	if head == "**" {
		if len(patternParts) == 1 {
			return true, nil
		}
		for i := 0; i <= len(pathParts); i++ {
			match, err := patternPartsMatch(patternParts[1:], pathParts[i:])
			if err != nil || match {
				return match, err
			}
		}
		return false, nil
	}
	if len(pathParts) == 0 {
		return false, nil
	}
	match, err := path.Match(head, pathParts[0])
	if err != nil {
		return false, err
	}
	if !match {
		return false, nil
	}
	return patternPartsMatch(patternParts[1:], pathParts[1:])
}

// NormalizePath canonicalizes a repository-relative path for comparison.
func NormalizePath(value string) string {
	value = filepath.ToSlash(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "./")
	value = path.Clean(value)
	if value == "." {
		return "."
	}
	return strings.TrimPrefix(value, "/")
}
