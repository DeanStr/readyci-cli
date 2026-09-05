package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func buildAgentAffectedPlanReport(apiURL, projectID, changes, goal, intent string, requestSecrets bool, cfg config.ProjectConfig, configPath string, inferred bool, signals []string) (agentPlanReport, error) {
	ctx, stop := signalContext()
	defer stop()
	resolveOptions := agentResolveOptionsForIntent(intent, checkResolveOptions{
		APIURL:    apiURL,
		ProjectID: projectID,
		Changes:   changes,
	})
	plans, dryRun, warnings, err := buildAgentAffectedRulePlans(ctx, ".", cfg, configPath, resolveOptions)
	if err != nil {
		return agentPlanReport{}, err
	}
	plans, dryRun = agentAugmentAffectedPlansForIntent(cfg, plans, dryRun, intent, resolveOptions)
	plans = applyAgentAffectedPlanOptionsForIntent(intent, requestSecrets, plans)
	status := "ready"
	reason := "matched_changed_paths"
	if len(plans) == 0 {
		status = "skipped"
		reason = "no_affected_rules_matched"
	}
	report := agentPlanReport{
		SchemaVersion: agentSchemaVersion,
		Status:        status,
		Mode:          "affected_rules",
		Intent:        intent,
		Goal:          strings.TrimSpace(goal),
		Reason:        reason,
		ProjectID:     dryRun.ProjectID,
		ChangeMode:    dryRun.ChangeMode,
		Checks:        agentAffectedChecksFromDryRun(dryRun),
		Plan: ptrVerificationPlan(buildAffectedVerificationPlan(cfg, configPath, dryRun, verificationPlanOptions{
			Goal:           goal,
			Intent:         intent,
			Mode:           "affected_rules",
			Inferred:       inferred,
			Signals:        signals,
			SelectedReason: "Configured affected rules decide which checks match the changed files.",
		})),
		Policy:        agentPolicyForPlansForIntent(dryRun.ChangeMode, intent, requestSecrets, checkRunPlansFromAffected(plans)),
		Warnings:      agentWarningsForAffectedProfileExpansion(warnings),
		VerifyCommand: agentAffectedVerifyCommand(apiURL, dryRun.ProjectID, dryRun.ChangeMode, intent, requestSecrets),
	}
	if len(plans) == 1 {
		plan := plans[0].Plan
		report.Check = plan.Name
		report.Command = plan.Options.Command
		report.RunnerSize = defaultString(plan.Options.RunnerSize, preflightapi.DefaultRunnerSize)
		report.TimeoutSeconds = defaultInt(plan.Options.TimeoutSeconds, defaultRunTimeoutSeconds)
		for _, variant := range plan.Variants {
			if variant.Name != "" {
				report.MatrixVariants = append(report.MatrixVariants, variant.Name)
			}
		}
		report.Estimated = &agentCheckEstimatedCost{RunnerSize: report.RunnerSize, TimeoutSeconds: report.TimeoutSeconds}
	}
	return report, nil
}

func checkRunPlansFromAffected(plans []affectedCheckPlan) []checkRunPlan {
	out := make([]checkRunPlan, 0, len(plans))
	for _, plan := range plans {
		out = append(out, plan.Plan)
	}
	return out
}

