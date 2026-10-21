package syncbundle

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
)

// ReviewedFixture permits one exact, reviewed regular file through a built-in
// confidentiality exclusion. A registered policy must independently authorize
// the same declaration before the service may treat it as trusted policy.
type ReviewedFixture struct {
	Path   string `json:"path" yaml:"path"`
	SHA256 string `json:"sha256" yaml:"sha256"`
}

// NormalizeReviewedFixtures validates exact paths and byte hashes, then sorts
// them by path. Paths and hashes are not repaired: accepting an ambiguous path
// or a partially specified hash could grant a broader exception than reviewed.
func NormalizeReviewedFixtures(fixtures []ReviewedFixture) ([]ReviewedFixture, error) {
	if len(fixtures) == 0 {
		return nil, nil
	}
	normalized := append([]ReviewedFixture(nil), fixtures...)
	seen := make(map[string]struct{}, len(fixtures))
	for _, fixture := range normalized {
		rel, err := ValidateRelativePath(fixture.Path)
		if err != nil {
			return nil, fmt.Errorf("reviewed fixture path: %w", err)
		}
		if rel != fixture.Path || strings.ContainsAny(fixture.Path, "*?[]{}") {
			return nil, fmt.Errorf("reviewed fixture path %q must be an exact normalized file path", fixture.Path)
		}
		if IsGitControlPath(rel) {
			return nil, fmt.Errorf("reviewed fixture path %q targets git control metadata", fixture.Path)
		}
		if _, exists := seen[fixture.Path]; exists {
			return nil, fmt.Errorf("duplicate reviewed fixture path %q", fixture.Path)
		}
		seen[fixture.Path] = struct{}{}
		decoded, err := hex.DecodeString(fixture.SHA256)
		if err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != fixture.SHA256 {
			return nil, fmt.Errorf("reviewed fixture %q sha256 must contain exactly 64 lowercase hex characters", fixture.Path)
		}
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].Path < normalized[j].Path })
	return normalized, nil
}

func validateReviewedFixtureDeclaration(manifest Manifest) error {
	normalized, err := NormalizeReviewedFixtures(manifest.ReviewedFixtures)
	if err != nil {
		return err
	}
	if !slices.Equal(manifest.ReviewedFixtures, normalized) {
		return fmt.Errorf("reviewed fixtures are not normalized")
	}
	if manifest.SourcePolicy != nil && !slices.Equal(manifest.ReviewedFixtures, manifest.SourcePolicy.ReviewedFixtures) {
		return fmt.Errorf("reviewed fixtures do not match source policy binding")
	}
	if len(normalized) == 0 {
		return nil
	}
	// Check metadata even for a delta whose external baseline bytes are not yet
	// available. Rehydration must validate those bytes before execution.
	reviewedPaths := make(map[string]bool, len(normalized))
	for _, fixture := range normalized {
		reviewedPaths[fixture.Path] = true
	}
	baselinePaths := make(map[string]bool, len(normalized))
	workspacePaths := make(map[string]bool, len(normalized))
	for _, group := range []struct {
		entries []Entry
		seen    map[string]bool
	}{{manifest.Baseline, baselinePaths}, {manifest.Workspace, workspacePaths}} {
		for _, entry := range group.entries {
			if !reviewedPaths[entry.Path] {
				continue
			}
			if entry.Type != EntryTypeFile {
				return fmt.Errorf("reviewed fixture %q must be a regular file in every source variant", entry.Path)
			}
			group.seen[entry.Path] = true
		}
	}
	deletedPaths := make(map[string]bool, len(manifest.Deleted))
	for _, deleted := range manifest.Deleted {
		deletedPaths[deleted] = true
	}
	for _, fixture := range normalized {
		if workspacePaths[fixture.Path] {
			continue
		}
		present := baselinePaths[fixture.Path]
		for candidate := fixture.Path; candidate != ""; candidate = parentPath(candidate) {
			if deletedPaths[candidate] {
				present = false
				break
			}
		}
		if !present {
			return fmt.Errorf("reviewed fixture %q is missing from materialized workspace", fixture.Path)
		}
	}
	return nil
}

