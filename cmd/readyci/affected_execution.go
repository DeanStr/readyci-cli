package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

func executeAffectedRules(cwd string, cfg config.ProjectConfig, configPath string, resolveOptions checkResolveOptions, executionOptions checkExecutionOptions) error {
	expanded, warnings, err := expandAffectedConfigForConfigPath(cfg, configPath)
	if err != nil {
		return err
	}
	if err := affectedProfileExpansionError(cfg, expanded, warnings); err != nil {
		return err
	}
	cfg = projectConfigWithCheckWhenRules(expanded)
	for _, warning := range warnings {
		fmt.Fprintf(os.Stderr, "readyci: warning: %s\n", warning)
	}
	if err := validateAffectedRulesConfig(cfg); err != nil {
		return err
	}
	if executionOptions.DryRun {
		ctx, stop := signalContext()
		defer stop()
		plans, report, err := buildAffectedRulePlans(ctx, cwd, cfg, configPath, resolveOptions)
		if err != nil {
			return err
		}
		token, tokenErr := effectiveAPIToken(executionOptions.APIURL, executionOptions.APIToken)
		_, report = applyProjectHomeRegionsForAffectedDiagnosticsBestEffort(ctx, plans, report, executionOptions.APIURL, token)
		if tokenErr != nil {
			report.Warnings = append(report.Warnings, "token-based diagnostics skipped: "+tokenErr.Error())
		}
		if executionOptions.Why {
			report.Plan = ptrVerificationPlan(affectedVerificationPlanForExecution(cfg, configPath, report, executionOptions))
		}
		if executionOptions.JSONOutput {
			return writeJSONOutput(report)
		}
		if executionOptions.SummaryOutput {
			printAffectedDryRunSummary(report)
			return nil
		}
		printAffectedDryRunReport(report)
		return nil
	}

	if executionOptions.Watch {
		if executionOptions.Why {
			ctx, stop := signalContext()
			plans, report, err := buildAffectedRulePlans(ctx, cwd, cfg, configPath, resolveOptions)
			if err != nil {
				stop()
				return err
			}
			token, tokenErr := effectiveAPIToken(executionOptions.APIURL, executionOptions.APIToken)
			_, report = applyProjectHomeRegionsForAffectedDiagnosticsBestEffort(ctx, plans, report, executionOptions.APIURL, token)
			if tokenErr != nil {
				report.Warnings = append(report.Warnings, "token-based diagnostics skipped: "+tokenErr.Error())
			}
			stop()
			printVerificationPlan(affectedVerificationPlanForExecution(cfg, configPath, report, executionOptions))
		}
		token, err := effectiveAPIToken(executionOptions.APIURL, executionOptions.APIToken)
		if err != nil {
			return err
		}
		return watchAffectedRules(cwd, cfg, configPath, resolveOptions, executionOptions.WatchInterval, token)
	}

	ctx, stop := signalContext()
	defer stop()
	plans, report, err := buildAffectedRulePlans(ctx, cwd, cfg, configPath, resolveOptions)
	if err != nil {
		return err
	}
	diagnosticToken, diagnosticTokenErr := effectiveAPIToken(executionOptions.APIURL, executionOptions.APIToken)
	plans, report = applyProjectHomeRegionsForAffectedDiagnosticsBestEffort(ctx, plans, report, executionOptions.APIURL, diagnosticToken)
	if diagnosticTokenErr != nil {
		report.Warnings = append(report.Warnings, "token-based diagnostics skipped: "+diagnosticTokenErr.Error())
	}
	if executionOptions.Why || executionOptions.JSONOutput {
		report.Plan = ptrVerificationPlan(affectedVerificationPlanForExecution(cfg, configPath, report, executionOptions))
	}
	if len(plans) == 0 {
		if executionOptions.JSONOutput {
			verification := verificationPlan{}
			if report.Plan != nil {
				verification = *report.Plan
			}
			return writeJSONOutput(skippedCheckExecutionReport(verification, "no affected checks selected"))
		}
		if report.Plan != nil {
			printVerificationPlan(*report.Plan)
		}
		fmt.Fprintln(os.Stderr, "readyci: no affected checks selected")
		return nil
	}
	token, err := effectiveAPIToken(executionOptions.APIURL, executionOptions.APIToken)
	if err != nil {
		return err
	}
	var warmPlans []affectedCheckPlan
	if executionOptions.WarmDependencies {
		warmPlans, err = dependencyWarmPlansForAffected(cwd, plans, cfg, resolveOptions)
		if err != nil {
			return err
		}
	}
	if executionOptions.JSONOutput {
		verification := verificationPlan{}
		if report.Plan != nil {
			verification = *report.Plan
		}
		var warmRuns []checkExecutionRunReport
		if executionOptions.WarmDependencies && len(warmPlans) > 0 {
			warmReport, warmErr := executeAffectedPlansJSONReport(warmPlans, token, verification)
			markCheckExecutionRunPhase(warmReport.CreatedRuns, "warm")
			warmRuns = append(warmRuns, warmReport.CreatedRuns...)
			if warmErr != nil && !isSuccessfulExit(warmErr) {
				warmReport.Result.Summary = defaultString(warmReport.Result.Summary, "dependency warm failed")
				if err := writeJSONOutput(warmReport); err != nil {
					return err
				}
				return warmErr
			}
		}
		executionReport, runErr := executeAffectedPlansJSONReport(plans, token, verification)
		markCheckExecutionRunPhase(executionReport.CreatedRuns, "verify")
		executionReport.CreatedRuns = append(warmRuns, executionReport.CreatedRuns...)
		if err := writeJSONOutput(executionReport); err != nil {
			return err
		}
		return runErr
	}
	if report.Plan != nil {
		printVerificationPlan(*report.Plan)
	}
	if executionOptions.WarmDependencies && len(warmPlans) > 0 {
		fmt.Fprintf(os.Stderr, "readyci: warming dependencies for selected affected checks: %s\n", strings.Join(affectedPlanNames(warmPlans), ", "))
		if err := executeAffectedPlans(warmPlans, token); err != nil && !isSuccessfulExit(err) {
			return err
		}
	}
	return executeAffectedPlans(plans, token)
}

