package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

type configExplainReport struct {
	ConfigPath           string                       `json:"config_path"`
	ProjectID            string                       `json:"project_id"`
	Region               string                       `json:"region"`
	Image                string                       `json:"image"`
	RunnerSize           string                       `json:"runner_size"`
	TimeoutSeconds       int                          `json:"timeout_seconds,omitempty"`
	Timeout              string                       `json:"timeout,omitempty"`
	Retries              int                          `json:"retries"`
	ConcurrencyGroup     string                       `json:"concurrency_group,omitempty"`
	ConcurrencyLimit     int                          `json:"concurrency_limit,omitempty"`
	CacheMode            string                       `json:"cache_mode"`
	CacheLaneSizeBytes   int64                        `json:"cache_lane_size_bytes,omitempty"`
	CachePaths           []string                     `json:"cache_paths,omitempty"`
	CacheKeyFiles        []string                     `json:"cache_key_files,omitempty"`
	ArtifactPaths        []string                     `json:"artifact_paths,omitempty"`
	NetworkMode          string                       `json:"network_mode"`
	NetworkAllowlist     []string                     `json:"network_allowlist,omitempty"`
	WarmNetworkMode      string                       `json:"warm_network_mode,omitempty"`
	WarmNetworkAllowlist []string                     `json:"warm_network_allowlist,omitempty"`
	SyncExclude          []string                     `json:"sync_exclude,omitempty"`
	SyncReviewedFixtures []syncbundle.ReviewedFixture `json:"sync_reviewed_fixtures,omitempty"`
	IncludeUntracked     bool                         `json:"include_untracked"`
	AffectedProfiles     []string                     `json:"affected_profiles,omitempty"`
	AffectedRules        []configExplainAffectedRule  `json:"affected_rules,omitempty"`
	AffectedUnmatched    []string                     `json:"affected_unmatched_checks,omitempty"`
	AffectedTests        int                          `json:"affected_tests,omitempty"`
	AffectedAuditIgnore  []string                     `json:"affected_audit_ignore,omitempty"`
	AffectedSubsumes     map[string][]string          `json:"affected_subsumes,omitempty"`
	Services             []configExplainService       `json:"services,omitempty"`
	Checks               []configExplainCheckReport   `json:"checks"`
	FieldHelp            []config.ConfigField         `json:"field_help,omitempty"`
	Status               string                       `json:"status"`
	Error                string                       `json:"error,omitempty"`
	Warnings             []string                     `json:"warnings,omitempty"`
}

type configExplainAffectedRule struct {
	Paths   []string `json:"paths"`
	Exclude []string `json:"exclude,omitempty"`
	Checks  []string `json:"checks"`
}

