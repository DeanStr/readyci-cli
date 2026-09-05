package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func TestAffectedWatchFingerprintPreservesPerCheckExclusions(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
affected:
  rules:
    - paths:
        - shared.txt
      checks:
        - excluded
        - included
checks:
  excluded:
    command: make excluded
    sync:
      exclude:
        - shared.txt
  included:
    command: make included
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "shared.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write shared: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	cfg, _, err := config.LoadProjectConfig(repo)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	cleanFingerprint, err := affectedWorkspaceFingerprint(context.Background(), repo, cfg, checkResolveOptions{})
	if err != nil {
		t.Fatalf("clean affected watch fingerprint: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "shared.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatalf("update shared: %v", err)
	}
	changedFingerprint, err := affectedWorkspaceFingerprint(context.Background(), repo, cfg, checkResolveOptions{})
	if err != nil {
		t.Fatalf("changed affected watch fingerprint: %v", err)
	}
	if changedFingerprint == cleanFingerprint {
		t.Fatal("affected watch fingerprint did not change for file included by one check but excluded by another")
	}
}

func TestAffectedWatchFingerprintIncludesUnmatchedCheckFilters(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
affected:
  unmatched_checks: [ci]
  rules:
    - paths: ["**/*.go"]
      checks: [go]
checks:
  go:
    command: go test ./...
    sync:
      exclude: [tooling/**]
  ci:
    command: make check
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "tooling"), 0o755); err != nil {
		t.Fatalf("mkdir tooling: %v", err)
	}
	path := filepath.Join(repo, "tooling", "unknown.conf")
	if err := os.WriteFile(path, []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write unmatched file: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	cfg, _, err := config.LoadProjectConfig(repo)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	cleanFingerprint, err := affectedWorkspaceFingerprint(context.Background(), repo, cfg, checkResolveOptions{})
	if err != nil {
		t.Fatalf("clean affected watch fingerprint: %v", err)
	}
	if err := os.WriteFile(path, []byte("changed\n"), 0o644); err != nil {
		t.Fatalf("update unmatched file: %v", err)
	}
	changedFingerprint, err := affectedWorkspaceFingerprint(context.Background(), repo, cfg, checkResolveOptions{})
	if err != nil {
		t.Fatalf("changed affected watch fingerprint: %v", err)
	}
	if changedFingerprint == cleanFingerprint {
		t.Fatal("affected watch fingerprint did not change for file visible only to unmatched check")
	}
}

func TestAffectedRulesStagedIgnoresUntrackedMatches(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
sync:
  include_untracked: true
affected:
  rules:
    - paths: ["docs/**"]
      checks: docs-lint
    - paths: ["apps/web/src/**"]
      checks: web-test
checks:
  docs-lint:
    command: make docs-lint
  web-test:
    command: pnpm test
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("readyci\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")

	if err := os.MkdirAll(filepath.Join(repo, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "docs", "guide.md"), []byte("# Guide\n"), 0o644); err != nil {
		t.Fatalf("write docs guide: %v", err)
	}
	runGitForCLITest(t, "add", "docs/guide.md")
	if err := os.MkdirAll(filepath.Join(repo, "apps", "web", "src"), 0o755); err != nil {
		t.Fatalf("mkdir web src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "apps", "web", "src", "Scratch.tsx"), []byte("export {}\n"), 0o644); err != nil {
		t.Fatalf("write untracked web file: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return affectedCommand([]string{"--staged", "--dry-run", "--json"})
	})
	if err != nil {
		t.Fatalf("affected staged dry-run: %v", err)
	}
	var report affectedDryRunReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode affected report: %v\n%s", err, output)
	}
	var got []string
	for _, check := range report.SelectedChecks {
		got = append(got, check.Name)
	}
	if strings.Join(got, ",") != "docs-lint" {
		t.Fatalf("selected staged checks = %v, want docs-lint only", got)
	}
	if report.Sync.IncludeUntracked {
		t.Fatalf("staged affected sync include_untracked = true, want false")
	}
	if contains(report.ChangedFiles, "apps/web/src/Scratch.tsx") {
		t.Fatalf("staged affected changed files included untracked web file: %v", report.ChangedFiles)
	}
}

func TestTouchedCommandIgnoresAffectedRules(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("readyci\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
affected:
  rules:
    - paths: ["**"]
      checks: web
checks:
  touched:
    command: make check-touched
  staged:
    command: make check-staged
    changes: staged
  web:
    command: make web
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("updated\n"), 0o644); err != nil {
		t.Fatalf("update readme: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return touchedCommand([]string{"--dry-run", "--json"})
	})
	if err != nil {
		t.Fatalf("touched dry-run: %v", err)
	}
	var report checkDryRunReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode touched report: %v\n%s", err, output)
	}
	if report.Check != "touched" || report.Command != "make check-touched" {
		t.Fatalf("touched selected %+v, want legacy touched check", report)
	}

	output, err = captureStdoutForCLITest(t, func() error {
		return touchedCommand([]string{"--staged", "--dry-run", "--json"})
	})
	if err != nil {
		t.Fatalf("touched staged dry-run: %v", err)
	}
	report = checkDryRunReport{}
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode staged touched report: %v\n%s", err, output)
	}
	if report.Check != "staged" || report.ChangeMode != syncbundle.ChangeModeStaged {
		t.Fatalf("touched --staged selected check %q change %q, want staged/staged", report.Check, report.ChangeMode)
	}
}

func TestAffectedRulesNoMatchDoesNotRequireToken(t *testing.T) {
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
	t.Setenv("READYCI_CONFIG_DIR", t.TempDir())
	runGitForCLITest(t, "init")
	runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("readyci\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
affected:
  rules:
    - paths:
        - apps/web/**
      checks: web
checks:
  web:
    command: make web
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("updated\n"), 0o644); err != nil {
		t.Fatalf("update readme: %v", err)
	}

	stderr, err := captureStderrForCLITest(t, func() error {
		return affectedCommand(nil)
	})
	if err != nil {
		t.Fatalf("affected no-match error = %v", err)
	}
	if !strings.Contains(stderr, "no affected checks selected") {
		t.Fatalf("stderr = %q, want no affected checks selected", stderr)
	}
}

func TestAffectedRulesExcludeAndSubsumesPruneChecks(t *testing.T) {
	cfg := config.ProjectConfig{
		Checks: map[string]config.CheckConfig{
			"web":           {Command: "pnpm web-check"},
			"web-typecheck": {Command: "pnpm typecheck"},
			"web-test":      {Command: "pnpm test"},
			"docs-lint":     {Command: "make docs-lint"},
		},
		Affected: config.AffectedConfig{
			Rules: []config.AffectedRule{
				{
					Paths:   []string{"apps/web/**"},
					Exclude: []string{"apps/web/generated/**"},
					Checks:  config.CommandList{"web"},
				},
				{
					Paths:  []string{"apps/web/src/**"},
					Checks: config.CommandList{"web-typecheck", "web-test"},
				},
				{
					Paths:  []string{"docs/**"},
					Checks: config.CommandList{"docs-lint"},
				},
			},
			Subsumes: map[string]config.CommandList{
				"web": {"web-typecheck", "web-test"},
			},
		},
	}
	changed := map[string][]string{
		"web":           {"apps/web/src/App.tsx", "apps/web/generated/client.ts"},
		"web-typecheck": {"apps/web/src/App.tsx", "apps/web/generated/client.ts"},
		"web-test":      {"apps/web/src/App.tsx"},
		"docs-lint":     {"docs/guide.md"},
	}
	selected, reasons, skipped, err := selectChecksForAffectedRulesWithSkipped(cfg, changed)
	if err != nil {
		t.Fatalf("select checks: %v", err)
	}
	if strings.Join(selected, ",") != "web,docs-lint" {
		t.Fatalf("selected = %v, want web and docs-lint after pruning", selected)
	}
	if _, ok := reasons["web-typecheck"]; ok {
		t.Fatalf("web-typecheck reasons should be pruned: %+v", reasons["web-typecheck"])
	}
	if len(reasons["web"]) != 1 || reasons["web"][0].Path != "apps/web/src/App.tsx" {
		t.Fatalf("web reasons = %+v, want only non-excluded source path", reasons["web"])
	}
	if len(skipped) != 2 {
		t.Fatalf("skipped checks = %+v, want web-typecheck and web-test", skipped)
	}
	skippedByName := make(map[string]affectedSkippedCheck, len(skipped))
	for _, check := range skipped {
		skippedByName[check.Name] = check
	}
	for _, name := range []string{"web-typecheck", "web-test"} {
		check, ok := skippedByName[name]
		if !ok {
			t.Fatalf("skipped checks = %+v, missing %s", skipped, name)
		}
		if check.Status != "skipped" || check.Reason != "subsumed" || strings.Join(check.SubsumedBy, ",") != "web" {
			t.Fatalf("skipped %s = %+v, want skipped/subsumed by web", name, check)
		}
		if len(check.MatchedPaths) != 1 || check.MatchedPaths[0] != "apps/web/src/App.tsx" {
			t.Fatalf("skipped %s matched paths = %+v, want source path", name, check.MatchedPaths)
		}
	}
}

func TestAgentAffectedChecksIncludeSubsumedSkippedChecks(t *testing.T) {
	report := affectedDryRunReport{
		SelectedChecks: []affectedDryRunCheck{{
			Name: "web",
			Reasons: []affectedRuleMatch{{
				Rule:    1,
				Pattern: "apps/web/**",
				Path:    "apps/web/src/App.tsx",
			}},
		}},
		SkippedChecks: []affectedSkippedCheck{{
			Name:         "web-test",
			Status:       "skipped",
			Reason:       "subsumed",
			SubsumedBy:   []string{"web"},
			MatchedPaths: []string{"apps/web/src/App.tsx"},
			Reasons: []affectedRuleMatch{{
				Rule:    2,
				Pattern: "apps/web/src/**",
				Path:    "apps/web/src/App.tsx",
			}},
		}},
	}
	checks := agentAffectedChecksFromDryRun(report)
	if len(checks) != 2 {
		t.Fatalf("agent checks = %+v, want selected and skipped entries", checks)
	}
	if checks[0].Check != "web" || !checks[0].Selected {
		t.Fatalf("selected agent check = %+v, want selected web", checks[0])
	}
	if checks[1].Check != "web-test" || checks[1].Selected {
		t.Fatalf("skipped agent check = %+v, want unselected web-test", checks[1])
	}
	if !contains(checks[1].MatchedPaths, "apps/web/src/App.tsx") || !containsSubstring(checks[1].Reasons, "subsumed by web") {
		t.Fatalf("skipped agent check = %+v, want matched path and subsumed reason", checks[1])
	}
}

func TestAffectedSubsumesKeepsChildForUncoveredPaths(t *testing.T) {
	cfg := config.ProjectConfig{
		Checks: map[string]config.CheckConfig{
			"web":      {Command: "pnpm web-check"},
			"web-test": {Command: "pnpm test"},
		},
		Affected: config.AffectedConfig{
			Rules: []config.AffectedRule{
				{
					Paths:   []string{"apps/web/**"},
					Exclude: []string{"apps/web/generated/**"},
					Checks:  config.CommandList{"web"},
				},
				{
					Paths:  []string{"apps/web/**"},
					Checks: config.CommandList{"web-test"},
				},
			},
			Subsumes: map[string]config.CommandList{
				"web": {"web-test"},
			},
		},
	}
	changed := map[string][]string{
		"web":      {"apps/web/src/App.tsx"},
		"web-test": {"apps/web/src/App.tsx", "apps/web/generated/client.ts"},
	}
	selected, reasons, err := selectChecksForAffectedRules(cfg, changed)
	if err != nil {
		t.Fatalf("select checks: %v", err)
	}
	if strings.Join(selected, ",") != "web,web-test" {
		t.Fatalf("selected = %v, want web and web-test because generated path is child-only", selected)
	}
	if len(reasons["web-test"]) != 2 {
		t.Fatalf("web-test reasons = %+v, want source and generated paths", reasons["web-test"])
	}
}

func TestExecuteAffectedPlansContinuesAfterSuccessfulCheck(t *testing.T) {
	plans := []affectedCheckPlan{
		{
			Plan: checkRunPlan{
				Name:    "web-typecheck",
				Options: remoteRunOptions{CommandAlias: "web-typecheck"},
			},
		},
		{
			Plan: checkRunPlan{
				Name:    "web-test",
				Options: remoteRunOptions{CommandAlias: "web-test"},
			},
		},
	}
	var ran []string
	err := executeAffectedPlansWithRunner(plans, "pfl_test", func(options remoteRunOptions, matrix config.MatrixConfig, retries int) error {
		if options.APIToken != "pfl_test" {
			t.Fatalf("token = %q, want injected token", options.APIToken)
		}
		ran = append(ran, options.CommandAlias)
		if len(ran) == 1 {
			return exitCodeError{code: 0}
		}
		return nil
	})
	if !isSuccessfulExit(err) {
		t.Fatalf("executeAffectedPlansWithRunner error = %v, want successful exit sentinel", err)
	}
	if strings.Join(ran, ",") != "web-typecheck,web-test" {
		t.Fatalf("ran checks = %v, want both selected checks", ran)
	}
}

func TestExecuteAffectedPlansContinuesAfterFailingCheck(t *testing.T) {
	plans := []affectedCheckPlan{
		{
			Plan: checkRunPlan{
				Name:    "web-typecheck",
				Options: remoteRunOptions{CommandAlias: "web-typecheck"},
			},
		},
		{
			Plan: checkRunPlan{
				Name:    "web-test",
				Options: remoteRunOptions{CommandAlias: "web-test"},
			},
		},
	}
	var ran []string
	err := executeAffectedPlansWithRunner(plans, "pfl_test", func(options remoteRunOptions, matrix config.MatrixConfig, retries int) error {
		ran = append(ran, options.CommandAlias)
		if len(ran) == 1 {
			return exitCodeError{code: 2}
		}
		return nil
	})
	var exitErr exitCodeError
	if !errors.As(err, &exitErr) || exitErr.code != 2 {
		t.Fatalf("executeAffectedPlansWithRunner error = %v, want exit code 2", err)
	}
	if strings.Join(ran, ",") != "web-typecheck,web-test" {
		t.Fatalf("ran checks = %v, want both selected checks", ran)
	}
}

func TestExecuteAffectedPlansSkipsStatusSuccessAfterFailure(t *testing.T) {
	plans := []affectedCheckPlan{
		{
			Plan: checkRunPlan{
				Name:    "web-typecheck",
				Options: remoteRunOptions{CommandAlias: "web-typecheck"},
			},
		},
		{
			Plan: checkRunPlan{
				Name:    "web-test",
				When:    &checkWhenReport{Status: "success"},
				Options: remoteRunOptions{CommandAlias: "web-test"},
			},
		},
		{
			Plan: checkRunPlan{
				Name:    "web-lint",
				Options: remoteRunOptions{CommandAlias: "web-lint"},
			},
		},
	}
	var ran []string
	err := executeAffectedPlansWithRunner(plans, "pfl_test", func(options remoteRunOptions, matrix config.MatrixConfig, retries int) error {
		ran = append(ran, options.CommandAlias)
		if len(ran) == 1 {
			return exitCodeError{code: 2}
		}
		return nil
	})
	var exitErr exitCodeError
	if !errors.As(err, &exitErr) || exitErr.code != 2 {
		t.Fatalf("executeAffectedPlansWithRunner error = %v, want exit code 2", err)
	}
	if strings.Join(ran, ",") != "web-typecheck,web-lint" {
		t.Fatalf("ran checks = %v, want failed check, skipped status-gated check, then ungated check", ran)
	}
}
