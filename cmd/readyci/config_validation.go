package main

import (
	"fmt"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/artifactbundle"
	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func validateProjectConfig(cfg config.ProjectConfig) error {
	return validateProjectConfigForConfigPath(cfg, "")
}

func validateProjectConfigForConfigPath(cfg config.ProjectConfig, configPath string) error {
	if err := validateProjectConfigSemantics(cfg, configPath); err != nil {
		return cliConfigError(err.Error(), err)
	}
	return nil
}

func validateProjectConfigSemantics(cfg config.ProjectConfig, configPath string) error {
	if err := validateControlFreeValue("project_id", cfg.ProjectID); err != nil {
		return err
	}
	if cfg.Retries < 0 {
		return cliConfigError("retries must be non-negative", nil)
	}
	if _, _, err := effectiveProjectConcurrency(cfg); err != nil {
		return err
	}
	if err := preflightapi.ValidateRegion(cfg.Region); err != nil {
		return err
	}
	if err := validateRunnerSize(cfg.RunnerSize); err != nil {
		return err
	}
	if err := validateRunnerSize(cfg.Resources.Default.Size); err != nil {
		return fmt.Errorf("resources.default.size: %w", err)
	}
	if _, err := parseTimeoutSeconds(0, cfg.Resources.Default.Timeout); err != nil {
		return fmt.Errorf("resources.default.timeout: %w", err)
	}
	if _, err := normalizeCacheMode(cfg.CacheMode); err != nil {
		return fmt.Errorf("cache_mode: %w", err)
	}
	if _, err := normalizeCacheMode(cfg.Cache.Mode); err != nil {
		return fmt.Errorf("cache.mode: %w", err)
	}
	if _, err := parseByteSize(cfg.Cache.Size); err != nil {
		return fmt.Errorf("cache.size: %w", err)
	}
	if cfg.Network.Mode != "" {
		if err := validateNetworkMode(cfg.Network.Mode); err != nil {
			return err
		}
	}
	if err := validateNetworkAllowlistUse(defaultString(cfg.Network.Mode, preflightapi.DefaultNetworkMode), cfg.Network.Allowlist); err != nil {
		return err
	}
	if cfg.Network.WarmMode == "" {
		if len(cfg.Network.WarmAllowlist) > 0 {
			return fmt.Errorf("network.warm_allowlist requires network.warm_mode=allowlist")
		}
	} else {
		if cfg.Network.WarmMode != "default" && cfg.Network.WarmMode != "allowlist" {
			return fmt.Errorf("network.warm_mode must be default or allowlist")
		}
		if err := validateNetworkAllowlistUse(cfg.Network.WarmMode, cfg.Network.WarmAllowlist); err != nil {
			return fmt.Errorf("network warm profile: %w", err)
		}
	}
	if err := validateServiceConfigs(cfg.Services); err != nil {
		return err
	}
	if _, err := normalizeCachePaths(cfg.Cache.Paths); err != nil {
		return err
	}
	if _, err := normalizeCacheKeyFiles(cfg.Cache.KeyFiles); err != nil {
		return err
	}
	if err := validateArtifactPathPatterns("artifacts.paths", cfg.Artifacts.Paths); err != nil {
		return err
	}
	if _, err := parseTimeoutSeconds(cfg.TimeoutSeconds, cfg.Timeout); err != nil {
		return err
	}
	for name, resource := range cfg.Resources.Checks {
		if err := validateRunnerSize(resource.Size); err != nil {
			return fmt.Errorf("resources.checks.%s.size: %w", name, err)
		}
		if _, err := parseTimeoutSeconds(0, resource.Timeout); err != nil {
			return fmt.Errorf("resources.checks.%s.timeout: %w", name, err)
		}
	}
	for name, check := range cfg.Checks {
		if strings.TrimSpace(name) == "" {
			return cliConfigError("check names cannot be empty", nil)
		}
		if strings.TrimSpace(check.Command) == "" && len(check.Steps) == 0 {
			return fmt.Errorf("check %q has no command or steps", name)
		}
		if strings.Contains(check.Command, "\x00") {
			return fmt.Errorf("check %q command contains a NUL byte", name)
		}
		if strings.TrimSpace(check.Command) != "" && len(check.Steps) > 0 {
			return fmt.Errorf("check %q cannot set both command and steps", name)
		}
		if err := validateCheckSteps(name, check.Steps); err != nil {
			return err
		}
		if err := validateCommandList("prepare", check.Prepare); err != nil {
			return fmt.Errorf("check %q: %w", name, err)
		}
		if err := validateCommandList("post", check.Post); err != nil {
			return fmt.Errorf("check %q: %w", name, err)
		}
		if err := validateCommandList("always", check.Always); err != nil {
			return fmt.Errorf("check %q: %w", name, err)
		}
		if check.Retries < 0 {
			return fmt.Errorf("check %q retries must be non-negative", name)
		}
		if _, _, err := effectiveCheckConcurrency(name, cfg, check, checkResolveOptions{}); err != nil {
			return fmt.Errorf("check %q: %w", name, err)
		}
		if err := validateRunnerSize(check.RunnerSize); err != nil {
			return fmt.Errorf("check %q runner_size: %w", name, err)
		}
		if err := preflightapi.ValidateRegion(check.Region); err != nil {
			return fmt.Errorf("check %q: %w", name, err)
		}
		if err := validateRunnerSize(check.Size); err != nil {
			return fmt.Errorf("check %q size: %w", name, err)
		}
		if _, err := syncbundle.NormalizeChangeMode(check.Changes); err != nil {
			return fmt.Errorf("check %q: %w", name, err)
		}
		if _, err := normalizeCacheMode(check.CacheMode); err != nil {
			return fmt.Errorf("check %q cache_mode: %w", name, err)
		}
		if _, err := normalizeCacheMode(check.Cache.Mode); err != nil {
			return fmt.Errorf("check %q cache.mode: %w", name, err)
		}
		if _, err := parseByteSize(check.Cache.Size); err != nil {
			return fmt.Errorf("check %q cache.size: %w", name, err)
		}
		if check.NetworkMode != "" {
			if err := validateNetworkMode(check.NetworkMode); err != nil {
				return fmt.Errorf("check %q: %w", name, err)
			}
		}
		effectiveNetworkMode := defaultString(check.NetworkMode, defaultString(cfg.Network.Mode, preflightapi.DefaultNetworkMode))
		allowlist := check.NetworkAllowlist
		if len(allowlist) == 0 && effectiveNetworkMode == "allowlist" {
			allowlist = cfg.Network.Allowlist
		}
		if err := validateNetworkAllowlistUse(effectiveNetworkMode, allowlist); err != nil {
			return fmt.Errorf("check %q: %w", name, err)
		}
		if check.Sync.RespectGitignore != nil && !*check.Sync.RespectGitignore {
			return fmt.Errorf("check %q: sync.respect_gitignore=false is not supported yet", name)
		}
		if _, err := normalizeCachePaths(append(append([]string(nil), cfg.Cache.Paths...), check.Cache.Paths...)); err != nil {
			return fmt.Errorf("check %q: %w", name, err)
		}
		if _, err := normalizeCacheKeyFiles(append(append([]string(nil), cfg.Cache.KeyFiles...), check.Cache.KeyFiles...)); err != nil {
			return fmt.Errorf("check %q: %w", name, err)
		}
		if err := validateArtifactPathPatterns(fmt.Sprintf("check %q artifact_paths", name), check.ArtifactPaths); err != nil {
			return err
		}
		if err := validateArtifactPathPatterns(fmt.Sprintf("check %q outputs.reviewable", name), check.Outputs.Reviewable); err != nil {
			return err
		}
		if _, err := parseTimeoutSeconds(defaultInt(check.TimeoutSeconds, cfg.TimeoutSeconds), defaultString(check.Timeout, cfg.Timeout)); err != nil {
			return fmt.Errorf("check %q: %w", name, err)
		}
		if _, err := expandMatrix(check.Matrix); err != nil {
			return fmt.Errorf("check %q: %w", name, err)
		}
		if err := validateWhenConfig(name, check.When); err != nil {
			return err
		}
		plan, err := resolveCheckRunPlan(name, cfg, checkResolveOptions{RetryCount: -1})
		if err != nil {
			return fmt.Errorf("check %q resolved plan: %w", name, err)
		}
		if err := validateCheckServiceRefs(name, check.Services, cfg.Services, plan.Options.Docker); err != nil {
			return err
		}
		if err := preflightapi.ValidateWorkerSelector(check.WorkerSelector); err != nil {
			return fmt.Errorf("check %q: %w", name, err)
		}
	}
	expanded, warnings, err := expandAffectedConfigForConfigPath(cfg, configPath)
	if err != nil {
		return err
	}
	if err := affectedProfileExpansionError(cfg, expanded, warnings); err != nil {
		return err
	}
	cfg = projectConfigWithCheckWhenRules(expanded)
	if err := validateAffectedRulesConfig(cfg); err != nil {
		return err
	}
	return nil
}

func validateArtifactPathPatterns(field string, patterns []string) error {
	if _, err := artifactbundle.NormalizePatterns(patterns); err != nil {
		return fmt.Errorf("%s: %w", field, err)
	}
	return nil
}

func validateWhenConfig(checkName string, when config.WhenConfig) error {
	for _, event := range when.Event {
		if err := validateWhenToken(event, "event"); err != nil {
			return fmt.Errorf("check %q when.event: %w", checkName, err)
		}
	}
	for _, branch := range when.Branch {
		if err := validateWhenGlob(branch, "branch"); err != nil {
			return fmt.Errorf("check %q when.branch: %w", checkName, err)
		}
	}
	if when.Tag != "" {
		if err := validateWhenGlob(when.Tag, "tag"); err != nil {
			return fmt.Errorf("check %q when.tag: %w", checkName, err)
		}
	}
	for key, value := range when.Env {
		if !validServiceEnvKey(key) {
			return fmt.Errorf("check %q when.env.%s has invalid env name", checkName, key)
		}
		if containsControlCharacter(value) {
			return fmt.Errorf("check %q when.env.%s must not contain control characters", checkName, key)
		}
		if secretBearingServiceEnv(key, value) {
			return fmt.Errorf("check %q when.env.%s must be a literal non-secret value", checkName, key)
		}
	}
	switch strings.TrimSpace(when.Status) {
	case "":
	case "success":
	default:
		return fmt.Errorf("check %q when.status must be success", checkName)
	}
	return validateWhenChanges(checkName, when.Changes)
}

func validateWhenToken(value, field string) error {
	if value != strings.TrimSpace(value) || value == "" {
		return fmt.Errorf("%s contains an empty or whitespace-padded value", field)
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.' {
			continue
		}
		return fmt.Errorf("%s %q must contain only letters, numbers, dots, underscores, or dashes", field, value)
	}
	return nil
}

