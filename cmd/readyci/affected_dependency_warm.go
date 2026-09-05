package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/verificationplan"
)

const dependencyWarmCheckName = "dependencies"

func dependencyWarmPlansForAffected(_ string, plans []affectedCheckPlan, cfg config.ProjectConfig, resolveOptions checkResolveOptions) ([]affectedCheckPlan, error) {
	const warmCheck = dependencyWarmCheckName
	if _, ok := cfg.Checks[warmCheck]; !ok {
		return nil, nil
	}
	targets := make([]checkRunPlan, 0, len(plans))
	for _, selected := range plans {
		if strings.TrimSpace(selected.Plan.Name) == warmCheck {
			continue
		}
		targets = append(targets, selected.Plan)
	}
	hasPersistentTarget := false
	for _, target := range checkPlansForPlanning(targets) {
		mode, err := verificationplan.NormalizeCacheMode(target.Run.CacheMode)
		if err != nil {
			return nil, fmt.Errorf("resolve dependency warm target %q cache mode: %w", target.Name, err)
		}
		if mode == "" || mode == "cache-only" {
			hasPersistentTarget = true
			break
		}
	}
	if !hasPersistentTarget {
		return nil, nil
	}
	warmResolve, err := warmDependencyResolveOptions(resolveOptions, cfg)
	if err != nil {
		return nil, err
	}
	warmResolve.Open = false
	plan, err := resolveCheckRunPlan(warmCheck, cfg, warmResolve)
	if err != nil {
		return nil, err
	}
	plan.Options.CommandAlias = defaultString(plan.Options.CommandAlias, warmCheck)
	resolvedWarmPlans, err := verificationplan.DependencyWarmPlansForTargets(
		checkPlansForPlanning([]checkRunPlan{plan})[0],
		checkPlansForPlanning(targets),
	)
	if err != nil {
		return nil, err
	}
	warmRunPlans := specializedDependencyWarmRunPlans(plan, resolvedWarmPlans)
	warmPlans := make([]affectedCheckPlan, 0, len(warmRunPlans))
	for _, warmRunPlan := range warmRunPlans {
		warmPlans = append(warmPlans, affectedCheckPlan{Plan: warmRunPlan})
	}
	return warmPlans, nil
}

func dependencyWarmTargetsForExplicitCheck(
	ctx context.Context,
	cwd string,
	cfg config.ProjectConfig,
	configPath string,
	resolveOptions checkResolveOptions,
	dependencyPlan checkRunPlan,
) ([]checkRunPlan, error) {
	if shouldUseAffectedRules(cfg, "") {
		selected, _, _, err := buildAgentAffectedRulePlans(ctx, cwd, cfg, configPath, resolveOptions)
		if err != nil {
			return nil, err
		}
		targets := make([]checkRunPlan, 0, len(selected))
		for _, item := range selected {
			if strings.TrimSpace(item.Plan.Name) != dependencyWarmCheckName {
				targets = append(targets, item.Plan)
			}
		}
		if len(targets) == 0 && len(cfg.Checks) == 1 {
			return []checkRunPlan{dependencyPlan}, nil
		}
		if len(targets) == 0 {
			err := fmt.Errorf("explicit dependency warm check %q has no affected consumer checks to warm", dependencyWarmCheckName)
			return nil, cliConfigError(err.Error(), err)
		}
		return targets, nil
	}

	// A policy without affected rules cannot identify one intended consumer.
	// Warm every configured consumer, failing closed if their cache contracts
	// cannot share a dependency warm operation. A dependency-only policy still
	// has a useful standalone warm lane, so retain the named check as its target.
	targets := make([]checkRunPlan, 0, len(cfg.Checks))
	for _, name := range verificationplan.SortedCheckNames(cfg.Checks) {
		if name == dependencyWarmCheckName {
			continue
		}
		plan, err := resolveCheckRunPlan(name, cfg, resolveOptions)
		if err != nil {
			return nil, err
		}
		targets = append(targets, plan)
	}
	if len(targets) == 0 {
		targets = append(targets, dependencyPlan)
	}
	return targets, nil
}