func buildAgentAffectedVerifyReport(apiURL, apiToken, projectID, idempotencyKey, changes, goal, intent string, requestSecrets bool, retryCount int, retryProvided bool, cfg config.ProjectConfig, configPath string, progress agentProgressSink) (agentVerifyReport, error) {
	ctx, stop := signalContext()
	defer stop()
	resolveOptions := agentResolveOptionsForIntent(intent, checkResolveOptions{
		APIURL:        apiURL,
		ProjectID:     projectID,
		Changes:       changes,
		RetryCount:    retryCount,
		RetryProvided: retryProvided,
	})
	plans, dryRun, warnings, err := buildAgentAffectedRulePlans(ctx, ".", cfg, configPath, resolveOptions)
	if err != nil {
		return agentCommandErrorReport(preflightapi.ClassificationConfigInvalid, err), err
	}
	plans, dryRun = agentAugmentAffectedPlansForIntent(cfg, plans, dryRun, intent, resolveOptions)
	plans = applyAgentAffectedPlanOptionsForIntent(intent, requestSecrets, plans)
	checks := agentAffectedChecksFromDryRun(dryRun)
	agentWarnings := agentWarningsForAffectedProfileExpansion(warnings)
	if len(plans) == 0 {
		emitAgentProgress(progress, agentProgressEvent{Kind: agentProgressPlanned, Mode: "affected checks"})
		report := agentVerifyReport{
			SchemaVersion:  agentSchemaVersion,
			Status:         "skipped",
			Mode:           "affected_rules",
			Intent:         intent,
			Goal:           strings.TrimSpace(goal),
			Reason:         "no_affected_rules_matched",
			GroupKind:      "affected_rules",
			Terminal:       true,
			Classification: preflightapi.ClassificationSuccess,
			Retryable:      false,
			AgentAction:    preflightapi.AgentActionNone,
			Summary:        "No affected rules matched changed files.",
			Checks:         checks,
			Runs:           []agentVerifyRunReport{},
			Policy:         agentPolicyForPlansForIntent(dryRun.ChangeMode, intent, requestSecrets, nil),
			Warnings:       agentWarnings,
			CLIExitCode:    cliExitSuccess,
		}
		return report, nil
	}
	token, err := effectiveAPIToken(apiURL, apiToken)
	if err != nil {
		return agentCommandErrorReport(preflightapi.ClassificationAuthMissing, err), err
	}
	if strings.TrimSpace(token) == "" {
		err := errors.New("no API token found; set READYCI_API_TOKEN")
		return agentCommandErrorReport(preflightapi.ClassificationAuthMissing, err), err
	}
	baseKey, err := normalizeIdempotencyKey(idempotencyKey)
	if err != nil {
		return agentCommandErrorReport(preflightapi.ClassificationConfigInvalid, err), err
	}
	if baseKey == "" {
		baseKey, err = newIdempotencyKey()
		if err != nil {
			return agentCommandErrorReport(preflightapi.ClassificationInternalError, err), err
		}
	}
	report := agentVerifyReport{
		SchemaVersion: agentSchemaVersion,
		Status:        "running",
		Mode:          "affected_rules",
		Intent:        intent,
		Goal:          strings.TrimSpace(goal),
		Reason:        "matched_changed_paths",
		GroupKind:     "affected_rules",
		Checks:        checks,
		Policy:        agentPolicyForPlansForIntent(dryRun.ChangeMode, intent, requestSecrets, checkRunPlansFromAffected(plans)),
		Warnings:      agentWarnings,
	}
	emitAgentProgress(progress, agentProgressEvent{Kind: agentProgressPlanned, Mode: "affected checks", Checks: affectedPlanNames(plans)})
	for i := range plans {
		plans[i].Plan.Options.Progress = progress
	}
	warmPlans, err := agentDependencyWarmPlansForAffected(".", plans, cfg, resolveOptions, intent)
	if err != nil {
		return agentCommandErrorReport(preflightapi.ClassificationConfigInvalid, err), err
	}
	if len(warmPlans) > 0 {
		for i := range warmPlans {
			warmPlans[i].Plan.Options.Progress = progress
		}
		runs, warmErr := executeAgentAffectedPlans(warmPlans, token, baseKey, intent, "warm")
		report.Runs = append(report.Runs, runs...)
		if warmErr != nil && !isSuccessfulExit(warmErr) {
			report = summarizeAgentVerifyRuns(report)
			return report, warmErr
		}
	}
	runs, runErr := executeAgentAffectedPlans(plans, token, baseKey, intent, "verify")
	report.Runs = append(report.Runs, runs...)
	report = summarizeAgentVerifyRuns(report)
	if intent == agentIntentFixtureUpdate {
		report.NextActions = append(report.NextActions, agentFixtureArtifactActions(apiURL, report.RunID, agentAffectedArtifactPaths(plans))...)
	}
	return report, runErr
}

