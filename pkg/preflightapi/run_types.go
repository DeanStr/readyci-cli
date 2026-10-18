package preflightapi

import (
	"encoding/base64"
	"time"
)

type ClientInfo struct {
	Version  string `json:"version"`
	Platform string `json:"platform"`
}

type GitInfo struct {
	Branch          string `json:"branch,omitempty"`
	Head            string `json:"head,omitempty"`
	Dirty           bool   `json:"dirty"`
	Materialization string `json:"materialization,omitempty"`
	Repository      string `json:"repository,omitempty"`
}

type CreateRunRequest struct {
	ProjectID             string            `json:"project_id"`
	Region                string            `json:"region,omitempty"`
	IdempotencyKey        string            `json:"idempotency_key,omitempty"`
	RunGroupKey           string            `json:"run_group_key,omitempty"`
	RunGroupKind          string            `json:"run_group_kind,omitempty"`
	RunGroupName          string            `json:"run_group_name,omitempty"`
	RunGroupPredecessorID string            `json:"run_group_predecessor_id,omitempty"`
	LogicalChildID        string            `json:"logical_child_id,omitempty"`
	LogicalChildName      string            `json:"logical_child_name,omitempty"`
	MatrixVariant         string            `json:"matrix_variant,omitempty"`
	Attempt               int               `json:"attempt,omitempty"`
	Image                 string            `json:"image"`
	Command               string            `json:"command"`
	CommandAlias          string            `json:"command_alias,omitempty"`
	Steps                 []RunStepSpec     `json:"steps,omitempty"`
	ConcurrencyGroup      string            `json:"concurrency_group,omitempty"`
	ConcurrencyLimit      int               `json:"concurrency_limit,omitempty"`
	RunnerSize            string            `json:"runner_size"`
	TimeoutSeconds        int               `json:"timeout_seconds"`
	CacheMode             string            `json:"cache_mode"`
	CacheLaneSizeBytes    int64             `json:"cache_lane_size_bytes,omitempty"`
	ArtifactPaths         []string          `json:"artifact_paths,omitempty"`
	NetworkMode           string            `json:"network_mode"`
	NetworkAllowlist      []string          `json:"network_allowlist,omitempty"`
	Docker                bool              `json:"docker"`
	Services              []RunServiceSpec  `json:"services,omitempty"`
	WorkerSelector        map[string]string `json:"worker_selector,omitempty"`
	ChangeMode            string            `json:"change_mode,omitempty"`
	SnapshotDigest        string            `json:"snapshot_digest,omitempty"`
	ChangedFilesDigest    string            `json:"changed_files_digest,omitempty"`
	BundleBaselineKey     string            `json:"bundle_baseline_key,omitempty"`
	SecretsRequested      *bool             `json:"secrets_requested,omitempty"`
	Client                ClientInfo        `json:"client"`
	Git                   GitInfo           `json:"git"`
}

type RunServiceSpec struct {
	Name    string               `json:"name"`
	Type    string               `json:"type"`
	Version string               `json:"version,omitempty"`
	Image   string               `json:"image,omitempty"`
	Ports   []string             `json:"ports,omitempty"`
	Env     map[string]string    `json:"env,omitempty"`
	Health  RunServiceHealthSpec `json:"health"`
	Logs    []string             `json:"logs,omitempty"`
}

