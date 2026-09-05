package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

const cliErrorSchemaVersion = "readyci.cli.v1"

type cliError struct {
	Code              string
	ExitCode          int
	Classification    string
	Retryable         bool
	RetryAfterSeconds *int
	Message           string
	NextActions       []cliNextAction
	Cause             error
}

func (e cliError) Error() string {
	if strings.TrimSpace(e.Message) != "" {
		return e.Message
	}
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return e.Code
}

func (e cliError) Unwrap() error { return e.Cause }

type cliNextAction struct {
	Kind    string `json:"kind"`
	Command string `json:"command,omitempty"`
}

type cliErrorEnvelope struct {
	SchemaVersion string       `json:"schema_version"`
	Command       string       `json:"command"`
	OK            bool         `json:"ok"`
	Error         cliErrorJSON `json:"error"`
}

type cliErrorJSON struct {
	Code                     string          `json:"code"`
	Classification           string          `json:"classification"`
	Retryable                bool            `json:"retryable"`
	RetryAfterSeconds        *int            `json:"retry_after_seconds,omitempty"`
	Message                  string          `json:"message"`
	ResourceID               string          `json:"resource_id,omitempty"`
	CancellationAcknowledged *bool           `json:"cancellation_acknowledged,omitempty"`
	NextActions              []cliNextAction `json:"next_actions"`
}

type cliInterruptedError struct {
	Signal                   os.Signal
	ResourceID               string
	APIURL                   string
	CancellationAcknowledged *bool
	CancellationError        error
}

func (e cliInterruptedError) Error() string {
	message := "interrupted"
	if e.Signal != nil {
		message += " by " + e.Signal.String()
	}
	if e.ResourceID != "" {
		if e.CancellationAcknowledged != nil && *e.CancellationAcknowledged {
			message += fmt.Sprintf("; remote cancellation acknowledged for %s", e.ResourceID)
		} else if e.CancellationError != nil {
			message += fmt.Sprintf("; remote cancellation was not acknowledged for %s: %v", e.ResourceID, e.CancellationError)
		} else {
			message += fmt.Sprintf("; remote status is unknown for %s", e.ResourceID)
		}
	}
	return message
}

func cliConfigError(message string, cause error) error {
	return cliError{Code: "config_invalid", ExitCode: cliExitUsageOrConfig, Classification: preflightapi.ClassificationConfigInvalid, Message: message, Cause: cause}
}

func cliAuthMissingError(message string, cause error) cliError {
	return cliError{
		Code:           "auth_missing",
		ExitCode:       cliExitAuthOrPolicy,
		Classification: preflightapi.ClassificationAuthMissing,
		Message:        message,
		Cause:          cause,
		NextActions:    []cliNextAction{{Kind: "authenticate", Command: "readyci login"}},
	}
}

