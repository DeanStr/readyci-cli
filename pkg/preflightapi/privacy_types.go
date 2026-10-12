package preflightapi

import (
	"encoding/json"
	"time"
)

const PrivacyExportSchemaVersion = "readyci.privacy_export.v2"

const (
	PrivacyRequestAccess      = "access"
	PrivacyRequestErasure     = "erasure"
	PrivacyRequestPortability = "portability"
)

type AdminCreatePrivacyCaseRequest struct {
	RequestType       string    `json:"request_type"`
	SubjectUserID     string    `json:"subject_user_id"`
	ControllerRole    string    `json:"controller_role"`
	IdentityVerified  bool      `json:"identity_verified"`
	AuthorityVerified bool      `json:"authority_verified"`
	DueAt             time.Time `json:"due_at"`
	Reason            string    `json:"reason"`
	HoldReason        string    `json:"hold_reason,omitempty"`
}

type AdminPrivacyCase struct {
	ID                     string                       `json:"id"`
	RequestType            string                       `json:"request_type"`
	Status                 string                       `json:"status"`
	SubjectUserID          string                       `json:"subject_user_id"`
	ControllerRole         string                       `json:"controller_role"`
	IdentityVerified       bool                         `json:"identity_verified"`
	AuthorityVerified      bool                         `json:"authority_verified"`
	DueAt                  time.Time                    `json:"due_at"`
	Reason                 string                       `json:"reason"`
	HoldReason             string                       `json:"hold_reason,omitempty"`
	PlanDigest             string                       `json:"plan_digest,omitempty"`
	LastExportedAt         *time.Time                   `json:"last_exported_at,omitempty"`
	LiveErasureCompletedAt *time.Time                   `json:"live_erasure_completed_at,omitempty"`
	CompletedAt            *time.Time                   `json:"completed_at,omitempty"`
	CreatedAt              time.Time                    `json:"created_at"`
	UpdatedAt              time.Time                    `json:"updated_at"`
	ProviderActions        []AdminPrivacyProviderAction `json:"provider_actions,omitempty"`
	RestoreLedger          *AdminPrivacyRestoreLedger   `json:"restore_ledger,omitempty"`
}

