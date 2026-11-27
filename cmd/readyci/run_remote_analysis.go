package main

import (
	"errors"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func trimStreamMarkerBuffer(buffer string) string {
	const maxMarkerBufferRunes = 4096
	runes := []rune(buffer)
	if len(runes) <= maxMarkerBufferRunes {
		return buffer
	}
	return string(runes[len(runes)-maxMarkerBufferRunes:])
}

func analyzeRemoteRunFailure(result remoteRunResult, err error, command string) preflightapi.RunFailureAnalysis {
	if err != nil && !isExitLikeError(err) {
		kind := classifyCommandError(err)
		return preflightapi.RunFailureAnalysis{
			Classification: kind,
			Confidence:     0.7,
			Retryable:      retryableClassification(kind),
			AgentAction:    actionForClassification(kind),
			Summary:        err.Error(),
		}
	}
	if strings.TrimSpace(result.Classification) != "" {
		summary := strings.TrimSpace(result.FailureSummary)
		if summary == "" {
			summary = strings.TrimSpace(result.FailureReason)
		}
		analysis := preflightapi.RunFailureAnalysis{
			Classification: strings.TrimSpace(result.Classification),
			Confidence:     0.9,
			Retryable:      result.Retryable,
			AgentAction:    defaultString(strings.TrimSpace(result.AgentAction), actionForClassification(result.Classification)),
			Summary:        summary,
			PrimaryFile:    strings.TrimSpace(result.PrimaryFile),
		}
		if result.Classification == preflightapi.ClassificationTestFailed || result.Classification == preflightapi.ClassificationEnvironmentSetupFailed {
			events := result.Events
			if result.Classification == preflightapi.ClassificationTestFailed {
				events = repositoryTestOutputEvents(events)
			}
			exitCode := runFailureExitCode(result, result.Status)
			observed := localAnalyzeRunFailureWithNetworkMode(result.Status, command, exitCode, events, result.FailureReason, result.NetworkMode)
			if observed.Classification == analysis.Classification {
				// Hosted verdict and guidance stay authoritative. Carry locally
				// selected evidence only when its classification agrees with them.
				analysis.Evidence = append([]preflightapi.RunFailureEvidence(nil), observed.Evidence...)
			}
			if result.Classification == preflightapi.ClassificationTestFailed {
				if refined, ok := refinedObservedTestFailureSummary(analysis.Classification, summary, observed); ok {
					analysis.Summary, analysis.PrimaryFile = refined, observed.PrimaryFile
				}
			}
		}
		return analysis
	}
	status := defaultString(result.Status, runFailureStatusFromError(err))
	exitCode := runFailureExitCode(result, status)
	return localAnalyzeRunFailureWithNetworkMode(status, command, exitCode, result.Events, result.FailureReason, result.NetworkMode)
}

func runFailureExitCode(result remoteRunResult, status string) *int {
	if preflightapi.IsTerminalRunStatus(status) {
		code := result.ExitCode
		return &code
	}
	if result.CommandExited {
		code := result.CommandExitCode
		return &code
	}
	return nil
}

func runFailureStatusFromError(err error) string {
	var terminalErr terminalRunError
	if !errors.As(err, &terminalErr) {
		return ""
	}
	return runStatusForTerminalEvent(terminalErr.eventType)
}

func runFailureDiagnosisHint(classification string) string {
	switch classification {
	case preflightapi.ClassificationCacheStorageExceeded:
		return "prune or purge the Ready CI cache, remove bulky cache paths such as build outputs, then rerun"
	case preflightapi.ClassificationDependencyDownloadFailed:
		return "retry once; if it repeats, inspect dependency sources and use readyci warm --check dependencies with an explicit network allowlist"
	case preflightapi.ClassificationDependencySetupFailed:
		return "install declared test or development dependencies, or update the Ready CI check command to include the project dependency group"
	case preflightapi.ClassificationEnvironmentSetupFailed:
		return "check exact runtimes, required executables and environment variables, platform, and filesystem support; for storage errors inspect bytes and inodes on the failed path's filesystem before rerunning"
	case preflightapi.ClassificationWorkerInternalError:
		return "retry once; if it repeats, inspect worker logs because the command appears to have been terminated outside normal test output"
	case preflightapi.ClassificationTimeout:
		return "increase the check timeout, split the workload, or use a larger runner size"
	case preflightapi.ClassificationNetworkNotAllowed:
		return "the run appears to need network access; use an explicit network_mode or prewarm dependencies before running with network none"
	case preflightapi.ClassificationWorkerUnavailable, preflightapi.ClassificationWorkerCapabilityMismatch:
		return "run readyci doctor for this check and verify a fresh worker advertises the required image, region, runner size, docker, and network mode"
	case preflightapi.ClassificationTransportFailure, preflightapi.ClassificationVerificationUnknown:
		return "retry after checking API connectivity; if the run may still be active, inspect it from the dashboard or readyci runs"
	default:
		return ""
	}
}

func runFailureAnalysisTextLooksUseful(text string) bool {
	text = trimRunFailureAnalysisSearchText(text)
	lower := strings.ToLower(text)
	if strings.Contains(lower, "--- fail:") || strings.Contains(lower, "assertionerror") {
		return true
	}
	if strings.Contains(lower, "missing go.sum entry") ||
		strings.Contains(lower, "updates to go.mod needed") ||
		strings.Contains(lower, "pytestunknownmarkwarning") ||
		strings.Contains(lower, "unknown pytest.mark") {
		return true
	}
	if runFailureAnalysisContainsAny(lower, "expected", "want") && runFailureAnalysisContainsAny(lower, "got", "received", "actual") {
		return true
	}
	for _, needle := range []string{
		"error", "failed", "failure", "fatal", "panic", "exception",
		"timed out", "timeout", "permission denied", "terminated", "killed",
	} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func runFailureAnalysisContainsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func trimRunFailureAnalysisSearchText(text string) string {
	const maxSearchRunes = runFailureAnalysisDataLimit * 2
	runes := []rune(text)
	if len(runes) <= maxSearchRunes {
		return text
	}
	half := maxSearchRunes / 2
	return string(runes[:half]) + "\n" + string(runes[len(runes)-half:])
}
