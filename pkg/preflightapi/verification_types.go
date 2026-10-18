package preflightapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

const VerificationSchemaVersion = "readyci.verification.v0alpha1"

const ExecutionPurpose = "execution"

const VerificationRecipeSchemaVersion = "readyci.verification_recipe.v1"

const (
	VerificationStateOpen     = "open"
	VerificationStateSealed   = "sealed"
	VerificationStateTerminal = "terminal"
)

const (
	VerificationVerdictPending              = "pending"
	VerificationVerdictVerified             = "verified"
	VerificationVerdictChecksPassed         = "checks_passed"
	VerificationVerdictFailed               = "failed"
	VerificationVerdictInsufficientEvidence = "insufficient_evidence"
	VerificationVerdictNoChangeRequired     = "no_change_required"
	VerificationVerdictNoVerificationNeeded = "no_verification_needed"
	VerificationVerdictBlocked              = "blocked"
	VerificationVerdictStale                = "stale"
	VerificationVerdictCancelled            = "cancelled"
)

const (
	ClassificationHarnessGap          = "harness_gap"
	ClassificationFlakeSuspected      = "flake_suspected"
	ClassificationStaleSnapshot       = "stale_snapshot"
	ClassificationPartialVerification = "partial_verification"
	ClassificationClientProposedPlan  = "client_proposed_plan"
	ClassificationUntrustedEvidence   = "untrusted_execution_evidence"
)

const VerificationNodeStatusSkipped = "skipped"

const (
	VerificationSchedulingReady                 = "ready"
	VerificationSchedulingWaitingForDependency  = "waiting_for_dependency"
	VerificationSchedulingWaitingForMatrixSlot  = "waiting_for_matrix_slot"
	VerificationSchedulingRetryPending          = "retry_pending"
	VerificationSchedulingActive                = "active"
	VerificationSchedulingSkippedDependency     = "skipped_dependency"
	VerificationSchedulingSkippedPlanFailFast   = "skipped_plan_fail_fast"
	VerificationSchedulingSkippedMatrixFailFast = "skipped_matrix_fail_fast"
	VerificationSchedulingTerminal              = "terminal"
)

const (
	VerificationPlanAuthorityClientProposed = "client_proposed"
	VerificationPlanAuthorityServer         = "server_authorized"
)

const (
	VerificationNodePhaseWarm   = "warm"
	VerificationNodePhaseVerify = "verify"
)

const (
	VerificationNodeConditionAlways    = "always"
	VerificationNodeConditionOnSuccess = "on_success"
)

const (
	VerificationCurrentnessCurrent = "current"
	VerificationCurrentnessStale   = "stale"
)

const (
	VerificationAssuranceScopeConfiguredChecks   = "configured_checks"
	VerificationExecutionAssurancePending        = "pending"
	VerificationExecutionAssuranceReceipt        = "production_runner_receipt"
	VerificationExecutionAssuranceNotEstablished = "not_established"
	VerificationEvaluatorAssuranceUnassessed     = "candidate_workspace_unassessed"
)

// VerificationAssurance makes the bounded meaning of a verdict machine
// readable. A production receipt establishes execution of the configured
// command, not that candidate-controlled tests or evaluators were unchanged.
type VerificationAssurance struct {
	Scope     string `json:"scope"`
	Execution string `json:"execution"`
	Evaluator string `json:"evaluator"`
}

type VerificationSnapshot struct {
	Digest       string   `json:"digest"`
	GitHead      string   `json:"git_head,omitempty"`
	GitBranch    string   `json:"git_branch,omitempty"`
	Dirty        bool     `json:"dirty"`
	ChangeMode   string   `json:"change_mode"`
	ChangedFiles []string `json:"changed_files"`
}

type VerificationCoverage struct {
	MatchedFiles   []string `json:"matched_files"`
	IgnoredFiles   []string `json:"ignored_files"`
	UncoveredFiles []string `json:"uncovered_files"`
}

type VerificationRetryPolicy struct {
	MaxAttempts              int      `json:"max_attempts"`
	RetryableClassifications []string `json:"retryable_classifications,omitempty"`
}

