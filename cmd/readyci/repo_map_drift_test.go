package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

func TestRepoMapReportsUndefinedCrossFileChecks(t *testing.T) {
	repo := chdirCLITestRepo(t, "repo-map-drift")
	writeRepoMapFixture(t, repo)
	writeFileForRepoMapTest(t, filepath.Join(repo, ".readyci.yml"), `
project_id: airline
checks:
  ci:
    command: make check-ci
  web:
    command: make web-check
`)
	cfg, configPath, err := config.LoadProjectConfig(repo)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	report := buildRepoMapReport(context.Background(), repo, cfg, configPath)
	if report.Status != "warn" {
		t.Fatalf("status = %q, want warn", report.Status)
	}
	undefined := map[string]bool{}
	for _, item := range report.UndefinedChecks {
		undefined[item.Check] = true
	}
	for _, check := range []string{"db", "db-serial", "all-db"} {
		if !undefined[check] {
			t.Fatalf("undefined checks = %+v, want %s", report.UndefinedChecks, check)
		}
	}
	if len(report.NodePackages) != 1 || !report.NodePackages[0].HasVite || !contains(report.NodePackages[0].ContractScripts, "contracts:check") {
		t.Fatalf("node packages = %+v, want Vite package with contracts", report.NodePackages)
	}
	if len(report.OpenAPI) != 1 || report.OpenAPI[0].Source != "docs/api/openapi.yaml" {
		t.Fatalf("openapi = %+v, want docs/api/openapi.yaml", report.OpenAPI)
	}
}

func TestRepoMapDetectsReadyCIRustWrapperMacro(t *testing.T) {
	repo := chdirCLITestRepo(t, "repo-map-rust-wrapper")
	writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), `
READYCI ?= readyci

define remote-readyci-rust-check
	@$(READYCI) check $(READYCI_CHECK_FLAGS) rust-deps
	@$(READYCI) check $(READYCI_CHECK_FLAGS) $(1)
endef

remote-check-db:
	$(call remote-readyci-rust-check,db)

remote-check-all:
	$(call remote-readyci-rust-check,all)

remote-check-all-db:
	$(call remote-readyci-rust-check,all-db)
`)
	writeFileForRepoMapTest(t, filepath.Join(repo, ".readyci.yml"), `
project_id: airline
checks:
  rust-deps:
    command: cargo fetch --locked
  db:
    command: cargo test --features db
  all:
    command: cargo test --workspace
  all-db:
    command: cargo test --workspace --features db
`)
	cfg, configPath, err := config.LoadProjectConfig(repo)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	report := buildRepoMapReport(context.Background(), repo, cfg, configPath)
	for _, check := range []string{"rust-deps", "db", "all", "all-db"} {
		if !repoCheckReferencesContain(report.CheckReferences, check) {
			t.Fatalf("check references = %+v, want %s from rust wrapper", report.CheckReferences, check)
		}
	}
	if len(report.UndefinedChecks) != 0 {
		t.Fatalf("undefined checks = %+v, want none", report.UndefinedChecks)
	}
	if len(report.Unreferenced) != 0 {
		t.Fatalf("unreferenced checks = %+v, want none", report.Unreferenced)
	}
}

func TestDoctorRepoModeFailsOnUndefinedCrossFileChecks(t *testing.T) {
	repo := chdirCLITestRepo(t, "repo-doctor")
	writeRepoMapFixture(t, repo)
	writeFileForRepoMapTest(t, filepath.Join(repo, ".readyci.yml"), `
project_id: airline
checks:
  ci:
    command: make check-ci
`)
	output, err := captureStdoutForCLITest(t, func() error {
		return doctorCommand([]string{"--repo", "--json"})
	})
	if err == nil {
		t.Fatalf("doctor --repo error = nil, want exit error\n%s", output)
	}
	var report doctorReport
	if decodeErr := json.Unmarshal([]byte(output), &report); decodeErr != nil {
		t.Fatalf("decode doctor report: %v\n%s", decodeErr, output)
	}
	if report.Repo == nil || len(report.Repo.UndefinedChecks) == 0 {
		t.Fatalf("repo doctor = %+v, want undefined checks", report.Repo)
	}
	if !containsDoctorDiagnostic(report.Diagnostics, "repo", "fail") {
		t.Fatalf("diagnostics = %+v, want repo failure", report.Diagnostics)
	}
}

