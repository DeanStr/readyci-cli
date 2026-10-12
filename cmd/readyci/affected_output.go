package main

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

func printAffectedSuggestReport(report affectedSuggestReport) {
	fmt.Println("affected: suggest")
	if report.ConfigPath != "" {
		fmt.Printf("config: %s\n", report.ConfigPath)
	}
	if len(report.Profiles) > 0 {
		fmt.Printf("profiles: %s\n", strings.Join(report.Profiles, ", "))
	}
	fmt.Printf("rules: %d\n", len(report.Rules))
	fmt.Printf("tests: %d\n", len(report.Tests))
	fmt.Printf("audit_ignore: %d\n", len(report.AuditIgnore))
	for _, warning := range report.Warnings {
		fmt.Printf("warning: %s\n", warning)
	}
	fmt.Println("snippet:")
	type affectedSuggestSnippet struct {
		AuditIgnore []string              `yaml:"audit_ignore,omitempty"`
		Subsumes    map[string][]string   `yaml:"subsumes,omitempty"`
		Rules       []affectedSuggestRule `yaml:"rules,omitempty"`
		Tests       []affectedSuggestTest `yaml:"tests,omitempty"`
	}
	snippet := struct {
		Affected affectedSuggestSnippet `yaml:"affected"`
	}{
		Affected: affectedSuggestSnippet{
			AuditIgnore: report.AuditIgnore,
			Subsumes:    report.Subsumes,
			Rules:       report.Rules,
			Tests:       report.Tests,
		},
	}
	data, err := yaml.Marshal(snippet)
	if err != nil {
		fmt.Printf("error: render snippet: %v\n", err)
		return
	}
	fmt.Print(string(data))
}

func printAffectedAuditReport(report affectedAuditReport) {
	fmt.Println("affected: audit")
	fmt.Printf("config: %s\n", report.ConfigPath)
	fmt.Printf("status: %s\n", report.Status)
	if len(report.Profiles) > 0 {
		fmt.Printf("profiles: %s\n", strings.Join(report.Profiles, ", "))
	}
	fmt.Printf("rules: %d\n", report.Rules)
	for _, source := range report.PathSources {
		fmt.Printf("path_source: rule=%d pattern=%s source=%s:%d", source.Rule, source.Pattern, source.SourceFile, source.SourceLine)
		if source.SourceTemplate != "" {
			fmt.Printf(" template=%s", source.SourceTemplate)
		}
		fmt.Println()
	}
	fmt.Printf("tests: %d\n", len(report.Tests))
	for index, test := range report.Tests {
		fmt.Printf("test: %d\n", index+1)
		for _, testPath := range test.Paths {
			fmt.Printf("  path: %s\n", testPath)
		}
		fmt.Printf("  expected: %s\n", strings.Join(test.ExpectedChecks, ", "))
		fmt.Printf("  actual: %s\n", strings.Join(test.ActualChecks, ", "))
		fmt.Printf("  status: %s\n", test.Status)
		if test.Error != "" {
			fmt.Printf("  error: %s\n", test.Error)
		}
	}
	fmt.Printf("unmatched_source_files: %d\n", len(report.UnmatchedFiles))
	printPathSample("unmatched_source_file", report.UnmatchedFiles, 20)
	if len(report.SuggestedRules) > 0 {
		fmt.Println("suggested_rules:")
		for _, rule := range report.SuggestedRules {
			fmt.Println("  - paths:")
			for _, pattern := range rule.Paths {
				fmt.Printf("      - %s\n", pattern)
			}
			fmt.Println("    checks:")
			for _, check := range rule.Checks {
				fmt.Printf("      - %s\n", check)
			}
		}
	}
	fmt.Printf("ignored_source_files: %d\n", len(report.IgnoredFiles))
	printPathSample("ignored_source_file", report.IgnoredFiles, 20)
	fmt.Printf("duplicate_selections: %d\n", len(report.DuplicateSelections))
	for index, duplicate := range report.DuplicateSelections {
		if index >= 20 {
			fmt.Printf("duplicate_selection: ... %d more\n", len(report.DuplicateSelections)-index)
			break
		}
		fmt.Printf("duplicate_selection: %s selects %s and %s\n", duplicate.Path, duplicate.Aggregate, strings.Join(duplicate.SubsumedChecks, ", "))
	}
	if len(report.Examples) > 0 {
		fmt.Println("examples:")
		for _, example := range report.Examples {
			fmt.Println("  - paths:")
			for _, examplePath := range example.Paths {
				fmt.Printf("      - %s\n", examplePath)
			}
			fmt.Println("    checks:")
			for _, check := range example.Checks {
				fmt.Printf("      - %s\n", check)
			}
		}
	}
	for _, warning := range report.Warnings {
		fmt.Printf("warning: %s\n", warning)
	}
}

func printAffectedExplainReport(report affectedExplainReport) {
	fmt.Println("affected: explain")
	fmt.Printf("config: %s\n", report.ConfigPath)
	fmt.Printf("status: %s\n", report.Status)
	for _, item := range report.Paths {
		fmt.Printf("path: %s\n", item.Path)
		if len(item.Checks) == 0 {
			fmt.Println("  checks: none")
			if item.WhyNot != "" {
				fmt.Printf("  why_not: %s\n", item.WhyNot)
			}
			if item.IgnoredBy != "" {
				fmt.Printf("  ignored_by: %s\n", item.IgnoredBy)
			}
			for _, hit := range item.FilterHits {
				fmt.Printf("  detail: %s\n", hit)
			}
			continue
		}
		fmt.Printf("  checks: %s\n", strings.Join(item.Checks, ", "))
		for _, reason := range item.Reasons {
			fmt.Printf("  reason: %s\n", affectedRuleMatchDescription(reason))
		}
		for _, skipped := range item.SkippedChecks {
			fmt.Printf("  skipped_check: %s\n", skipped.Name)
			fmt.Printf("    reason: %s\n", skipped.Reason)
			if len(skipped.SubsumedBy) > 0 {
				fmt.Printf("    subsumed_by: %s\n", strings.Join(skipped.SubsumedBy, ","))
			}
		}
	}
}