type RunStepSpec struct {
	Name           string            `json:"name"`
	Command        string            `json:"command"`
	Always         bool              `json:"always,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
}

type RunServiceHealthSpec struct {
	Command  string `json:"command"`
	Timeout  string `json:"timeout,omitempty"`
	Interval string `json:"interval,omitempty"`
}

type CreateRunResponse struct {
	RunID                  string            `json:"run_id"`
	RunGroupID             string            `json:"run_group_id,omitempty"`
	UploadURL              string            `json:"upload_url"`
	UploadMethod           string            `json:"upload_method,omitempty"`
	UploadHeaders          map[string]string `json:"upload_headers,omitempty"`
	UploadWorkerID         string            `json:"upload_worker_id,omitempty"`
	BundleBaselineReusable bool              `json:"bundle_baseline_reusable,omitempty"`
	EventsURL              string            `json:"events_url"`
	Status                 string            `json:"status"`
}

type CompleteUploadResponse struct {
	RunID  string `json:"run_id"`
	Status string `json:"status"`
}

type CompleteUploadRequest struct {
	WorkerUploadProof *WorkerBundleUploadProof `json:"worker_upload_proof,omitempty"`
}

type RunSummary struct {
	RunID            string `json:"run_id"`
	RunGroupID       string `json:"run_group_id,omitempty"`
	TeamID           string `json:"team_id,omitempty"`
	ProjectID        string `json:"project_id"`
	Region           string `json:"region"`
	Command          string `json:"command"`
	CommandAlias     string `json:"command_alias,omitempty"`
	ConcurrencyGroup string `json:"concurrency_group,omitempty"`
	ConcurrencyLimit int    `json:"concurrency_limit,omitempty"`
	Status           string `json:"status"`
	WorkerID         string `json:"worker_id,omitempty"`
	// Git identifies the revision a run checked, so a list of runs can be read
	// without fetching each run's spec. Ready CI runs pre-push working trees, so
	// Dirty is part of identifying the revision, not an aside.
	Git            GitInfo    `json:"git,omitempty"`
	RunnerSize     string     `json:"runner_size"`
	NetworkMode    string     `json:"network_mode"`
	Docker         bool       `json:"docker"`
	CacheMode      string     `json:"cache_mode"`
	CacheLaneID    string     `json:"cache_lane_id,omitempty"`
	CacheLaneIndex *int       `json:"cache_lane_index,omitempty"`
	ExitCode       *int       `json:"exit_code,omitempty"`
	Classification string     `json:"classification,omitempty"`
	Retryable      bool       `json:"retryable"`
	AgentAction    string     `json:"agent_action,omitempty"`
	FailureSummary string     `json:"failure_summary,omitempty"`
	PrimaryFile    string     `json:"primary_file,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	QueuedAt       *time.Time `json:"queued_at,omitempty"`
	ClaimedAt      *time.Time `json:"claimed_at,omitempty"`
	LeaseExpiresAt *time.Time `json:"lease_expires_at,omitempty"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	EventCount     int        `json:"event_count"`
}

type RunDetail struct {
	Summary       RunSummary                 `json:"summary"`
	Spec          RunSpec                    `json:"spec"`
	SecretPolicy  *RunSecretPolicyEvaluation `json:"secret_policy,omitempty"`
	PolicyReasons []string                   `json:"policy_reasons,omitempty"`
	TestResults   *RunTestResults            `json:"test_results,omitempty"`
	Timings       *RunTimings                `json:"timings,omitempty"`
}

type RunSecretPolicyEvaluation struct {
	Requested   bool     `json:"requested"`
	Eligible    bool     `json:"eligible"`
	CommandHash string   `json:"command_hash,omitempty"`
	Reasons     []string `json:"reasons,omitempty"`
}

type RunGroupSummary struct {
	RunGroupID         string     `json:"run_group_id"`
	TaskID             string     `json:"task_id,omitempty"`
	ProjectID          string     `json:"project_id"`
	Kind               string     `json:"kind"`
	Name               string     `json:"name,omitempty"`
	Status             string     `json:"status"`
	ChildCount         int        `json:"child_count"`
	LatestAttemptCount int        `json:"latest_attempt_count"`
	FailedChildCount   int        `json:"failed_child_count"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	SuccessorID        string     `json:"successor_id,omitempty"`
	PredecessorID      string     `json:"predecessor_id,omitempty"`
	CompletedAt        *time.Time `json:"completed_at,omitempty"`
}