func TestDoctorRepoModeUsesConfigRootFromSubdirectory(t *testing.T) {
	repo := chdirCLITestRepo(t, "repo-doctor-subdir")
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
		return doctorCommand([]string{"--repo", "--json"})
	})
	if err != nil {
		t.Fatalf("doctor --repo from subdir: %v\n%s", err, output)
	}
	var report doctorReport
	if decodeErr := json.Unmarshal([]byte(output), &report); decodeErr != nil {
		t.Fatalf("decode doctor report: %v\n%s", decodeErr, output)
	}
	if report.Repo == nil || report.Repo.Status == "" {
		t.Fatalf("doctor repo = %+v, want root repo map from subdir", report.Repo)
	}
}

func TestDoctorRepoModeDoesNotFailConfiglessRepoReferences(t *testing.T) {
	repo := chdirCLITestRepo(t, "repo-doctor-no-config")
	runGitForCLITest(t, "init")
	writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), `
READYCI ?= readyci

remote-check-ci:
	$(READYCI) check ci
`)
	output, err := captureStdoutForCLITest(t, func() error {
		return doctorCommand([]string{"--repo", "--json"})
	})
	if err != nil {
		t.Fatalf("doctor --repo configless: %v\n%s", err, output)
	}
	var report doctorReport
	if decodeErr := json.Unmarshal([]byte(output), &report); decodeErr != nil {
		t.Fatalf("decode doctor report: %v\n%s", decodeErr, output)
	}
	if !containsDoctorDiagnostic(report.Diagnostics, "repo", "warn") {
		t.Fatalf("diagnostics = %+v, want repo warning for missing committed config", report.Diagnostics)
	}
	if containsDoctorDiagnostic(report.Diagnostics, "repo", "fail") {
		t.Fatalf("diagnostics = %+v, should not fail config-less repo check references", report.Diagnostics)
	}
}

func TestRepoMapReportsUndefinedMakeReferencesFromDocs(t *testing.T) {
	repo := chdirCLITestRepo(t, "repo-map-missing-make")
	writeRepoMapFixture(t, repo)
	writeFileForRepoMapTest(t, filepath.Join(repo, "docs", "ci-cd.md"), "Use `make remote-old-db` for old DB checks.\n")
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
	cfg, configPath, err := config.LoadProjectConfig(repo)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	report := buildRepoMapReport(context.Background(), repo, cfg, configPath)
	if len(report.UndefinedMake) != 1 || report.UndefinedMake[0].Target != "remote-old-db" {
		t.Fatalf("undefined make = %+v, want remote-old-db", report.UndefinedMake)
	}
}

func TestRepoMapReportsUndefinedReferencesFromNestedAgentsFiles(t *testing.T) {
	repo := chdirCLITestRepo(t, "repo-map-nested-agents")
	writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), `
check-ci:
	@echo ci
`)
	writeFileForRepoMapTest(t, filepath.Join(repo, "cli", "AGENTS.md"), "Run `readyci check old-db` and `make check-db` for DB-sensitive CLI changes.\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, "node_modules", "pkg", "AGENTS.md"), "Ignore `readyci check vendored-old` here.\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, ".readyci.yml"), `
project_id: airline
checks:
  ci:
    command: make check-ci
`)
	cfg, configPath, err := config.LoadProjectConfig(repo)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	report := buildRepoMapReport(context.Background(), repo, cfg, configPath)
	if len(report.UndefinedChecks) != 1 || report.UndefinedChecks[0].Check != "old-db" {
		t.Fatalf("undefined checks = %+v, want old-db from nested AGENTS.md", report.UndefinedChecks)
	}
	if len(report.UndefinedChecks[0].References) != 1 || report.UndefinedChecks[0].References[0].Path != "cli/AGENTS.md" {
		t.Fatalf("undefined check references = %+v, want cli/AGENTS.md only", report.UndefinedChecks[0].References)
	}
	if len(report.UndefinedMake) != 1 || report.UndefinedMake[0].Target != "check-db" {
		t.Fatalf("undefined make = %+v, want check-db from nested AGENTS.md", report.UndefinedMake)
	}
	if len(report.UndefinedMake[0].References) != 1 || report.UndefinedMake[0].References[0].Path != "cli/AGENTS.md" {
		t.Fatalf("undefined make references = %+v, want cli/AGENTS.md only", report.UndefinedMake[0].References)
	}
}