type VerificationNodePlan struct {
	ID               string                  `json:"id"`
	Name             string                  `json:"name"`
	Kind             string                  `json:"kind"`
	Phase            string                  `json:"phase"`
	Condition        string                  `json:"condition"`
	MatrixGroup      string                  `json:"matrix_group,omitempty"`
	Required         bool                    `json:"required"`
	DependsOn        []string                `json:"depends_on,omitempty"`
	SuccessDependsOn []string                `json:"success_depends_on,omitempty"`
	Retry            VerificationRetryPolicy `json:"retry"`
	Run              CreateRunRequest        `json:"run"`
}

type VerificationMatrixGroup struct {
	ID          string `json:"id"`
	MaxParallel int    `json:"max_parallel"`
	FailFast    bool   `json:"fail_fast"`
}

type VerificationPlan struct {
	Digest       string                    `json:"digest"`
	Authority    string                    `json:"authority"`
	FailFast     bool                      `json:"fail_fast"`
	MatrixGroups []VerificationMatrixGroup `json:"matrix_groups,omitempty"`
	Nodes        []VerificationNodePlan    `json:"nodes"`
}

type VerificationPlanAuthorization struct {
	Source         string `json:"source,omitempty"`
	PolicyDigest   string `json:"policy_digest,omitempty"`
	BaseCommit     string `json:"base_commit,omitempty"`
	PlannerVersion string `json:"planner_version,omitempty"`
}

type VerificationPlanDivergence struct {
	AddedNodes      []string `json:"added_nodes,omitempty"`
	RemovedNodes    []string `json:"removed_nodes,omitempty"`
	ChangedCommands []string `json:"changed_commands,omitempty"`
	ChangedCoverage []string `json:"changed_coverage,omitempty"`
	ChangedPolicy   []string `json:"changed_policy,omitempty"`
}

// VerificationRecipe records the logical CLI inputs needed to create a
// successor against a new workspace. It never grants plan authority: the
// service still derives and authorizes the execution plan from trusted policy.
type VerificationRecipe struct {
	SkipWarm    bool     `json:"skip_warm,omitempty"`
	WarmChecks  []string `json:"warm_checks,omitempty"`
	SkipConfirm bool     `json:"skip_confirm,omitempty"`
	// Purpose is immutable and never grants plan authority. Empty means verification.
	Command        string                    `json:"command,omitempty"`
	Purpose        string                    `json:"purpose,omitempty"`
	SchemaVersion  string                    `json:"schema_version"`
	Digest         string                    `json:"digest,omitempty"`
	Rerunnable     bool                      `json:"rerunnable"`
	Check          string                    `json:"check,omitempty"`
	ChangeMode     string                    `json:"change_mode"`
	RequestSecrets bool                      `json:"request_secrets"`
	RetryCount     int                       `json:"retry_count"`
	RetryProvided  bool                      `json:"retry_provided"`
	Source         *VerificationSourceRecipe `json:"source"`
}

// VerificationSourceRecipe is the exact local packaging contract needed to
// recompute whether a completed session still describes the caller's current
// workspace. It is untrusted input and never grants source-policy authority.
type VerificationSourceRecipe struct {
	UploadBaselineSessionID string   `json:"upload_baseline_session_id,omitempty"`
	ExcludePatterns         []string `json:"exclude_patterns"`
	DefaultExcludePatterns  []string `json:"default_exclude_patterns"`
	IncludeUntracked        bool     `json:"include_untracked"`
	UseProjectIgnore        bool     `json:"use_project_ignore"`
	SourcePolicyDigest      string   `json:"source_policy_digest,omitempty"`
}

type CreateVerificationSessionRequest struct {
	SchemaVersion  string               `json:"schema_version"`
	ProjectID      string               `json:"project_id"`
	IdempotencyKey string               `json:"idempotency_key"`
	TaskID         string               `json:"task_id,omitempty"`
	PredecessorID  string               `json:"predecessor_id,omitempty"`
	Intent         string               `json:"intent,omitempty"`
	Goal           string               `json:"goal,omitempty"`
	Recipe         VerificationRecipe   `json:"recipe"`
	Snapshot       VerificationSnapshot `json:"snapshot"`
	Coverage       VerificationCoverage `json:"coverage"`
	Plan           VerificationPlan     `json:"plan"`
}

// RetryVerificationSessionRequest creates one idempotent successor using the
// predecessor's exact retained source. The service assigns all other fields.
type RetryVerificationSessionRequest struct {
}

type RetryVerificationSessionResponse struct {
	SchemaVersion      string              `json:"schema_version"`
	RetryOfSessionID   string              `json:"retry_of_session_id"`
	PolicyReauthorized bool                `json:"policy_reauthorized"`
	Session            VerificationSession `json:"session"`
}