type RunGroupChild struct {
	RunID                 string                 `json:"run_id"`
	OriginalRunID         string                 `json:"original_run_id,omitempty"`
	RetryOfRunID          string                 `json:"retry_of_run_id,omitempty"`
	LogicalChildID        string                 `json:"logical_child_id"`
	LogicalChildName      string                 `json:"logical_child_name,omitempty"`
	CommandAlias          string                 `json:"command_alias,omitempty"`
	MatrixVariant         string                 `json:"matrix_variant,omitempty"`
	Attempt               int                    `json:"attempt"`
	LatestAttempt         bool                   `json:"latest_attempt"`
	Status                string                 `json:"status"`
	ExitCode              *int                   `json:"exit_code,omitempty"`
	Classification        string                 `json:"classification,omitempty"`
	Retryable             bool                   `json:"retryable"`
	AgentAction           string                 `json:"agent_action,omitempty"`
	FailureSummary        string                 `json:"failure_summary,omitempty"`
	PolicyReasons         []string               `json:"policy_reasons,omitempty"`
	ArtifactCount         int                    `json:"artifact_count"`
	ArtifactDigest        string                 `json:"artifact_digest,omitempty"`
	RetainedArtifactCount int                    `json:"retained_artifact_count"`
	ArtifactsAvailable    bool                   `json:"artifacts_available"`
	EvidenceAttested      bool                   `json:"evidence_attested"`
	EvidenceDigest        string                 `json:"evidence_digest,omitempty"`
	EvidenceReceipt       *RunnerEvidenceReceipt `json:"evidence_receipt,omitempty"`
	CreatedAt             time.Time              `json:"created_at"`
	QueuedAt              *time.Time             `json:"queued_at,omitempty"`
	ClaimedAt             *time.Time             `json:"claimed_at,omitempty"`
	LeaseExpiresAt        *time.Time             `json:"lease_expires_at,omitempty"`
	FinishedAt            *time.Time             `json:"finished_at,omitempty"`
}

type RunGroupDetail struct {
	Summary  RunGroupSummary `json:"summary"`
	Children []RunGroupChild `json:"children"`
}

type ListRunGroupsResponse struct {
	Groups []RunGroupSummary `json:"groups"`
}

type RunDetailsResponse struct {
	Runs []RunDetail `json:"runs"`
}

type ListRunsResponse struct {
	Runs           []RunSummary `json:"runs"`
	NextCursor     int64        `json:"next_cursor,omitempty"`
	NextPageCursor string       `json:"next_page_cursor,omitempty"`
	HasMore        bool         `json:"has_more,omitempty"`
}

type DashboardSeriesPoint struct {
	Date  string `json:"date"`
	Value int64  `json:"value"`
}

type ArtifactTypeAggregate struct {
	Type      string `json:"type"`
	Count     int64  `json:"count"`
	SizeBytes int64  `json:"size_bytes"`
}

type CustomerRunSummaryResponse struct {
	ProjectID             string                  `json:"project_id,omitempty"`
	TotalRuns             int64                   `json:"total_runs"`
	ActiveRuns            int64                   `json:"active_runs"`
	TerminalRuns          int64                   `json:"terminal_runs"`
	SucceededRuns         int64                   `json:"succeeded_runs"`
	RunsByDay             []DashboardSeriesPoint  `json:"runs_by_day"`
	TerminalRunsByDay     []DashboardSeriesPoint  `json:"terminal_runs_by_day"`
	SucceededByDay        []DashboardSeriesPoint  `json:"succeeded_by_day"`
	RetainedArtifactCount int64                   `json:"retained_artifact_count"`
	RetainedArtifactBytes int64                   `json:"retained_artifact_bytes"`
	ArtifactBytesByDay    []DashboardSeriesPoint  `json:"artifact_bytes_by_day"`
	ArtifactTypes         []ArtifactTypeAggregate `json:"artifact_types"`
}

