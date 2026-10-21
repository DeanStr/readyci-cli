package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

func TestImportMakeReportsWrapperCandidates(t *testing.T) {
	repo := chdirCLITestRepo(t, "repo-import-make")
	writeRepoMapFixture(t, repo)
	writeFileForRepoMapTest(t, filepath.Join(repo, ".readyci.yml"), `
project_id: airline
checks:
  ci:
    command: make check-ci
  db:
    command: make check-ci-db-parallel
`)
	output, err := captureStdoutForCLITest(t, func() error {
		return importCommand([]string{"make", "--json"})
	})
	if err != nil {
		t.Fatalf("import make: %v\n%s", err, output)
	}
	var report repoImportReport
	if decodeErr := json.Unmarshal([]byte(output), &report); decodeErr != nil {
		t.Fatalf("decode import report: %v\n%s", decodeErr, output)
	}
	if report.Source != "make" || !importReportHasCheck(report, "ci") || !importReportHasCheck(report, "db") {
		t.Fatalf("import report = %+v, want ci and db Make candidates", report)
	}
	if report.Mode != "preview" {
		t.Fatalf("import report mode = %q, want preview", report.Mode)
	}
	if !importReportHasNameContaining(report, "target remote-check-ci:") {
		t.Fatalf("import report = %+v, want Make candidate named with enclosing target", report)
	}
}

func TestImportMakeReportsOrdinaryVerificationTargets(t *testing.T) {
	repo := chdirCLITestRepo(t, "repo-import-make-ordinary")
	writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), `
check-ci:
	go test ./...
lint:
	golangci-lint run
bootstrap:
	echo setup
`)
	writeFileForRepoMapTest(t, filepath.Join(repo, "cli", "Makefile"), `
check:
	go test ./...
`)
	writeFileForRepoMapTest(t, filepath.Join(repo, ".readyci.yml"), `
project_id: airline
checks:
  ci:
    command: make check-ci
`)
	output, err := captureStdoutForCLITest(t, func() error {
		return importCommand([]string{"make", "--json"})
	})
	if err != nil {
		t.Fatalf("import make: %v\n%s", err, output)
	}
	var report repoImportReport
	if decodeErr := json.Unmarshal([]byte(output), &report); decodeErr != nil {
		t.Fatalf("decode import report: %v\n%s", decodeErr, output)
	}
	if !importReportHasCheck(report, "ci") || !importReportHasCheck(report, "lint") || !importReportHasCheck(report, "cli") {
		t.Fatalf("import report = %+v, want ordinary Make verification candidates for ci, lint, and cli", report)
	}
	if importReportHasCheck(report, "bootstrap") {
		t.Fatalf("import report = %+v, should not import non-verification bootstrap target", report)
	}
	if !importReportHasCommand(report, "make -C cli -f Makefile -- check") {
		t.Fatalf("import report = %+v, want subdirectory Make command", report)
	}
}

func TestImportGitHubActionsReportsJobCandidates(t *testing.T) {
	repo := chdirCLITestRepo(t, "repo-import-github-actions")
	writeRepoMapFixture(t, repo)
	writeFileForRepoMapTest(t, filepath.Join(repo, ".github", "workflows", "deploy-api.yml"), `
name: deploy api
on: [push]
jobs:
  deploy-api:
    runs-on: ubuntu-latest
    steps:
      - run: sudo -n --preserve-env=SS_DEPLOY_GIT_SHA /usr/local/sbin/readyci-api-deploy
`)
	writeFileForRepoMapTest(t, filepath.Join(repo, ".github", "workflows", "release-cli.yml"), `
name: release cli
on: [workflow_dispatch]
jobs:
  build-release:
    runs-on: ubuntu-latest
    steps:
      - run: |
          set -euo pipefail
          if [ -z "${READYCI_RELEASE_MINISIGN_KEY_BASE64:-}" ]; then
            echo "release signing key is required"
            exit 1
          fi
          cli/scripts/build-release-artifacts.sh
  release-cli:
    runs-on: ubuntu-latest
    steps:
      - run: gh release create "$READYCI_WORKFLOW_RELEASE_VERSION" dist/* --draft
`)
	writeFileForRepoMapTest(t, filepath.Join(repo, ".readyci.yml"), `
project_id: airline
checks:
  ci:
    command: make check-ci
`)
	output, err := captureStdoutForCLITest(t, func() error {
		return importCommand([]string{"github-actions", "--json"})
	})
	if err != nil {
		t.Fatalf("import github-actions: %v\n%s", err, output)
	}
	var report repoImportReport
	if decodeErr := json.Unmarshal([]byte(output), &report); decodeErr != nil {
		t.Fatalf("decode import report: %v\n%s", decodeErr, output)
	}
	if report.Source != "github-actions" || !importReportHasName(report, "check-ci") || !importReportHasName(report, "check-db") {
		t.Fatalf("import report = %+v, want check-ci and check-db GitHub Actions candidates", report)
	}
	if importReportHasName(report, "deploy-api") || importReportHasName(report, "build-release") || importReportHasName(report, "release-cli") {
		t.Fatalf("import report = %+v, should not suggest deployment or publishing jobs as checks", report)
	}
}

