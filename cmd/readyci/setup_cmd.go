package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"golang.org/x/term"
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
	authError              error
	configError            error
}

func setupCommand(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	projectID := fs.String("project", envDefault("READYCI_PROJECT_ID", ""), "project id")
	checkName := fs.String("check", "", "preferred configured check for first-run commands")
	profile := fs.String("profile", "remote-ci", "setup profile: remote-ci or strict")
	image := fs.String("image", "", "runner image for the generated configuration")
	printConfig := fs.Bool("print", false, "print starter configuration locally without authentication or writes")
	preview := fs.Bool("preview", false, "inspect setup without writing files, signing in, or running checks")
	yes := fs.Bool("yes", false, "save configuration and run the first check without prompts; requires authentication")
	runCheck := fs.Bool("run", false, "preview and run the selected remote check")
	writeConfig := fs.Bool("write-config", false, "write .readyci.yml when no config exists")
	detect := fs.Bool("detect", true, "detect common project checks when writing config")
	force := fs.Bool("force", false, "allow replacing existing Ready CI configuration")
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
		return cliConfigError("usage: readyci setup [--yes|--preview] [--api-url URL] [--token TOKEN] [--project PROJECT] [--check NAME] [--profile remote-ci|strict] [--write-config] [--run] [--write-makefile] [--write-helpers] [--detect=false] [--force] [--makefile] [--helper NAME] [--write-helper] [--json]", nil)
	}
	if *profile != "remote-ci" && *profile != "strict" {
		return cliConfigError(fmt.Sprintf("unknown setup profile %q", *profile), nil)
	}
	explicitAction := flagProvided(fs, "write-config") || flagProvided(fs, "run") || *writeMakefile || *writeHelpers || *writeHelper
	inspection := *preview || *printConfig || *jsonOutput || *makefile || *helper != ""
	if *yes && (explicitAction || inspection) {
		return cliConfigError("--yes runs guided setup; do not combine it with preview, JSON, or explicit write/run options", nil)
	}
	if *preview && (explicitAction || *printConfig || *makefile || *helper != "") {
		return cliConfigError("--preview cannot be combined with writes, execution, or helper output", nil)
	}
	if *runCheck && (*jsonOutput || *makefile || *helper != "" || *writeHelper) {
		return cliConfigError("--run cannot be combined with --json or helper output; use readyci check --json for execution", nil)
	}
	if *printConfig {
		if *runCheck || *writeConfig || *writeMakefile || *writeHelpers || *writeHelper || *makefile || *helper != "" || *jsonOutput {
			return cliConfigError("--print cannot be combined with execution, writes, helper output, or --json", nil)
		}
		project := resolveSetupProjectID(*projectID, config.ProjectConfig{})
		if err := validateControlFreeValue("project_id", project); err != nil {
			return err
		}
		if *detect {
			fmt.Print(detectedProjectTemplateWithProfile(".", project, *image, *profile))
		} else {
			fmt.Print(remoteCIProjectTemplateWithProfile(project, *image, *profile))
		}
		return nil
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
	options := setupOptions{
		APIURL:        *apiURL,
		APIToken:      *apiToken,
		ProjectID:     reportProjectID,
		CheckName:     *checkName,
		Profile:       *profile,
		Image:         *image,
		WriteConfig:   *writeConfig,
		WriteMakefile: *writeMakefile,
		WriteHelpers:  *writeHelpers,
		Detect:        *detect,
		Force:         *force,
	}
	if !explicitAction && !inspection {
		if !*yes && !term.IsTerminal(int(os.Stdin.Fd())) {
			return cliConfigError("guided setup requires a terminal; use readyci setup --yes for automation or --preview to inspect setup", nil)
		}
		return guidedSetup(options, os.Stdin, os.Stdout, *yes)
	}
	report, err := buildSetupReport(options)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSONOutput(report)
	}
	printSetupReport(report)
	if *runCheck {
		if err := setupCheck(options, report, true); err != nil {
			return err
		}
		return setupCheck(options, report, false)
	}
	return nil
}
