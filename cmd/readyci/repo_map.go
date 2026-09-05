package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func planCommand(args []string) error {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	repoMap := fs.Bool("repo-map", false, "print the inferred repository verification map")
	recommend := fs.Bool("recommend", false, "send a redacted repo inventory to the Ready CI analyzer")
	apiURL, apiToken := apiFlags(fs)
	projectID := fs.String("project", envDefault("READYCI_PROJECT_ID", ""), "project id")
	printInventory := fs.Bool("print-inventory", false, "print the redacted analyzer inventory instead of sending it")
	suggestConfig := fs.Bool("suggest-config", false, "request a reviewable .readyci.yml suggestion from the hosted analyzer")
	jsonOutput := fs.Bool("json", false, "print machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 || (!*repoMap && !*recommend) || (*repoMap && *recommend) {
		return cliConfigError("usage: readyci plan --repo-map [--json] OR readyci plan --recommend [--print-inventory] [--suggest-config] [--json]", nil)
	}
	if *repoMap && (*printInventory || *suggestConfig) {
		return cliConfigError("readyci plan --repo-map does not accept --print-inventory or --suggest-config", nil)
	}
	if *printInventory && *suggestConfig {
		return cliConfigError("readyci plan --recommend accepts only one of --print-inventory or --suggest-config", nil)
	}
	loaded, err := loadRepoMapConfig(".")
	if err != nil {
		return err
	}
	ctx, stop := signalContext()
	defer stop()
	root := repoMapRoot(".", loaded.Path)
	report := buildRepoMapReport(ctx, root, loaded.Config, loaded.Path)
	report.Inferred = loaded.Inferred
	if *recommend {
		projectIDValue := strings.TrimSpace(*projectID)
		if err := validateControlFreeValue("project_id", *projectID); err != nil {
			return err
		}
		if projectIDValue == "" {
			projectIDValue = report.ProjectID
		}
		if err := validateControlFreeValue("project_id", projectIDValue); err != nil {
			return err
		}
		if projectIDValue == "" {
			projectIDValue = inferProjectID(root)
		}
		inventory := projectAnalysisInventoryFromRepoMap(report)
		inventory.ProjectID = projectIDValue
		if *printInventory {
			if *jsonOutput {
				return writeJSONOutput(inventory)
			}
			printProjectAnalysisInventory(inventory)
			return nil
		}
		client, err := authenticatedClient(*apiURL, *apiToken)
		if err != nil {
			return err
		}
		if *suggestConfig {
			suggestions, err := client.ConfigSuggestions(ctx, projectIDValue, preflightapi.ConfigSuggestionsRequest{Inventory: inventory})
			if err != nil {
				return err
			}
			if *jsonOutput {
				return writeJSONOutput(suggestions)
			}
			printProjectConfigSuggestions(suggestions)
			return nil
		}
		recommendations, err := client.AnalyzeProject(ctx, projectIDValue, preflightapi.AnalyzeProjectRequest{Inventory: inventory})
		if err != nil {
			return err
		}
		if *jsonOutput {
			return writeJSONOutput(recommendations)
		}
		printProjectRecommendations(recommendations)
		return nil
	}
	if *jsonOutput {
		return writeJSONOutput(report)
	}
	printRepoMapReport(report)
	return nil
}

func printProjectAnalysisInventory(inventory preflightapi.ProjectAnalysisInventory) {
	fmt.Println("plan: analyzer-inventory")
	if inventory.ProjectID != "" {
		fmt.Printf("project_id: %s\n", inventory.ProjectID)
	}
	if inventory.ConfigPath != "" {
		fmt.Printf("config: %s\n", inventory.ConfigPath)
	}
	fmt.Printf("checks: %d\n", len(inventory.Checks))
	fmt.Printf("signals: %d\n", len(inventory.Signals))
	fmt.Printf("files: %d\n", len(inventory.Files))
	fmt.Printf("packages: %d\n", len(inventory.Packages))
	for _, signal := range inventory.Signals {
		fmt.Printf("signal: %s", signal.Kind)
		if signal.Path != "" {
			fmt.Printf(" path=%s", signal.Path)
		}
		fmt.Println()
	}
	for _, file := range inventory.Files {
		fmt.Printf("file: %s", file.Path)
		if file.Kind != "" {
			fmt.Printf(" kind=%s", file.Kind)
		}
		fmt.Println()
	}
	for _, pkg := range inventory.Packages {
		fmt.Printf("package: %s ecosystem=%s", pkg.Path, pkg.Ecosystem)
		if pkg.PackageManager != "" {
			fmt.Printf(" package_manager=%s", pkg.PackageManager)
		}
		if len(pkg.Features) > 0 {
			fmt.Printf(" features=%s", strings.Join(pkg.Features, ","))
		}
		fmt.Println()
	}
}

func printProjectRecommendations(response preflightapi.AnalyzeProjectResponse) {
	fmt.Println("plan: recommendations")
	if response.ProjectID != "" {
		fmt.Printf("project_id: %s\n", response.ProjectID)
	}
	fmt.Printf("packs: %d\n", len(response.Packs))
	for _, pack := range response.Packs {
		fmt.Printf("pack: %s\n", pack.ID)
		if pack.Name != "" {
			fmt.Printf("  name: %s\n", pack.Name)
		}
		fmt.Printf("  version: %s\n", pack.Version)
		if pack.Confidence > 0 {
			fmt.Printf("  confidence: %.2f\n", pack.Confidence)
		}
		for _, rationale := range pack.Rationale {
			fmt.Printf("  rationale: %s\n", rationale)
		}
	}
	for _, warning := range response.Warnings {
		fmt.Printf("warning: %s\n", warning)
	}
}

