package main

import (
	"context"
	"fmt"
	"sort"

	"github.com/DeanStr/readyci-cli/internal/syncbundlebuild"
	"github.com/DeanStr/readyci-cli/pkg/config"
)

func resolveWarmCheckNames(ctx context.Context, cfg config.ProjectConfig, configPath string, base checkResolveOptions, checkName, profile string) ([]string, error) {
	names, err := warmCheckNames(cfg, checkName, profile)
	if err != nil || checkName != "" || profile != "dependencies" || !shouldUseAffectedRules(cfg, "") || len(cfg.Checks) == 1 {
		return names, err
	}
	// A dependency profile follows affected consumers. Unrelated installers
	// must not turn a valid component warm into an explicit-check error.
	selected, _, _, err := buildAgentAffectedRulePlans(ctx, ".", cfg, configPath, base)
	if err != nil {
		return nil, err
	}
	needed := map[string]bool{}
	for _, item := range selected {
		dependency, err := config.DependencyCheckFor(cfg, item.Plan.Name)
		if err != nil {
			return nil, err
		}
		needed[dependency] = true
	}
	var affectedNames []string
	for _, name := range names {
		if needed[name] {
			affectedNames = append(affectedNames, name)
		}
	}
	if len(affectedNames) == 0 {
		return nil, fmt.Errorf("warm profile %q has no affected consumer checks to warm", profile)
	}
	return affectedNames, nil
}

// Both warm commands and their reruns use the same dependency specialization
// as explicit verification. A dependencies check only prepares its consumers;
// rerunning the installer offline is not a consumer confirmation.
func prepareWarmChecks(ctx context.Context, names []string, cfg config.ProjectConfig, configPath string, base checkResolveOptions, skipConfirm bool) (preparedVerificationSession, error) {
	prepared := preparedVerificationSession{ProjectID: defaultString(base.ProjectID, cfg.ProjectID), CoverageMode: "check"}
	warmResolve, err := warmDependencyResolveOptions(base, cfg)
	if err != nil {
		return prepared, cliConfigError(err.Error(), err)
	}
	var targets []checkRunPlan
	// Profile names are alphabetic, but dependency preparation must precede
	// every ordinary warm consumer, including checks named api or browser.
	var uniqueNames []string
	seen := map[string]bool{}
	for _, name := range names {
		if !seen[name] {
			uniqueNames = append(uniqueNames, name)
			seen[name] = true
		}
	}
	names = uniqueNames
	// Warming a named consumer must include its selected installer before the
	// optional online warm command and offline confirmation.
	for _, name := range uniqueNames {
		dependency, err := config.DependencyCheckFor(cfg, name)
		if err != nil {
			return prepared, cliConfigError(err.Error(), err)
		}
		if dependency != "" && !seen[dependency] {
			seen[dependency] = true
			names = append(names, dependency)
		}
	}
	sort.SliceStable(names, func(i, j int) bool {
		return config.IsDependencyCheck(cfg, names[i]) && !config.IsDependencyCheck(cfg, names[j])
	})
	for _, name := range names {
		warm, err := resolveCheckRunPlan(name, cfg, warmResolve)
		if err != nil {
			return prepared, err
		}
		if config.IsDependencyCheck(cfg, name) {
			// A mixed profile explicitly requests these consumers, regardless
			// of affected-file policy. A dependency-only request instead derives
			// its consumers from that policy.
			var consumers []checkRunPlan
			for _, targetName := range names {
				dependency, err := config.DependencyCheckFor(cfg, targetName)
				if err != nil {
					return prepared, cliConfigError(err.Error(), err)
				}
				if dependency != name {
					continue
				}
				consumer, err := resolveCheckRunPlan(targetName, cfg, base)
				if err != nil {
					return prepared, err
				}
				consumers = append(consumers, consumer)
			}
			if len(consumers) == 0 {
				consumers, err = dependencyWarmTargetsForExplicitCheck(ctx, ".", cfg, configPath, base, warm)
				if err != nil {
					return prepared, err
				}
			}
			groups, err := dependencyWarmPlansForExplicitCheck(warm, consumers, cfg, base)
			if err != nil {
				return prepared, err
			}
			prepared.WarmPlans = append(prepared.WarmPlans, groups...)
			targets = append(targets, consumers...)
			continue
		}
		warm.Options.SecretsRequested = boolPtr(false)
		prepared.WarmPlans = append(prepared.WarmPlans, warm)
		if !skipConfirm {
			check, err := resolveCheckRunPlan(name, cfg, base)
			if err != nil {
				return prepared, err
			}
			check.Options.SecretsRequested = boolPtr(base.RequestSecrets)
			prepared.Plans = append(prepared.Plans, check)
		}
	}
	prepared.BuildOptions, err = explicitDependencyWarmSessionBuildOptions(prepared.WarmPlans, append(targets, prepared.Plans...), syncbundlebuild.BuildOptions{ChangeMode: base.Changes})
	return prepared, err
}
