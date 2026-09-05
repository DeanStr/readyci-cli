package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type startReport struct {
	ConfigPath string   `json:"config_path"`
	Inferred   bool     `json:"inferred"`
	ProjectID  string   `json:"project_id"`
	Checks     []string `json:"checks"`
	Selected   string   `json:"selected"`
	Signals    []string `json:"signals,omitempty"`
	Written    string   `json:"written,omitempty"`
	Next       []string `json:"next"`
}

func startCommand(args []string) error {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	projectID := fs.String("project", envDefault("READYCI_PROJECT_ID", ""), "project id")
	writeConfig := fs.Bool("write-config", false, "write the inferred .readyci.yml when no config exists")
	force := fs.Bool("force", false, "overwrite an existing Ready CI config with --write-config")
	runCheck := fs.Bool("run", false, "create the first remote check instead of only dry-running it")
	jsonOutput := fs.Bool("json", false, "print machine-readable JSON and skip the check dry-run")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci start [--write-config] [--run] [--json]", nil)
	}
	if *jsonOutput && *runCheck {
		return cliConfigError("--json cannot be combined with --run", nil)
	}
	if err := validateControlFreeValue("project_id", *projectID); err != nil {
		return err
	}
	var loaded loadedReadyCIConfig
	var err error
	if *writeConfig && *force {
		cfg, signals, inferErr := inferProjectConfigProject(".", *projectID)
		if inferErr != nil {
			return inferErr
		}
		loaded = loadedReadyCIConfig{
			Config:   cfg,
			Path:     inferredConfigPath,
			Inferred: true,
			Signals:  signals,
		}
	} else {
		loaded, err = loadProjectConfigOrInferProject(".", *projectID)
		if err != nil {
			return err
		}
	}
	resolveOptions := checkResolveOptions{
		APIURL:    *apiURL,
		ProjectID: *projectID,
	}
	selected, selectedCheckArg, err := startSelectedCheck(".", loaded, resolveOptions)
	if err != nil {
		return err
	}
	effectiveProjectID := strings.TrimSpace(*projectID)
	if effectiveProjectID == "" {
		effectiveProjectID = loaded.Config.ProjectID
	}
	if err := validateControlFreeValue("project_id", effectiveProjectID); err != nil {
		return err
	}
	report := startReport{
		ConfigPath: loaded.Path,
		Inferred:   loaded.Inferred,
		ProjectID:  effectiveProjectID,
		Checks:     sortedCheckNames(loaded.Config.Checks),
		Selected:   selected,
		Signals:    append([]string(nil), loaded.Signals...),
		Next:       []string{"readyci check", "readyci check --dry-run"},
	}
	if *writeConfig {
		if !loaded.Inferred {
			return cliConfigError("Ready CI config already exists; pass --force to overwrite it", nil)
		}
		path, err := startConfigPath(*force)
		if err != nil {
			return err
		}
		template := inferredProjectTemplate(".", *projectID)
		if err := writeSetupGeneratedFile(path, template, 0o644, *force); err != nil {
			return err
		}
		report.Written = path
		report.ConfigPath = path
	}
	if *jsonOutput {
		return writeJSONOutput(report)
	}
	printStartReport(report, *runCheck)
	checkArgs := []string{"--api-url", *apiURL}
	if strings.TrimSpace(*apiToken) != "" {
		checkArgs = append(checkArgs, "--token", *apiToken)
	}
	if strings.TrimSpace(*projectID) != "" {
		checkArgs = append(checkArgs, "--project", *projectID)
	}
	if !*runCheck {
		checkArgs = append(checkArgs, "--dry-run")
	}
	if selectedCheckArg != "" {
		checkArgs = append(checkArgs, selectedCheckArg)
	}
	return runStartCheckCommand(checkArgs, loaded.Inferred)
}

func printStartReport(report startReport, runCheck bool) {
	fmt.Println("Ready CI start")
	if report.Inferred {
		fmt.Printf("config: inferred in memory\n")
	} else {
		fmt.Printf("config: %s\n", report.ConfigPath)
	}
	if report.Written != "" {
		fmt.Printf("wrote: %s\n", report.Written)
	}
	if report.ProjectID != "" {
		fmt.Printf("project: %s\n", report.ProjectID)
	}
	if len(report.Signals) > 0 {
		fmt.Println("detected:")
		for _, signal := range report.Signals {
			fmt.Printf("  - %s\n", signal)
		}
	}
	fmt.Printf("checks: %s\n", strings.Join(report.Checks, ", "))
	fmt.Printf("selected: %s\n", report.Selected)
	if runCheck {
		fmt.Println("running: readyci check")
	} else {
		fmt.Println("previewing: readyci check --dry-run")
	}
}

func runStartCheckCommand(args []string, fromRoot bool) error {
	if !fromRoot {
		return checkCommand(args)
	}
	root, err := inferenceRoot(".")
	if err != nil {
		return err
	}
	previous, err := os.Getwd()
	if err != nil {
		return err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	previousAbs, err := filepath.Abs(previous)
	if err != nil {
		return err
	}
	if previousAbs == rootAbs {
		return checkCommand(args)
	}
	if err := os.Chdir(rootAbs); err != nil {
		return err
	}
	defer func() {
		_ = os.Chdir(previous)
	}()
	return checkCommand(args)
}

func startSelectedCheck(cwd string, loaded loadedReadyCIConfig, resolveOptions checkResolveOptions) (string, string, error) {
	if shouldUseAffectedRules(loaded.Config, "") {
		if loaded.Inferred {
			ctx, stop := signalContext()
			defer stop()
			plans, _, _, err := buildAgentAffectedRulePlans(ctx, cwd, loaded.Config, loaded.Path, resolveOptions)
			if err != nil {
				return "", "", err
			}
			if len(plans) == 0 {
				recommendation, err := defaultCheckSelection(loaded.Config, "")
				if err != nil {
					return "", "", err
				}
				return recommendation.Check, recommendation.Check, nil
			}
		}
		return "affected checks", "", nil
	}
	recommendation, err := defaultCheckSelection(loaded.Config, "")
	if err != nil {
		return "", "", err
	}
	return recommendation.Check, recommendation.Check, nil
}

func startConfigPath(force bool) (string, error) {
	root, err := inferenceRoot(".")
	if err != nil {
		return "", err
	}
	for _, existingPath := range []string{".readyci.yml", ".readyci.yaml"} {
		fullPath := filepath.Join(root, existingPath)
		if _, err := os.Stat(fullPath); err == nil {
			if force {
				return existingPath, nil
			}
			return "", fmt.Errorf("%s already exists; use --force to overwrite", existingPath)
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	return ".readyci.yml", nil
}