func printCargoAffectedReport(report cargoAffectedReport) {
	fmt.Println("affected: cargo")
	fmt.Printf("workspace_root: %s\n", report.WorkspaceRoot)
	fmt.Printf("packages: %d\n", len(report.Packages))
	for _, warning := range report.Warnings {
		fmt.Printf("warning: %s\n", warning)
	}
	fmt.Println("suggested_yaml:")
	fmt.Println("  checks:")
	for _, pkg := range report.Packages {
		fmt.Printf("    %s:\n", pkg.Check)
		fmt.Printf("      command: cargo test -p %s\n", shellQuoteArg(pkg.Name))
	}
	fmt.Println("  affected:")
	fmt.Println("    rules:")
	for _, rule := range report.Rules {
		fmt.Println("      - paths:")
		for _, rulePath := range rule.Paths {
			fmt.Printf("          - %s\n", rulePath)
		}
		fmt.Println("        checks:")
		for _, check := range rule.Checks {
			fmt.Printf("          - %s\n", check)
		}
	}
}

func printAffectedDryRunReport(report affectedDryRunReport) {
	fmt.Println("affected: dry-run")
	if report.ConfigPath != "" {
		fmt.Printf("config: %s\n", report.ConfigPath)
	}
	printDryRunWarnings(report.Warnings)
	fmt.Printf("project_id: %s\n", report.ProjectID)
	fmt.Printf("change_mode: %s\n", report.ChangeMode)
	fmt.Printf("changed_files: %d\n", len(report.ChangedFiles))
	printPathSample("changed_file", report.ChangedFiles, 20)
	fmt.Printf("checks: %d\n", len(report.SelectedChecks))
	for _, check := range report.SelectedChecks {
		fmt.Printf("check: %s\n", check.Name)
		fmt.Printf("  command: %s\n", check.Command)
		fmt.Printf("  image: %s\n", check.Image)
		fmt.Printf("  region: %s\n", check.Region)
		fmt.Printf("  runner_size: %s\n", check.RunnerSize)
		fmt.Printf("  timeout_seconds: %d\n", check.TimeoutSeconds)
		if check.CacheMode != "" {
			fmt.Printf("  cache_mode: %s\n", check.CacheMode)
		}
		fmt.Printf("  network_mode: %s\n", check.NetworkMode)
		fmt.Printf("  secrets_requested: %t\n", check.SecretsRequested)
		fmt.Printf("  docker: %t\n", check.Docker)
		if len(check.Services) > 0 {
			fmt.Printf("  services: %s\n", strings.Join(check.Services, ","))
		}
		for _, path := range check.ArtifactPaths {
			fmt.Printf("  artifact_path: %s\n", path)
		}
		fmt.Printf("  changes: %s\n", check.ChangeMode)
		fmt.Printf("  matrix_variants: %d\n", check.MatrixVariantCount)
		for _, warning := range check.Warnings {
			fmt.Printf("  warning: %s\n", warning)
		}
		for _, reason := range check.Reasons {
			fmt.Printf("  reason: %s\n", affectedRuleMatchDescription(reason))
		}
	}
	for _, check := range report.SkippedChecks {
		fmt.Printf("skipped_check: %s\n", check.Name)
		fmt.Printf("  status: %s\n", check.Status)
		fmt.Printf("  reason: %s\n", check.Reason)
		if len(check.SubsumedBy) > 0 {
			fmt.Printf("  subsumed_by: %s\n", strings.Join(check.SubsumedBy, ","))
		}
		for _, path := range check.MatchedPaths {
			fmt.Printf("  matched_path: %s\n", path)
		}
	}
	if report.Plan != nil {
		printVerificationPlan(*report.Plan)
	}
}

func printAffectedDryRunSummary(report affectedDryRunReport) {
	fmt.Printf("affected: %d check(s)\n", len(report.SelectedChecks))
	fmt.Printf("change_mode: %s changed_files: %d untracked_files: %d\n", report.ChangeMode, len(report.ChangedFiles), report.Sync.UntrackedCount)
	for _, check := range report.SelectedChecks {
		parts := []string{
			check.Name,
			"image=" + check.Image,
			"runner_size=" + check.RunnerSize,
			"network=" + check.NetworkMode,
			fmt.Sprintf("docker=%t", check.Docker),
			fmt.Sprintf("artifacts=%d", len(check.ArtifactPaths)),
		}
		if len(check.Services) > 0 {
			parts = append(parts, "services="+strings.Join(check.Services, ","))
		}
		fmt.Println(strings.Join(parts, " "))
		fmt.Printf("  command: %s\n", compactCommand(check.Command, 140))
		if len(check.Reasons) > 0 {
			reason := check.Reasons[0]
			fmt.Printf("  reason: %s\n", affectedRuleMatchDescription(reason))
		}
		printDryRunWarnings(check.Warnings)
	}
	printDryRunWarnings(append(append([]string(nil), report.Warnings...), report.Sync.Warnings...))
}
