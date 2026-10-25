package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func runsCommand(args []string) error {
	if len(args) == 0 {
		printUsage()
		return cliConfigError("usage: readyci runs <latest|status|explain|logs|artifacts|cancel>", nil)
	}
	switch args[0] {
	case "latest":
		return runsLatestCommand(args[1:])
	case "status":
		return runsStatusCommand(args[1:])
	case "explain":
		return agentExplainCommand(args[1:])
	case "logs":
		return runsLogsCommand(args[1:])
	case "artifacts":
		return runsArtifactsCommand(args[1:])
	case "tests":
		return runsTestsCommand(args[1:])
	case "cancel":
		return runsCancelCommand(args[1:])
	default:
		printUsage()
		return cliConfigError(fmt.Sprintf("unknown runs command %q", args[0]), nil)
	}
}

func runsCancelCommand(args []string) error {
	fs := flag.NewFlagSet("runs cancel", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	reason := fs.String("reason", "cancelled by user", "audit reason for cancellation")
	jsonOutput := fs.Bool("json", false, "print machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 1 {
		return cliConfigError("usage: readyci runs cancel [--reason TEXT] [--json] RUN_ID", nil)
	}
	if strings.TrimSpace(*reason) == "" {
		return cliConfigError("cancellation reason must not be empty", nil)
	}
	if err := validateControlFreeValue("reason", *reason); err != nil {
		return cliConfigError(err.Error(), err)
	}
	client, err := authenticatedClient(*apiURL, *apiToken)
	if err != nil {
		return err
	}
	ctx, stop := signalContext()
	defer stop()
	response, err := client.CancelRun(ctx, fs.Arg(0), preflightapi.CancelRunRequest{Reason: strings.TrimSpace(*reason)})
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSONOutput(response)
	}
	fmt.Printf("run: %s\nstatus: %s\n", response.RunID, response.Status)
	return nil
}

func runsLatestCommand(args []string) error {
	fs := flag.NewFlagSet("runs latest", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	projectID := fs.String("project", envDefault("READYCI_PROJECT_ID", ""), "project id")
	status := fs.String("status", "", "filter by run status")
	idempotencyKey := fs.String("by-idempotency-key", "", "return the run created with this idempotency key")
	jsonOutput := fs.Bool("json", false, "print machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci runs latest [--api-url URL] [--token TOKEN] [--project PROJECT] [--status STATUS] [--by-idempotency-key KEY] [--json]", nil)
	}
	projectIDValue, err := effectiveProjectID(*apiURL, *projectID)
	if err != nil {
		return err
	}
	client, err := authenticatedClient(*apiURL, *apiToken)
	if err != nil {
		return err
	}
	ctx, stop := signalContext()
	defer stop()
	if strings.TrimSpace(*idempotencyKey) != "" {
		key, err := normalizeIdempotencyKey(*idempotencyKey)
		if err != nil {
			return err
		}
		detail, err := client.GetRunByIdempotencyKey(ctx, projectIDValue, key)
		if err != nil {
			return err
		}
		if *jsonOutput {
			return writeJSONOutput(detail.Summary)
		}
		printRunSummary(detail.Summary)
		return nil
	}
	resp, err := client.ListRuns(ctx, projectIDValue, *status, 1)
	if err != nil {
		return err
	}
	if len(resp.Runs) == 0 {
		if *jsonOutput {
			return writeJSONOutput(nil)
		}
		if projectIDValue != "" {
			fmt.Printf("no runs for project %s\n", projectIDValue)
		} else {
			fmt.Println("no runs")
		}
		return nil
	}
	if *jsonOutput {
		return writeJSONOutput(resp.Runs[0])
	}
	printRunSummary(resp.Runs[0])
	return nil
}

