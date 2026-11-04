package syncbundle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// NormalizeSourcePolicyBinding returns the canonical bundle declaration for an
// independently supplied source filter. ProjectIgnoreUsed must remain explicit:
// a candidate-controlled .readyciignore is not part of a registered policy.
func NormalizeSourcePolicyBinding(binding SourcePolicyBinding) (SourcePolicyBinding, error) {
	if len(binding.Scopes) > 0 {
		return normalizeSourceScopeSet(binding)
	}
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
	if len(binding.Scopes) != len(normalized.Scopes) {
		return fmt.Errorf("source policy scopes are not normalized")
	}
	for index, scope := range binding.Scopes {
		if err := validateSourcePolicyBinding(scope); err != nil {
			return err
		}
		if SourcePolicyScopeKey(scope) != SourcePolicyScopeKey(normalized.Scopes[index]) {
			return fmt.Errorf("source policy scopes are not sorted")
		}
	}
	if binding.IncludeUntracked != normalized.IncludeUntracked || binding.ProjectIgnoreUsed != normalized.ProjectIgnoreUsed {
		return fmt.Errorf("source policy selection is not normalized")
	}
	return nil
}

// SourcePolicyScopeKey names an exact normalized filter, including its trusted
// policy revision. It is independent of check names and matrix variants.
func SourcePolicyScopeKey(binding SourcePolicyBinding) string {
	encoded, _ := json.Marshal(binding)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

const requestedSourcePolicyDomain = "readyci.requested_source_scopes.v1\x00"

// RequestedSourcePolicyDigest names a requested execution scope set by its
// canonical members. It is domain-separated from registered policy revisions,
// is recomputed by the service, and grants no verification authority.
func RequestedSourcePolicyDigest(scopes []SourcePolicyBinding) (string, error) {
	placeholder := "sha256:" + strings.Repeat("0", 64)
	set := make(map[string]SourcePolicyBinding, len(scopes))
	for _, scope := range scopes {
		if len(scope.Scopes) > 0 || scope.ProjectIgnoreUsed {
			return "", fmt.Errorf("requested source scopes must be explicit filters without nesting or project ignore")
		}
		scope.Digest = placeholder
		normalized, err := NormalizeSourcePolicyBinding(scope)
		if err != nil {
			return "", err
		}
		set[SourcePolicyScopeKey(normalized)] = normalized
	}
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	members := make([]SourcePolicyBinding, 0, len(keys))
	for _, key := range keys {
		members = append(members, set[key])
	}
	encoded, err := json.Marshal(members)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(append([]byte(requestedSourcePolicyDomain), encoded...))
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func normalizeSourceScopeSet(binding SourcePolicyBinding) (SourcePolicyBinding, error) {
	if len(binding.Scopes) > 256 || binding.ProjectIgnoreUsed {
		return SourcePolicyBinding{}, fmt.Errorf("source scope set requires at most 256 scopes and no project ignore")
	}
	set := make(map[string]SourcePolicyBinding)
	var fixtures []ReviewedFixture
	fixtureHashes := make(map[string]string)
	includeUntracked := false
	for _, scope := range binding.Scopes {
		if len(scope.Scopes) > 0 {
			return SourcePolicyBinding{}, fmt.Errorf("nested source scope sets are unsupported")
		}
		normalized, err := NormalizeSourcePolicyBinding(scope)
		if err != nil {
			return SourcePolicyBinding{}, err
		}
		if normalized.Digest != strings.TrimSpace(binding.Digest) || normalized.ProjectIgnoreUsed {
			return SourcePolicyBinding{}, fmt.Errorf("source scopes must bind the same declared policy without implicit project ignore")
		}
		set[SourcePolicyScopeKey(normalized)] = normalized
	}
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) == 1 {
		return set[keys[0]], nil
	}
	result := SourcePolicyBinding{
		Digest:                          strings.TrimSpace(binding.Digest),
		DefaultExcludePatterns:          DefaultSourceExcludePatterns(),
		DefaultUntrackedExcludePatterns: DefaultUntrackedExcludePatterns(),
	}
	for _, key := range keys {
		scope := set[key]
		result.Scopes = append(result.Scopes, scope)
		includeUntracked = includeUntracked || scope.IncludeUntracked
		for _, fixture := range scope.ReviewedFixtures {
			if hash, exists := fixtureHashes[fixture.Path]; exists {
				if hash != fixture.SHA256 {
					return SourcePolicyBinding{}, fmt.Errorf("source scopes require conflicting bytes for reviewed fixture %q", fixture.Path)
				}
				continue
			}
			fixtureHashes[fixture.Path] = fixture.SHA256
			fixtures = append(fixtures, fixture)
		}
	}
	// A single coherent snapshot cannot satisfy conflicting reviewed bytes.
	var err error
	result.ReviewedFixtures, err = NormalizeReviewedFixtures(fixtures)
	if err != nil {
		return SourcePolicyBinding{}, err
	}
	result.IncludeUntracked = includeUntracked
	scopes := result.Scopes
	result.Scopes = nil
	result, err = NormalizeSourcePolicyBinding(result)
	result.Scopes = scopes
	return result, err
}

// SourcePolicyAllowsPath applies the confidentiality floor as well as the
// exact registered filter. A scope set allows only paths admitted by a member.
func SourcePolicyAllowsPath(binding SourcePolicyBinding, rel string, untracked bool) bool {
	if len(binding.Scopes) > 0 {
		for _, scope := range binding.Scopes {
			if SourcePolicyAllowsPath(scope, rel, untracked) {
				return true
			}
		}
		return false
	}
	if IsGitControlPath(rel) || PathMatchesExcludePatterns(binding.ExcludePatterns, rel) {
		return false
	}
	if untracked && (!binding.IncludeUntracked || PathMatchesExcludePatterns(binding.DefaultUntrackedExcludePatterns, rel)) {
		return false
	}
	reviewed := false
	for _, fixture := range binding.ReviewedFixtures {
		if fixture.Path == rel && !untracked {
			reviewed = true
			break
		}
	}
	for _, pattern := range append(DefaultSourceExcludePatterns(), binding.DefaultExcludePatterns...) {
		if pattern == ".env*" && (IsPublicEnvExamplePath(rel) || reviewed) {
			continue
		}
		if PathMatchesExcludePatterns([]string{pattern}, rel) {
			return false
		}
	}
	return true
}
