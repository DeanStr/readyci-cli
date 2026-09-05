package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestPlanRepoMapCommandJSON(t *testing.T) {
	repo := chdirCLITestRepo(t, "repo-plan-command")
	writeRepoMapFixture(t, repo)
	writeFileForRepoMapTest(t, filepath.Join(repo, ".readyci.yml"), `
project_id: airline
network:
  mode: none
cache:
  paths:
    - ~/.pnpm-store
checks:
  ci:
    command: make check-ci
  db:
    command: make check-ci-db
    docker: true
    network_mode: none
  web:
    command: make web-check
  spec-lint:
    command: make spec-lint
`)
	output, err := captureStdoutForCLITest(t, func() error {
		return planCommand([]string{"--repo-map", "--json"})
	})
	if err != nil {
		t.Fatalf("plan repo-map: %v\n%s", err, output)
	}
	var report repoMapReport
	if decodeErr := json.Unmarshal([]byte(output), &report); decodeErr != nil {
		t.Fatalf("decode repo map: %v\n%s", decodeErr, output)
	}
	if report.ProjectID != "airline" || len(report.Checks) != 4 {
		t.Fatalf("report project/checks = %s/%v, want airline checks", report.ProjectID, report.Checks)
	}
}

func TestPlanRecommendPrintInventoryJSONDoesNotRequireAuth(t *testing.T) {
	t.Setenv("READYCI_PROJECT_ID", "")
	repo := chdirCLITestRepo(t, "repo-plan-recommend-inventory")
	writeFileForRepoMapTest(t, filepath.Join(repo, ".readyci.yml"), `
project_id: airline
checks:
  web:
    command: pnpm test
`)
	writeFileForRepoMapTest(t, filepath.Join(repo, "package.json"), `{
  "private": true,
  "packageManager": "pnpm@10.16.0",
  "scripts": {
    "test": "vitest"
  },
  "dependencies": {"react": "latest", "vite": "latest"}
}`)
	writeFileForRepoMapTest(t, filepath.Join(repo, "docs", "api", "openapi.yaml"), "openapi: 3.1.0\n")

	output, err := captureStdoutForCLITest(t, func() error {
		return planCommand([]string{"--recommend", "--print-inventory", "--json", "--token", ""})
	})
	if err != nil {
		t.Fatalf("plan recommend print inventory: %v\n%s", err, output)
	}
	var inventory preflightapi.ProjectAnalysisInventory
	if decodeErr := json.Unmarshal([]byte(output), &inventory); decodeErr != nil {
		t.Fatalf("decode inventory: %v\n%s", decodeErr, output)
	}
	if inventory.ProjectID != "airline" {
		t.Fatalf("project_id = %q, want airline", inventory.ProjectID)
	}
	if inventory.ConfigPath != ".readyci.yml" || filepath.IsAbs(inventory.ConfigPath) || strings.Contains(inventory.ConfigPath, repo) {
		t.Fatalf("config_path = %q, want redacted display path", inventory.ConfigPath)
	}
	if !projectAnalysisInventoryHasFile(inventory, "docs/api/openapi.yaml", "openapi") {
		t.Fatalf("files = %+v, want OpenAPI file fact", inventory.Files)
	}
	if len(inventory.Packages) == 0 || inventory.Packages[0].Ecosystem != "node" {
		t.Fatalf("packages = %+v, want node inventory", inventory.Packages)
	}
}