const (
	VerificationSourceUploadModeDirect     = "direct_object_store"
	VerificationSourceUploadModeServerCopy = "server_copy"
)

// A session uploads exactly one immutable source bundle at session scope; the
// service materializes it into every physical node attempt, including deferred
// nodes and retries. There is no per-node upload and no client-driven advance.
type CreateVerificationSessionResponse struct {
	SchemaVersion                 string              `json:"schema_version"`
	Session                       VerificationSession `json:"session"`
	SourceUploadMode              string              `json:"source_upload_mode"`
	SourceUploadBaselineSessionID string              `json:"source_upload_baseline_session_id,omitempty"`
	SourceUploadPrepareURL        string              `json:"source_upload_prepare_url,omitempty"`
}

type PrepareVerificationSessionSourceUploadRequest struct {
	SizeBytes     int64  `json:"size_bytes"`
	ContentSHA256 string `json:"content_sha256"`
	ContentMD5    string `json:"content_md5"`
}

type PrepareVerificationSessionSourceUploadResponse struct {
	UploadURL     string            `json:"upload_url"`
	UploadMethod  string            `json:"upload_method"`
	UploadHeaders map[string]string `json:"upload_headers,omitempty"`
	CompleteURL   string            `json:"complete_url"`
}

type CompleteVerificationSessionSourceUploadRequest struct {
	SizeBytes     int64  `json:"size_bytes"`
	ContentSHA256 string `json:"content_sha256"`
}

