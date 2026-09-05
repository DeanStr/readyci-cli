package preflightapi

import (
	"encoding/json"
	"time"
)

type AdminQueueStatusResponse struct {
	AwaitingUploadRuns             int       `json:"awaiting_upload_runs"`
	StaleAwaitingUploadRuns        int       `json:"stale_awaiting_upload_runs"`
	OldestAwaitingUploadAgeSeconds int64     `json:"oldest_awaiting_upload_age_seconds"`
	QueuedRuns                     int       `json:"queued_runs"`
	CapacityWaitingRuns            int       `json:"capacity_waiting_runs"`
	ScheduledRuns                  int       `json:"scheduled_runs"`
	StartingVMRuns                 int       `json:"starting_vm_runs"`
	RunningRuns                    int       `json:"running_runs"`
	BlockedRuns                    int       `json:"blocked_runs"`
	DeniedRuns                     int       `json:"denied_runs"`
	StaleLeases                    int       `json:"stale_leases"`
	FreshWorkers                   int       `json:"fresh_workers"`
	StaleWorkers                   int       `json:"stale_workers"`
	DrainingWorkers                int       `json:"draining_workers"`
	PlatformEvents                 int64     `json:"platform_events"`
	PlatformDiscards               int64     `json:"platform_event_discards"`
	PlatformErrors                 int64     `json:"platform_event_errors"`
	UpdatedAt                      time.Time `json:"updated_at"`
}

type AdminQueueExplainResponse struct {
	RunID            string                         `json:"run_id"`
	ProjectID        string                         `json:"project_id"`
	Status           string                         `json:"status"`
	QueueState       string                         `json:"queue_state"`
	WorkerID         string                         `json:"worker_id,omitempty"`
	QueuedAt         *time.Time                     `json:"queued_at,omitempty"`
	LeaseExpiresAt   *time.Time                     `json:"lease_expires_at,omitempty"`
	Reasons          []string                       `json:"reasons,omitempty"`
	WorkerCapability *WorkerCapabilityCheckResponse `json:"worker_capability,omitempty"`
}