func normalizeCLIError(err error) cliError {
	var redacted redactedCLIError
	if errors.As(err, &redacted) {
		normalized := normalizeCLIError(redacted.cause)
		normalized.Message = redacted.message
		normalized.Cause = err
		return normalized
	}
	var typed cliError
	if errors.As(err, &typed) {
		if typed.ExitCode == 0 {
			typed.ExitCode = cliExitInternal
		}
		return typed
	}
	var interrupted cliInterruptedError
	if errors.As(err, &interrupted) {
		exitCode := 130
		if interrupted.Signal != nil && interrupted.Signal.String() == "terminated" {
			exitCode = 143
		}
		next := []cliNextAction{}
		if interrupted.ResourceID != "" && (interrupted.CancellationAcknowledged == nil || !*interrupted.CancellationAcknowledged) {
			next = append(next,
				cliNextAction{Kind: "inspect", Command: runRecoveryCommand(interrupted.APIURL, "status", interrupted.ResourceID)},
				cliNextAction{Kind: "cancel", Command: runRecoveryCommand(interrupted.APIURL, "cancel", interrupted.ResourceID)},
			)
		}
		return cliError{Code: "interrupted", ExitCode: exitCode, Classification: preflightapi.ClassificationCancelled, Retryable: true, Message: interrupted.Error(), NextActions: next, Cause: err}
	}
	if errors.Is(err, context.Canceled) {
		return cliError{Code: "interrupted", ExitCode: 130, Classification: preflightapi.ClassificationCancelled, Retryable: true, Message: "interrupted", Cause: err}
	}
	var streamErr api.EventStreamError
	if errors.As(err, &streamErr) {
		return cliError{Code: "verification_unknown", ExitCode: cliExitVerificationUnknown, Classification: preflightapi.ClassificationVerificationUnknown, Retryable: true, Message: err.Error(), Cause: err}
	}
	var responseErr api.ResponseError
	if errors.As(err, &responseErr) {
		if normalized, ok := normalizeCodedAPIError(responseErr, err); ok {
			return normalized
		}
		if code := strings.TrimSpace(responseErr.Code); code != "" {
			return cliError{
				Code:           code,
				ExitCode:       cliExitInternal,
				Classification: preflightapi.ClassificationInternalError,
				Message:        err.Error(),
				Cause:          err,
			}
		}
		switch responseErr.StatusCode {
		case http.StatusBadRequest:
			return cliError{Code: "request_invalid", ExitCode: cliExitUsageOrConfig, Classification: preflightapi.ClassificationConfigInvalid, Message: err.Error(), Cause: err}
		case http.StatusPaymentRequired:
			return cliError{Code: "billing_subscription_required", ExitCode: cliExitAuthOrPolicy, Classification: preflightapi.ClassificationBillingRequired, Message: err.Error(), Cause: err}
		case http.StatusRequestTimeout:
			return apiCLIError(responseErr, "transport_timeout", preflightapi.ClassificationTransportFailure, cliExitVerificationUnknown, true, err)
		case http.StatusUnauthorized:
			normalized := cliAuthMissingError("Ready CI authentication is required.", err)
			return normalized
		case http.StatusForbidden:
			return cliError{Code: "auth_forbidden", ExitCode: cliExitAuthOrPolicy, Classification: preflightapi.ClassificationAuthForbidden, Message: "Ready CI denied this operation.", Cause: err}
		case http.StatusNotFound:
			return cliError{Code: "not_found", ExitCode: cliExitUsageOrConfig, Classification: preflightapi.ClassificationConfigInvalid, Message: err.Error(), Cause: err}
		case http.StatusConflict, http.StatusLocked:
			return cliError{Code: "state_conflict", ExitCode: cliExitUsageOrConfig, Classification: preflightapi.ClassificationStateConflict, Message: err.Error(), Cause: err}
		case http.StatusTooManyRequests:
			return apiCLIError(responseErr, "rate_limited", preflightapi.ClassificationRateLimited, cliExitTemporaryUnavailable, true, err)
		default:
			if responseErr.StatusCode >= http.StatusInternalServerError {
				return apiCLIError(responseErr, "platform_internal", preflightapi.ClassificationPlatformInternalError, cliExitInternal, true, err)
			}
			if responseErr.StatusCode >= http.StatusBadRequest {
				return cliError{Code: "request_invalid", ExitCode: cliExitUsageOrConfig, Classification: preflightapi.ClassificationConfigInvalid, Message: err.Error(), Cause: err}
			}
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return cliError{Code: "transport_timeout", ExitCode: cliExitVerificationUnknown, Classification: preflightapi.ClassificationTransportFailure, Retryable: true, Message: err.Error(), Cause: err}
	}
	var netErr net.Error
	var urlErr *url.Error
	if errors.As(err, &netErr) || errors.As(err, &urlErr) {
		return cliError{Code: "transport_failure", ExitCode: cliExitVerificationUnknown, Classification: preflightapi.ClassificationTransportFailure, Retryable: true, Message: err.Error(), Cause: err}
	}
	var exitErr exitCodeError
	if errors.As(err, &exitErr) {
		return cliError{Code: "command_failed", ExitCode: exitErr.code, Classification: preflightapi.ClassificationTestFailed, Message: err.Error(), Cause: err}
	}
	var configErr config.Error
	if errors.As(err, &configErr) {
		return cliError{Code: "config_invalid", ExitCode: cliExitUsageOrConfig, Classification: preflightapi.ClassificationConfigInvalid, Message: err.Error(), Cause: err}
	}
	return cliError{Code: "internal_error", ExitCode: cliExitInternal, Classification: preflightapi.ClassificationInternalError, Message: err.Error(), Cause: err}
}

func normalizeCodedAPIError(responseErr api.ResponseError, cause error) (cliError, bool) {
	code := strings.TrimSpace(responseErr.Code)
	if code == "" {
		return cliError{}, false
	}
	if code == "auth_missing" {
		normalized := cliAuthMissingError("Ready CI authentication is required.", cause)
		normalized.Code = code
		return normalized, true
	}
	if code == "plan_entitlement_required" {
		switch responseErr.StatusCode {
		case http.StatusPaymentRequired:
			return apiCLIError(responseErr, code, preflightapi.ClassificationBillingRequired, cliExitAuthOrPolicy, false, cause), true
		case http.StatusForbidden:
			return apiCLIError(responseErr, code, preflightapi.ClassificationPolicyDenied, cliExitAuthOrPolicy, false, cause), true
		default:
			// This stable code has a two-status contract. An unexpected pairing is
			// a server/client contract error and must fail closed below.
			return cliError{}, false
		}
	}
	type metadata struct {
		classification string
		exitCode       int
		retryable      bool
	}
	values := map[string]metadata{
		"worker_unavailable":                            {preflightapi.ClassificationWorkerUnavailable, cliExitTemporaryUnavailable, true},
		"verification_source_direct_upload_unavailable": {preflightapi.ClassificationPlatformInternalError, cliExitTemporaryUnavailable, true},
		"verification_source_legacy_ingress_retired":    {preflightapi.ClassificationPreconditionFailed, cliExitUsageOrConfig, false},
		"verification_session_not_terminal":             {preflightapi.ClassificationStateConflict, cliExitTemporaryUnavailable, true},
		"verification_session_not_open":                 {preflightapi.ClassificationStateConflict, cliExitUsageOrConfig, false},
		"idempotency_key_reused":                        {preflightapi.ClassificationStateConflict, cliExitUsageOrConfig, false},
		"verification_task_mismatch":                    {preflightapi.ClassificationConfigInvalid, cliExitUsageOrConfig, false},
		"verification_source_expired":                   {preflightapi.ClassificationPreconditionFailed, cliExitUsageOrConfig, false},
		"verification_source_policy_changed":            {preflightapi.ClassificationPreconditionFailed, cliExitAuthOrPolicy, false},
		"source_upload_incomplete":                      {preflightapi.ClassificationStateConflict, cliExitTemporaryUnavailable, true},
		"source_upload_mismatch":                        {preflightapi.ClassificationPreconditionFailed, cliExitUsageOrConfig, false},
		"source_upload_mode_mismatch":                   {preflightapi.ClassificationPreconditionFailed, cliExitUsageOrConfig, false},
		"source_upload_stale_object_removed":            {preflightapi.ClassificationStateConflict, cliExitTemporaryUnavailable, true},
		"source_upload_validation_active":               {preflightapi.ClassificationStateConflict, cliExitTemporaryUnavailable, true},
		"project_policy_conflict":                       {preflightapi.ClassificationPreconditionFailed, cliExitAuthOrPolicy, false},
		"privacy_case_conflict":                         {preflightapi.ClassificationStateConflict, cliExitUsageOrConfig, false},
		"privacy_case_blocked":                          {preflightapi.ClassificationStateConflict, cliExitUsageOrConfig, false},
		"billing_subscription_required":                 {preflightapi.ClassificationBillingRequired, cliExitAuthOrPolicy, false},
		"billing_hold_active":                           {preflightapi.ClassificationBillingBlocked, cliExitAuthOrPolicy, false},
		"billing_hold_review_required":                  {preflightapi.ClassificationBillingBlocked, cliExitAuthOrPolicy, false},
		"run_creation_rate_limited":                     {preflightapi.ClassificationRateLimited, cliExitTemporaryUnavailable, true},
		"billing_customer_not_found":                    {preflightapi.ClassificationBillingRequired, cliExitAuthOrPolicy, false},
		"team_run_creation_disabled":                    {preflightapi.ClassificationPolicyDenied, cliExitAuthOrPolicy, false},
		"team_compute_disabled":                         {preflightapi.ClassificationPolicyDenied, cliExitAuthOrPolicy, false},
		"monthly_compute_allowance_reached":             {preflightapi.ClassificationBillingRequired, cliExitAuthOrPolicy, false},
		"plan_resource_limit_exceeded":                  {preflightapi.ClassificationPolicyDenied, cliExitAuthOrPolicy, false},
		"action_velocity_limit_exceeded":                {preflightapi.ClassificationRateLimited, cliExitTemporaryUnavailable, true},
		"team_pending_run_limit":                        {preflightapi.ClassificationCapacityExhausted, cliExitTemporaryUnavailable, true},
		"team_compute_capacity_exhausted":               {preflightapi.ClassificationCapacityExhausted, cliExitTemporaryUnavailable, true},
		"rate_limited":                                  {preflightapi.ClassificationRateLimited, cliExitTemporaryUnavailable, true},
		"secret_scope_required":                         {preflightapi.ClassificationAuthForbidden, cliExitAuthOrPolicy, false},
		"auth_scope_required":                           {preflightapi.ClassificationAuthForbidden, cliExitAuthOrPolicy, false},
		"auth_forbidden":                                {preflightapi.ClassificationAuthForbidden, cliExitAuthOrPolicy, false},
		"request_invalid":                               {preflightapi.ClassificationConfigInvalid, cliExitUsageOrConfig, false},
		"request_timeout":                               {preflightapi.ClassificationTransportFailure, cliExitVerificationUnknown, true},
		"not_found":                                     {preflightapi.ClassificationConfigInvalid, cliExitUsageOrConfig, false},
		"method_not_allowed":                            {preflightapi.ClassificationInternalError, cliExitInternal, false},
		"resource_gone":                                 {preflightapi.ClassificationPreconditionFailed, cliExitUsageOrConfig, false},
		"precondition_failed":                           {preflightapi.ClassificationPreconditionFailed, cliExitUsageOrConfig, false},
		"request_too_large":                             {preflightapi.ClassificationSyncTooLarge, cliExitRemoteFailure, false},
		"platform_internal":                             {preflightapi.ClassificationPlatformInternalError, cliExitInternal, true},
		"operation_unsupported":                         {preflightapi.ClassificationPlatformInternalError, cliExitInternal, false},
		"billing_subscription_exists":                   {preflightapi.ClassificationStateConflict, cliExitUsageOrConfig, false},
		"api_token_limit_exceeded":                      {preflightapi.ClassificationStateConflict, cliExitUsageOrConfig, false},
		"run_event_limit_exceeded":                      {preflightapi.ClassificationPreconditionFailed, cliExitUsageOrConfig, false},
		"web_identity_conflict":                         {preflightapi.ClassificationStateConflict, cliExitUsageOrConfig, false},
		"sso_connection_conflict":                       {preflightapi.ClassificationStateConflict, cliExitUsageOrConfig, false},
		"maintenance_job_already_running":               {preflightapi.ClassificationStateConflict, cliExitUsageOrConfig, false},
		"stale_run_lease":                               {preflightapi.ClassificationPreconditionFailed, cliExitUsageOrConfig, false},
		"worker_has_active_runs":                        {preflightapi.ClassificationStateConflict, cliExitUsageOrConfig, false},
		"worker_not_draining":                           {preflightapi.ClassificationStateConflict, cliExitUsageOrConfig, false},
		"worker_recently_seen":                          {preflightapi.ClassificationStateConflict, cliExitUsageOrConfig, false},
		"worker_release_conflict":                       {preflightapi.ClassificationStateConflict, cliExitUsageOrConfig, false},
		"state_conflict":                                {preflightapi.ClassificationStateConflict, cliExitUsageOrConfig, false},
		"resource_locked":                               {preflightapi.ClassificationStateConflict, cliExitUsageOrConfig, false},
	}
	value, ok := values[code]
	if !ok {
		return cliError{}, false
	}
	return apiCLIError(responseErr, code, value.classification, value.exitCode, value.retryable, cause), true
}

func apiCLIError(responseErr api.ResponseError, code, classification string, exitCode int, retryable bool, cause error) cliError {
	normalized := cliError{Code: code, ExitCode: exitCode, Classification: classification, Retryable: retryable, Message: cause.Error(), Cause: cause}
	if retryable && exitCode == cliExitTemporaryUnavailable {
		normalized.NextActions = []cliNextAction{{Kind: "wait"}}
	}
	if responseErr.HasRetryAfter {
		seconds := int((responseErr.RetryAfter + time.Second - 1) / time.Second)
		normalized.RetryAfterSeconds = &seconds
	}
	return normalized
}

func renderCLIError(args []string, err error) int {
	normalized := normalizeCLIError(err)
	redactor := newCLISecretRedactor(args)
	normalized.Message = redactor.Redact(normalized.Message)
	if invocationRequestsStructuredOutput(args) {
		nextActions := normalized.NextActions
		if nextActions == nil {
			nextActions = []cliNextAction{}
		}
		document := cliErrorEnvelope{
			SchemaVersion: cliErrorSchemaVersion,
			Command:       invocationCommand(args),
			OK:            false,
			Error: cliErrorJSON{
				Code:              normalized.Code,
				Classification:    normalized.Classification,
				Retryable:         normalized.Retryable,
				RetryAfterSeconds: normalized.RetryAfterSeconds,
				Message:           normalized.Message,
				NextActions:       nextActions,
			},
		}
		var interrupted cliInterruptedError
		if errors.As(err, &interrupted) {
			document.Error.ResourceID = interrupted.ResourceID
			document.Error.CancellationAcknowledged = interrupted.CancellationAcknowledged
		}
		encoder := json.NewEncoder(os.Stdout)
		if !invocationRequestsJSONL(args) {
			encoder.SetIndent("", "  ")
		}
		if encodeErr := encoder.Encode(document); encodeErr != nil {
			fmt.Fprintln(os.Stderr, "readyci: could not encode structured error")
		}
	} else {
		fmt.Fprintln(os.Stderr, "readyci:", normalized.Message)
	}
	return normalized.ExitCode
}

func invocationRequestsStructuredOutput(args []string) bool {
	if !invocationSupportsStructuredOutput(args) {
		return false
	}
	for _, arg := range args {
		if arg == "--json" || arg == "--json=true" || arg == "--jsonl" || arg == "--jsonl=true" {
			return true
		}
	}
	return false
}

func invocationSupportsStructuredOutput(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "start", "setup", "config", "plan", "import", "sync", "doctor", "usage", "feedback",
		"affected", "touched", "groups", "runs", "verify", "sessions", "agent", "artifacts", "fixtures", "check", "admin":
		return true
	case "project":
		return len(args) > 1 && args[1] == "policy"
	default:
		return false
	}
}

