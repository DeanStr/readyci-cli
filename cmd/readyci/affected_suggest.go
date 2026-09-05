package main

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

func affectedSuggestCommand(args []string) error {
	fs := flag.NewFlagSet("affected suggest", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOutput := fs.Bool("json", false, "print machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci affected suggest [--json]", nil)
	}
	cfg, configPath, err := config.LoadProjectConfig(".")
	if err != nil {
		return err
	}
	suggestRoot := "."
	ctx, stop := signalContext()
	defer stop()
	if root, err := gitOutput(ctx, "-C", ".", "rev-parse", "--show-toplevel"); err == nil && strings.TrimSpace(root) != "" {
		suggestRoot = strings.TrimSpace(root)
	} else if strings.TrimSpace(configPath) != "" {
		suggestRoot = filepath.Dir(configPath)
	}
	report, err := buildAffectedSuggestReport(suggestRoot, cfg, configPath)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSONOutput(report)
	}
	printAffectedSuggestReport(report)
	return nil
}

func buildAffectedSuggestReport(cwd string, cfg config.ProjectConfig, configPath string) (affectedSuggestReport, error) {
	ctx, stop := signalContext()
	defer stop()
	profiles, err := suggestAffectedProfiles(ctx, cwd)
	if err != nil {
		return affectedSuggestReport{}, err
	}
	cargoRules, cargoWarnings, err := suggestCargoAffectedRules(ctx, cwd, cfg.Checks)
	if err != nil {
		return affectedSuggestReport{}, err
	}
	auditIgnore, err := suggestAffectedAuditIgnore(ctx, cwd)
	if err != nil {
		return affectedSuggestReport{}, err
	}
	subsumes := suggestAffectedSubsumes(cfg.Checks)
	suggested := cfg
	suggested.Affected = config.AffectedConfig{Profiles: profiles, Rules: cargoRules, Subsumes: commandListMapFromStrings(subsumes), AuditIgnore: auditIgnore}
	expanded, warnings, err := expandAffectedConfigForConfigPath(suggested, configPath)
	if err != nil {
		return affectedSuggestReport{}, err
	}
	report := affectedSuggestReport{
		ConfigPath:  configPath,
		Profiles:    profiles,
		Rules:       affectedSuggestRulesFromConfig(expanded.Affected.Rules),
		Subsumes:    subsumes,
		AuditIgnore: auditIgnore,
		Warnings:    append(append([]string(nil), cargoWarnings...), warnings...),
	}
	if len(report.Rules) == 0 {
		report.Warnings = append(report.Warnings, "no affected rules could be generated from existing check names")
		return report, nil
	}
	expanded.Affected.AuditIgnore = auditIgnore
	filterCache := newAffectedPathFilterCache(ctx, cwd, expanded)
	tracked, err := trackedAffectedAuditFiles(cwd)
	if err != nil {
		return affectedSuggestReport{}, err
	}
	seen := make(map[string]bool)
	for _, file := range tracked {
		if !sourceLikeAffectedPath(file) {
			continue
		}
		ignored, err := affectedAuditIgnoreMatches(auditIgnore, file)
		if err != nil {
			return affectedSuggestReport{}, err
		}
		if ignored {
			continue
		}
		checks, err := selectChecksForAffectedPath(filterCache, expanded, file)
		if err != nil {
			return affectedSuggestReport{}, err
		}
		if len(checks) == 0 {
			continue
		}
		key := strings.Join(checks, "\x00")
		if seen[key] {
			continue
		}
		seen[key] = true
		report.Tests = append(report.Tests, affectedSuggestTest{Path: file, Checks: append([]string(nil), checks...)})
		if len(report.Tests) >= 20 {
			break
		}
	}
	return report, nil
}

func suggestAffectedSubsumes(checks map[string]config.CheckConfig) map[string][]string {
	suggested := make(map[string][]string)
	for _, parent := range []string{"web", "frontend", "ci", "all"} {
		if _, ok := checks[parent]; !ok {
			continue
		}
		var children []string
		for _, child := range affectedAggregateSubsumedChecks[parent] {
			if _, ok := checks[child]; ok {
				children = append(children, child)
			}
		}
		if len(children) > 0 {
			suggested[parent] = children
		}
	}
	if len(suggested) == 0 {
		return nil
	}
	return suggested
}

