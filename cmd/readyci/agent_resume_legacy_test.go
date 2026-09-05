package main

import (
	"context"
	"flag"
	"os"

	"github.com/DeanStr/readyci-cli/internal/api"
)

// Migration-era helpers retained only for old report fixtures. The production
// CLI uses sessions get/watch/cancel as its sole durable resume surface.
func agentResumeCommand(args []string) error {
	fs := flag.NewFlagSet("agent resume", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	projectID := fs.String("project", envDefault("READYCI_PROJECT_ID", ""), "project id")
	jsonOutput := fs.Bool("json", false, "print machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 1 {
		return cliConfigError("usage: readyci agent resume [--json] [--project PROJECT] IDEMPOTENCY_KEY", nil)
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
	report, err := buildAgentResumeReport(ctx, client, *apiURL, projectIDValue, fs.Arg(0))
	if err != nil {
		return err
	}
	if *jsonOutput {
		if err := writeJSONOutput(report); err != nil {
			return err
		}
	} else {
		printAgentVerifyReport(report)
	}
	if code := agentReportProcessExitCode(report); code != 0 {
		return exitCodeError{code: code}
	}
	return nil
}

func buildAgentResumeReport(ctx context.Context, client *api.Client, apiURL, projectID, key string) (agentVerifyReport, error) {
	resp, err := client.ListRunsByIdempotencyKey(ctx, projectID, key)
	if err != nil {
		return agentVerifyReport{}, err
	}
	if len(resp.Runs) == 0 {
		return agentVerifyReport{}, cliConfigError("no runs found for idempotency key", nil)
	}
	reports := make([]agentVerifyRunReport, 0, len(resp.Runs))
	for _, detail := range resp.Runs {
		report, err := agentRunReportFromDetail(ctx, client, apiURL, detail)
		if err != nil {
			return agentVerifyReport{}, err
		}
		reports = append(reports, report)
	}
	reports = latestAgentRunReportsByIdempotencyGroup(key, reports)
	report := agentVerifyReport{SchemaVersion: agentSchemaVersion, Status: "running", Check: commonAgentCheckName(reports), Runs: reports}
	return summarizeAgentVerifyRuns(report), nil
}
