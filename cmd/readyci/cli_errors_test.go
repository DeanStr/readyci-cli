package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/internal/syncbundlebuild"
	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

func TestRenderCLIErrorWritesOneRedactedJSONDocument(t *testing.T) {
	t.Setenv("READYCI_API_TOKEN", "secret-token-value")
	output, _ := captureStdoutForCLITest(t, func() error {
		code := renderCLIError(
			[]string{"runs", "status", "--json", "--token", "secret-token-value", "run_1"},
			cliError{
				Code:           "auth_missing",
				ExitCode:       cliExitAuthOrPolicy,
				Classification: preflightapi.ClassificationAuthMissing,
				Message:        "request failed with Bearer secret-token-value",
			},
		)
		if code != cliExitAuthOrPolicy {
			t.Fatalf("exit code = %d, want %d", code, cliExitAuthOrPolicy)
		}
		return nil
	})
	if strings.Contains(output, "secret-token-value") {
		t.Fatalf("structured error leaked token: %s", output)
	}
	var document cliErrorEnvelope
	if err := json.Unmarshal([]byte(output), &document); err != nil {
		t.Fatalf("decode structured error: %v\n%s", err, output)
	}
	if document.SchemaVersion != cliErrorSchemaVersion || document.OK || document.Error.Code != "auth_missing" {
		t.Fatalf("structured error = %+v", document)
	}
	schemaPath, err := filepath.Abs(filepath.Join("..", "..", "schema", "readyci.cli.v1.schema.json"))
	if err != nil {
		t.Fatalf("resolve CLI schema: %v", err)
	}
	schema, err := jsonschema.Compile("file://" + schemaPath)
	if err != nil {
		t.Fatalf("compile CLI schema: %v", err)
	}
	var value any
	if err := json.Unmarshal([]byte(output), &value); err != nil {
		t.Fatalf("decode schema input: %v", err)
	}
	if err := schema.Validate(value); err != nil {
		t.Fatalf("structured error failed schema validation: %v", err)
	}
}

func TestInterruptedErrorUsesConventionalSignalExitAndRecoveryCommands(t *testing.T) {
	acknowledged := false
	normalized := normalizeCLIError(cliInterruptedError{
		Signal:                   syscall.SIGTERM,
		ResourceID:               "run_1",
		CancellationAcknowledged: &acknowledged,
	})
	if normalized.ExitCode != 143 || normalized.Classification != preflightapi.ClassificationCancelled {
		t.Fatalf("normalized interruption = %+v", normalized)
	}
	if len(normalized.NextActions) != 2 || normalized.NextActions[0].Command != "readyci runs status run_1" || normalized.NextActions[1].Command != "readyci runs cancel run_1" {
		t.Fatalf("next actions = %+v", normalized.NextActions)
	}
}

func TestInterruptedErrorPreservesCustomAPIURLInRecoveryCommands(t *testing.T) {
	acknowledged := false
	normalized := normalizeCLIError(cliInterruptedError{
		Signal:                   syscall.SIGINT,
		ResourceID:               "run_1",
		APIURL:                   "https://user:secret@api.example.test/prefix/?token=secret#fragment",
		CancellationAcknowledged: &acknowledged,
	})
	wantStatus := "readyci runs status --api-url https://api.example.test/prefix run_1"
	wantCancel := "readyci runs cancel --api-url https://api.example.test/prefix run_1"
	if len(normalized.NextActions) != 2 || normalized.NextActions[0].Command != wantStatus || normalized.NextActions[1].Command != wantCancel {
		t.Fatalf("next actions = %+v", normalized.NextActions)
	}
}

func TestClassifyCommandErrorDoesNotInferFromMessageText(t *testing.T) {
	if got := classifyCommandError(errors.New("401 unauthorized")); got != preflightapi.ClassificationInternalError {
		t.Fatalf("classification = %q, want typed errors only", got)
	}
}

func TestSecretRedactorCoversHeadersURLsQueriesAndExactValues(t *testing.T) {
	t.Setenv("READYCI_API_TOKEN", "exact-secret")
	redacted := newCLISecretRedactor(nil).Redact("Bearer abc123 https://user:pass@example.test/path?token=query-secret&signature=worker-grant&X-Amz-Credential=access-id&X-Amz-Signature=s3-grant&X-Amz-Security-Token=session-grant exact-secret")
	for _, secret := range []string{"abc123", "user:pass", "query-secret", "worker-grant", "access-id", "s3-grant", "session-grant", "exact-secret"} {
		if strings.Contains(redacted, secret) {
			t.Fatalf("redacted text still contains %q: %s", secret, redacted)
		}
	}
}

