package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

type checkListReport struct {
	ConfigPath string             `json:"config_path"`
	Checks     []checkListSummary `json:"checks"`
}

type checkListSummary struct {
	DependencyCheck         string            `json:"dependency_check,omitempty"`
	RuntimeRequirements     map[string]string `json:"runtime_requirements,omitempty"`
	EnvironmentRequirements []string          `json:"environment_requirements,omitempty"`
	Name                    string            `json:"name"`
	Command                 string            `json:"command"`
	Steps                   []checkStepReport `json:"steps,omitempty"`
	Changes                 string            `json:"changes"`
	Image                   string            `json:"image,omitempty"`
	Region                  string            `json:"region,omitempty"`
	Docker                  bool              `json:"docker"`
	ConcurrencyGroup        string            `json:"concurrency_group,omitempty"`
	ConcurrencyLimit        int               `json:"concurrency_limit,omitempty"`
	CacheMode               string            `json:"cache_mode,omitempty"`
	Timeout                 string            `json:"timeout,omitempty"`
	Matrix                  int               `json:"matrix_variants"`
	Services                []string          `json:"services,omitempty"`
	WorkerSelector          map[string]string `json:"worker_selector,omitempty"`
}

type checkStepReport struct {
	Name           string            `json:"name"`
	Command        string            `json:"command"`
	Always         bool              `json:"always,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
}

type checkDryRunReport struct {
	DependencyCheck         string                        `json:"dependency_check,omitempty"`
	RuntimeRequirements     map[string]string             `json:"runtime_requirements,omitempty"`
	EnvironmentRequirements []string                      `json:"environment_requirements,omitempty"`
	SkipWarm                bool                          `json:"skip_warm"`
	SecretsRequested        bool                          `json:"secrets_requested"`
	Check                   string                        `json:"check"`
	ConfigPath              string                        `json:"config_path"`
	ProjectID               string                        `json:"project_id"`
	Region                  string                        `json:"region"`
	Command                 string                        `json:"command"`
	Steps                   []checkStepReport             `json:"steps,omitempty"`
	PrepareCommands         []string                      `json:"prepare,omitempty"`
	PostCommands            []string                      `json:"post,omitempty"`
	AlwaysCommands          []string                      `json:"always,omitempty"`
	Image                   string                        `json:"image"`
	RunnerSize              string                        `json:"runner_size"`
	TimeoutSeconds          int                           `json:"timeout_seconds"`
	ConcurrencyGroup        string                        `json:"concurrency_group,omitempty"`
	ConcurrencyLimit        int                           `json:"concurrency_limit,omitempty"`
	CacheMode               string                        `json:"cache_mode"`
	CachePaths              []string                      `json:"cache_paths,omitempty"`
	CacheKeyFiles           []string                      `json:"cache_key_files,omitempty"`
	ArtifactPaths           []string                      `json:"artifact_paths,omitempty"`
	NetworkMode             string                        `json:"network_mode"`
	NetworkAllowlist        []string                      `json:"network_allowlist,omitempty"`
	Docker                  bool                          `json:"docker"`
	Services                []string                      `json:"services,omitempty"`
	ServiceDefinitions      []preflightapi.RunServiceSpec `json:"service_definitions,omitempty"`
	WorkerSelector          map[string]string             `json:"worker_selector,omitempty"`
	When                    *checkWhenReport              `json:"when,omitempty"`
	ChangeMode              string                        `json:"change_mode"`
	KeepFailed              bool                          `json:"keep_failed"`
	Retries                 int                           `json:"retries"`
	MatrixVariants          []string                      `json:"matrix_variants,omitempty"`
	MatrixVariantCount      int                           `json:"matrix_variant_count"`
	Warnings                []string                      `json:"warnings,omitempty"`
	Plan                    *verificationPlan             `json:"plan,omitempty"`
	Sync                    syncDryRunReport              `json:"sync"`
}

func buildCheckListReport(cfg config.ProjectConfig, configPath string) checkListReport {
	names := sortedCheckNames(cfg.Checks)
	checks := make([]checkListSummary, 0, len(names))
	for _, name := range names {
		check := cfg.Checks[name]
		concurrencyGroup, concurrencyLimit, _ := effectiveCheckConcurrency(name, cfg, check, checkResolveOptions{})
		matrixCount := 1
		if variants, err := expandMatrix(check.Matrix); err == nil {
			matrixCount = len(variants)
		} else if !check.Matrix.Empty() {
			matrixCount = 0
		}
		checks = append(checks, checkListSummary{
			DependencyCheck:         check.DependencyCheck,
			Name:                    name,
			Command:                 check.Command,
			Steps:                   checkStepReportsFromConfig(check.Steps),
			Changes:                 defaultString(check.Changes, "working"),
			Image:                   check.Image,
			Region:                  preflightapi.NormalizeRegion(defaultString(check.Region, cfg.Region)),
			Docker:                  effectiveCheckDocker(cfg, name, check, checkResolveOptions{}),
			ConcurrencyGroup:        concurrencyGroup,
			ConcurrencyLimit:        concurrencyLimit,
			CacheMode:               defaultString(check.CacheMode, check.Cache.Mode),
			Timeout:                 defaultString(check.Timeout, secondsLabel(check.TimeoutSeconds)),
			Matrix:                  matrixCount,
			Services:                append([]string(nil), check.Services...),
			RuntimeRequirements:     preflightapi.CloneRuntimeRequirements(check.RuntimeRequirements),
			EnvironmentRequirements: append(preflightapi.CloneEnvironmentRequirements(cfg.EnvironmentRequirements), check.EnvironmentRequirements...),
			WorkerSelector:          copyStringMap(check.WorkerSelector),
		})
	}
	return checkListReport{ConfigPath: configPath, Checks: checks}
}

func printCheckListReport(report checkListReport) {
	fmt.Printf("config: %s\n", report.ConfigPath)
	if len(report.Checks) == 0 {
		fmt.Println("checks: 0")
		return
	}
	fmt.Printf("checks: %d\n", len(report.Checks))
	for _, check := range report.Checks {
		parts := []string{
			check.Name,
			"changes=" + check.Changes,
			fmt.Sprintf("docker=%t", check.Docker),
		}
		if check.Image != "" {
			parts = append(parts, "image="+check.Image)
		}
		if check.Region != "" {
			parts = append(parts, "region="+check.Region)
		}
		if check.ConcurrencyGroup != "" {
			parts = append(parts, "concurrency_group="+check.ConcurrencyGroup, fmt.Sprintf("concurrency_limit=%d", check.ConcurrencyLimit))
		}
		if check.CacheMode != "" {
			parts = append(parts, "cache_mode="+check.CacheMode)
		}
		if check.Timeout != "" {
			parts = append(parts, "timeout="+check.Timeout)
		}
		if len(check.Services) > 0 {
			parts = append(parts, "services="+strings.Join(check.Services, ","))
		}
		if check.Matrix != 1 {
			parts = append(parts, fmt.Sprintf("matrix=%d", check.Matrix))
		}
		if check.DependencyCheck != "" {
			parts = append(parts, "dependency_check="+check.DependencyCheck)
		}
		if len(check.Steps) > 0 {
			parts = append(parts, fmt.Sprintf("steps=%d", len(check.Steps)))
		}
		parts = append(parts, "command="+strconv.Quote(oneLine(check.Command)))
		fmt.Println(strings.Join(parts, " "))
	}
}

func buildCheckDryRunReport(ctx context.Context, cwd string, cfg config.ProjectConfig, configPath string, plan checkRunPlan) (checkDryRunReport, error) {
	syncReport, err := buildSyncDryRunReport(ctx, cwd, cfg, configPath, syncDryRunOptions{
		CheckName:  plan.Name,
		ChangeMode: plan.Options.ChangeMode,
	})
	if err != nil {
		return checkDryRunReport{}, err
	}
	variantNames := make([]string, 0, len(plan.Variants))
	for _, variant := range plan.Variants {
		if variant.Name != "" {
			variantNames = append(variantNames, variant.Name)
		}
	}
	report := checkDryRunReport{
		DependencyCheck:         cfg.Checks[plan.Name].DependencyCheck,
		SkipWarm:                plan.SkipWarm,
		SecretsRequested:        plan.Options.SecretsRequested != nil && *plan.Options.SecretsRequested,
		Check:                   plan.Name,
		ConfigPath:              configPath,
		ProjectID:               defaultString(plan.Options.ProjectID, "proj_dev"),
		Region:                  checkPlanDiagnosticRegion(plan),
		Command:                 plan.Options.Command,
		Steps:                   checkStepReportsFromSpecs(plan.Options.Steps),
		PrepareCommands:         append([]string(nil), plan.Options.PrepareCommands...),
		PostCommands:            append([]string(nil), plan.Options.PostCommands...),
		AlwaysCommands:          append([]string(nil), plan.Options.AlwaysCommands...),
		Image:                   defaultString(plan.Options.Image, preflightapi.DefaultImage),
		RunnerSize:              defaultString(plan.Options.RunnerSize, "standard"),
		TimeoutSeconds:          defaultInt(plan.Options.TimeoutSeconds, defaultRunTimeoutSeconds),
		ConcurrencyGroup:        plan.Options.ConcurrencyGroup,
		ConcurrencyLimit:        plan.Options.ConcurrencyLimit,
		CacheMode:               defaultString(plan.Options.CacheMode, "cache-only"),
		CachePaths:              append([]string(nil), plan.Options.CachePaths...),
		CacheKeyFiles:           append([]string(nil), plan.Options.CacheKeyFiles...),
		ArtifactPaths:           append([]string(nil), plan.Options.ArtifactPaths...),
		NetworkMode:             plan.Options.NetworkMode,
		NetworkAllowlist:        append([]string(nil), plan.Options.NetworkAllowlist...),
		Docker:                  plan.Options.Docker,
		Services:                append([]string(nil), plan.Services...),
		ServiceDefinitions:      cloneRunServices(plan.ServiceSpecs),
		RuntimeRequirements:     preflightapi.CloneRuntimeRequirements(plan.Options.RuntimeRequirements),
		EnvironmentRequirements: preflightapi.CloneEnvironmentRequirements(plan.Options.EnvironmentRequirements),
		WorkerSelector:          copyStringMap(plan.Options.WorkerSelector),
		When:                    plan.When,
		ChangeMode:              plan.Options.ChangeMode,
		KeepFailed:              plan.Options.KeepFailed,
		Retries:                 plan.Retries,
		MatrixVariants:          variantNames,
		MatrixVariantCount:      len(plan.Variants),
		Sync:                    syncReport,
	}
	report.Warnings = append(report.Warnings, artifactProducerWarningsForPaths(report, checkSpecificArtifactPaths(report.ArtifactPaths, cfg.Artifacts.Paths))...)
	return report, nil
}

func printCheckDryRunReport(report checkDryRunReport) {
	fmt.Println("check: dry-run")
	fmt.Printf("name: %s\n", report.Check)
	fmt.Printf("config: %s\n", report.ConfigPath)
	fmt.Printf("project_id: %s\n", report.ProjectID)
	fmt.Printf("secrets_requested: %t\n", report.SecretsRequested)
	fmt.Printf("region: %s\n", report.Region)
	fmt.Printf("command: %s\n", report.Command)
	for _, step := range report.Steps {
		fmt.Printf("step: %s command=%s", step.Name, step.Command)
		if step.Always {
			fmt.Print(" always=true")
		}
		if step.TimeoutSeconds > 0 {
			fmt.Printf(" timeout_seconds=%d", step.TimeoutSeconds)
		}
		fmt.Println()
	}
	for _, command := range report.PrepareCommands {
		fmt.Printf("prepare: %s\n", command)
	}
	for _, command := range report.PostCommands {
		fmt.Printf("post: %s\n", command)
	}
	for _, command := range report.AlwaysCommands {
		fmt.Printf("always: %s\n", command)
	}
	fmt.Printf("image: %s\n", report.Image)
	fmt.Printf("runner_size: %s\n", report.RunnerSize)
	fmt.Printf("timeout_seconds: %d\n", report.TimeoutSeconds)
	if report.ConcurrencyGroup != "" {
		fmt.Printf("concurrency_group: %s\n", report.ConcurrencyGroup)
		fmt.Printf("concurrency_limit: %d\n", report.ConcurrencyLimit)
	}
	fmt.Printf("cache_mode: %s\n", report.CacheMode)
	fmt.Printf("network_mode: %s\n", report.NetworkMode)
	if len(report.NetworkAllowlist) > 0 {
		fmt.Printf("network_allowlist: %s\n", strings.Join(report.NetworkAllowlist, ","))
	}
	fmt.Printf("docker: %t\n", report.Docker)
	if len(report.Services) > 0 {
		fmt.Printf("services: %s\n", strings.Join(report.Services, ","))
	}
	for _, service := range report.ServiceDefinitions {
		fmt.Printf("service: %s type=%s version=%s\n", service.Name, service.Type, service.Version)
	}
	printCheckWhenReport("when", report.When)
	fmt.Printf("change_mode: %s\n", report.ChangeMode)
	fmt.Printf("keep_failed: %t\n", report.KeepFailed)
	fmt.Printf("retries: %d\n", report.Retries)
	fmt.Printf("matrix_variants: %d\n", report.MatrixVariantCount)
	for _, variant := range report.MatrixVariants {
		fmt.Printf("matrix_variant: %s\n", variant)
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
	printDryRunWarnings(report.Warnings)
	if report.Plan != nil {
		fmt.Println()
		printVerificationPlan(*report.Plan)
	}
	fmt.Println()
	printSyncDryRunReport(report.Sync)
}

func printCheckDryRunSummary(report checkDryRunReport) {
	fmt.Printf("check: %s\n", report.Check)
	fmt.Printf("command: %s\n", compactCommand(report.Command, 140))
	if len(report.Steps) > 0 {
		fmt.Printf("steps: %d\n", len(report.Steps))
	}
	fmt.Printf("image: %s runner_size: %s timeout_seconds: %d\n", report.Image, report.RunnerSize, report.TimeoutSeconds)
	fmt.Printf("docker: %t network_mode: %s\n", report.Docker, report.NetworkMode)
	if len(report.Services) > 0 {
		fmt.Printf("services: %s\n", strings.Join(report.Services, ","))
	}
	fmt.Printf("artifacts: %d\n", len(report.ArtifactPaths))
	printPathSample("artifact", report.ArtifactPaths, 8)
	fmt.Printf("changed_files: %d untracked_files: %d\n", report.Sync.ChangedFilesCount, report.Sync.UntrackedCount)
	printDryRunWarnings(append(append([]string(nil), report.Warnings...), report.Sync.Warnings...))
}

func artifactProducerWarnings(report checkDryRunReport) []string {
	return artifactProducerWarningsForPaths(report, report.ArtifactPaths)
}

func artifactProducerWarningsForPaths(report checkDryRunReport, artifactPaths []string) []string {
	if len(artifactPaths) == 0 {
		return nil
	}
	var commands []string
	commands = append(commands, report.Command)
	commands = append(commands, report.PrepareCommands...)
	commands = append(commands, report.PostCommands...)
	commands = append(commands, report.AlwaysCommands...)
	for _, step := range report.Steps {
		commands = append(commands, step.Command)
	}
	corpus := strings.ToLower(strings.Join(commands, "\n"))
	var warnings []string
	for _, artifactPath := range artifactPaths {
		token := artifactProducerToken(artifactPath)
		if token == "" {
			continue
		}
		if !strings.Contains(corpus, strings.ToLower(token)) && !commonArtifactProducerMatches(artifactPath, corpus) {
			warnings = append(warnings, fmt.Sprintf("artifact path %q is not mentioned by command, steps, or lifecycle commands", artifactPath))
		}
	}
	return warnings
}

func checkSpecificArtifactPaths(allPaths []string, globalPaths []string) []string {
	global := make(map[string]bool, len(globalPaths))
	for _, path := range globalPaths {
		global[path] = true
	}
	var out []string
	for _, path := range allPaths {
		if global[path] {
			continue
		}
		out = append(out, path)
	}
	return out
}

func commonArtifactProducerMatches(pattern string, commandCorpus string) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	switch {
	case strings.Contains(pattern, "playwright-report") || strings.Contains(pattern, "test-results"):
		return strings.Contains(commandCorpus, "playwright")
	case strings.HasPrefix(pattern, "dist/") || strings.HasPrefix(pattern, "build/") || strings.Contains(pattern, "/dist/") || strings.Contains(pattern, "/build/"):
		return strings.Contains(commandCorpus, " build") || strings.Contains(commandCorpus, ":build") || strings.Contains(commandCorpus, "vite build")
	case strings.Contains(pattern, "coverage"):
		return strings.Contains(commandCorpus, "coverage") || strings.Contains(commandCorpus, "test")
	default:
		return false
	}
}

func artifactProducerToken(pattern string) string {
	pattern = strings.TrimSpace(strings.TrimPrefix(pattern, "./"))
	if pattern == "" {
		return ""
	}
	cut := len(pattern)
	for _, marker := range []string{"*", "?", "["} {
		if index := strings.Index(pattern, marker); index >= 0 && index < cut {
			cut = index
		}
	}
	token := strings.TrimRight(pattern[:cut], "/")
	if token == "" {
		return ""
	}
	if slash := strings.LastIndex(token, "/"); slash > 0 && slash == len(token)-1 {
		token = strings.TrimRight(token[:slash], "/")
	}
	return token
}

func compactCommand(command string, max int) string {
	command = oneLine(command)
	runes := []rune(command)
	if max <= 0 || len(runes) <= max {
		return command
	}
	if max <= 3 {
		return string(runes[:max])
	}
	return string(runes[:max-3]) + "..."
}

func printDryRunWarnings(warnings []string) {
	if len(warnings) == 0 {
		return
	}
	fmt.Println("warnings:")
	for _, warning := range uniqueStrings(warnings) {
		fmt.Printf("  - %s\n", warning)
	}
}

func checkStepReportsFromConfig(steps []config.StepConfig) []checkStepReport {
	if len(steps) == 0 {
		return nil
	}
	reports := make([]checkStepReport, 0, len(steps))
	for _, step := range steps {
		timeoutSeconds, _ := parseTimeoutSeconds(0, step.Timeout)
		reports = append(reports, checkStepReport{
			Name:           step.Name,
			Command:        step.Command,
			Always:         step.Always,
			Env:            copyStringMap(step.Env),
			TimeoutSeconds: timeoutSeconds,
		})
	}
	return reports
}

func checkStepReportsFromSpecs(steps []preflightapi.RunStepSpec) []checkStepReport {
	if len(steps) == 0 {
		return nil
	}
	reports := make([]checkStepReport, 0, len(steps))
	for _, step := range steps {
		reports = append(reports, checkStepReport{
			Name:           step.Name,
			Command:        step.Command,
			Always:         step.Always,
			Env:            copyStringMap(step.Env),
			TimeoutSeconds: step.TimeoutSeconds,
		})
	}
	return reports
}

func secondsLabel(value int) string {
	if value <= 0 {
		return ""
	}
	return fmt.Sprintf("%ds", value)
}