type VerificationAttempt struct {
	RunID                 string    `json:"run_id"`
	Attempt               int       `json:"attempt"`
	Status                string    `json:"status"`
	ExitCode              *int      `json:"exit_code,omitempty"`
	Classification        string    `json:"classification,omitempty"`
	Retryable             bool      `json:"retryable"`
	ArtifactCount         int       `json:"artifact_count"`
	ArtifactDigest        string    `json:"artifact_digest,omitempty"`
	RetainedArtifactCount int       `json:"retained_artifact_count"`
	ArtifactsAvailable    bool      `json:"artifacts_available"`
	CreatedAt             time.Time `json:"created_at"`
	// Lifecycle timestamps are service observations for turnaround diagnostics.
	QueuedAt   *time.Time `json:"queued_at,omitempty"`
	ClaimedAt  *time.Time `json:"claimed_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	// EvidenceAttested is assigned only after the service validates a
	// runner-generated attestation. Callers and artifact filenames cannot set it.
	EvidenceAttested bool `json:"evidence_attested"`
	// EvidenceDigest binds the validated attestation consumed by the verdict.
	// It remains empty while EvidenceAttested is false.
	EvidenceDigest string `json:"evidence_digest,omitempty"`
	// EvidenceReceipt is the service-validated runner record. Candidate
	// artifacts and public session requests cannot populate it.
	EvidenceReceipt *RunnerEvidenceReceipt `json:"evidence_receipt,omitempty"`
}

// VerificationCurrentnessObservation is caller-relative state recorded after
// execution. It never changes the immutable terminal result or its digest.
type VerificationCurrentnessObservation struct {
	State                  string    `json:"state"`
	ObservedSnapshotDigest string    `json:"observed_snapshot_digest,omitempty"`
	ObservedAt             time.Time `json:"observed_at,omitempty"`
	ObservedBy             string    `json:"observed_by,omitempty"`
}

// VerificationNodeScheduling is a service-derived explanation of why a
// logical node has or has not received a physical attempt. It is deliberately
// separate from Status: status reports execution outcome, while scheduling
// reports graph readiness and skip causes.
type VerificationNodeScheduling struct {
	State         string `json:"state"`
	Reason        string `json:"reason,omitempty"`
	DependencyID  string `json:"dependency_id,omitempty"`
	MatrixGroupID string `json:"matrix_group_id,omitempty"`
}

type VerificationNode struct {
	ID         string                     `json:"id"`
	Name       string                     `json:"name"`
	Kind       string                     `json:"kind"`
	Required   bool                       `json:"required"`
	Status     string                     `json:"status"`
	Scheduling VerificationNodeScheduling `json:"scheduling"`
	Attempts   []VerificationAttempt      `json:"attempts"`
}

// VerificationClientInterruption records the CLI's observation of a local
// signal. It is not part of the server's authoritative verification result.
type VerificationClientInterruption struct {
	Signal                   string `json:"signal"`
	ResourceID               string `json:"resource_id"`
	CancellationAcknowledged bool   `json:"cancellation_acknowledged"`
	StatusCommand            string `json:"status_command"`
	CancelCommand            string `json:"cancel_command"`
}

// VerificationClientError describes a failure in the local observation path.
// It must not replace the service-owned verdict or classification.
type VerificationClientError struct {
	Code              string `json:"code"`
	Classification    string `json:"classification"`
	Retryable         bool   `json:"retryable"`
	RetryAfterSeconds *int   `json:"retry_after_seconds,omitempty"`
	Message           string `json:"message"`
}

type VerificationSession struct {
	SchemaVersion          string                              `json:"schema_version"`
	SessionID              string                              `json:"session_id"`
	IdempotencyKey         string                              `json:"idempotency_key"`
	TaskID                 string                              `json:"task_id,omitempty"`
	TeamID                 string                              `json:"team_id,omitempty"`
	ProjectID              string                              `json:"project_id"`
	PredecessorID          string                              `json:"predecessor_id,omitempty"`
	SuccessorID            string                              `json:"successor_id,omitempty"`
	Intent                 string                              `json:"intent,omitempty"`
	Goal                   string                              `json:"goal,omitempty"`
	Recipe                 VerificationRecipe                  `json:"recipe"`
	SecretsRequested       bool                                `json:"secrets_requested"`
	SecretsAuthorized      bool                                `json:"secrets_authorized"`
	SourceReady            bool                                `json:"source_ready"`
	SnapshotRetry          *VerificationSnapshotRetry          `json:"snapshot_retry,omitempty"`
	State                  string                              `json:"state"`
	Verdict                string                              `json:"verdict"`
	Assurance              VerificationAssurance               `json:"assurance"`
	Classification         string                              `json:"classification,omitempty"`
	TerminalReason         string                              `json:"terminal_reason,omitempty"`
	Currentness            string                              `json:"currentness"`
	CurrentSnapshotDigest  string                              `json:"current_snapshot_digest,omitempty"`
	CurrentnessObservation *VerificationCurrentnessObservation `json:"currentness_observation,omitempty"`
	Snapshot               VerificationSnapshot                `json:"snapshot"`
	Coverage               VerificationCoverage                `json:"coverage"`
	Plan                   VerificationPlan                    `json:"plan"`
	ProposedPlan           *VerificationPlan                   `json:"proposed_plan,omitempty"`
	PlanAuthorization      *VerificationPlanAuthorization      `json:"plan_authorization,omitempty"`
	PlanDivergence         *VerificationPlanDivergence         `json:"plan_divergence,omitempty"`
	Nodes                  []VerificationNode                  `json:"nodes"`
	CreatedAt              time.Time                           `json:"created_at"`
	UpdatedAt              time.Time                           `json:"updated_at"`
	CompletedAt            *time.Time                          `json:"completed_at,omitempty"`
	ResultDigest           string                              `json:"result_digest,omitempty"`
	ClientInterruption     *VerificationClientInterruption     `json:"client_interruption,omitempty"`
	ClientError            *VerificationClientError            `json:"client_error,omitempty"`
	// Warnings reflect currently retained diagnostic reports. They never change
	// the execution verdict, classification, or immutable result digest.
	Warnings []DiagnosticWarning `json:"warnings,omitempty"`
}

// VerificationSnapshotRetry is a service-derived recovery projection. It does
// not alter the immutable verdict, result digest, or retry operation identity.
type VerificationSnapshotRetry struct {
	Action      string `json:"action"`
	SuccessorID string `json:"successor_id"`
}

type MarkVerificationSessionStaleRequest struct {
	CurrentSnapshotDigest string `json:"current_snapshot_digest"`
}

type VerificationSessionSummary struct {
	SessionID      string     `json:"session_id"`
	TeamID         string     `json:"team_id,omitempty"`
	TaskID         string     `json:"task_id,omitempty"`
	ProjectID      string     `json:"project_id"`
	Intent         string     `json:"intent,omitempty"`
	Goal           string     `json:"goal,omitempty"`
	State          string     `json:"state"`
	Verdict        string     `json:"verdict"`
	Currentness    string     `json:"currentness"`
	PlanAuthority  string     `json:"plan_authority"`
	Classification string     `json:"classification,omitempty"`
	CoverageGap    bool       `json:"coverage_gap"`
	ChainLength    int        `json:"chain_length"`
	PredecessorID  string     `json:"predecessor_id,omitempty"`
	SuccessorID    string     `json:"successor_id,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
}

