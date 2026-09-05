package preflightapi

import (
	"time"

	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

const ProjectVerificationPolicySchemaVersion = "readyci.project_verification_policy.v1"
const ProjectVerificationSourcePolicySchemaVersion = "readyci.project_verification_source_policy.v1"

type ProjectVerificationPolicyRevision struct {
	PolicyID          string    `json:"policy_id"`
	ProjectID         string    `json:"project_id"`
	Revision          int64     `json:"revision"`
	Digest            string    `json:"digest"`
	ConfigYAML        string    `json:"config_yaml,omitempty"`
	CheckNames        []string  `json:"check_names"`
	AffectedRuleCount int       `json:"affected_rule_count"`
	Source            string    `json:"source"`
	Status            string    `json:"status"`
	CreatedByUserID   string    `json:"created_by_user_id,omitempty"`
	CreatedByTokenID  string    `json:"created_by_token_id,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	ActivatedAt       time.Time `json:"activated_at"`
}

type ProjectVerificationPolicyResponse struct {
	SchemaVersion string                              `json:"schema_version"`
	ProjectID     string                              `json:"project_id"`
	Active        *ProjectVerificationPolicyRevision  `json:"active,omitempty"`
	Revisions     []ProjectVerificationPolicyRevision `json:"revisions"`
}

// ProjectVerificationSourcePolicyResponse exposes only the active revision's
// source-selection contract. Verification tokens need this to build the exact
// upload accepted by the service, but do not need policy commands or history.
type ProjectVerificationSourcePolicyResponse struct {
	SchemaVersion string                          `json:"schema_version"`
	ProjectID     string                          `json:"project_id"`
	Active        *syncbundle.SourcePolicyBinding `json:"active,omitempty"`
}

type PreviewProjectVerificationPolicyRequest struct {
	ConfigYAML string `json:"config_yaml"`
}

type ProjectVerificationPolicyPreview struct {
	SchemaVersion     string   `json:"schema_version"`
	ProjectID         string   `json:"project_id"`
	Digest            string   `json:"digest"`
	ConfigYAML        string   `json:"config_yaml"`
	ActiveDigest      string   `json:"active_digest,omitempty"`
	CheckNames        []string `json:"check_names"`
	AffectedRuleCount int      `json:"affected_rule_count"`
	AddedChecks       []string `json:"added_checks"`
	RemovedChecks     []string `json:"removed_checks"`
	ChangedChecks     []string `json:"changed_checks"`
	Warnings          []string `json:"warnings"`
}

type ActivateProjectVerificationPolicyRequest struct {
	ConfigYAML           string `json:"config_yaml"`
	PreviewDigest        string `json:"preview_digest"`
	ExpectedActiveDigest string `json:"expected_active_digest,omitempty"`
}