func agentDependencyWarmPlansForAffected(cwd string, plans []affectedCheckPlan, cfg config.ProjectConfig, resolveOptions checkResolveOptions, intent string) ([]affectedCheckPlan, error) {
	if intent == agentIntentSecurity {
		return nil, nil
	}
	warmPlans, err := dependencyWarmPlansForAffected(cwd, plans, cfg, resolveOptions)
	if err != nil {
		return nil, err
	}
	return applyAgentAffectedPlanOptionsForIntent(intent, false, warmPlans), nil
}

func executeAgentAffectedPlans(plans []affectedCheckPlan, token, baseKey, intent, phase string) ([]agentVerifyRunReport, error) {
	return executeAgentAffectedPlansWithRunner(plans, token, baseKey, intent, phase, executeAgentCheckMatrix)
}

type agentPlanRunFunc func(checkRunPlan) ([]agentVerifyRunReport, error)

func executeAgentAffectedPlansWithRunner(plans []affectedCheckPlan, token, baseKey, intent, phase string, run agentPlanRunFunc) ([]agentVerifyRunReport, error) {
	var reports []agentVerifyRunReport
	var lastErr error
	var successErr error
	for _, selected := range plans {
		plan := selected.Plan
		if lastErr != nil && checkPlanRequiresSuccessfulStatus(plan) {
			if plan.Options.Progress != nil {
				plan.Options.Progress.Emit(agentProgressEvent{Kind: agentProgressSkipped, RunName: plan.Name})
			} else {
				fmt.Fprintf(os.Stderr, "readyci: affected path rules skipped %s because a previous check failed\n", plan.Name)
			}
			continue
		}
		plan.Options.APIToken = token
		plan.Options.RunGroupKey = baseKey
		plan.Options.RunGroupKind = "affected_rules"
		plan.Options.RunGroupName = agentRunGroupNameForIntent(intent, "affected verification")
		childID := defaultString(plan.Options.LogicalChildID, plan.Name)
		plan.Options.LogicalChildID = childID
		plan.Options.LogicalChildName = defaultString(plan.Options.LogicalChildName, plan.Name)
		plan.Options.IdempotencyKey = agentAffectedChildIdempotencyKey(baseKey, childID)
		runs, runErr := run(plan)
		for i := range runs {
			runs[i].Phase = phase
		}
		reports = append(reports, runs...)
		if runErr == nil {
			continue
		}
		if isSuccessfulExit(runErr) {
			successErr = runErr
			continue
		}
		lastErr = runErr
		var exitErr exitCodeError
		if !errors.As(runErr, &exitErr) {
			break
		}
	}
	if lastErr != nil {
		return reports, lastErr
	}
	return reports, successErr
}

func agentAugmentAffectedPlansForIntent(cfg config.ProjectConfig, plans []affectedCheckPlan, report affectedDryRunReport, intent string, resolveOptions checkResolveOptions) ([]affectedCheckPlan, affectedDryRunReport) {
	return plans, report
}

func agentPrePRReasonPath(paths []string) string {
	for _, path := range paths {
		if strings.TrimSpace(path) != "" {
			return path
		}
	}
	return "changed files"
}

func agentAffectedArtifactPaths(plans []affectedCheckPlan) []string {
	seen := map[string]bool{}
	var paths []string
	for _, selected := range plans {
		for _, path := range selected.Plan.Options.ArtifactPaths {
			if path == "" || seen[path] {
				continue
			}
			seen[path] = true
			paths = append(paths, path)
		}
	}
	return paths
}

func buildAgentAffectedRulePlans(ctx context.Context, cwd string, cfg config.ProjectConfig, configPath string, resolveOptions checkResolveOptions) ([]affectedCheckPlan, affectedDryRunReport, []string, error) {
	expanded, warnings, err := expandAffectedConfigForConfigPath(cfg, configPath)
	if err != nil {
		return nil, affectedDryRunReport{}, nil, cliConfigError(err.Error(), err)
	}
	if err := affectedProfileExpansionError(cfg, expanded, warnings); err != nil {
		return nil, affectedDryRunReport{}, nil, cliConfigError(err.Error(), err)
	}
	expanded = projectConfigWithCheckWhenRules(expanded)
	if err := validateAffectedRulesConfig(expanded); err != nil {
		return nil, affectedDryRunReport{}, nil, cliConfigError(err.Error(), err)
	}
	plans, report, err := buildAffectedRulePlans(ctx, cwd, expanded, configPath, resolveOptions)
	if err != nil {
		return nil, affectedDryRunReport{}, nil, cliConfigError(err.Error(), err)
	}
	return plans, report, warnings, nil
}

