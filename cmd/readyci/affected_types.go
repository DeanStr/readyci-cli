package main

import (
	"context"

	"github.com/DeanStr/readyci-cli/internal/syncbundlebuild"
	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

type affectedDryRunReport struct {
	SkipWarm       bool                   `json:"skip_warm"`
	ConfigPath     string                 `json:"config_path"`
	ProjectID      string                 `json:"project_id"`
	ChangeMode     string                 `json:"change_mode"`
	ChangedFiles   []string               `json:"changed_files,omitempty"`
	SelectedChecks []affectedDryRunCheck  `json:"checks"`
	SkippedChecks  []affectedSkippedCheck `json:"skipped_checks,omitempty"`
	Warnings       []string               `json:"warnings,omitempty"`
	Plan           *verificationPlan      `json:"plan,omitempty"`
	Sync           syncDryRunReport       `json:"sync"`
}

type affectedDryRunCheck struct {
	SecretsRequested   bool                `json:"secrets_requested"`
	Name               string              `json:"name"`
	Command            string              `json:"command"`
	Image              string              `json:"image"`
	Region             string              `json:"region"`
	RunnerSize         string              `json:"runner_size"`
	TimeoutSeconds     int                 `json:"timeout_seconds"`
	CacheMode          string              `json:"cache_mode,omitempty"`
	CacheLaneSizeBytes int64               `json:"cache_lane_size_bytes,omitempty"`
	NetworkMode        string              `json:"network_mode"`
	Docker             bool                `json:"docker"`
	Services           []string            `json:"services,omitempty"`
	ArtifactPaths      []string            `json:"artifact_paths,omitempty"`
	ChangeMode         string              `json:"change_mode"`
	MatrixVariantCount int                 `json:"matrix_variant_count"`
	Warnings           []string            `json:"warnings,omitempty"`
	Reasons            []affectedRuleMatch `json:"reasons,omitempty"`
}

type affectedCheckPlan struct {
	Plan    checkRunPlan
	Reasons []affectedRuleMatch
}

type affectedChangedFiles struct {
	Root             string
	ChangeMode       string
	IncludeUntracked bool
	ExcludePatterns  []string
	ReviewedFixtures []syncbundle.ReviewedFixture
	All              []string
	Untracked        []string
	ByCheck          map[string][]string
}

type affectedAuditReport struct {
	ConfigPath          string                       `json:"config_path"`
	Profiles            []string                     `json:"profiles,omitempty"`
	Rules               int                          `json:"rules"`
	PathSources         []affectedRulePathSource     `json:"path_sources,omitempty"`
	Tests               []affectedAuditTestReport    `json:"tests,omitempty"`
	Examples            []affectedAuditExample       `json:"examples,omitempty"`
	DuplicateSelections []affectedDuplicateSelection `json:"duplicate_selections,omitempty"`
	UnmatchedFiles      []string                     `json:"unmatched_files,omitempty"`
	SuggestedRules      []affectedSuggestRule        `json:"suggested_rules,omitempty"`
	IgnoredFiles        []string                     `json:"ignored_files,omitempty"`
	Warnings            []string                     `json:"warnings,omitempty"`
	Status              string                       `json:"status"`
}

type affectedRulePathSource struct {
	Rule           int    `json:"rule"`
	Pattern        string `json:"pattern"`
	SourceFile     string `json:"source_file"`
	SourceLine     int    `json:"source_line"`
	SourceTemplate string `json:"source_template,omitempty"`
}

type affectedAuditTestReport struct {
	Paths          []string `json:"paths"`
	ExpectedChecks []string `json:"expected_checks"`
	ActualChecks   []string `json:"actual_checks"`
	Status         string   `json:"status"`
	Error          string   `json:"error,omitempty"`
}

type affectedAuditExample struct {
	Paths  []string `json:"paths"`
	Checks []string `json:"checks"`
}

type affectedDuplicateSelection struct {
	Path           string   `json:"path"`
	Aggregate      string   `json:"aggregate"`
	SubsumedChecks []string `json:"subsumed_checks"`
}

type affectedExplainReport struct {
	ConfigPath string                `json:"config_path"`
	Paths      []affectedExplainPath `json:"paths"`
	Status     string                `json:"status"`
}

type affectedExplainPath struct {
	Path          string                 `json:"path"`
	Checks        []string               `json:"checks,omitempty"`
	Reasons       []affectedRuleMatch    `json:"reasons,omitempty"`
	SkippedChecks []affectedSkippedCheck `json:"skipped_checks,omitempty"`
	Covered       bool                   `json:"covered"`
	WhyNot        string                 `json:"why_not,omitempty"`
	Ignored       bool                   `json:"ignored,omitempty"`
	IgnoredBy     string                 `json:"ignored_by,omitempty"`
	FilterHits    []string               `json:"filter_hits,omitempty"`
}

type cargoAffectedReport struct {
	WorkspaceRoot string                        `json:"workspace_root"`
	CheckPrefix   string                        `json:"check_prefix"`
	Packages      []cargoAffectedPackage        `json:"packages"`
	Rules         []cargoAffectedRuleSuggestion `json:"rules"`
	Warnings      []string                      `json:"warnings,omitempty"`
}

type cargoAffectedPackage struct {
	Name       string   `json:"name"`
	Check      string   `json:"check"`
	Path       string   `json:"path"`
	Dependents []string `json:"dependents,omitempty"`
}

type cargoAffectedRuleSuggestion struct {
	Paths  []string `json:"paths"`
	Checks []string `json:"checks"`
}

type affectedProfileRule struct {
	Paths               []string
	CheckCandidates     []string
	FallbackCheckGroups [][]string
}

type affectedSuggestReport struct {
	ConfigPath  string                `json:"config_path,omitempty"`
	Profiles    []string              `json:"profiles,omitempty"`
	Rules       []affectedSuggestRule `json:"rules,omitempty"`
	Subsumes    map[string][]string   `json:"subsumes,omitempty"`
	Tests       []affectedSuggestTest `json:"tests,omitempty"`
	AuditIgnore []string              `json:"audit_ignore,omitempty"`
	Warnings    []string              `json:"warnings,omitempty"`
}

type affectedSuggestRule struct {
	Paths   []string `json:"paths" yaml:"paths"`
	Exclude []string `json:"exclude,omitempty" yaml:"exclude,omitempty"`
	Checks  []string `json:"checks" yaml:"checks"`
}

type affectedSuggestTest struct {
	Path   string   `json:"path,omitempty" yaml:"path,omitempty"`
	Paths  []string `json:"paths,omitempty" yaml:"paths,omitempty"`
	Checks []string `json:"checks" yaml:"checks"`
}

type affectedPathFilterCache struct {
	ctx        context.Context
	cwd        string
	cfg        config.ProjectConfig
	changeMode string
	filters    map[string]syncbundlebuild.PathFilter
}