func validateWhenGlob(value, field string) error {
	if value != strings.TrimSpace(value) || value == "" {
		return fmt.Errorf("%s contains an empty or whitespace-padded value", field)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%s %q must not contain control characters", field, value)
		}
	}
	return nil
}

func validateWhenChanges(checkName string, changes config.WhenChangesConfig) error {
	for _, pattern := range changes.Include {
		if pattern != strings.TrimSpace(pattern) {
			return fmt.Errorf("check %q when.changes.include contains a whitespace-padded path pattern %q", checkName, pattern)
		}
		if err := validateAffectedPattern(pattern); err != nil {
			return fmt.Errorf("check %q when.changes.include: %w", checkName, err)
		}
	}
	for _, pattern := range changes.Exclude {
		if pattern != strings.TrimSpace(pattern) {
			return fmt.Errorf("check %q when.changes.exclude contains a whitespace-padded path pattern %q", checkName, pattern)
		}
		if err := validateAffectedPattern(pattern); err != nil {
			return fmt.Errorf("check %q when.changes.exclude: %w", checkName, err)
		}
	}
	if len(changes.Exclude) > 0 && len(changes.Include) == 0 {
		return fmt.Errorf("check %q when.changes.exclude requires when.changes.include", checkName)
	}
	return nil
}