type AdminPrivacyProviderAction struct {
	Provider    string     `json:"provider"`
	Status      string     `json:"status"`
	Reason      string     `json:"reason,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type AdminPrivacyRestoreLedger struct {
	Status            string     `json:"status"`
	BackupExpiresAt   time.Time  `json:"backup_expires_at"`
	Reason            string     `json:"reason,omitempty"`
	ReplayCompletedAt *time.Time `json:"replay_completed_at,omitempty"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type AdminPrivacyOwnership struct {
	TeamID                      string `json:"team_id"`
	Role                        string `json:"role"`
	ActiveOwners                int    `json:"active_owners"`
	OwnershipResolutionRequired bool   `json:"ownership_resolution_required"`
}

type AdminPrivacyErasurePlan struct {
	CaseID        string                  `json:"case_id"`
	SubjectUserID string                  `json:"subject_user_id"`
	PlanDigest    string                  `json:"plan_digest"`
	TargetDigest  string                  `json:"target_digest"`
	GeneratedAt   time.Time               `json:"generated_at"`
	Ownership     []AdminPrivacyOwnership `json:"ownership"`
	RecordCounts  map[string]int64        `json:"record_counts"`
	Providers     []string                `json:"providers"`
	Blockers      []string                `json:"blockers"`
}

type AdminPlanPrivacyCaseRequest struct {
	Reason string `json:"reason"`
}

type AdminReviewPrivacyCaseRequest struct {
	IdentityVerified  *bool      `json:"identity_verified,omitempty"`
	AuthorityVerified *bool      `json:"authority_verified,omitempty"`
	DueAt             *time.Time `json:"due_at,omitempty"`
	// HoldReason is omitted to preserve the current hold, set to a non-empty
	// value to replace it, or set to an empty string to clear it.
	HoldReason *string `json:"hold_reason,omitempty"`
	Reason     string  `json:"reason"`
}

type AdminExecutePrivacyErasureRequest struct {
	PlanDigest      string    `json:"plan_digest"`
	BackupExpiresAt time.Time `json:"backup_expires_at"`
	Reason          string    `json:"reason"`
}

type AdminCompletePrivacyStepRequest struct {
	Reason string `json:"reason"`
}

type PrivacyExportSubject struct {
	UserID         string     `json:"user_id"`
	Email          string     `json:"email"`
	VerifiedEmails []string   `json:"verified_emails"`
	Name           string     `json:"name"`
	DisabledAt     *time.Time `json:"disabled_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

// PrivacyExportCase is the subject-visible projection of an operational privacy
// case. Free-form intake, hold, provider, and restore reasons remain restricted
// operator evidence; the export retains the resulting state and timestamps.
type PrivacyExportCase struct {
	CaseID                 string                        `json:"case_id"`
	RequestType            string                        `json:"request_type"`
	Status                 string                        `json:"status"`
	ControllerRole         string                        `json:"controller_role"`
	IdentityVerified       bool                          `json:"identity_verified"`
	AuthorityVerified      bool                          `json:"authority_verified"`
	DueAt                  time.Time                     `json:"due_at"`
	HoldActive             bool                          `json:"hold_active"`
	PlanDigest             string                        `json:"plan_digest,omitempty"`
	LastExportedAt         *time.Time                    `json:"last_exported_at,omitempty"`
	LiveErasureCompletedAt *time.Time                    `json:"live_erasure_completed_at,omitempty"`
	CompletedAt            *time.Time                    `json:"completed_at,omitempty"`
	CreatedAt              time.Time                     `json:"created_at"`
	UpdatedAt              time.Time                     `json:"updated_at"`
	ProviderActions        []PrivacyExportProviderAction `json:"provider_actions,omitempty"`
	RestoreLedger          *PrivacyExportRestoreLedger   `json:"restore_ledger,omitempty"`
}

type PrivacyExportProviderAction struct {
	Provider    string     `json:"provider"`
	Status      string     `json:"status"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type PrivacyExportRestoreLedger struct {
	Status            string     `json:"status"`
	BackupExpiresAt   time.Time  `json:"backup_expires_at"`
	ReplayCompletedAt *time.Time `json:"replay_completed_at,omitempty"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type PrivacyExportIdentity struct {
	Provider   string    `json:"provider"`
	Issuer     string    `json:"issuer"`
	Subject    string    `json:"subject"`
	Email      string    `json:"email"`
	Name       string    `json:"name"`
	LastTeamID string    `json:"last_team_id"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type PrivacyExportMembership struct {
	TeamID    string     `json:"team_id"`
	Role      string     `json:"role"`
	RemovedAt *time.Time `json:"removed_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

type PrivacyExportBrowserSession struct {
	SessionID    string     `json:"session_id"`
	TeamID       string     `json:"team_id"`
	AuthProvider string     `json:"auth_provider"`
	AuthIssuer   string     `json:"auth_issuer"`
	AuthSubject  string     `json:"auth_subject"`
	ExpiresAt    time.Time  `json:"expires_at"`
	RevokedAt    *time.Time `json:"revoked_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	LastSeenAt   *time.Time `json:"last_seen_at,omitempty"`
}

type PrivacyExportBillingCheckout struct {
	Provider                  string          `json:"provider"`
	CheckoutContextID         string          `json:"checkout_context_id"`
	CheckoutSessionID         string          `json:"checkout_session_id"`
	CheckoutSessionURLStored  bool            `json:"checkout_session_url_stored"`
	CheckoutSessionCreatedAt  *time.Time      `json:"checkout_session_created_at,omitempty"`
	CheckoutSessionTerminalAt *time.Time      `json:"checkout_session_terminal_at,omitempty"`
	TeamID                    string          `json:"team_id"`
	PlanID                    string          `json:"plan_id"`
	IdempotencyKey            string          `json:"idempotency_key"`
	ClientIP                  string          `json:"client_ip"`
	ClientIPSource            string          `json:"client_ip_source"`
	RemoteAddr                string          `json:"remote_addr"`
	TrustedProxy              bool            `json:"trusted_proxy"`
	UserAgent                 string          `json:"user_agent"`
	UserAgentSHA256           string          `json:"user_agent_sha256"`
	FirstClientIP             string          `json:"first_client_ip"`
	FirstClientIPSource       string          `json:"first_client_ip_source"`
	FirstRemoteAddr           string          `json:"first_remote_addr"`
	FirstTrustedProxy         bool            `json:"first_trusted_proxy"`
	FirstUserAgent            string          `json:"first_user_agent"`
	FirstUserAgentSHA256      string          `json:"first_user_agent_sha256"`
	FirstRecordedAt           *time.Time      `json:"first_recorded_at,omitempty"`
	EvidenceAttempts          int             `json:"evidence_attempts"`
	IdentityRedactedAt        *time.Time      `json:"identity_redacted_at,omitempty"`
	Metadata                  json.RawMessage `json:"metadata"`
	CreatedAt                 time.Time       `json:"created_at"`
	UpdatedAt                 time.Time       `json:"updated_at"`
}

type PrivacyExportPolicyAcceptance struct {
	TeamID        string          `json:"team_id"`
	PolicyKind    string          `json:"policy_kind"`
	PolicyVersion string          `json:"policy_version"`
	PolicyHash    string          `json:"policy_hash"`
	AcceptedAt    time.Time       `json:"accepted_at"`
	IPAddress     string          `json:"ip_address"`
	UserAgent     string          `json:"user_agent"`
	Provider      string          `json:"provider"`
	Metadata      json.RawMessage `json:"metadata"`
}

type PrivacyExportRunGroup struct {
	RunGroupID string    `json:"run_group_id"`
	TeamID     string    `json:"team_id"`
	ProjectID  string    `json:"project_id"`
	Kind       string    `json:"kind"`
	Name       string    `json:"name"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type PrivacyExportPolicy struct {
	PolicyID    string    `json:"policy_id"`
	TeamID      string    `json:"team_id"`
	ProjectID   string    `json:"project_id"`
	Revision    int64     `json:"revision"`
	Digest      string    `json:"digest"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	ActivatedAt time.Time `json:"activated_at"`
}

type PrivacyExportVerificationCurrentness struct {
	SessionID              string    `json:"session_id"`
	TeamID                 string    `json:"team_id"`
	ActorKey               string    `json:"actor_key"`
	ActorKind              string    `json:"actor_kind"`
	State                  string    `json:"state"`
	ObservedSnapshotDigest string    `json:"observed_snapshot_digest"`
	ObservedAt             time.Time `json:"observed_at"`
}

type PrivacyExportAuditEvent struct {
	TeamID     string          `json:"team_id"`
	Action     string          `json:"action"`
	TargetType string          `json:"target_type"`
	TargetID   string          `json:"target_id"`
	Metadata   json.RawMessage `json:"metadata"`
	CreatedAt  time.Time       `json:"created_at"`
}

type PrivacyExportProjectSecretDeletion struct {
	ID               int64     `json:"id"`
	TeamID           string    `json:"team_id"`
	ProjectID        string    `json:"project_id"`
	Name             string    `json:"name"`
	DeletedByUserID  string    `json:"deleted_by_user_id"`
	DeletedByTokenID string    `json:"deleted_by_token_id"`
	Reason           string    `json:"reason"`
	DeletedAt        time.Time `json:"deleted_at"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type AdminPrivacyExport struct {
	SchemaVersion     string                                 `json:"schema_version"`
	CaseID            string                                 `json:"case_id"`
	GeneratedAt       time.Time                              `json:"generated_at"`
	Subject           PrivacyExportSubject                   `json:"subject"`
	Cases             []PrivacyExportCase                    `json:"privacy_cases"`
	Memberships       []PrivacyExportMembership              `json:"memberships"`
	Tokens            []APITokenSummary                      `json:"tokens"`
	Identities        []PrivacyExportIdentity                `json:"identities"`
	BrowserSessions   []PrivacyExportBrowserSession          `json:"browser_sessions"`
	Feedback          []FeedbackRecord                       `json:"feedback"`
	Invitations       []TeamInvitationSummary                `json:"invitations"`
	PrepaidOrders     []PrepaidOrderSummary                  `json:"prepaid_orders"`
	BillingCheckouts  []PrivacyExportBillingCheckout         `json:"billing_checkouts"`
	PolicyAcceptances []PrivacyExportPolicyAcceptance        `json:"policy_acceptances"`
	RunGroups         []PrivacyExportRunGroup                `json:"run_groups"`
	Policies          []PrivacyExportPolicy                  `json:"verification_policies"`
	Currentness       []PrivacyExportVerificationCurrentness `json:"verification_currentness"`
	AuditEvents       []PrivacyExportAuditEvent              `json:"audit_events"`
	SecretDeletions   []PrivacyExportProjectSecretDeletion   `json:"project_secret_deletions"`
	Manifest          map[string]int64                       `json:"manifest"`
	Digest            string                                 `json:"digest"`
}
