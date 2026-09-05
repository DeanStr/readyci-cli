package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func adminWorkersCommand(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "list":
			return adminWorkersListCommand(args[1:])
		case "drain":
			return adminWorkersDrainCommand(args[1:])
		case "undrain":
			return adminWorkersUndrainCommand(args[1:])
		case "delete":
			return adminWorkersDeleteCommand(args[1:])
		case "stage-release":
			return adminWorkersStageReleaseCommand(args[1:])
		case "promote-release":
			return adminWorkersPromoteReleaseCommand(args[1:])
		case "demote-release":
			return adminWorkersDemoteReleaseCommand(args[1:])
		}
	}
	return adminWorkersListCommand(args)
}

func adminWorkersListCommand(args []string) error {
	fs := flag.NewFlagSet("admin workers", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, adminToken := adminAPIFlags(fs)
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci admin workers [list] [--api-url URL] [--token TOKEN]", nil)
	}
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()

	resp, err := api.NewClient(*apiURL, api.WithToken(*adminToken)).AdminListWorkers(ctx)
	if err != nil {
		return err
	}
	if len(resp.Workers) == 0 {
		fmt.Println("no workers")
		return nil
	}
	fmt.Printf("%-24s %-18s %-12s %-13s %-11s %-16s %-7s %-8s %-9s %-20s %-20s %-18s %-24s %s\n", "WORKER ID", "PERSISTENT ID", "RUNTIME", "VERSION", "RELEASE", "PLATFORM", "SLOTS", "UPLOAD", "DRAINING", "LAST SEEN", "LAST CLAIM", "NETWORK", "IMAGES", "DOCKER IMAGES")
	for _, worker := range resp.Workers {
		maxActiveSlots := workerActiveSlotCapacity(worker)
		uploadCapable := strings.TrimSpace(worker.BundleUploadBaseURL) != ""
		fmt.Printf("%-24s %-18s %-12s %-13s %-11s %-16s %-7d %-8t %-9t %-20s %-20s %-18s %-24s %s\n",
			truncate(worker.ID, 24), truncate(valueOrDash(worker.PersistentWorkerID), 18), worker.Runtime, truncate(valueOrDash(worker.Version), 13), truncate(valueOrDash(worker.ReleaseState), 11), truncate(valueOrDash(worker.Platform), 16), maxActiveSlots, uploadCapable, worker.Draining,
			timeOrDash(worker.LastSeenAt), timeOrDash(worker.LastClaimedAt), strings.Join(worker.NetworkModes, ","), strings.Join(worker.SupportedImages, ","),
			strings.Join(worker.SupportedDockerImages, ","))
	}
	return nil
}