// ValidateReviewedFixtureContents checks actual source bytes against every
// declared exception. It validates both baseline and workspace variants, even
// when one overwrites the other: both are transmitted and need review. Delta
// bundles defer only their absent external baseline bytes until rehydration.
func ValidateReviewedFixtureContents(manifest Manifest, baseline, workspace []ArchiveEntry) error {
	if err := validateReviewedFixtureDeclaration(manifest); err != nil {
		return err
	}
	if len(manifest.ReviewedFixtures) == 0 {
		return nil
	}
	byPath := make(map[string]ReviewedFixture, len(manifest.ReviewedFixtures))
	for _, fixture := range manifest.ReviewedFixtures {
		byPath[fixture.Path] = fixture
	}
	seenBaseline := make(map[string]bool, len(byPath))
	seenWorkspace := make(map[string]bool, len(byPath))
	for _, group := range []struct {
		name    string
		entries []ArchiveEntry
		seen    map[string]bool
	}{
		{name: "baseline", entries: baseline, seen: seenBaseline},
		{name: "workspace", entries: workspace, seen: seenWorkspace},
	} {
		for _, item := range group.entries {
			fixture, ok := byPath[item.Entry.Path]
			if !ok {
				continue
			}
			if item.Entry.Type != EntryTypeFile {
				return fmt.Errorf("reviewed fixture %q in %s must be a regular file", fixture.Path, group.name)
			}
			digest, err := reviewedFixtureContentHash(item)
			if err != nil {
				return fmt.Errorf("reviewed fixture %q in %s: %w", fixture.Path, group.name, err)
			}
			if digest != fixture.SHA256 {
				return fmt.Errorf("reviewed fixture %q in %s does not match its reviewed sha256", fixture.Path, group.name)
			}
			group.seen[fixture.Path] = true
		}
	}
	for _, group := range []struct {
		name    string
		entries []Entry
		seen    map[string]bool
	}{
		{name: "baseline", entries: manifest.Baseline, seen: seenBaseline},
		{name: "workspace", entries: manifest.Workspace, seen: seenWorkspace},
	} {
		if group.name == "baseline" && manifest.BaselineRef {
			continue
		}
		for _, entry := range group.entries {
			if _, reviewed := byPath[entry.Path]; reviewed && !group.seen[entry.Path] {
				return fmt.Errorf("reviewed fixture %q is missing %s bytes", entry.Path, group.name)
			}
		}
	}
	return nil
}

func reviewedFixtureContentHash(item ArchiveEntry) (string, error) {
	if item.Source == "" {
		if int64(len(item.Data)) != item.Entry.Size {
			return "", fmt.Errorf("file data size does not match declared size")
		}
		digest := sha256.Sum256(item.Data)
		return hex.EncodeToString(digest[:]), nil
	}
	file, err := openArchiveSource(item)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, readErr := io.CopyN(hash, file, item.Entry.Size)
	closeErr := file.Close()
	if readErr != nil {
		return "", readErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// ReviewedFixturesInEntries selects declarations whose exact paths are present
// in an entry set. It preserves the canonical declaration order.
func ReviewedFixturesInEntries(fixtures []ReviewedFixture, entries []ArchiveEntry) []ReviewedFixture {
	paths := make(map[string]bool, len(entries))
	for _, item := range entries {
		paths[item.Entry.Path] = true
	}
	var included []ReviewedFixture
	for _, fixture := range fixtures {
		if paths[fixture.Path] {
			included = append(included, fixture)
		}
	}
	return included
}

// Freeze reviewed source-backed files before any archive bytes are written.
// Validating a path and reopening it later would allow unreviewed replacement
// bytes to be transmitted if the file changed between those operations.
func freezeReviewedFixtureSources(manifest Manifest, baseline, workspace []ArchiveEntry) ([]ArchiveEntry, []ArchiveEntry, error) {
	if len(manifest.ReviewedFixtures) == 0 {
		return baseline, workspace, nil
	}
	if err := validateReviewedFixtureDeclaration(manifest); err != nil {
		return nil, nil, err
	}
	byPath := make(map[string]bool, len(manifest.ReviewedFixtures))
	for _, fixture := range manifest.ReviewedFixtures {
		byPath[fixture.Path] = true
	}
	frozen := make([][]ArchiveEntry, 0, 2)
	remaining := int64(MaxExpandedBundleBytes)
	for _, entries := range [][]ArchiveEntry{baseline, workspace} {
		copyEntries := append([]ArchiveEntry(nil), entries...)
		for index, item := range copyEntries {
			if !byPath[item.Entry.Path] || item.Source == "" {
				continue
			}
			if item.Entry.Type != EntryTypeFile {
				return nil, nil, fmt.Errorf("reviewed fixture %q must be a regular file", item.Entry.Path)
			}
			if item.Entry.Size < 0 || item.Entry.Size > remaining {
				return nil, nil, fmt.Errorf("reviewed fixture %q size exceeds source bundle bounds", item.Entry.Path)
			}
			remaining -= item.Entry.Size
			file, err := openArchiveSource(item)
			if err != nil {
				return nil, nil, err
			}
			data, readErr := io.ReadAll(io.LimitReader(file, item.Entry.Size+1))
			closeErr := file.Close()
			if readErr != nil {
				return nil, nil, readErr
			}
			if closeErr != nil {
				return nil, nil, closeErr
			}
			if int64(len(data)) != item.Entry.Size {
				return nil, nil, fmt.Errorf("reviewed fixture %q source size changed while freezing", item.Entry.Path)
			}
			copyEntries[index].Source = ""
			copyEntries[index].Data = data
		}
		frozen = append(frozen, copyEntries)
	}
	if err := ValidateReviewedFixtureContents(manifest, frozen[0], frozen[1]); err != nil {
		return nil, nil, err
	}
	return frozen[0], frozen[1], nil
}
