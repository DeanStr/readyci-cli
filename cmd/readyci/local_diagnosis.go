package main

import (
	"regexp"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/diagnosiscommon"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

var localDiagnosisFileLinePattern = regexp.MustCompile(`(?:^|[^A-Za-z0-9_./@-])([A-Za-z0-9_./@-]+\.[A-Za-z0-9_+-]+):([0-9]+)(?::[0-9]+)?`)

var localDiagnosisPytestFailedPattern = regexp.MustCompile(`(?i)\bFAILED\s+([A-Za-z0-9_./-]+\.py)(?:::|\s|$)`)

var localDiagnosisPythonMissingModulePattern = regexp.MustCompile(`(?i)(?:modulenotfounderror:\s*)?no module named ['"]([^'"]+)['"]`)

var localDiagnosisTSDiagnosticPattern = regexp.MustCompile(`(?i)\bTS[0-9]{3,5}\b`)

var localDiagnosisAssertionPattern = regexp.MustCompile(`\b(?:expected|want)\b.*\b(?:got|received|actual)\b|\b(?:got|received|actual)\b.*\b(?:expected|want)\b|\b(?:expected|received)\s*:`)

var localDiagnosisMypyDiagnosticPattern = regexp.MustCompile(`(?im)^\s*(?:[A-Z]:)?[^:\n]+\.pyi?:[0-9]+(?::[0-9]+)?:\s+error:\s+`)

var localDiagnosisMypySummaryPattern = regexp.MustCompile(`(?im)^\s*found\s+[0-9]+\s+errors?\s+in\s+[0-9]+\s+files?\s+\(checked\s+[0-9]+\s+source\s+files?\)\s*$`)

var localDiagnosisPyrightDiagnosticPattern = regexp.MustCompile(`(?im)^\s*(?:[A-Z]:)?[^:\n]+\.pyi?:[0-9]+:[0-9]+\s+-\s+error:\s+`)

var localDiagnosisRegistryHTTPFailurePattern = regexp.MustCompile(`(?i)\b(?:(?:http(?:/[0-9.]+)?(?:\s+error)?|(?:response\s+)?status(?:\s+code)?(?:\s+does\s+not\s+indicate\s+success)?|npm\s+(?:err!|error))(?:\s*[:=]\s*|\s+)[45][0-9]{2}|(?:got|received|returned)\s+(?:an?\s+)?[45][0-9]{2}(?:\s+response)?|[45][0-9]{2}\s+(?:(?:client|server)\s+error:\s+)?(?:unauthorized|forbidden|not\s+found|gone|request\s+timeout|too\s+many\s+requests|internal\s+server\s+error|bad\s+gateway|service\s+unavailable|gateway\s+timeout))\b`)

var localDiagnosisCurlFailureCodePattern = regexp.MustCompile(`(?i)\bcurl:\s*\(([0-9]{1,3})\)`)

var localDiagnosisHTTP413Pattern = regexp.MustCompile(`\b(?:(?:http(?:/[0-9.]+)?|status(?:\s+code)?)(?:\s*:\s*|\s+)413|413\s+(?:request entity too large|payload too large|content too large))\b`)

var localLogDeliveryFailurePattern = regexp.MustCompile(`(?i)(?:^|; )log delivery incomplete:`)

var localArtifactUploadFailurePattern = regexp.MustCompile(`(?i)(?:^|; )artifact upload failed:`)

const localGoToolchainDownloadFailureMarker = "readyci: go toolchain download failed:"

func localAnalyzeRunFailure(status, command string, exitCode *int, events []preflightapi.RunEvent, failureReason string) preflightapi.RunFailureAnalysis {
	return localAnalyzeRunFailureWithNetworkMode(status, command, exitCode, events, failureReason, "")
}

func localAnalyzeRunFailureWithNetworkMode(status, command string, exitCode *int, events []preflightapi.RunEvent, failureReason, networkMode string) preflightapi.RunFailureAnalysis {
	status = strings.TrimSpace(status)
	primaryFailureReason := localPrimaryWorkerFailureReason(failureReason, events)
	switch {
	case status == preflightapi.StatusSucceeded:
		return preflightapi.RunFailureAnalysis{Classification: preflightapi.ClassificationSuccess, Confidence: 1, Retryable: false, AgentAction: preflightapi.AgentActionNone, Summary: "The run passed."}
	case localPendingRunStatus(status) && (exitCode == nil || *exitCode == 0):
		return preflightapi.RunFailureAnalysis{Classification: preflightapi.ClassificationPending, Confidence: 0.55, Retryable: false, AgentAction: preflightapi.AgentActionWait, Summary: "The run is not terminal yet."}
	case status == preflightapi.StatusTimedOut:
		return localFailureAnalysis(preflightapi.ClassificationTimeout, 0.7, "The run timed out.", primaryFailureReason)
	case status == preflightapi.StatusCancelled:
		return localFailureAnalysis(preflightapi.ClassificationCancelled, 0.7, "The run was cancelled.", primaryFailureReason)
	case status == preflightapi.StatusUploadExpired:
		return localFailureAnalysis(preflightapi.ClassificationSyncFailed, 0.7, "The source upload expired before the run could start.", primaryFailureReason)
	case status == preflightapi.StatusDenied:
		return localFailureAnalysis(preflightapi.ClassificationPolicyDenied, 0.8, "The run was denied by project policy.", primaryFailureReason)
	}

	analysisText := command + "\n" + primaryFailureReason + "\n" + localRunEventSearchText(events)
	analysisLower := strings.ToLower(analysisText)
	evidence := localEvidenceFromEvents(events, 12)
	for _, reason := range strings.Split(failureReason, "; ") {
		if localContainsAny(strings.ToLower(reason), "firecracker shutdown failed", "remove firecracker cgroup", "terminate firecracker cgroup", "firecracker containment failure") {
			analysis := localFailureAnalysis(preflightapi.ClassificationWorkerInternalError, 0.7, localDefaultSummary(preflightapi.ClassificationWorkerInternalError), reason)
			analysis.Evidence = evidence
			return analysis
		}
	}
	if artifactFailureKind := localTerminalArtifactUploadFailureClassification(failureReason, events); artifactFailureKind != "" {
		analysis := localFailureAnalysis(artifactFailureKind, 0.65, localDefaultSummary(artifactFailureKind), failureReason)
		analysis.PrimaryFile = localPrimaryFileFromEvidence(evidence)
		analysis.Evidence = evidence
		return analysis
	}
	if diagnosiscommon.RuntimePreparationFailed(events, exitCode) {
		analysis := localFailureAnalysis(preflightapi.ClassificationEnvironmentSetupFailed, 1,
			"Required runtime preparation failed before repository commands started; inspect preparation logs.", "")
		analysis.Evidence = evidence
		return analysis
	}
	// Refine only an ordinary command failure. A worker's recorded primary
	// failure reason retains precedence over a test runner's output.
	if primaryFailureReason == "" {
		if failure := diagnosiscommon.MavenTestFailureFromEvents(events, exitCode); failure != nil {
			analysis := localFailureAnalysis(preflightapi.ClassificationTestFailed, 0.65,
				localDefaultSummary(preflightapi.ClassificationTestFailed), failure.Summary)
			analysis.Evidence = append(failure.Evidence, evidence...)
			if len(analysis.Evidence) > 12 {
				analysis.Evidence = analysis.Evidence[:12]
			}
			return analysis
		}
	}
	if module := localMissingPythonModuleName(analysisText); module != "" && !localAnalysisTextLooksTestFailure(analysisText, analysisLower) {
		analysis := localFailureAnalysis(preflightapi.ClassificationDependencySetupFailed, 0.55, failureReason, "Missing Python dependency: "+module+".")
		analysis.PrimaryFile = localPrimaryFile(analysisText)
		analysis.Evidence = evidence
		return analysis
	}
	kind, confidence := localClassifyCommandFailure(command, analysisText, exitCode)
	if !localAnalysisTextLooksTestFailure(analysisText, analysisLower) && (kind == preflightapi.ClassificationDependencyDownloadFailed || kind == preflightapi.ClassificationLintFailed || kind == preflightapi.ClassificationBuildFailed || kind == preflightapi.ClassificationTestFailed) {
		if download := diagnosiscommon.BrowserDownloadFailureFromEvents(events); download != nil {
			kind, summary := preflightapi.ClassificationDependencyDownloadFailed, download.Summary
			if download.ReachabilityFailure && strings.EqualFold(strings.TrimSpace(networkMode), preflightapi.DefaultNetworkMode) {
				kind = preflightapi.ClassificationNetworkNotAllowed
				summary = "Browser installation required a download, but this run used network_mode: none."
			}
			analysis := localFailureAnalysis(kind, 0.65, localDefaultSummary(kind), summary)
			analysis.Evidence = append(download.Evidence, evidence...)
			if len(analysis.Evidence) > 12 {
				analysis.Evidence = analysis.Evidence[:12]
			}
			return analysis
		}
	}
	if strings.HasPrefix(strings.ToLower(primaryFailureReason), "artifact upload failed:") {
		kind = preflightapi.ClassificationSyncFailed
		confidence = 0.6
		if localSyncTooLargeFailureText(strings.ToLower(primaryFailureReason)) {
			kind = preflightapi.ClassificationSyncTooLarge
		}
	}
	if (kind == preflightapi.ClassificationDependencyDownloadFailed || kind == preflightapi.ClassificationTestFailed) &&
		!localExplicitAssertionFailureText(analysisLower) &&
		strings.EqualFold(strings.TrimSpace(networkMode), preflightapi.DefaultNetworkMode) &&
		localDependencyRegistryEgressFailureText(command, analysisLower, exitCode) {
		kind = preflightapi.ClassificationNetworkNotAllowed
		confidence = 0.65
	}
	failureSummary := strings.TrimSpace(primaryFailureReason)
	summary := failureSummary
	evidenceSummary := localPreferredEvidenceText(kind, evidence)
	if kind == preflightapi.ClassificationNetworkNotAllowed &&
		strings.EqualFold(strings.TrimSpace(networkMode), preflightapi.DefaultNetworkMode) &&
		localDependencyRegistryEgressFailureText(command, analysisLower, exitCode) {
		summary = "A dependency or container image pull required network access, but this run used network_mode: none."
	} else if evidenceSummary != "" && !localSummarySupportsClassification(kind, failureSummary) {
		summary = evidenceSummary
	}
	if summary == "" {
		summary = evidenceSummary
	}
	if summary == "" {
		summary = localFirstEvidenceText(evidence)
	}
	if summary == "" {
		summary = failureSummary
	}
	analysis := localFailureAnalysis(kind, confidence, localDefaultSummary(kind), summary)
	analysis.PrimaryFile = localPrimaryFileFromEvidence(evidence)
	if kind == preflightapi.ClassificationTestFailed {
		if _, path := diagnosiscommon.PytestFailureSummary(evidence); path != "" {
			analysis.PrimaryFile = path
		}
	}
	if analysis.PrimaryFile == "" {
		analysis.PrimaryFile = localPrimaryFile(analysisText)
	}
	analysis.Evidence = evidence
	return analysis
}

func retryableClassification(kind string) bool {
	return diagnosiscommon.RetryableClassification(kind)
}

func actionForClassification(kind string) string {
	return diagnosiscommon.AgentActionForClassification(kind)
}

func localClassifyCommandFailure(command, analysisText string, exitCode *int) (string, float64) {
	commandLower := strings.ToLower(command)
	analysisLower := strings.ToLower(analysisText)
	commandLooksTypecheck := localCommandLooksTypecheck(commandLower)
	commandLooksTest := localCommandLooksTest(commandLower)
	textLooksTestFailure := localAnalysisTextLooksTestFailure(analysisText, analysisLower)
	switch {
	case textLooksTestFailure && (commandLooksTest || localConcreteTestFailureText(analysisText, analysisLower)):
		return preflightapi.ClassificationTestFailed, 0.45
	case localReadyCIConfigFailureText(commandLower, analysisLower):
		return preflightapi.ClassificationConfigInvalid, 0.45
	case localDependencyDownloadFailureText(commandLower, analysisLower, exitCode):
		return preflightapi.ClassificationDependencyDownloadFailed, 0.45
	case strings.Contains(analysisLower, "missing go.sum entry") ||
		strings.Contains(analysisLower, "updates to go.mod needed"):
		return preflightapi.ClassificationDependencySetupFailed, 0.45
	case commandLooksTypecheck:
		return preflightapi.ClassificationTypecheckFailed, 0.45
	case localAnalysisTextLooksTypecheckFailure(analysisText, analysisLower):
		return preflightapi.ClassificationTypecheckFailed, 0.45
	case localCommandContainsToken(commandLower, "lint") ||
		strings.Contains(commandLower, "eslint") ||
		strings.Contains(commandLower, "golangci-lint") ||
		strings.Contains(commandLower, "ruff check") ||
		strings.Contains(commandLower, "shellcheck"):
		return preflightapi.ClassificationLintFailed, 0.45
	case commandLooksTest || textLooksTestFailure:
		return preflightapi.ClassificationTestFailed, 0.45
	case localCommandContainsToken(commandLower, "build"):
		return preflightapi.ClassificationBuildFailed, 0.45
	case exitCode == nil && strings.TrimSpace(analysisText) != "":
		return preflightapi.ClassificationInternalError, 0.35
	default:
		return preflightapi.ClassificationBuildFailed, 0.35
	}
}
