package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func agentRunReportFromResult(checkName, variantName string, options remoteRunOptions, result remoteRunResult, runErr error) agentVerifyRunReport {
	diagnosis := agentDiagnosisFromAnalysis(analyzeRemoteRunFailure(result, runErr, options.Command))
	terminal := preflightapi.IsTerminalRunStatus(result.Status)
	report := agentVerifyRunReport{
		APIURL:           options.APIURL,
		SecretsRequested: boolPtrValue(options.SecretsRequested),
		RunID:            result.RunID,
		RunGroupID:       result.RunGroupID,
		Status:           defaultString(result.Status, "error"),
		Check:            checkName,
		MatrixVariant:    variantName,
		Terminal:         terminal,
		ExitCode:         exitCodeForRunResult(result, terminal),
		Classification:   diagnosis.Kind,
		Retryable:        diagnosis.Retryable,
		AgentAction:      diagnosis.Action,
		Summary:          diagnosis.Summary,
		LogsURL:          readyCILogURI(result.RunID),
		LogsCommand:      readyCILogsCommand(options.APIURL, result.RunID),
		ExplainCommand:   readyCIExplainCommand(options.APIURL, result.RunID),
		IdempotencyKey:   result.IdempotencyKey,
		Evidence:         evidenceForDiagnosis(diagnosis, result.Events, 5),
	}
	if runErr != nil && !isExitLikeError(runErr) {
		report.Status = "error"
		report.Terminal = true
		report.Summary = runErr.Error()
		report.Classification = classifyCommandError(runErr)
		report.AgentAction = actionForClassification(report.Classification)
		report.Retryable = retryableClassification(report.Classification)
		if report.Classification == preflightapi.ClassificationVerificationUnknown {
			report.Status = "unknown"
			report.Terminal = false
		}
	}
	var exitErr exitCodeError
	if report.Terminal && (report.ExitCode == nil || *report.ExitCode == 0) && errors.As(runErr, &exitErr) && exitErr.code != 0 {
		report.ExitCode = intPtr(exitErr.code)
	}
	return report
}

func agentRunReportFromDetail(ctx context.Context, client *api.Client, apiURL string, detail preflightapi.RunDetail) (agentVerifyRunReport, error) {
	eventsResp, err := client.ListRunEvents(ctx, detail.Summary.RunID)
	if err != nil {
		return agentVerifyRunReport{}, err
	}
	failureReason := terminalFailureReason(eventsResp.Events)
	status := effectiveRunStatus(detail.Summary.Status, eventsResp.Events)
	diagnosis := agentDiagnosisFromAnalysis(analyzeRemoteRunFailure(remoteRunResultFromDetailDiagnosis(detail, status, failureReason, eventsResp.Events), nil, detail.Spec.Command))
	checkName, variantName := preflightapi.SplitCheckAliasVariant(detail.Spec.CommandAlias)
	if checkName == "" {
		checkName = detail.Spec.CommandAlias
	}
	return agentVerifyRunReport{
		APIURL:           apiURL,
		SecretsRequested: persistedRunSecretsRequested(detail.Spec.SecretsRequested),
		RunID:            detail.Summary.RunID,
		RunGroupID:       detail.Summary.RunGroupID,
		Status:           status,
		Check:            checkName,
		MatrixVariant:    variantName,
		Terminal:         preflightapi.IsTerminalRunStatus(status),
		ExitCode:         exitCodeForRunDetail(detail, eventsResp.Events),
		Classification:   diagnosis.Kind,
		Retryable:        diagnosis.Retryable,
		AgentAction:      diagnosis.Action,
		Summary:          diagnosis.Summary,
		LogsURL:          readyCILogURI(detail.Summary.RunID),
		LogsCommand:      readyCILogsCommand(apiURL, detail.Summary.RunID),
		ExplainCommand:   readyCIExplainCommand(apiURL, detail.Summary.RunID),
		IdempotencyKey:   detail.Spec.IdempotencyKey,
		Evidence:         evidenceForDiagnosis(diagnosis, eventsResp.Events, 5),
	}, nil
}

func agentDiagnosisFromAnalysis(analysis preflightapi.RunFailureAnalysis) agentDiagnosis {
	return agentDiagnosis{
		Kind:        analysis.Classification,
		Confidence:  analysis.Confidence,
		Summary:     analysis.Summary,
		Retryable:   analysis.Retryable,
		Action:      analysis.AgentAction,
		PrimaryFile: analysis.PrimaryFile,
	}
}