type ListVerificationSessionsResponse struct {
	Sessions       []VerificationSessionSummary `json:"sessions"`
	NextPageCursor string                       `json:"next_page_cursor,omitempty"`
	HasMore        bool                         `json:"has_more"`
}

func NormalizeCreateVerificationSessionRequest(req CreateVerificationSessionRequest) (CreateVerificationSessionRequest, error) {
	if strings.TrimSpace(req.SchemaVersion) == "" {
		req.SchemaVersion = VerificationSchemaVersion
	}
	if req.SchemaVersion != VerificationSchemaVersion {
		return req, fmt.Errorf("unsupported verification schema version %q", req.SchemaVersion)
	}
	req.ProjectID = strings.TrimSpace(req.ProjectID)
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	req.TaskID = strings.TrimSpace(req.TaskID)
	req.PredecessorID = strings.TrimSpace(req.PredecessorID)
	req.Intent = strings.TrimSpace(req.Intent)
	req.Goal = strings.TrimSpace(req.Goal)
	req.Recipe.SchemaVersion = strings.TrimSpace(req.Recipe.SchemaVersion)
	if req.Recipe.SchemaVersion == "" {
		return req, fmt.Errorf("recipe.schema_version is required")
	}
	if req.Recipe.SchemaVersion != VerificationRecipeSchemaVersion {
		return req, fmt.Errorf("unsupported verification recipe schema version %q", req.Recipe.SchemaVersion)
	}
	if req.Recipe.SkipWarm {
		if req.Recipe.Purpose != ExecutionPurpose || len(req.Recipe.WarmChecks) > 0 || req.Recipe.SkipConfirm {
			return req, fmt.Errorf("skip_warm is only valid for ordinary execution without explicit warming")
		}
		for _, node := range req.Plan.Nodes {
			if strings.TrimSpace(node.Phase) == VerificationNodePhaseWarm {
				return req, fmt.Errorf("skip_warm cannot include warm nodes")
			}
		}
	}
	if len(req.Recipe.WarmChecks) > 0 {
		if req.Recipe.Purpose != ExecutionPurpose || req.Recipe.Check != "" || req.Recipe.Command != "" {
			return req, fmt.Errorf("warm recipe requires execution purpose and cannot select another check or command")
		}
		for _, name := range req.Recipe.WarmChecks {
			if strings.TrimSpace(name) == "" || containsVerificationControl(name) {
				return req, fmt.Errorf("invalid warm check name")
			}
		}
	}
	if req.Recipe.SkipConfirm && len(req.Recipe.WarmChecks) == 0 {
		return req, fmt.Errorf("skip_confirm requires warm_checks")
	}

	if req.Recipe.Command != "" && req.Recipe.Purpose != ExecutionPurpose {
		return req, fmt.Errorf("recipe command is only valid for requested execution")
	}
	if req.Recipe.Purpose != "" && req.Recipe.Purpose != ExecutionPurpose {
		return req, fmt.Errorf("unsupported recipe purpose %q", req.Recipe.Purpose)
	}
	if req.Recipe.Purpose == ExecutionPurpose && strings.TrimSpace(req.Plan.Authority) == VerificationPlanAuthorityServer {
		return req, fmt.Errorf("requested execution cannot have verification plan authority")
	}
	if req.Recipe.Purpose == ExecutionPurpose && len(req.Plan.Nodes) == 0 {
		return req, fmt.Errorf("requested execution requires at least one check")
	}
	req.Recipe.Check = strings.TrimSpace(req.Recipe.Check)
	if strings.TrimSpace(req.Recipe.ChangeMode) == "" {
		return req, fmt.Errorf("recipe.change_mode is required")
	}
	changeMode, err := syncbundle.NormalizeChangeMode(req.Recipe.ChangeMode)
	if err != nil {
		return req, fmt.Errorf("recipe change_mode: %w", err)
	}
	req.Recipe.ChangeMode = changeMode
	if !req.Recipe.RetryProvided {
		req.Recipe.RetryCount = 0
	}
	if req.Recipe.RetryCount < 0 || req.Recipe.RetryCount > 9 {
		return req, fmt.Errorf("recipe retry_count must be between 0 and 9")
	}
	if req.Recipe.Source == nil {
		return req, fmt.Errorf("recipe.source is required")
	}
	req.Recipe.Source.ExcludePatterns = normalizedStrings(req.Recipe.Source.ExcludePatterns)
	req.Recipe.Source.DefaultExcludePatterns = normalizedStrings(req.Recipe.Source.DefaultExcludePatterns)
	if id := req.Recipe.Source.UploadBaselineSessionID; id != "" {
		if req.Recipe.Purpose != ExecutionPurpose || len(id) > 160 || strings.ContainsAny(id, "/\\") || containsVerificationControl(id) {
			return req, fmt.Errorf("invalid source upload baseline session")
		}
	}
	req.Recipe.Source.SourcePolicyDigest = strings.TrimSpace(req.Recipe.Source.SourcePolicyDigest)
	for _, pattern := range append(append([]string(nil), req.Recipe.Source.ExcludePatterns...), req.Recipe.Source.DefaultExcludePatterns...) {
		if containsVerificationControl(pattern) {
			return req, fmt.Errorf("recipe source pattern contains control characters")
		}
	}
	if req.Recipe.Source.SourcePolicyDigest != "" {
		if err := syncbundle.ValidateSnapshotDigest(req.Recipe.Source.SourcePolicyDigest); err != nil {
			return req, fmt.Errorf("recipe source_policy_digest: %w", err)
		}
	}
	recipeDigest, err := VerificationRecipeDigest(req.Recipe)
	if err != nil {
		return req, err
	}
	if req.Recipe.Digest != "" && req.Recipe.Digest != recipeDigest {
		return req, fmt.Errorf("recipe digest does not match normalized verification recipe")
	}
	req.Recipe.Digest = recipeDigest
	if req.ProjectID == "" {
		return req, fmt.Errorf("project_id is required")
	}
	if req.IdempotencyKey == "" {
		return req, fmt.Errorf("idempotency_key is required")
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "project_id", value: req.ProjectID},
		{name: "idempotency_key", value: req.IdempotencyKey},
		{name: "task_id", value: req.TaskID},
		{name: "predecessor_id", value: req.PredecessorID},
		{name: "intent", value: req.Intent},
		{name: "goal", value: req.Goal},
		{name: "recipe.check", value: req.Recipe.Check},
	} {
		if containsVerificationControl(field.value) {
			return req, fmt.Errorf("%s must not contain control characters", field.name)
		}
	}
	if len(req.ProjectID) > 200 || len(req.IdempotencyKey) > 200 || len(req.TaskID) > 200 || len(req.PredecessorID) > 200 || len(req.Intent) > 100 || len(req.Goal) > 1000 || len(req.Recipe.Check) > 200 {
		return req, fmt.Errorf("verification session metadata is too long")
	}
	if len(req.Plan.Nodes) > 128 {
		return req, fmt.Errorf("verification plan exceeds 128 nodes")
	}
	if len(req.Snapshot.ChangedFiles) > 10000 {
		return req, fmt.Errorf("snapshot exceeds 10000 changed files")
	}
	if err := syncbundle.ValidateSnapshotDigest(req.Snapshot.Digest); err != nil {
		return req, err
	}
	changeMode, err = syncbundle.NormalizeChangeMode(req.Snapshot.ChangeMode)
	if err != nil {
		return req, err
	}
	req.Snapshot.ChangeMode = changeMode
	if req.Recipe.ChangeMode != req.Snapshot.ChangeMode {
		return req, fmt.Errorf("recipe change_mode %q does not match snapshot change_mode %q", req.Recipe.ChangeMode, req.Snapshot.ChangeMode)
	}
	if req.Snapshot.ChangedFiles, err = normalizedPaths(req.Snapshot.ChangedFiles); err != nil {
		return req, fmt.Errorf("snapshot changed_files: %w", err)
	}
	if req.Coverage.MatchedFiles, err = normalizedPaths(req.Coverage.MatchedFiles); err != nil {
		return req, fmt.Errorf("coverage matched_files: %w", err)
	}
	if req.Coverage.IgnoredFiles, err = normalizedPaths(req.Coverage.IgnoredFiles); err != nil {
		return req, fmt.Errorf("coverage ignored_files: %w", err)
	}
	if req.Coverage.UncoveredFiles, err = normalizedPaths(req.Coverage.UncoveredFiles); err != nil {
		return req, fmt.Errorf("coverage uncovered_files: %w", err)
	}
	if err := validateCoverage(req.Snapshot.ChangedFiles, req.Coverage); err != nil {
		return req, err
	}
	if err := normalizeVerificationPlan(&req.Plan, req.ProjectID); err != nil {
		return req, err
	}
	digest, err := VerificationPlanDigest(req.Plan)
	if err != nil {
		return req, err
	}
	if req.Plan.Digest != "" && req.Plan.Digest != digest {
		return req, fmt.Errorf("plan digest does not match normalized verification plan")
	}
	req.Plan.Digest = digest
	if len(req.Snapshot.ChangedFiles) > 0 && len(req.Plan.Nodes) == 0 && len(req.Coverage.UncoveredFiles) == 0 && len(req.Coverage.IgnoredFiles) != len(req.Snapshot.ChangedFiles) {
		return req, fmt.Errorf("changed files without verification nodes must be reported as uncovered")
	}
	return req, nil
}