func TestPlanRecommendPrintInventoryIncludesGoModules(t *testing.T) {
	t.Setenv("READYCI_PROJECT_ID", "")
	repo := chdirCLITestRepo(t, "repo-plan-recommend-go-inventory")
	runGitForCLITest(t, "init")
	writeFileForRepoMapTest(t, filepath.Join(repo, "go.mod"), "module example.com/repo\n\ngo 1.25\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, "pkg", "pkg.go"), "package pkg\n")

	output, err := captureStdoutForCLITest(t, func() error {
		return planCommand([]string{"--recommend", "--print-inventory", "--json", "--token", ""})
	})
	if err != nil {
		t.Fatalf("plan recommend print Go inventory: %v\n%s", err, output)
	}
	var inventory preflightapi.ProjectAnalysisInventory
	if decodeErr := json.Unmarshal([]byte(output), &inventory); decodeErr != nil {
		t.Fatalf("decode inventory: %v\n%s", decodeErr, output)
	}
	if !projectAnalysisInventoryHasPackage(inventory, ".", "go") {
		t.Fatalf("packages = %+v, want root Go module inventory", inventory.Packages)
	}
}

func TestPlanRecommendPrintInventoryIncludesOpsOnlyPaths(t *testing.T) {
	t.Setenv("READYCI_PROJECT_ID", "")
	repo := chdirCLITestRepo(t, "repo-plan-recommend-ops-inventory")
	writeFileForRepoMapTest(t, filepath.Join(repo, ".readyci.yml"), "project_id: airline\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, "deploy", "systemd", "readyci.service"), "[Unit]\nDescription=Ready CI\n")

	output, err := captureStdoutForCLITest(t, func() error {
		return planCommand([]string{"--recommend", "--print-inventory", "--json", "--token", ""})
	})
	if err != nil {
		t.Fatalf("plan recommend print ops inventory: %v\n%s", err, output)
	}
	var inventory preflightapi.ProjectAnalysisInventory
	if decodeErr := json.Unmarshal([]byte(output), &inventory); decodeErr != nil {
		t.Fatalf("decode inventory: %v\n%s", decodeErr, output)
	}
	if !projectAnalysisInventoryHasSignal(inventory, "ops-path", "deploy") {
		t.Fatalf("signals = %+v, want ops-path deploy signal", inventory.Signals)
	}
	if !projectAnalysisInventoryHasFile(inventory, "deploy", "ops") {
		t.Fatalf("files = %+v, want ops deploy file fact", inventory.Files)
	}
}

func TestPlanRecommendPrintInventoryIncludesShellOnlyPaths(t *testing.T) {
	t.Setenv("READYCI_PROJECT_ID", "")
	repo := chdirCLITestRepo(t, "repo-plan-recommend-shell-inventory")
	writeFileForRepoMapTest(t, filepath.Join(repo, ".readyci.yml"), "project_id: airline\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, "scripts", "check.sh"), "#!/usr/bin/env bash\necho ok\n")

	output, err := captureStdoutForCLITest(t, func() error {
		return planCommand([]string{"--recommend", "--print-inventory", "--json", "--token", ""})
	})
	if err != nil {
		t.Fatalf("plan recommend print shell inventory: %v\n%s", err, output)
	}
	var inventory preflightapi.ProjectAnalysisInventory
	if decodeErr := json.Unmarshal([]byte(output), &inventory); decodeErr != nil {
		t.Fatalf("decode inventory: %v\n%s", decodeErr, output)
	}
	if !projectAnalysisInventoryHasSignal(inventory, "shell-script", "scripts/check.sh") {
		t.Fatalf("signals = %+v, want shell-script signal", inventory.Signals)
	}
	if !projectAnalysisInventoryHasFile(inventory, "scripts/check.sh", "shell") {
		t.Fatalf("files = %+v, want shell file fact", inventory.Files)
	}
}

func TestPlanRecommendPrintInventoryPreservesLockfilePackageManager(t *testing.T) {
	t.Setenv("READYCI_PROJECT_ID", "")
	repo := chdirCLITestRepo(t, "repo-plan-recommend-lockfile-package-manager")
	writeFileForRepoMapTest(t, filepath.Join(repo, "package.json"), `{
  "private": true,
  "scripts": {"test": "vitest"},
  "dependencies": {"react": "latest"}
}`)
	writeFileForRepoMapTest(t, filepath.Join(repo, "pnpm-lock.yaml"), "lockfileVersion: '9.0'\n")

	output, err := captureStdoutForCLITest(t, func() error {
		return planCommand([]string{"--recommend", "--print-inventory", "--json", "--token", ""})
	})
	if err != nil {
		t.Fatalf("plan recommend print lockfile inventory: %v\n%s", err, output)
	}
	var inventory preflightapi.ProjectAnalysisInventory
	if decodeErr := json.Unmarshal([]byte(output), &inventory); decodeErr != nil {
		t.Fatalf("decode inventory: %v\n%s", decodeErr, output)
	}
	if len(inventory.Packages) == 0 {
		t.Fatalf("packages = %+v, want node inventory", inventory.Packages)
	}
	if got := inventory.Packages[0].PackageManager; got != "pnpm" {
		t.Fatalf("package_manager = %q, want lockfile-derived pnpm", got)
	}
	if got := inventory.Packages[0].Lockfile; got != "pnpm-lock.yaml" {
		t.Fatalf("lockfile = %q, want pnpm-lock.yaml", got)
	}
}

func TestPlanRecommendSuggestConfigCallsConfigSuggestions(t *testing.T) {
	t.Setenv("READYCI_PROJECT_ID", "")
	repo := chdirCLITestRepo(t, "repo-plan-recommend-suggest-config")
	writeFileForRepoMapTest(t, filepath.Join(repo, ".readyci.yml"), "project_id: readyci\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, "package.json"), `{
  "private": true,
  "scripts": {"test": "vitest"},
  "dependencies": {"react": "latest", "vite": "latest"}
}`)

	var requestedPath string
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if requestedPath != "/v1/projects/readyci/config-suggestions" {
			t.Fatalf("path = %s, want config-suggestions endpoint", requestedPath)
		}
		var req preflightapi.ConfigSuggestionsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Inventory.ProjectID != "readyci" || len(req.Inventory.Packages) == 0 {
			t.Fatalf("inventory = %+v, want readyci node inventory", req.Inventory)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"schema_version":"readyci.config_suggestions.v1","project_id":"readyci","suggested_config":"version: 1\nchecks:\n  web:\n    command: npm test\n"}`))
	}))
	defer server.Close()

	output, err := captureStdoutForCLITest(t, func() error {
		return planCommand([]string{"--recommend", "--suggest-config", "--json", "--api-url", server.URL, "--token", "token_test"})
	})
	if err != nil {
		t.Fatalf("plan recommend suggest config: %v\n%s", err, output)
	}
	if requestedPath == "" {
		t.Fatal("config-suggestions endpoint was not called")
	}
	if gotAuth != "Bearer token_test" {
		t.Fatalf("Authorization = %q, want bearer token", gotAuth)
	}
	var response preflightapi.ConfigSuggestionsResponse
	if err := json.Unmarshal([]byte(output), &response); err != nil {
		t.Fatalf("decode response: %v\n%s", err, output)
	}
	if !strings.Contains(response.SuggestedConfig, "command: npm test") {
		t.Fatalf("suggested config = %q, want server response", response.SuggestedConfig)
	}
}