func TestRepoMapReportsUndefinedReferencesFromTopLevelDocs(t *testing.T) {
	repo := chdirCLITestRepo(t, "repo-map-top-level-docs")
	writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), `
check-ci:
	@echo ci
`)
	writeFileForRepoMapTest(t, filepath.Join(repo, "README.md"), "Run `readyci check old-db` before merging DB changes.\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, "CONTRIBUTING.md"), "Use `make check-db` before opening a PR.\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, "NOTES.md"), "Do not scan `readyci check scratch-old` from arbitrary root notes.\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, ".readyci.yml"), `
project_id: airline
checks:
  ci:
    command: make check-ci
`)
	cfg, configPath, err := config.LoadProjectConfig(repo)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	report := buildRepoMapReport(context.Background(), repo, cfg, configPath)
	if len(report.UndefinedChecks) != 1 || report.UndefinedChecks[0].Check != "old-db" {
		t.Fatalf("undefined checks = %+v, want old-db from README.md only", report.UndefinedChecks)
	}
	if len(report.UndefinedChecks[0].References) != 1 || report.UndefinedChecks[0].References[0].Path != "README.md" {
		t.Fatalf("undefined check references = %+v, want README.md", report.UndefinedChecks[0].References)
	}
	if len(report.UndefinedMake) != 1 || report.UndefinedMake[0].Target != "check-db" {
		t.Fatalf("undefined make = %+v, want check-db from CONTRIBUTING.md", report.UndefinedMake)
	}
	if len(report.UndefinedMake[0].References) != 1 || report.UndefinedMake[0].References[0].Path != "CONTRIBUTING.md" {
		t.Fatalf("undefined make references = %+v, want CONTRIBUTING.md", report.UndefinedMake[0].References)
	}
}

func TestRepoMapIgnoresMarkedIllustrativeTopLevelDocs(t *testing.T) {
	repo := chdirCLITestRepo(t, "repo-map-ignored-top-level-docs")
	writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), `
check-ci:
	@echo ci
`)
	writeFileForRepoMapTest(t, filepath.Join(repo, "README.md"), `
<!-- readyci-doctor: ignore-start -->

`+"```bash"+`
readyci check old-db
make check-db
`+"```"+`

<!-- readyci-doctor: ignore-end -->

Run `+"`readyci check stale`"+` for real stale instructions.
`)
	writeFileForRepoMapTest(t, filepath.Join(repo, ".readyci.yml"), `
project_id: airline
checks:
  ci:
    command: make check-ci
`)
	cfg, configPath, err := config.LoadProjectConfig(repo)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	report := buildRepoMapReport(context.Background(), repo, cfg, configPath)
	if len(report.UndefinedChecks) != 1 || report.UndefinedChecks[0].Check != "stale" {
		t.Fatalf("undefined checks = %+v, want only stale outside ignored block", report.UndefinedChecks)
	}
	if len(report.UndefinedMake) != 0 {
		t.Fatalf("undefined make = %+v, ignored block should hide check-db", report.UndefinedMake)
	}
	if report.UndefinedChecks[0].References[0].Line == 0 {
		t.Fatalf("undefined check reference = %+v, want preserved line number", report.UndefinedChecks[0].References[0])
	}
}

func TestRepoMapReportsLaterUndefinedReadyCIChecksOnOneLine(t *testing.T) {
	repo := chdirCLITestRepo(t, "repo-map-one-line-readyci")
	writeFileForRepoMapTest(t, filepath.Join(repo, "docs", "ci-cd.md"), "Run `readyci check ci && readyci check old-db` before merging.\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, ".readyci.yml"), `
project_id: airline
checks:
  ci:
    command: make check-ci
`)
	cfg, configPath, err := config.LoadProjectConfig(repo)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	report := buildRepoMapReport(context.Background(), repo, cfg, configPath)
	if len(report.UndefinedChecks) != 1 || report.UndefinedChecks[0].Check != "old-db" {
		t.Fatalf("undefined checks = %+v, want old-db", report.UndefinedChecks)
	}
}

func TestRepoMapReportsLaterUndefinedMakeGoalsOnOneLine(t *testing.T) {
	repo := chdirCLITestRepo(t, "repo-map-one-line-make")
	writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), `