func TestRepoMapParsesMultilineGitHubActionsMakeCommands(t *testing.T) {
	repo := chdirCLITestRepo(t, "repo-map-gha-multiline-make")
	writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), `
check-ci:
	@echo ci
lint:
	@echo lint
`)
	writeFileForRepoMapTest(t, filepath.Join(repo, ".github", "workflows", "ci.yml"), `
name: ci
on: [push]
jobs:
  check:
    runs-on: ubuntu-latest
    steps:
      - run: |
          echo setup
          make missing-check
      - run: |
          make check-ci
          make lint
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
	if len(report.UndefinedMake) != 1 || report.UndefinedMake[0].Target != "missing-check" {
		t.Fatalf("undefined make = %+v, want only missing-check from multiline workflow block", report.UndefinedMake)
	}
}

func TestRepoMapIgnoresGitHubActionsMakeRedirections(t *testing.T) {
	repo := chdirCLITestRepo(t, "repo-map-gha-make-redirection")
	writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), `
check-ci:
	@echo ci
lint:
	@echo lint
`)
	writeFileForRepoMapTest(t, filepath.Join(repo, ".github", "workflows", "ci.yml"), `
name: ci
on: [push]
jobs:
  check:
    runs-on: ubuntu-latest
    steps:
      - run: make check-ci > ci.log
      - run: make lint 2>&1
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
		t.Fatalf("undefined make = %+v, redirection operands should not be Make targets", report.UndefinedMake)
	}
}

func TestRepoMapParsesGitHubActionsCommentsAndBackgroundMake(t *testing.T) {
	repo := chdirCLITestRepo(t, "repo-map-gha-comments-background")
	writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), `
serve:
	@echo serve
test:
	@echo test
`)
	writeFileForRepoMapTest(t, filepath.Join(repo, ".github", "workflows", "ci.yml"), `
name: ci
on: [push]
jobs:
  check:
    runs-on: ubuntu-latest
    steps:
      - run: |
          # setup before stale check
          make missing-check
      - run: make serve & make test
`)
	writeFileForRepoMapTest(t, filepath.Join(repo, ".readyci.yml"), `
project_id: airline
checks:
  ci:
    command: make test
`)
	cfg, configPath, err := config.LoadProjectConfig(repo)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	report := buildRepoMapReport(context.Background(), repo, cfg, configPath)
	if len(report.UndefinedMake) != 1 || report.UndefinedMake[0].Target != "missing-check" {
		t.Fatalf("undefined make = %+v, want only missing-check after workflow comment", report.UndefinedMake)
	}
}

