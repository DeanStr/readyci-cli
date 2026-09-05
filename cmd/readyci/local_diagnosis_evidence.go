package main

import (
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/diagnosiscommon"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/textlimit"
)

func localTerminalArtifactUploadFailureClassification(failureReason string, events []preflightapi.RunEvent) string {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(failureReason)), "artifact upload failed:") {
		return ""
	}
	for index := len(events) - 1; index >= 0; index-- {
		event := events[index]
		if event.Type == preflightapi.EventCommandExited && event.ExitCode != nil {
			if *event.ExitCode != 0 {
				return ""
			}
			if localSyncTooLargeFailureText(strings.ToLower(failureReason)) {
				return preflightapi.ClassificationSyncTooLarge
			}
			return preflightapi.ClassificationSyncFailed
		}
	}
	return ""
}

func localPrimaryWorkerFailureReason(failureReason string, events []preflightapi.RunEvent) string {
	reason := strings.TrimSpace(failureReason)
	cut := len(reason)

	if match := localLogDeliveryFailurePattern.FindStringIndex(reason); match != nil {
		cut = min(cut, match[0])
	}
	if match := localArtifactUploadFailurePattern.FindStringIndex(reason); match != nil {
		if match[0] != 0 {
			cut = min(cut, match[0])
		} else if localFailedCommandExitEvent(events) {
			cut = 0
		}
	}
	return strings.TrimSpace(reason[:cut])
}

func localFailedCommandExitEvent(events []preflightapi.RunEvent) bool {
	for index := len(events) - 1; index >= 0; index-- {
		event := events[index]
		if event.Type == preflightapi.EventCommandExited && event.ExitCode != nil {
			return *event.ExitCode != 0
		}
	}
	return false
}

func localSyncTooLargeFailureText(lower string) bool {
	if localContainsAny(lower,
		"bundle exceeds",
		"compressed bundle exceeds",
		"expanded bundle exceeds",
		"artifact bundle too large",
		"bundle is too large",
	) {
		return true
	}
	if !localContainsAny(lower, "upload", "bundle", "sync", "artifact") {
		return false
	}
	return localContainsAny(lower, "request entity too large", "payload too large", "content too large") ||
		localDiagnosisHTTP413Pattern.MatchString(lower)
}

func localAnalysisTextLooksTypecheckFailure(text, lower string) bool {
	return localDiagnosisTSDiagnosticPattern.MatchString(text) ||
		localDiagnosisMypyDiagnosticPattern.MatchString(text) ||
		localDiagnosisMypySummaryPattern.MatchString(text) ||
		localDiagnosisPyrightDiagnosticPattern.MatchString(text) ||
		strings.Contains(lower, "required type parameters") ||
		strings.Contains(lower, "cannot find name") ||
		strings.Contains(lower, "is not assignable to type") ||
		strings.Contains(lower, "does not exist on type") ||
		strings.Contains(lower, "type error")
}

func localMissingPythonModuleName(text string) string {
	match := localDiagnosisPythonMissingModulePattern.FindStringSubmatch(text)
	if len(match) < 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}

func localFailureAnalysis(kind string, confidence float64, fallbackSummary, summary string) preflightapi.RunFailureAnalysis {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		summary = fallbackSummary
	}
	return preflightapi.RunFailureAnalysis{
		Classification: kind,
		Confidence:     confidence,
		Retryable:      retryableClassification(kind),
		AgentAction:    actionForClassification(kind),
		Summary:        summary,
	}
}

func localPreferredEvidenceText(kind string, evidence []preflightapi.RunFailureEvidence) string {
	for _, item := range evidence {
		text := strings.TrimSpace(item.Text)
		if text == "" || localLowSignalEvidenceText(text) {
			continue
		}
		switch kind {
		case preflightapi.ClassificationTypecheckFailed:
			if item.Path != "" || localAnalysisTextLooksTypecheckFailure(text, strings.ToLower(text)) {
				return text
			}
		case preflightapi.ClassificationTestFailed:
			if item.Path != "" || localDiagnosisPytestFailedPattern.MatchString(text) || localDiagnosisAssertionPattern.MatchString(strings.ToLower(text)) {
				return text
			}
		case preflightapi.ClassificationLintFailed:
			if item.Path != "" || localLintEvidenceText(text) {
				return text
			}
		case preflightapi.ClassificationDependencyDownloadFailed:
			lower := strings.ToLower(text)
			if localDependencyRegistryAccessFailureText(lower) || localDependencyDownloadOutputContext(lower) {
				return text
			}
		}
	}
	return ""
}

