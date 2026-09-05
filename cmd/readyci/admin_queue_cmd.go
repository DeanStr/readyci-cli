package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/DeanStr/readyci-cli/internal/api"
)

func adminQueueCommand(args []string) error {
	if len(args) == 0 {
		printUsage()
		return cliConfigError("usage: readyci admin queue <status|explain>", nil)
	}
	switch args[0] {
	case "status":
		return adminQueueStatusCommand(args[1:])
	case "explain":
		return adminQueueExplainCommand(args[1:])
	default:
		printUsage()
		return cliConfigError(fmt.Sprintf("unknown admin queue command %q", args[0]), nil)
	}
}

func adminQueueStatusCommand(args []string) error {
	fs := flag.NewFlagSet("admin queue status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, adminToken := adminAPIFlags(fs)
	jsonOutput := fs.Bool("json", false, "print machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci admin queue status [--api-url URL] [--token TOKEN] [--json]", nil)
	}
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()

	status, err := api.NewClient(*apiURL, api.WithToken(*adminToken)).AdminQueueStatus(ctx)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSONOutput(status)
	}
	fmt.Printf("awaiting_upload_runs: %d\n", status.AwaitingUploadRuns)
	fmt.Printf("stale_awaiting_upload_runs: %d\n", status.StaleAwaitingUploadRuns)
	fmt.Printf("oldest_awaiting_upload_age_seconds: %d\n", status.OldestAwaitingUploadAgeSeconds)
	fmt.Printf("queued_runs: %d\n", status.QueuedRuns)
	fmt.Printf("capacity_waiting_runs: %d\n", status.CapacityWaitingRuns)
	fmt.Printf("scheduled_runs: %d\n", status.ScheduledRuns)
	fmt.Printf("starting_vm_runs: %d\n", status.StartingVMRuns)
	fmt.Printf("running_runs: %d\n", status.RunningRuns)
	fmt.Printf("blocked_runs: %d\n", status.BlockedRuns)
	fmt.Printf("denied_runs: %d\n", status.DeniedRuns)
	fmt.Printf("stale_leases: %d\n", status.StaleLeases)
	fmt.Printf("fresh_workers: %d\n", status.FreshWorkers)
	fmt.Printf("stale_workers: %d\n", status.StaleWorkers)
	fmt.Printf("draining_workers: %d\n", status.DrainingWorkers)
	fmt.Printf("platform_events: %d\n", status.PlatformEvents)
	fmt.Printf("platform_event_discards: %d\n", status.PlatformDiscards)
	fmt.Printf("platform_event_errors: %d\n", status.PlatformErrors)
	fmt.Printf("updated_at: %s\n", status.UpdatedAt.Format(time.RFC3339))
	return nil
}

func adminQueueExplainCommand(args []string) error {
	fs := flag.NewFlagSet("admin queue explain", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, adminToken := adminAPIFlags(fs)
	jsonOutput := fs.Bool("json", false, "print machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 1 {
		return cliConfigError("usage: readyci admin queue explain [--api-url URL] [--token TOKEN] [--json] RUN_ID", nil)
	}
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()

	resp, err := api.NewClient(*apiURL, api.WithToken(*adminToken)).AdminQueueExplain(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSONOutput(resp)
	}
	fmt.Printf("run_id: %s\n", resp.RunID)
	fmt.Printf("project_id: %s\n", resp.ProjectID)
	fmt.Printf("status: %s\n", resp.Status)
	fmt.Printf("queue_state: %s\n", resp.QueueState)
	if resp.WorkerID != "" {
		fmt.Printf("worker_id: %s\n", resp.WorkerID)
	}
	if resp.QueuedAt != nil {
		fmt.Printf("queued_at: %s\n", resp.QueuedAt.Format(time.RFC3339))
	}
	if resp.LeaseExpiresAt != nil {
		fmt.Printf("lease_expires_at: %s\n", resp.LeaseExpiresAt.Format(time.RFC3339))
	}
	for _, reason := range resp.Reasons {
		fmt.Printf("reason: %s\n", reason)
	}
	if resp.WorkerCapability != nil {
		fmt.Printf("claimable: %t\n", resp.WorkerCapability.Claimable)
		fmt.Printf("upload_capable: %t\n", resp.WorkerCapability.UploadCapable)
		fmt.Printf("fresh_worker_count: %d\n", resp.WorkerCapability.FreshWorkerCount)
		fmt.Printf("matching_worker_count: %d\n", resp.WorkerCapability.MatchingWorkerCount)
		fmt.Printf("matching_upload_worker_count: %d\n", resp.WorkerCapability.MatchingUploadWorkerCount)
	}
	return nil
}
