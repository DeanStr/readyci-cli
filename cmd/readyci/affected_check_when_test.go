package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

func TestCheckWhenChangesCompilesIntoAffectedRules(t *testing.T) {
	cfg := config.ProjectConfig{
		Checks: map[string]config.CheckConfig{
			"go": {
				Command: "go test ./...",
				When: config.WhenConfig{Changes: config.WhenChangesConfig{
					Include: []string{"**/*.go"},
					Exclude: []string{"vendor/**"},
				}},
			},
			"docs": {Command: "make docs"},
		},
	}
	if !shouldUseAffectedRules(cfg, "") {
		t.Fatal("check when.changes did not enable affected rules")
	}
	compiled := projectConfigWithCheckWhenRules(cfg)
	selected, reasons, err := selectChecksForAffectedRules(compiled, map[string][]string{
		"go": {"main.go", "vendor/skip.go"},
	})
	if err != nil {
		t.Fatalf("select checks: %v", err)
	}
	if len(selected) != 1 || selected[0] != "go" {
		t.Fatalf("selected checks = %v, want go", selected)
	}
	if len(reasons["go"]) != 1 || reasons["go"][0].Path != "main.go" {
		t.Fatalf("reasons = %+v, want main.go only", reasons["go"])
	}
}

func TestCheckWhenChangesCompilationIsIdempotent(t *testing.T) {
	cfg := config.ProjectConfig{
		Checks: map[string]config.CheckConfig{
			"go": {
				Command: "go test ./...",
				When: config.WhenConfig{Changes: config.WhenChangesConfig{
					Include: []string{"**/*.go"},
					Exclude: []string{"vendor/**"},
				}},
			},
		},
	}

	compiled := projectConfigWithCheckWhenRules(projectConfigWithCheckWhenRules(cfg))
	selected, reasons, err := selectChecksForAffectedRules(compiled, map[string][]string{
		"go": {"main.go"},
	})
	if err != nil {
		t.Fatalf("select checks: %v", err)
	}
	if len(selected) != 1 || selected[0] != "go" {
		t.Fatalf("selected checks = %v, want go", selected)
	}
	if got := len(reasons["go"]); got != 1 {
		t.Fatalf("reason count = %d, want 1: %+v", got, reasons["go"])
	}
}

func TestCheckWhenTypedConditionsFilterAffectedSelection(t *testing.T) {
	t.Setenv("GITHUB_EVENT_NAME", "push")
	t.Setenv("GITHUB_REF", "refs/heads/main")
	t.Setenv("READYCI_E2E_WEBKIT", "1")
	cfg := projectConfigWithCheckWhenRules(config.ProjectConfig{
		Checks: map[string]config.CheckConfig{
			"web": {
				Command: "pnpm test",
				When: config.WhenConfig{
					Event:  []string{"push", "pull_request"},
					Branch: []string{"main"},
					Env:    map[string]string{"READYCI_E2E_WEBKIT": "1"},
					Status: "success",
					Changes: config.WhenChangesConfig{
						Include: []string{"web/**"},
					},
				},
			},
		},
	})

	selected, _, skipped, err := selectChecksForAffectedRulesWithSkipped(cfg, map[string][]string{
		"web": {"web/app.ts"},
	})
	if err != nil {
		t.Fatalf("select checks: %v", err)
	}
	if len(selected) != 1 || selected[0] != "web" || len(skipped) != 0 {
		t.Fatalf("selected=%v skipped=%+v, want web selected", selected, skipped)
	}

	t.Setenv("GITHUB_EVENT_NAME", "schedule")
	selected, _, skipped, err = selectChecksForAffectedRulesWithSkipped(cfg, map[string][]string{
		"web": {"web/app.ts"},
	})
	if err != nil {
		t.Fatalf("select checks after event mismatch: %v", err)
	}
	if len(selected) != 0 || len(skipped) != 1 || skipped[0].Name != "web" || skipped[0].Reason != "when.event" || strings.Join(skipped[0].MatchedPaths, ",") != "web/app.ts" {
		t.Fatalf("selected=%v skipped=%+v, want web skipped by when.event with matched path", selected, skipped)
	}

	t.Setenv("GITHUB_EVENT_NAME", "push")
	t.Setenv("READYCI_STATUS", "failure")
	selected, _, skipped, err = selectChecksForAffectedRulesWithSkipped(cfg, map[string][]string{
		"web": {"web/app.ts"},
	})
	if err != nil {
		t.Fatalf("select checks after status mismatch: %v", err)
	}
	if len(selected) != 0 || len(skipped) != 1 || skipped[0].Name != "web" || skipped[0].Reason != "when.status" || strings.Join(skipped[0].MatchedPaths, ",") != "web/app.ts" {
		t.Fatalf("selected=%v skipped=%+v, want web skipped by when.status with matched path", selected, skipped)
	}
}

