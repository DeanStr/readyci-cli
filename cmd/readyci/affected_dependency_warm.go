package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/verificationplan"
)

func dependencyWarmPreview(cwd string, plans []affectedCheckPlan, cfg config.ProjectConfig, resolve checkResolveOptions) ([]string, error) {
	if resolve.SkipWarm {
		return []string{"Automatic dependency preparation skipped (--skip-warm); tests retain their configured network policy"}, nil
	}
	warm, err := dependencyWarmPlansForAffected(cwd, plans, cfg, resolve)
	if err != nil {
		return nil, cliConfigError(err.Error(), err)
	}
	var notes []string
	for _, selected := range warm {
		p := selected.Plan
		notes = append(notes, fmt.Sprintf("Automatic dependency preparation: %s; command=%q; network_mode=%s; network_allowlist=%s", p.Name, p.Options.Command, p.Options.NetworkMode, strings.Join(p.Options.NetworkAllowlist, ",")))
	}
	return notes, nil
}

func dependencyWarmPlansForAffected(_ string, plans []affectedCheckPlan, cfg config.ProjectConfig, resolveOptions checkResolveOptions) ([]affectedCheckPlan, error) {
	if resolveOptions.SkipWarm {
		return nil, nil
	}
	targets := make([]checkRunPlan, 0, len(plans))
	for _, selected := range plans {
		targets = append(targets, selected.Plan)
	}
	resolved, err := verificationplan.DependencyWarmPlansForChecks(cfg, checkPlansForPlanning(targets), resolveOptionsForPlanning(resolveOptions))
	if err != nil {
		return nil, cliConfigError(err.Error(), err)
	}
	if len(resolved) == 0 {
		return nil, nil
	}
	warmResolve, err := warmDependencyResolveOptions(resolveOptions, cfg)
	if err != nil {
		return nil, cliConfigError(err.Error(), err)
	}
	warmResolve.Open = false
	var warmPlans []affectedCheckPlan
	for _, warm := range resolved {
		template, err := resolveCheckRunPlan(warm.Name, cfg, warmResolve)
		if err != nil {
			return nil, err
		}
		for _, plan := range specializedDependencyWarmRunPlans(template, []verificationplan.CheckPlan{warm}) {
			warmPlans = append(warmPlans, affectedCheckPlan{Plan: plan})
		}
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
			if dependency, err := config.DependencyCheckFor(cfg, item.Plan.Name); err != nil {
				return nil, cliConfigError(err.Error(), err)
			} else if dependency == dependencyPlan.Name {
				targets = append(targets, item.Plan)
			}
		}
		if len(targets) == 0 && len(cfg.Checks) == 1 {
			return []checkRunPlan{dependencyPlan}, nil
		}
		if len(targets) == 0 {
			err := fmt.Errorf("explicit dependency warm check %q has no affected consumer checks to warm", dependencyPlan.Name)
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
		dependency, err := config.DependencyCheckFor(cfg, name)
		if err != nil {
			return nil, cliConfigError(err.Error(), err)
		}
		if dependency != dependencyPlan.Name {
			continue
		}
		plan, err := resolveCheckRunPlan(name, cfg, resolveOptions)
		if err != nil {
			return nil, err
		}
		targets = append(targets, plan)
	}
	if len(targets) == 0 && len(cfg.Checks) == 1 {
		targets = append(targets, dependencyPlan)
	}
	if len(targets) == 0 {
		return nil, cliConfigError(fmt.Sprintf("explicit dependency warm check %q has no consumer checks to warm", dependencyPlan.Name), nil)
	}
	return targets, nil
}

func dependencyWarmPlansForExplicitCheck(plan checkRunPlan, targets []checkRunPlan, cfg config.ProjectConfig, resolveOptions checkResolveOptions) ([]checkRunPlan, error) {
	warmCheck := plan.Name
	if !config.IsDependencyCheck(cfg, warmCheck) {
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

func explicitDependencyWarmRequested(cfg config.ProjectConfig, checkName, intent string) bool {
	return config.IsDependencyCheck(cfg, strings.TrimSpace(checkName)) && intent != agentIntentSecurity
}

func validateExplicitDependencyWarmSecretRequest(cfg config.ProjectConfig, checkName, intent string, requestSecrets bool) error {
	if requestSecrets && explicitDependencyWarmRequested(cfg, checkName, intent) {
		return fmt.Errorf("--request-secrets cannot be combined with the no-secrets dependency warm check %q", checkName)
	}
	return nil
}

func specializedDependencyWarmRunPlans(template checkRunPlan, resolvedPlans []verificationplan.CheckPlan) []checkRunPlan {
	plans := make([]checkRunPlan, 0, len(resolvedPlans))
	for _, resolved := range resolvedPlans {
		plan := template
		plan.DependencyWarmTargets = append([]string(nil), resolved.DependencyWarmTargets...)
		plan.Options.CacheDependencyCheck = resolved.Run.CacheDependencyCheck
		plan.Options.RuntimeRequirements = preflightapi.CloneRuntimeRequirements(resolved.Run.RuntimeRequirements)
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
	warmResolve.RequestSecrets = false
	warmResolve.SkipWarm = false
	warmResolve.NetworkMode = warmMode
	warmResolve.NetworkAllowlist = strings.Join(cfg.Network.WarmAllowlist, ",")
	return warmResolve, nil
}
