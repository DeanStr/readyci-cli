package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentInstructionsPrintAndInstall(t *testing.T) {
	output, err := captureStdoutForCLITest(t, func() error {
		return agentInstructionsPrintCommand(nil)
	})
	if err != nil {
		t.Fatalf("agent instructions print: %v", err)
	}
	requireCLITestOutputContains(t, output, "name: readyci-verify", "fixture artifacts need preview/apply", "readyci verify --json --progress=status", "Do not ask for admin tokens", "Plan the smallest relevant verification")
	output, err = captureStdoutForCLITest(t, func() error {
		return agentInstructionsPrintCommand([]string{"--target", "agents-md"})
	})
	if err != nil {
		t.Fatalf("agent AGENTS.md instructions print: %v", err)
	}
	requireCLITestOutputContains(t, output, agentInstructionsStart, "Use Ready CI as the verification protocol", "Keep `--task-id` stable", "fresh `--idempotency-key`")
	output, err = captureStdoutForCLITest(t, func() error {
		return agentInstructionsPrintCommand([]string{"--target", "codex"})
	})
	if err != nil {
		t.Fatalf("agent codex instructions print: %v", err)
	}
	requireCLITestOutputContains(t, output, "Ready CI Verification", "--intent edit-loop", "repair", "readyci.verification.v0alpha1")
	output, err = captureStdoutForCLITest(t, func() error {
		return agentInstructionsPrintCommand([]string{"--target", "generic-shell"})
	})
	if err != nil {
		t.Fatalf("agent generic shell instructions print: %v", err)
	}
	requireCLITestOutputContains(t, output, "Ready CI Shell Contract", "schema_version", "Never store token values", "Preview generated artifacts")
	output, err = captureStdoutForCLITest(t, func() error {
		return agentInstructionsPrintCommand([]string{"--target", "slash-commands"})
	})
	if err != nil {
		t.Fatalf("agent slash command instructions print: %v", err)
	}
	requireCLITestOutputContains(t, output, "Ready CI Slash Commands", "/readyci-plan", "/readyci-verify", "/readyci-final", "/readyci-explain", "/readyci-fixtures")
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
	if err := os.WriteFile("AGENTS.md", []byte("# Project\n"), 0o644); err != nil {
		t.Fatalf("write agents file: %v", err)
	}
	if err := os.WriteFile(".readyci.yml", []byte(`
project_id: proj_story
checks:
  ci:
    command: make ci
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	output, err = captureStdoutForCLITest(t, func() error {
		return agentInstructionsInstallCommand(nil)
	})
	if err != nil {
		t.Fatalf("agent instructions install: %v", err)
	}
	requireCLITestOutputContains(t, output, "wrote .agents/skills/readyci-verify/SKILL.md", "wrote .claude/skills/readyci-verify/SKILL.md")
	agentsData, err := os.ReadFile("AGENTS.md")
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	requireCLITestOutputContains(t, string(agentsData), "# Project")
	if strings.Contains(string(agentsData), agentInstructionsStart) {
		t.Fatalf("default install modified AGENTS.md:\n%s", agentsData)
	}
	skillData, err := os.ReadFile(filepath.Join(".agents", "skills", "readyci-verify", "SKILL.md"))
	if err != nil {
		t.Fatalf("read portable skill: %v", err)
	}
	requireCLITestOutputContains(t, string(skillData), "description: Verify code changes with Ready CI remote checks", "/readyci-final", "readyci runs logs --tail 200 --include-lifecycle --json RUN_ID")
	skillData, err = os.ReadFile(filepath.Join(".claude", "skills", "readyci-verify", "SKILL.md"))
	if err != nil {
		t.Fatalf("read claude skill: %v", err)
	}
	requireCLITestOutputContains(t, string(skillData), "description: Verify code changes with Ready CI remote checks", "/readyci-final", "readyci runs logs --tail 200 --include-lifecycle --json RUN_ID")

	if _, err := captureStdoutForCLITest(t, func() error {
		return agentInstructionsInstallCommand(nil)
	}); err != nil {
		t.Fatalf("agent instructions reinstall: %v", err)
	}
	agentsData, err = os.ReadFile("AGENTS.md")
	if err != nil {
		t.Fatalf("read reinstalled AGENTS.md: %v", err)
	}
	if strings.Contains(string(agentsData), agentInstructionsStart) {
		t.Fatalf("default reinstall modified AGENTS.md:\n%s", agentsData)
	}

	output, err = captureStdoutForCLITest(t, func() error {
		return agentInstructionsInstallCommand([]string{"--target", "agents-md"})
	})
	if err != nil {
		t.Fatalf("agent AGENTS.md install: %v", err)
	}
	agentsData, err = os.ReadFile("AGENTS.md")
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	requireCLITestOutputContains(t, output, "wrote AGENTS.md")
	requireCLITestOutputContains(t, string(agentsData), agentInstructionsStart, "Do not request admin tokens", "--intent final-handoff", "run_id", "classification")

	output, err = captureStdoutForCLITest(t, func() error {
		return agentInstructionsInstallCommand([]string{"--target", "cursor-skill"})
	})
	if err != nil {
		t.Fatalf("agent cursor skill install: %v", err)
	}
	requireCLITestOutputContains(t, output, "wrote .cursor/skills/readyci-verify/SKILL.md")

	output, err = captureStdoutForCLITest(t, func() error {
		return agentInstructionsDoctorCommand([]string{"--json"})
	})
	if err != nil {
		t.Fatalf("agent instructions doctor: %v\n%s", err, output)
	}
	requireCLITestOutputContains(t, output, `"schema_version": "readyci.verification.v0alpha1"`, `"status": "ok"`, `"path": ".agents/skills/readyci-verify/SKILL.md"`, `"name": "skill_copies_match"`, `"name": "reporting_fields"`)

	output, err = captureStdoutForCLITest(t, func() error {
		return agentCommand([]string{"setup", "--claude=false"})
	})
	if err != nil {
		t.Fatalf("agent setup: %v\n%s", err, output)
	}
	requireCLITestOutputContains(t, output, "wrote .agents/skills/readyci-verify/SKILL.md", "status: ok", "readyci verify --json --progress=status --intent edit-loop")

	skillOnlyRepo := t.TempDir()
	if err := os.Chdir(skillOnlyRepo); err != nil {
		t.Fatalf("chdir skill-only repo: %v", err)
	}
	output, err = captureStdoutForCLITest(t, func() error {
		return agentInstructionsInstallCommand([]string{"--target", "skill"})
	})
	if err != nil {
		t.Fatalf("agent skill-only install: %v\n%s", err, output)
	}
	requireCLITestOutputContains(t, output, "wrote .agents/skills/readyci-verify/SKILL.md")
	if _, err := os.Stat(filepath.Join(".claude", "skills", "readyci-verify", "SKILL.md")); err == nil {
		t.Fatalf("skill-only install wrote Claude compatibility skill")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat Claude skill: %v", err)
	}
	if _, err := os.Stat("AGENTS.md"); err == nil {
		t.Fatalf("skill-only install wrote AGENTS.md")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat AGENTS.md after skill-only install: %v", err)
	}

	setupRepo := t.TempDir()
	if err := os.Chdir(setupRepo); err != nil {
		t.Fatalf("chdir setup repo: %v", err)
	}
	if err := os.WriteFile(".readyci.yml", []byte(`
project_id: proj_setup
checks:
  ci:
    command: make ci
`), 0o644); err != nil {
		t.Fatalf("write setup config: %v", err)
	}
	output, err = captureStdoutForCLITest(t, func() error {
		return agentCommand([]string{"setup", "--claude=false"})
	})
	if err != nil {
		t.Fatalf("fresh agent setup: %v\n%s", err, output)
	}
	requireCLITestOutputContains(t, output, "wrote .agents/skills/readyci-verify/SKILL.md", "status: ok")
	if strings.Contains(output, "file is not installed") {
		t.Fatalf("fresh skill-only setup reported optional files as missing:\n%s", output)
	}
	if _, err := os.Stat("AGENTS.md"); err == nil {
		t.Fatalf("fresh skill-only setup wrote AGENTS.md")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat AGENTS.md: %v", err)
	}
}