func invocationRequestsJSONL(args []string) bool {
	for _, arg := range args {
		if arg == "--jsonl" || arg == "--jsonl=true" {
			return true
		}
	}
	return false
}

func invocationCommand(args []string) string {
	if len(args) == 0 {
		return ""
	}
	root := args[0]
	if len(args) < 2 || !knownInvocationSubcommand(root, args[1]) {
		return root
	}
	return root + " " + args[1]
}

func knownInvocationSubcommand(root, candidate string) bool {
	known := map[string]map[string]bool{
		"admin":     {"queue": true, "runs": true, "storage": true, "teams": true, "tokens": true, "workers": true},
		"affected":  {"audit": true, "explain": true, "profile": true, "suggest": true, "validate": true},
		"agent":     {"artifacts": true, "explain": true, "instructions": true, "plan": true, "setup": true, "token": true},
		"artifacts": {"apply": true, "download": true, "ls": true},
		"config":    {"explain": true, "validate": true},
		"fixtures":  {"update": true},
		"groups":    {"cancel": true, "latest": true, "logs": true, "status": true},
		"project":   {"policy": true},
		"runs":      {"artifacts": true, "cancel": true, "explain": true, "latest": true, "logs": true, "status": true},
		"sessions":  {"cancel": true, "get": true, "rerun": true, "retry": true, "watch": true},
	}
	return known[root][candidate]
}