type RunEventsResponse struct {
	RunID      string     `json:"run_id"`
	Events     []RunEvent `json:"events"`
	NextCursor int64      `json:"next_cursor,omitempty"`
	HasMore    bool       `json:"has_more,omitempty"`
	Truncated  bool       `json:"truncated,omitempty"`
}

// MaxRunEventsTailLimit is the largest single-run event tail accepted by the API.
const MaxRunEventsTailLimit = 200

type BatchRunEventsResponse struct {
	Runs []RunEventsResponse `json:"runs"`
}

type RunGroupEventsResponse struct {
	RunGroupID string              `json:"run_group_id"`
	Cursor     int64               `json:"cursor"`
	Runs       []RunEventsResponse `json:"runs"`
	Truncated  bool                `json:"truncated,omitempty"`
}

type ArtifactSummary struct {
	ID            string    `json:"id"`
	RunID         string    `json:"run_id"`
	ProjectID     string    `json:"project_id,omitempty"`
	Name          string    `json:"name"`
	Path          string    `json:"path"`
	ObjectKey     string    `json:"-"`
	SizeBytes     int64     `json:"size_bytes"`
	ContentSHA256 string    `json:"content_sha256"`
	DownloadURL   string    `json:"download_url,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	ExpiresAt     time.Time `json:"expires_at"`
	// Service-derived index, persisted atomically with this exact archive.
	TestReportIndex *TestReportIndex `json:"-"`
}

type ListArtifactsResponse struct {
	RunID     string            `json:"run_id"`
	Artifacts []ArtifactSummary `json:"artifacts"`
}

type BatchArtifactsResponse struct {
	Runs []ListArtifactsResponse `json:"runs"`
}

type ArtifactIndexResponse struct {
	Artifacts      []ArtifactSummary `json:"artifacts"`
	NextPageCursor string            `json:"next_page_cursor,omitempty"`
	HasMore        bool              `json:"has_more"`
	TotalCount     int64             `json:"total_count"`
	TotalSizeBytes int64             `json:"total_size_bytes"`
}

type UploadArtifactResponse struct {
	Artifact ArtifactSummary `json:"artifact"`
}

type RunStatusResponse struct {
	RunID  string `json:"run_id"`
	Status string `json:"status"`
}

type CancelRunRequest struct {
	Reason string `json:"reason,omitempty"`
}

type CancelRunResponse struct {
	RunID  string `json:"run_id"`
	Status string `json:"status"`
}

type CancelRunGroupRequest struct {
	Reason string `json:"reason,omitempty"`
}

type CancelRunGroupResponse struct {
	RunGroupID    string         `json:"run_group_id"`
	CancelledRuns []string       `json:"cancelled_runs"`
	Detail        RunGroupDetail `json:"detail"`
}

type RunSpec struct {
	RunID              string            `json:"run_id"`
	TeamID             string            `json:"team_id,omitempty"`
	RunGroupID         string            `json:"run_group_id,omitempty"`
	RunGroupKey        string            `json:"run_group_key,omitempty"`
	RunGroupKind       string            `json:"run_group_kind,omitempty"`
	RunGroupName       string            `json:"run_group_name,omitempty"`
	LogicalChildID     string            `json:"logical_child_id,omitempty"`
	LogicalChildName   string            `json:"logical_child_name,omitempty"`
	MatrixVariant      string            `json:"matrix_variant,omitempty"`
	Attempt            int               `json:"attempt,omitempty"`
	ProjectID          string            `json:"project_id"`
	Region             string            `json:"region"`
	IdempotencyKey     string            `json:"idempotency_key,omitempty"`
	Image              string            `json:"image"`
	Command            string            `json:"command"`
	CommandAlias       string            `json:"command_alias,omitempty"`
	Steps              []RunStepSpec     `json:"steps,omitempty"`
	ConcurrencyGroup   string            `json:"concurrency_group,omitempty"`
	ConcurrencyLimit   int               `json:"concurrency_limit,omitempty"`
	RunnerSize         string            `json:"runner_size"`
	TimeoutSeconds     int               `json:"timeout_seconds"`
	CacheMode          string            `json:"cache_mode"`
	ArtifactPaths      []string          `json:"artifact_paths,omitempty"`
	NetworkMode        string            `json:"network_mode"`
	NetworkAllowlist   []string          `json:"network_allowlist,omitempty"`
	Docker             bool              `json:"docker"`
	Services           []RunServiceSpec  `json:"services,omitempty"`
	WorkerSelector     map[string]string `json:"worker_selector,omitempty"`
	ChangeMode         string            `json:"change_mode,omitempty"`
	SnapshotDigest     string            `json:"snapshot_digest,omitempty"`
	ChangedFilesDigest string            `json:"changed_files_digest,omitempty"`
	BundleBaselineKey  string            `json:"bundle_baseline_key,omitempty"`
	SecretsRequested   *bool             `json:"secrets_requested,omitempty"`
	// SecretRevisionDigest identifies the exact server-selected project-secret
	// revision set without exposing secret names or values. It is populated by
	// the control plane when a worker claim is finalized, never by run creation.
	SecretRevisionDigest string            `json:"secret_revision_digest,omitempty"`
	Git                  GitInfo           `json:"git,omitempty"`
	ClaimToken           string            `json:"claim_token,omitempty"`
	LeaseExpiresAt       *time.Time        `json:"lease_expires_at,omitempty"`
	WorkerReleaseState   string            `json:"worker_release_state,omitempty"`
	WorkerReleaseDigest  string            `json:"worker_release_digest,omitempty"`
	CacheLaneID          string            `json:"cache_lane_id,omitempty"`
	CacheLaneIndex       *int              `json:"cache_lane_index,omitempty"`
	CacheLaneSizeBytes   int64             `json:"cache_lane_size_bytes,omitempty"`
	SecretEnv            map[string]string `json:"secret_env,omitempty"`
}

type ProjectSettings struct {
	ProjectID                  string                   `json:"project_id"`
	HomeRegion                 string                   `json:"home_region"`
	GitHubRepository           string                   `json:"github_repository,omitempty"`
	GitHubDefaultBranch        string                   `json:"github_default_branch,omitempty"`
	TrustedSecretCommandHashes []string                 `json:"trusted_secret_command_hashes,omitempty"`
	EffectiveRetention         EffectiveRetentionPolicy `json:"effective_retention"`
	Secrets                    []ProjectSecretSummary   `json:"secrets,omitempty"`
	CreatedAt                  time.Time                `json:"created_at"`
	UpdatedAt                  time.Time                `json:"updated_at"`
}

// EffectiveRetentionPolicy reports the server-owned retention behavior active
// for this API deployment. Eligibility is not a deletion SLA: the next
// successful scheduled cleanup performs the physical removal.
type EffectiveRetentionPolicy struct {
	SchemaVersion                        string `json:"schema_version"`
	ArtifactAvailabilitySeconds          int64  `json:"artifact_availability_seconds"`
	ArtifactDeletionEligibleAfterSeconds int64  `json:"artifact_deletion_eligible_after_seconds"`
	RunLogAvailabilitySeconds            int64  `json:"run_log_availability_seconds"`
	RunLogDeletionEligibleAfterSeconds   int64  `json:"run_log_deletion_eligible_after_seconds"`
	ScheduledCleanupEnabled              bool   `json:"scheduled_cleanup_enabled"`
	RunLogCleanupEnabled                 bool   `json:"run_log_cleanup_enabled"`
	CleanupIntervalSeconds               int64  `json:"cleanup_interval_seconds"`
}

type ProjectSummary struct {
	ProjectID                  string     `json:"project_id"`
	HomeRegion                 string     `json:"home_region"`
	GitHubRepository           string     `json:"github_repository,omitempty"`
	GitHubDefaultBranch        string     `json:"github_default_branch,omitempty"`
	TrustedSecretCommandHashes []string   `json:"trusted_secret_command_hashes,omitempty"`
	RunCount                   *int       `json:"run_count,omitempty"`
	LastRunAt                  *time.Time `json:"last_run_at,omitempty"`
	CreatedAt                  time.Time  `json:"created_at"`
	UpdatedAt                  time.Time  `json:"updated_at"`
}

type ListProjectsResponse struct {
	Projects []ProjectSummary `json:"projects"`
}

type UpsertProjectSettingsRequest struct {
	HomeRegion                 *string  `json:"home_region,omitempty"`
	GitHubRepository           *string  `json:"github_repository,omitempty"`
	GitHubDefaultBranch        *string  `json:"github_default_branch,omitempty"`
	TrustedSecretCommandHashes []string `json:"trusted_secret_command_hashes"`
}

type ProjectSecretSummary struct {
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ListProjectSecretsResponse struct {
	ProjectID string                 `json:"project_id"`
	Secrets   []ProjectSecretSummary `json:"secrets"`
}

type UpsertProjectSecretRequest struct {
	Value string `json:"value"`
}

type UpsertProjectSecretResponse struct {
	Secret ProjectSecretSummary `json:"secret"`
}

type DeleteProjectSecretRequest struct {
	Reason string `json:"reason"`
}

type DeleteProjectSecretResponse struct {
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
	Deleted   bool   `json:"deleted"`
}

type CacheLaneInfo struct {
	ID          string     `json:"id"`
	WorkerID    string     `json:"worker_id"`
	LaneIndex   int        `json:"lane_index"`
	Status      string     `json:"status"`
	ActiveRunID string     `json:"active_run_id,omitempty"`
	QuotaBytes  int64      `json:"quota_bytes"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
}

