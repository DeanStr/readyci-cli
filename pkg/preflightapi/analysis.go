package preflightapi

const (
	ClassificationSuccess                  = "success"
	ClassificationPending                  = "pending"
	ClassificationTestFailed               = "test_failed"
	ClassificationLintFailed               = "lint_failed"
	ClassificationBuildFailed              = "build_failed"
	ClassificationTypecheckFailed          = "typecheck_failed"
	ClassificationConfigInvalid            = "config_invalid"
	ClassificationAuthMissing              = "auth_missing"
	ClassificationAuthForbidden            = "auth_forbidden"
	ClassificationPolicyDenied             = "policy_denied"
	ClassificationStateConflict            = "state_conflict"
	ClassificationPreconditionFailed       = "precondition_failed"
	ClassificationCapacityExhausted        = "capacity_exhausted"
	ClassificationBillingRequired          = "billing_required"
	ClassificationBillingBlocked           = "billing_blocked"
	ClassificationRateLimited              = "rate_limited"
	ClassificationProjectNotFound          = "project_not_found"
	ClassificationSyncFailed               = "sync_failed"
	ClassificationSyncTooLarge             = "sync_too_large"
	ClassificationCacheStorageExceeded     = "cache_storage_exceeded"
	ClassificationWorkerUnavailable        = "worker_unavailable"
	ClassificationWorkerCapabilityMismatch = "worker_capability_mismatch"
	ClassificationTransportFailure         = "transport_failure"
	ClassificationVerificationUnknown      = "verification_unknown"
	ClassificationNetworkNotAllowed        = "network_not_allowed"
	ClassificationDependencyDownloadFailed = "dependency_download_failed"
	ClassificationDependencySetupFailed    = "dependency_setup_failed"
	ClassificationEnvironmentSetupFailed   = "environment_setup_failed"
	ClassificationServiceSetupFailed       = "service_setup_failed"
	ClassificationTimeout                  = "timeout"
	ClassificationCancelled                = "cancelled"
	ClassificationInternalError            = "internal_error"
	ClassificationWorkerInternalError      = "worker_internal_error"
	ClassificationPlatformInternalError    = "platform_internal_error"
)

const (
	AgentActionNone              = "none"
	AgentActionFixCode           = "fix_code"
	AgentActionAskForCredentials = "ask_for_credentials"
	AgentActionRetry             = "retry"
	AgentActionRetryOrSplit      = "retry_or_split"
	AgentActionReduceScope       = "reduce_scope"
	AgentActionWait              = "wait"
	AgentActionWaitOrReportInfra = "wait_or_report_infra"
	AgentActionReportSetup       = "report_setup"
	AgentActionStop              = "stop"
)

type RunFailureEvidence struct {
	EventID int64  `json:"event_id,omitempty"`
	Stream  string `json:"stream,omitempty"`
	Seq     int    `json:"seq"`
	Line    int    `json:"line,omitempty"`
	Event   string `json:"event,omitempty"`
	Path    string `json:"path,omitempty"`
	Text    string `json:"text,omitempty"`
}

type RunFailureAnalysis struct {
	Classification string               `json:"classification"`
	Confidence     float64              `json:"confidence"`
	Retryable      bool                 `json:"retryable"`
	AgentAction    string               `json:"agent_action"`
	Summary        string               `json:"summary,omitempty"`
	PrimaryFile    string               `json:"primary_file,omitempty"`
	Evidence       []RunFailureEvidence `json:"evidence,omitempty"`
}
