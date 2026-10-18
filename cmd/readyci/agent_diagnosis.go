package main

import (
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func diagnoseRun(status, command string, events []preflightapi.RunEvent, runErr error, failureReason string) agentDiagnosis {
	if runErr != nil && !isExitLikeError(runErr) {
		kind := classifyCommandError(runErr)
		return agentDiagnosis{
			Kind:       kind,
			Confidence: 0.7,
			Summary:    runErr.Error(),
			Retryable:  retryableClassification(kind),
			Action:     actionForClassification(kind),
		}
	}
	analysis := localAnalyzeRunFailure(status, command, terminalExitCode(events), events, failureReason)
	return agentDiagnosis{
		Kind:        analysis.Classification,
		Confidence:  analysis.Confidence,
		Summary:     analysis.Summary,
		Retryable:   analysis.Retryable,
		Action:      analysis.AgentAction,
		PrimaryFile: analysis.PrimaryFile,
	}
}

func classifyCommandError(err error) string {
	return normalizeCLIError(err).Classification
}

func evidenceFromEvents(events []preflightapi.RunEvent, limit int) []agentEvidence {
	analysis := localAnalyzeRunFailure(preflightapi.StatusFailed, "", nil, events, "")
	items := analysis.Evidence
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	evidence := make([]agentEvidence, 0, len(items))
	for _, item := range items {
		evidence = append(evidence, agentEvidence{
			EventID: item.EventID,
			Stream:  item.Stream,
			Seq:     item.Seq,
			Line:    item.Line,
			Event:   item.Event,
			Path:    item.Path,
			Text:    item.Text,
		})
	}
	return evidence
}

func evidenceForDiagnosis(diagnosis agentDiagnosis, events []preflightapi.RunEvent, limit int) []agentEvidence {
	switch diagnosis.Kind {
	case preflightapi.ClassificationSuccess, preflightapi.ClassificationPending:
		return nil
	default:
		return evidenceFromEvents(events, limit)
	}
}

func buildAgentRepairPacket(diagnosis agentDiagnosis, command, rerunCommand, logsCommand string, evidence []agentEvidence, evidencePacks []agentEvidencePack, policyReasons []string) agentRepairPacket {
	return agentRepairPacket{
		Classification: diagnosis.Kind,
		AgentAction:    diagnosis.Action,
		Summary:        diagnosis.Summary,
		RootCause:      rootCauseForClassification(diagnosis.Kind),
		PrimaryFile:    diagnosis.PrimaryFile,
		TargetFiles:    repairTargetsForDiagnosis(diagnosis, evidence),
		FailingCommand: command,
		FailingTest:    failingTestFromEvidence(evidence),
		Evidence:       append([]agentEvidence(nil), evidence...),
		EvidencePacks:  append([]agentEvidencePack(nil), evidencePacks...),
		Commands:       repairCommands(diagnosis, evidence, rerunCommand, logsCommand, evidencePacks),
		RerunCommand:   rerunCommand,
		LogsCommand:    logsCommand,
		PolicyReasons:  append([]string(nil), policyReasons...),
		Confidence:     diagnosis.Confidence,
		ConfidenceNote: confidenceNote(diagnosis.Confidence),
	}
}

func rootCauseForClassification(kind string) string {
	switch kind {
	case preflightapi.ClassificationTestFailed:
		return "The command reached the test runner and at least one test failed."
	case preflightapi.ClassificationLintFailed:
		return "The command reached the linter and reported style or static-analysis violations."
	case preflightapi.ClassificationTypecheckFailed:
		return "The command reached the type checker and reported type contract violations."
	case preflightapi.ClassificationBuildFailed:
		return "The build command exited unsuccessfully."
	case preflightapi.ClassificationConfigInvalid:
		return "Ready CI configuration could not be parsed or resolved."
	case preflightapi.ClassificationAuthMissing:
		return "Ready CI could not find usable credentials for this request."
	case preflightapi.ClassificationAuthForbidden:
		return "The provided Ready CI token does not authorize this request."
	case preflightapi.ClassificationPolicyDenied:
		return "Project policy denied the run before verification could complete."
	case preflightapi.ClassificationSyncFailed:
		return "Workspace upload or materialization failed before command execution completed."
	case preflightapi.ClassificationSyncTooLarge:
		return "The workspace bundle exceeded Ready CI sync limits."
	case preflightapi.ClassificationCacheStorageExceeded:
		return "The run exceeded the configured cache storage policy."
	case preflightapi.ClassificationWorkerUnavailable:
		return "No available worker could accept the run."
	case preflightapi.ClassificationWorkerCapabilityMismatch:
		return "Registered workers did not match the run requirements."
	case preflightapi.ClassificationNetworkNotAllowed:
		return "The run attempted network access that project policy did not allow."
	case preflightapi.ClassificationDependencyDownloadFailed:
		return "Dependency setup failed, commonly due to network, registry, or cache state."
	case preflightapi.ClassificationDependencySetupFailed:
		return "The run environment is missing a dependency needed by the command."
	case preflightapi.ClassificationServiceSetupFailed:
		return "A configured service failed before or during verification."
	case preflightapi.ClassificationTimeout:
		return "The run exceeded its Ready CI timeout."
	case preflightapi.ClassificationCancelled:
		return "The run was cancelled before completion."
	case preflightapi.ClassificationVerificationUnknown:
		return "Ready CI could not determine the final verification result from available events."
	case preflightapi.ClassificationTransportFailure:
		return "The CLI could not complete communication with the Ready CI API."
	case preflightapi.ClassificationWorkerInternalError:
		return "The worker failed outside normal command failure handling."
	case preflightapi.ClassificationPlatformInternalError:
		return "The Ready CI control plane reported an internal failure."
	case preflightapi.ClassificationSuccess:
		return "The run completed successfully."
	case preflightapi.ClassificationPending:
		return "The run has not reached a terminal state yet."
	default:
		return "Ready CI classified the run from available status, events, and logs."
	}
}

func repairTargetsForDiagnosis(diagnosis agentDiagnosis, evidence []agentEvidence) []agentRepairTarget {
	seen := map[string]bool{}
	var targets []agentRepairTarget
	add := func(path, reason string) {
		path = strings.TrimSpace(path)
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		targets = append(targets, agentRepairTarget{Path: path, Reason: reason})
	}
	add(diagnosis.PrimaryFile, "primary file from failure analysis")
	for _, item := range evidence {
		add(item.Path, "referenced by failure evidence")
		if len(targets) >= 8 {
			break
		}
	}
	return targets
}

func repairCommands(diagnosis agentDiagnosis, evidence []agentEvidence, rerunCommand, logsCommand string, evidencePacks []agentEvidencePack) []agentRepairCommand {
	var commands []agentRepairCommand
	logReason := "Read lifecycle and recent log evidence before deciding how to recover."
	if diagnosis.Action == preflightapi.AgentActionFixCode && len(evidence) > 0 {
		logReason = "Read full logs if the supplied failure evidence is insufficient for repair."
	}
	if strings.TrimSpace(logsCommand) != "" {
		commands = append(commands, agentRepairCommand{
			Kind:    "inspect_logs",
			Command: logsCommand,
			Reason:  logReason,
		})
	}
	for _, pack := range evidencePacks {
		if strings.TrimSpace(pack.PreviewCommand) == "" {
			continue
		}
		commands = append(commands, agentRepairCommand{
			Kind:    "inspect_evidence_pack",
			Command: pack.PreviewCommand,
			Reason:  "Preview " + pack.Kind + " evidence retained by the run.",
		})
	}
	if strings.TrimSpace(rerunCommand) != "" {
		commands = append(commands, agentRepairCommand{
			Kind:    "rerun",
			Command: rerunCommand,
			Reason:  "Rerun the same verification after applying the repair.",
		})
	}
	return commands
}

func confidenceNote(confidence float64) string {
	switch {
	case confidence >= 0.85:
		return "high: classification came from structured run data or strong log signals"
	case confidence >= 0.55:
		return "medium: classification came from status and log heuristics"
	case confidence > 0:
		return "low: inspect logs and evidence before making broad changes"
	default:
		return ""
	}
}

func failingTestFromEvidence(evidence []agentEvidence) string {
	for _, item := range evidence {
		text := strings.TrimSpace(item.Text)
		if text == "" {
			continue
		}
		if name := failingTestFromLine(text); name != "" {
			return name
		}
		for _, line := range strings.Split(text, "\n") {
			if name := failingTestFromLine(line); name != "" {
				return name
			}
		}
	}
	return ""
}

func failingTestFromLine(line string) string {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "--- FAIL: ") {
		fields := strings.Fields(strings.TrimPrefix(line, "--- FAIL: "))
		if len(fields) > 0 {
			return strings.TrimSuffix(fields[0], ":")
		}
	}
	if strings.HasPrefix(line, "FAIL ") {
		fields := strings.Fields(strings.TrimPrefix(line, "FAIL "))
		if len(fields) > 0 && strings.HasPrefix(fields[0], "Test") {
			return strings.TrimSuffix(fields[0], ":")
		}
	}
	return ""
}