func TestNormalizeCLIErrorPreservesRedactedMessageForTypedCause(t *testing.T) {
	redactor := cliSecretRedactor{exact: []string{"secret-token"}}
	err := redactor.RedactError(cliError{
		Code:           "auth_forbidden",
		ExitCode:       cliExitAuthOrPolicy,
		Classification: preflightapi.ClassificationAuthForbidden,
		Message:        "server rejected secret-token",
	})
	normalized := normalizeCLIError(err)
	if strings.Contains(normalized.Message, "secret-token") || normalized.Message != "server rejected [REDACTED]" {
		t.Fatalf("normalized message = %q", normalized.Message)
	}
	if normalized.Code != "auth_forbidden" || normalized.ExitCode != cliExitAuthOrPolicy {
		t.Fatalf("normalized typed metadata = %+v", normalized)
	}
}

func TestNormalizeCLIErrorClassifiesRateLimitsAndCancellation(t *testing.T) {
	rateLimited := normalizeCLIError(api.ResponseError{StatusCode: http.StatusTooManyRequests, Status: "429 Too Many Requests"})
	if rateLimited.Code != "rate_limited" || rateLimited.ExitCode != cliExitTemporaryUnavailable || rateLimited.Classification != preflightapi.ClassificationRateLimited || !rateLimited.Retryable {
		t.Fatalf("rate-limit error = %+v", rateLimited)
	}
	cancelled := normalizeCLIError(context.Canceled)
	if cancelled.Code != "interrupted" || cancelled.ExitCode != 130 || cancelled.Classification != preflightapi.ClassificationCancelled {
		t.Fatalf("cancelled error = %+v", cancelled)
	}
	streamCancelled := normalizeCLIError(api.EventStreamError{Err: context.Canceled})
	if streamCancelled.Code != "interrupted" || streamCancelled.ExitCode != 130 {
		t.Fatalf("cancelled stream error = %+v", streamCancelled)
	}
}

func TestNormalizeCLIErrorPreservesCodedWorkerUnavailable(t *testing.T) {
	normalized := normalizeCLIError(api.ResponseError{
		StatusCode: http.StatusServiceUnavailable,
		Status:     "503 Service Unavailable",
		Code:       "worker_unavailable",
	})
	if normalized.Code != "worker_unavailable" || normalized.Classification != preflightapi.ClassificationWorkerUnavailable || !normalized.Retryable {
		t.Fatalf("worker unavailable error = %+v", normalized)
	}
}

func TestNormalizeCLIErrorClassifiesPaymentRequiredAsBillingRequired(t *testing.T) {
	normalized := normalizeCLIError(api.ResponseError{StatusCode: http.StatusPaymentRequired, Status: "402 Payment Required"})
	if normalized.Code != "billing_subscription_required" || normalized.ExitCode != cliExitAuthOrPolicy || normalized.Classification != preflightapi.ClassificationBillingRequired {
		t.Fatalf("payment-required error = %+v", normalized)
	}
}

func TestNormalizeCLIErrorPreservesCodedRecoveryAndRetryAfter(t *testing.T) {
	normalized := normalizeCLIError(api.ResponseError{
		StatusCode:    http.StatusTooManyRequests,
		Status:        "429 Too Many Requests",
		Code:          "team_compute_capacity_exhausted",
		RetryAfter:    1500 * time.Millisecond,
		HasRetryAfter: true,
	})
	if normalized.Code != "team_compute_capacity_exhausted" || normalized.Classification != preflightapi.ClassificationCapacityExhausted || normalized.ExitCode != cliExitTemporaryUnavailable || !normalized.Retryable {
		t.Fatalf("capacity error = %+v", normalized)
	}
	if normalized.RetryAfterSeconds == nil || *normalized.RetryAfterSeconds != 2 || len(normalized.NextActions) != 1 || normalized.NextActions[0].Kind != "wait" {
		t.Fatalf("capacity recovery metadata = %+v", normalized)
	}
}