func summarizeAgentVerifyRuns(report agentVerifyReport) agentVerifyReport {
	if report.SchemaVersion == "" {
		report.SchemaVersion = agentSchemaVersion
	}
	normalizeAgentVerifyCollections(&report)
	if len(report.Runs) == 0 {
		report.Status = "error"
		report.Classification = "internal_error"
		report.AgentAction = "report_setup"
		report.Summary = "No runs were created."
		report.Terminal = true
		report.CLIExitCode = cliExitInternal
		return report
	}
	overall := selectAgentOverallRun(report.Runs)
	report.RunID = overall.RunID
	report.Status = overall.Status
	if overall.RunGroupID != "" {
		report.RunGroupID = stringPtrValue(overall.RunGroupID)
	}
	if report.GroupKind != "" && report.RunGroupID != nil {
		report.GroupStatus = stringPtrValue(report.Status)
	}
	report.Terminal = overall.Terminal
	report.ExitCode = overall.ExitCode
	report.Classification = overall.Classification
	report.Retryable = overall.Retryable
	report.AgentAction = overall.AgentAction
	report.Summary = overall.Summary
	report.LogsURL = overall.LogsURL
	report.LogsCommand = overall.LogsCommand
	report.ExplainCommand = overall.ExplainCommand
	report.NextActions = nextActionsForRun(overall)
	if allAgentRunsSucceeded(report.Runs) {
		report.Status = preflightapi.StatusSucceeded
		report.Terminal = true
		report.ExitCode = intPtr(0)
		report.Classification = "success"
		report.Retryable = false
		report.AgentAction = "none"
		report.Summary = agentVerifySuccessSummary(report)
		report.NextActions = nil
	}
	report.CLIExitCode = agentReportProcessExitCode(report)
	return report
}

func remoteRunResultFromDetailDiagnosis(detail preflightapi.RunDetail, status, failureReason string, events []preflightapi.RunEvent) remoteRunResult {
	result := remoteRunResult{
		RunID:          detail.Summary.RunID,
		RunGroupID:     detail.Summary.RunGroupID,
		Status:         status,
		NetworkMode:    detail.Spec.NetworkMode,
		FailureReason:  failureReason,
		Classification: detail.Summary.Classification,
		Retryable:      detail.Summary.Retryable,
		AgentAction:    detail.Summary.AgentAction,
		FailureSummary: detail.Summary.FailureSummary,
		PrimaryFile:    detail.Summary.PrimaryFile,
		Events:         events,
	}
	if exitCode := nonZeroCommandExitCode(events); exitCode != nil {
		result.CommandExited = true
		result.CommandExitCode = *exitCode
	}
	return result
}

func normalizeAgentVerifyCollections(report *agentVerifyReport) {
	if report.Checks == nil {
		report.Checks = []agentAffectedCheck{}
	}
	if report.Runs == nil {
		report.Runs = []agentVerifyRunReport{}
	}
}

func agentVerifySuccessSummary(report agentVerifyReport) string {
	if check := strings.TrimSpace(report.Check); check != "" {
		return fmt.Sprintf("Ready CI check %s passed.", check)
	}
	checks := uniqueAgentRunChecks(report.Runs)
	switch len(checks) {
	case 0:
		return "Ready CI verification passed."
	case 1:
		return fmt.Sprintf("Ready CI check %s passed.", checks[0])
	default:
		return fmt.Sprintf("Ready CI verification passed for %d checks.", len(checks))
	}
}

func uniqueAgentRunChecks(runs []agentVerifyRunReport) []string {
	seen := make(map[string]bool)
	var checks []string
	for _, run := range runs {
		if run.Phase == "warm" {
			continue
		}
		check := strings.TrimSpace(run.Check)
		if check == "" || seen[check] {
			continue
		}
		seen[check] = true
		checks = append(checks, check)
	}
	sort.Strings(checks)
	return checks
}

func stringPtrValue(value string) *string {
	return &value
}

func boolPtrValue(value *bool) bool {
	return value != nil && *value
}

func persistedRunSecretsRequested(value *bool) bool {
	return value == nil || *value
}

func selectAgentOverallRun(runs []agentVerifyRunReport) agentVerifyRunReport {
	for _, run := range runs {
		if agentRunFailure(run) {
			return run
		}
	}
	for _, run := range runs {
		if !agentRunSucceeded(run) {
			return run
		}
	}
	for _, run := range runs {
		if run.Phase != "warm" {
			return run
		}
	}
	return runs[0]
}

