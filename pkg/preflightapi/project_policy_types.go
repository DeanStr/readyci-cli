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
	ProposalID           string `json:"proposal_id,omitempty"`
	ConfigYAML           string `json:"config_yaml,omitempty"`
	PreviewDigest        string `json:"preview_digest"`
	ExpectedActiveDigest string `json:"expected_active_digest,omitempty"`
}

// PolicyChange describes a complete changed YAML field. Missing values are null;
// empty strings are real values. Paths are segments so keys containing dots remain unambiguous.
type PolicyChange struct {
	Path   []string `json:"path"`
	Before *string  `json:"before"`
	After  *string  `json:"after"`
}

type ProjectVerificationPolicyProposal struct {
	ProjectVerificationPolicyPreview
	ProposalID  string                   `json:"proposal_id"`
	Changes     []PolicyChange           `json:"changes"`
	CreatedAt   time.Time                `json:"created_at"`
	ExpiresAt   time.Time                `json:"expires_at"`
	ReviewURL   string                   `json:"review_url,omitempty"`
	SubmittedBy *PolicyProposalSubmitter `json:"submitted_by,omitempty"`
	ApprovedAt  *time.Time               `json:"approved_at,omitempty"`
}

// Submitter attribution is resolved from trusted identities, never request YAML.
type PolicyProposalSubmitter struct {
	UserID    string `json:"user_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Email     string `json:"email,omitempty"`
	TokenID   string `json:"token_id,omitempty"`
	TokenName string `json:"token_name,omitempty"`
}

// The inbox omits configuration bytes and field values to keep listing bounded.
type ProjectVerificationPolicyProposalSummary struct {
	ProjectID   string                   `json:"project_id"`
	ProposalID  string                   `json:"proposal_id"`
	Digest      string                   `json:"digest"`
	ChangeCount int                      `json:"change_count"`
	CheckCount  int                      `json:"check_count"`
	CreatedAt   time.Time                `json:"created_at"`
	ExpiresAt   time.Time                `json:"expires_at"`
	SubmittedBy *PolicyProposalSubmitter `json:"submitted_by,omitempty"`
	ReviewURL   string                   `json:"review_url,omitempty"`
}

type ProjectVerificationPolicyProposalList struct {
	ProjectID string                                     `json:"project_id"`
	Proposals []ProjectVerificationPolicyProposalSummary `json:"proposals"`
}