func commandListMapFromStrings(values map[string][]string) map[string]config.CommandList {
	if len(values) == 0 {
		return nil
	}
	converted := make(map[string]config.CommandList, len(values))
	for key, list := range values {
		converted[key] = config.CommandList(append([]string(nil), list...))
	}
	return converted
}

func affectedSuggestRulesFromConfig(rules []config.AffectedRule) []affectedSuggestRule {
	suggested := make([]affectedSuggestRule, 0, len(rules))
	for _, rule := range rules {
		suggested = append(suggested, affectedSuggestRule{
			Paths:   append([]string(nil), rule.Paths...),
			Exclude: append([]string(nil), rule.Exclude...),
			Checks:  append([]string(nil), rule.Checks...),
		})
	}
	return suggested
}

func suggestCargoAffectedRules(ctx context.Context, cwd string, checks map[string]config.CheckConfig) ([]config.AffectedRule, []string, error) {
	if !pathExists(cwd, "Cargo.toml") {
		return nil, nil, nil
	}
	report, err := buildCargoAffectedReport(ctx, cwd, "rust-")
	if err != nil {
		return nil, []string{"cargo affected graph unavailable: " + err.Error()}, nil
	}
	var rules []config.AffectedRule
	for _, rule := range report.Rules {
		var existing config.CommandList
		for _, check := range rule.Checks {
			if _, ok := checks[check]; ok {
				existing = append(existing, check)
			}
		}
		if len(existing) == 0 {
			continue
		}
		rules = append(rules, config.AffectedRule{
			Paths:  append([]string(nil), rule.Paths...),
			Checks: existing,
		})
	}
	if len(rules) == 0 && len(report.Packages) > 0 {
		return nil, []string{"cargo affected graph found packages but no matching rust-* checks are configured; run readyci affected cargo for check suggestions"}, nil
	}
	return rules, report.Warnings, nil
}

func suggestAffectedProfiles(ctx context.Context, cwd string) ([]string, error) {
	var profiles []string
	add := func(name string) {
		if !containsString(profiles, name) {
			profiles = append(profiles, name)
		}
	}
	if anyPathExists(cwd, "docs", "README.md", "AGENTS.md") {
		add("docs")
	}
	if anyPathExists(cwd, ".readyci.yml", ".readyci.yaml", ".readyciignore") {
		add("readyci")
	}
	shellFiles, err := gitOutputBytes(ctx, "-C", cwd, "ls-files", "-z", "--", "*.sh", "scripts/git-hooks/*")
	if err == nil && len(splitNULStrings(shellFiles)) > 0 {
		add("shell")
	}
	return profiles, nil
}

func suggestAffectedAuditIgnore(ctx context.Context, cwd string) ([]string, error) {
	var patterns []string
	for _, candidate := range []string{"legacy", "vendor", "third_party", "datasets/work", "datasets/parity"} {
		if pathExists(cwd, candidate) {
			patterns = append(patterns, normalizeAffectedPath(candidate)+"/**")
		}
	}
	ignoredFiles, err := gitOutputBytes(ctx, "-C", cwd, "ls-files", "-z", "--", "legacy/**", "vendor/**", "third_party/**", "datasets/work/**", "datasets/parity/**")
	if err == nil && len(splitNULStrings(ignoredFiles)) > 0 && len(patterns) == 0 {
		patterns = append(patterns, "legacy/**", "vendor/**", "datasets/work/**", "datasets/parity/**")
	}
	return patterns, nil
}

func anyPathExists(cwd string, paths ...string) bool {
	for _, candidate := range paths {
		if pathExists(cwd, candidate) {
			return true
		}
	}
	return false
}

func pathExists(cwd string, rel string) bool {
	_, err := os.Stat(filepath.Join(cwd, filepath.FromSlash(rel)))
	return err == nil
}
