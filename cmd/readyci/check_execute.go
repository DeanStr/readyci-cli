package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

func executeResolvedCheck(name string, cfg config.ProjectConfig, configPath string, resolveOptions checkResolveOptions, executionOptions checkExecutionOptions) error {
	plan, err := resolveCheckRunPlan(name, cfg, resolveOptions)
	if err != nil {
		return err
	}
	return executeCheckPlan(plan, cfg, configPath, executionOptions)
}

func executeAffectedResolvedCheck(name string, cfg config.ProjectConfig, configPath string, resolveOptions checkResolveOptions, executionOptions checkExecutionOptions) error {
	plan, err := resolveCheckRunPlan(name, cfg, resolveOptions)
	if err != nil {
		return err
	}
	plan.Options.IncludeUntracked = affectedCheckIncludeUntracked(cfg.Checks[name], plan.Options.ChangeMode)
	cfg = projectConfigWithAffectedCheckSyncOverride(cfg, name, plan.Options.IncludeUntracked)
	if executionOptions.WarmDependencies && !executionOptions.DryRun && !executionOptions.Watch {
		warmPlans, err := dependencyWarmPlansForAffected(".", []affectedCheckPlan{{Plan: plan}}, cfg, resolveOptions)
		if err != nil {
			return err
		}
		if len(warmPlans) > 0 {
			token, err := effectiveAPIToken(executionOptions.APIURL, executionOptions.APIToken)
			if err != nil {
				return err
			}
			if executionOptions.JSONOutput {
				verification := buildCheckVerificationPlan(cfg, configPath, plan, verificationPlanOptions{
					Mode:           defaultString(executionOptions.SelectionMode, "check"),
					Inferred:       executionOptions.Inferred,
					Signals:        executionOptions.Signals,
					SelectedReason: executionOptions.SelectedReason,
				})
				warmReport, warmErr := executeAffectedPlansJSONReport(warmPlans, token, verification)
				markCheckExecutionRunPhase(warmReport.CreatedRuns, "warm")
				if warmErr != nil && !isSuccessfulExit(warmErr) {
					warmReport.Result.Summary = defaultString(warmReport.Result.Summary, "dependency warm failed")
					if err := writeJSONOutput(warmReport); err != nil {
						return err
					}
					return warmErr
				}
				executionReport, runErr := executeCheckPlanJSONReport(plan, verification, token)
				markCheckExecutionRunPhase(executionReport.CreatedRuns, "verify")
				executionReport.CreatedRuns = append(warmReport.CreatedRuns, executionReport.CreatedRuns...)
				if err := writeJSONOutput(executionReport); err != nil {
					return err
				}
				return runErr
			}
			fmt.Fprintf(os.Stderr, "readyci: warming dependencies for selected check: %s\n", strings.Join(affectedPlanNames(warmPlans), ", "))
			if err := executeAffectedPlans(warmPlans, token); err != nil && !isSuccessfulExit(err) {
				return err
			}
			executionOptions.APIToken = token
		}
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
	if executionOptions.JSONOutput {
		token, err := effectiveAPIToken(executionOptions.APIURL, executionOptions.APIToken)
		if err != nil {
			return err
		}
		report, runErr := executeCheckPlanJSONReport(plan, verification, token)
		if err := writeJSONOutput(report); err != nil {
			return err
		}
		return runErr
	}
	if executionOptions.Why {
		printVerificationPlan(verification)
	}
	token, err := effectiveAPIToken(executionOptions.APIURL, executionOptions.APIToken)
	if err != nil {
		return err
	}
	plan.Options.APIToken = token
	if executionOptions.Watch {
		return watchCheck(plan.Options, plan.Matrix, plan.Retries, executionOptions.WatchInterval)
	}
	return executeCheckMatrix(plan.Options, plan.Matrix, plan.Retries)
}

func projectConfigWithAffectedCheckSyncOverride(cfg config.ProjectConfig, checkName string, includeUntracked *bool) config.ProjectConfig {
	if includeUntracked == nil {
		return cfg
	}
	check, ok := cfg.Checks[checkName]
	if !ok {
		return cfg
	}
	check.Sync.IncludeUntracked = includeUntracked
	checks := make(map[string]config.CheckConfig, len(cfg.Checks))
	for name, existing := range cfg.Checks {
		checks[name] = existing
	}
	checks[checkName] = check
	cfg.Checks = checks
	return cfg
}

func ptrVerificationPlan(plan verificationPlan) *verificationPlan {
	return &plan
}

func selectAffectedCheck(cfg config.ProjectConfig, explicit string, staged bool, workingCandidates, stagedCandidates []string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	candidates := workingCandidates
	label := "affected"
	if staged {
		candidates = stagedCandidates
		label = "staged affected"
	}
	for _, candidate := range candidates {
		if _, ok := cfg.Checks[candidate]; ok {
			return candidate, nil
		}
	}
	available := sortedCheckNames(cfg.Checks)
	if len(available) == 0 {
		return "", fmt.Errorf("no configured checks found; run readyci init --detect or define a %s check in .readyci.yml", label)
	}
	return "", fmt.Errorf("no %s check found (looked for: %s; available: %s); pass --check NAME to choose a configured check", label, strings.Join(candidates, ", "), strings.Join(available, ", "))
}
