package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

func affectedAuditCommand(args []string) error {
	fs := flag.NewFlagSet("affected audit", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOutput := fs.Bool("json", false, "print machine-readable JSON")
	strict := fs.Bool("strict", false, "fail when source-like tracked files are not covered by affected rules")
	examples := fs.Bool("examples", false, "include suggested affected.tests fixtures based on covered tracked files")
	duplicates := fs.Bool("duplicates", false, "fail when aggregate checks duplicate narrower selected checks")
	fix := fs.Bool("fix", false, "write missing affected.tests examples to the config")
	fixIgnoreUnmatched := fs.Bool("fix-ignore-unmatched", false, "with --fix, add exact unmatched files to affected.audit_ignore")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci affected audit [--json] [--strict] [--examples] [--duplicates] [--fix] [--fix-ignore-unmatched]", nil)
	}
	if *jsonOutput && *fix {
		return cliConfigError("--json cannot be combined with --fix", nil)
	}
	if *fixIgnoreUnmatched && !*fix {
		return cliConfigError("--fix-ignore-unmatched requires --fix", nil)
	}
	cfg, configPath, err := config.LoadProjectConfig(".")
	if err != nil {
		return err
	}
	if configPath == "" {
		return cliConfigError("readyci affected audit requires .readyci.yml or .readyci.yaml", nil)
	}
	report, err := buildAffectedAuditReport(".", cfg, configPath, *strict, *examples || *fix, *duplicates)
	if err != nil {
		return err
	}
	if *fix {
		fixReport, err := applyAffectedAuditFix(configPath, report, *fixIgnoreUnmatched)
		if err != nil {
			return err
		}
		printAffectedAuditFixReport(fixReport)
		if report.Status != "ok" {
			updatedCfg, _, err := config.LoadProjectConfig(".")
			if err != nil {
				return err
			}
			updatedReport, err := buildAffectedAuditReport(".", updatedCfg, configPath, *strict, false, *duplicates)
			if err != nil {
				return err
			}
			if updatedReport.Status != "ok" {
				return exitCodeError{code: 1}
			}
		}
		return nil
	}
	if *jsonOutput {
		if err := writeJSONOutput(report); err != nil {
			return err
		}
		if report.Status != "ok" {
			return exitCodeError{code: 1}
		}
		return nil
	}
	printAffectedAuditReport(report)
	if report.Status != "ok" {
		return exitCodeError{code: 1}
	}
	return nil
}