func TestNormalizeCLIErrorUsesStableAPICodesBeforeStatus(t *testing.T) {
	tests := []struct {
		code           string
		status         int
		classification string
		exitCode       int
		retryable      bool
	}{
		{"verification_session_not_terminal", http.StatusConflict, preflightapi.ClassificationStateConflict, cliExitTemporaryUnavailable, true},
		{"verification_session_not_open", http.StatusConflict, preflightapi.ClassificationStateConflict, cliExitUsageOrConfig, false},
		{"verification_source_direct_upload_unavailable", http.StatusServiceUnavailable, preflightapi.ClassificationPlatformInternalError, cliExitTemporaryUnavailable, true},
		{"verification_source_legacy_ingress_retired", http.StatusGone, preflightapi.ClassificationPreconditionFailed, cliExitUsageOrConfig, false},
		{"idempotency_key_reused", http.StatusConflict, preflightapi.ClassificationStateConflict, cliExitUsageOrConfig, false},
		{"verification_source_expired", http.StatusGone, preflightapi.ClassificationPreconditionFailed, cliExitUsageOrConfig, false},
		{"verification_source_policy_changed", http.StatusPreconditionFailed, preflightapi.ClassificationPreconditionFailed, cliExitAuthOrPolicy, false},
		{"source_upload_incomplete", http.StatusConflict, preflightapi.ClassificationStateConflict, cliExitTemporaryUnavailable, true},
		{"source_upload_mismatch", http.StatusConflict, preflightapi.ClassificationPreconditionFailed, cliExitUsageOrConfig, false},
		{"source_upload_mode_mismatch", http.StatusConflict, preflightapi.ClassificationPreconditionFailed, cliExitUsageOrConfig, false},
		{"source_upload_stale_object_removed", http.StatusConflict, preflightapi.ClassificationStateConflict, cliExitTemporaryUnavailable, true},
		{"source_upload_validation_active", http.StatusConflict, preflightapi.ClassificationStateConflict, cliExitTemporaryUnavailable, true},
		{"billing_subscription_required", http.StatusPaymentRequired, preflightapi.ClassificationBillingRequired, cliExitAuthOrPolicy, false},
		{"billing_hold_active", http.StatusLocked, preflightapi.ClassificationBillingBlocked, cliExitAuthOrPolicy, false},
		{"run_creation_rate_limited", http.StatusTooManyRequests, preflightapi.ClassificationRateLimited, cliExitTemporaryUnavailable, true},
		{"team_run_creation_disabled", http.StatusForbidden, preflightapi.ClassificationPolicyDenied, cliExitAuthOrPolicy, false},
		{"team_compute_disabled", http.StatusForbidden, preflightapi.ClassificationPolicyDenied, cliExitAuthOrPolicy, false},
		{"monthly_compute_allowance_reached", http.StatusPaymentRequired, preflightapi.ClassificationBillingRequired, cliExitAuthOrPolicy, false},
		{"plan_entitlement_required", http.StatusPaymentRequired, preflightapi.ClassificationBillingRequired, cliExitAuthOrPolicy, false},
		{"plan_entitlement_required", http.StatusForbidden, preflightapi.ClassificationPolicyDenied, cliExitAuthOrPolicy, false},
		{"plan_resource_limit_exceeded", http.StatusForbidden, preflightapi.ClassificationPolicyDenied, cliExitAuthOrPolicy, false},
		{"privacy_case_conflict", http.StatusConflict, preflightapi.ClassificationStateConflict, cliExitUsageOrConfig, false},
		{"privacy_case_blocked", http.StatusLocked, preflightapi.ClassificationStateConflict, cliExitUsageOrConfig, false},
		{"action_velocity_limit_exceeded", http.StatusTooManyRequests, preflightapi.ClassificationRateLimited, cliExitTemporaryUnavailable, true},
		{"team_pending_run_limit", http.StatusTooManyRequests, preflightapi.ClassificationCapacityExhausted, cliExitTemporaryUnavailable, true},
		{"team_compute_capacity_exhausted", http.StatusConflict, preflightapi.ClassificationCapacityExhausted, cliExitTemporaryUnavailable, true},
		{"rate_limited", http.StatusTooManyRequests, preflightapi.ClassificationRateLimited, cliExitTemporaryUnavailable, true},
		{"secret_scope_required", http.StatusForbidden, preflightapi.ClassificationAuthForbidden, cliExitAuthOrPolicy, false},
		{"api_token_limit_exceeded", http.StatusConflict, preflightapi.ClassificationStateConflict, cliExitUsageOrConfig, false},
	}
	for _, test := range tests {
		t.Run(test.code, func(t *testing.T) {
			normalized := normalizeCLIError(api.ResponseError{Code: test.code, StatusCode: test.status, Status: http.StatusText(test.status)})
			if normalized.Code != test.code || normalized.Classification != test.classification || normalized.ExitCode != test.exitCode || normalized.Retryable != test.retryable {
				t.Fatalf("normalized error = %+v", normalized)
			}
		})
	}
}