func printProjectConfigSuggestions(response preflightapi.ConfigSuggestionsResponse) {
	fmt.Println("plan: config-suggestions")
	if response.ProjectID != "" {
		fmt.Printf("project_id: %s\n", response.ProjectID)
	}
	fmt.Printf("packs: %d\n", len(response.Packs))
	for _, pack := range response.Packs {
		fmt.Printf("pack: %s\n", pack.ID)
		if pack.Name != "" {
			fmt.Printf("  name: %s\n", pack.Name)
		}
		fmt.Printf("  version: %s\n", pack.Version)
		if pack.Confidence > 0 {
			fmt.Printf("  confidence: %.2f\n", pack.Confidence)
		}
		for _, rationale := range pack.Rationale {
			fmt.Printf("  rationale: %s\n", rationale)
		}
	}
	for _, warning := range response.Warnings {
		fmt.Printf("warning: %s\n", warning)
	}
	if strings.TrimSpace(response.SuggestedConfig) != "" {
		fmt.Println("suggested_config:")
		fmt.Print(response.SuggestedConfig)
		if !strings.HasSuffix(response.SuggestedConfig, "\n") {
			fmt.Println()
		}
	}
}

func importCommand(args []string) error {
	if len(args) == 0 {
		return cliConfigError("usage: readyci import make|github-actions [--json]", nil)
	}
	source := args[0]
	fs := flag.NewFlagSet("import "+source, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOutput := fs.Bool("json", false, "print machine-readable JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError(fmt.Sprintf("usage: readyci import %s [--json]", source), nil)
	}
	if source != "make" && source != "github-actions" {
		return cliConfigError(fmt.Sprintf("unknown import source %q (available: make, github-actions)", source), nil)
	}
	loaded, err := loadRepoMapConfig(".")
	if err != nil {
		return err
	}
	ctx, stop := signalContext()
	defer stop()
	repo := buildRepoMapReport(ctx, repoMapRoot(".", loaded.Path), loaded.Config, loaded.Path)
	repo.Inferred = loaded.Inferred
	report := buildRepoImportReport(source, repo)
	if *jsonOutput {
		return writeJSONOutput(report)
	}
	printRepoImportReport(report)
	return nil
}

func loadRepoMapConfig(cwd string) (loadedReadyCIConfig, error) {
	cfg, path, err := config.LoadProjectConfig(cwd)
	if err != nil {
		return loadedReadyCIConfig{}, err
	}
	if path != "" {
		return loadedReadyCIConfig{Config: cfg, Path: path}, nil
	}
	if inferred, inferErr := loadProjectConfigOrInfer(cwd); inferErr == nil {
		return inferred, nil
	}
	return loadedReadyCIConfig{Config: cfg}, nil
}

func buildRepoMapReport(ctx context.Context, cwd string, cfg config.ProjectConfig, configPath string) repoMapReport {
	report := repoMapReport{
		Status:     "ok",
		ConfigPath: configPath,
		Inferred:   configPath == inferredConfigPath,
		ProjectID:  cfg.ProjectID,
		Checks:     sortedCheckNames(cfg.Checks),
	}
	report.Make = scanRepoMake(cwd)
	report.CheckReferences = append(report.CheckReferences, checkReferencesFromMake(cwd, report.Make)...)
	docCheckRefs, makeRefs := scanRepoDocs(cwd)
	report.CheckReferences = append(report.CheckReferences, docCheckRefs...)
	report.MakeReferences = append(report.MakeReferences, makeRefs...)
	report.GitHubActions = scanRepoGitHubActions(cwd)
	report.CheckReferences = append(report.CheckReferences, checkReferencesFromGitHubActions(report.GitHubActions)...)
	report.MakeReferences = append(report.MakeReferences, makeReferencesFromGitHubActions(report.GitHubActions)...)
	report.GoModules = scanRepoGoModules(cwd)
	report.Cargo = scanRepoCargo(ctx, cwd)
	report.NodePackages = scanRepoNodePackages(cwd)
	report.OpenAPI = scanRepoOpenAPI(cwd, report.NodePackages, cfg)
	report.OpsPaths = scanRepoOpsPaths(cwd)
	report.ShellPaths = scanRepoShellPaths(cwd)
	report.Signals = repoSignalsFromMap(report)
	report.UndefinedChecks = undefinedRepoChecks(cfg, report.CheckReferences)
	report.UndefinedMake = undefinedRepoMakeTargets(report.Make, report.MakeReferences)
	report.Unreferenced = unreferencedRepoChecks(cfg, configPath, report.CheckReferences)
	if len(report.UndefinedChecks) > 0 || len(report.UndefinedMake) > 0 || len(report.Unreferenced) > 0 || len(report.Warnings) > 0 {
		report.Status = "warn"
	}
	return report
}

func repoMapRoot(cwd string, configPath string) string {
	configPath = strings.TrimSpace(configPath)
	if configPath != "" && !strings.HasPrefix(configPath, "<") {
		return filepath.Dir(configPath)
	}
	if root, err := inferenceRoot(cwd); err == nil && strings.TrimSpace(root) != "" {
		return root
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return cwd
	}
	return abs
}
