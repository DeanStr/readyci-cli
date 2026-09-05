package main

import (
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

const (
	agentSchemaVersion     = "readyci.verification.v0alpha1"
	agentInstructionsStart = "<!-- readyci-agent-instructions:start -->"
	agentInstructionsEnd   = "<!-- readyci-agent-instructions:end -->"
)

const (
	agentIntentEditLoop      = "edit-loop"
	agentIntentPreCommit     = "pre-commit"
	agentIntentPrePR         = "pre-pr"
	agentIntentFixtureUpdate = "fixture-update"
	agentIntentSecurity      = "security-sensitive"
	agentIntentFinalHandoff  = "final-handoff"
)

var agentTokenPresets = map[string][]string{
	"verify-only":             {"runs:create", "runs:read", "cache:read", "projects:read", "account:read"},
	"verify-with-cache-purge": {"runs:create", "runs:read", "cache:read", "cache:purge", "projects:read", "account:read"},
	"verify-with-feedback":    {"runs:create", "runs:read", "cache:read", "projects:read", "account:read", "feedback:write"},
	"verify-with-secrets":     {"runs:create", "runs:create_with_secrets", "runs:read", "cache:read", "projects:read", "account:read"},
	"project-maintainer":      {"runs:create", "runs:read", "cache:read", "cache:purge", "projects:read", "projects:write", "policies:read", "secrets:write", "account:read"},
}

type agentCheckRecommendation struct {
	Check        string                   `json:"check"`
	Reason       string                   `json:"reason"`
	Alternatives []agentCheckAlternative  `json:"alternatives,omitempty"`
	Estimated    *agentCheckEstimatedCost `json:"estimated,omitempty"`
}

type agentCheckAlternative struct {
	Check  string `json:"check"`
	Reason string `json:"reason"`
}

type agentCheckEstimatedCost struct {
	RunnerSize     string `json:"runner_size"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

type agentPlanReport struct {
	SchemaVersion  string                   `json:"schema_version"`
	Status         string                   `json:"status"`
	Mode           string                   `json:"mode,omitempty"`
	Intent         string                   `json:"intent"`
	Goal           string                   `json:"goal,omitempty"`
	Check          string                   `json:"check,omitempty"`
	Reason         string                   `json:"reason"`
	Command        string                   `json:"command,omitempty"`
	ProjectID      string                   `json:"project_id,omitempty"`
	ChangeMode     string                   `json:"change_mode"`
	RunnerSize     string                   `json:"runner_size,omitempty"`
	TimeoutSeconds int                      `json:"timeout_seconds,omitempty"`
	MatrixVariants []string                 `json:"matrix_variants,omitempty"`
	Checks         []agentAffectedCheck     `json:"checks,omitempty"`
	Alternatives   []agentCheckAlternative  `json:"alternatives,omitempty"`
	Estimated      *agentCheckEstimatedCost `json:"estimated,omitempty"`
	Plan           *verificationPlan        `json:"plan,omitempty"`
	Policy         *agentPolicyReport       `json:"policy,omitempty"`
	Warnings       []agentWarning           `json:"warnings,omitempty"`
	VerifyCommand  string                   `json:"verify_command"`
}

type agentPolicyReport struct {
	NetworkMode          string   `json:"network_mode"`
	NetworkAllowlist     []string `json:"network_allowlist"`
	SecretsRequested     bool     `json:"secrets_requested"`
	SecretsAuthorized    bool     `json:"secrets_authorized"`
	TokenScopesRequired  []string `json:"token_scopes_required"`
	TokenScopesMissing   []string `json:"token_scopes_missing"`
	UploadPreviewCommand string   `json:"upload_preview_command,omitempty"`
	PolicyReasons        []string `json:"policy_reasons,omitempty"`
}

type agentNextAction struct {
	Kind    string `json:"kind"`
	Command string `json:"command,omitempty"`
	Path    string `json:"path,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

type agentDiagnosis struct {
	Kind        string  `json:"kind"`
	Confidence  float64 `json:"confidence"`
	Summary     string  `json:"summary"`
	Retryable   bool    `json:"retryable"`
	Action      string  `json:"agent_action"`
	PrimaryFile string  `json:"primary_file,omitempty"`
}

type agentEvidence struct {
	EventID int64  `json:"event_id,omitempty"`
	Stream  string `json:"stream,omitempty"`
	Seq     int    `json:"seq"`
	Line    int    `json:"line,omitempty"`
	Time    string `json:"time,omitempty"`
	Event   string `json:"event,omitempty"`
	Path    string `json:"path,omitempty"`
	Text    string `json:"text,omitempty"`
}

type agentEvidencePack struct {
	Name           string   `json:"name"`
	Kind           string   `json:"kind"`
	Path           string   `json:"path"`
	Source         string   `json:"source,omitempty"`
	ArtifactID     string   `json:"artifact_id,omitempty"`
	DownloadURL    string   `json:"download_url,omitempty"`
	SizeBytes      int64    `json:"size_bytes,omitempty"`
	PreviewCommand string   `json:"preview_command,omitempty"`
	Description    string   `json:"description,omitempty"`
	Files          []string `json:"files,omitempty"`
}

type agentRepairTarget struct {
	Path   string `json:"path"`
	Reason string `json:"reason,omitempty"`
}

type agentRepairCommand struct {
	Kind    string `json:"kind"`
	Command string `json:"command"`
	Reason  string `json:"reason,omitempty"`
}

type agentVerifyRunReport struct {
	APIURL           string          `json:"-"`
	SecretsRequested bool            `json:"-"`
	RunID            string          `json:"run_id"`
	RunGroupID       string          `json:"run_group_id,omitempty"`
	Phase            string          `json:"phase,omitempty"`
	Status           string          `json:"status"`
	Check            string          `json:"check"`
	MatrixVariant    string          `json:"matrix_variant,omitempty"`
	Terminal         bool            `json:"terminal"`
	ExitCode         *int            `json:"exit_code,omitempty"`
	Classification   string          `json:"classification"`
	Retryable        bool            `json:"retryable"`
	AgentAction      string          `json:"agent_action"`
	Summary          string          `json:"summary"`
	LogsURL          string          `json:"logs_url,omitempty"`
	LogsCommand      string          `json:"logs_command,omitempty"`
	ExplainCommand   string          `json:"explain_command,omitempty"`
	IdempotencyKey   string          `json:"idempotency_key,omitempty"`
	Evidence         []agentEvidence `json:"evidence,omitempty"`
}

type agentVerifyReport struct {
	SchemaVersion  string                    `json:"schema_version"`
	Status         string                    `json:"status"`
	Mode           string                    `json:"mode,omitempty"`
	Intent         string                    `json:"intent,omitempty"`
	Goal           string                    `json:"goal,omitempty"`
	Reason         string                    `json:"reason,omitempty"`
	RunGroupID     *string                   `json:"run_group_id"`
	GroupStatus    *string                   `json:"group_status"`
	GroupKind      string                    `json:"group_kind,omitempty"`
	RunID          string                    `json:"run_id,omitempty"`
	Check          string                    `json:"check,omitempty"`
	Terminal       bool                      `json:"terminal"`
	ExitCode       *int                      `json:"exit_code,omitempty"`
	CLIExitCode    int                       `json:"cli_exit_code"`
	Classification string                    `json:"classification"`
	Retryable      bool                      `json:"retryable"`
	AgentAction    string                    `json:"agent_action"`
	Summary        string                    `json:"summary"`
	Recommendation *agentCheckRecommendation `json:"recommendation,omitempty"`
	Checks         []agentAffectedCheck      `json:"checks"`
	Runs           []agentVerifyRunReport    `json:"runs"`
	Policy         *agentPolicyReport        `json:"policy,omitempty"`
	NextActions    []agentNextAction         `json:"next_actions,omitempty"`
	Warnings       []agentWarning            `json:"warnings,omitempty"`
	ArtifactPaths  []string                  `json:"artifact_paths,omitempty"`
	LogsURL        string                    `json:"logs_url,omitempty"`
	LogsCommand    string                    `json:"logs_command,omitempty"`
	ExplainCommand string                    `json:"explain_command,omitempty"`
	Error          string                    `json:"error,omitempty"`
}

type agentAffectedCheck struct {
	Check        string   `json:"check"`
	Selected     bool     `json:"selected"`
	NetworkMode  string   `json:"network_mode,omitempty"`
	MatchedPaths []string `json:"matched_paths,omitempty"`
	Reasons      []string `json:"reasons,omitempty"`
}

type agentWarning struct {
	Section string `json:"section,omitempty"`
	Message string `json:"message"`
}

type agentExplainReport struct {
	SchemaVersion       string                         `json:"schema_version"`
	RunID               string                         `json:"run_id"`
	Status              string                         `json:"status"`
	Terminal            bool                           `json:"terminal"`
	ExitCode            *int                           `json:"exit_code,omitempty"`
	Check               string                         `json:"check,omitempty"`
	MatrixVariant       string                         `json:"matrix_variant,omitempty"`
	Command             string                         `json:"command"`
	Diagnosis           agentDiagnosis                 `json:"diagnosis"`
	Repair              agentRepairPacket              `json:"repair"`
	Evidence            []agentEvidence                `json:"evidence,omitempty"`
	EvidencePacks       []agentEvidencePack            `json:"evidence_packs,omitempty"`
	RecommendedNextStep string                         `json:"recommended_next_step"`
	RerunCommand        string                         `json:"rerun_command,omitempty"`
	LogsCommand         string                         `json:"logs_command"`
	Artifacts           []preflightapi.ArtifactSummary `json:"artifacts,omitempty"`
	PolicyReasons       []string                       `json:"policy_reasons,omitempty"`
	Warnings            []agentWarning                 `json:"warnings,omitempty"`
	LogTruncated        bool                           `json:"log_truncated"`
	Spec                preflightapi.RunSpec           `json:"spec"`
}

type agentRepairPacket struct {
	Classification string               `json:"classification"`
	AgentAction    string               `json:"agent_action"`
	Summary        string               `json:"summary,omitempty"`
	RootCause      string               `json:"root_cause,omitempty"`
	PrimaryFile    string               `json:"primary_file,omitempty"`
	TargetFiles    []agentRepairTarget  `json:"target_files,omitempty"`
	FailingCommand string               `json:"failing_command"`
	FailingTest    string               `json:"failing_test,omitempty"`
	Evidence       []agentEvidence      `json:"evidence,omitempty"`
	EvidencePacks  []agentEvidencePack  `json:"evidence_packs,omitempty"`
	Commands       []agentRepairCommand `json:"commands,omitempty"`
	RerunCommand   string               `json:"rerun_command,omitempty"`
	LogsCommand    string               `json:"logs_command"`
	PolicyReasons  []string             `json:"policy_reasons,omitempty"`
	Confidence     float64              `json:"confidence"`
	ConfidenceNote string               `json:"confidence_note,omitempty"`
}