func TestNormalizeCLIErrorFailsFastOnInvalidPlanEntitlementStatus(t *testing.T) {
	normalized := normalizeCLIError(api.ResponseError{
		Code:       "plan_entitlement_required",
		StatusCode: http.StatusConflict,
		Status:     "409 Conflict",
	})
	if normalized.Code != "plan_entitlement_required" || normalized.Classification != preflightapi.ClassificationInternalError || normalized.ExitCode != cliExitInternal || normalized.Retryable {
		t.Fatalf("invalid entitlement status error = %+v, want non-retryable contract failure", normalized)
	}
}

func TestNormalizeCLIErrorFailsFastOnUnknownStableCode(t *testing.T) {
	normalized := normalizeCLIError(api.ResponseError{
		Code:       "new_server_recovery_contract",
		StatusCode: http.StatusTooManyRequests,
		Status:     "429 Too Many Requests",
	})
	if normalized.Code != "new_server_recovery_contract" || normalized.Classification != preflightapi.ClassificationInternalError || normalized.ExitCode != cliExitInternal || normalized.Retryable {
		t.Fatalf("unknown coded error = %+v, want non-retryable contract failure", normalized)
	}
}

func TestNormalizeCLIErrorCodedAuthMissingKeepsAuthenticationRecovery(t *testing.T) {
	normalized := normalizeCLIError(api.ResponseError{
		Code:       "auth_missing",
		StatusCode: http.StatusUnauthorized,
		Status:     "401 Unauthorized",
	})
	if normalized.Classification != preflightapi.ClassificationAuthMissing || len(normalized.NextActions) != 1 || normalized.NextActions[0].Kind != "authenticate" {
		t.Fatalf("coded auth error = %+v", normalized)
	}
}

func TestCLIValidationFailuresUseConfigErrorTaxonomy(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{name: "unknown setup profile", run: func() error { return setupCommand([]string{"--profile", "bogus"}) }},
		{name: "unknown init profile", run: func() error { return generateSetupConfigForTest([]string{"--profile", "bogus"}) }},
		{name: "unknown instructions print target", run: func() error {
			return agentInstructionsPrintCommand([]string{"--target", "bogus"})
		}},
		{name: "unknown instructions install target", run: func() error {
			return agentInstructionsInstallCommand([]string{"--target", "bogus"})
		}},
		{name: "unknown import source", run: func() error { return importCommand([]string{"bogus", "--json"}) }},
		{name: "unknown verification check", run: func() error {
			_, err := recommendAgentCheckWithIntent(config.ProjectConfig{Checks: map[string]config.CheckConfig{"test": {Command: "true"}}}, "missing", "", agentIntentEditLoop)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			normalized := normalizeCLIError(tc.run())
			if normalized.Code != "config_invalid" || normalized.ExitCode != cliExitUsageOrConfig || normalized.Classification != preflightapi.ClassificationConfigInvalid {
				t.Fatalf("validation error = %+v", normalized)
			}
		})
	}

	t.Run("invalid check network mode", func(t *testing.T) {
		repo := t.TempDir()
		if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte("version: 1\nproject_id: test\nchecks:\n  test:\n    command: \"true\"\n"), 0o600); err != nil {
			t.Fatalf("write project config: %v", err)
		}
		t.Chdir(repo)
		err := checkCommand([]string{"--dry-run", "--json", "--network-mode", "invalid", "test"})
		normalized := normalizeCLIError(err)
		if normalized.Code != "config_invalid" || normalized.ExitCode != cliExitUsageOrConfig || normalized.Classification != preflightapi.ClassificationConfigInvalid {
			t.Fatalf("invalid network mode = %+v (raw error: %v)", normalized, err)
		}
	})

	t.Run("invalid verification change mode", func(t *testing.T) {
		repo := t.TempDir()
		if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte("version: 1\nproject_id: test\nchecks:\n  test:\n    command: \"true\"\n"), 0o600); err != nil {
			t.Fatalf("write project config: %v", err)
		}
		t.Chdir(repo)
		err := verificationCommand([]string{"--json", "--changes", "invalid"})
		normalized := normalizeCLIError(err)
		if normalized.Code != "config_invalid" || normalized.ExitCode != cliExitUsageOrConfig || normalized.Classification != preflightapi.ClassificationConfigInvalid {
			t.Fatalf("invalid change mode = %+v (raw error: %v)", normalized, err)
		}
	})

	t.Run("invalid direct-run runner size", func(t *testing.T) {
		t.Chdir(t.TempDir())
		err := runCommand([]string{"--runner-size", "invalid", "echo true"})
		normalized := normalizeCLIError(err)
		if normalized.Code != "config_invalid" || normalized.ExitCode != cliExitUsageOrConfig || normalized.Classification != preflightapi.ClassificationConfigInvalid {
			t.Fatalf("invalid runner size = %+v (raw error: %v)", normalized, err)
		}
	})
}