func terminalExitCode(events []preflightapi.RunEvent) *int {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].ExitCode == nil {
			continue
		}
		switch events[i].Type {
		case preflightapi.EventRunFinished, preflightapi.EventRunFailed, preflightapi.EventRunTimedOut, preflightapi.EventRunCancelled, preflightapi.EventCommandExited:
			value := *events[i].ExitCode
			return &value
		}
	}
	return nil
}

func terminalFailureReason(events []preflightapi.RunEvent) string {
	for i := len(events) - 1; i >= 0; i-- {
		switch events[i].Type {
		case preflightapi.EventRunFailed, preflightapi.EventRunTimedOut, preflightapi.EventRunCancelled:
			return strings.TrimSpace(events[i].Message)
		}
	}
	return ""
}

func nextActionsForRun(run agentVerifyRunReport) []agentNextAction {
	if run.Classification == "success" {
		return nil
	}
	var actions []agentNextAction
	if run.ExplainCommand != "" {
		actions = append(actions, agentNextAction{
			Kind:    "inspect",
			Command: run.ExplainCommand,
			Reason:  "Review the structured failure explanation.",
		})
	}
	if shouldRecommendAgentDoctor(run) {
		actions = append(actions, agentNextAction{
			Kind:    "diagnose",
			Command: readyCIDoctorCommand(run.APIURL, run.Check),
			Reason:  "Check worker availability, upload capability, and matching run requirements.",
		})
	}
	if run.AgentAction == "fix_code" {
		actions = append(actions, agentNextAction{Kind: "edit", Reason: run.Summary})
	}
	if run.AgentAction == preflightapi.AgentActionReduceScope {
		actions = append(actions, agentNextAction{Kind: "adjust", Reason: run.Summary})
	}
	if shouldRecommendAgentRerun(run) {
		actions = append(actions, agentNextAction{
			Kind:    "rerun",
			Command: rerunCommand(run.APIURL, run.Check, run.SecretsRequested),
			Reason:  "Rerun the same verification after addressing the issue.",
		})
	}
	return actions
}

