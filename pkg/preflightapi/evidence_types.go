package preflightapi

import (
	"fmt"
	"sort"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

const EvidenceSchemaVersion = "readyci.evidence.v0alpha1"
const TestResultSchemaVersion = "readyci.test-result.v0alpha1"

const (
	EvidenceTrustLockedVerifier = "T0"
	EvidenceTrustUnchangedTest  = "T1"
	EvidenceTrustCandidateTest  = "T2"
	EvidenceTrustHeuristic      = "T3"
	EvidenceTrustHumanApproval  = "T4"
)

type EvidenceProducer struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type EvidenceExecution struct {
	WorkspaceDigest   string `json:"workspace_digest"`
	TestDigest        string `json:"test_digest,omitempty"`
	CommandDigest     string `json:"command_digest,omitempty"`
	EnvironmentDigest string `json:"environment_digest,omitempty"`
	NetworkPolicy     string `json:"network_policy,omitempty"`
	ServiceDigest     string `json:"service_digest,omitempty"`
	Seed              string `json:"seed,omitempty"`
	Shard             string `json:"shard,omitempty"`
	Attempt           int    `json:"attempt"`
}

type EvidenceClaim struct {
	Criterion  string `json:"criterion"`
	Outcome    string `json:"outcome"`
	TrustLevel string `json:"trust_level"`
	Source     string `json:"source,omitempty"`
	Confidence string `json:"confidence,omitempty"`
}

type EvidenceFile struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size_bytes,omitempty"`
}

type EvidenceManifest struct {
	SchemaVersion string            `json:"schema_version"`
	Producer      EvidenceProducer  `json:"producer"`
	Execution     EvidenceExecution `json:"execution"`
	Claims        []EvidenceClaim   `json:"claims"`
	Files         []EvidenceFile    `json:"files"`
}

type StructuredTestResult struct {
	Ordinal          int    `json:"ordinal"`
	ID               string `json:"id,omitempty"`
	SchemaVersion    string `json:"schema_version"`
	Adapter          string `json:"adapter"`
	Suite            string `json:"suite,omitempty"`
	Class            string `json:"class,omitempty"`
	Test             string `json:"test"`
	Status           string `json:"status"`
	File             string `json:"file,omitempty"`
	Line             int    `json:"line,omitempty"`
	DurationMS       *int64 `json:"duration_ms,omitempty"`
	Attempt          int    `json:"attempt"`
	Message          string `json:"message,omitempty"`
	ReportPath       string `json:"report_path,omitempty"`
	MessageTruncated bool   `json:"message_truncated,omitempty"`
}