func VerificationRecipeDigest(recipe VerificationRecipe) (string, error) {
	recipe.Digest = ""
	encoded, err := json.Marshal(recipe)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func VerificationPlanDigest(plan VerificationPlan) (string, error) {
	plan.Digest = ""
	encoded, err := json.Marshal(plan)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func normalizeVerificationPlan(plan *VerificationPlan, projectID string) error {
	if plan == nil {
		return fmt.Errorf("plan is required")
	}
	if plan.Nodes == nil {
		plan.Nodes = []VerificationNodePlan{}
	}
	if plan.Authority == "" {
		plan.Authority = VerificationPlanAuthorityClientProposed
	}
	switch plan.Authority {
	case VerificationPlanAuthorityClientProposed, VerificationPlanAuthorityServer:
	default:
		return fmt.Errorf("unsupported verification plan authority %q", plan.Authority)
	}
	groupIDs := make(map[string]struct{}, len(plan.MatrixGroups))
	for index := range plan.MatrixGroups {
		group := &plan.MatrixGroups[index]
		group.ID = strings.TrimSpace(group.ID)
		if group.ID == "" {
			return fmt.Errorf("verification matrix group id is required")
		}
		if _, ok := groupIDs[group.ID]; ok {
			return fmt.Errorf("duplicate verification matrix group %q", group.ID)
		}
		if group.MaxParallel < 0 {
			return fmt.Errorf("verification matrix group %q max_parallel must be non-negative", group.ID)
		}
		groupIDs[group.ID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(plan.Nodes))
	for index := range plan.Nodes {
		node := &plan.Nodes[index]
		node.ID = strings.TrimSpace(node.ID)
		node.Name = strings.TrimSpace(node.Name)
		node.Kind = strings.TrimSpace(node.Kind)
		if node.ID == "" || node.Name == "" || node.Kind == "" {
			return fmt.Errorf("verification node id, name, and kind are required")
		}
		node.Phase = strings.TrimSpace(node.Phase)
		if node.Phase == "" {
			node.Phase = VerificationNodePhaseVerify
		}
		switch node.Phase {
		case VerificationNodePhaseWarm, VerificationNodePhaseVerify:
		default:
			return fmt.Errorf("verification node %q has unsupported phase %q", node.ID, node.Phase)
		}
		node.Condition = strings.TrimSpace(node.Condition)
		if node.Condition == "" {
			node.Condition = VerificationNodeConditionOnSuccess
		}
		switch node.Condition {
		case VerificationNodeConditionAlways, VerificationNodeConditionOnSuccess:
		default:
			return fmt.Errorf("verification node %q has unsupported condition %q", node.ID, node.Condition)
		}
		node.MatrixGroup = strings.TrimSpace(node.MatrixGroup)
		if node.MatrixGroup != "" {
			if _, ok := groupIDs[node.MatrixGroup]; !ok {
				return fmt.Errorf("verification node %q references unknown matrix group %q", node.ID, node.MatrixGroup)
			}
		}
		if _, ok := seen[node.ID]; ok {
			return fmt.Errorf("duplicate verification node %q", node.ID)
		}
		seen[node.ID] = struct{}{}
		if node.Retry.MaxAttempts == 0 {
			node.Retry.MaxAttempts = 1
		}
		if node.Retry.MaxAttempts < 1 || node.Retry.MaxAttempts > 10 {
			return fmt.Errorf("verification node %q max_attempts must be between 1 and 10", node.ID)
		}
		node.Retry.RetryableClassifications = normalizedStrings(node.Retry.RetryableClassifications)
		node.DependsOn = normalizedStrings(node.DependsOn)
		node.SuccessDependsOn = normalizedStrings(node.SuccessDependsOn)
		node.DependsOn = normalizedStrings(append(node.DependsOn, node.SuccessDependsOn...))
		node.Run.ProjectID = projectID
		node.Run.RunGroupKey = ""
		node.Run.RunGroupKind = ""
		node.Run.RunGroupName = ""
		node.Run.RunGroupPredecessorID = ""
		node.Run.LogicalChildID = ""
		node.Run.LogicalChildName = ""
		node.Run.IdempotencyKey = ""
		node.Run.Attempt = 0
		node.Run.ChangeMode = ""
		node.Run.SnapshotDigest = ""
		node.Run.ChangedFilesDigest = ""
		node.Run.Git.Branch = ""
		node.Run.Git.Head = ""
		node.Run.Git.Dirty = false
	}
	for _, node := range plan.Nodes {
		for _, dependency := range node.DependsOn {
			if dependency == node.ID {
				return fmt.Errorf("verification node %q cannot depend on itself", node.ID)
			}
			if _, ok := seen[dependency]; !ok {
				return fmt.Errorf("verification node %q depends on unknown node %q", node.ID, dependency)
			}
		}
		for _, dependency := range node.SuccessDependsOn {
			if _, ok := seen[dependency]; !ok {
				return fmt.Errorf("verification node %q requires success from unknown node %q", node.ID, dependency)
			}
		}
	}
	return validateAcyclicVerificationPlan(plan.Nodes)
}

func validateAcyclicVerificationPlan(nodes []VerificationNodePlan) error {
	dependencies := make(map[string][]string, len(nodes))
	for _, node := range nodes {
		dependencies[node.ID] = node.DependsOn
	}
	visiting := map[string]bool{}
	visited := map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if visiting[id] {
			return fmt.Errorf("verification plan contains a dependency cycle at %q", id)
		}
		if visited[id] {
			return nil
		}
		visiting[id] = true
		for _, dependency := range dependencies[id] {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		delete(visiting, id)
		visited[id] = true
		return nil
	}
	for id := range dependencies {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

func validateCoverage(changed []string, coverage VerificationCoverage) error {
	changedSet := make(map[string]struct{}, len(changed))
	for _, path := range changed {
		changedSet[path] = struct{}{}
	}
	seen := map[string]string{}
	for label, paths := range map[string][]string{
		"matched":   coverage.MatchedFiles,
		"ignored":   coverage.IgnoredFiles,
		"uncovered": coverage.UncoveredFiles,
	} {
		for _, path := range paths {
			if _, ok := changedSet[path]; !ok {
				return fmt.Errorf("coverage %s path %q is not in snapshot changed_files", label, path)
			}
			if previous, ok := seen[path]; ok {
				return fmt.Errorf("changed path %q appears in both %s and %s coverage", path, previous, label)
			}
			seen[path] = label
		}
	}
	for _, path := range changed {
		if _, ok := seen[path]; !ok {
			return fmt.Errorf("changed path %q is missing from coverage", path)
		}
	}
	return nil
}

func normalizedPaths(values []string) ([]string, error) {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		rel, err := syncbundle.ValidateRelativePath(value)
		if err != nil {
			return nil, err
		}
		if syncbundle.IsGitControlPath(rel) {
			return nil, fmt.Errorf("path %q targets git control metadata", value)
		}
		if _, ok := seen[rel]; ok {
			continue
		}
		seen[rel] = struct{}{}
		out = append(out, rel)
	}
	sort.Strings(out)
	return out, nil
}

func normalizedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func containsVerificationControl(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func IsTerminalVerificationNodeStatus(status string) bool {
	return status == VerificationNodeStatusSkipped ||
		status == StatusBlocked ||
		status == StatusDenied ||
		IsTerminalRunStatus(status)
}

func VerificationNodeRetryPending(planned VerificationNodePlan, node VerificationNode) bool {
	if len(node.Attempts) == 0 || len(node.Attempts) >= planned.Retry.MaxAttempts {
		return false
	}
	latest := node.Attempts[len(node.Attempts)-1]
	if !IsTerminalVerificationNodeStatus(latest.Status) || !latest.Retryable {
		return false
	}
	for _, classification := range planned.Retry.RetryableClassifications {
		if latest.Classification == classification {
			return true
		}
	}
	return false
}
