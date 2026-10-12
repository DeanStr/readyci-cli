package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/DeanStr/readyci-cli/internal/syncbundlebuild"
	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func shouldUseAffectedRules(cfg config.ProjectConfig, explicitCheck string) bool {
	return strings.TrimSpace(explicitCheck) == "" && (len(cfg.Affected.Rules) > 0 || len(cfg.Affected.Profiles) > 0 || hasCheckWhenChanges(cfg))
}

func buildAffectedRulePlans(ctx context.Context, cwd string, cfg config.ProjectConfig, configPath string, resolveOptions checkResolveOptions) ([]affectedCheckPlan, affectedDryRunReport, error) {
	cfg = projectConfigWithCheckWhenRules(cfg)
	changeMode := strings.TrimSpace(resolveOptions.Changes)
	if changeMode == "" {
		changeMode = syncbundle.ChangeModeWorking
	}
	resolveOptions.Changes = changeMode
	changed, err := collectAffectedChangedFiles(ctx, cwd, cfg, changeMode)
	if err != nil {
		return nil, affectedDryRunReport{}, err
	}
	selectedNames, reasons, skippedChecks, err := selectChecksForAffectedRulesWithSkipped(cfg, changed.ByCheck)
	if err != nil {
		return nil, affectedDryRunReport{}, err
	}
	plans := make([]affectedCheckPlan, 0, len(selectedNames))
	reportChecks := make([]affectedDryRunCheck, 0, len(selectedNames))
	for _, name := range selectedNames {
		plan, err := resolveCheckRunPlan(name, cfg, resolveOptions)
		if err != nil {
			return nil, affectedDryRunReport{}, err
		}
		plan.Options.IncludeUntracked = affectedCheckIncludeUntracked(cfg.Checks[name], changeMode)
		selected := affectedCheckPlan{
			Plan:    plan,
			Reasons: append([]affectedRuleMatch(nil), reasons[name]...),
		}
		checkReport := checkDryRunReport{
			Command:         plan.Options.Command,
			Steps:           checkStepReportsFromSpecs(plan.Options.Steps),
			PrepareCommands: plan.Options.PrepareCommands,
			PostCommands:    plan.Options.PostCommands,
			AlwaysCommands:  plan.Options.AlwaysCommands,
			ArtifactPaths:   plan.Options.ArtifactPaths,
		}
		checkWarnings := artifactProducerWarningsForPaths(checkReport, checkSpecificArtifactPaths(plan.Options.ArtifactPaths, cfg.Artifacts.Paths))
		plans = append(plans, selected)
		reportChecks = append(reportChecks, affectedDryRunCheck{
			SecretsRequested:   plan.Options.SecretsRequested != nil && *plan.Options.SecretsRequested,
			Name:               plan.Name,
			Command:            plan.Options.Command,
			Image:              defaultString(plan.Options.Image, preflightapi.DefaultImage),
			Region:             checkPlanDiagnosticRegion(plan),
			RunnerSize:         defaultString(plan.Options.RunnerSize, "standard"),
			TimeoutSeconds:     defaultInt(plan.Options.TimeoutSeconds, defaultRunTimeoutSeconds),
			CacheMode:          defaultString(plan.Options.CacheMode, "cache-only"),
			CacheLaneSizeBytes: plan.Options.CacheLaneSizeBytes,
			NetworkMode:        plan.Options.NetworkMode,
			Docker:             plan.Options.Docker,
			Services:           append([]string(nil), plan.Services...),
			ArtifactPaths:      append([]string(nil), plan.Options.ArtifactPaths...),
			ChangeMode:         plan.Options.ChangeMode,
			MatrixVariantCount: len(plan.Variants),
			Warnings:           checkWarnings,
			Reasons:            selected.Reasons,
		})
	}
	projectID := strings.TrimSpace(resolveOptions.ProjectID)
	if projectID == "" {
		projectID = cfg.ProjectID
	}
	if err := validateControlFreeValue("project_id", projectID); err != nil {
		return nil, affectedDryRunReport{}, err
	}
	report := affectedDryRunReport{
		SkipWarm:       resolveOptions.SkipWarm,
		ConfigPath:     configPath,
		ProjectID:      defaultString(projectID, "proj_dev"),
		ChangeMode:     changed.ChangeMode,
		ChangedFiles:   append([]string(nil), changed.All...),
		SelectedChecks: reportChecks,
		SkippedChecks:  skippedChecks,
		Sync: syncDryRunReport{
			Root:              changed.Root,
			ConfigPath:        configPath,
			ChangeMode:        changed.ChangeMode,
			IncludeUntracked:  changed.IncludeUntracked,
			ExcludePatterns:   append([]string(nil), changed.ExcludePatterns...),
			ChangedFilesCount: len(changed.All),
			UntrackedCount:    len(changed.Untracked),
			ChangedFiles:      append([]string(nil), changed.All...),
			UntrackedFiles:    append([]string(nil), changed.Untracked...),
		},
	}
	return plans, report, nil
}