func allAgentRunsSucceeded(runs []agentVerifyRunReport) bool {
	for _, run := range runs {
		if !agentRunSucceeded(run) {
			return false
		}
	}
	return true
}

func agentRunSucceeded(run agentVerifyRunReport) bool {
	return run.Terminal && run.Status == preflightapi.StatusSucceeded && exitCodeValue(run.ExitCode) == 0
}

func agentRunFailure(run agentVerifyRunReport) bool {
	if agentRunSucceeded(run) {
		return false
	}
	if run.Terminal || exitCodeValue(run.ExitCode) != 0 {
		return true
	}
	switch run.Classification {
	case "", preflightapi.ClassificationSuccess, preflightapi.ClassificationPending:
		return false
	default:
		return true
	}
}

func latestAgentRunReportsByIdempotencyGroup(baseKey string, reports []agentVerifyRunReport) []agentVerifyRunReport {
	baseKey = strings.TrimSpace(baseKey)
	if baseKey == "" || len(reports) <= 1 {
		return reports
	}
	order := make([]string, 0, len(reports))
	latest := make(map[string]agentVerifyRunReport, len(reports))
	for _, report := range reports {
		group := preflightapi.AgentIdempotencyGroup(baseKey, report.IdempotencyKey)
		if _, ok := latest[group]; !ok {
			order = append(order, group)
		}
		latest[group] = report
	}
	out := make([]agentVerifyRunReport, 0, len(order))
	for _, group := range order {
		out = append(out, latest[group])
	}
	return out
}

func commonAgentCheckName(reports []agentVerifyRunReport) string {
	if len(reports) == 0 {
		return ""
	}
	check := reports[0].Check
	for _, report := range reports[1:] {
		if report.Check != check {
			return ""
		}
	}
	return check
}

func exitCodeForRunResult(result remoteRunResult, terminal bool) *int {
	if terminal {
		return intPtr(result.ExitCode)
	}
	if result.CommandExited && result.CommandExitCode != 0 {
		return intPtr(result.CommandExitCode)
	}
	return nil
}

func exitCodeForRunDetail(detail preflightapi.RunDetail, events []preflightapi.RunEvent) *int {
	status := effectiveRunStatus(detail.Summary.Status, events)
	if !preflightapi.IsTerminalRunStatus(status) {
		if detail.Summary.ExitCode != nil && *detail.Summary.ExitCode != 0 {
			value := *detail.Summary.ExitCode
			return &value
		}
		if exitCode := nonZeroCommandExitCode(events); exitCode != nil {
			return exitCode
		}
		return nil
	}
	if exitCode := terminalExitCode(events); exitCode != nil {
		return exitCode
	}
	if detail.Summary.ExitCode != nil {
		value := *detail.Summary.ExitCode
		return &value
	}
	if status != preflightapi.StatusSucceeded {
		return intPtr(1)
	}
	return intPtr(0)
}

func effectiveRunStatus(summaryStatus string, events []preflightapi.RunEvent) string {
	if status := terminalStatusFromEvents(events); status != "" {
		return status
	}
	return summaryStatus
}

func terminalStatusFromEvents(events []preflightapi.RunEvent) string {
	for i := len(events) - 1; i >= 0; i-- {
		switch events[i].Type {
		case preflightapi.EventRunFinished:
			return terminalEventStatus(events[i], preflightapi.StatusSucceeded)
		case preflightapi.EventRunFailed:
			return terminalEventStatus(events[i], preflightapi.StatusFailed)
		case preflightapi.EventRunTimedOut:
			return terminalEventStatus(events[i], preflightapi.StatusTimedOut)
		case preflightapi.EventRunCancelled:
			return terminalEventStatus(events[i], preflightapi.StatusCancelled)
		}
	}
	return ""
}

func terminalEventStatus(event preflightapi.RunEvent, fallback string) string {
	if preflightapi.IsTerminalRunStatus(event.Status) {
		return event.Status
	}
	return fallback
}

func nonZeroCommandExitCode(events []preflightapi.RunEvent) *int {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != preflightapi.EventCommandExited || events[i].ExitCode == nil || *events[i].ExitCode == 0 {
			continue
		}
		value := *events[i].ExitCode
		return &value
	}
	return nil
}