func NormalizeEvidenceManifest(manifest EvidenceManifest) (EvidenceManifest, error) {
	if strings.TrimSpace(manifest.SchemaVersion) == "" {
		manifest.SchemaVersion = EvidenceSchemaVersion
	}
	if manifest.SchemaVersion != EvidenceSchemaVersion {
		return manifest, fmt.Errorf("unsupported evidence schema version %q", manifest.SchemaVersion)
	}
	manifest.Producer.Name = strings.TrimSpace(manifest.Producer.Name)
	manifest.Producer.Version = strings.TrimSpace(manifest.Producer.Version)
	if manifest.Producer.Name == "" {
		return manifest, fmt.Errorf("evidence producer name is required")
	}
	if err := syncbundle.ValidateSnapshotDigest(manifest.Execution.WorkspaceDigest); err != nil {
		return manifest, fmt.Errorf("workspace_digest: %w", err)
	}
	for field, digest := range map[string]string{
		"test_digest": manifest.Execution.TestDigest, "command_digest": manifest.Execution.CommandDigest,
		"environment_digest": manifest.Execution.EnvironmentDigest, "service_digest": manifest.Execution.ServiceDigest,
	} {
		if digest != "" {
			if err := syncbundle.ValidateSnapshotDigest(digest); err != nil {
				return manifest, fmt.Errorf("%s: %w", field, err)
			}
		}
	}
	if manifest.Execution.Attempt < 1 {
		return manifest, fmt.Errorf("evidence attempt must be positive")
	}
	seenClaims := map[string]bool{}
	for index := range manifest.Claims {
		claim := &manifest.Claims[index]
		claim.Criterion = strings.TrimSpace(claim.Criterion)
		claim.Outcome = strings.TrimSpace(claim.Outcome)
		claim.TrustLevel = strings.TrimSpace(claim.TrustLevel)
		if claim.Criterion == "" || claim.Outcome == "" || !validEvidenceTrustLevel(claim.TrustLevel) {
			return manifest, fmt.Errorf("evidence claim requires criterion, outcome, and trust level T0 through T4")
		}
		claim.Source = strings.TrimSpace(claim.Source)
		claim.Confidence = strings.TrimSpace(claim.Confidence)
		if claim.Source == "inferred" && claim.Confidence != "low" {
			return manifest, fmt.Errorf("inferred evidence claim %q must have low confidence", claim.Criterion)
		}
		key := claim.Criterion + "\x00" + claim.TrustLevel
		if seenClaims[key] {
			return manifest, fmt.Errorf("duplicate evidence claim for criterion %q at trust level %s", claim.Criterion, claim.TrustLevel)
		}
		seenClaims[key] = true
	}
	seenFiles := map[string]bool{}
	for index := range manifest.Files {
		file := &manifest.Files[index]
		path, err := syncbundle.ValidateRelativePath(file.Path)
		if err != nil {
			return manifest, fmt.Errorf("evidence file: %w", err)
		}
		if syncbundle.IsGitControlPath(path) {
			return manifest, fmt.Errorf("evidence file %q targets git control metadata", path)
		}
		file.Path = path
		file.Kind = strings.TrimSpace(file.Kind)
		if file.Kind == "" || file.Size < 0 {
			return manifest, fmt.Errorf("evidence file %q requires kind and non-negative size", file.Path)
		}
		if err := syncbundle.ValidateSnapshotDigest(file.SHA256); err != nil {
			return manifest, fmt.Errorf("evidence file %q sha256: %w", file.Path, err)
		}
		if seenFiles[file.Path] {
			return manifest, fmt.Errorf("duplicate evidence file %q", file.Path)
		}
		seenFiles[file.Path] = true
	}
	sort.Slice(manifest.Claims, func(i, j int) bool {
		if manifest.Claims[i].Criterion != manifest.Claims[j].Criterion {
			return manifest.Claims[i].Criterion < manifest.Claims[j].Criterion
		}
		return manifest.Claims[i].TrustLevel < manifest.Claims[j].TrustLevel
	})
	sort.Slice(manifest.Files, func(i, j int) bool { return manifest.Files[i].Path < manifest.Files[j].Path })
	return manifest, nil
}

func NormalizeStructuredTestResult(result StructuredTestResult) (StructuredTestResult, error) {
	if strings.TrimSpace(result.SchemaVersion) == "" {
		result.SchemaVersion = TestResultSchemaVersion
	}
	if result.SchemaVersion != TestResultSchemaVersion {
		return result, fmt.Errorf("unsupported test result schema version %q", result.SchemaVersion)
	}
	result.Adapter = strings.TrimSpace(result.Adapter)
	result.Suite = strings.TrimSpace(result.Suite)
	result.Test = strings.TrimSpace(result.Test)
	result.Status = strings.TrimSpace(result.Status)
	result.File = strings.TrimSpace(result.File)
	result.Message = strings.TrimSpace(result.Message)
	if result.Adapter == "" || result.Test == "" {
		return result, fmt.Errorf("structured test result requires adapter and test")
	}
	switch result.Status {
	case "passed", "failed", "skipped", "error":
	default:
		return result, fmt.Errorf("unsupported structured test status %q", result.Status)
	}
	if result.Attempt < 1 || result.Line < 0 || result.Ordinal < 0 || result.DurationMS != nil && *result.DurationMS < 0 {
		return result, fmt.Errorf("structured test attempt must be positive and line/duration non-negative")
	}
	if result.File != "" {
		path, err := syncbundle.ValidateRelativePath(result.File)
		if err != nil || syncbundle.IsGitControlPath(path) {
			return result, fmt.Errorf("invalid structured test file %q", result.File)
		}
		result.File = path
	}
	if result.ReportPath != "" {
		path, err := syncbundle.ValidateRelativePath(result.ReportPath)
		if err != nil || syncbundle.IsGitControlPath(path) {
			return result, fmt.Errorf("invalid test report path %q", result.ReportPath)
		}
		result.ReportPath = path
	}
	return result, nil
}

func validEvidenceTrustLevel(value string) bool {
	switch value {
	case EvidenceTrustLockedVerifier, EvidenceTrustUnchangedTest, EvidenceTrustCandidateTest, EvidenceTrustHeuristic, EvidenceTrustHumanApproval:
		return true
	default:
		return false
	}
}