func localFirstEvidenceText(evidence []preflightapi.RunFailureEvidence) string {
	for _, item := range evidence {
		if text := strings.TrimSpace(item.Text); text != "" && !localLowSignalEvidenceText(text) {
			return text
		}
	}
	return ""
}

func localLowSignalEvidenceText(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if diagnosiscommon.DefiniteTestSuccessLine(text) {
		return true
	}
	if localAnalysisTextLooksTestFailure(text, lower) {
		return false
	}
	return strings.Contains(lower, "npm warn deprecated") ||
		strings.Contains(lower, "warning: deprecated") ||
		strings.Contains(lower, "deprecated package") ||
		strings.HasPrefix(lower, "readyci: retained failed workspace") ||
		strings.HasPrefix(lower, "readyci: retained failed workspace artifact") ||
		strings.HasPrefix(lower, "readyci: cache filesystem usage") ||
		strings.HasPrefix(lower, "readyci: cache top-level usage") ||
		(strings.HasPrefix(lower, "filesystem ") && strings.Contains(lower, "mounted on")) ||
		strings.Contains(lower, "pip's dependency resolver does not currently take into account") ||
		strings.Contains(lower, "this behaviour is the source of the following dependency conflicts") ||
		localPipDependencyConflictDetailText(text) ||
		strings.HasPrefix(lower, "internalerror> traceback") ||
		strings.HasPrefix(lower, "internalerror>   file ") ||
		strings.HasPrefix(lower, "internalerror>     ") ||
		strings.HasPrefix(lower, "internalerror>            ") ||
		strings.HasPrefix(lower, "internalerror>           ") ||
		strings.HasPrefix(lower, "internalerror>             ")
}

func localPipDependencyConflictDetailText(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	return strings.Contains(lower, " requires ") &&
		strings.Contains(lower, " but you have ") &&
		strings.Contains(lower, " which is incompatible") &&
		!localAnalysisTextLooksTestFailure(text, lower)
}

func localLintEvidenceText(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "eslint") ||
		strings.Contains(lower, "golangci-lint") ||
		strings.Contains(lower, "ruff check") ||
		strings.Contains(lower, "flake8") ||
		strings.Contains(lower, "shellcheck") ||
		strings.Contains(lower, "lint error") ||
		strings.Contains(lower, "lint failed")
}

func localPrimaryFileFromEvidence(evidence []preflightapi.RunFailureEvidence) string {
	return diagnosiscommon.PrimaryFileFromEvidence(evidence)
}

func localPendingRunStatus(status string) bool {
	switch status {
	case preflightapi.StatusQueued, preflightapi.StatusAwaitingUpload, preflightapi.StatusScheduled, preflightapi.StatusStartingVM, preflightapi.StatusRunning:
		return true
	default:
		return false
	}
}

func localRunEventSearchText(events []preflightapi.RunEvent) string {
	return diagnosiscommon.EventSearchText(events)
}

func localEvidenceFromEvents(events []preflightapi.RunEvent, limit int) []preflightapi.RunFailureEvidence {
	var evidence []preflightapi.RunFailureEvidence
	seq := 0
	for _, event := range events {
		if event.Type != preflightapi.EventStdout && event.Type != preflightapi.EventStderr {
			continue
		}
		data := diagnosiscommon.EventData(event)
		if data == "" {
			continue
		}
		stream, _ := diagnosiscommon.EventStream(event)
		for index, line := range strings.Split(data, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			path := localPrimaryFile(line)
			if localLowSignalEvidenceText(line) || (path == "" && !localEvidenceLineLooksUseful(line)) {
				continue
			}
			seq++
			evidence = append(evidence, preflightapi.RunFailureEvidence{
				EventID: event.ID,
				Stream:  stream,
				Seq:     seq,
				Line:    index + 1,
				Event:   event.Type,
				Path:    path,
				Text:    textlimit.PrefixUTF8WithinBytes(line, 500),
			})
			if limit > 0 && len(evidence) >= limit {
				return evidence
			}
		}
	}
	return evidence
}