check-ci:
	@echo ci
`)
	writeFileForRepoMapTest(t, filepath.Join(repo, "docs", "ci-cd.md"), "Run `make check-ci check-db` before merging DB changes.\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, ".readyci.yml"), `
project_id: airline
checks:
  ci:
    command: make check-ci
`)
	cfg, configPath, err := config.LoadProjectConfig(repo)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	report := buildRepoMapReport(context.Background(), repo, cfg, configPath)
	if len(report.UndefinedMake) != 1 || report.UndefinedMake[0].Target != "check-db" {
		t.Fatalf("undefined make = %+v, want check-db", report.UndefinedMake)
	}
}

func TestRepoMapReportsEnvPrefixedCommandReferences(t *testing.T) {
	repo := chdirCLITestRepo(t, "repo-map-env-prefixed")
	writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), `
check-ci:
	READYCI_PROJECT_ID=readyci readyci check old-db
`)
	writeFileForRepoMapTest(t, filepath.Join(repo, ".github", "workflows", "ci.yml"), `
name: ci
on: [push]
jobs:
  check:
    runs-on: ubuntu-latest
    steps:
      - run: FOO=bar make check-db
`)
	writeFileForRepoMapTest(t, filepath.Join(repo, ".readyci.yml"), `
project_id: airline
checks:
  ci:
    command: make check-ci
`)
	cfg, configPath, err := config.LoadProjectConfig(repo)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	report := buildRepoMapReport(context.Background(), repo, cfg, configPath)
	if len(report.UndefinedChecks) != 1 || report.UndefinedChecks[0].Check != "old-db" {
		t.Fatalf("undefined checks = %+v, want old-db", report.UndefinedChecks)
	}
	if len(report.UndefinedMake) != 1 || report.UndefinedMake[0].Target != "check-db" {
		t.Fatalf("undefined make = %+v, want check-db", report.UndefinedMake)
	}
}

func TestRepoMapReportsUndefinedDirectoryMakeReferencesFromDocs(t *testing.T) {
	repo := chdirCLITestRepo(t, "repo-map-missing-directory-make")
	writeFileForRepoMapTest(t, filepath.Join(repo, "cli", "Makefile"), `
check:
	@echo check
`)
	writeFileForRepoMapTest(t, filepath.Join(repo, "docs", "ci-cd.md"), "Use `make -C cli check-db` for DB checks.\nUse `make --directory=cli check-db` before merging DB changes.\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, ".readyci.yml"), `
project_id: airline
checks:
  ci:
    command: make check-ci
`)
	cfg, configPath, err := config.LoadProjectConfig(repo)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	report := buildRepoMapReport(context.Background(), repo, cfg, configPath)
	if len(report.UndefinedMake) != 1 || report.UndefinedMake[0].Target != "cli:check-db" {
		t.Fatalf("undefined make = %+v, want cli:check-db", report.UndefinedMake)
	}
	if len(report.UndefinedMake[0].References) != 2 {
		t.Fatalf("undefined make references = %+v, want both documented directory invocations", report.UndefinedMake[0].References)
	}
}

func TestRepoMapIgnoresArchivedDocReferencesForDrift(t *testing.T) {
	repo := chdirCLITestRepo(t, "repo-map-archive-docs")
	writeRepoMapFixture(t, repo)
	writeFileForRepoMapTest(t, filepath.Join(repo, "docs", "archive", "ci-old.md"), "Old flow used `readyci check old-db` and `make remote-old-db`.\n")
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
	cfg, configPath, err := config.LoadProjectConfig(repo)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	report := buildRepoMapReport(context.Background(), repo, cfg, configPath)
	for _, undefined := range report.UndefinedChecks {
		if undefined.Check == "old-db" {
			t.Fatalf("undefined checks = %+v, should ignore archive docs", report.UndefinedChecks)
		}
	}
	for _, undefined := range report.UndefinedMake {
		if undefined.Target == "remote-old-db" {
			t.Fatalf("undefined make = %+v, should ignore archive docs", report.UndefinedMake)
		}
	}
}

func TestRepoMapDoesNotTreatGitHubActionsMakeTargetsAsReadyCIChecks(t *testing.T) {
	repo := chdirCLITestRepo(t, "repo-map-gha-make")
	writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), `