func TestPlanRepoMapRejectsAnalyzerOnlyFlags(t *testing.T) {
	repo := chdirCLITestRepo(t, "repo-plan-repo-map-rejects-analyzer-flags")
	writeFileForRepoMapTest(t, filepath.Join(repo, ".readyci.yml"), "project_id: readyci\n")

	for _, args := range [][]string{
		{"--repo-map", "--print-inventory"},
		{"--repo-map", "--suggest-config"},
		{"--recommend", "--print-inventory", "--suggest-config"},
	} {
		if err := planCommand(args); err == nil {
			t.Fatalf("planCommand(%v) succeeded, want validation error", args)
		}
	}
}

func TestPlanRecommendPrintInventoryInfersProjectFromRepoRoot(t *testing.T) {
	t.Setenv("READYCI_PROJECT_ID", "")
	repo := chdirCLITestRepo(t, "repo-plan-recommend-subdir-project")
	writeFileForRepoMapTest(t, filepath.Join(repo, ".readyci.yml"), `
checks:
  web:
    command: pnpm test
`)
	subdir := filepath.Join(repo, "apps", "web")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	if err := os.Chdir(subdir); err != nil {
		t.Fatalf("chdir subdir: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return planCommand([]string{"--recommend", "--print-inventory", "--json", "--token", ""})
	})
	if err != nil {
		t.Fatalf("plan recommend from subdir: %v\n%s", err, output)
	}
	var inventory preflightapi.ProjectAnalysisInventory
	if decodeErr := json.Unmarshal([]byte(output), &inventory); decodeErr != nil {
		t.Fatalf("decode inventory: %v\n%s", decodeErr, output)
	}
	if want := inferProjectID(repo); inventory.ProjectID != want {
		t.Fatalf("project_id = %q, want repo root fallback %q", inventory.ProjectID, want)
	}
}