func collectAffectedChangedFiles(ctx context.Context, cwd string, cfg config.ProjectConfig, changeMode string) (affectedChangedFiles, error) {
	type snapshot struct {
		root             string
		changeMode       string
		includeUntracked bool
		excludePatterns  []string
		changedFiles     []string
		untrackedFiles   []string
	}
	cache := make(map[string]snapshot)
	byCheck := make(map[string][]string)
	seenCheck := make(map[string]bool)
	unionSeen := make(map[string]bool)
	untrackedSeen := make(map[string]bool)
	excludeSeen := make(map[string]bool)
	var allChanged []string
	var allUntracked []string
	var allExcludePatterns []string
	var root string
	includeUntracked := false
	for _, checkName := range affectedRoutingCheckNames(cfg) {
		if seenCheck[checkName] {
			continue
		}
		seenCheck[checkName] = true
		check := cfg.Checks[checkName]
		syncConfig := effectiveAffectedCheckSyncConfig(cfg.Sync, check.Sync, changeMode)
		key := affectedSyncConfigKey(syncConfig)
		current, ok := cache[key]
		if !ok {
			changed, err := syncbundlebuild.ChangedFiles(ctx, cwd, syncbundlebuild.BuildOptions{
				ChangeMode:       changeMode,
				ExcludePatterns:  append([]string(nil), syncConfig.Exclude...),
				IncludeUntracked: syncConfig.IncludeUntracked,
			})
			if err != nil {
				return affectedChangedFiles{}, err
			}
			current = snapshot{
				root:             changed.Root,
				changeMode:       changed.ChangeMode,
				includeUntracked: changed.IncludeUntracked,
				excludePatterns:  append([]string(nil), changed.ExcludePatterns...),
				changedFiles:     append([]string(nil), changed.ChangedFiles...),
				untrackedFiles:   append([]string(nil), changed.UntrackedFiles...),
			}
			cache[key] = current
		}
		byCheck[checkName] = append([]string(nil), current.changedFiles...)
		if root == "" {
			root = current.root
		}
		if current.includeUntracked {
			includeUntracked = true
		}
		for _, pattern := range current.excludePatterns {
			if !excludeSeen[pattern] {
				excludeSeen[pattern] = true
				allExcludePatterns = append(allExcludePatterns, pattern)
			}
		}
		for _, changedFile := range current.changedFiles {
			if !unionSeen[changedFile] {
				unionSeen[changedFile] = true
				allChanged = append(allChanged, changedFile)
			}
		}
		for _, untrackedFile := range current.untrackedFiles {
			if !untrackedSeen[untrackedFile] {
				untrackedSeen[untrackedFile] = true
				allUntracked = append(allUntracked, untrackedFile)
			}
		}
	}
	sort.Strings(allChanged)
	sort.Strings(allUntracked)
	return affectedChangedFiles{
		Root:             root,
		ChangeMode:       changeMode,
		IncludeUntracked: includeUntracked,
		ExcludePatterns:  allExcludePatterns,
		All:              allChanged,
		Untracked:        allUntracked,
		ByCheck:          byCheck,
	}, nil
}

func affectedRoutingCheckNames(cfg config.ProjectConfig) []string {
	seen := make(map[string]bool)
	var names []string
	for _, rule := range cfg.Affected.Rules {
		for _, name := range rule.Checks {
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	for _, name := range cfg.Affected.UnmatchedChecks {
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
}

func effectiveAffectedCheckSyncConfig(projectSync, checkSync config.SyncConfig, changeMode string) config.SyncConfig {
	syncConfig := effectiveCheckSyncConfig(projectSync, checkSync)
	syncConfig.IncludeUntracked = affectedCheckIncludeUntracked(config.CheckConfig{Sync: checkSync}, changeMode)
	return syncConfig
}

func affectedCheckIncludeUntracked(check config.CheckConfig, changeMode string) *bool {
	if changeMode == syncbundle.ChangeModeStaged {
		return boolPtr(false)
	}
	if check.Sync.IncludeUntracked != nil {
		return check.Sync.IncludeUntracked
	}
	return boolPtr(true)
}

func affectedSyncConfigKey(syncConfig config.SyncConfig) string {
	includeUntracked := true
	if syncConfig.IncludeUntracked != nil {
		includeUntracked = *syncConfig.IncludeUntracked
	}
	return fmt.Sprintf("%t\x00%s", includeUntracked, strings.Join(syncConfig.Exclude, "\x00"))
}
