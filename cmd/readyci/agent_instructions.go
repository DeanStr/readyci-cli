package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func agentInstructionsCommand(args []string) error {
	if len(args) == 0 {
		return cliConfigError("usage: readyci agent instructions <print|install|doctor>", nil)
	}
	switch args[0] {
	case "print":
		return agentInstructionsPrintCommand(args[1:])
	case "install":
		return agentInstructionsInstallCommand(args[1:])
	case "doctor":
		return agentInstructionsDoctorCommand(args[1:])
	default:
		return cliConfigError(fmt.Sprintf("unknown agent instructions command %q", args[0]), nil)
	}
}

func agentInstructionsPrintCommand(args []string) error {
	fs := flag.NewFlagSet("agent instructions print", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	target := fs.String("target", "skill", "instructions target: skill, agents-md, agents, codex, generic-shell, slash-commands, claude-skill, claude, or cursor-skill")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci agent instructions print [--target skill|agents-md|agents|codex|generic-shell|slash-commands|claude-skill|claude|cursor-skill]", nil)
	}
	switch normalizeAgentInstructionsTarget(*target) {
	case "skill", "agents-skill", "codex-skill", "claude-skill", "cursor-skill":
		fmt.Print(readyCIAgentSkill())
	case "agents", "agents-md":
		fmt.Print(readyCIAgentsSnippet())
	case "codex":
		fmt.Print(readyCICodexSnippet())
	case "generic-shell":
		fmt.Print(readyCIGenericShellSnippet())
	case "slash", "slash-commands", "commands":
		fmt.Print(readyCISlashCommandsSnippet())
	case "claude":
		fmt.Print(readyCIAgentSkill())
	default:
		return cliConfigError(fmt.Sprintf("unknown instructions target %q", *target), nil)
	}
	return nil
}

func agentInstructionsInstallCommand(args []string) error {
	fs := flag.NewFlagSet("agent instructions install", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	agentsFile := fs.String("agents-file", "AGENTS.md", "portable agent instructions file")
	target := fs.String("target", "all", "instructions target: all, skill, agents-md, agents, codex, generic-shell, claude-skill, claude, or cursor-skill")
	agentsMD := fs.Bool("agents-md", false, "also write the portable AGENTS.md instruction block")
	claude := fs.Bool("claude", true, "with --target all, write .claude/skills/readyci-verify/SKILL.md")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci agent instructions install [--target all|skill|agents-md|agents|codex|generic-shell|claude-skill|claude|cursor-skill] [--agents-md] [--agents-file AGENTS.md] [--claude=false]", nil)
	}
	return installAgentInstructions(*target, *agentsFile, *claude, *agentsMD)
}

func agentSetupCommand(args []string) error {
	fs := flag.NewFlagSet("agent setup", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	agentsFile := fs.String("agents-file", "AGENTS.md", "portable agent instructions file")
	agentsMD := fs.Bool("agents-md", false, "also write the portable AGENTS.md instruction block")
	claude := fs.Bool("claude", true, "write .claude/skills/readyci-verify/SKILL.md")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci agent setup [--agents-md] [--agents-file AGENTS.md] [--claude=false]", nil)
	}
	if err := installAgentInstructions("all", *agentsFile, *claude, *agentsMD); err != nil {
		return err
	}
	report := buildAgentInstructionsDoctorReport(*agentsFile)
	printAgentInstructionsDoctorReport(report)
	if report.Status == "fail" {
		return exitCodeError{code: cliExitUsageOrConfig}
	}
	fmt.Println("next:")
	fmt.Println("  1. Set READYCI_API_URL, READYCI_PROJECT_ID, and READYCI_API_TOKEN in your agent environment.")
	fmt.Println("  2. Commit the installed Ready CI skill files.")
	fmt.Println("  3. Ask your agent to run: readyci verify --json --progress=status --intent edit-loop")
	return nil
}

func installAgentInstructions(target, agentsFile string, claude, agentsMD bool) error {
	switch normalizeAgentInstructionsTarget(target) {
	case "all":
		if err := writeReadyCIAgentSkill(); err != nil {
			return err
		}
		if claude {
			if err := writeReadyCIClaudeSkill(); err != nil {
				return err
			}
		}
		if agentsMD {
			return writeReadyCIAgentsFile(agentsFile, readyCIAgentsSnippet())
		}
	case "skill", "agents-skill", "codex-skill":
		if err := writeReadyCIAgentSkill(); err != nil {
			return err
		}
		if agentsMD {
			return writeReadyCIAgentsFile(agentsFile, readyCIAgentsSnippet())
		}
	case "agents", "agents-md":
		return writeReadyCIAgentsFile(agentsFile, readyCIAgentsSnippet())
	case "codex":
		return writeReadyCIAgentsFile(agentsFile, readyCICodexSnippet())
	case "generic-shell":
		return writeReadyCIAgentsFile(agentsFile, readyCIGenericShellSnippet())
	case "claude", "claude-skill":
		return writeReadyCIClaudeSkill()
	case "cursor-skill":
		return writeReadyCICursorSkill()
	default:
		return cliConfigError(fmt.Sprintf("unknown instructions target %q", target), nil)
	}
	return nil
}

func writeReadyCIAgentsFile(path, snippet string) error {
	if err := upsertMarkedBlock(path, snippet); err != nil {
		return err
	}
	fmt.Printf("wrote %s\n", path)
	return nil
}

func writeReadyCIAgentSkill() error {
	return writeReadyCISkill(readyCIAgentSkillPath())
}

func writeReadyCIClaudeSkill() error {
	return writeReadyCISkill(readyCIClaudeSkillPath())
}

func writeReadyCICursorSkill() error {
	return writeReadyCISkill(readyCICursorSkillPath())
}

func writeReadyCISkill(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(readyCIAgentSkill()), 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s\n", path)
	return nil
}

func readyCIAgentSkillPath() string {
	return filepath.Join(".agents", "skills", "readyci-verify", "SKILL.md")
}

func readyCIClaudeSkillPath() string {
	return filepath.Join(".claude", "skills", "readyci-verify", "SKILL.md")
}

func readyCICursorSkillPath() string {
	return filepath.Join(".cursor", "skills", "readyci-verify", "SKILL.md")
}

func normalizeAgentInstructionsTarget(target string) string {
	return strings.ToLower(strings.TrimSpace(target))
}