func TestPlanRepoMapCommandTextPrintsUnreferencedChecks(t *testing.T) {
	repo := chdirCLITestRepo(t, "repo-plan-text-unreferenced")
	writeFileForRepoMapTest(t, filepath.Join(repo, ".readyci.yml"), `
project_id: airline
affected:
  rules:
    - paths:
        - README.md
      checks:
        - ci
checks:
  ci:
    command: make check-ci
  old-db:
    command: make old-db
`)
	output, err := captureStdoutForCLITest(t, func() error {
		return planCommand([]string{"--repo-map"})
	})
	if err != nil {
		t.Fatalf("plan repo-map text: %v\n%s", err, output)
	}
	requireCLITestOutputContains(t, output,
		"status: warn",
		"unreferenced_checks:",
		"  old-db",
	)
}

func TestRepoMapUnreferencedChecksExpandsAffectedProfiles(t *testing.T) {
	repo := chdirCLITestRepo(t, "repo-map-profile-usage")
	writeFileForRepoMapTest(t, filepath.Join(repo, ".readyci.yml"), `
project_id: airline
affected:
  profiles:
    - shell
checks:
  shell-syntax:
    command: bash -n scripts/check.sh
`)
	cfg, configPath, err := config.LoadProjectConfig(repo)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	report := buildRepoMapReport(context.Background(), repo, cfg, configPath)
	if contains(report.Unreferenced, "shell-syntax") {
		t.Fatalf("unreferenced checks = %v, shell-syntax is selected by shell profile", report.Unreferenced)
	}
}

func TestRepoMapUnreferencedChecksIncludesCheckWhenChanges(t *testing.T) {
	repo := chdirCLITestRepo(t, "repo-map-when-usage")
	writeFileForRepoMapTest(t, filepath.Join(repo, ".readyci.yml"), `
project_id: airline
checks:
  web:
    command: make web-check
    when:
      changes:
        include:
          - apps/web/**
`)
	cfg, configPath, err := config.LoadProjectConfig(repo)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	report := buildRepoMapReport(context.Background(), repo, cfg, configPath)
	if contains(report.Unreferenced, "web") {
		t.Fatalf("unreferenced checks = %v, web is selected by checks.web.when.changes", report.Unreferenced)
	}
}

func TestRepoMapUnreferencedChecksIncludesUnmatchedChecks(t *testing.T) {
	repo := chdirCLITestRepo(t, "repo-map-unmatched-usage")
	writeFileForRepoMapTest(t, filepath.Join(repo, ".readyci.yml"), `
project_id: airline
affected:
  unmatched_checks: [ci]
  rules:
    - paths: ["**/*.go"]
      checks: [go]
checks:
  go:
    command: go test ./...
  ci:
    command: make check
`)
	cfg, configPath, err := config.LoadProjectConfig(repo)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	report := buildRepoMapReport(context.Background(), repo, cfg, configPath)
	if contains(report.Unreferenced, "ci") {
		t.Fatalf("unreferenced checks = %v, ci is selected by affected.unmatched_checks", report.Unreferenced)
	}
}

