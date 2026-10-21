package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

const inferredConfigPath = "<inferred .readyci.yml>"

var errCouldNotInferSafeCheck = errors.New("could not infer a safe check")

type loadedReadyCIConfig struct {
	Config   config.ProjectConfig
	Path     string
	Inferred bool
	Signals  []string
}

func loadProjectConfigOrInfer(cwd string) (loadedReadyCIConfig, error) {
	return loadProjectConfigOrInferProject(cwd, "")
}

func loadProjectConfigOrInferProject(cwd, projectID string) (loadedReadyCIConfig, error) {
	cfg, path, err := config.LoadProjectConfig(cwd)
	if err != nil {
		return loadedReadyCIConfig{}, err
	}
	if path != "" {
		return loadedReadyCIConfig{Config: cfg, Path: path}, nil
	}
	inferred, signals, err := inferProjectConfigProject(cwd, projectID)
	if err != nil {
		return loadedReadyCIConfig{}, err
	}
	return loadedReadyCIConfig{
		Config:   inferred,
		Path:     inferredConfigPath,
		Inferred: true,
		Signals:  signals,
	}, nil
}

func inferProjectConfig(cwd string) (config.ProjectConfig, []string, error) {
	return inferProjectConfigProject(cwd, "")
}

func inferProjectConfigProject(cwd, projectID string) (config.ProjectConfig, []string, error) {
	root, err := inferenceRoot(cwd)
	if err != nil {
		return config.ProjectConfig{}, nil, err
	}
	template, templateErr := inferredProjectTemplateWithError(root, projectID)
	if templateErr != nil {
		return config.ProjectConfig{}, nil, fmt.Errorf("%w: %w", errCouldNotInferSafeCheck, templateErr)
	}
	if strings.TrimSpace(template) == "" {
		return config.ProjectConfig{}, nil, fmt.Errorf("%w; run readyci setup --print to inspect a starter config or readyci plan --recommend --suggest-config for hosted suggestions", errCouldNotInferSafeCheck)
	}
	signals := detectedTemplateSignals(template)
	if len(signals) == 0 {
		signals = []string{"conservative local starter"}
	}
	cfg, err := config.ParseProjectConfig([]byte(template), inferredConfigPath)
	if err != nil {
		return config.ProjectConfig{}, nil, err
	}
	if len(cfg.Checks) == 0 {
		return config.ProjectConfig{}, nil, fmt.Errorf("readyci check inferred repo signals but no runnable checks; run readyci setup --print to inspect the generated config")
	}
	return cfg, signals, nil
}

func inferredProjectTemplate(cwd, projectID string) string {
	template, _ := inferredProjectTemplateWithError(cwd, projectID)
	return template
}

func inferredProjectTemplateWithError(cwd, projectID string) (string, error) {
	root, err := inferenceRoot(cwd)
	if err != nil {
		root = cwd
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		projectID = strings.TrimSpace(os.Getenv("READYCI_PROJECT_ID"))
	}
	if projectID == "" {
		projectID = inferProjectID(root)
	}
	return setupDetectedTemplate(root, projectID, setupOptions{Detect: true, Profile: "remote-ci"}, setupImportCandidates(root, ""))
}

func inferenceRoot(cwd string) (string, error) {
	root, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	if cwd == "." {
		if wd, wdErr := os.Getwd(); wdErr == nil {
			root = wd
		}
	}
	if gitRoot, err := gitOutput(context.Background(), "-C", root, "rev-parse", "--show-toplevel"); err == nil && strings.TrimSpace(gitRoot) != "" {
		if abs, absErr := filepath.Abs(strings.TrimSpace(gitRoot)); absErr == nil {
			return abs, nil
		}
		return strings.TrimSpace(gitRoot), nil
	}
	return root, nil
}

func detectedTemplateSignals(template string) []string {
	var signals []string
	inSignals := false
	for _, line := range strings.Split(template, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "# detected project signals:" {
			inSignals = true
			continue
		}
		if !inSignals {
			continue
		}
		if trimmed == "" {
			break
		}
		if !strings.HasPrefix(trimmed, "# - ") {
			break
		}
		signal := strings.TrimSpace(strings.TrimPrefix(trimmed, "# - "))
		if signal == "" {
			continue
		}
		signals = append(signals, signal)
	}
	return signals
}

func printInferredConfigNotice(loaded loadedReadyCIConfig, selected string) {
	if !loaded.Inferred {
		return
	}
	names := sortedCheckNames(loaded.Config.Checks)
	fmt.Fprintf(os.Stderr, "readyci: no .readyci.yml found; inferred %d check(s): %s\n", len(names), strings.Join(names, ", "))
	for _, signal := range loaded.Signals {
		fmt.Fprintf(os.Stderr, "readyci: inferred: %s\n", signal)
	}
	if strings.TrimSpace(selected) != "" {
		fmt.Fprintf(os.Stderr, "readyci: selected %s; save or edit this plan with readyci setup --print\n", selected)
	}
}

func defaultCheckSelection(cfg config.ProjectConfig, goal string) (agentCheckRecommendation, error) {
	return recommendAgentCheck(cfg, "", goal)
}

func executeDefaultInferredCheck(cwd string, loaded loadedReadyCIConfig, resolveOptions checkResolveOptions, executionOptions checkExecutionOptions) error {
	cfg := loaded.Config
	executionOptions.Inferred = loaded.Inferred
	if len(executionOptions.Signals) == 0 {
		executionOptions.Signals = append([]string(nil), loaded.Signals...)
	}
	if shouldUseAffectedRules(cfg, "") {
		ctx, stop := signalContext()
		defer stop()
		plans, _, _, err := buildAgentAffectedRulePlans(ctx, cwd, cfg, loaded.Path, resolveOptions)
		if err != nil {
			return err
		}
		if len(plans) > 0 {
			executionOptions.SelectionMode = "affected_rules"
			executionOptions.SelectedReason = "Inferred affected rules matched changed files."
			printInferredConfigNotice(loaded, "affected checks")
			return executeAffectedRules(cwd, cfg, loaded.Path, resolveOptions, executionOptions)
		}
		fmt.Fprintln(os.Stderr, "readyci: no inferred affected checks matched changed files; falling back to the narrowest detected check")
	}
	recommendation, err := defaultCheckSelection(cfg, "")
	if err != nil {
		return err
	}
	executionOptions.SelectionMode = "inferred_fallback"
	executionOptions.SelectedReason = "No inferred affected checks matched changed files; selected the narrowest detected check."
	printInferredConfigNotice(loaded, recommendation.Check)
	return executeResolvedCheck(recommendation.Check, cfg, loaded.Path, resolveOptions, executionOptions)
}
