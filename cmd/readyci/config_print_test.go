package main

import (
	"os"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

func TestPrintConfigExplainReportIncludesAllConfiguredSections(t *testing.T) {
	report := configExplainReport{
		ConfigPath:           ".readyci.yml",
		ProjectID:            "proj_dev",
		Region:               "us-east-1",
		Image:                "ubuntu-24.04-ci",
		RunnerSize:           "large",
		TimeoutSeconds:       1800,
		Retries:              2,
		ConcurrencyGroup:     "deploy/main",
		ConcurrencyLimit:     1,
		CacheMode:            "pull-push",
		CachePaths:           []string{".pnpm-store"},
		CacheKeyFiles:        []string{"pnpm-lock.yaml"},
		ArtifactPaths:        []string{"dist/**"},
		NetworkMode:          "allowlist",
		NetworkAllowlist:     []string{"api.readyci.test"},
		WarmNetworkMode:      "allowlist",
		WarmNetworkAllowlist: []string{"packages.readyci.test"},
		SyncExclude:          []string{"node_modules/**"},
		IncludeUntracked:     false,
		AffectedProfiles:     []string{"node"},
		AffectedUnmatched:    []string{"ci"},
		AffectedTests:        2,
		AffectedAuditIgnore:  []string{"docs/**"},
		AffectedSubsumes:     map[string][]string{"build": {"lint", "unit"}},
		Warnings:             []string{"profile warning"},
		Status:               "invalid",
		Error:                "check failed validation",
		FieldHelp: []config.ConfigField{{
			Scope:       config.ScopeCheck,
			Name:        "command",
			Path:        "checks.<name>.command",
			Description: "Shell command for a check.",
		}},
		Services: []configExplainService{{
			Name:           "postgres",
			Type:           "postgres",
			Version:        "16",
			Env:            map[string]string{"POSTGRES_DB": "readyci"},
			HealthCommand:  "pg_isready",
			HealthTimeout:  "30s",
			HealthInterval: "2s",
			Logs:           []string{"/var/log/postgres/*.log"},
		}},
		AffectedRules: []configExplainAffectedRule{{
			Paths:   []string{"src/**"},
			Exclude: []string{"src/generated/**"},
			Checks:  []string{"unit"},
		}},
		Checks: []configExplainCheckReport{
			{
				Name:               "unit",
				Command:            "go test ./...",
				PrepareCommands:    []string{"pnpm install"},
				PostCommands:       []string{"echo post"},
				AlwaysCommands:     []string{"echo always"},
				Image:              "ubuntu-24.04-ci",
				Region:             "us-east-1",
				RunnerSize:         "large",
				TimeoutSeconds:     1800,
				ConcurrencyGroup:   "deploy/main",
				ConcurrencyLimit:   1,
				CacheMode:          "pull-push",
				CachePaths:         []string{".pnpm-store"},
				CacheKeyFiles:      []string{"pnpm-lock.yaml"},
				ArtifactPaths:      []string{"dist/**"},
				NetworkMode:        "allowlist",
				NetworkAllowlist:   []string{"api.readyci.test"},
				SyncExclude:        []string{"node_modules/**"},
				IncludeUntracked:   false,
				Docker:             true,
				Services:           []string{"postgres"},
				WorkerSelector:     map[string]string{"pool": "linux", "size": "large"},
				When:               &checkWhenReport{Changes: &checkWhenChangesReport{Include: []string{"src/**"}, Exclude: []string{"src/generated/**"}}},
				ChangeMode:         "working",
				KeepFailed:         true,
				Retries:            2,
				MatrixVariantCount: 3,
			},
			{
				Name:  "broken",
				Error: "missing command",
			},
		},
	}

	output, err := captureStdoutForCLITest(t, func() error {
		printConfigExplainReport(report)
		return nil
	})
	if err != nil {
		t.Fatalf("capture config explain report: %v", err)
	}
	requireCLITestOutputContains(t, output,
		"config: .readyci.yml",
		"status: invalid",
		"error: check failed validation",
		"concurrency_group: deploy/main",
		"cache_key_file: pnpm-lock.yaml",
		"artifact_path: dist/**",
		"warm_network_mode: allowlist",
		"warm_network_allowlist: packages.readyci.test",
		"affected_profiles: node",
		"affected_unmatched_checks: ci",
		"affected_subsumes: build -> lint, unit",
		"warning: profile warning",
		"service: postgres",
		"  env: POSTGRES_DB=readyci",
		"check: unit",
		"  prepare: pnpm install",
		"  always: echo always",
		"  network_allowlist: api.readyci.test",
		"  worker_selector: pool=linux,size=large",
		"  when.changes.include: src/**",
		"  matrix_variants: 3",
		"check: broken",
		"  error: missing command",
		"fields: 1",
		"field: checks.<name>.command",
		"  scope: check",
		"  description: Shell command for a check.",
	)
	if strings.Contains(output, "network_allowlist: []") {
		t.Fatalf("output leaked Go slice formatting: %s", output)
	}
}

func TestConfigCommandDispatchesAndRejectsUnknownSubcommands(t *testing.T) {
	if err := configCommand(nil); err == nil || !strings.Contains(err.Error(), "readyci config") {
		t.Fatalf("configCommand(nil) error = %v, want usage", err)
	}
	if err := configCommand([]string{"unknown"}); err == nil || !strings.Contains(err.Error(), "unknown config command") {
		t.Fatalf("configCommand unknown error = %v, want unknown command", err)
	}
	if err := configValidateCommand([]string{"extra"}); err == nil || !strings.Contains(err.Error(), "usage: readyci config validate") {
		t.Fatalf("configValidateCommand extra arg error = %v, want usage", err)
	}
}

func TestConfigValidateCommandPrintsSummary(t *testing.T) {
	repo := t.TempDir()
	t.Chdir(repo)
	if err := os.WriteFile(".readyci.yml", []byte(`
project_id: owner/repo
region: iad
checks:
  go:
    command: go test ./...
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	output, err := captureStdoutForCLITest(t, func() error {
		return configValidateCommand(nil)
	})
	if err != nil {
		t.Fatalf("config validate: %v", err)
	}
	requireCLITestOutputContains(t, output,
		"config:",
		".readyci.yml",
		"project_id: owner/repo",
		"region: iad",
		"checks: 1",
		"status: ok",
	)
}