func TestPlanRepoMapCommandUsesConfigRootFromSubdirectory(t *testing.T) {
	repo := chdirCLITestRepo(t, "repo-plan-subdir")
	writeRepoMapFixture(t, repo)
	writeFileForRepoMapTest(t, filepath.Join(repo, ".readyci.yml"), `
project_id: airline
checks:
  ci:
    command: make check-ci
  db:
    command: make check-ci-db-parallel
  db-serial:
    command: make check-ci-db
  all-db:
    command: make check-all-db
`)
	subdir := filepath.Join(repo, "apps", "web")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	if err := os.Chdir(subdir); err != nil {
		t.Fatalf("chdir subdir: %v", err)
	}
	output, err := captureStdoutForCLITest(t, func() error {
		return planCommand([]string{"--repo-map", "--json"})
	})
	if err != nil {
		t.Fatalf("plan repo-map from subdir: %v\n%s", err, output)
	}
	var report repoMapReport
	if decodeErr := json.Unmarshal([]byte(output), &report); decodeErr != nil {
		t.Fatalf("decode repo map: %v\n%s", decodeErr, output)
	}
	if len(report.Make.Targets) == 0 {
		t.Fatalf("repo map = %+v, want root Makefile scanned from subdir", report)
	}
}

func TestPlanRepoMapCommandUsesGitRootWithoutConfigFromSubdirectory(t *testing.T) {
	repo := chdirCLITestRepo(t, "repo-plan-subdir-no-config")
	runGitForCLITest(t, "init")
	writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), `
check-ci:
	go test ./...
`)
	writeFileForRepoMapTest(t, filepath.Join(repo, ".github", "workflows", "ci.yml"), `
name: ci
on: [push]
jobs:
  check:
    runs-on: ubuntu-latest
    steps:
      - run: make check-ci
`)
	subdir := filepath.Join(repo, "cli", "cmd", "readyci")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	if err := os.Chdir(subdir); err != nil {
		t.Fatalf("chdir subdir: %v", err)
	}
	output, err := captureStdoutForCLITest(t, func() error {
		return planCommand([]string{"--repo-map", "--json"})
	})
	if err != nil {
		t.Fatalf("plan repo-map from config-less subdir: %v\n%s", err, output)
	}
	var report repoMapReport
	if decodeErr := json.Unmarshal([]byte(output), &report); decodeErr != nil {
		t.Fatalf("decode repo map: %v\n%s", decodeErr, output)
	}
	if len(report.Make.Targets) != 1 || report.Make.Targets[0].Name != "check-ci" || report.Make.Targets[0].Path != "Makefile" {
		t.Fatalf("make targets = %+v, want root Makefile check-ci from subdir", report.Make.Targets)
	}
	if len(report.GitHubActions.Workflows) != 1 || report.GitHubActions.Workflows[0].Path != ".github/workflows/ci.yml" {
		t.Fatalf("workflows = %+v, want root GitHub Actions workflow from subdir", report.GitHubActions.Workflows)
	}
}

func TestPlanRepoMapCommandUsesInferredConfigWhenAvailable(t *testing.T) {
	repo := chdirCLITestRepo(t, "repo-plan-inferred-config")
	runGitForCLITest(t, "init")
	writeFileForRepoMapTest(t, filepath.Join(repo, "go.mod"), "module example.com/repo\n\ngo 1.25\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, "pkg", "pkg.go"), "package pkg\n")
	output, err := captureStdoutForCLITest(t, func() error {
		return planCommand([]string{"--repo-map", "--json"})
	})
	if err != nil {
		t.Fatalf("plan repo-map inferred: %v\n%s", err, output)
	}
	var report repoMapReport
	if decodeErr := json.Unmarshal([]byte(output), &report); decodeErr != nil {
		t.Fatalf("decode repo map: %v\n%s", decodeErr, output)
	}
	if !report.Inferred || report.ConfigPath != inferredConfigPath || !contains(report.Checks, "go") {
		t.Fatalf("repo map = %+v, want inferred go config", report)
	}
}