func adminWorkersStageReleaseCommand(args []string) error {
	fs := flag.NewFlagSet("admin workers stage-release", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, adminToken := adminAPIFlags(fs)
	candidateSHA := fs.String("candidate-sha", "", "exact candidate Git SHA")
	minimumSlots := fs.Int("minimum-production-slots", 2, "minimum fresh production slots that must remain in the worker's region")
	maintenanceCutover := fs.Bool("maintenance-cutover", false, "adopt an existing fleet maintenance drain; requires minimum-production-slots=0")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 1 || *candidateSHA == "" {
		return cliConfigError("usage: readyci admin workers stage-release --candidate-sha SHA [--minimum-production-slots N] WORKER_ID", nil)
	}
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()
	resp, err := api.NewClient(*apiURL, api.WithToken(*adminToken)).AdminStageWorkerRelease(ctx, fs.Arg(0), preflightapi.AdminStageWorkerRequest{CandidateSHA: *candidateSHA, MinimumProductionSlots: *minimumSlots, MaintenanceCutover: *maintenanceCutover})
	if err != nil {
		return err
	}
	fmt.Printf("%s release_state=%s candidate_sha=%s draining=%t remaining_production_slots=%d\n", resp.Worker.ID, resp.Worker.ReleaseState, resp.Worker.CandidateSHA, resp.Worker.Draining, resp.RemainingProductionSlots)
	return nil
}

func adminWorkersPromoteReleaseCommand(args []string) error {
	fs := flag.NewFlagSet("admin workers promote-release", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, adminToken := adminAPIFlags(fs)
	candidateSHA := fs.String("candidate-sha", "", "exact candidate Git SHA")
	version := fs.String("version", "", "exact worker-reported version")
	runtimeManifest := fs.String("runtime-manifest", "", "exact runtime manifest SHA-256")
	approvedRuntime := fs.String("approved-runtime", "", "exact approved runtime descriptor SHA-256")
	workerRelease := fs.String("worker-release", "", "exact canonical worker release digest")
	acceptanceRun := fs.String("acceptance-run", "", "successful candidate acceptance run ID")
	minimumSlots := fs.Int("minimum-production-slots", 2, "minimum fresh production slots that must remain in the worker's region")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 1 || *candidateSHA == "" || *version == "" || *runtimeManifest == "" || *approvedRuntime == "" || *workerRelease == "" || *acceptanceRun == "" {
		return cliConfigError("usage: readyci admin workers promote-release --candidate-sha SHA --version VERSION --runtime-manifest SHA256 --approved-runtime SHA256 --worker-release sha256:DIGEST --acceptance-run RUN_ID [--minimum-production-slots N] WORKER_ID", nil)
	}
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()
	resp, err := api.NewClient(*apiURL, api.WithToken(*adminToken)).AdminPromoteWorkerRelease(ctx, fs.Arg(0), preflightapi.AdminPromoteWorkerRequest{
		CandidateSHA: *candidateSHA, Version: *version, RuntimeManifestDigest: *runtimeManifest, ApprovedRuntimeDigest: *approvedRuntime, WorkerReleaseDigest: *workerRelease, AcceptanceRunID: *acceptanceRun, MinimumProductionSlots: *minimumSlots,
	})
	if err != nil {
		return err
	}
	fmt.Printf("%s release_state=%s acceptance_run_id=%s draining=%t remaining_production_slots=%d\n", resp.Worker.ID, resp.Worker.ReleaseState, resp.Worker.AcceptanceRunID, resp.Worker.Draining, resp.RemainingProductionSlots)
	return nil
}

func adminWorkersDemoteReleaseCommand(args []string) error {
	fs := flag.NewFlagSet("admin workers demote-release", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, adminToken := adminAPIFlags(fs)
	reason := fs.String("reason", "", "operator-visible demotion reason")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 1 {
		return cliConfigError("usage: readyci admin workers demote-release --reason REASON WORKER_ID", nil)
	}
	reasonValue := strings.TrimSpace(*reason)
	if reasonValue == "" {
		return cliConfigError("readyci admin workers demote-release requires --reason", nil)
	}
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()
	resp, err := api.NewClient(*apiURL, api.WithToken(*adminToken)).AdminDemoteWorkerRelease(ctx, fs.Arg(0), reasonValue)
	if err != nil {
		return err
	}
	fmt.Printf("%s release_state=%s draining=%t remaining_production_slots=%d\n", resp.Worker.ID, resp.Worker.ReleaseState, resp.Worker.Draining, resp.RemainingProductionSlots)
	return nil
}

func adminWorkersDrainCommand(args []string) error {
	fs := flag.NewFlagSet("admin workers drain", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, adminToken := adminAPIFlags(fs)
	reason := fs.String("reason", "", "operator-visible drain reason")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 1 {
		return cliConfigError("usage: readyci admin workers drain [--api-url URL] [--token TOKEN] --reason REASON WORKER_ID", nil)
	}
	reasonValue := strings.TrimSpace(*reason)
	if reasonValue == "" {
		return cliConfigError("readyci admin workers drain requires --reason", nil)
	}
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()

	resp, err := api.NewClient(*apiURL, api.WithToken(*adminToken)).AdminDrainWorker(ctx, fs.Arg(0), reasonValue)
	if err != nil {
		return err
	}
	fmt.Printf("%s draining=%t reason=%s\n", resp.Worker.ID, resp.Worker.Draining, valueOrDash(resp.Worker.DrainReason))
	return nil
}

func adminWorkersUndrainCommand(args []string) error {
	fs := flag.NewFlagSet("admin workers undrain", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, adminToken := adminAPIFlags(fs)
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 1 {
		return cliConfigError("usage: readyci admin workers undrain [--api-url URL] [--token TOKEN] WORKER_ID", nil)
	}
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()

	resp, err := api.NewClient(*apiURL, api.WithToken(*adminToken)).AdminUndrainWorker(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	fmt.Printf("%s draining=%t\n", resp.Worker.ID, resp.Worker.Draining)
	return nil
}

func adminWorkersDeleteCommand(args []string) error {
	fs := flag.NewFlagSet("admin workers delete", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, adminToken := adminAPIFlags(fs)
	reason := fs.String("reason", "", "operator-visible deletion reason")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 1 {
		return cliConfigError("usage: readyci admin workers delete [--api-url URL] [--token TOKEN] --reason REASON WORKER_ID", nil)
	}
	reasonValue := strings.TrimSpace(*reason)
	if reasonValue == "" {
		return cliConfigError("readyci admin workers delete requires --reason", nil)
	}
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()

	resp, err := api.NewClient(*apiURL, api.WithToken(*adminToken)).AdminDeleteWorker(ctx, fs.Arg(0), reasonValue)
	if err != nil {
		return err
	}
	fmt.Printf("%s deleted=%t cleared_run_references=%d deleted_cache_lanes=%d\n",
		resp.WorkerID, resp.Deleted, resp.ClearedRunReferences, resp.DeletedCacheLanes)
	return nil
}
