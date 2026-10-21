package syncbundle

import (
	"fmt"
	"strings"
	"time"
)

const (
	FormatVersion   = 3
	BundleMediaType = "application/vnd.readyci.bundle+gzip"

	MaxCompressedBundleBytes = 400 << 20
	MaxExpandedBundleBytes   = 900 << 20
	MaxManifestBytes         = 4 << 20
	MaxBundleEntries         = 100000

	EntryTypeFile    = "file"
	EntryTypeSymlink = "symlink"

	entryTypeFile    = EntryTypeFile
	entryTypeSymlink = EntryTypeSymlink
)

const (
	ChangeModeWorking = "working"
	ChangeModeStaged  = "staged"
	ChangeModeAll     = "all"

	ChangedFilesManifestPath = ".git/readyci/check-touched-files"
)

type Manifest struct {
	Version    int         `json:"version"`
	CreatedAt  time.Time   `json:"created_at"`
	Git        GitMetadata `json:"git"`
	Submodules []Submodule `json:"submodules,omitempty"`
	ChangeMode string      `json:"change_mode,omitempty"`
	// SourcePolicy records the independently registered policy whose sync
	// filter was used to construct this workspace. It is absent for ordinary
	// candidate-controlled bundles. A service-authorized planner verifies this
	// binding against its own active policy revision before trusting the
	// bundle's changed-file view.
	SourcePolicy *SourcePolicyBinding `json:"source_policy,omitempty"`
	// ReviewedFixtures grants narrowly reviewed, content-bound exceptions to the
	// default confidentiality filter. Explicit exclusions still take precedence.
	ReviewedFixtures []ReviewedFixture `json:"reviewed_fixtures,omitempty"`
	// SnapshotDigest identifies the fully materialized workspace after baseline,
	// workspace overlay, deletions, ignore rules, and change-mode selection have
	// been applied, together with submodule pins and reviewed fixture bindings.
	// It is independent of archive compression and manifest timestamps.
	SnapshotDigest string   `json:"snapshot_digest"`
	BaselineKey    string   `json:"baseline_key,omitempty"`
	BaselineRef    bool     `json:"baseline_ref,omitempty"`
	ChangedFiles   []string `json:"changed_files,omitempty"`
	Baseline       []Entry  `json:"baseline"`
	Workspace      []Entry  `json:"workspace"`
	Deleted        []string `json:"deleted"`
}

type SourcePolicyBinding struct {
	Digest                          string            `json:"digest"`
	ExcludePatterns                 []string          `json:"exclude_patterns"`
	DefaultExcludePatterns          []string          `json:"default_exclude_patterns"`
	DefaultUntrackedExcludePatterns []string          `json:"default_untracked_exclude_patterns"`
	ReviewedFixtures                []ReviewedFixture `json:"reviewed_fixtures"`
	IncludeUntracked                bool              `json:"include_untracked"`
	ProjectIgnoreUsed               bool              `json:"project_ignore_used"`
}

func NormalizeBaselineKey(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "", nil
	}
	if len(value) != 64 {
		return "", fmt.Errorf("baseline key %q must be a 64-character sha256 hex digest", value)
	}
	for _, r := range value {
		if r >= '0' && r <= '9' || r >= 'a' && r <= 'f' {
			continue
		}
		return "", fmt.Errorf("baseline key %q must be a sha256 hex digest", value)
	}
	return value, nil
}

type GitMetadata struct {
	Branch string `json:"branch,omitempty"`
	Head   string `json:"head,omitempty"`
	Dirty  bool   `json:"dirty"`
}

type Entry struct {
	Path     string `json:"path"`
	Type     string `json:"type"`
	Mode     int64  `json:"mode"`
	Size     int64  `json:"size,omitempty"`
	Linkname string `json:"linkname,omitempty"`
}

func (manifest Manifest) IsZero() bool {
	return manifest.Version == 0
}

func NormalizeChangeMode(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = ChangeModeWorking
	}
	switch value {
	case ChangeModeWorking, ChangeModeStaged, ChangeModeAll:
		return value, nil
	default:
		return "", fmt.Errorf("invalid change mode %q", value)
	}
}