func runsStatusCommand(args []string) error {
	fs := flag.NewFlagSet("runs status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	adminToken := adminOnlyTokenFlag(fs, "optional admin token for queued-run worker diagnostics")
	jsonOutput := fs.Bool("json", false, "print machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 1 {
		return cliConfigError("usage: readyci runs status [--api-url URL] [--token TOKEN] [--admin-token TOKEN] [--json] RUN_ID", nil)
	}
	client, err := authenticatedClient(*apiURL, *apiToken)
	if err != nil {
		return err
	}
	ctx, stop := signalContext()
	defer stop()
	detail, err := client.GetRun(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	diagnostics, adminTokenProvided := queuedRunDiagnostics{}, strings.TrimSpace(*adminToken) != ""
	if shouldPrintQueuedRunDiagnostics(detail) {
		if adminTokenProvided {
			resp, err := api.NewClient(*apiURL, api.WithToken(*adminToken)).AdminListWorkers(ctx)
			if err != nil {
				diagnostics.WorkerInventoryErr = err
			} else {
				diagnostics.WorkerCount = len(resp.Workers)
				capability := runSpecWorkerCapability(detail.Spec, resp.Workers)
				diagnostics.WorkerCapability = &capability
			}
		}
	}
	if *jsonOutput {
		return writeJSONOutput(buildRunStatusReport(detail, *apiURL, adminTokenProvided, diagnostics))
	}
	printRunDetail(detail)
	if shouldPrintQueuedRunDiagnostics(detail) {
		printQueuedRunDiagnostics(detail, *apiURL, adminTokenProvided, diagnostics)
	}
	return nil
}

type runStatusReport struct {
	TestResults      *preflightapi.RunTestResults `json:"test_results,omitempty"`
	Timings          *preflightapi.RunTimings     `json:"timings,omitempty"`
	Summary          preflightapi.RunSummary      `json:"summary"`
	Spec             preflightapi.RunSpec         `json:"spec"`
	PolicyReasons    []string                     `json:"policy_reasons,omitempty"`
	Diagnostics      []doctorDiagnostic           `json:"diagnostics,omitempty"`
	WorkerCapability *workerCapabilityReport      `json:"worker_capability,omitempty"`
}

type queuedRunDiagnostics struct {
	WorkerCount        int
	WorkerCapability   *workerCapabilityReport
	WorkerInventoryErr error
}

func shouldPrintQueuedRunDiagnostics(detail preflightapi.RunDetail) bool {
	return detail.Summary.Status == preflightapi.StatusQueued && strings.TrimSpace(detail.Summary.WorkerID) == ""
}

func runSpecWorkerCapability(spec preflightapi.RunSpec, workers []preflightapi.AdminWorkerSummary) workerCapabilityReport {
	return checkWorkerCapability(checkRunPlan{Options: remoteRunOptions{
		RuntimeRequirements:     preflightapi.CloneRuntimeRequirements(spec.RuntimeRequirements),
		EnvironmentRequirements: preflightapi.CloneEnvironmentRequirements(spec.EnvironmentRequirements),
		Image:                   spec.Image,
		Region:                  spec.Region,
		RunnerSize:              spec.RunnerSize,
		NetworkMode:             spec.NetworkMode,
		NetworkAllowlist:        append([]string(nil), spec.NetworkAllowlist...),
		Docker:                  spec.Docker,
		Services:                cloneRunServices(spec.Services),
		WorkerSelector:          copyStringMap(spec.WorkerSelector),
	}}, workers)
}

func buildRunStatusReport(detail preflightapi.RunDetail, apiURL string, adminTokenProvided bool, diagnostics queuedRunDiagnostics) runStatusReport {
	report := runStatusReport{
		TestResults:   detail.TestResults,
		Timings:       detail.Timings,
		Summary:       detail.Summary,
		Spec:          detail.Spec,
		PolicyReasons: append([]string(nil), detail.PolicyReasons...),
	}
	if shouldPrintQueuedRunDiagnostics(detail) {
		report.Diagnostics = queuedRunDiagnosticEntries(detail, apiURL, adminTokenProvided, diagnostics)
		report.WorkerCapability = diagnostics.WorkerCapability
	}
	return report
}

func printQueuedRunDiagnostics(detail preflightapi.RunDetail, apiURL string, adminTokenProvided bool, diagnostics queuedRunDiagnostics) {
	entries := queuedRunDiagnosticEntries(detail, apiURL, adminTokenProvided, diagnostics)
	fmt.Println("diagnostics:")
	for _, entry := range entries {
		fmt.Printf("  %s: %s\n", entry.Level, entry.Message)
		for _, detail := range entry.Details {
			fmt.Printf("  detail: %s\n", detail)
		}
		for _, hint := range entry.Hints {
			fmt.Printf("  hint: %s\n", hint)
		}
	}
	fmt.Printf("  next: %s\n", queuedRunDoctorCommand(detail, apiURL, false))
}

func queuedRunDiagnosticEntries(detail preflightapi.RunDetail, apiURL string, adminTokenProvided bool, diagnostics queuedRunDiagnostics) []doctorDiagnostic {
	entries := []doctorDiagnostic{{
		Section: "queue",
		Level:   "warn",
		Message: "run is queued and has not been claimed by a worker yet",
		Hints:   []string{"run " + queuedRunDoctorCommand(detail, apiURL, false)},
	}}
	if !adminTokenProvided {
		entries = append(entries, doctorDiagnostic{
			Section: "workers",
			Level:   "warn",
			Message: "worker capability diagnostics skipped because no admin token was provided",
			Hints:   []string{"rerun runs status with --admin-token TOKEN to compare this run with registered workers"},
		})
		return entries
	}
	if diagnostics.WorkerInventoryErr != nil {
		entries = append(entries, doctorDiagnostic{
			Section: "workers",
			Level:   "warn",
			Message: "worker inventory unavailable: " + diagnostics.WorkerInventoryErr.Error(),
		})
		return entries
	}
	if diagnostics.WorkerCount == 0 {
		entries = append(entries, doctorDiagnostic{
			Section: "workers",
			Level:   "warn",
			Message: "no workers registered",
			Hints:   []string{"start readyci-worker, then rerun readyci doctor"},
		})
		return entries
	}
	if diagnostics.WorkerCapability == nil {
		entries = append(entries, doctorDiagnostic{
			Section: "workers",
			Level:   "warn",
			Message: "worker capability diagnostics were not available",
		})
		return entries
	}
	if diagnostics.WorkerCapability.Claimable {
		entries = append(entries, doctorDiagnostic{
			Section: "workers",
			Level:   "ok",
			Message: "run matches worker capabilities: " + strings.Join(diagnostics.WorkerCapability.MatchingWorkers, ","),
			Hints:   []string{"a matching worker exists; the run may be waiting for capacity or the next worker poll"},
		})
		return entries
	}
	entries = append(entries, doctorDiagnostic{
		Section: "workers",
		Level:   "fail",
		Message: "run is not claimable by registered workers",
		Details: append([]string(nil), diagnostics.WorkerCapability.Reasons...),
		Hints:   []string{"adjust region, image, runner_size, docker, or network_mode, or start a fresh worker that advertises those capabilities"},
	})
	return entries
}

func queuedRunDoctorCommand(detail preflightapi.RunDetail, apiURL string, includeAdminToken bool) string {
	summary := detail.Summary
	command := "readyci doctor" + apiURLFlag(normalizeAPIURL(apiURL)) + " --project " + shellQuoteArg(defaultString(summary.ProjectID, detail.Spec.ProjectID))
	if checkName := strings.TrimSpace(detail.Spec.CommandAlias); checkName != "" {
		command += " --check " + shellQuoteArg(checkName)
	}
	if includeAdminToken {
		command += " --admin-token " + shellQuoteArg("<admin-token>")
	}
	return command
}
