package syncbundle

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// NormalizeSourcePolicyBinding returns the canonical bundle declaration for an
// independently supplied source filter. ProjectIgnoreUsed must remain explicit:
// a candidate-controlled .readyciignore is not part of a registered policy.
func NormalizeSourcePolicyBinding(binding SourcePolicyBinding) (SourcePolicyBinding, error) {
	binding.Digest = strings.TrimSpace(binding.Digest)
	if err := ValidateSnapshotDigest(binding.Digest); err != nil {
		return SourcePolicyBinding{}, fmt.Errorf("source policy digest: %w", err)
	}
	var err error
	binding.ExcludePatterns, err = normalizeSourcePolicyExcludePatterns(binding.ExcludePatterns)
	if err != nil {
		return SourcePolicyBinding{}, err
	}
	binding.DefaultExcludePatterns, err = normalizeSourcePolicyExcludePatterns(binding.DefaultExcludePatterns)
	if err != nil {
		return SourcePolicyBinding{}, fmt.Errorf("default source policy %w", err)
	}
	binding.DefaultUntrackedExcludePatterns, err = normalizeSourcePolicyExcludePatterns(binding.DefaultUntrackedExcludePatterns)
	if err != nil {
		return SourcePolicyBinding{}, fmt.Errorf("default untracked source policy %w", err)
	}
	binding.ReviewedFixtures, err = NormalizeReviewedFixtures(binding.ReviewedFixtures)
	if err != nil {
		return SourcePolicyBinding{}, fmt.Errorf("source policy reviewed fixtures: %w", err)
	}
	return binding, nil
}

func normalizeSourcePolicyExcludePatterns(patterns []string) ([]string, error) {
	if err := ValidateExcludePatterns(patterns); err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(patterns))
	excludes := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if strings.ContainsAny(pattern, "\x00\r\n") {
			return nil, fmt.Errorf("source policy exclude pattern contains control characters")
		}
		if _, ok := seen[pattern]; ok {
			continue
		}
		seen[pattern] = struct{}{}
		excludes = append(excludes, pattern)
	}
	sort.Strings(excludes)
	return excludes, nil
}

func validateSourcePolicyBinding(binding SourcePolicyBinding) error {
	normalized, err := NormalizeSourcePolicyBinding(binding)
	if err != nil {
		return err
	}
	if binding.Digest != normalized.Digest {
		return fmt.Errorf("source policy digest is not normalized")
	}
	if len(binding.ExcludePatterns) != len(normalized.ExcludePatterns) {
		return fmt.Errorf("source policy exclude patterns are not normalized")
	}
	for index := range binding.ExcludePatterns {
		if binding.ExcludePatterns[index] != normalized.ExcludePatterns[index] {
			return fmt.Errorf("source policy exclude patterns are not normalized")
		}
	}
	if len(binding.DefaultExcludePatterns) != len(normalized.DefaultExcludePatterns) {
		return fmt.Errorf("source policy default exclude patterns are not normalized")
	}
	for index := range binding.DefaultExcludePatterns {
		if binding.DefaultExcludePatterns[index] != normalized.DefaultExcludePatterns[index] {
			return fmt.Errorf("source policy default exclude patterns are not normalized")
		}
	}
	if !slices.Equal(binding.DefaultUntrackedExcludePatterns, normalized.DefaultUntrackedExcludePatterns) {
		return fmt.Errorf("source policy untracked default exclude patterns are not normalized")
	}
	if !slices.Equal(binding.ReviewedFixtures, normalized.ReviewedFixtures) {
		return fmt.Errorf("source policy reviewed fixtures are not normalized")
	}
	return nil
}