func shouldRecommendAgentDoctor(run agentVerifyRunReport) bool {
	switch run.Classification {
	case preflightapi.ClassificationWorkerUnavailable, preflightapi.ClassificationWorkerCapabilityMismatch:
		return true
	default:
		return false
	}
}

func shouldRecommendAgentRerun(run agentVerifyRunReport) bool {
	if !preflightapi.IsTerminalRunStatus(run.Status) {
		return false
	}
	switch run.AgentAction {
	case "fix_code", "retry", "retry_or_split":
		return true
	default:
		return false
	}
}

func recommendedNextStep(diagnosis agentDiagnosis, apiURL, check string, requestSecrets bool) string {
	switch diagnosis.Action {
	case preflightapi.AgentActionInspectTestReport:
		return preflightapi.TestReportConflictMessage
	case "fix_code":
		if diagnosis.Kind == preflightapi.ClassificationTestFailed {
			return "Inspect the failing test and implementation, repair the defect, then rerun the same Ready CI check."
		}
		return "Inspect the failure evidence and relevant source, repair the defect, then rerun the same Ready CI check."
	case "retry", "retry_or_split":
		return "Retry once; if it fails again, inspect logs and reduce the check scope."
	case preflightapi.AgentActionReduceScope:
		if diagnosis.Kind == preflightapi.ClassificationCacheStorageExceeded {
			return "Prune or purge the Ready CI cache, remove bulky cache paths such as build outputs, then rerun the check."
		}
		return "Reduce the sync scope, exclude bulky paths, or split the task before rerunning Ready CI."
	case "ask_for_credentials":
		return "Ask for a scoped Ready CI token with the required verification permissions."
	case "wait", "wait_or_report_infra":
		return "Wait briefly or ask an operator to inspect worker capacity and capabilities."
	case "none":
		return "No follow-up is required."
	default:
		if check != "" {
			return "Inspect the logs and rerun " + rerunCommand(apiURL, check, requestSecrets) + " after fixing setup."
		}
		return "Inspect the logs and rerun Ready CI after fixing setup."
	}
}