func TestNormalizeCLIErrorClassifiesProjectConfigParseFailure(t *testing.T) {
	_, err := config.ParseProjectConfig([]byte("checks: ["), ".readyci.yml")
	if err == nil {
		t.Fatal("expected malformed project config to fail")
	}
	normalized := normalizeCLIError(err)
	if normalized.Code != "config_invalid" || normalized.ExitCode != cliExitUsageOrConfig || normalized.Classification != preflightapi.ClassificationConfigInvalid {
		t.Fatalf("config parse error = %+v", normalized)
	}
}

func TestSemanticProjectConfigValidationUsesConfigTaxonomy(t *testing.T) {
	err := validateProjectConfig(config.ProjectConfig{RunnerSize: "bogus"})
	if err == nil {
		t.Fatal("expected unsupported runner size to fail validation")
	}
	normalized := normalizeCLIError(err)
	if normalized.Code != "config_invalid" || normalized.ExitCode != cliExitUsageOrConfig || normalized.Classification != preflightapi.ClassificationConfigInvalid {
		t.Fatalf("semantic config error = %+v (raw error: %v)", normalized, err)
	}
}

func TestStructuredErrorDetectionIgnoresRunCommandArguments(t *testing.T) {
	if invocationRequestsStructuredOutput([]string{"run", "tool", "--json"}) {
		t.Fatal("run payload flag was mistaken for Ready CI structured output")
	}
	if !invocationRequestsStructuredOutput([]string{"runs", "status", "--json", "run_1"}) {
		t.Fatal("runs status --json was not detected")
	}
	if invocationRequestsStructuredOutput([]string{"secrets", "set", "NAME", "--json"}) {
		t.Fatal("secret value was mistaken for Ready CI structured output")
	}
	if !invocationRequestsStructuredOutput([]string{"artifacts", "apply", "run_1", "--json"}) {
		t.Fatal("artifacts apply --json was not detected")
	}
}

func TestInvocationCommandNeverTreatsFlagValuesAsSubcommands(t *testing.T) {
	if got := invocationCommand([]string{"verify", "--token", "secret-token", "--json"}); got != "verify" {
		t.Fatalf("verification command = %q, want root only", got)
	}
	if got := invocationCommand([]string{"runs", "status", "--token", "secret-token", "--json", "run_1"}); got != "runs status" {
		t.Fatalf("runs command = %q, want namespace and known subcommand", got)
	}
	if got := invocationCommand([]string{"runs", "--token", "secret-token", "--json"}); got != "runs" {
		t.Fatalf("malformed runs command leaked a flag value: %q", got)
	}
}

func TestSourceCompatibilityErrorsAreActionableConfigurationFailures(t *testing.T) {
	for _, err := range []error{syncbundlebuild.ErrUnsupportedRepository, syncbundle.ValidateExcludePatterns([]string{"[broken"})} {
		result := normalizeCLIError(err)
		if result.ExitCode != cliExitUsageOrConfig || result.Classification != preflightapi.ClassificationConfigInvalid || result.Retryable {
			t.Fatalf("source incompatibility was misclassified: %+v", result)
		}
	}
}
