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
)

func TestAffectedAuditRunsRuleTests(t *testing.T) {
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
	if err := os.MkdirAll(filepath.Join(repo, "scripts"), 0o755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "scripts", "check.sh"), []byte("#!/usr/bin/env bash\necho ok\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
affected:
  profiles:
    - shell
  tests:
    - path: scripts/check.sh
      checks:
        - shell-syntax
checks:
  shell-syntax:
    command: bash -n scripts/check.sh
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")

	output, err := captureStdoutForCLITest(t, func() error {
		return affectedAuditCommand([]string{"--json"})
	})
	if err != nil {
		t.Fatalf("affected audit: %v", err)
	}
	var report affectedAuditReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode audit report: %v\n%s", err, output)
	}
	if report.Status != "ok" {
		t.Fatalf("audit status = %q warnings=%v", report.Status, report.Warnings)
	}
	if len(report.Tests) != 1 || report.Tests[0].Status != "ok" {
		t.Fatalf("audit tests = %+v", report.Tests)
	}
}

func TestAffectedAuditIgnoreAllowsStrictGateForIntentionalGaps(t *testing.T) {
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
	if err := os.MkdirAll(filepath.Join(repo, "legacy", "sql"), 0o755); err != nil {
		t.Fatalf("mkdir legacy: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "legacy", "sql", "patch.sql"), []byte("select 1;\n"), 0o644); err != nil {
		t.Fatalf("write legacy sql: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "apps", "web", "src"), 0o755); err != nil {
		t.Fatalf("mkdir web src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "apps", "web", "src", "app.ts"), []byte("export {}\n"), 0o644); err != nil {
		t.Fatalf("write web source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
affected:
  audit_ignore:
    - legacy/**
  rules:
    - paths:
        - .readyci.yml
        - apps/web/src/**
      checks:
        - web-test
checks:
  web-test:
    command: pnpm test
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")

	output, err := captureStdoutForCLITest(t, func() error {
		return affectedAuditCommand([]string{"--json", "--strict"})
	})
	if err != nil {
		t.Fatalf("affected audit strict with ignored legacy path: %v\n%s", err, output)
	}
	var report affectedAuditReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode audit report: %v\n%s", err, output)
	}
	if report.Status != "ok" || len(report.UnmatchedFiles) != 0 {
		t.Fatalf("audit report = %+v, want ok with no unmatched files", report)
	}
	if !contains(report.IgnoredFiles, "legacy/sql/patch.sql") {
		t.Fatalf("ignored files = %v, want legacy/sql/patch.sql", report.IgnoredFiles)
	}

	if err := os.WriteFile(filepath.Join(repo, "uncovered.py"), []byte("print('live')\n"), 0o644); err != nil {
		t.Fatalf("write uncovered source: %v", err)
	}
	runGitForCLITest(t, "add", "uncovered.py")
	runGitForCLITest(t, "commit", "-m", "add uncovered source")
	output, err = captureStdoutForCLITest(t, func() error {
		return affectedAuditCommand([]string{"--json", "--strict"})
	})
	var exitErr exitCodeError
	if !errors.As(err, &exitErr) || exitErr.code != 1 {
		t.Fatalf("affected audit strict error = %v, want exit 1\n%s", err, output)
	}
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode failed audit report: %v\n%s", err, output)
	}
	if !contains(report.UnmatchedFiles, "uncovered.py") {
		t.Fatalf("unmatched files = %v, want uncovered.py", report.UnmatchedFiles)
	}
	if !contains(report.IgnoredFiles, "legacy/sql/patch.sql") {
		t.Fatalf("ignored files after failure = %v, want legacy/sql/patch.sql", report.IgnoredFiles)
	}
}

func TestAffectedAuditDetectsDuplicateAggregateSelections(t *testing.T) {
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
	if err := os.MkdirAll(filepath.Join(repo, "apps", "web", "src"), 0o755); err != nil {
		t.Fatalf("mkdir web src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "apps", "web", "src", "app.tsx"), []byte("export {}\n"), 0o644); err != nil {
		t.Fatalf("write app: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
affected:
  rules:
    - paths:
        - apps/web/src/**
      checks:
        - web
        - web-test
checks:
  web:
    command: pnpm check
  web-test:
    command: pnpm test
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")

	output, err := captureStdoutForCLITest(t, func() error {
		return affectedAuditCommand([]string{"--json", "--duplicates"})
	})
	var exitErr exitCodeError
	if !errors.As(err, &exitErr) || exitErr.code != 1 {
		t.Fatalf("affected audit duplicates error = %v, want exit 1\n%s", err, output)
	}
	var report affectedAuditReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode audit report: %v\n%s", err, output)
	}
	if len(report.DuplicateSelections) != 1 || report.DuplicateSelections[0].Aggregate != "web" {
		t.Fatalf("duplicates = %+v, want web aggregate duplicate", report.DuplicateSelections)
	}
}

func TestAffectedAuditSuggestsRulesForUnmatchedModuleScripts(t *testing.T) {
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
	if err := os.MkdirAll(filepath.Join(repo, "apps", "web", "src", "__tests__"), 0o755); err != nil {
		t.Fatalf("mkdir web tests: %v", err)
	}
	testPath := filepath.Join(repo, "apps", "web", "src", "__tests__", "static_route_contract.test.mjs")
	if err := os.WriteFile(testPath, []byte("export {}\n"), 0o644); err != nil {
		t.Fatalf("write mjs test: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
affected:
  rules:
    - paths:
        - apps/web/src/**/*.ts
        - apps/web/src/**/*.tsx
        - apps/web/src/**/*.js
        - apps/web/src/**/*.jsx
      checks:
        - web
checks:
  web:
    command: pnpm check
  web-typecheck:
    command: pnpm typecheck
  web-lint:
    command: pnpm lint
  web-test:
    command: pnpm test
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")

	output, err := captureStdoutForCLITest(t, func() error {
		return affectedAuditCommand([]string{"--json", "--strict"})
	})
	var exitErr exitCodeError
	if !errors.As(err, &exitErr) || exitErr.code != 1 {
		t.Fatalf("affected audit strict error = %v, want exit 1\n%s", err, output)
	}
	var report affectedAuditReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode audit report: %v\n%s", err, output)
	}
	if !contains(report.UnmatchedFiles, "apps/web/src/__tests__/static_route_contract.test.mjs") {
		t.Fatalf("unmatched files = %v, want module script test", report.UnmatchedFiles)
	}
	if len(report.SuggestedRules) == 0 {
		t.Fatalf("suggested rules empty for unmatched module script")
	}
	rule := report.SuggestedRules[0]
	if len(rule.Paths) != 1 || rule.Paths[0] != "apps/web/src/**/*.mjs" {
		t.Fatalf("suggested paths = %v, want apps/web/src/**/*.mjs", rule.Paths)
	}
	if got := strings.Join(rule.Checks, ","); got != "web-typecheck,web-lint,web-test" {
		t.Fatalf("suggested checks = %q, want web granular test checks", got)
	}
}

func TestAffectedAuditSuggestsNarrowChecksForGoModuleFixtures(t *testing.T) {
	cfg := config.ProjectConfig{
		Checks: map[string]config.CheckConfig{
			"cli":     {Command: "go -C cli test ./..."},
			"cli-vet": {Command: "go -C cli vet ./..."},
			"go-fmt":  {Command: "bash scripts/go-fmt-check.sh"},
			"ci":      {Command: "make ci"},
		},
	}
	rules := suggestedAffectedRulesForUnmatchedFiles(cfg, []string{
		"cli/cmd/readyci/testdata/agent_contracts/plan.golden.json",
		"cli/cmd/readyci/main.go",
	})
	if len(rules) != 2 {
		t.Fatalf("suggested rules = %+v, want fixture and Go source suggestions", rules)
	}
	if rules[0].Paths[0] != "cli/cmd/readyci/testdata/**/*.json" || strings.Join(rules[0].Checks, ",") != "cli" {
		t.Fatalf("fixture suggestion = %+v, want cli-only testdata JSON rule", rules[0])
	}
	if rules[1].Paths[0] != "cli/cmd/readyci/main.go" || strings.Join(rules[1].Checks, ",") != "cli,cli-vet,go-fmt" {
		t.Fatalf("Go source suggestion = %+v, want cli/vet/fmt rule", rules[1])
	}
}

func TestAffectedAuditFixWritesMissingTests(t *testing.T) {
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
	if err := os.MkdirAll(filepath.Join(repo, "apps", "web", "src"), 0o755); err != nil {
		t.Fatalf("mkdir web src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "apps", "web", "src", "app.tsx"), []byte("export {}\n"), 0o644); err != nil {
		t.Fatalf("write app: %v", err)
	}
	configPath := filepath.Join(repo, ".readyci.yml")
	if err := os.WriteFile(configPath, []byte(`
affected:
  subsumes:
    web:
      - web-test
  rules:
    - paths:
        - apps/web/src/**
      checks:
        - web
    - paths:
        - apps/web/src/**
      checks:
        - web-test
checks:
  web:
    command: pnpm web
  web-test:
    command: pnpm test
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.Chmod(configPath, 0o600); err != nil {
		t.Fatalf("chmod config: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")

	output, err := captureStdoutForCLITest(t, func() error {
		return affectedAuditCommand([]string{"--fix"})
	})
	if err != nil {
		t.Fatalf("affected audit fix: %v\n%s", err, output)
	}
	cfg, _, err := config.LoadProjectConfig(repo)
	if err != nil {
		t.Fatalf("load fixed config: %v", err)
	}
	if len(cfg.Affected.Tests) != 1 || cfg.Affected.Tests[0].Path != "apps/web/src/app.tsx" {
		t.Fatalf("affected tests = %+v, want generated app fixture", cfg.Affected.Tests)
	}
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("stat fixed config: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("fixed config mode = %v, want 0600", got)
	}
}

func TestAffectedAuditRejectsProfileOnlyConfigWithNoGeneratedRules(t *testing.T) {
	cfg := config.ProjectConfig{
		Checks: map[string]config.CheckConfig{
			"custom": {Command: "make custom"},
		},
		Affected: config.AffectedConfig{Profiles: []string{"shell"}},
	}
	_, err := buildAffectedAuditReport(".", cfg, ".readyci.yml", false, false, false)
	if err == nil || !strings.Contains(err.Error(), `affected profile "shell" did not match any configured check names`) {
		t.Fatalf("audit error = %v, want profile expansion error", err)
	}
}

func TestAffectedAuditUsesRepoRelativePathsFromSubdirectory(t *testing.T) {
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
	if err := os.MkdirAll(filepath.Join(repo, "a", "b"), 0o755); err != nil {
		t.Fatalf("mkdir nested source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "a", "b", "file.ts"), []byte("export {}\n"), 0o644); err != nil {
		t.Fatalf("write nested source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
affected:
  rules:
    - paths:
        - .readyci.yml
      checks:
        - config-check
    - paths:
        - a/b/**
      checks:
        - web-check
checks:
  config-check:
    command: readyci config validate
  web-check:
    command: pnpm test
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.Chdir(filepath.Join(repo, "a")); err != nil {
		t.Fatalf("chdir subdir: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return affectedAuditCommand([]string{"--json", "--strict", "--examples"})
	})
	if err != nil {
		t.Fatalf("affected audit from subdir: %v\n%s", err, output)
	}
	var report affectedAuditReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode audit report: %v\n%s", err, output)
	}
	if report.Status != "ok" || len(report.UnmatchedFiles) != 0 {
		t.Fatalf("audit report = %+v, want no unmatched files", report)
	}
	foundRepoRelativeExample := false
	for _, example := range report.Examples {
		if contains(example.Paths, "a/b/file.ts") {
			foundRepoRelativeExample = true
		}
		if contains(example.Paths, "b/file.ts") {
			t.Fatalf("audit example used subdirectory-relative path: %+v", report.Examples)
		}
	}
	if !foundRepoRelativeExample {
		t.Fatalf("audit examples = %+v, want repo-relative a/b/file.ts", report.Examples)
	}
}

func TestAffectedAuditExamplesAndNarrowWebLint(t *testing.T) {
	cfg := config.ProjectConfig{
		Checks: map[string]config.CheckConfig{
			"web-test": {Command: "pnpm test"},
		},
		Affected: config.AffectedConfig{
			Rules: []config.AffectedRule{
				{Paths: []string{"apps/web/src/components/**/*.tsx"}, Checks: config.CommandList{"web-test"}},
			},
		},
	}
	warnings := affectedRuleLintWarnings(cfg)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "apps/web/src/**") {
		t.Fatalf("lint warnings = %v, want generic web source warning", warnings)
	}
	cfg.Affected.UnmatchedChecks = config.CommandList{"web-test"}
	if warnings := affectedRuleLintWarnings(cfg); len(warnings) != 0 {
		t.Fatalf("lint warnings = %v, unmatched check covers new web source paths", warnings)
	}
	repo := t.TempDir()
	runGitForCLITest(t, "-C", repo, "init", "-b", "main")
	filterCache := newAffectedPathFilterCache(context.Background(), repo, cfg)
	checks, err := selectChecksForAffectedPath(filterCache, cfg, "apps/web/src/components/Button.tsx")
	if err != nil {
		t.Fatalf("select affected path: %v", err)
	}
	if got := strings.Join(checks, ","); got != "web-test" {
		t.Fatalf("checks = %q, want web-test", got)
	}
	if !sourceLikeAffectedPath("app.py") {
		t.Fatal("Python source file was not treated as source-like")
	}
	if !sourceLikeAffectedPath("app.mts") || !sourceLikeAffectedPath("app.cts") {
		t.Fatal("TypeScript module source files were not treated as source-like")
	}
}

func TestAffectedExplainHonorsIncludeUntrackedFalse(t *testing.T) {
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
    - paths:
        - new.go
      checks:
        - tracked-only
checks:
  tracked-only:
    command: go test ./...
    sync:
      include_untracked: false
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runGitForCLITest(t, "add", ".readyci.yml")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, "new.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write untracked source: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return affectedExplainCommand([]string{"--json", "new.go"})
	})
	if err != nil {
		t.Fatalf("affected explain: %v\n%s", err, output)
	}
	var report affectedExplainReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode explain report: %v\n%s", err, output)
	}
	if len(report.Paths) != 1 || report.Paths[0].Covered || len(report.Paths[0].Checks) != 0 {
		t.Fatalf("explain path = %+v, want untracked file excluded", report.Paths)
	}
}

func TestAffectedExplainAndAuditRespectCheckSyncExclude(t *testing.T) {
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
	if err := os.MkdirAll(filepath.Join(repo, "generated"), 0o755); err != nil {
		t.Fatalf("mkdir generated: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "generated", "foo.go"), []byte("package generated\n"), 0o644); err != nil {
		t.Fatalf("write generated source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
affected:
  rules:
    - paths:
        - generated/**
      checks:
        - generated-check
  tests:
    - path: generated/foo.go
      checks:
        - generated-check
checks:
  generated-check:
    command: go test ./generated
    sync:
      exclude:
        - generated/**
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")

	explainOutput, err := captureStdoutForCLITest(t, func() error {
		return affectedExplainCommand([]string{"--json", "generated/foo.go"})
	})
	if err != nil {
		t.Fatalf("affected explain: %v", err)
	}
	var explain affectedExplainReport
	if err := json.Unmarshal([]byte(explainOutput), &explain); err != nil {
		t.Fatalf("decode explain report: %v\n%s", err, explainOutput)
	}
	if len(explain.Paths) != 1 || explain.Paths[0].Covered || len(explain.Paths[0].Checks) != 0 {
		t.Fatalf("explain path = %+v, want excluded uncovered path", explain.Paths)
	}

	auditOutput, err := captureStdoutForCLITest(t, func() error {
		return affectedAuditCommand([]string{"--json"})
	})
	var exitErr exitCodeError
	if !errors.As(err, &exitErr) || exitErr.code != 1 {
		t.Fatalf("affected audit error = %v, want exit 1\n%s", err, auditOutput)
	}
	var audit affectedAuditReport
	if err := json.Unmarshal([]byte(auditOutput), &audit); err != nil {
		t.Fatalf("decode audit report: %v\n%s", err, auditOutput)
	}
	if len(audit.Tests) != 1 || audit.Tests[0].Status != "failed" || len(audit.Tests[0].ActualChecks) != 0 {
		t.Fatalf("audit tests = %+v, want failed excluded path", audit.Tests)
	}
}
