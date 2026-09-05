package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

func doctorCoverageCommand(args []string) error {
	fs := flag.NewFlagSet("doctor coverage", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOutput := fs.Bool("json", false, "print machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci doctor coverage [--json]", nil)
	}
	report := buildCoverageReport(".")
	if *jsonOutput {
		return writeJSONOutput(report)
	}
	printCoverageReport(report)
	return nil
}

type coverageReport struct {
	Status            string          `json:"status"`
	ConfigPath        string          `json:"config_path,omitempty"`
	ProjectID         string          `json:"project_id,omitempty"`
	Checks            []coverageCheck `json:"checks"`
	PossiblySkipped   []coverageGap   `json:"possibly_skipped"`
	SuggestedProfiles []string        `json:"suggested_profiles,omitempty"`
	Warnings          []string        `json:"warnings,omitempty"`
}

type coverageCheck struct {
	Name             string   `json:"name"`
	Command          string   `json:"command"`
	GeneratedProfile string   `json:"generated_profile,omitempty"`
	Reasons          []string `json:"reasons,omitempty"`
}

type coverageGap struct {
	Target           string   `json:"target"`
	Reason           string   `json:"reason"`
	Signals          []string `json:"signals,omitempty"`
	Missing          []string `json:"missing,omitempty"`
	SuggestedProfile string   `json:"suggested_profile,omitempty"`
	SuggestedCheck   string   `json:"suggested_check,omitempty"`
}

func buildCoverageReport(cwd string) coverageReport {
	report := coverageReport{
		Status:          "ok",
		Checks:          []coverageCheck{},
		PossiblySkipped: []coverageGap{},
	}
	cfg, configPath, err := config.LoadProjectConfig(cwd)
	if err != nil {
		report.Status = "warn"
		report.Warnings = append(report.Warnings, "config could not be loaded: "+err.Error())
		return report
	}
	report.ConfigPath = configPath
	report.ProjectID = cfg.ProjectID
	if configPath == "" {
		report.Status = "warn"
		report.Warnings = append(report.Warnings, "no .readyci.yml found; run readyci init --detect --print to inspect generated coverage")
	}
	for _, name := range sortedCheckNames(cfg.Checks) {
		check := cfg.Checks[name]
		report.Checks = append(report.Checks, coverageCheck{
			Name:             name,
			Command:          check.Command,
			GeneratedProfile: inferredGeneratedProfile(name, check.Command),
			Reasons:          coverageReasonsForCheck(name, check.Command),
		})
	}
	if len(cfg.Affected.Profiles) > 0 {
		report.SuggestedProfiles = append(report.SuggestedProfiles, cfg.Affected.Profiles...)
	}
	for _, module := range detectGoModules(cwd) {
		signals := detectGoPostgresSignals(cwd, module)
		if len(signals) == 0 {
			continue
		}
		if coverageHasPostgresCheck(cfg, module) {
			continue
		}
		target := displayRel(module)
		report.PossiblySkipped = append(report.PossiblySkipped, coverageGap{
			Target:  target,
			Reason:  "Go module has Postgres-backed test signals; run hosted config suggestions for an opinionated database test plan.",
			Signals: signals,
			Missing: []string{"READYCI_TEST_DATABASE_URL", "first-class postgres service"},
		})
	}
	report.SuggestedProfiles = uniqueStrings(report.SuggestedProfiles)
	if len(report.PossiblySkipped) > 0 || len(report.Warnings) > 0 {
		report.Status = "warn"
	}
	return report
}

func inferredGeneratedProfile(name, command string) string {
	lowerName := strings.ToLower(name)
	lowerCommand := strings.ToLower(command)
	switch {
	case lowerName == "go" && strings.Contains(lowerCommand, "go test"):
		return "go"
	case lowerName == "shell" || lowerName == "shell-syntax":
		return "shell"
	default:
		return ""
	}
}

func coverageReasonsForCheck(name, command string) []string {
	var reasons []string
	profile := inferredGeneratedProfile(name, command)
	if profile != "" {
		reasons = append(reasons, "matches generated profile "+profile)
	}
	if strings.Contains(command, "go test") {
		reasons = append(reasons, "runs Go tests")
	}
	if strings.Contains(command, "bash -n") {
		reasons = append(reasons, "runs shell syntax validation")
	}
	return reasons
}

func coverageHasPostgresCheck(cfg config.ProjectConfig, module string) bool {
	for name, check := range cfg.Checks {
		lowerName := strings.ToLower(name)
		lowerCommand := strings.ToLower(check.Command)
		for _, serviceRef := range check.Services {
			service, ok := cfg.Services[serviceRef]
			if ok && service.Type == "postgres" {
				return true
			}
			if strings.Contains(strings.ToLower(serviceRef), "postgres") {
				return true
			}
		}
		if strings.Contains(lowerName, "go-postgres") || strings.Contains(lowerName, "db") || strings.Contains(lowerName, "postgres") {
			return true
		}
		if strings.Contains(lowerCommand, "readyci_test_database_url") || strings.Contains(lowerCommand, "test_database_url") || strings.Contains(lowerCommand, "postgres") {
			return true
		}
	}
	return false
}

func printCoverageReport(report coverageReport) {
	fmt.Println("coverage: readyci")
	fmt.Printf("status: %s\n", report.Status)
	if report.ConfigPath != "" {
		fmt.Printf("config_path: %s\n", report.ConfigPath)
	}
	if report.ProjectID != "" {
		fmt.Printf("project_id: %s\n", report.ProjectID)
	}
	if len(report.Checks) > 0 {
		fmt.Println("checks:")
		for _, check := range report.Checks {
			fmt.Printf("  %s: %s\n", check.Name, check.Command)
			if check.GeneratedProfile != "" {
				fmt.Printf("    profile: %s\n", check.GeneratedProfile)
			}
			for _, reason := range check.Reasons {
				fmt.Printf("    reason: %s\n", reason)
			}
		}
	}
	if len(report.PossiblySkipped) > 0 {
		fmt.Println("possibly_skipped:")
		for _, gap := range report.PossiblySkipped {
			fmt.Printf("  %s: %s\n", gap.Target, gap.Reason)
			for _, signal := range gap.Signals {
				fmt.Printf("    signal: %s\n", signal)
			}
			for _, missing := range gap.Missing {
				fmt.Printf("    missing: %s\n", missing)
			}
			if gap.SuggestedCheck != "" {
				fmt.Printf("    suggested_check: %s\n", gap.SuggestedCheck)
			}
		}
	}
	for _, warning := range report.Warnings {
		fmt.Printf("warning: %s\n", warning)
	}
}