type AdminTeamSummary struct {
	ID                  string     `json:"id"`
	Name                string     `json:"name"`
	Plan                string     `json:"plan"`
	DisabledAt          *time.Time `json:"disabled_at,omitempty"`
	DisabledReason      string     `json:"disabled_reason,omitempty"`
	BillingHoldAt       *time.Time `json:"billing_hold_at,omitempty"`
	BillingHoldReason   string     `json:"billing_hold_reason,omitempty"`
	BillingHoldProvider string     `json:"billing_hold_provider,omitempty"`
	BillingHoldEventID  string     `json:"billing_hold_provider_event_id,omitempty"`
	AwaitingUploadRuns  int        `json:"awaiting_upload_runs"`
	QueuedRuns          int        `json:"queued_runs"`
	ActiveRuns          int        `json:"active_runs"`
	ActiveAPITokens     int        `json:"active_api_tokens"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

type AdminListTeamsResponse struct {
	Teams []AdminTeamSummary `json:"teams"`
}

// AdminProjectSummary is intentionally small: it backs the operator console's
// project-scope autocomplete, which needs IDs and their owning teams, not the
// full tenant-scoped ProjectSummary.
type AdminProjectSummary struct {
	ProjectID string `json:"project_id"`
	TeamID    string `json:"team_id"`
}

type AdminListProjectsResponse struct {
	Projects []AdminProjectSummary `json:"projects"`
}

type AdminTeamMemberSummary struct {
	UserID         string     `json:"user_id"`
	Email          string     `json:"email"`
	Name           string     `json:"name"`
	Role           string     `json:"role"`
	RemovedAt      *time.Time `json:"removed_at,omitempty"`
	UserDisabledAt *time.Time `json:"user_disabled_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

type AdminTeamDetailResponse struct {
	Team    AdminTeamSummary         `json:"team"`
	Members []AdminTeamMemberSummary `json:"members"`
	Tokens  []APITokenSummary        `json:"tokens"`
}

type AdminDisableTeamRequest struct {
	Reason string `json:"reason,omitempty"`
}

type AdminDisableTeamResponse struct {
	Team          AdminTeamSummary `json:"team"`
	CancelledRuns []string         `json:"cancelled_runs"`
}

type AdminReleaseBillingHoldRequest struct {
	Reason string `json:"reason,omitempty"`
}

type AdminReleaseBillingHoldResponse struct {
	Team AdminTeamSummary `json:"team"`
}

type AdminCancelTeamRunsRequest struct {
	Reason string `json:"reason,omitempty"`
}

type AdminCancelTeamRunsResponse struct {
	TeamID        string   `json:"team_id"`
	CancelledRuns []string `json:"cancelled_runs"`
}

type BillingDisputeEvidenceResponse struct {
	Provider    string          `json:"provider"`
	DisputeID   string          `json:"dispute_id"`
	TeamID      string          `json:"team_id,omitempty"`
	GeneratedAt time.Time       `json:"generated_at"`
	Packet      json.RawMessage `json:"packet"`
}

type BillingRiskCaseSummary struct {
	CaseID            string     `json:"case_id"`
	Kind              string     `json:"kind"`
	Provider          string     `json:"provider"`
	TeamID            string     `json:"team_id,omitempty"`
	TeamName          string     `json:"team_name,omitempty"`
	EventType         string     `json:"event_type,omitempty"`
	Status            string     `json:"status,omitempty"`
	Reason            string     `json:"reason,omitempty"`
	Actionable        bool       `json:"actionable,omitempty"`
	AmountCents       int64      `json:"amount_cents,omitempty"`
	Currency          string     `json:"currency,omitempty"`
	CustomerID        string     `json:"customer_id,omitempty"`
	SubscriptionID    string     `json:"subscription_id,omitempty"`
	InvoiceID         string     `json:"invoice_id,omitempty"`
	ChargeID          string     `json:"charge_id,omitempty"`
	PaymentIntentID   string     `json:"payment_intent_id,omitempty"`
	EvidenceDueBy     *time.Time `json:"evidence_due_by,omitempty"`
	ProviderCreatedAt *time.Time `json:"provider_created_at,omitempty"`
	ProviderUpdatedAt *time.Time `json:"provider_updated_at,omitempty"`
	LastEventID       string     `json:"last_event_id,omitempty"`
	BillingHoldAt     *time.Time `json:"billing_hold_at,omitempty"`
	BillingHoldReason string     `json:"billing_hold_reason,omitempty"`
	Notes             int        `json:"notes"`
	SupportMessages   int        `json:"support_messages"`
	RecommendedAction string     `json:"recommended_action"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type AdminListBillingRiskCasesResponse struct {
	Cases      []BillingRiskCaseSummary `json:"cases"`
	HasMore    bool                     `json:"has_more"`
	NextCursor string                   `json:"next_cursor,omitempty"`
}

type BillingDisputeNote struct {
	ID                int64           `json:"id"`
	Provider          string          `json:"provider"`
	DisputeID         string          `json:"dispute_id"`
	TeamID            string          `json:"team_id,omitempty"`
	Note              string          `json:"note"`
	CreatedBy         string          `json:"created_by,omitempty"`
	Metadata          json.RawMessage `json:"metadata,omitempty"`
	ContentRedactedAt *time.Time      `json:"content_redacted_at,omitempty"`
	CreatedAt         time.Time       `json:"created_at"`
}

type BillingSupportMessage struct {
	ID                     int64           `json:"id"`
	TeamID                 string          `json:"team_id,omitempty"`
	Provider               string          `json:"provider"`
	DisputeID              string          `json:"dispute_id,omitempty"`
	RiskEventID            string          `json:"risk_event_id,omitempty"`
	ExternalSystem         string          `json:"external_system,omitempty"`
	ExternalConversationID string          `json:"external_conversation_id,omitempty"`
	ExternalMessageID      string          `json:"external_message_id,omitempty"`
	Direction              string          `json:"direction,omitempty"`
	AuthorEmail            string          `json:"author_email,omitempty"`
	Body                   string          `json:"body"`
	ReceivedAt             time.Time       `json:"received_at"`
	Metadata               json.RawMessage `json:"metadata,omitempty"`
	ContentRedactedAt      *time.Time      `json:"content_redacted_at,omitempty"`
	CreatedAt              time.Time       `json:"created_at"`
}

type AdminBillingRiskCaseDetailResponse struct {
	Case                         BillingRiskCaseSummary          `json:"case"`
	Dispute                      json.RawMessage                 `json:"dispute,omitempty"`
	RiskEvent                    json.RawMessage                 `json:"risk_event,omitempty"`
	PaymentEvents                []json.RawMessage               `json:"payment_events,omitempty"`
	RelatedCases                 []BillingRiskCaseSummary        `json:"related_cases,omitempty"`
	Notes                        []BillingDisputeNote            `json:"notes,omitempty"`
	SupportMessages              []BillingSupportMessage         `json:"support_messages,omitempty"`
	CaseSupportMessages          []BillingSupportMessage         `json:"case_support_messages,omitempty"`
	TeamContextMessages          []BillingSupportMessage         `json:"team_context_messages,omitempty"`
	Evidence                     *BillingDisputeEvidenceResponse `json:"evidence,omitempty"`
	CancelledRuns                []string                        `json:"cancelled_runs,omitempty"`
	RelatedPaymentEventsLinked   int                             `json:"related_payment_events_linked,omitempty"`
	RelatedWebhookEventsLinked   int                             `json:"related_webhook_events_linked,omitempty"`
	ManualLinkSourceIdentifiers  []string                        `json:"manual_link_source_identifiers,omitempty"`
	ManualLinkMatchedIdentifiers []string                        `json:"manual_link_matched_identifiers,omitempty"`
}

type AdminLinkBillingRiskCaseRequest struct {
	TeamID             string `json:"team_id"`
	Reason             string `json:"reason,omitempty"`
	ConfirmBulkLink    bool   `json:"confirm_bulk_link,omitempty"`
	IncludeTeamContext bool   `json:"include_team_context,omitempty"`
}

type AdminCreateBillingDisputeNoteRequest struct {
	Note      string          `json:"note"`
	CreatedBy string          `json:"created_by,omitempty"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
}

type AdminCreateBillingDisputeNoteResponse struct {
	Note BillingDisputeNote `json:"note"`
}

type AdminRecordBillingSupportMessageRequest struct {
	TeamID                 string          `json:"team_id,omitempty"`
	Provider               string          `json:"provider,omitempty"`
	DisputeID              string          `json:"dispute_id,omitempty"`
	RiskEventID            string          `json:"risk_event_id,omitempty"`
	ExternalSystem         string          `json:"external_system,omitempty"`
	ExternalConversationID string          `json:"external_conversation_id,omitempty"`
	ExternalMessageID      string          `json:"external_message_id,omitempty"`
	Direction              string          `json:"direction,omitempty"`
	AuthorEmail            string          `json:"author_email,omitempty"`
	Body                   string          `json:"body"`
	ReceivedAt             *time.Time      `json:"received_at,omitempty"`
	Metadata               json.RawMessage `json:"metadata,omitempty"`
}

type AdminRecordBillingSupportMessageResponse struct {
	Message BillingSupportMessage `json:"message"`
}

type AdminRedactBillingEvidenceRequest struct {
	RawPayloadMinAgeHours int64 `json:"raw_payload_min_age_hours,omitempty"`
	IdentityMinAgeHours   int64 `json:"identity_min_age_hours,omitempty"`
	Limit                 int   `json:"limit,omitempty"`
	DryRun                bool  `json:"dry_run,omitempty"`
}

type AdminRedactBillingEvidenceResponse struct {
	RawDisputesRedacted        int `json:"raw_disputes_redacted"`
	RawPaymentEventsRedacted   int `json:"raw_payment_events_redacted"`
	RawRiskEventsRedacted      int `json:"raw_risk_events_redacted"`
	RawEvidencePacketsRedacted int `json:"raw_evidence_packets_redacted"`
	CheckoutEvidenceRedacted   int `json:"checkout_evidence_redacted"`
	PolicyAcceptancesRedacted  int `json:"policy_acceptances_redacted"`
	EvidencePacketsRedacted    int `json:"evidence_packets_redacted"`
	SupportMessagesRedacted    int `json:"support_messages_redacted"`
	DisputeNotesRedacted       int `json:"dispute_notes_redacted"`
}

type BillingEvidenceExportFile struct {
	FieldName string `json:"field_name"`
	Filename  string `json:"filename"`
	MimeType  string `json:"mime_type"`
	Content   string `json:"content"`
}

type AdminBillingDisputeEvidenceExportResponse struct {
	Provider         string                      `json:"provider"`
	DisputeID        string                      `json:"dispute_id"`
	TeamID           string                      `json:"team_id,omitempty"`
	GeneratedAt      time.Time                   `json:"generated_at"`
	ProviderEvidence json.RawMessage             `json:"provider_evidence"`
	Files            []BillingEvidenceExportFile `json:"files,omitempty"`
}

type AdminStorageAuditResponse struct {
	UploadStoreRoot          string   `json:"upload_store_root"`
	ArtifactStoreRoot        string   `json:"artifact_store_root"`
	AwaitingUploadRuns       int      `json:"awaiting_upload_runs"`
	QueuedRuns               int      `json:"queued_runs"`
	ArtifactRecords          int      `json:"artifact_records"`
	ExpiredArtifactRecords   int      `json:"expired_artifact_records"`
	MalformedArtifactRecords int      `json:"malformed_artifact_records"`
	TerminalRunsWithBundles  int      `json:"terminal_runs_with_bundles"`
	RequeuedArtifactRecords  int      `json:"requeued_artifact_records"`
	QueuedWorkerLocalBundles int      `json:"queued_worker_local_bundles"`
	PendingCacheLaneDeletes  int      `json:"pending_cache_lane_deletes"`
	WorkersNotSeenRecently   int      `json:"workers_not_seen_recently"`
	Notes                    []string `json:"notes,omitempty"`
}

type AdminCleanupExpiredArtifactsRequest struct {
	DryRun                    bool  `json:"dry_run,omitempty"`
	Limit                     int   `json:"limit,omitempty"`
	MinAgeSeconds             int64 `json:"min_age_seconds,omitempty"`
	IncludeMalformedArtifacts bool  `json:"include_malformed_artifacts,omitempty"`
	IncludeStaleUploads       bool  `json:"include_stale_uploads,omitempty"`
	IncludeRunLogs            bool  `json:"include_run_logs,omitempty"`
	LogMinAgeSeconds          int64 `json:"log_min_age_seconds,omitempty"`
}

type AdminCleanupCacheLanesRequest struct {
	DryRun                    bool  `json:"dry_run,omitempty"`
	Limit                     int   `json:"limit,omitempty"`
	MinAgeSeconds             int64 `json:"min_age_seconds,omitempty"`
	NonHomeMinAgeSeconds      int64 `json:"non_home_min_age_seconds,omitempty"`
	KeepLanesPerProjectRegion int   `json:"keep_lanes_per_project_region,omitempty"`
}

type MaintenanceJobSummary struct {
	ID               string            `json:"id"`
	Type             string            `json:"type"`
	Status           string            `json:"status"`
	DryRun           bool              `json:"dry_run"`
	StartedAt        time.Time         `json:"started_at"`
	FinishedAt       *time.Time        `json:"finished_at,omitempty"`
	ProgressCurrent  int               `json:"progress_current"`
	ProgressTotal    int               `json:"progress_total"`
	DeletedArtifacts int               `json:"deleted_artifacts"`
	DeletedObjects   int               `json:"deleted_objects"`
	FailedObjects    int               `json:"failed_objects"`
	Error            string            `json:"error,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
}

type AdminCleanupExpiredArtifactsResponse struct {
	DryRun                    bool                   `json:"dry_run"`
	ExpiredArtifacts          int                    `json:"expired_artifacts"`
	MalformedArtifactRecords  int                    `json:"malformed_artifact_records"`
	ExpiredUploads            int                    `json:"expired_uploads"`
	ExpiredLogEvents          int                    `json:"expired_log_events"`
	DeletedArtifacts          int                    `json:"deleted_artifacts"`
	DeletedMalformedArtifacts int                    `json:"deleted_malformed_artifacts"`
	DeletedObjects            int                    `json:"deleted_objects"`
	DeletedUploads            int                    `json:"deleted_uploads"`
	DeletedLogEvents          int                    `json:"deleted_log_events"`
	FailedObjects             []string               `json:"failed_objects,omitempty"`
	Artifacts                 []ArtifactSummary      `json:"artifacts,omitempty"`
	Uploads                   []UploadObjectSummary  `json:"uploads,omitempty"`
	Job                       *MaintenanceJobSummary `json:"job,omitempty"`
}

type CacheLaneCleanupCandidate struct {
	ID         string     `json:"id"`
	TeamID     string     `json:"team_id,omitempty"`
	ProjectID  string     `json:"project_id"`
	WorkerID   string     `json:"worker_id"`
	Region     string     `json:"region"`
	HomeRegion string     `json:"home_region"`
	LaneIndex  int        `json:"lane_index"`
	Status     string     `json:"status"`
	QuotaBytes int64      `json:"quota_bytes"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	Reason     string     `json:"reason"`
}

type AdminCleanupCacheLanesResponse struct {
	DryRun       bool                        `json:"dry_run"`
	Candidates   int                         `json:"candidates"`
	DeletedLanes int                         `json:"deleted_lanes"`
	Lanes        []CacheLaneCleanupCandidate `json:"lanes,omitempty"`
}

type UploadObjectSummary struct {
	RunID     string    `json:"run_id"`
	ObjectKey string    `json:"object_key"`
	SizeBytes int64     `json:"size_bytes"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type AdminCleanupExpiredArtifactsStatusResponse struct {
	Job *MaintenanceJobSummary `json:"job,omitempty"`
}

type AdminCancelCleanupExpiredArtifactsRequest struct {
	Reason string `json:"reason,omitempty"`
}

type AdminCancelCleanupExpiredArtifactsResponse struct {
	Job *MaintenanceJobSummary `json:"job,omitempty"`
}