func rerunCommand(apiURL, check string, requestSecrets bool) string {
	if strings.TrimSpace(check) == "" {
		return ""
	}
	command := "readyci verify --json --progress=status" + apiURLFlag(apiURL) + " --check " + shellQuoteArg(check)
	if requestSecrets {
		command += " --request-secrets"
	}
	return command
}

func rerunCommandForDiagnosis(status string, diagnosis agentDiagnosis, apiURL, check string, requestSecrets bool) string {
	if !preflightapi.IsTerminalRunStatus(status) {
		return ""
	}
	switch diagnosis.Action {
	case preflightapi.AgentActionFixCode, preflightapi.AgentActionRetry, preflightapi.AgentActionRetryOrSplit:
		return rerunCommand(apiURL, check, requestSecrets)
	default:
		return ""
	}
}

func agentVerifyCommandForPlan(plan checkRunPlan, intent string) string {
	command := "readyci verify --json --progress=status"
	command += apiURLFlag(plan.Options.APIURL)
	if projectID := strings.TrimSpace(plan.Options.ProjectID); projectID != "" {
		command += " --project " + shellQuoteArg(projectID)
	}
	normalizedIntent, _ := normalizeAgentIntent(intent)
	if strings.TrimSpace(intent) != "" && normalizedIntent != agentIntentEditLoop {
		command += " --intent " + shellQuoteArg(normalizedIntent)
	}
	if changeMode := strings.TrimSpace(plan.Options.ChangeMode); changeMode != "" {
		command += " --changes " + shellQuoteArg(changeMode)
	}
	if plan.Options.SecretsRequested != nil && *plan.Options.SecretsRequested {
		command += " --request-secrets"
	}
	command += " --check " + shellQuoteArg(plan.Name)
	return command
}

func readyCILogURI(runID string) string {
	if runID == "" {
		return ""
	}
	return "readyci://runs/" + runID + "/logs"
}

func readyCILogsCommand(apiURL, runID string) string {
	if runID == "" {
		return ""
	}
	return "readyci runs logs" + apiURLFlag(apiURL) + " --tail 200 --include-lifecycle --json " + shellQuoteArg(runID)
}

func readyCIExplainCommand(apiURL, runID string) string {
	if runID == "" {
		return ""
	}
	return "readyci agent explain" + apiURLFlag(apiURL) + " --json " + shellQuoteArg(runID)
}

func readyCIDoctorCommand(apiURL, check string) string {
	command := "readyci doctor --json" + apiURLFlag(apiURL)
	if strings.TrimSpace(check) != "" {
		command += " --check " + shellQuoteArg(check)
	}
	return command
}
