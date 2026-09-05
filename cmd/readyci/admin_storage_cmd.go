package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func adminStorageCommand(args []string) error {
	if len(args) == 0 {
		printUsage()
		return cliConfigError("usage: readyci admin storage <audit|cleanup-expired-artifacts|cleanup-cache-lanes|cleanup-status|cleanup-cancel>", nil)
	}
	switch args[0] {
	case "audit":
		return adminStorageAuditCommand(args[1:])
	case "cleanup-expired-artifacts":
		return adminStorageCleanupExpiredArtifactsCommand(args[1:])
	case "cleanup-cache-lanes":
		return adminStorageCleanupCacheLanesCommand(args[1:])
	case "cleanup-status":
		return adminStorageCleanupStatusCommand(args[1:])
	case "cleanup-cancel":
		return adminStorageCleanupCancelCommand(args[1:])
	default:
		printUsage()
		return cliConfigError(fmt.Sprintf("unknown admin storage command %q", args[0]), nil)
	}
}

func adminStorageAuditCommand(args []string) error {
	fs := flag.NewFlagSet("admin storage audit", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, adminToken := adminAPIFlags(fs)
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci admin storage audit [--api-url URL] [--token TOKEN]", nil)
	}
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()

	audit, err := api.NewClient(*apiURL, api.WithToken(*adminToken)).AdminStorageAudit(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("upload_store: %s\n", audit.UploadStoreRoot)
	fmt.Printf("artifact_store: %s\n", audit.ArtifactStoreRoot)
	fmt.Printf("awaiting_upload_runs: %d\n", audit.AwaitingUploadRuns)
	fmt.Printf("queued_runs: %d\n", audit.QueuedRuns)
	fmt.Printf("artifact_records: %d\n", audit.ArtifactRecords)
	fmt.Printf("expired_artifact_records: %d\n", audit.ExpiredArtifactRecords)
	fmt.Printf("malformed_artifact_records: %d\n", audit.MalformedArtifactRecords)
	fmt.Printf("terminal_runs_with_bundles: %d\n", audit.TerminalRunsWithBundles)
	fmt.Printf("requeued_artifact_records: %d\n", audit.RequeuedArtifactRecords)
	fmt.Printf("queued_worker_local_bundles: %d\n", audit.QueuedWorkerLocalBundles)
	fmt.Printf("pending_cache_lane_deletes: %d\n", audit.PendingCacheLaneDeletes)
	fmt.Printf("workers_not_seen_recently: %d\n", audit.WorkersNotSeenRecently)
	for _, note := range audit.Notes {
		fmt.Printf("note: %s\n", note)
	}
	return nil
}

func adminStorageCleanupExpiredArtifactsCommand(args []string) error {
	fs := flag.NewFlagSet("admin storage cleanup-expired-artifacts", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, adminToken := adminAPIFlags(fs)
	dryRun := fs.Bool("dry-run", false, "preview expired artifacts without deleting objects or metadata")
	limit := fs.Int("limit", 100, "maximum expired artifact records to clean")
	minAgeDays := fs.Int("min-age-days", 0, "only clean artifacts created at least this many days ago")
	includeMalformedArtifacts := fs.Bool("include-malformed-artifacts", false, "also clean malformed artifact metadata records")
	includeStaleUploads := fs.Bool("include-stale-uploads", false, "also clean expired upload objects and metadata")
	includeRunLogs := fs.Bool("include-run-logs", false, "also delete old stdout/stderr run log events for terminal runs")
	logMinAgeDays := fs.Int("log-min-age-days", 30, "only clean run log events created at least this many days ago")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci admin storage cleanup-expired-artifacts [--api-url URL] [--token TOKEN] [--dry-run] [--limit N] [--min-age-days N] [--include-malformed-artifacts] [--include-stale-uploads] [--include-run-logs] [--log-min-age-days N]", nil)
	}
	if *limit <= 0 {
		return cliConfigError("--limit must be positive", nil)
	}
	if *minAgeDays < 0 {
		return cliConfigError("--min-age-days must be non-negative", nil)
	}
	if *logMinAgeDays < 0 {
		return cliConfigError("--log-min-age-days must be non-negative", nil)
	}
	const secondsPerDay = int64(24 * 60 * 60)
	if int64(*minAgeDays) > preflightapi.MaxTimeoutSeconds/secondsPerDay {
		return cliConfigError("--min-age-days is too large", nil)
	}
	if int64(*logMinAgeDays) > preflightapi.MaxTimeoutSeconds/secondsPerDay {
		return cliConfigError("--log-min-age-days is too large", nil)
	}
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()

	resp, err := api.NewClient(*apiURL, api.WithToken(*adminToken)).AdminCleanupExpiredArtifacts(ctx, preflightapi.AdminCleanupExpiredArtifactsRequest{
		DryRun:                    *dryRun,
		Limit:                     *limit,
		MinAgeSeconds:             int64(*minAgeDays) * secondsPerDay,
		IncludeMalformedArtifacts: *includeMalformedArtifacts,
		IncludeStaleUploads:       *includeStaleUploads,
		IncludeRunLogs:            *includeRunLogs,
		LogMinAgeSeconds:          int64(*logMinAgeDays) * secondsPerDay,
	})
	if err != nil {
		return err
	}
	fmt.Printf("dry_run: %t\n", resp.DryRun)
	fmt.Printf("expired_artifacts: %d\n", resp.ExpiredArtifacts)
	fmt.Printf("malformed_artifact_records: %d\n", resp.MalformedArtifactRecords)
	fmt.Printf("expired_uploads: %d\n", resp.ExpiredUploads)
	fmt.Printf("expired_log_events: %d\n", resp.ExpiredLogEvents)
	fmt.Printf("deleted_artifact_records: %d\n", resp.DeletedArtifacts)
	fmt.Printf("deleted_malformed_artifact_records: %d\n", resp.DeletedMalformedArtifacts)
	fmt.Printf("deleted_upload_records: %d\n", resp.DeletedUploads)
	fmt.Printf("deleted_log_events: %d\n", resp.DeletedLogEvents)
	fmt.Printf("deleted_objects: %d\n", resp.DeletedObjects)
	fmt.Printf("failed_objects: %d\n", len(resp.FailedObjects))
	printMaintenanceJob(resp.Job)
	for _, failed := range resp.FailedObjects {
		fmt.Printf("failed_object: %s\n", failed)
	}
	for _, artifact := range resp.Artifacts {
		fmt.Printf("artifact: %s run=%s path=%s expires_at=%s size_bytes=%d\n",
			artifact.ID,
			artifact.RunID,
			artifact.Path,
			artifact.ExpiresAt.Format(time.RFC3339),
			artifact.SizeBytes,
		)
	}
	for _, upload := range resp.Uploads {
		fmt.Printf("upload: run=%s object=%s expires_at=%s size_bytes=%d\n",
			upload.RunID,
			upload.ObjectKey,
			upload.ExpiresAt.Format(time.RFC3339),
			upload.SizeBytes,
		)
	}
	if !resp.DryRun && len(resp.FailedObjects) > 0 {
		return fmt.Errorf("failed to delete %d artifact object(s)", len(resp.FailedObjects))
	}
	return nil
}

func adminStorageCleanupCacheLanesCommand(args []string) error {
	fs := flag.NewFlagSet("admin storage cleanup-cache-lanes", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, adminToken := adminAPIFlags(fs)
	dryRun := fs.Bool("dry-run", false, "preview cache lanes without marking them for deletion")
	limit := fs.Int("limit", 100, "maximum cache lanes to mark deleted")
	minAgeDays := fs.Int("min-age-days", 14, "only clean duplicate region lanes last used at least this many days ago")
	nonHomeMinAgeDays := fs.Int("non-home-min-age-days", 1, "only clean non-home-region lanes last used at least this many days ago")
	keepLanesPerProjectRegion := fs.Int("keep-lanes-per-project-region", 1, "available lanes to keep for each project/region before duplicate cleanup")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci admin storage cleanup-cache-lanes [--api-url URL] [--token TOKEN] [--dry-run] [--limit N] [--min-age-days N] [--non-home-min-age-days N] [--keep-lanes-per-project-region N]", nil)
	}
	if *limit <= 0 {
		return cliConfigError("--limit must be positive", nil)
	}
	if *minAgeDays <= 0 {
		return cliConfigError("--min-age-days must be positive", nil)
	}
	if *nonHomeMinAgeDays <= 0 {
		return cliConfigError("--non-home-min-age-days must be positive", nil)
	}
	if *keepLanesPerProjectRegion <= 0 {
		return cliConfigError("--keep-lanes-per-project-region must be positive", nil)
	}
	const secondsPerDay = int64(24 * 60 * 60)
	if int64(*minAgeDays) > preflightapi.MaxTimeoutSeconds/secondsPerDay {
		return cliConfigError("--min-age-days is too large", nil)
	}
	if int64(*nonHomeMinAgeDays) > preflightapi.MaxTimeoutSeconds/secondsPerDay {
		return cliConfigError("--non-home-min-age-days is too large", nil)
	}
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()

	resp, err := api.NewClient(*apiURL, api.WithToken(*adminToken)).AdminCleanupCacheLanes(ctx, preflightapi.AdminCleanupCacheLanesRequest{
		DryRun:                    *dryRun,
		Limit:                     *limit,
		MinAgeSeconds:             int64(*minAgeDays) * secondsPerDay,
		NonHomeMinAgeSeconds:      int64(*nonHomeMinAgeDays) * secondsPerDay,
		KeepLanesPerProjectRegion: *keepLanesPerProjectRegion,
	})
	if err != nil {
		return err
	}
	fmt.Printf("dry_run: %t\n", resp.DryRun)
	fmt.Printf("candidate_lanes: %d\n", resp.Candidates)
	fmt.Printf("deleted_lanes: %d\n", resp.DeletedLanes)
	for _, lane := range resp.Lanes {
		lastUsedAt := "-"
		if lane.LastUsedAt != nil {
			lastUsedAt = lane.LastUsedAt.Format(time.RFC3339)
		}
		fmt.Printf("cache_lane: %s project=%s worker=%s region=%s home_region=%s index=%d reason=%s quota_bytes=%d last_used_at=%s\n",
			lane.ID,
			lane.ProjectID,
			lane.WorkerID,
			lane.Region,
			lane.HomeRegion,
			lane.LaneIndex,
			lane.Reason,
			lane.QuotaBytes,
			lastUsedAt,
		)
	}
	return nil
}

func adminStorageCleanupStatusCommand(args []string) error {
	fs := flag.NewFlagSet("admin storage cleanup-status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, adminToken := adminAPIFlags(fs)
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci admin storage cleanup-status [--api-url URL] [--token TOKEN]", nil)
	}
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()

	resp, err := api.NewClient(*apiURL, api.WithToken(*adminToken)).AdminCleanupExpiredArtifactsStatus(ctx)
	if err != nil {
		return err
	}
	if resp.Job == nil {
		fmt.Println("no cleanup jobs")
		return nil
	}
	printMaintenanceJob(resp.Job)
	return nil
}

func adminStorageCleanupCancelCommand(args []string) error {
	fs := flag.NewFlagSet("admin storage cleanup-cancel", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, adminToken := adminAPIFlags(fs)
	reason := fs.String("reason", "", "operator-facing cleanup cancellation reason")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci admin storage cleanup-cancel [--api-url URL] [--token TOKEN] --reason TEXT", nil)
	}
	reasonValue := strings.TrimSpace(*reason)
	if reasonValue == "" {
		return cliConfigError("readyci admin storage cleanup-cancel requires --reason", nil)
	}
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()

	resp, err := api.NewClient(*apiURL, api.WithToken(*adminToken)).AdminCancelCleanupExpiredArtifacts(ctx, preflightapi.AdminCancelCleanupExpiredArtifactsRequest{Reason: reasonValue})
	if err != nil {
		return err
	}
	printMaintenanceJob(resp.Job)
	return nil
}

func printMaintenanceJob(job *preflightapi.MaintenanceJobSummary) {
	if job == nil {
		return
	}
	fmt.Printf("job_id: %s\n", job.ID)
	fmt.Printf("job_type: %s\n", job.Type)
	fmt.Printf("job_status: %s\n", job.Status)
	fmt.Printf("job_dry_run: %t\n", job.DryRun)
	fmt.Printf("job_started_at: %s\n", job.StartedAt.Format(time.RFC3339))
	if job.FinishedAt != nil {
		fmt.Printf("job_finished_at: %s\n", job.FinishedAt.Format(time.RFC3339))
	}
	fmt.Printf("job_progress: %d/%d\n", job.ProgressCurrent, job.ProgressTotal)
	fmt.Printf("job_deleted_artifacts: %d\n", job.DeletedArtifacts)
	fmt.Printf("job_deleted_objects: %d\n", job.DeletedObjects)
	fmt.Printf("job_failed_objects: %d\n", job.FailedObjects)
	if job.Error != "" {
		fmt.Printf("job_error: %s\n", job.Error)
	}
	if job.Metadata["cancel_requested"] == "true" {
		fmt.Println("job_cancel_requested: true")
	}
	if reason := strings.TrimSpace(job.Metadata["cancel_reason"]); reason != "" {
		fmt.Printf("job_cancel_reason: %s\n", reason)
	}
}