release-check-extra:
	@echo release
`)
	writeFileForRepoMapTest(t, filepath.Join(repo, ".github", "workflows", "ci.yml"), `
name: ci
on: [push]
jobs:
  release-check-extra:
    runs-on: ubuntu-latest
    steps:
      - run: make release-check-extra
`)
	writeFileForRepoMapTest(t, filepath.Join(repo, ".readyci.yml"), `
project_id: airline
checks:
  ci:
    command: make check-ci
`)
	cfg, configPath, err := config.LoadProjectConfig(repo)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	report := buildRepoMapReport(context.Background(), repo, cfg, configPath)
	for _, undefined := range report.UndefinedChecks {
		if undefined.Check == "release-check-extra" {
			t.Fatalf("undefined checks = %+v, should not infer Ready CI check from GitHub Actions make target", report.UndefinedChecks)
		}
	}
	if len(report.UndefinedMake) != 0 {
		t.Fatalf("undefined make = %+v, want existing release-check-extra target accepted", report.UndefinedMake)
	}
}

func TestRepoMapRecognizesMultiTargetMakeRules(t *testing.T) {
	repo := chdirCLITestRepo(t, "repo-map-multi-target-make")
	writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), `
check lint:
	@echo ok
`)
	writeFileForRepoMapTest(t, filepath.Join(repo, "docs", "ci-cd.md"), "Run `make check lint` before merging.\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, ".readyci.yml"), `
project_id: airline
checks:
  ci:
    command: make check
`)
	cfg, configPath, err := config.LoadProjectConfig(repo)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	report := buildRepoMapReport(context.Background(), repo, cfg, configPath)
	if len(report.UndefinedMake) != 0 {
		t.Fatalf("undefined make = %+v, want multi-target rule to define check and lint", report.UndefinedMake)
	}
}

func TestRepoMapValidatesGitHubActionsMakeDirectoryTargets(t *testing.T) {
	repo := chdirCLITestRepo(t, "repo-map-gha-make-directory")
	writeFileForRepoMapTest(t, filepath.Join(repo, "cli", "Makefile"), `
check:
	@echo check
`)
	writeFileForRepoMapTest(t, filepath.Join(repo, ".github", "workflows", "ci.yml"), `
name: ci
on: [push]
jobs:
  cli:
    runs-on: ubuntu-latest
    steps:
      - run: make -C cli check
      - run: make --directory=cli check
`)
	writeFileForRepoMapTest(t, filepath.Join(repo, ".readyci.yml"), `
project_id: airline
checks:
  ci:
    command: make check-ci
`)
	cfg, configPath, err := config.LoadProjectConfig(repo)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	report := buildRepoMapReport(context.Background(), repo, cfg, configPath)
	if len(report.UndefinedMake) != 0 {
		t.Fatalf("undefined make = %+v, want cli:check resolved from cli/Makefile", report.UndefinedMake)
	}
}

func TestRepoMapValidatesGitHubActionsMakeWorkingDirectoryTargets(t *testing.T) {
	repo := chdirCLITestRepo(t, "repo-map-gha-make-working-directory")
	writeFileForRepoMapTest(t, filepath.Join(repo, "cli", "Makefile"), `
check:
	@echo check
test:
	@echo test
lint:
	@echo lint
`)
	writeFileForRepoMapTest(t, filepath.Join(repo, ".github", "workflows", "ci.yml"), `
name: ci
on: [push]
defaults:
  run:
    working-directory: cli
jobs:
  workflow-default:
    runs-on: ubuntu-latest
    steps:
      - run: make check
  job-default:
    runs-on: ubuntu-latest
    defaults:
      run:
        working-directory: cli
    steps:
      - run: make test
  step-default:
    runs-on: ubuntu-latest
    steps:
      - run: make lint
        working-directory: cli
`)
	writeFileForRepoMapTest(t, filepath.Join(repo, ".readyci.yml"), `
project_id: airline
checks:
  ci:
    command: make check-ci
`)
	cfg, configPath, err := config.LoadProjectConfig(repo)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	report := buildRepoMapReport(context.Background(), repo, cfg, configPath)
	if len(report.UndefinedMake) != 0 {
		t.Fatalf("undefined make = %+v, want working-directory targets resolved from cli/Makefile", report.UndefinedMake)
	}
}
