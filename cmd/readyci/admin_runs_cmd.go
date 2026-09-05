package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func adminRunsCommand(args []string) error {
	fs := flag.NewFlagSet("admin runs", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, adminToken := adminAPIFlags(fs)
	projectID := fs.String("project", "", "filter by project id")
	status := fs.String("status", "", "filter by run status")
	limit := fs.Int("limit", 50, "maximum runs to list")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci admin runs [--api-url URL] [--project PROJECT] [--status STATUS] [--limit N]", nil)
	}

	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()

	resp, err := api.NewClient(*apiURL, api.WithToken(*adminToken)).AdminListRuns(ctx, *projectID, *status, *limit)
	if err != nil {
		return err
	}
	if len(resp.Runs) == 0 {
		fmt.Println("no runs")
		return nil
	}
	fmt.Printf("%-20s %-16s %-11s %-12s %-7s %s\n", "RUN ID", "PROJECT", "STATUS", "WORKER", "EVENTS", "COMMAND")
	for _, run := range resp.Runs {
		fmt.Printf("%-20s %-16s %-11s %-12s %-7d %s\n",
			run.RunID, truncate(run.ProjectID, 16), run.Status, truncate(run.WorkerID, 12), run.EventCount, truncate(oneLine(run.Command), 80))
	}
	return nil
}

func adminInspectCommand(args []string) error {
	fs := flag.NewFlagSet("admin inspect", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, adminToken := adminAPIFlags(fs)
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 1 {
		return cliConfigError("usage: readyci admin inspect [--api-url URL] RUN_ID", nil)
	}

	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()

	detail, err := api.NewClient(*apiURL, api.WithToken(*adminToken)).AdminGetRun(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	printRunDetail(detail)
	return nil
}

func adminEventsCommand(args []string) error {
	fs := flag.NewFlagSet("admin events", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, adminToken := adminAPIFlags(fs)
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 1 {
		return cliConfigError("usage: readyci admin events [--api-url URL] RUN_ID", nil)
	}

	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()

	resp, err := api.NewClient(*apiURL, api.WithToken(*adminToken)).AdminListEvents(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	for _, event := range resp.Events {
		if err := encoder.Encode(event); err != nil {
			return err
		}
	}
	return nil
}

func adminLogsCommand(args []string) error {
	fs := flag.NewFlagSet("admin logs", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, adminToken := adminAPIFlags(fs)
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 1 {
		return cliConfigError("usage: readyci admin logs [--api-url URL] RUN_ID", nil)
	}

	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()

	resp, err := api.NewClient(*apiURL, api.WithToken(*adminToken)).AdminListEvents(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	for _, event := range resp.Events {
		switch event.Type {
		case preflightapi.EventStdout:
			data, err := event.DataBytes()
			if err != nil {
				return err
			}
			if _, err := os.Stdout.Write(data); err != nil {
				return err
			}
		case preflightapi.EventStderr:
			data, err := event.DataBytes()
			if err != nil {
				return err
			}
			if _, err := os.Stderr.Write(data); err != nil {
				return err
			}
		case preflightapi.EventLogTruncated:
			if err := writeLogTruncationNotice(event); err != nil {
				return err
			}
		}
	}
	return nil
}

func adminCancelCommand(args []string) error {
	fs := flag.NewFlagSet("admin cancel", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, adminToken := adminAPIFlags(fs)
	reason := fs.String("reason", "", "cancellation reason")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 1 {
		return cliConfigError("usage: readyci admin cancel [--api-url URL] --reason TEXT RUN_ID", nil)
	}
	reasonValue := strings.TrimSpace(*reason)
	if reasonValue == "" {
		return cliConfigError("readyci admin cancel requires --reason", nil)
	}

	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()

	resp, err := api.NewClient(*apiURL, api.WithToken(*adminToken)).AdminCancelRun(ctx, fs.Arg(0), reasonValue)
	if err != nil {
		return err
	}
	fmt.Printf("%s status=%s\n", resp.RunID, resp.Status)
	return nil
}