func affectedVerificationPlanForExecution(cfg config.ProjectConfig, configPath string, report affectedDryRunReport, executionOptions checkExecutionOptions) verificationPlan {
	return buildAffectedVerificationPlan(cfg, configPath, report, verificationPlanOptions{
		Mode:           defaultString(executionOptions.SelectionMode, "affected_rules"),
		Inferred:       executionOptions.Inferred,
		Signals:        executionOptions.Signals,
		SelectedReason: executionOptions.SelectedReason,
	})
}

func runAffectedRulesOnce(ctx context.Context, cwd string, cfg config.ProjectConfig, configPath string, resolveOptions checkResolveOptions, token string) error {
	plans, _, err := buildAffectedRulePlans(ctx, cwd, cfg, configPath, resolveOptions)
	if err != nil {
		return err
	}
	if len(plans) == 0 {
		fmt.Fprintln(os.Stderr, "readyci: no affected checks selected")
		return nil
	}
	return executeAffectedPlans(plans, token)
}

func executeAffectedPlans(plans []affectedCheckPlan, token string) error {
	return executeAffectedPlansWithRunner(plans, token, executeCheckMatrix)
}

func affectedPlanNames(plans []affectedCheckPlan) []string {
	names := make([]string, 0, len(plans))
	for _, plan := range plans {
		names = append(names, plan.Plan.Name)
	}
	return names
}

func markCheckExecutionRunPhase(runs []checkExecutionRunReport, phase string) {
	for i := range runs {
		runs[i].Phase = phase
	}
}

