package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
	"github.com/DeanStr/readyci-cli/pkg/verificationplan"
)

func TestCheckDryRunReportUsesResolvedPlan(t *testing.T) {
	cliTestWorkingDirectoryMu.Lock()
	defer cliTestWorkingDirectoryMu.Unlock()

	repo := t.TempDir()
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore wd: %v", err)
		}
	}()

	runGitForCLITest(t, "init")
	runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	if err := os.WriteFile("tracked.txt", []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write tracked: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.WriteFile("tracked.txt", []byte("changed\n"), 0o644); err != nil {
		t.Fatalf("change tracked: %v", err)
	}
	if err := os.WriteFile(".readyci.yml", []byte(`
project_id: airline
image: ubuntu-24.04-ci-docker
retries: 2
cache:
  paths:
    - ~/.cargo
  key_files:
    - Cargo.lock
artifacts:
  paths:
    - coverage/**
services:
  postgres:
    type: postgres
    version: "16"
    env:
      POSTGRES_DB: readyci
      POSTGRES_PASSWORD: readyci
    health:
      command: pg_isready -U postgres
      timeout: 30s
      interval: 2s
checks:
  ci:
    command: make check-ci
    docker: true
    services:
      - postgres
    concurrency_group: db
    concurrency_limit: 1
    prepare: scripts/bootstrap-web-deps.sh
    always:
      - scripts/collect-logs.sh
    changes: working
    when:
      event:
        - push
        - pull_request
      branch:
        - main
      tag: v*
      env:
        READYCI_E2E_WEBKIT: "1"
      status: success
      changes:
        include:
          - apps/web/**
        exclude:
          - apps/web/generated/**
    timeout: 30m
    cache:
      paths:
        - target
      key_files:
        - apps/web/pnpm-lock.yaml
    matrix:
      NODE_VERSION: ["22", "24", "26"]
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, path, err := config.LoadProjectConfig(".")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	plan, err := resolveCheckRunPlan("ci", cfg, checkResolveOptions{RetryCount: -1})
	if err != nil {
		t.Fatalf("resolve check: %v", err)
	}
	overridePlan, err := resolveCheckRunPlan("ci", cfg, checkResolveOptions{RetryCount: -1, Timeout: "45m"})
	if err != nil {
		t.Fatalf("resolve check with timeout override: %v", err)
	}
	if overridePlan.Options.TimeoutSeconds != 2700 {
		t.Fatalf("timeout override seconds = %d, want 2700", overridePlan.Options.TimeoutSeconds)
	}
	report, err := buildCheckDryRunReport(context.Background(), ".", cfg, path, plan)
	if err != nil {
		t.Fatalf("build check dry-run: %v", err)
	}
	if report.ProjectID != "airline" || report.Image != "ubuntu-24.04-ci-docker" || report.Retries != 2 {
		t.Fatalf("dry-run plan = %+v", report)
	}
	if len(report.Services) != 1 || report.Services[0] != "postgres" {
		t.Fatalf("dry-run services = %v, want postgres", report.Services)
	}
	if len(report.ServiceDefinitions) != 1 || report.ServiceDefinitions[0].Name != "postgres" || report.ServiceDefinitions[0].Env["POSTGRES_DB"] != "readyci" {
		t.Fatalf("dry-run service definitions = %+v, want postgres", report.ServiceDefinitions)
	}
	if report.ConcurrencyGroup != "db" || report.ConcurrencyLimit != 1 {
		t.Fatalf("dry-run concurrency = %q/%d, want db/1", report.ConcurrencyGroup, report.ConcurrencyLimit)
	}
	if report.When == nil || report.When.Changes == nil || strings.Join(report.When.Changes.Include, ",") != "apps/web/**" || strings.Join(report.When.Changes.Exclude, ",") != "apps/web/generated/**" {
		t.Fatalf("dry-run when = %+v, want include/exclude changes", report.When)
	}
	if strings.Join(report.When.Event, ",") != "push,pull_request" || strings.Join(report.When.Branch, ",") != "main" || report.When.Tag != "v*" || report.When.Env["READYCI_E2E_WEBKIT"] != "1" || report.When.Status != "success" {
		t.Fatalf("dry-run typed when = %+v, want event/branch/tag/env/status", report.When)
	}
	if report.MatrixVariantCount != 3 {
		t.Fatalf("matrix variants = %d, want 3", report.MatrixVariantCount)
	}
	if !contains(report.CachePaths, "target") || !contains(report.ArtifactPaths, "coverage/**") {
		t.Fatalf("paths not resolved in dry-run: cache=%v artifacts=%v", report.CachePaths, report.ArtifactPaths)
	}
	if !contains(report.CacheKeyFiles, "Cargo.lock") || !contains(report.CacheKeyFiles, "apps/web/pnpm-lock.yaml") {
		t.Fatalf("cache key files not resolved in dry-run: %v", report.CacheKeyFiles)
	}
	if len(report.PrepareCommands) != 1 || report.PrepareCommands[0] != "scripts/bootstrap-web-deps.sh" {
		t.Fatalf("prepare commands = %v", report.PrepareCommands)
	}
	if len(report.AlwaysCommands) != 1 || report.AlwaysCommands[0] != "scripts/collect-logs.sh" {
		t.Fatalf("always commands = %v", report.AlwaysCommands)
	}
	if report.Sync.ChangeMode != syncbundle.ChangeModeWorking || !contains(report.Sync.ChangedFiles, "tracked.txt") {
		t.Fatalf("sync report = %+v", report.Sync)
	}
}

func TestCheckListReportSummarizesChecks(t *testing.T) {
	cfg := config.ProjectConfig{Checks: map[string]config.CheckConfig{
		"ci":      {Command: "make ci", Changes: "all"},
		"touched": {Command: "make touched", Docker: boolPtr(true)},
	}}
	report := buildCheckListReport(cfg, ".readyci.yml")
	if len(report.Checks) != 2 {
		t.Fatalf("checks = %d, want 2", len(report.Checks))
	}
	if report.Checks[0].Name != "ci" || report.Checks[1].Name != "touched" {
		t.Fatalf("checks not sorted: %+v", report.Checks)
	}
	if !report.Checks[1].Docker {
		t.Fatalf("docker check not reported: %+v", report.Checks[1])
	}
}

func TestCheckListReportShowsInheritedDocker(t *testing.T) {
	cfg := config.ProjectConfig{
		Resources: config.ResourceConfig{
			Default: config.ResourceDefaults{Docker: boolPtr(true)},
		},
		Checks: map[string]config.CheckConfig{
			"ci": {Command: "make ci"},
		},
	}
	report := buildCheckListReport(cfg, ".readyci.yml")
	if len(report.Checks) != 1 || !report.Checks[0].Docker {
		t.Fatalf("check list report = %+v, want inherited docker=true", report.Checks)
	}
}

func TestResolveCheckRunPlanAllowsCheckDockerFalseOverride(t *testing.T) {
	cfg := config.ProjectConfig{
		Resources: config.ResourceConfig{
			Default: config.ResourceDefaults{Docker: boolPtr(true)},
		},
		Checks: map[string]config.CheckConfig{
			"ci": {Command: "make ci", Docker: boolPtr(false)},
		},
	}
	plan, err := resolveCheckRunPlan("ci", cfg, checkResolveOptions{RetryCount: -1})
	if err != nil {
		t.Fatalf("resolve check: %v", err)
	}
	if plan.Options.Docker {
		t.Fatalf("docker = true, want explicit check override to false")
	}
}

func TestResolveCheckRunPlanCheckTimeoutSecondsOverridesResourceDefault(t *testing.T) {
	cfg := config.ProjectConfig{
		Resources: config.ResourceConfig{
			Default: config.ResourceDefaults{Timeout: "45m"},
		},
		Checks: map[string]config.CheckConfig{
			"ci": {Command: "make ci", TimeoutSeconds: 300},
		},
	}
	plan, err := resolveCheckRunPlan("ci", cfg, checkResolveOptions{RetryCount: -1})
	if err != nil {
		t.Fatalf("resolve check: %v", err)
	}
	if plan.Options.TimeoutSeconds != 300 {
		t.Fatalf("timeout seconds = %d, want check timeout_seconds 300", plan.Options.TimeoutSeconds)
	}
}

func TestResolveCheckRunPlanKeepsCommandChecksOutOfStepMode(t *testing.T) {
	cfg := config.ProjectConfig{
		Checks: map[string]config.CheckConfig{
			"ci": {
				Prepare: config.CommandList{"READYCI_PHASE_VALUE=prepared"},
				Command: `printf "%s" "$READYCI_PHASE_VALUE" > phase.log`,
			},
		},
	}
	plan, err := resolveCheckRunPlan("ci", cfg, checkResolveOptions{RetryCount: -1})
	if err != nil {
		t.Fatalf("resolve check: %v", err)
	}
	if len(plan.Options.Steps) != 0 {
		t.Fatalf("legacy command check steps = %+v, want none", plan.Options.Steps)
	}
	command, err := verificationplan.CommandForRunSpec(verificationplan.RunSpec{
		PrepareCommands: plan.Options.PrepareCommands,
		Command:         plan.Options.Command,
		Steps:           plan.Options.Steps,
	})
	if err != nil {
		t.Fatalf("build remote command: %v", err)
	}
	workdir := t.TempDir()
	cmd := exec.Command("bash", "-lc", command)
	cmd.Dir = workdir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run command: %v: %s", err, output)
	}
	data, err := os.ReadFile(filepath.Join(workdir, "phase.log"))
	if err != nil {
		t.Fatalf("read phase log: %v", err)
	}
	if got := string(data); got != "prepared" {
		t.Fatalf("phase log = %q, want prepared", got)
	}
}

func TestResolveCheckRunPlanCacheSize(t *testing.T) {
	cfg := config.ProjectConfig{
		Image:      "ubuntu-24.04-base",
		RunnerSize: "standard",
		Cache: config.CacheConfig{
			Size: "20GiB",
		},
		Checks: map[string]config.CheckConfig{
			"project-default": {Command: "make ci"},
			"ephemeral": {
				Command: "make isolated",
				Cache: config.CacheConfig{
					Mode: "ephemeral",
					Size: "40GiB",
				},
			},
			"override": {
				Command: "make web",
				Cache:   config.CacheConfig{Size: "30GiB"},
			},
		},
	}

	projectPlan, err := resolveCheckRunPlan("project-default", cfg, checkResolveOptions{RetryCount: -1})
	if err != nil {
		t.Fatalf("resolve project default: %v", err)
	}
	if projectPlan.Options.CacheLaneSizeBytes != 20*1024*1024*1024 {
		t.Fatalf("project cache size = %d, want 20 GiB", projectPlan.Options.CacheLaneSizeBytes)
	}
	overridePlan, err := resolveCheckRunPlan("override", cfg, checkResolveOptions{RetryCount: -1})
	if err != nil {
		t.Fatalf("resolve override: %v", err)
	}
	if overridePlan.Options.CacheLaneSizeBytes != 30*1024*1024*1024 {
		t.Fatalf("override cache size = %d, want 30 GiB", overridePlan.Options.CacheLaneSizeBytes)
	}
	ephemeralPlan, err := resolveCheckRunPlan("ephemeral", cfg, checkResolveOptions{RetryCount: -1})
	if err != nil {
		t.Fatalf("resolve ephemeral: %v", err)
	}
	if ephemeralPlan.Options.CacheMode != "ephemeral" {
		t.Fatalf("ephemeral cache mode = %q, want ephemeral", ephemeralPlan.Options.CacheMode)
	}
	if ephemeralPlan.Options.CacheLaneSizeBytes != 0 {
		t.Fatalf("ephemeral cache size = %d, want ignored zero size", ephemeralPlan.Options.CacheLaneSizeBytes)
	}
}
