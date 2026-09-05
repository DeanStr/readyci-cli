package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentPlanPrePRSupplementalChecksRespectWhenFilters(t *testing.T) {
	t.Setenv("READYCI_API_URL", "")
	t.Setenv("READYCI_PROJECT_ID", "")

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
	if err := os.MkdirAll(filepath.Join(repo, "web", "src", "pages"), 0o755); err != nil {
		t.Fatalf("mkdir web: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
project_id: proj_story
affected:
  rules:
    - paths: ["web/src/**"]
      checks: ["web"]
checks:
  web:
    command: pnpm web:check
  web-e2e:
    command: pnpm web:test:e2e
    when:
      env:
        READYCI_BROWSER_E2E_ENABLED: "yes"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "web", "src", "pages", "Login.tsx"), []byte("export function Login() { return null }\n"), 0o644); err != nil {
		t.Fatalf("write page: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, "web", "src", "pages", "Login.tsx"), []byte("export function Login() { return <main /> }\n"), 0o644); err != nil {
		t.Fatalf("change page: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return agentPlanCommand([]string{"--json", "--intent", "pre-pr"})
	})
	if err != nil {
		t.Fatalf("agent pre-pr plan: %v\n%s", err, output)
	}
	var report agentPlanReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode pre-pr plan: %v\n%s", err, output)
	}
	var sawWeb bool
	for _, check := range report.Checks {
		switch check.Check {
		case "web":
			if check.Selected {
				sawWeb = true
			}
		case "web-e2e":
			t.Fatalf("checks = %+v, local pre-pr planner should not add supplemental web-e2e", report.Checks)
		}
	}
	if !sawWeb {
		t.Fatalf("checks = %+v, want web selected", report.Checks)
	}
}