func validateCheckSteps(checkName string, steps []config.StepConfig) error {
	seen := make(map[string]struct{}, len(steps))
	for index, step := range steps {
		name := strings.TrimSpace(step.Name)
		if name == "" {
			return fmt.Errorf("check %q steps[%d].name is required", checkName, index)
		}
		if name != step.Name || !preflightapi.ValidRunStepName(name) {
			return fmt.Errorf("check %q steps[%d].name %q is invalid", checkName, index, step.Name)
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("check %q steps contains duplicate step %q", checkName, name)
		}
		seen[name] = struct{}{}
		if strings.TrimSpace(step.Command) == "" {
			return fmt.Errorf("check %q steps[%d].command is required", checkName, index)
		}
		if strings.Contains(step.Command, "\x00") {
			return fmt.Errorf("check %q steps[%d].command contains a NUL byte", checkName, index)
		}
		if _, err := parseTimeoutSeconds(0, step.Timeout); err != nil {
			return fmt.Errorf("check %q steps[%d].timeout: %w", checkName, index, err)
		}
		for key, value := range step.Env {
			if !validServiceEnvKey(key) {
				return fmt.Errorf("check %q steps[%d].env.%s has invalid env name", checkName, index, key)
			}
			if containsControlCharacter(value) {
				return fmt.Errorf("check %q steps[%d].env.%s must not contain control characters", checkName, index, key)
			}
			if secretBearingServiceEnv(key, value) {
				return fmt.Errorf("check %q steps[%d].env.%s must be a literal non-secret value", checkName, index, key)
			}
		}
	}
	return nil
}

func validateCommandList(label string, commands []string) error {
	for _, command := range commands {
		if strings.TrimSpace(command) == "" {
			return fmt.Errorf("%s contains an empty command", label)
		}
		if strings.ContainsAny(command, "\x00") {
			return fmt.Errorf("%s contains a NUL byte", label)
		}
	}
	return nil
}
