package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentPlanFinalGoalUsesRecommendedFullCheckBeforeAffectedRules(t *testing.T) {
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
	if err := os.MkdirAll(filepath.Join(repo, "platform"), 0o755); err != nil {
		t.Fatalf("mkdir platform: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
project_id: proj_story
affected:
  rules:
    - paths: ["platform/**"]
      checks: ["go"]
checks:
  go:
    command: go test ./...
  full:
    command: make ci
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "platform", "api.go"), []byte("package platform\n"), 0o644); err != nil {
		t.Fatalf("write platform file: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, "platform", "api.go"), []byte("package platform\n// changed\n"), 0o644); err != nil {
		t.Fatalf("change platform file: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return agentPlanCommand([]string{"--json", "--goal", "final handoff"})
	})
	if err != nil {
		t.Fatalf("agent final plan: %v\n%s", err, output)
	}
	var report agentPlanReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode final plan: %v\n%s", err, output)
	}
	if report.Mode != "check" || report.Check != "full" || report.VerifyCommand != "readyci verify --json --progress=status --project proj_story --changes working --check full" {
		t.Fatalf("final plan = %+v, want full check plan", report)
	}
	if report.Plan == nil || report.Plan.Goal != "final handoff" || len(report.Plan.Selected) != 1 || report.Plan.Selected[0].Name != "full" {
		t.Fatalf("final plan planner = %+v, want selected full with goal", report.Plan)
	}
}

func TestAgentPlanFinalHandoffIntentUsesBroadCheckBeforeAffectedAlias(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
project_id: proj_story
checks:
  affected:
    command: make affected
  touched:
    command: make touched
  ci:
    command: make ci
  full:
    command: make full
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return agentPlanCommand([]string{"--json", "--intent", "final-handoff"})
	})
	if err != nil {
		t.Fatalf("agent final-handoff plan: %v\n%s", err, output)
	}
	var report agentPlanReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode final-handoff plan: %v\n%s", err, output)
	}
	wantVerify := "readyci verify --json --progress=status --project proj_story --intent final-handoff --changes working --check full"
	if report.Intent != agentIntentFinalHandoff || report.Mode != "check" || report.Check != "full" || report.VerifyCommand != wantVerify {
		t.Fatalf("final-handoff plan = %+v, want broad full check with verify %q", report, wantVerify)
	}
}

func TestAgentPlanAffectedRulesIncludesPlannerAndProfileWarnings(t *testing.T) {
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
	if err := os.MkdirAll(filepath.Join(repo, "scripts"), 0o755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
project_id: proj_story
affected:
  profiles:
    - docs
    - shell
checks:
  shell-syntax:
    command: bash -n scripts/check.sh
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "scripts", "check.sh"), []byte("#!/usr/bin/env bash\n"), 0o755); err != nil {
		t.Fatalf("write shell script: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, "scripts", "check.sh"), []byte("#!/usr/bin/env bash\necho changed\n"), 0o755); err != nil {
		t.Fatalf("change shell script: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return agentPlanCommand([]string{"--json", "--goal", "verify changed files"})
	})
	if err != nil {
		t.Fatalf("agent affected plan with warning: %v\n%s", err, output)
	}
	var report agentPlanReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode affected plan with warning: %v\n%s", err, output)
	}
	if report.Mode != "affected_rules" || report.Plan == nil || len(report.Plan.Selected) != 1 || report.Plan.Selected[0].Name != "shell-syntax" {
		t.Fatalf("affected planner = %+v report=%+v, want selected shell-syntax", report.Plan, report)
	}
	if len(report.Warnings) != 1 || report.Warnings[0].Section != "affected.profiles" || !strings.Contains(report.Warnings[0].Message, `affected profile "docs" did not match`) {
		t.Fatalf("warnings = %+v, want structured docs profile warning", report.Warnings)
	}
}
