package main

import (
	"github.com/DeanStr/readyci-cli/pkg/config"
)

func executeResolvedCheck(name string, cfg config.ProjectConfig, configPath string, resolveOptions checkResolveOptions, executionOptions checkExecutionOptions) error {
	plan, err := resolveCheckRunPlan(name, cfg, resolveOptions)
	if err != nil {
		return err
	}
	return executeCheckPlan(plan, cfg, configPath, executionOptions)
}

func executeCheckPlan(plan checkRunPlan, cfg config.ProjectConfig, configPath string, executionOptions checkExecutionOptions) error {
	if executionOptions.DryRun {
		ctx, stop := signalContext()
		defer stop()
		token, tokenErr := effectiveAPIToken(executionOptions.APIURL, executionOptions.APIToken)
		diagnosticPlan := applyProjectHomeRegionForDiagnosticsBestEffort(ctx, plan, executionOptions.APIURL, token)
		report, err := buildCheckDryRunReport(ctx, ".", cfg, configPath, diagnosticPlan)
		if err != nil {
			return err
		}
		if executionOptions.SelectionMode != "warm" {
			preparation, err := dependencyWarmPreview(".", []affectedCheckPlan{{Plan: plan}}, cfg, checkResolveOptions{APIURL: plan.Options.APIURL, ProjectID: plan.Options.ProjectID, Changes: plan.Options.ChangeMode, SkipWarm: plan.SkipWarm})
			if err != nil {
				return err
			}
			report.Warnings = append(report.Warnings, preparation...)
		}
		if tokenErr != nil {
			report.Warnings = append(report.Warnings, "token-based diagnostics skipped: "+tokenErr.Error())
		}
		if executionOptions.Why {
			report.Plan = ptrVerificationPlan(buildCheckVerificationPlan(cfg, configPath, diagnosticPlan, verificationPlanOptions{
				Mode:           defaultString(executionOptions.SelectionMode, "check"),
				Inferred:       executionOptions.Inferred,
				Signals:        executionOptions.Signals,
				SelectedReason: executionOptions.SelectedReason,
			}))
		}
		if executionOptions.JSONOutput {
			return writeJSONOutput(report)
		}
		if executionOptions.SummaryOutput {
			printCheckDryRunSummary(report)
			return nil
		}
		printCheckDryRunReport(report)
		return nil
	}
	verification := buildCheckVerificationPlan(cfg, configPath, plan, verificationPlanOptions{
		Mode:           defaultString(executionOptions.SelectionMode, "check"),
		Inferred:       executionOptions.Inferred,
		Signals:        executionOptions.Signals,
		SelectedReason: executionOptions.SelectedReason,
	})
	if executionOptions.Why && !executionOptions.JSONOutput {
		printVerificationPlan(verification)
	}
	token, err := effectiveAPIToken(executionOptions.APIURL, executionOptions.APIToken)
	if err != nil {
		return err
	}
	plan.Options.APIToken = token
	warm, err := dependencyWarmPlansForAffected(".", []affectedCheckPlan{{Plan: plan}}, cfg, checkResolveOptions{APIURL: plan.Options.APIURL, ProjectID: plan.Options.ProjectID, Changes: plan.Options.ChangeMode, SkipWarm: plan.SkipWarm})
	if err != nil {
		return err
	}
	warmChecks := make([]checkRunPlan, 0, len(warm))
	for _, selected := range warm {
		warmChecks = append(warmChecks, selected.Plan)
	}
	if executionOptions.Watch {
		return watchCheck(plan, warmChecks, token, executionOptions.WatchInterval)
	}
	return executeSessionPlans([]checkRunPlan{plan}, warmChecks, token, executionOptions.JSONOutput, verification)
}

func ptrVerificationPlan(plan verificationPlan) *verificationPlan {
	return &plan
}

func checkPlanRequiresSuccessfulStatus(plan checkRunPlan) bool {
	return plan.WhenConfig.Status == "success" || (plan.When != nil && plan.When.Status == "success")
}
