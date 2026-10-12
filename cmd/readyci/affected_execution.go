package main

import (
	"context"

	"fmt"
	"os"

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
		preparation, err := dependencyWarmPreview(cwd, plans, cfg, resolveOptions)
		if err != nil {
			return err
		}
		report.Warnings = append(report.Warnings, preparation...)
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
	warmPlans, err := dependencyWarmPlansForAffected(cwd, plans, cfg, resolveOptions)
	if err != nil {
		return err
	}
	checks, warm := make([]checkRunPlan, 0, len(plans)), make([]checkRunPlan, 0, len(warmPlans))
	for _, selected := range plans {
		checks = append(checks, selected.Plan)
	}
	for _, selected := range warmPlans {
		warm = append(warm, selected.Plan)
	}
	return executeSessionPlans(checks, warm, token, executionOptions.JSONOutput, affectedVerificationPlanForExecution(cfg, configPath, report, executionOptions))
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
	warm, err := dependencyWarmPlansForAffected(cwd, plans, cfg, resolveOptions)
	if err != nil {
		return err
	}
	checks, preparation := []checkRunPlan{}, []checkRunPlan{}
	for _, p := range plans {
		checks = append(checks, p.Plan)
	}
	for _, p := range warm {
		preparation = append(preparation, p.Plan)
	}
	return executeSessionPlans(checks, preparation, token, false, verificationPlan{Mode: "affected_rules"})
}

func executeAffectedPlans(plans []affectedCheckPlan, token string) error {
	checks := make([]checkRunPlan, 0, len(plans))
	for _, selected := range plans {
		checks = append(checks, selected.Plan)
	}
	return executeSessionPlans(checks, nil, token, false)
}

func affectedPlanNames(plans []affectedCheckPlan) []string {
	names := make([]string, 0, len(plans))
	for _, plan := range plans {
		names = append(names, plan.Plan.Name)
	}
	return names
}