func dependencyWarmPlansForExplicitCheck(plan checkRunPlan, targets []checkRunPlan, cfg config.ProjectConfig, resolveOptions checkResolveOptions) ([]checkRunPlan, error) {
	const warmCheck = dependencyWarmCheckName
	if strings.TrimSpace(plan.Name) != warmCheck {
		return nil, nil
	}
	warmResolve, err := warmDependencyResolveOptions(resolveOptions, cfg)
	if err != nil {
		return nil, err
	}
	warmResolve.Open = false
	warmPlan, err := resolveCheckRunPlan(warmCheck, cfg, warmResolve)
	if err != nil {
		return nil, err
	}
	warmPlan.Options.CommandAlias = defaultString(warmPlan.Options.CommandAlias, warmCheck)
	resolved, err := verificationplan.DependencyWarmPlansForTargets(
		checkPlansForPlanning([]checkRunPlan{warmPlan})[0],
		checkPlansForPlanning(targets),
	)
	if err != nil {
		return nil, cliConfigError(err.Error(), err)
	}
	if len(resolved) == 0 {
		err := fmt.Errorf("explicit dependency warm check %q must use a persistent cache lane", warmCheck)
		return nil, cliConfigError(err.Error(), err)
	}
	plans := specializedDependencyWarmRunPlans(warmPlan, resolved)
	for index := range plans {
		plans[index].Options.SecretsRequested = boolPtr(false)
	}
	return plans, nil
}

func explicitDependencyWarmRequested(checkName, intent string) bool {
	return strings.TrimSpace(checkName) == dependencyWarmCheckName && intent != agentIntentSecurity
}

func validateExplicitDependencyWarmSecretRequest(checkName, intent string, requestSecrets bool) error {
	if requestSecrets && explicitDependencyWarmRequested(checkName, intent) {
		return fmt.Errorf("--request-secrets cannot be combined with the no-secrets dependency warm check %q", dependencyWarmCheckName)
	}
	return nil
}

func specializedDependencyWarmRunPlans(template checkRunPlan, resolvedPlans []verificationplan.CheckPlan) []checkRunPlan {
	plans := make([]checkRunPlan, 0, len(resolvedPlans))
	for _, resolved := range resolvedPlans {
		plan := template
		plan.Options.CacheMode = resolved.Run.CacheMode
		plan.Options.CacheLaneSizeBytes = resolved.Run.CacheLaneSizeBytes
		plan.Options.CacheKeyFiles = append([]string(nil), resolved.Run.CacheKeyFiles...)
		plan.Options.CachePaths = append([]string(nil), resolved.Run.CachePaths...)
		plan.Options.Region = resolved.Run.Region
		plan.Options.Image = resolved.Run.Image
		plan.Options.RunnerSize = resolved.Run.RunnerSize
		plan.Options.Docker = resolved.Run.Docker
		plan.Options.WorkerSelector = copyStringMap(resolved.Run.WorkerSelector)
		plan.Options.LogicalChildID = resolved.LogicalChildID
		plan.Options.LogicalChildName = resolved.LogicalChildName
		plan.RegionDefaulted = strings.TrimSpace(resolved.Run.Region) == ""
		plan.DiagnosticRegion = preflightapi.NormalizeRegion(resolved.Run.Region)
		plans = append(plans, plan)
	}
	return plans
}

func warmDependencyResolveOptions(resolveOptions checkResolveOptions, cfg config.ProjectConfig) (checkResolveOptions, error) {
	warmMode := strings.TrimSpace(cfg.Network.WarmMode)
	if warmMode == "" {
		return checkResolveOptions{}, fmt.Errorf("dependency warming requires an explicit network.warm_mode; use allowlist with network.warm_allowlist on public deployments, or default only on local/private deployments")
	}
	warmResolve := resolveOptions
	warmResolve.NetworkMode = warmMode
	warmResolve.NetworkAllowlist = strings.Join(cfg.Network.WarmAllowlist, ",")
	return warmResolve, nil
}