func executeAffectedPlansJSONReport(plans []affectedCheckPlan, token string, verification verificationPlan) (checkExecutionReport, error) {
	groupKey, err := newIdempotencyKey()
	if err != nil {
		return checkExecutionReport{}, err
	}
	var runs []checkExecutionRunReport
	var lastResult checkExecutionResultReport
	var lastFailureResult checkExecutionResultReport
	var lastErr error
	var successErr error
	for _, selected := range plans {
		runPlan := selected.Plan
		if lastErr != nil && checkPlanRequiresSuccessfulStatus(runPlan) {
			fmt.Fprintf(os.Stderr, "readyci: affected path rules skipped %s because a previous check failed\n", runPlan.Name)
			continue
		}
		runPlan.Options.APIToken = token
		runPlan.Options.RunGroupKey = groupKey
		runPlan.Options.RunGroupKind = "affected"
		runPlan.Options.RunGroupName = "affected checks"
		if strings.TrimSpace(runPlan.Options.IdempotencyKey) == "" {
			runPlan.Options.IdempotencyKey = groupKey + ":check=" + runPlan.Name
		}
		runPlan.Options.LogicalChildID = defaultString(runPlan.Options.LogicalChildID, runPlan.Name)
		runPlan.Options.LogicalChildName = defaultString(runPlan.Options.LogicalChildName, runPlan.Name)
		runPlan.Options.CommandAlias = defaultString(runPlan.Options.CommandAlias, runPlan.Name)
		selectedRuns, result, err := executeCheckMatrixForReport(runPlan.Options, runPlan.Matrix, runPlan.Retries)
		runs = append(runs, selectedRuns...)
		lastResult = result
		if err == nil {
			continue
		}
		if isSuccessfulExit(err) {
			successErr = err
			continue
		}
		lastErr = err
		lastFailureResult = result
		var exitErr exitCodeError
		if !errors.As(err, &exitErr) {
			return checkExecutionReport{Plan: &verification, CreatedRuns: runs, Result: result}, err
		}
	}
	if lastErr != nil {
		return checkExecutionReport{Plan: &verification, CreatedRuns: runs, Result: lastFailureResult}, lastErr
	}
	return checkExecutionReport{Plan: &verification, CreatedRuns: runs, Result: lastResult}, successErr
}

func executeAffectedPlansWithRunner(plans []affectedCheckPlan, token string, run func(remoteRunOptions, config.MatrixConfig, int) error) error {
	var lastErr error
	var successErr error
	groupKey, err := newIdempotencyKey()
	if err != nil {
		return err
	}
	for _, selected := range plans {
		runPlan := selected.Plan
		if lastErr != nil && checkPlanRequiresSuccessfulStatus(runPlan) {
			fmt.Fprintf(os.Stderr, "readyci: affected path rules skipped %s because a previous check failed\n", runPlan.Name)
			continue
		}
		runPlan.Options.APIToken = token
		runPlan.Options.RunGroupKey = groupKey
		runPlan.Options.RunGroupKind = "affected"
		runPlan.Options.RunGroupName = "affected checks"
		if strings.TrimSpace(runPlan.Options.IdempotencyKey) == "" {
			runPlan.Options.IdempotencyKey = groupKey + ":check=" + runPlan.Name
		}
		runPlan.Options.LogicalChildID = defaultString(runPlan.Options.LogicalChildID, runPlan.Name)
		runPlan.Options.LogicalChildName = defaultString(runPlan.Options.LogicalChildName, runPlan.Name)
		fmt.Fprintf(os.Stderr, "readyci: affected path rules selected %s\n", runPlan.Name)
		if err := run(runPlan.Options, runPlan.Matrix, runPlan.Retries); err != nil {
			if isSuccessfulExit(err) {
				successErr = err
				continue
			}
			lastErr = err
			var exitErr exitCodeError
			if !errors.As(err, &exitErr) {
				return err
			}
		}
	}
	if lastErr != nil {
		return lastErr
	}
	return successErr
}

func checkPlanRequiresSuccessfulStatus(plan checkRunPlan) bool {
	return plan.When != nil && strings.TrimSpace(plan.When.Status) == "success"
}
