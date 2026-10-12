package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func printDoctorReport(report doctorReport) {
	fmt.Println("doctor: readyci")
	fmt.Printf("api_url: %s\n", report.APIURL)
	fmt.Printf("project_id: %s\n", report.ProjectID)
	fmt.Printf("project_id_source: %s\n", report.ProjectIDSource)
	if report.Check != "" {
		fmt.Printf("check: %s\n", report.Check)
	}
	printed := map[string]bool{}
	for _, section := range []string{"git", "config", "affected", "check", "repo", "harness", "api", "auth", "project", "workers"} {
		if printDoctorSection(report.Diagnostics, section) {
			printed[section] = true
		}
	}
	for _, diagnostic := range report.Diagnostics {
		if printed[diagnostic.Section] {
			continue
		}
		if printDoctorSection(report.Diagnostics, diagnostic.Section) {
			printed[diagnostic.Section] = true
		}
	}
	if len(report.Next) > 0 {
		fmt.Println("next:")
		for _, command := range report.Next {
			fmt.Printf("  %s\n", command)
		}
	}
	fmt.Printf("summary: %d failure(s), %d warning(s)\n", report.Summary.Failures, report.Summary.Warnings)
}

func printDoctorSection(diagnostics []doctorDiagnostic, section string) bool {
	var sectionDiagnostics []doctorDiagnostic
	for _, diagnostic := range diagnostics {
		if diagnostic.Section == section {
			sectionDiagnostics = append(sectionDiagnostics, diagnostic)
		}
	}
	if len(sectionDiagnostics) == 0 {
		return false
	}
	fmt.Printf("%s:\n", section)
	for _, diagnostic := range sectionDiagnostics {
		fmt.Printf("  %s: %s\n", diagnostic.Level, diagnostic.Message)
		for _, detail := range diagnostic.Details {
			fmt.Printf("  detail: %s\n", detail)
		}
		for _, hint := range diagnostic.Hints {
			fmt.Printf("  hint: %s\n", hint)
		}
	}
	return true
}

func doctorNextCommands(report doctorReport) []string {
	var commands []string
	apiFlag := apiURLFlag(report.APIURL)
	if reportHasDiagnostic(report, "config", "warn", "no .readyci.yml") {
		commands = append(commands, doctorSetupCommand(report))
		commands = append(commands, "readyci setup --print")
	}
	if reportHasLevel(report, "config", "fail") {
		commands = append(commands, "readyci config validate")
	}
	if reportHasLevel(report, "auth", "warn") || reportHasLevel(report, "auth", "fail") {
		commands = append(commands, "readyci login"+apiFlag)
		commands = append(commands, readyCIAPIEnvCommand(report.APIURL, "<token>"))
	}
	if report.Check != "" {
		commands = append(commands, "readyci check --dry-run "+shellQuoteArg(report.Check))
		if reportHasDiagnostic(report, "workers", "warn", "skipped") {
			commands = append(commands, "readyci doctor"+apiFlag+" --project "+shellQuoteArg(report.ProjectID)+" --check "+shellQuoteArg(report.Check)+" --admin-token "+shellQuoteArg("<admin-token>"))
		}
		if report.WorkerCapability != nil && !report.WorkerCapability.UploadCapable {
			commands = append(commands, "readyci admin workers"+apiFlag+" --token "+shellQuoteArg("<admin-token>"))
		}
		if report.Summary.Failures == 0 && report.Summary.Warnings == 0 {
			commands = append(commands, "readyci check"+apiFlag+" --project "+shellQuoteArg(report.ProjectID)+" --open "+shellQuoteArg(report.Check))
		}
	} else if report.Summary.Failures == 0 && report.Summary.Warnings == 0 {
		commands = append(commands, "readyci run"+apiFlag+" --project "+shellQuoteArg(report.ProjectID)+" "+shellQuoteArg(suggestedDoctorRunCommand(".")))
	}
	return uniqueStrings(commands)
}

func suggestedDoctorRunCommand(cwd string) string {
	root, err := inferenceRoot(cwd)
	if err != nil || strings.TrimSpace(root) == "" {
		root = cwd
	}
	if fileExists(filepath.Join(root, "Cargo.toml")) {
		return "cargo test --workspace"
	}
	if fileExists(filepath.Join(root, "package.json")) {
		if command := suggestedNodeVerificationCommand(root); command != "" {
			return command
		}
	}
	if fileExists(filepath.Join(root, "go.work")) || fileExists(filepath.Join(root, "go.mod")) {
		return "go test ./..."
	}
	return "go test ./..."
}

func suggestedNodeVerificationCommand(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return ""
	}
	var pkg struct {
		Scripts        map[string]string `json:"scripts"`
		PackageManager string            `json:"packageManager"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return ""
	}
	manager := "npm"
	if strings.HasPrefix(pkg.PackageManager, "pnpm@") || fileExists(filepath.Join(root, "pnpm-lock.yaml")) {
		manager = "pnpm"
	} else if strings.HasPrefix(pkg.PackageManager, "yarn@") || fileExists(filepath.Join(root, "yarn.lock")) {
		manager = "yarn"
	}
	for _, script := range []string{"check", "test", "build"} {
		if strings.TrimSpace(pkg.Scripts[script]) != "" {
			return packageManagerScriptCommand(manager, script)
		}
	}
	return ""
}

func packageManagerScriptCommand(manager, script string) string {
	if script == "test" {
		return manager + " test"
	}
	return manager + " run " + script
}

func doctorSetupCommand(report doctorReport) string {
	command := "readyci setup --write-config --detect" + apiURLFlag(report.APIURL)
	if report.ProjectIDSource != "default" && strings.TrimSpace(report.ProjectID) != "" {
		command += " --project " + shellQuoteArg(report.ProjectID)
	}
	return command
}

func reportHasLevel(report doctorReport, section, level string) bool {
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Section == section && diagnostic.Level == level {
			return true
		}
	}
	return false
}

func reportHasDiagnostic(report doctorReport, section, level, containsText string) bool {
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Section == section && diagnostic.Level == level && strings.Contains(diagnostic.Message, containsText) {
			return true
		}
	}
	return false
}