type cliSecretRedactor struct{ exact []string }

type redactedCLIError struct {
	message string
	cause   error
}

func (e redactedCLIError) Error() string { return e.message }
func (e redactedCLIError) Unwrap() error { return e.cause }

var (
	bearerCredentialPattern = regexp.MustCompile(`(?i)(authorization\s*:\s*bearer\s+|bearer\s+)[^\s,;]+`)
	urlCredentialPattern    = regexp.MustCompile(`(https?://)[^/@\s]+@`)
	secretQueryPattern      = regexp.MustCompile(`(?i)([?&](?:token|api[_-]?key|access[_-]?token|secret|signature|x-amz-signature|x-amz-credential|x-amz-security-token)=)[^&#\s]+`)
)

func newCLISecretRedactor(args []string) cliSecretRedactor {
	values := []string{os.Getenv("READYCI_API_TOKEN"), os.Getenv("READYCI_ADMIN_TOKEN")}
	for index, arg := range args {
		if (arg == "--token" || arg == "--admin-token") && index+1 < len(args) {
			values = append(values, args[index+1])
		}
		if strings.HasPrefix(arg, "--token=") || strings.HasPrefix(arg, "--admin-token=") {
			values = append(values, strings.SplitN(arg, "=", 2)[1])
		}
	}
	redactor := cliSecretRedactor{}
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if len(value) >= 4 && !seen[value] {
			seen[value] = true
			redactor.exact = append(redactor.exact, value)
		}
	}
	return redactor
}

func (r cliSecretRedactor) Redact(value string) string {
	for _, secret := range r.exact {
		value = strings.ReplaceAll(value, secret, "[REDACTED]")
	}
	value = bearerCredentialPattern.ReplaceAllString(value, "${1}[REDACTED]")
	value = urlCredentialPattern.ReplaceAllString(value, "${1}[REDACTED]@")
	return secretQueryPattern.ReplaceAllString(value, "${1}[REDACTED]")
}

func (r cliSecretRedactor) RedactError(err error) error {
	if err == nil {
		return nil
	}
	return redactedCLIError{message: r.Redact(err.Error()), cause: err}
}