func affectedExplainCommand(args []string) error {
	fs := flag.NewFlagSet("affected explain", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOutput := fs.Bool("json", false, "print machine-readable JSON")
	whyNot := fs.Bool("why-not", false, "explain why uncovered paths select no checks")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() == 0 {
		return cliConfigError("usage: readyci affected explain [--json] [--why-not] <path> [path...]", nil)
	}
	cfg, configPath, err := config.LoadProjectConfig(".")
	if err != nil {
		return err
	}
	if configPath == "" {
		return cliConfigError("readyci affected explain requires .readyci.yml or .readyci.yaml", nil)
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
	ctx, stop := signalContext()
	defer stop()
	filterCache := newAffectedPathFilterCache(ctx, ".", cfg)
	report := affectedExplainReport{
		ConfigPath: configPath,
		Status:     "ok",
	}
	for _, warning := range warnings {
		fmt.Fprintf(os.Stderr, "readyci: warning: %s\n", warning)
	}
	for _, rawPath := range fs.Args() {
		repoPath, err := repoRelativeInputPath(ctx, ".", rawPath)
		if err != nil {
			return err
		}
		pathReport, err := explainAffectedPath(filterCache, cfg, repoPath, *whyNot)
		if err != nil {
			return err
		}
		report.Paths = append(report.Paths, pathReport)
	}
	if *jsonOutput {
		return writeJSONOutput(report)
	}
	printAffectedExplainReport(report)
	return nil
}

func repoRelativeInputPath(ctx context.Context, cwd string, rawPath string) (string, error) {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return "", errors.New("affected explain path cannot be empty")
	}
	root, err := gitOutput(ctx, "-C", cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("git rev-parse --show-toplevel: %w", err)
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	var absPath string
	if filepath.IsAbs(rawPath) {
		absPath = filepath.Clean(rawPath)
	} else {
		cwdAbs, err := filepath.Abs(cwd)
		if err != nil {
			return "", err
		}
		absPath = filepath.Join(cwdAbs, rawPath)
	}
	rel, err := filepath.Rel(rootAbs, absPath)
	if err != nil {
		return "", err
	}
	rel = filepath.Clean(rel)
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q is outside the git worktree", rawPath)
	}
	return normalizeAffectedPath(rel), nil
}

func buildAffectedAuditReport(cwd string, cfg config.ProjectConfig, configPath string, strict bool, includeExamples bool, includeDuplicates bool) (affectedAuditReport, error) {
	profiles := append([]string(nil), cfg.Affected.Profiles...)
	expanded, warnings, err := expandAffectedConfigForConfigPath(cfg, configPath)
	if err != nil {
		return affectedAuditReport{}, err
	}
	if err := affectedProfileExpansionError(cfg, expanded, warnings); err != nil {
		return affectedAuditReport{}, err
	}
	cfg = expanded
	if err := validateAffectedRulesConfig(cfg); err != nil {
		return affectedAuditReport{}, err
	}
	report := affectedAuditReport{
		ConfigPath:  configPath,
		Profiles:    profiles,
		Rules:       len(cfg.Affected.Rules),
		PathSources: affectedRulePathSources(cfg),
		Warnings:    append([]string(nil), warnings...),
		Status:      "ok",
	}
	lintWarnings := affectedRuleLintWarnings(cfg)
	if len(lintWarnings) > 0 {
		report.Warnings = append(report.Warnings, lintWarnings...)
		if strict {
			report.Status = "failed"
		}
	}
	ctx, stop := signalContext()
	defer stop()
	filterCache := newAffectedPathFilterCache(ctx, cwd, cfg)
	for _, test := range cfg.Affected.Tests {
		testReport, ok, err := runAffectedAuditTest(filterCache, cfg, test)
		if err != nil {
			return affectedAuditReport{}, err
		}
		if !ok {
			report.Status = "failed"
		}
		report.Tests = append(report.Tests, testReport)
	}
	tracked, err := trackedAffectedAuditFiles(cwd)
	if err != nil {
		return affectedAuditReport{}, err
	}
	exampleSeen := make(map[string]bool)
	for _, file := range tracked {
		if !sourceLikeAffectedPath(file) {
			continue
		}
		ignored, err := affectedAuditIgnoreMatches(cfg.Affected.AuditIgnore, file)
		if err != nil {
			return affectedAuditReport{}, err
		}
		if ignored {
			report.IgnoredFiles = append(report.IgnoredFiles, file)
			continue
		}
		checks, err := selectChecksForAffectedPath(filterCache, cfg, file)
		if err != nil {
			return affectedAuditReport{}, err
		}
		if len(checks) == 0 {
			report.UnmatchedFiles = append(report.UnmatchedFiles, file)
			continue
		}
		if includeDuplicates {
			duplicates := duplicateAffectedSelections(file, checks)
			report.DuplicateSelections = append(report.DuplicateSelections, duplicates...)
		}
		if includeExamples && !affectedTestsIncludeExactPath(cfg.Affected.Tests, file) {
			key := strings.Join(checks, "\x00")
			if !exampleSeen[key] && len(report.Examples) < 20 {
				exampleSeen[key] = true
				report.Examples = append(report.Examples, affectedAuditExample{
					Paths:  []string{file},
					Checks: append([]string(nil), checks...),
				})
			}
		}
	}
	if len(report.UnmatchedFiles) > 0 {
		report.Warnings = append(report.Warnings, fmt.Sprintf("%d source-like tracked file(s) are not covered by affected.rules", len(report.UnmatchedFiles)))
		if strict && report.Status == "ok" {
			report.Status = "failed"
		}
	}
	if len(report.DuplicateSelections) > 0 {
		report.Warnings = append(report.Warnings, fmt.Sprintf("%d path(s) select aggregate checks plus narrower checks", len(report.DuplicateSelections)))
		report.Status = "failed"
	}
	sort.Strings(report.UnmatchedFiles)
	sort.Strings(report.IgnoredFiles)
	report.SuggestedRules = suggestedAffectedRulesForUnmatchedFiles(cfg, report.UnmatchedFiles)
	sort.Slice(report.DuplicateSelections, func(i, j int) bool {
		if report.DuplicateSelections[i].Path != report.DuplicateSelections[j].Path {
			return report.DuplicateSelections[i].Path < report.DuplicateSelections[j].Path
		}
		return report.DuplicateSelections[i].Aggregate < report.DuplicateSelections[j].Aggregate
	})
	return report, nil
}

func affectedRulePathSources(cfg config.ProjectConfig) []affectedRulePathSource {
	var sources []affectedRulePathSource
	for ruleIndex, rule := range cfg.Affected.Rules {
		for _, pattern := range rule.Paths {
			source, ok := rule.PathSources[pattern]
			if !ok {
				continue
			}
			sources = append(sources, affectedRulePathSource{
				Rule:           ruleIndex + 1,
				Pattern:        pattern,
				SourceFile:     source.File,
				SourceLine:     source.Line,
				SourceTemplate: source.Template,
			})
		}
	}
	return sources
}

func suggestedAffectedRulesForUnmatchedFiles(cfg config.ProjectConfig, files []string) []affectedSuggestRule {
	if len(files) == 0 {
		return nil
	}
	const limit = 20
	capacity := len(files)
	if capacity > limit {
		capacity = limit
	}
	suggestions := make([]affectedSuggestRule, 0, capacity)
	seen := make(map[string]bool, len(files))
	for _, file := range files {
		checks := suggestedChecksForUnmatchedFile(cfg.Checks, file)
		if len(checks) == 0 {
			continue
		}
		pattern := suggestedPatternForUnmatchedFile(file)
		key := pattern + "\x00" + strings.Join(checks, "\x00")
		if seen[key] {
			continue
		}
		seen[key] = true
		suggestions = append(suggestions, affectedSuggestRule{
			Paths:  []string{pattern},
			Checks: checks,
		})
		if len(suggestions) >= limit {
			break
		}
	}
	return suggestions
}

func suggestedChecksForUnmatchedFile(checks map[string]config.CheckConfig, file string) []string {
	file = normalizeAffectedPath(file)
	groups := [][]string{}
	if strings.HasPrefix(file, ".github/workflows/") || strings.HasPrefix(file, "deploy/") || strings.HasPrefix(file, "ops/") || strings.HasPrefix(file, "infra/") {
		groups = append(groups, []string{"ci"})
	}
	fileExt := path.Ext(file)
	if strings.HasPrefix(file, "cli/") && fileExt == ".go" {
		groups = append(groups, []string{"cli", "cli-vet", "go-fmt"}, []string{"go"}, []string{"ci"})
	} else if strings.HasPrefix(file, "cli/") {
		groups = append(groups, []string{"cli"}, []string{"go"}, []string{"ci"})
	}
	if strings.HasPrefix(file, "platform/") && fileExt == ".go" {
		groups = append(groups, []string{"platform", "platform-vet", "go-fmt"}, []string{"go"}, []string{"ci"})
	} else if strings.HasPrefix(file, "platform/") {
		groups = append(groups, []string{"platform"}, []string{"go"}, []string{"ci"})
	}
	if strings.HasPrefix(file, "tools/") {
		groups = append(groups, []string{"tools"}, []string{"python"}, []string{"ci"})
	}
	if strings.HasPrefix(file, "scripts/") && !strings.HasSuffix(file, ".sh") {
		groups = append(groups, []string{"lint"}, []string{"ci"})
	}
	if strings.HasSuffix(file, ".sh") || strings.Contains(file, "/scripts/git-hooks/") || strings.HasPrefix(file, "scripts/git-hooks/") {
		groups = append(groups, []string{"shell-syntax", "shell"}, []string{"ci"})
	}
	if strings.Contains(file, "/src/") || strings.HasPrefix(file, "src/") || strings.HasPrefix(file, "apps/web/") || strings.HasPrefix(file, "packages/") {
		if strings.Contains(file, "/__tests__/") || strings.Contains(path.Base(file), ".test.") || strings.Contains(file, "/test/") || strings.Contains(file, "/tests/") {
			groups = append(groups, []string{"web-typecheck", "web-lint", "web-test"}, []string{"unit", "test"}, []string{"web"}, []string{"frontend"}, []string{"ci"})
		} else {
			groups = append(groups, []string{"web-typecheck", "web-lint", "web-test"}, []string{"lint", "typecheck", "unit"}, []string{"web"}, []string{"frontend"}, []string{"ci"})
		}
	}
	switch fileExt {
	case ".go":
		groups = append(groups, []string{"go"}, []string{"test"}, []string{"ci"})
	case ".rs":
		groups = append(groups, []string{"rust"}, []string{"test"}, []string{"ci"})
	case ".py":
		groups = append(groups, []string{"python"}, []string{"test"}, []string{"ci"})
	case ".sql":
		groups = append(groups, []string{"db"}, []string{"ci"})
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".mts", ".cts", ".json", ".css", ".scss":
		groups = append(groups, []string{"web-typecheck", "web-lint", "web-test"}, []string{"lint", "typecheck", "unit"}, []string{"web"}, []string{"node"}, []string{"test"}, []string{"ci"})
	case ".yaml", ".yml", ".toml":
		groups = append(groups, []string{"ci"})
	}
	groups = append(groups, []string{"test"}, []string{"ci"})
	for _, group := range groups {
		if selected := existingAffectedProfileCheckGroup(checks, group); len(selected) > 0 {
			return selected
		}
	}
	return nil
}

func suggestedPatternForUnmatchedFile(file string) string {
	file = normalizeAffectedPath(file)
	ext := path.Ext(file)
	if ext == "" {
		return file
	}
	for _, marker := range []string{"/testdata/", "/src/", "/test/", "/tests/", "/scripts/"} {
		if index := strings.Index(file, marker); index > 0 {
			return file[:index+len(marker)] + "**/*" + ext
		}
	}
	if strings.HasPrefix(file, "src/") {
		return "src/**/*" + ext
	}
	if strings.HasPrefix(file, "scripts/") {
		return "scripts/**/*" + ext
	}
	return file
}

var affectedAggregateSubsumedChecks = map[string][]string{
	"web":      {"web-contracts", "web-typecheck", "web-lint", "web-test", "web-build"},
	"frontend": {"web-contracts", "web-typecheck", "web-lint", "web-test", "web-build"},
	"ci":       {"test", "lint", "docs-lint", "spec-lint", "web", "frontend", "web-contracts", "web-typecheck", "web-lint", "web-test", "web-build", "rust", "go", "node", "python"},
	"all":      {"ci", "test", "lint", "docs-lint", "spec-lint", "web", "frontend", "web-contracts", "web-typecheck", "web-lint", "web-test", "web-build", "rust", "go", "node", "python"},
}

func duplicateAffectedSelections(file string, checks []string) []affectedDuplicateSelection {
	checkSet := make(map[string]bool, len(checks))
	for _, check := range checks {
		checkSet[check] = true
	}
	var duplicates []affectedDuplicateSelection
	for aggregate, subsumedCandidates := range affectedAggregateSubsumedChecks {
		if !checkSet[aggregate] {
			continue
		}
		var subsumed []string
		for _, candidate := range subsumedCandidates {
			if checkSet[candidate] {
				subsumed = append(subsumed, candidate)
			}
		}
		if len(subsumed) == 0 {
			continue
		}
		duplicates = append(duplicates, affectedDuplicateSelection{
			Path:           normalizeAffectedPath(file),
			Aggregate:      aggregate,
			SubsumedChecks: subsumed,
		})
	}
	return duplicates
}

func affectedAuditIgnoreMatches(patterns []string, file string) (bool, error) {
	for index, pattern := range patterns {
		match, err := affectedPatternMatches(pattern, file)
		if err != nil {
			return false, fmt.Errorf("affected.audit_ignore[%d]: %w", index, err)
		}
		if match {
			return true, nil
		}
	}
	return false, nil
}