func agentWarningsForAffectedProfileExpansion(warnings []string) []agentWarning {
	if len(warnings) == 0 {
		return nil
	}
	out := make([]agentWarning, 0, len(warnings))
	for _, warning := range warnings {
		warning = strings.TrimSpace(warning)
		if warning == "" {
			continue
		}
		out = append(out, agentWarning{
			Section: "affected.profiles",
			Message: warning,
		})
	}
	return out
}

func agentAffectedChecksFromDryRun(report affectedDryRunReport) []agentAffectedCheck {
	checks := make([]agentAffectedCheck, 0, len(report.SelectedChecks)+len(report.SkippedChecks))
	for _, selected := range report.SelectedChecks {
		paths := make([]string, 0, len(selected.Reasons))
		pathSeen := make(map[string]bool, len(selected.Reasons))
		reasons := make([]string, 0, len(selected.Reasons))
		for _, reason := range selected.Reasons {
			if reason.Path != "" && !pathSeen[reason.Path] {
				pathSeen[reason.Path] = true
				paths = append(paths, reason.Path)
			}
			reasons = append(reasons, affectedRuleReasonText(reason))
		}
		checks = append(checks, agentAffectedCheck{
			Check:        selected.Name,
			Selected:     true,
			NetworkMode:  selected.NetworkMode,
			MatchedPaths: paths,
			Reasons:      reasons,
		})
	}
	for _, skipped := range report.SkippedChecks {
		reasons := make([]string, 0, len(skipped.Reasons)+1)
		if len(skipped.SubsumedBy) > 0 {
			reasons = append(reasons, "skipped because "+skipped.Name+" is subsumed by "+strings.Join(skipped.SubsumedBy, ", "))
		} else {
			reasons = append(reasons, "skipped because "+skipped.Reason)
		}
		for _, reason := range skipped.Reasons {
			reasons = append(reasons, affectedRuleReasonText(reason))
		}
		checks = append(checks, agentAffectedCheck{
			Check:        skipped.Name,
			Selected:     false,
			MatchedPaths: append([]string(nil), skipped.MatchedPaths...),
			Reasons:      reasons,
		})
	}
	return checks
}

func affectedRuleReasonText(match affectedRuleMatch) string {
	if match.Rule == 0 && match.Pattern == "" && match.Path == "" {
		return ""
	}
	if match.Rule <= 0 {
		if match.Path == "" {
			return match.Pattern
		}
		if match.Pattern == "affected.unmatched_checks" {
			return fmt.Sprintf("affected.unmatched_checks selected %s", match.Path)
		}
		return fmt.Sprintf("%s selected because %s changed", match.Pattern, match.Path)
	}
	return fmt.Sprintf("affected.rules[%d] matched %s with %s", match.Rule, match.Path, match.Pattern)
}

func agentAffectedChildIdempotencyKey(baseKey, check string) string {
	baseKey = strings.TrimSpace(baseKey)
	check = strings.TrimSpace(check)
	if baseKey == "" || check == "" {
		return baseKey
	}
	return baseKey + ":check=" + check
}

func agentAffectedVerifyCommand(apiURL, projectID, changes, intent string, requestSecrets bool) string {
	command := "readyci verify --json --progress=status"
	command += apiURLFlag(apiURL)
	if strings.TrimSpace(projectID) != "" {
		command += " --project " + shellQuoteArg(projectID)
	}
	if strings.TrimSpace(intent) != "" && intent != agentIntentEditLoop {
		command += " --intent " + shellQuoteArg(intent)
	}
	if strings.TrimSpace(changes) != "" {
		command += " --changes " + shellQuoteArg(changes)
	}
	if requestSecrets {
		command += " --request-secrets"
	}
	return command
}
