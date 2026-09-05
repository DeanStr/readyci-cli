package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/DeanStr/readyci-cli/internal/api"
)

func adminTeamsCommand(args []string) error {
	if len(args) == 0 {
		printUsage()
		return cliConfigError("usage: readyci admin teams <list|disable|cancel-runs>", nil)
	}
	switch args[0] {
	case "list":
		return adminTeamsListCommand(args[1:])
	case "disable":
		return adminTeamsDisableCommand(args[1:])
	case "cancel-runs":
		return adminTeamsCancelRunsCommand(args[1:])
	default:
		printUsage()
		return cliConfigError(fmt.Sprintf("unknown admin teams command %q", args[0]), nil)
	}
}

func adminTeamsListCommand(args []string) error {
	fs := flag.NewFlagSet("admin teams list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, adminToken := adminAPIFlags(fs)
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci admin teams list [--api-url URL] [--token TOKEN]", nil)
	}
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()

	resp, err := api.NewClient(*apiURL, api.WithToken(*adminToken)).AdminListTeams(ctx)
	if err != nil {
		return err
	}
	if len(resp.Teams) == 0 {
		fmt.Println("no teams")
		return nil
	}
	fmt.Printf("%-22s %-18s %-10s %-9s %-7s %-7s %-7s %-7s\n", "TEAM ID", "NAME", "PLAN", "STATUS", "AWAIT", "QUEUED", "ACTIVE", "TOKENS")
	for _, team := range resp.Teams {
		status := "active"
		if team.DisabledAt != nil {
			status = "disabled"
		}
		fmt.Printf("%-22s %-18s %-10s %-9s %-7d %-7d %-7d %-7d\n",
			team.ID, truncate(team.Name, 18), team.Plan, status,
			team.AwaitingUploadRuns, team.QueuedRuns, team.ActiveRuns, team.ActiveAPITokens)
	}
	return nil
}

func adminTeamsDisableCommand(args []string) error {
	fs := flag.NewFlagSet("admin teams disable", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, adminToken := adminAPIFlags(fs)
	reason := fs.String("reason", "", "disable reason")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 1 {
		return cliConfigError("usage: readyci admin teams disable [--api-url URL] [--token TOKEN] --reason TEXT TEAM_ID", nil)
	}
	reasonValue := strings.TrimSpace(*reason)
	if reasonValue == "" {
		return cliConfigError("readyci admin teams disable requires --reason", nil)
	}
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()

	resp, err := api.NewClient(*apiURL, api.WithToken(*adminToken)).AdminDisableTeam(ctx, fs.Arg(0), reasonValue)
	if err != nil {
		return err
	}
	fmt.Printf("%s disabled_at=%s cancelled_runs=%d\n", resp.Team.ID, timeOrDash(resp.Team.DisabledAt), len(resp.CancelledRuns))
	return nil
}

func adminTeamsCancelRunsCommand(args []string) error {
	fs := flag.NewFlagSet("admin teams cancel-runs", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, adminToken := adminAPIFlags(fs)
	reason := fs.String("reason", "", "cancellation reason")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 1 {
		return cliConfigError("usage: readyci admin teams cancel-runs [--api-url URL] [--token TOKEN] --reason TEXT TEAM_ID", nil)
	}
	reasonValue := strings.TrimSpace(*reason)
	if reasonValue == "" {
		return cliConfigError("readyci admin teams cancel-runs requires --reason", nil)
	}
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()

	resp, err := api.NewClient(*apiURL, api.WithToken(*adminToken)).AdminCancelTeamRuns(ctx, fs.Arg(0), reasonValue)
	if err != nil {
		return err
	}
	fmt.Printf("%s cancelled_runs=%d\n", resp.TeamID, len(resp.CancelledRuns))
	for _, runID := range resp.CancelledRuns {
		fmt.Println(runID)
	}
	return nil
}