func localEvidenceLineLooksUseful(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	if localDiagnosisRegistryHTTPFailurePattern.MatchString(line) || localRegistryTransportFailureText(lower) {
		return true
	}
	if strings.HasPrefix(lower, "fail ") || strings.HasPrefix(lower, "--- fail") {
		return true
	}
	if strings.Contains(lower, "missing go.sum entry") ||
		strings.Contains(lower, "updates to go.mod needed") ||
		strings.Contains(lower, "pytestunknownmarkwarning") ||
		strings.Contains(lower, "unknown pytest.mark") {
		return true
	}
	return runFailureAnalysisTextLooksUseful(line)
}

func localPrimaryFile(text string) string {
	for _, match := range localDiagnosisPytestFailedPattern.FindAllStringSubmatch(text, -1) {
		path := strings.TrimSpace(match[1])
		if localValidPrimaryFilePath(path) {
			return path
		}
	}
	for _, match := range localDiagnosisFileLinePattern.FindAllStringSubmatch(text, -1) {
		path := strings.TrimSpace(match[1])
		if !localValidPrimaryFilePath(path) {
			continue
		}
		return path
	}
	return ""
}

func localValidPrimaryFilePath(path string) bool {
	return diagnosiscommon.ValidPrimaryFilePath(path)
}

func localPathHasParentTraversal(path string) bool {
	return diagnosiscommon.PathHasParentTraversal(path)
}

func localReadyCIConfigFailureText(commandLower, analysisLower string) bool {
	configFailure := localContainsAny(analysisLower, "yaml:", "invalid config", "unknown config key", ".readyci.yml", ".readyci.yaml")
	if !configFailure {
		return false
	}
	if localContainsAny(analysisLower, ".readyci.yml", ".readyci.yaml") ||
		localContainsAny(commandLower, "readyci config", "preflight config", "readyci setup", "preflight setup") {
		return true
	}
	for _, line := range strings.Split(analysisLower, "\n") {
		if localContainsAny(line, "readyci", "preflight") && localContainsAny(line, "invalid config", "yaml:", "unknown config key") {
			return true
		}
	}
	return false
}

func localSummarySupportsClassification(kind, summary string) bool {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return false
	}
	lower := strings.ToLower(summary)
	switch kind {
	case preflightapi.ClassificationTypecheckFailed:
		return localAnalysisTextLooksTypecheckFailure(summary, lower) || localPrimaryFile(summary) != ""
	case preflightapi.ClassificationTestFailed:
		return localAnalysisTextLooksTestFailure(summary, lower)
	case preflightapi.ClassificationLintFailed:
		return localLintEvidenceText(summary) || localPrimaryFile(summary) != ""
	case preflightapi.ClassificationDependencyDownloadFailed:
		return localDependencyRegistryAccessFailureText(lower) || localDependencyDownloadOutputContext(lower)
	default:
		return true
	}
}

func localDefaultSummary(kind string) string {
	switch kind {
	case preflightapi.ClassificationTestFailed:
		return "Tests failed."
	case preflightapi.ClassificationLintFailed:
		return "Lint checks failed."
	case preflightapi.ClassificationTypecheckFailed:
		return "Type checking failed."
	case preflightapi.ClassificationConfigInvalid:
		return "Ready CI configuration is invalid."
	case preflightapi.ClassificationAuthMissing:
		return "Authentication is missing."
	case preflightapi.ClassificationAuthForbidden:
		return "The token is not authorized for this action."
	case preflightapi.ClassificationPolicyDenied:
		return "The run was denied by project policy."
	case preflightapi.ClassificationProjectNotFound:
		return "The project was not found."
	case preflightapi.ClassificationSyncFailed:
		return "Workspace sync failed."
	case preflightapi.ClassificationSyncTooLarge:
		return "Workspace sync exceeded the size limit."
	case preflightapi.ClassificationWorkerCapabilityMismatch:
		return "No worker matched the run requirements."
	case preflightapi.ClassificationNetworkNotAllowed:
		return "Network access was blocked by policy."
	case preflightapi.ClassificationDependencyDownloadFailed:
		return "A dependency download failed."
	case preflightapi.ClassificationDependencySetupFailed:
		return "A dependency is missing from the run environment."
	case preflightapi.ClassificationTimeout:
		return "The run timed out."
	case preflightapi.ClassificationCancelled:
		return "The run was cancelled."
	default:
		return "The command failed."
	}
}
