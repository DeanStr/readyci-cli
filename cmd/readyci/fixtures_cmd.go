package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

func fixturesCommand(args []string) error {
	if len(args) == 0 {
		printUsage()
		return cliConfigError("usage: readyci fixtures update --paths PATTERN [--from RUN_ID|latest]", nil)
	}
	switch args[0] {
	case "update":
		return fixturesUpdateCommand(args[1:])
	default:
		printUsage()
		return cliConfigError(fmt.Sprintf("unknown fixtures command %q", args[0]), nil)
	}
}

func fixturesUpdateCommand(args []string) error {
	fs := flag.NewFlagSet("fixtures update", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	projectID := fs.String("project", envDefault("READYCI_PROJECT_ID", ""), "project id")
	from := fs.String("from", "latest", "source run id, or latest")
	dryRun := fs.Bool("dry-run", false, "preview fixture updates without writing files")
	jsonOutput := fs.Bool("json", false, "print machine-readable JSON")
	allowEmpty := fs.Bool("allow-empty", false, "allow zero artifact matches")
	var paths stringSliceFlag
	fs.Var(&paths, "paths", "artifact path glob to apply; repeatable")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 || len(paths) == 0 {
		return cliConfigError("usage: readyci fixtures update --paths PATTERN [--from RUN_ID|latest] [--dry-run] [--json] [--allow-empty]", nil)
	}
	runID := strings.TrimSpace(*from)
	if runID == "" {
		return cliConfigError("--from cannot be empty", nil)
	}
	if runID == "latest" {
		resolved, err := latestFixtureRunID(*apiURL, *apiToken, *projectID)
		if err != nil {
			return err
		}
		runID = resolved
	}
	return applyArtifactsFromRun(artifactApplyOptions{
		APIURL:     *apiURL,
		APIToken:   *apiToken,
		RunID:      runID,
		Paths:      []string(paths),
		DryRun:     *dryRun,
		JSONOutput: *jsonOutput,
		AllowEmpty: *allowEmpty,
	})
}

func latestFixtureRunID(apiURL, apiToken, projectID string) (string, error) {
	if err := validateControlFreeValue("project_id", projectID); err != nil {
		return "", err
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		cfg, _, err := config.LoadProjectConfig(".")
		if err != nil {
			return "", err
		}
		if err := validateControlFreeValue("project_id", cfg.ProjectID); err != nil {
			return "", err
		}
		projectID = cfg.ProjectID
	}
	if projectID == "" {
		return "", cliConfigError("fixtures update --from latest requires --project or project_id in .readyci.yml", nil)
	}
	client, err := authenticatedClient(apiURL, apiToken)
	if err != nil {
		return "", err
	}
	ctx, stop := signalContext()
	defer stop()
	resp, err := client.ListRuns(ctx, projectID, "succeeded", 1)
	if err != nil {
		return "", err
	}
	if len(resp.Runs) == 0 {
		return "", fmt.Errorf("no successful runs found for project %s", projectID)
	}
	runID := resp.Runs[0].RunID
	artifacts, err := client.ListArtifacts(ctx, runID)
	if err != nil {
		return "", fmt.Errorf("inspect artifacts for latest successful run %s: %w", runID, err)
	}
	if len(artifacts.Artifacts) == 0 {
		return "", fmt.Errorf("latest successful run %s has no artifacts; choose a run with artifacts or publish artifacts before updating fixtures", runID)
	}
	return runID, nil
}
