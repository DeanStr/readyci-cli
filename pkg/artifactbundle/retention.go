package artifactbundle

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strings"
)

const (
	RetentionManifestPath     = ".readyci/artifact-retention.json"
	RetentionSchemaVersion    = "readyci.artifact_retention.v1"
	MaxRetentionManifestBytes = 1 << 20
	MaxRetentionOmissions     = 128
	MaxRetentionCandidates    = 100000
)

// Retention records the complete selected file count and a bounded list of
// omissions. It describes collection, independently of any report parser limit.
type Retention struct {
	SchemaVersion     string `json:"schema_version"`
	MatchedFileCount  int    `json:"matched_file_count"`
	RetainedFileCount int    `json:"retained_file_count"`
	OmittedFileCount  int    `json:"omitted_file_count"`
	// OmittedReportCount counts path-based candidates; it does not establish
	// that omitted XML contained JUnit results, nor count any test executions.
	OmittedReportCount     int           `json:"omitted_report_count"`
	FailedWorkspaceOmitted bool          `json:"failed_workspace_omitted,omitempty"`
	OmittedFiles           []OmittedFile `json:"omitted_files"`
	OmissionsTruncated     bool          `json:"omissions_truncated,omitempty"`
	// UnmatchedPaths records configured literal paths with no selected files,
	// independently of the bounded omitted-file sample.
	UnmatchedPaths []string `json:"unmatched_paths,omitempty"`
}

type OmittedFile struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
	Reason    string `json:"reason"`
}

// RequiredOmissions excludes only Ready CI's optional generated workspace
// archive. All other explicitly selected artifacts remain required evidence.
func (r *Retention) RequiredOmissions() bool {
	if r == nil {
		return false
	}
	optional := 0
	if r.FailedWorkspaceOmitted {
		optional = 1
	}
	return r.OmittedFileCount > optional
}

// IsTestReportPath selects report candidates without parsing their contents.
func IsTestReportPath(name string) bool {
	lower := strings.ToLower(name)
	base := path.Base(lower)
	return strings.HasSuffix(lower, ".xml") || strings.HasSuffix(lower, ".trx") ||
		strings.HasSuffix(lower, ".junit") || base == "junit.json" || base == "test-results.json"
}

func DecodeRetention(data []byte) (*Retention, error) {
	if len(data) > MaxRetentionManifestBytes {
		return nil, fmt.Errorf("artifact retention manifest exceeds its byte limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var r Retention
	if err := decoder.Decode(&r); err != nil {
		return nil, fmt.Errorf("invalid artifact retention manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("artifact retention manifest contains trailing data")
	}
	if r.SchemaVersion != RetentionSchemaVersion || r.MatchedFileCount < 1 ||
		r.MatchedFileCount > MaxRetentionCandidates || r.RetainedFileCount < 0 ||
		r.RetainedFileCount > MaxEntryCount || r.OmittedFileCount < 1 ||
		r.OmittedFileCount != r.MatchedFileCount-r.RetainedFileCount ||
		r.OmittedReportCount < 0 || r.OmittedReportCount > r.OmittedFileCount ||
		len(r.OmittedFiles) < 1 || len(r.OmittedFiles) > min(r.OmittedFileCount, MaxRetentionOmissions) ||
		r.OmissionsTruncated != (r.OmittedFileCount > len(r.OmittedFiles)) {
		return nil, fmt.Errorf("artifact retention manifest has invalid counts or version")
	}
	seen := map[string]bool{}
	reports, workspace := 0, false
	for _, f := range r.OmittedFiles {
		rel, err := ValidateArchivePath(f.Path)
		if err != nil || rel != f.Path || rel == RetentionManifestPath || len(rel) > 4096 ||
			seen[rel] || f.SizeBytes < 0 || f.Reason != "bundle_limit" {
			return nil, fmt.Errorf("artifact retention manifest has invalid omitted file")
		}
		seen[rel] = true
		if IsTestReportPath(rel) {
			reports++
		}
		workspace = workspace || IsFailedWorkspaceArtifactPattern(rel)
	}
	if workspace != r.FailedWorkspaceOmitted || reports > r.OmittedReportCount ||
		!r.OmissionsTruncated && reports != r.OmittedReportCount {
		return nil, fmt.Errorf("artifact retention manifest has inconsistent omission kinds")
	}
	if len(r.UnmatchedPaths) > MaxRetentionCandidates {
		return nil, fmt.Errorf("artifact retention manifest has too many unmatched paths")
	}
	omittedFiles := make([]File, len(r.OmittedFiles))
	for index, file := range r.OmittedFiles {
		omittedFiles[index] = File{Path: file.Path}
	}
	for _, value := range r.UnmatchedPaths {
		rel, err := NormalizePattern(value)
		if err != nil || rel == "" || rel != value || len(rel) > 4096 || hasPatternMeta(rel) || IsFailedWorkspaceArtifactPattern(rel) || seen[rel] {
			return nil, fmt.Errorf("artifact retention manifest has an invalid unmatched path")
		}
		seen[rel] = true
	}
	if unmatched, err := UnmatchedLiteralPaths(r.UnmatchedPaths, omittedFiles); err != nil || len(unmatched) != len(r.UnmatchedPaths) {
		return nil, fmt.Errorf("artifact retention manifest lists an omitted path as unmatched")
	}
	return &r, nil
}

// ValidateRetentionFiles binds decoded collection counts to all retained files,
// including the reserved metadata entry. Consumers must validate the entire
// archive, even when only some files will be applied or indexed.
func ValidateRetentionFiles(retention *Retention, files []File) error {
	if retention == nil {
		return nil
	}
	if retention.RetainedFileCount != len(files)-1 {
		return fmt.Errorf("artifact retention manifest does not match retained files")
	}
	retained := make(map[string]bool, len(files))
	for _, f := range files {
		retained[f.Path] = true
	}
	for _, f := range retention.OmittedFiles {
		if retained[f.Path] {
			return fmt.Errorf("artifact retention manifest lists a retained file as omitted")
		}
	}
	if len(retention.UnmatchedPaths) > 0 {
		selectedFiles := make([]File, 0, len(files)-1)
		for _, f := range files {
			if f.Path != RetentionManifestPath {
				selectedFiles = append(selectedFiles, f)
			}
		}
		if unmatched, err := UnmatchedLiteralPaths(retention.UnmatchedPaths, selectedFiles); err != nil || len(unmatched) != len(retention.UnmatchedPaths) {
			return fmt.Errorf("artifact retention manifest lists a retained path as unmatched")
		}
	}
	return nil
}
