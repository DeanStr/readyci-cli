package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type agentInstructionsDoctorReport struct {
	SchemaVersion string                         `json:"schema_version"`
	Status        string                         `json:"status"`
	Checks        []agentInstructionsDoctorCheck `json:"checks"`
	Warnings      []agentWarning                 `json:"warnings,omitempty"`
}

type agentInstructionsDoctorCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

func agentInstructionsDoctorCommand(args []string) error {
	fs := flag.NewFlagSet("agent instructions doctor", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOutput := fs.Bool("json", false, "print machine-readable JSON")
	agentsFile := fs.String("agents-file", "AGENTS.md", "portable agent instructions file")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci agent instructions doctor [--json] [--agents-file AGENTS.md]", nil)
	}
	report := buildAgentInstructionsDoctorReport(*agentsFile)
	if *jsonOutput {
		if err := writeJSONOutput(report); err != nil {
			return err
		}
		if report.Status == "fail" {
			return exitCodeError{code: cliExitUsageOrConfig}
		}
		return nil
	}
	printAgentInstructionsDoctorReport(report)
	if report.Status == "fail" {
		return exitCodeError{code: cliExitUsageOrConfig}
	}
	return nil
}

func buildAgentInstructionsDoctorReport(agentsFile string) agentInstructionsDoctorReport {
	report := agentInstructionsDoctorReport{
		SchemaVersion: agentSchemaVersion,
		Status:        "ok",
	}
	instructionFiles := []string{
		readyCIAgentSkillPath(),
		readyCIClaudeSkillPath(),
		readyCICursorSkillPath(),
		strings.TrimSpace(agentsFile),
	}
	instructionText := ""
	installed := false
	skillTexts := map[string]string{}
	for _, path := range instructionFiles {
		if path == "" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			report.Checks = append(report.Checks, agentInstructionsDoctorCheck{Name: "instructions_file", Status: "fail", Path: path, Message: err.Error()})
			report.Status = "fail"
			continue
		}
		installed = true
		text := string(data)
		instructionText += "\n" + text
		if strings.HasSuffix(filepath.ToSlash(path), "/skills/readyci-verify/SKILL.md") {
			skillTexts[path] = text
		}
		report.Checks = append(report.Checks, agentInstructionsDoctorCheck{Name: "instructions_file", Status: "ok", Path: path, Message: "file is installed"})
		if tokenLeakLike(text) {
			report.Checks = append(report.Checks, agentInstructionsDoctorCheck{Name: "token_values_absent", Status: "fail", Path: path, Message: "file appears to contain a Ready CI token value or assignment"})
			report.Status = "fail"
		} else {
			report.Checks = append(report.Checks, agentInstructionsDoctorCheck{Name: "token_values_absent", Status: "ok", Path: path, Message: "no Ready CI token values detected"})
		}
	}
	if !installed {
		report.Status = "fail"
		report.Checks = append(report.Checks, agentInstructionsDoctorCheck{Name: "instructions_installed", Status: "fail", Message: "no Ready CI agent instructions were found"})
	} else {
		report.Checks = append(report.Checks, agentInstructionsDoctorCheck{Name: "instructions_installed", Status: "ok", Message: "Ready CI agent instructions are installed"})
	}
	if reportInstructionFieldsPresent(instructionText) {
		report.Checks = append(report.Checks, agentInstructionsDoctorCheck{Name: "reporting_fields", Status: "ok", Message: "instructions require check/run_id/status/classification/exit code reporting"})
	} else {
		report.Status = "fail"
		report.Checks = append(report.Checks, agentInstructionsDoctorCheck{Name: "reporting_fields", Status: "fail", Message: "instructions should require check name, run_id, status, classification, and exit code reporting"})
	}
	if len(skillTexts) > 1 {
		if skillCopiesDrifted(skillTexts) {
			report.Status = "fail"
			report.Checks = append(report.Checks, agentInstructionsDoctorCheck{Name: "skill_copies_match", Status: "fail", Message: "installed Ready CI skill copies differ; rerun readyci agent instructions install"})
		} else {
			report.Checks = append(report.Checks, agentInstructionsDoctorCheck{Name: "skill_copies_match", Status: "ok", Message: "installed Ready CI skill copies match"})
		}
	}
	loaded, err := loadProjectConfigOrInfer(".")
	if err != nil {
		report.Status = "fail"
		report.Checks = append(report.Checks, agentInstructionsDoctorCheck{Name: "readyci_config", Status: "fail", Message: err.Error()})
		return report
	}
	if err := validateProjectConfigForConfigPath(loaded.Config, loaded.Path); err != nil {
		report.Status = "fail"
		report.Checks = append(report.Checks, agentInstructionsDoctorCheck{Name: "readyci_config", Status: "fail", Path: loaded.Path, Message: err.Error()})
	} else if loaded.Inferred {
		report.Checks = append(report.Checks, agentInstructionsDoctorCheck{Name: "readyci_config", Status: "warn", Message: "no Ready CI config found; repository signals can infer a temporary plan"})
	} else {
		report.Checks = append(report.Checks, agentInstructionsDoctorCheck{Name: "readyci_config", Status: "ok", Path: loaded.Path, Message: "Ready CI config validates"})
	}
	if len(loaded.Config.Affected.Rules) > 0 || len(loaded.Config.Affected.Profiles) > 0 {
		report.Checks = append(report.Checks, agentInstructionsDoctorCheck{Name: "affected_rules", Status: "ok", Path: loaded.Path, Message: "affected rules or profiles are configured"})
	} else {
		report.Checks = append(report.Checks, agentInstructionsDoctorCheck{Name: "affected_rules", Status: "warn", Path: loaded.Path, Message: "no affected rules or profiles configured; agents may use fallback check selection"})
	}
	return report
}

func tokenLeakLike(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(text, "READYCI_API_TOKEN=") ||
		strings.Contains(text, "READYCI_API_TOKEN:") ||
		strings.Contains(lower, "bearer pfl_") ||
		strings.Contains(text, "pfl_")
}

func skillCopiesDrifted(skillTexts map[string]string) bool {
	var baseline string
	first := true
	for _, text := range skillTexts {
		if first {
			baseline = text
			first = false
			continue
		}
		if text != baseline {
			return true
		}
	}
	return false
}

func reportInstructionFieldsPresent(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "check") &&
		(strings.Contains(lower, "run_id") || strings.Contains(lower, "run id")) &&
		strings.Contains(lower, "status") &&
		strings.Contains(lower, "classification") &&
		strings.Contains(lower, "exit code")
}

func printAgentInstructionsDoctorReport(report agentInstructionsDoctorReport) {
	fmt.Printf("status: %s\n", report.Status)
	for _, check := range report.Checks {
		if check.Path != "" {
			fmt.Printf("%s: %s path=%s message=%s\n", check.Name, check.Status, check.Path, check.Message)
		} else {
			fmt.Printf("%s: %s message=%s\n", check.Name, check.Status, check.Message)
		}
	}
	for _, warning := range report.Warnings {
		fmt.Printf("warning: %s\n", warning.Message)
	}
}
