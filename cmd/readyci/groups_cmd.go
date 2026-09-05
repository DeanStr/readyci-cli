package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func groupsCommand(args []string) error {
	if len(args) == 0 {
		printUsage()
		return cliConfigError("usage: readyci groups <latest|status|logs|cancel>", nil)
	}
	switch args[0] {
	case "latest":
		return groupsLatestCommand(args[1:])
	case "status":
		return groupsStatusCommand(args[1:])
	case "logs":
		return groupsLogsCommand(args[1:])
	case "cancel":
		return groupsCancelCommand(args[1:])
	default:
		printUsage()
		return cliConfigError(fmt.Sprintf("unknown groups command %q", args[0]), nil)
	}
}

func groupsLatestCommand(args []string) error {
	fs := flag.NewFlagSet("groups latest", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	projectID := fs.String("project", envDefault("READYCI_PROJECT_ID", ""), "project id")
	jsonOutput := fs.Bool("json", false, "print machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci groups latest [--api-url URL] [--token TOKEN] [--project PROJECT] [--json]", nil)
	}
	projectIDValue, err := groupProjectID(*apiURL, *projectID)
	if err != nil {
		return err
	}
	client, err := authenticatedClient(*apiURL, *apiToken)
	if err != nil {
		return err
	}
	ctx, stop := signalContext()
	defer stop()
	detail, err := client.LatestRunGroup(ctx, projectIDValue)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSONOutput(detail)
	}
	printRunGroupDetail(detail)
	return nil
}

func groupsStatusCommand(args []string) error {
	fs := flag.NewFlagSet("groups status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	jsonOutput := fs.Bool("json", false, "print machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 1 {
		return cliConfigError("usage: readyci groups status [--api-url URL] [--token TOKEN] [--json] GROUP_ID", nil)
	}
	client, err := authenticatedClient(*apiURL, *apiToken)
	if err != nil {
		return err
	}
	ctx, stop := signalContext()
	defer stop()
	detail, err := client.GetRunGroup(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSONOutput(detail)
	}
	printRunGroupDetail(detail)
	return nil
}

func groupsLogsCommand(args []string) error {
	fs := flag.NewFlagSet("groups logs", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	tail := fs.Int("tail", 200, "max events to fetch per child run")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 1 {
		return cliConfigError("usage: readyci groups logs [--api-url URL] [--token TOKEN] [--tail N] GROUP_ID", nil)
	}
	if *tail <= 0 {
		return cliConfigError("--tail must be positive", nil)
	}
	client, err := authenticatedClient(*apiURL, *apiToken)
	if err != nil {
		return err
	}
	ctx, stop := signalContext()
	defer stop()
	detail, err := client.GetRunGroup(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	for _, child := range detail.Children {
		fmt.Printf("== %s %s (%s) ==\n", child.RunID, defaultString(child.CommandAlias, child.LogicalChildID), child.Status)
		events, err := client.ListRunEventsTail(ctx, child.RunID, *tail, false, true, true)
		if err != nil {
			return err
		}
		if err := writeHumanLogEntries(runLogEntries(events.Events, false, runLogFilters{})); err != nil {
			return err
		}
	}
	return nil
}

func groupsCancelCommand(args []string) error {
	fs := flag.NewFlagSet("groups cancel", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	reason := fs.String("reason", "", "cancellation reason")
	jsonOutput := fs.Bool("json", false, "print machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 1 {
		return cliConfigError("usage: readyci groups cancel [--api-url URL] [--token TOKEN] [--reason TEXT] [--json] GROUP_ID", nil)
	}
	client, err := authenticatedClient(*apiURL, *apiToken)
	if err != nil {
		return err
	}
	ctx, stop := signalContext()
	defer stop()
	resp, err := client.CancelRunGroup(ctx, fs.Arg(0), preflightapi.CancelRunGroupRequest{Reason: *reason})
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSONOutput(resp)
	}
	fmt.Printf("run_group_id: %s\n", resp.RunGroupID)
	fmt.Printf("cancelled_runs: %d\n", len(resp.CancelledRuns))
	for _, runID := range resp.CancelledRuns {
		fmt.Printf("cancelled: %s\n", runID)
	}
	fmt.Printf("status: %s\n", resp.Detail.Summary.Status)
	return nil
}