type configExplainService struct {
	Name           string            `json:"name"`
	Type           string            `json:"type"`
	Version        string            `json:"version,omitempty"`
	Image          string            `json:"image,omitempty"`
	Ports          []string          `json:"ports,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	HealthCommand  string            `json:"health_command,omitempty"`
	HealthTimeout  string            `json:"health_timeout,omitempty"`
	HealthInterval string            `json:"health_interval,omitempty"`
	Logs           []string          `json:"logs,omitempty"`
}

type checkWhenReport struct {
	Event   []string                `json:"event,omitempty"`
	Branch  []string                `json:"branch,omitempty"`
	Tag     string                  `json:"tag,omitempty"`
	Env     map[string]string       `json:"env,omitempty"`
	Status  string                  `json:"status,omitempty"`
	Changes *checkWhenChangesReport `json:"changes,omitempty"`
}

type checkWhenChangesReport struct {
	Include []string `json:"include,omitempty"`
	Exclude []string `json:"exclude,omitempty"`
}

type configExplainCheckReport struct {
	RuntimeRequirements     map[string]string            `json:"runtime_requirements,omitempty"`
	EnvironmentRequirements []string                     `json:"environment_requirements,omitempty"`
	Name                    string                       `json:"name"`
	Command                 string                       `json:"command,omitempty"`
	PrepareCommands         []string                     `json:"prepare,omitempty"`
	PostCommands            []string                     `json:"post,omitempty"`
	AlwaysCommands          []string                     `json:"always,omitempty"`
	Image                   string                       `json:"image,omitempty"`
	Region                  string                       `json:"region,omitempty"`
	RunnerSize              string                       `json:"runner_size,omitempty"`
	TimeoutSeconds          int                          `json:"timeout_seconds,omitempty"`
	ConcurrencyGroup        string                       `json:"concurrency_group,omitempty"`
	ConcurrencyLimit        int                          `json:"concurrency_limit,omitempty"`
	CacheMode               string                       `json:"cache_mode,omitempty"`
	CacheLaneSizeBytes      int64                        `json:"cache_lane_size_bytes,omitempty"`
	CachePaths              []string                     `json:"cache_paths,omitempty"`
	CacheKeyFiles           []string                     `json:"cache_key_files,omitempty"`
	ArtifactPaths           []string                     `json:"artifact_paths,omitempty"`
	NetworkMode             string                       `json:"network_mode,omitempty"`
	NetworkAllowlist        []string                     `json:"network_allowlist,omitempty"`
	SyncExclude             []string                     `json:"sync_exclude,omitempty"`
	SyncReviewedFixtures    []syncbundle.ReviewedFixture `json:"sync_reviewed_fixtures,omitempty"`
	IncludeUntracked        bool                         `json:"include_untracked"`
	Docker                  bool                         `json:"docker"`
	Services                []string                     `json:"services,omitempty"`
	WorkerSelector          map[string]string            `json:"worker_selector,omitempty"`
	When                    *checkWhenReport             `json:"when,omitempty"`
	ChangeMode              string                       `json:"change_mode,omitempty"`
	KeepFailed              bool                         `json:"keep_failed"`
	Retries                 int                          `json:"retries"`
	MatrixVariantCount      int                          `json:"matrix_variant_count"`
	Error                   string                       `json:"error,omitempty"`
}

func buildConfigExplainReport(cfg config.ProjectConfig, configPath string) configExplainReport {
	profiles := append([]string(nil), cfg.Affected.Profiles...)
	expanded, warnings, expandErr := expandAffectedConfigForConfigPath(cfg, configPath)
	var profileExpandErr error
	if expandErr == nil {
		profileExpandErr = affectedProfileExpansionError(cfg, expanded, warnings)
	}
	if expandErr == nil && profileExpandErr == nil {
		cfg = expanded
	}
	includeUntracked := includeUntrackedValue(cfg.Sync.IncludeUntracked)
	timeoutSeconds, _ := parseTimeoutSeconds(cfg.TimeoutSeconds, defaultString(cfg.Resources.Default.Timeout, cfg.Timeout))
	timeoutSeconds = defaultInt(timeoutSeconds, defaultRunTimeoutSeconds)
	cacheMode := defaultString(projectCacheMode(cfg), "cache-only")
	cacheLaneSizeBytes, _ := parseByteSize(cfg.Cache.Size)
	cacheLaneSizeBytes = cacheLaneSizeForMode(cacheMode, cacheLaneSizeBytes)
	runnerSize := cfg.RunnerSize
	if cfg.Resources.Default.Size != "" {
		runnerSize = cfg.Resources.Default.Size
	}
	concurrencyGroup, concurrencyLimit, _ := effectiveProjectConcurrency(cfg)
	report := configExplainReport{
		ConfigPath:           configPath,
		ProjectID:            defaultString(cfg.ProjectID, "proj_dev"),
		Region:               preflightapi.NormalizeRegion(cfg.Region),
		Image:                defaultString(cfg.Image, preflightapi.DefaultImage),
		RunnerSize:           defaultString(runnerSize, "standard"),
		TimeoutSeconds:       timeoutSeconds,
		Timeout:              cfg.Timeout,
		Retries:              cfg.Retries,
		ConcurrencyGroup:     concurrencyGroup,
		ConcurrencyLimit:     concurrencyLimit,
		CacheMode:            cacheMode,
		CacheLaneSizeBytes:   cacheLaneSizeBytes,
		CachePaths:           append([]string(nil), cfg.Cache.Paths...),
		CacheKeyFiles:        append([]string(nil), cfg.Cache.KeyFiles...),
		ArtifactPaths:        append([]string(nil), cfg.Artifacts.Paths...),
		NetworkMode:          defaultString(cfg.Network.Mode, preflightapi.DefaultNetworkMode),
		NetworkAllowlist:     append([]string(nil), cfg.Network.Allowlist...),
		WarmNetworkMode:      cfg.Network.WarmMode,
		WarmNetworkAllowlist: append([]string(nil), cfg.Network.WarmAllowlist...),
		SyncExclude:          append([]string(nil), cfg.Sync.Exclude...), SyncReviewedFixtures: append([]syncbundle.ReviewedFixture(nil), cfg.Sync.ReviewedFixtures...),
		IncludeUntracked:    includeUntracked,
		AffectedProfiles:    profiles,
		AffectedUnmatched:   append([]string(nil), cfg.Affected.UnmatchedChecks...),
		AffectedTests:       len(cfg.Affected.Tests),
		AffectedAuditIgnore: append([]string(nil), cfg.Affected.AuditIgnore...),
		AffectedSubsumes:    copyAffectedSubsumes(cfg.Affected.Subsumes),
		Warnings:            append([]string(nil), warnings...),
		Status:              "ok",
	}
	for _, name := range sortedServiceNames(cfg.Services) {
		service := cfg.Services[name]
		report.Services = append(report.Services, configExplainService{
			Name:           name,
			Type:           resolvedServiceType(service),
			Version:        service.Version,
			Image:          service.Image,
			Ports:          append([]string(nil), service.Ports...),
			Env:            copyStringMap(service.Env),
			HealthCommand:  service.Health.Command,
			HealthTimeout:  service.Health.Timeout,
			HealthInterval: service.Health.Interval,
			Logs:           append([]string(nil), service.Logs...),
		})
	}
	for _, rule := range cfg.Affected.Rules {
		report.AffectedRules = append(report.AffectedRules, configExplainAffectedRule{
			Paths:   append([]string(nil), rule.Paths...),
			Exclude: append([]string(nil), rule.Exclude...),
			Checks:  append([]string(nil), rule.Checks...),
		})
	}
	if expandErr != nil {
		report.Status = "invalid"
		report.Error = expandErr.Error()
	} else if profileExpandErr != nil {
		report.Status = "invalid"
		report.Error = profileExpandErr.Error()
	} else if err := validateProjectConfigForConfigPath(cfg, configPath); err != nil {
		report.Status = "invalid"
		report.Error = err.Error()
	}
	for _, name := range sortedCheckNames(cfg.Checks) {
		plan, err := resolveCheckRunPlan(name, cfg, checkResolveOptions{RetryCount: -1})
		if err != nil {
			check := cfg.Checks[name]
			syncConfig := effectiveCheckSyncConfig(cfg.Sync, check.Sync)
			report.Checks = append(report.Checks, configExplainCheckReport{
				Name:            name,
				Command:         check.Command,
				PrepareCommands: append([]string(nil), check.Prepare...),
				PostCommands:    append([]string(nil), check.Post...),
				AlwaysCommands:  append([]string(nil), check.Always...),
				SyncExclude:     append([]string(nil), syncConfig.Exclude...), SyncReviewedFixtures: append([]syncbundle.ReviewedFixture(nil), syncConfig.ReviewedFixtures...),
				IncludeUntracked:        includeUntrackedValue(syncConfig.IncludeUntracked),
				Services:                append([]string(nil), check.Services...),
				RuntimeRequirements:     preflightapi.CloneRuntimeRequirements(check.RuntimeRequirements),
				EnvironmentRequirements: append(preflightapi.CloneEnvironmentRequirements(cfg.EnvironmentRequirements), check.EnvironmentRequirements...),
				WorkerSelector:          copyStringMap(check.WorkerSelector),
				When:                    checkWhenReportFromConfig(check.When),
				Error:                   err.Error(),
			})
			continue
		}
		report.Checks = append(report.Checks, configExplainCheckReport{
			Name:               name,
			Command:            plan.Options.Command,
			PrepareCommands:    append([]string(nil), plan.Options.PrepareCommands...),
			PostCommands:       append([]string(nil), plan.Options.PostCommands...),
			AlwaysCommands:     append([]string(nil), plan.Options.AlwaysCommands...),
			Image:              defaultString(plan.Options.Image, preflightapi.DefaultImage),
			Region:             checkPlanDiagnosticRegion(plan),
			RunnerSize:         defaultString(plan.Options.RunnerSize, "standard"),
			TimeoutSeconds:     defaultInt(plan.Options.TimeoutSeconds, defaultRunTimeoutSeconds),
			ConcurrencyGroup:   plan.Options.ConcurrencyGroup,
			ConcurrencyLimit:   plan.Options.ConcurrencyLimit,
			CacheMode:          defaultString(plan.Options.CacheMode, "cache-only"),
			CacheLaneSizeBytes: plan.Options.CacheLaneSizeBytes,
			CachePaths:         append([]string(nil), plan.Options.CachePaths...),
			CacheKeyFiles:      append([]string(nil), plan.Options.CacheKeyFiles...),
			ArtifactPaths:      append([]string(nil), plan.Options.ArtifactPaths...),
			NetworkMode:        plan.Options.NetworkMode,
			NetworkAllowlist:   append([]string(nil), plan.Options.NetworkAllowlist...),
			SyncExclude:        append([]string(nil), plan.Options.SyncExclude...), SyncReviewedFixtures: append([]syncbundle.ReviewedFixture(nil), plan.Options.SyncReviewedFixtures...),
			IncludeUntracked:        includeUntrackedValue(plan.Options.IncludeUntracked),
			Docker:                  plan.Options.Docker,
			Services:                append([]string(nil), plan.Services...),
			RuntimeRequirements:     preflightapi.CloneRuntimeRequirements(plan.Options.RuntimeRequirements),
			EnvironmentRequirements: preflightapi.CloneEnvironmentRequirements(plan.Options.EnvironmentRequirements),
			WorkerSelector:          copyStringMap(plan.Options.WorkerSelector),
			When:                    plan.When,
			ChangeMode:              plan.Options.ChangeMode,
			KeepFailed:              plan.Options.KeepFailed,
			Retries:                 plan.Retries,
			MatrixVariantCount:      len(plan.Variants),
		})
	}
	return report
}

func includeUntrackedValue(value *bool) bool {
	if value == nil {
		return true
	}
	return *value
}

func printConfigExplainReport(report configExplainReport) {
	fmt.Printf("config: %s\n", report.ConfigPath)
	fmt.Printf("status: %s\n", report.Status)
	if report.Error != "" {
		fmt.Printf("error: %s\n", report.Error)
	}
	fmt.Printf("project_id: %s\n", report.ProjectID)
	fmt.Printf("region: %s\n", report.Region)
	fmt.Printf("image: %s\n", report.Image)
	fmt.Printf("runner_size: %s\n", report.RunnerSize)
	if report.TimeoutSeconds > 0 {
		fmt.Printf("timeout_seconds: %d\n", report.TimeoutSeconds)
	}
	fmt.Printf("retries: %d\n", report.Retries)
	if report.ConcurrencyGroup != "" {
		fmt.Printf("concurrency_group: %s\n", report.ConcurrencyGroup)
		fmt.Printf("concurrency_limit: %d\n", report.ConcurrencyLimit)
	}
	fmt.Printf("cache_mode: %s\n", report.CacheMode)
	if report.CacheLaneSizeBytes > 0 {
		fmt.Printf("cache_lane_size_bytes: %d\n", report.CacheLaneSizeBytes)
	}
	for _, path := range report.CachePaths {
		fmt.Printf("cache_path: %s\n", path)
	}
	for _, path := range report.CacheKeyFiles {
		fmt.Printf("cache_key_file: %s\n", path)
	}
	for _, path := range report.ArtifactPaths {
		fmt.Printf("artifact_path: %s\n", path)
	}
	fmt.Printf("network_mode: %s\n", report.NetworkMode)
	if report.WarmNetworkMode != "" {
		fmt.Printf("warm_network_mode: %s\n", report.WarmNetworkMode)
	}
	if len(report.WarmNetworkAllowlist) > 0 {
		fmt.Printf("warm_network_allowlist: %s\n", strings.Join(report.WarmNetworkAllowlist, ","))
	}
	fmt.Printf("include_untracked: %t\n", report.IncludeUntracked)
	for _, pattern := range report.SyncExclude {
		fmt.Printf("sync_exclude: %s\n", pattern)
	}
	for _, fixture := range report.SyncReviewedFixtures {
		fmt.Printf("sync_reviewed_fixture: %s sha256=%s\n", fixture.Path, fixture.SHA256)
	}
	if len(report.AffectedProfiles) > 0 {
		fmt.Printf("affected_profiles: %s\n", strings.Join(report.AffectedProfiles, ", "))
	}
	if len(report.AffectedUnmatched) > 0 {
		fmt.Printf("affected_unmatched_checks: %s\n", strings.Join(report.AffectedUnmatched, ", "))
	}
	fmt.Printf("affected_rules: %d\n", len(report.AffectedRules))
	for index, rule := range report.AffectedRules {
		fmt.Printf("affected_rule: %d\n", index+1)
		for _, pattern := range rule.Paths {
			fmt.Printf("  path: %s\n", pattern)
		}
		for _, pattern := range rule.Exclude {
			fmt.Printf("  exclude: %s\n", pattern)
		}
		for _, check := range rule.Checks {
			fmt.Printf("  check: %s\n", check)
		}
	}
	if len(report.AffectedSubsumes) > 0 {
		keys := sortedMapKeys(report.AffectedSubsumes)
		for _, parent := range keys {
			fmt.Printf("affected_subsumes: %s -> %s\n", parent, strings.Join(report.AffectedSubsumes[parent], ", "))
		}
	}
	fmt.Printf("affected_tests: %d\n", report.AffectedTests)
	for _, pattern := range report.AffectedAuditIgnore {
		fmt.Printf("affected_audit_ignore: %s\n", pattern)
	}
	for _, warning := range report.Warnings {
		fmt.Printf("warning: %s\n", warning)
	}
	if len(report.Services) > 0 {
		fmt.Printf("services: %d\n", len(report.Services))
		for _, service := range report.Services {
			fmt.Printf("service: %s\n", service.Name)
			fmt.Printf("  type: %s\n", service.Type)
			if service.Version != "" {
				fmt.Printf("  version: %s\n", service.Version)
			}
			if service.Image != "" {
				fmt.Printf("  image: %s\n", service.Image)
			}
			for _, port := range service.Ports {
				fmt.Printf("  port: %s\n", port)
			}
			if service.HealthCommand != "" {
				fmt.Printf("  health_command: %s\n", service.HealthCommand)
			}
			if service.HealthTimeout != "" {
				fmt.Printf("  health_timeout: %s\n", service.HealthTimeout)
			}
			if service.HealthInterval != "" {
				fmt.Printf("  health_interval: %s\n", service.HealthInterval)
			}
			for _, key := range sortedStringMapKeys(service.Env) {
				fmt.Printf("  env: %s=%s\n", key, service.Env[key])
			}
			for _, logPath := range service.Logs {
				fmt.Printf("  log: %s\n", logPath)
			}
		}
	}
	fmt.Printf("checks: %d\n", len(report.Checks))
	for _, check := range report.Checks {
		fmt.Printf("check: %s\n", check.Name)
		if check.Error != "" {
			fmt.Printf("  error: %s\n", check.Error)
			continue
		}
		fmt.Printf("  command: %s\n", check.Command)
		for _, command := range check.PrepareCommands {
			fmt.Printf("  prepare: %s\n", command)
		}
		for _, command := range check.PostCommands {
			fmt.Printf("  post: %s\n", command)
		}
		for _, command := range check.AlwaysCommands {
			fmt.Printf("  always: %s\n", command)
		}
		fmt.Printf("  image: %s\n", check.Image)
		fmt.Printf("  region: %s\n", preflightapi.NormalizeRegion(check.Region))
		fmt.Printf("  runner_size: %s\n", check.RunnerSize)
		fmt.Printf("  timeout_seconds: %d\n", check.TimeoutSeconds)
		if check.ConcurrencyGroup != "" {
			fmt.Printf("  concurrency_group: %s\n", check.ConcurrencyGroup)
			fmt.Printf("  concurrency_limit: %d\n", check.ConcurrencyLimit)
		}
		fmt.Printf("  cache_mode: %s\n", check.CacheMode)
		if check.CacheLaneSizeBytes > 0 {
			fmt.Printf("  cache_lane_size_bytes: %d\n", check.CacheLaneSizeBytes)
		}
		for _, path := range check.CachePaths {
			fmt.Printf("  cache_path: %s\n", path)
		}
		for _, path := range check.CacheKeyFiles {
			fmt.Printf("  cache_key_file: %s\n", path)
		}
		for _, path := range check.ArtifactPaths {
			fmt.Printf("  artifact_path: %s\n", path)
		}
		fmt.Printf("  network_mode: %s\n", check.NetworkMode)
		if len(check.NetworkAllowlist) > 0 {
			fmt.Printf("  network_allowlist: %s\n", strings.Join(check.NetworkAllowlist, ","))
		}
		fmt.Printf("  include_untracked: %t\n", check.IncludeUntracked)
		for _, pattern := range check.SyncExclude {
			fmt.Printf("  sync_exclude: %s\n", pattern)
		}
		for _, fixture := range check.SyncReviewedFixtures {
			fmt.Printf("  sync_reviewed_fixture: %s sha256=%s\n", fixture.Path, fixture.SHA256)
		}
		fmt.Printf("  docker: %t\n", check.Docker)
		if len(check.Services) > 0 {
			fmt.Printf("  services: %s\n", strings.Join(check.Services, ","))
		}
		if len(check.WorkerSelector) > 0 {
			fmt.Printf("  worker_selector: %s\n", formatStringMap(check.WorkerSelector))
		}
		printCheckWhenReport("  when", check.When)
		fmt.Printf("  changes: %s\n", check.ChangeMode)
		fmt.Printf("  keep_failed: %t\n", check.KeepFailed)
		fmt.Printf("  retries: %d\n", check.Retries)
		fmt.Printf("  matrix_variants: %d\n", check.MatrixVariantCount)
	}
	printConfigFieldHelp(report.FieldHelp)
}

func printConfigFieldHelp(fields []config.ConfigField) {
	if len(fields) == 0 {
		return
	}
	fmt.Printf("fields: %d\n", len(fields))
	for _, field := range fields {
		fmt.Printf("field: %s\n", field.Path)
		fmt.Printf("  scope: %s\n", field.Scope)
		fmt.Printf("  description: %s\n", field.Description)
		if field.Default != "" {
			fmt.Printf("  default: %s\n", field.Default)
		}
	}
}

func checkWhenReportFromConfig(when config.WhenConfig) *checkWhenReport {
	if len(when.Event) == 0 && len(when.Branch) == 0 && when.Tag == "" && len(when.Env) == 0 && when.Status == "" && len(when.Changes.Include) == 0 && len(when.Changes.Exclude) == 0 {
		return nil
	}
	report := &checkWhenReport{
		Event:  append([]string(nil), when.Event...),
		Branch: append([]string(nil), when.Branch...),
		Tag:    when.Tag,
		Env:    copyStringMap(when.Env),
		Status: when.Status,
	}
	if len(when.Changes.Include) > 0 || len(when.Changes.Exclude) > 0 {
		report.Changes = &checkWhenChangesReport{
			Include: append([]string(nil), when.Changes.Include...),
			Exclude: append([]string(nil), when.Changes.Exclude...),
		}
	}
	return report
}

func printCheckWhenReport(prefix string, report *checkWhenReport) {
	if report == nil {
		return
	}
	for _, event := range report.Event {
		fmt.Printf("%s.event: %s\n", prefix, event)
	}
	for _, branch := range report.Branch {
		fmt.Printf("%s.branch: %s\n", prefix, branch)
	}
	if report.Tag != "" {
		fmt.Printf("%s.tag: %s\n", prefix, report.Tag)
	}
	for _, key := range sortedStringMapKeys(report.Env) {
		fmt.Printf("%s.env.%s: %s\n", prefix, key, report.Env[key])
	}
	if report.Status != "" {
		fmt.Printf("%s.status: %s\n", prefix, report.Status)
	}
	if report.Changes != nil {
		for _, pattern := range report.Changes.Include {
			fmt.Printf("%s.changes.include: %s\n", prefix, pattern)
		}
		for _, pattern := range report.Changes.Exclude {
			fmt.Printf("%s.changes.exclude: %s\n", prefix, pattern)
		}
	}
}

func copyAffectedSubsumes(subsumes map[string]config.CommandList) map[string][]string {
	if len(subsumes) == 0 {
		return nil
	}
	copied := make(map[string][]string, len(subsumes))
	for parent, children := range subsumes {
		copied[parent] = append([]string(nil), children...)
	}
	return copied
}

func sortedMapKeys(values map[string][]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func formatStringMap(values map[string]string) string {
	if len(values) == 0 {
		return ""
	}
	parts := make([]string, 0, len(values))
	for _, key := range sortedStringMapKeys(values) {
		parts = append(parts, key+"="+values[key])
	}
	return strings.Join(parts, ",")
}