func TestAgentPrePRSupplementalChecksPreserveIncludeUntracked(t *testing.T) {
	t.Setenv("READYCI_API_URL", "")
	t.Setenv("READYCI_PROJECT_ID", "")

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
	if err := os.MkdirAll(filepath.Join(repo, "web", "src", "pages"), 0o755); err != nil {
		t.Fatalf("mkdir web: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
project_id: proj_story
affected:
  rules:
    - paths: ["web/src/**"]
      checks: ["web"]
checks:
  web:
    command: pnpm web:check
  web-e2e:
    command: pnpm web:test:e2e
    sync:
      include_untracked: false
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "web", "src", "pages", "Login.tsx"), []byte("export function Login() { return null }\n"), 0o644); err != nil {
		t.Fatalf("write page: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, "web", "src", "pages", "Login.tsx"), []byte("export function Login() { return <main /> }\n"), 0o644); err != nil {
		t.Fatalf("change page: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "web", "src", "pages", "Scratch.tsx"), []byte("export const scratch = true\n"), 0o644); err != nil {
		t.Fatalf("write untracked page: %v", err)
	}
	loaded, err := loadProjectConfigOrInfer(".")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	resolveOptions := checkResolveOptions{Changes: "working"}
	plans, dryRun, _, err := buildAgentAffectedRulePlans(context.Background(), ".", loaded.Config, loaded.Path, resolveOptions)
	if err != nil {
		t.Fatalf("build affected plans: %v", err)
	}
	plans, _ = agentAugmentAffectedPlansForIntent(loaded.Config, plans, dryRun, agentIntentPrePR, resolveOptions)
	if len(plans) != 1 || plans[0].Plan.Name != "web" {
		t.Fatalf("plans = %+v, want only affected-rule web plan", plans)
	}
}

func TestAgentPlanSecuritySensitiveIntentForcesNoNetwork(t *testing.T) {
	t.Setenv("READYCI_API_URL", "")
	t.Setenv("READYCI_PROJECT_ID", "")

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
	if err := os.MkdirAll(filepath.Join(repo, "security"), 0o755); err != nil {
		t.Fatalf("mkdir security: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
project_id: proj_story
network:
  mode: default
affected:
  rules:
    - paths: ["security/**"]
      checks: ["security"]
checks:
  security:
    command: make security-check
    network_mode: default
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "security", "auth.go"), []byte("package security\n"), 0o644); err != nil {
		t.Fatalf("write security file: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, "security", "auth.go"), []byte("package security\n// changed\n"), 0o644); err != nil {
		t.Fatalf("change security file: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return agentPlanCommand([]string{"--json", "--intent", "security-sensitive"})
	})
	if err != nil {
		t.Fatalf("agent security-sensitive plan: %v\n%s", err, output)
	}
	var report agentPlanReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode security-sensitive plan: %v\n%s", err, output)
	}
	if report.Policy == nil || report.Policy.NetworkMode != "none" || !contains(report.Policy.PolicyReasons, "security-sensitive intent does not request project secrets") {
		t.Fatalf("security-sensitive policy = %+v, want no-network/no-secrets policy", report.Policy)
	}
	if len(report.Checks) != 1 || report.Checks[0].NetworkMode != "none" {
		t.Fatalf("security-sensitive checks = %+v, want selected check forced to no network", report.Checks)
	}
}

func TestAgentPlanFixtureUpdateIntentSelectsArtifactCheck(t *testing.T) {
	t.Setenv("READYCI_API_URL", "")
	t.Setenv("READYCI_PROJECT_ID", "")

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
	if err := os.WriteFile(".readyci.yml", []byte(`
project_id: proj_story
checks:
  web:
    command: pnpm web:check
  fixtures:
    command: pnpm fixtures:update
    artifact_paths:
      - tests/fixtures/**
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return agentPlanCommand([]string{"--json", "--intent", "fixture-update"})
	})
	if err != nil {
		t.Fatalf("agent fixture-update plan: %v\n%s", err, output)
	}
	var report agentPlanReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode fixture-update plan: %v\n%s", err, output)
	}
	if report.Mode != "check" || report.Check != "fixtures" || !strings.Contains(report.VerifyCommand, "--intent fixture-update") {
		t.Fatalf("fixture-update report = %+v, want artifact-producing fixtures check", report)
	}
	actions := agentFixtureArtifactActions("https://api.readyci.test", "run_fixture", []string{"tests/fixtures/**"})
	if len(actions) != 1 || actions[0].Kind != "artifacts_apply" || !strings.Contains(actions[0].Command, "readyci agent artifacts apply --api-url https://api.readyci.test --json run_fixture --paths 'tests/fixtures/**'") {
		t.Fatalf("fixture actions = %+v, want artifact apply dry-run command", actions)
	}
	if actions := agentFixtureArtifactActions("https://api.readyci.test", "run_fixture", nil); len(actions) != 0 {
		t.Fatalf("fixture actions without paths = %+v, want no artifact apply guidance", actions)
	}
}

func TestAgentPlanFixtureUpdateIntentSelectsReviewableOutputCheck(t *testing.T) {
	t.Setenv("READYCI_API_URL", "")
	t.Setenv("READYCI_PROJECT_ID", "")

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
	if err := os.WriteFile(".readyci.yml", []byte(`
project_id: proj_story
checks:
  web:
    command: pnpm web:check
  fixtures:
    command: pnpm fixtures:update
    outputs:
      reviewable:
        - tests/fixtures/**
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return agentPlanCommand([]string{"--json", "--intent", "fixture-update"})
	})
	if err != nil {
		t.Fatalf("agent fixture-update plan: %v\n%s", err, output)
	}
	var report agentPlanReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode fixture-update plan: %v\n%s", err, output)
	}
	if report.Mode != "check" || report.Check != "fixtures" || !strings.Contains(report.VerifyCommand, "--check fixtures") {
		t.Fatalf("fixture-update report = %+v, want reviewable-output fixtures check", report)
	}
}

func TestAgentPlanFixtureUpdateIntentUsesGlobalArtifactsForFixtureCheck(t *testing.T) {
	t.Setenv("READYCI_API_URL", "")
	t.Setenv("READYCI_PROJECT_ID", "")

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
	if err := os.WriteFile(".readyci.yml", []byte(`
project_id: proj_story
artifacts:
  paths:
    - tests/fixtures/**
checks:
  web:
    command: pnpm web:check
  fixtures:
    command: pnpm fixtures:update
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return agentPlanCommand([]string{"--json", "--intent", "fixture-update"})
	})
	if err != nil {
		t.Fatalf("agent fixture-update plan: %v\n%s", err, output)
	}
	var report agentPlanReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode fixture-update plan: %v\n%s", err, output)
	}
	if report.Check != "fixtures" || !strings.Contains(report.VerifyCommand, "--check fixtures") {
		t.Fatalf("fixture-update report = %+v, want fixtures check selected via global artifact paths", report)
	}
}

func TestAgentPlanFixtureUpdateIntentDoesNotSelectArbitraryGlobalArtifactCheck(t *testing.T) {
	t.Setenv("READYCI_API_URL", "")
	t.Setenv("READYCI_PROJECT_ID", "")

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
	if err := os.WriteFile(".readyci.yml", []byte(`
project_id: proj_story
artifacts:
  paths:
    - dist/**
checks:
  web:
    command: pnpm web:check
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return agentPlanCommand([]string{"--json", "--intent", "fixture-update"})
	})
	if err == nil || !strings.Contains(err.Error(), "fixture-update intent requires") {
		t.Fatalf("agent fixture-update plan err = %v output=%s, want missing fixture-like artifact check error", err, output)
	}
}

func TestAgentPlanFixtureUpdateIntentRequiresArtifactCheck(t *testing.T) {
	t.Setenv("READYCI_API_URL", "")
	t.Setenv("READYCI_PROJECT_ID", "")

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
	if err := os.WriteFile(".readyci.yml", []byte(`
project_id: proj_story
checks:
  web:
    command: pnpm web:check
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return agentPlanCommand([]string{"--json", "--intent", "fixture-update"})
	})
	if err == nil || !strings.Contains(err.Error(), "fixture-update intent requires") {
		t.Fatalf("agent fixture-update plan err = %v output=%s, want missing artifact check error", err, output)
	}
}

func agentPlanCheckNames(checks []agentAffectedCheck) []string {
	names := make([]string, 0, len(checks))
	for _, check := range checks {
		names = append(names, check.Check)
	}
	return names
}
