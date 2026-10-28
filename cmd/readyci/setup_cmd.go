package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"golang.org/x/term"
)

type setupReport struct {
	APIURL           string                  `json:"api_url"`
	ProjectID        string                  `json:"project_id"`
	ConfigPath       string                  `json:"config_path,omitempty"`
	ConfigStatus     string                  `json:"config_status"`
	AuthStatus       string                  `json:"auth_status"`
	TeamID           string                  `json:"team_id,omitempty"`
	PlanID           string                  `json:"plan_id,omitempty"`
	Check            string                  `json:"check,omitempty"`
	Checks           []string                `json:"checks,omitempty"`
	Written          []string                `json:"written,omitempty"`
	Warnings         []string                `json:"warnings,omitempty"`
	Next             []string                `json:"next"`
	ImportCandidates []repoImportCandidate   `json:"import_candidates,omitempty"`
	SelectionStatus  string                  `json:"selection_status,omitempty"`
	SelectionReason  string                  `json:"selection_reason,omitempty"`
	ImportScope      *setupImportScope       `json:"import_scope,omitempty"`
	ProposedConfig   string                  `json:"proposed_config,omitempty"`
	Environment      *setupEnvironmentReport `json:"environment,omitempty"`
	RuntimePins      map[string]string       `json:"runtime_pins,omitempty"`

	warmProfileAvailable   bool
	affectedAuditAvailable bool
	authError              error
	configError            error
	scopeCandidates        []repoImportCandidate
	configGenerated        bool
}

func setupCommand(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	projectID := fs.String("project", envDefault("READYCI_PROJECT_ID", ""), "project id")
	checkName := fs.String("check", "", "preferred configured check for first-run commands")
	profile := fs.String("profile", "remote-ci", "setup profile: remote-ci or strict")
	image := fs.String("image", "", "runner image for the generated configuration")
	networkAllowlist := fs.String("network-allowlist", "", "replace generated network policy with this reviewed comma-separated allowlist for the entire check")
	var runtimeSelections stringSliceFlag
	fs.Var(&runtimeSelections, "runtime", "pin an imported workflow runtime numerically, e.g. node=24 or python=3.12; repeatable for node, go, python, pnpm")
	printConfig := fs.Bool("print", false, "print starter configuration without authentication or writes; moving runtime aliases use the API")
	preview := fs.Bool("preview", false, "inspect setup without writing files, signing in, or running checks")
	yes := fs.Bool("yes", false, "save configuration and run the first check without prompts; requires authentication")
	runCheck := fs.Bool("run", false, "preview and run the selected remote check")
	writeConfig := fs.Bool("write-config", false, "write .readyci.yml when no config exists")
	detect := fs.Bool("detect", true, "detect common project checks when writing config")
	importSource := fs.String("import", "", "select an existing command from make or github-actions")
	importCandidate := fs.String("candidate", "", "import candidate ID from setup --preview --json")
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
		return cliConfigError("usage: readyci setup [--yes|--preview] [--api-url URL] [--token TOKEN] [--project PROJECT] [--check NAME] [--profile remote-ci|strict] [--runtime NAME=VERSION] [--write-config] [--run] [--write-makefile] [--write-helpers] [--detect=false] [--force] [--makefile] [--helper NAME] [--write-helper] [--json]", nil)
	}
	if *profile != "remote-ci" && *profile != "strict" {
		return cliConfigError(fmt.Sprintf("unknown setup profile %q", *profile), nil)
	}
	runtimePins, err := parseSetupRuntimePins(runtimeSelections)
	if err != nil {
		return err
	}
	if len(runtimePins) > 0 && (!*detect || *makefile || *helper != "" || *writeMakefile || *writeHelpers || *importSource == "make") {
		return cliConfigError("--runtime requires detected workflow setup without helper output", nil)
	}
	var reviewedNetwork []string
	if flagProvided(fs, "network-allowlist") {
		var err error
		reviewedNetwork, err = normalizeSetupNetworkAllowlist(*networkAllowlist, *profile)
		if err != nil {
			return err
		}
		if *makefile || *helper != "" || *writeMakefile || *writeHelpers {
			return cliConfigError("--network-allowlist configures generated project checks and cannot be combined with helper output", nil)
		}
	}
	if *importSource != "" && *importSource != "make" && *importSource != "github-actions" {
		return cliConfigError("--import must be make or github-actions", nil)
	}
	if (*importSource != "" || *importCandidate != "") && (!*detect || *makefile || *helper != "" || *writeMakefile || *writeHelpers) {
		return cliConfigError("import selection requires detected setup without helper output", nil)
	}
	explicitAction := flagProvided(fs, "write-config") || flagProvided(fs, "run") || *writeMakefile || *writeHelpers || *writeHelper
	if (*importSource != "" || *importCandidate != "") && explicitAction && *importCandidate == "" {
		return cliConfigError("explicit import actions require --candidate ID", nil)
	}
	if (*importSource != "" || *importCandidate != "") && *runCheck && !*writeConfig {
		return cliConfigError("running an imported candidate requires saving it with --write-config, or use guided setup", nil)
	}
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
	previousAliasResolver := setupRuntimeAliasResolve
	setupRuntimeAliasResolve = setupAPIRuntimeAliasResolver(*apiURL)
	defer func() { setupRuntimeAliasResolve = previousAliasResolver }()
	if *printConfig {
		if *runCheck || *writeConfig || *writeMakefile || *writeHelpers || *writeHelper || *makefile || *helper != "" || *jsonOutput {
			return cliConfigError("--print cannot be combined with execution, writes, helper output, or --json", nil)
		}
		project := resolveSetupProjectID(*projectID, config.ProjectConfig{})
		if err := validateControlFreeValue("project_id", project); err != nil {
			return err
		}
		if *detect {
			options := setupOptions{Detect: true, Image: *image, Profile: *profile, ImportSource: *importSource, ImportCandidate: *importCandidate, NetworkAllowlist: reviewedNetwork, RuntimePins: runtimePins}
			allCandidates := setupImportCandidates(".", "", runtimePins)
			candidates := filterSetupImportCandidates(allCandidates, options.ImportSource)
			var template string
			var err error
			if options.ImportSource != "" || options.ImportCandidate != "" {
				var selected *repoImportCandidate
				selected, err = selectSetupImport(options, candidates, nil, io.Discard, true)
				if err == nil {
					template = setupImportedTemplateWithScope(".", options, project, *selected, allCandidates)
					err = validateSetupRuntimeTemplate(".", template)
				}
			} else {
				template, err = setupDetectedTemplate(".", project, options, candidates)
			}
			if err != nil {
				return err
			}
			template, err = applySetupNetworkPolicy(template, options)
			if err != nil {
				return err
			}
			fmt.Print(template)
		} else {
			template, err := applySetupNetworkPolicy(remoteCIProjectTemplateWithProfile(project, *image, *profile), setupOptions{NetworkAllowlist: reviewedNetwork})
			if err != nil {
				return err
			}
			fmt.Print(template)
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
		APIURL:           *apiURL,
		APIToken:         *apiToken,
		ProjectID:        reportProjectID,
		CheckName:        *checkName,
		Profile:          *profile,
		Image:            *image,
		WriteConfig:      *writeConfig,
		WriteMakefile:    *writeMakefile,
		WriteHelpers:     *writeHelpers,
		Detect:           *detect,
		Force:            *force,
		ImportSource:     *importSource,
		ImportCandidate:  *importCandidate,
		NetworkAllowlist: reviewedNetwork,
		RuntimePins:      runtimePins,
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