func TestRepoMapParsesGitHubActionsReadyCILineContinuations(t *testing.T) {
	repo := chdirCLITestRepo(t, "repo-map-gha-readyci-continuation")
	writeFileForRepoMapTest(t, filepath.Join(repo, ".github", "workflows", "ci.yml"), `
name: ci
on: [push]
jobs:
  check:
    runs-on: ubuntu-latest
    steps:
      - run: |
          readyci check --region fsn1 \
            old-db
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
		t.Fatalf("undefined checks = %+v, want old-db from continued workflow command", report.UndefinedChecks)
	}
}

func TestRepoMapReadsGitHubActionsBlockRunCommands(t *testing.T) {
	repo := chdirCLITestRepo(t, "repo-map-gha-block-run")
	writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), `
check-ci:
	@echo ci
`)
	writeFileForRepoMapTest(t, filepath.Join(repo, ".github", "workflows", "ci.yml"), `
name: ci
on: [push]
jobs:
  check-ci:
    runs-on: ubuntu-latest
    steps:
      - run: |
          echo setup
          readyci check ci
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
	found := false
	for _, ref := range report.CheckReferences {
		if ref.Source == "github-actions" && ref.Check == "ci" {
			found = true
		}
	}
	if !found {
		t.Fatalf("check references = %+v, want github-actions readyci check ci from block run", report.CheckReferences)
	}
}

func TestRepoMapReportsGitHubActionsCheckReferenceWorkflowLines(t *testing.T) {
	repo := chdirCLITestRepo(t, "repo-map-gha-reference-lines")
	workflow := `
name: ci
on: [push]
jobs:
  inline:
    runs-on: ubuntu-latest
    steps:
      - run: readyci check old-inline
  block:
    runs-on: ubuntu-latest
    steps:
      - run: |
          echo setup
          readyci check old-block
`
	writeFileForRepoMapTest(t, filepath.Join(repo, ".github", "workflows", "ci.yml"), workflow)
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
	lines := map[string]int{}
	for _, undefined := range report.UndefinedChecks {
		for _, ref := range undefined.References {
			lines[undefined.Check] = ref.Line
		}
	}
	if lines["old-inline"] != lineContaining(workflow, "readyci check old-inline") {
		t.Fatalf("old-inline line = %d, want workflow line %d; refs=%+v", lines["old-inline"], lineContaining(workflow, "readyci check old-inline"), report.UndefinedChecks)
	}
	if lines["old-block"] != lineContaining(workflow, "readyci check old-block") {
		t.Fatalf("old-block line = %d, want workflow line %d; refs=%+v", lines["old-block"], lineContaining(workflow, "readyci check old-block"), report.UndefinedChecks)
	}
}

func TestRepoMapIgnoresCommentedReadyCICommands(t *testing.T) {
	repo := chdirCLITestRepo(t, "repo-map-comments")
	writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), `
check-ci:
	@echo ci

# readyci check old-db
`)
	writeFileForRepoMapTest(t, filepath.Join(repo, ".github", "workflows", "ci.yml"), `
name: ci
on: [push]
jobs:
  check-ci:
    runs-on: ubuntu-latest
    steps:
      - run: |
          # readyci check old-db
          readyci check ci
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
		if undefined.Check == "old-db" {
			t.Fatalf("undefined checks = %+v, should ignore commented commands", report.UndefinedChecks)
		}
	}
}

func TestRepoMapIgnoresCommentedGitHubActionsRunSteps(t *testing.T) {
	repo := chdirCLITestRepo(t, "repo-map-commented-gha-run")
	writeFileForRepoMapTest(t, filepath.Join(repo, ".github", "workflows", "ci.yml"), `
name: ci
on: [push]
jobs:
  check-ci:
    runs-on: ubuntu-latest
    steps:
      # - run: readyci check old-db
      - run: readyci check ci
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
		if undefined.Check == "old-db" {
			t.Fatalf("undefined checks = %+v, should ignore commented workflow run step", report.UndefinedChecks)
		}
	}
}

func TestRepoMapResolvesStaticAndIgnoresDynamicGitHubActionsExpressions(t *testing.T) {
	repo := chdirCLITestRepo(t, "repo-map-gha-expressions")
	writeFileForRepoMapTest(t, filepath.Join(repo, ".github", "workflows", "ci.yml"), `
name: ci
on: [push]
jobs:
  check:
    strategy:
      matrix:
        check: [ci]
        target: [check-ci]
        dir: [cli]
    runs-on: ubuntu-latest
    steps:
      - run: readyci check ${{ matrix.check }}
      - run: make ${{ matrix.target }}
      - run: make -C ${{ matrix.dir }} check
      - run: readyci check ${{ inputs.check }}
      - run: make ${{ inputs.target }}
      - run: make -C ${{ vars.dir }} check
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
	if len(report.UndefinedChecks) != 0 {
		t.Fatalf("undefined checks = %+v, want dynamic GitHub Actions check expression ignored", report.UndefinedChecks)
	}
	if len(report.UndefinedMake) != 2 || report.UndefinedMake[0].Target != "check-ci" || report.UndefinedMake[1].Target != "cli:check" {
		t.Fatalf("undefined make = %+v, want concrete matrix Make references and no dynamic provider references", report.UndefinedMake)
	}
}