func TestCheckWhenBranchAndTagConditionsMatchCurrentRefAlternative(t *testing.T) {
	t.Setenv("GITHUB_EVENT_NAME", "push")
	t.Setenv("GITHUB_REF", "refs/tags/v1.2.3")
	cfg := projectConfigWithCheckWhenRules(config.ProjectConfig{
		Checks: map[string]config.CheckConfig{
			"release": {
				Command: "make release",
				When: config.WhenConfig{
					Event:  []string{"push"},
					Branch: []string{"main"},
					Tag:    "v*",
					Changes: config.WhenChangesConfig{
						Include: []string{"cli/**"},
					},
				},
			},
		},
	})

	selected, _, skipped, err := selectChecksForAffectedRulesWithSkipped(cfg, map[string][]string{
		"release": {"cli/main.go"},
	})
	if err != nil {
		t.Fatalf("select checks: %v", err)
	}
	if len(selected) != 1 || selected[0] != "release" || len(skipped) != 0 {
		t.Fatalf("selected=%v skipped=%+v, want tag glob to select release", selected, skipped)
	}
}

func TestCurrentBranchFromEnvUsesPullRequestBaseBranch(t *testing.T) {
	t.Setenv("GITHUB_EVENT_NAME", "pull_request")
	t.Setenv("GITHUB_HEAD_REF", "feature/new-check")
	t.Setenv("GITHUB_BASE_REF", "main")

	if got := currentBranchFromEnv(); got != "main" {
		t.Fatalf("currentBranchFromEnv() = %q, want pull request base branch main", got)
	}
}

func TestCurrentBranchFromEnvAllowsReadyCIBranchOverride(t *testing.T) {
	t.Setenv("READYCI_BRANCH", "release/1.2")
	t.Setenv("GITHUB_HEAD_REF", "feature/new-check")
	t.Setenv("GITHUB_BASE_REF", "main")

	if got := currentBranchFromEnv(); got != "release/1.2" {
		t.Fatalf("currentBranchFromEnv() = %q, want READYCI_BRANCH override", got)
	}
}

func TestAffectedExplainUsesCheckWhenChanges(t *testing.T) {
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
	if err := os.MkdirAll(filepath.Join(repo, "app"), 0o755); err != nil {
		t.Fatalf("mkdir app: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "app", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
project_id: airline
checks:
  go:
    command: go test ./...
    when:
      changes:
        include:
          - app/**/*.go
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")

	output, err := captureStdoutForCLITest(t, func() error {
		return affectedExplainCommand([]string{"--json", "app/main.go"})
	})
	if err != nil {
		t.Fatalf("affected explain: %v\n%s", err, output)
	}
	var report affectedExplainReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode explain report: %v\n%s", err, output)
	}
	if len(report.Paths) != 1 || !report.Paths[0].Covered {
		t.Fatalf("explain paths = %+v, want covered path", report.Paths)
	}
	if got := strings.Join(report.Paths[0].Checks, ","); got != "go" {
		t.Fatalf("checks = %q, want go from check when.changes", got)
	}
}
