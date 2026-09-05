package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

type setupReport struct {
	APIURL       string   `json:"api_url"`
	ProjectID    string   `json:"project_id"`
	ConfigPath   string   `json:"config_path,omitempty"`
	ConfigStatus string   `json:"config_status"`
	AuthStatus   string   `json:"auth_status"`
	TeamID       string   `json:"team_id,omitempty"`
	PlanID       string   `json:"plan_id,omitempty"`
	Check        string   `json:"check,omitempty"`
	Checks       []string `json:"checks,omitempty"`
	Written      []string `json:"written,omitempty"`
	Warnings     []string `json:"warnings,omitempty"`
	Next         []string `json:"next"`

	warmProfileAvailable   bool
	affectedAuditAvailable bool
}

func setupCommand(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	projectID := fs.String("project", envDefault("READYCI_PROJECT_ID", ""), "project id")
	checkName := fs.String("check", "", "preferred configured check for first-run commands")
	profile := fs.String("profile", "remote-ci", "setup profile: remote-ci or strict")
	writeConfig := fs.Bool("write-config", false, "write .readyci.yml when no config exists")
	detect := fs.Bool("detect", true, "detect common project checks when writing config")
	force := fs.Bool("force", false, "overwrite an existing Ready CI config with --write-config")
	makefile := fs.Bool("makefile", false, "print Makefile wrapper targets for Ready CI")
	writeMakefile := fs.Bool("write-makefile", false, "write Ready CI Makefile wrapper targets to readyci.mk")
	helper := fs.String("helper", "", "print helper script: shell-syntax or ops-config")
	writeHelper := fs.Bool("write-helper", false, "write --helper output to its recommended path")
	writeHelpers := fs.Bool("write-helpers", false, "write all standard Ready CI helper scripts")
	jsonOutput := fs.Bool("json", false, "print machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci setup [--api-url URL] [--token TOKEN] [--project PROJECT] [--check NAME] [--profile remote-ci|strict] [--write-config] [--write-makefile] [--write-helpers] [--detect=false] [--force] [--makefile] [--helper NAME] [--write-helper] [--json]", nil)
	}
	if *profile != "remote-ci" && *profile != "strict" {
		return cliConfigError(fmt.Sprintf("unknown setup profile %q", *profile), nil)
	}
	if *makefile && *writeConfig {
		return cliConfigError("--makefile cannot be combined with --write-config", nil)
	}
	if *makefile && *writeMakefile {
		return cliConfigError("--makefile cannot be combined with --write-makefile", nil)
	}
	if *makefile && *writeHelper {
		return cliConfigError("--makefile cannot be combined with --write-helper", nil)
	}
	if *makefile && *writeHelpers {
		return cliConfigError("--makefile cannot be combined with --write-helpers", nil)
	}
	if *writeHelper && strings.TrimSpace(*helper) == "" {
		return cliConfigError("--write-helper requires --helper NAME", nil)
	}
	if *writeHelper && *writeHelpers {
		return cliConfigError("use either --write-helper or --write-helpers, not both", nil)
	}
	if strings.TrimSpace(*helper) != "" && *writeConfig {
		return cliConfigError("--helper cannot be combined with --write-config", nil)
	}
	if strings.TrimSpace(*helper) != "" && *writeMakefile {
		return cliConfigError("--helper cannot be combined with --write-makefile", nil)
	}
	if strings.TrimSpace(*helper) != "" && *writeHelpers {
		return cliConfigError("--helper cannot be combined with --write-helpers", nil)
	}
	if *makefile {
		if *jsonOutput {
			return cliConfigError("--json cannot be combined with --makefile", nil)
		}
		if strings.TrimSpace(*helper) != "" {
			return cliConfigError("--makefile cannot be combined with --helper", nil)
		}
		projectArg := ""
		if flagProvided(fs, "project") {
			projectArg = *projectID
		}
		project := resolveSetupProjectID(projectArg, config.ProjectConfig{})
		if err := validateControlFreeValue("project_id", project); err != nil {
			return err
		}
		fmt.Print(setupMakefileScaffold(normalizeAPIURL(*apiURL), project))
		return nil
	}
	if strings.TrimSpace(*helper) != "" {
		if *jsonOutput {
			return cliConfigError("--json cannot be combined with --helper", nil)
		}
		path, content, err := setupHelperScript(*helper)
		if err != nil {
			return cliConfigError(err.Error(), err)
		}
		if *writeHelper {
			if err := writeSetupHelperScript(path, content, *force); err != nil {
				return err
			}
			fmt.Printf("wrote: %s\n", path)
			return nil
		}
		fmt.Print(content)
		return nil
	}

	reportProjectID := *projectID
	if !flagProvided(fs, "project") {
		reportProjectID = ""
	}
	report, err := buildSetupReport(setupOptions{
		APIURL:        *apiURL,
		APIToken:      *apiToken,
		ProjectID:     reportProjectID,
		CheckName:     *checkName,
		Profile:       *profile,
		WriteConfig:   *writeConfig,
		WriteMakefile: *writeMakefile,
		WriteHelpers:  *writeHelpers,
		Detect:        *detect,
		Force:         *force,
	})
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSONOutput(report)
	}
	printSetupReport(report)
	return nil
}
