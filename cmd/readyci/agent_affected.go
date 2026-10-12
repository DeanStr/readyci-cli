package main

import (
	"context"
	"fmt"
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
