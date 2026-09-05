package main

import (
	"errors"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func isExitLikeError(err error) bool {
	var exitErr exitCodeError
	return errors.As(err, &exitErr)
}

func agentExitError(err error) error {
	var exitErr exitCodeError
	if errors.As(err, &exitErr) {
		if exitErr.code == 0 {
			return nil
		}
		return exitCodeError{code: exitErr.code}
	}
	return exitCodeError{code: 1}
}

func agentRunErrorCoveredByReport(report agentVerifyReport, err error) bool {
	return errors.Is(err, errRunStreamEndedBeforeTerminal) &&
		!report.Terminal &&
		report.Classification == preflightapi.ClassificationPending &&
		report.AgentAction == preflightapi.AgentActionWait
}

func agentCommandErrorReport(classification string, err error) agentVerifyReport {
	report := agentVerifyReport{
		SchemaVersion:  agentSchemaVersion,
		Status:         "error",
		Terminal:       true,
		Classification: classification,
		Retryable:      retryableClassification(classification),
		AgentAction:    actionForClassification(classification),
		Summary:        err.Error(),
		Checks:         []agentAffectedCheck{},
		Runs:           []agentVerifyRunReport{},
		Error:          err.Error(),
	}
	report.CLIExitCode = agentReportProcessExitCode(report)
	return report
}

func agentReportProcessExitCode(report agentVerifyReport) int {
	switch report.Classification {
	case "", preflightapi.ClassificationSuccess:
		return cliExitSuccess
	case preflightapi.ClassificationConfigInvalid,
		preflightapi.ClassificationStateConflict,
		preflightapi.ClassificationPreconditionFailed:
		return cliExitUsageOrConfig
	case preflightapi.ClassificationAuthMissing,
		preflightapi.ClassificationAuthForbidden,
		preflightapi.ClassificationPolicyDenied,
		preflightapi.ClassificationBillingRequired,
		preflightapi.ClassificationBillingBlocked,
		preflightapi.ClassificationProjectNotFound:
		return cliExitAuthOrPolicy
	case preflightapi.ClassificationCapacityExhausted,
		preflightapi.ClassificationRateLimited,
		preflightapi.ClassificationWorkerUnavailable:
		return cliExitTemporaryUnavailable
	case preflightapi.ClassificationTransportFailure,
		preflightapi.ClassificationVerificationUnknown:
		return cliExitVerificationUnknown
	case preflightapi.ClassificationInternalError,
		preflightapi.ClassificationWorkerInternalError,
		preflightapi.ClassificationPlatformInternalError:
		return cliExitInternal
	case preflightapi.ClassificationTestFailed,
		preflightapi.ClassificationLintFailed,
		preflightapi.ClassificationBuildFailed,
		preflightapi.ClassificationTypecheckFailed,
		preflightapi.ClassificationTimeout,
		preflightapi.ClassificationCancelled,
		preflightapi.ClassificationNetworkNotAllowed,
		preflightapi.ClassificationDependencyDownloadFailed,
		preflightapi.ClassificationDependencySetupFailed,
		preflightapi.ClassificationServiceSetupFailed,
		preflightapi.ClassificationSyncFailed,
		preflightapi.ClassificationSyncTooLarge,
		preflightapi.ClassificationCacheStorageExceeded,
		preflightapi.ClassificationWorkerCapabilityMismatch:
		return cliExitRemoteFailure
	}
	if !report.Terminal {
		return cliExitSuccess
	}
	if report.Status == preflightapi.StatusSucceeded {
		return cliExitSuccess
	}
	return cliExitRemoteFailure
}

func exitCodeValue(exitCode *int) int {
	if exitCode == nil {
		return 0
	}
	return *exitCode
}

func intPtr(value int) *int {
	return &value
}
