package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/DeanStr/readyci-cli/internal/api"
)

func cacheCommand(args []string) error {
	if len(args) == 0 {
		printUsage()
		return cliConfigError("usage: readyci cache <ls|purge> [project]", nil)
	}
	switch args[0] {
	case "ls":
		return cacheListCommand(args[1:])
	case "purge":
		return cachePurgeCommand(args[1:])
	default:
		printUsage()
		return cliConfigError(fmt.Sprintf("unknown cache command %q", args[0]), nil)
	}
}

func cacheListCommand(args []string) error {
	fs := flag.NewFlagSet("cache ls", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	projectID := fs.String("project", envDefault("READYCI_PROJECT_ID", ""), "project id")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() > 1 {
		return cliConfigError("usage: readyci cache ls [--api-url URL] [project]", nil)
	}
	if fs.NArg() == 1 {
		*projectID = fs.Arg(0)
	}
	projectIDValue, err := effectiveProjectID(*apiURL, *projectID)
	if err != nil {
		return err
	}

	ctx, stop := signalContext()
	defer stop()

	token, err := effectiveAPIToken(*apiURL, *apiToken)
	if err != nil {
		return err
	}
	usage, err := api.NewClient(*apiURL, api.WithToken(token)).CacheUsage(ctx, projectIDValue)
	if err != nil {
		return err
	}
	if len(usage.Lanes) == 0 {
		fmt.Printf("no cache lanes for project %s\n", usage.ProjectID)
		return nil
	}
	fmt.Printf("cache lanes for project %s\n", usage.ProjectID)
	for _, lane := range usage.Lanes {
		fmt.Printf("%s worker=%s index=%d status=%s quota_bytes=%d\n",
			lane.ID, lane.WorkerID, lane.LaneIndex, lane.Status, lane.QuotaBytes)
	}
	return nil
}

func cachePurgeCommand(args []string) error {
	fs := flag.NewFlagSet("cache purge", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	projectID := fs.String("project", envDefault("READYCI_PROJECT_ID", ""), "project id")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() > 1 {
		return cliConfigError("usage: readyci cache purge [--api-url URL] [project]", nil)
	}
	if fs.NArg() == 1 {
		*projectID = fs.Arg(0)
	}
	projectIDValue, err := effectiveProjectID(*apiURL, *projectID)
	if err != nil {
		return err
	}

	ctx, stop := signalContext()
	defer stop()

	token, err := effectiveAPIToken(*apiURL, *apiToken)
	if err != nil {
		return err
	}
	resp, err := api.NewClient(*apiURL, api.WithToken(token)).PurgeCache(ctx, projectIDValue)
	if err != nil {
		return err
	}
	fmt.Printf("purged %d cache lane(s) for project %s\n", resp.PurgedLanes, resp.ProjectID)
	return nil
}