type CacheUsageResponse struct {
	ProjectID       string          `json:"project_id"`
	Lanes           []CacheLaneInfo `json:"lanes"`
	TotalQuotaBytes int64           `json:"total_quota_bytes"`
}

type PurgeCacheResponse struct {
	ProjectID   string `json:"project_id"`
	PurgedLanes int    `json:"purged_lanes"`
}

type WorkerCachePurgeResponse struct {
	Lanes []CacheLaneInfo `json:"lanes"`
}

type RunEvent struct {
	ID         int64     `json:"event_id,omitempty"`
	Type       string    `json:"type"`
	RunID      string    `json:"run_id"`
	Timestamp  time.Time `json:"timestamp"`
	Phase      string    `json:"phase,omitempty"`
	Step       string    `json:"step,omitempty"`
	Service    string    `json:"service,omitempty"`
	Data       string    `json:"data,omitempty"`
	DataBase64 string    `json:"data_base64,omitempty"`
	Message    string    `json:"message,omitempty"`
	Status     string    `json:"status,omitempty"`
	ExitCode   *int      `json:"exit_code,omitempty"`
}

func (e RunEvent) DataBytes() ([]byte, error) {
	if e.DataBase64 != "" {
		return base64.StdEncoding.DecodeString(e.DataBase64)
	}
	return []byte(e.Data), nil
}

type PostRunEventRequest struct {
	ClaimToken string   `json:"claim_token,omitempty"`
	Event      RunEvent `json:"event"`
}

type FinishRunRequest struct {
	// CacheTeardownComplete acknowledges that the runtime has released all
	// writable cache resources. Missing or false retires the generation.
	CacheTeardownComplete bool                       `json:"cache_teardown_complete"`
	ClaimToken            string                     `json:"claim_token,omitempty"`
	Status                string                     `json:"status"`
	ExitCode              int                        `json:"exit_code"`
	FailureReason         string                     `json:"failure_reason,omitempty"`
	Evidence              *RunnerEvidenceAttestation `json:"evidence,omitempty"`
}
