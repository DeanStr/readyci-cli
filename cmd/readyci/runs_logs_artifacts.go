package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func runsLogsCommand(args []string) error {
	fs := flag.NewFlagSet("runs logs", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	jsonOutput := fs.Bool("json", false, "print compact machine-readable JSON")
	jsonlOutput := fs.Bool("jsonl", false, "print one JSON object per log event")
	summaryOutput := fs.Bool("summary", false, "print concise failure classification and diagnostic evidence")
	errorsOnly := fs.Bool("errors-only", false, "include only stderr, truncation, and failing terminal events")
	includeLifecycle := fs.Bool("include-lifecycle", false, "include lifecycle events when tailing structured logs")
	var phaseFilter optionalStringFlag
	var stepFilter optionalStringFlag
	var serviceFilter optionalStringFlag
	fs.Var(&phaseFilter, "phase", "include only events for phase")
	fs.Var(&stepFilter, "step", "include only events for step")
	fs.Var(&serviceFilter, "service", "include only events for service")
	tail := fs.Int("tail", 0, "include only the last N structured log events")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 1 {
		return cliConfigError("usage: readyci runs logs [--api-url URL] [--token TOKEN] [--json|--jsonl] [--summary] [--tail N] [--errors-only] [--include-lifecycle] [--phase PHASE] [--step STEP] [--service SERVICE] RUN_ID", nil)
	}
	if *jsonOutput && *jsonlOutput {
		return cliConfigError("--json and --jsonl cannot be combined", nil)
	}
	if *summaryOutput && *jsonlOutput {
		return cliConfigError("--summary and --jsonl cannot be combined", nil)
	}
	if *tail < 0 {
		return cliConfigError("--tail must be non-negative", nil)
	}
	if *includeLifecycle && *tail == 0 {
		return cliConfigError("--include-lifecycle requires --tail", nil)
	}
	if *summaryOutput && (*tail > 0 || *errorsOnly || *includeLifecycle || phaseFilter.set || stepFilter.set || serviceFilter.set) {
		return cliConfigError("--summary cannot be combined with tail, errors-only, lifecycle, or event filters", nil)
	}
	if phaseFilter.set && strings.TrimSpace(phaseFilter.value) == "" {
		return cliConfigError("--phase cannot be empty", nil)
	}
	if stepFilter.set && strings.TrimSpace(stepFilter.value) == "" {
		return cliConfigError("--step cannot be empty", nil)
	}
	if serviceFilter.set && strings.TrimSpace(serviceFilter.value) == "" {
		return cliConfigError("--service cannot be empty", nil)
	}
	filters := runLogFilters{
		Phase:   phaseFilter.value,
		Step:    stepFilter.value,
		Service: serviceFilter.value,
	}
	client, err := authenticatedClient(*apiURL, *apiToken)
	if err != nil {
		return err
	}
	ctx, stop := signalContext()
	defer stop()
	runID := fs.Arg(0)
	if *summaryOutput {
		detail, err := client.GetRun(ctx, runID)
		if err != nil {
			return err
		}
		resp, err := collectRunEventsForFailureSummary(ctx, client, runID)
		if err != nil {
			return err
		}
		report := buildRunLogsFailureSummaryReport(detail, resp.Events, resp.Truncated)
		if *jsonOutput {
			return writeJSONOutput(report)
		}
		return writeHumanRunLogsFailureSummary(report)
	}
	if *jsonOutput || *jsonlOutput || *tail > 0 || *errorsOnly {
		resp := preflightapi.RunEventsResponse{RunID: runID}
		var err error
		if *tail > 0 {
			resp, err = collectRunEventsTail(ctx, client, runID, *tail, *errorsOnly, *includeLifecycle)
		} else {
			resp.Events, err = collectRunEvents(ctx, client, runID)
		}
		if err != nil {
			return err
		}
		entries := runLogEntries(resp.Events, *errorsOnly, filters)
		if *tail > 0 && len(entries) > *tail {
			entries = tailLogEntries(entries, *tail)
		}
		if *jsonlOutput {
			return writeJSONLLogs(entries)
		}
		if !*jsonOutput {
			return writeHumanLogEntries(entries)
		}
		return writeJSONOutput(runLogsJSONReport{
			RunID:      runID,
			Events:     entries,
			ErrorsOnly: *errorsOnly,
			Lifecycle:  *includeLifecycle,
			Tail:       *tail,
			Truncated:  resp.Truncated || eventsWereTruncated(resp.Events),
		})
	}
	return client.StreamRunEvents(ctx, runID, func(event preflightapi.RunEvent) error {
		if !runEventMatchesLogFilters(event, filters) {
			return nil
		}
		switch event.Type {
		case preflightapi.EventStdout:
			data, err := event.DataBytes()
			if err != nil {
				return err
			}
			_, err = os.Stdout.WriteString(sanitizeLogDisplayText(string(data)))
			return err
		case preflightapi.EventStderr:
			data, err := event.DataBytes()
			if err != nil {
				return err
			}
			_, err = os.Stderr.WriteString(sanitizeLogDisplayText(string(data)))
			return err
		case preflightapi.EventLogTruncated:
			return writeLogTruncationNotice(event)
		default:
			return nil
		}
	})
}

func writeLogTruncationNotice(event preflightapi.RunEvent) error {
	message := strings.TrimSpace(sanitizeLogDisplayText(event.Message))
	if message == "" {
		message = "preflight: run log limit reached; further log output was truncated"
	}
	_, err := fmt.Fprintln(os.Stderr, message)
	return err
}

func runsArtifactsCommand(args []string) error {
	fs := flag.NewFlagSet("runs artifacts", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	download := fs.Bool("download", false, "download the artifact archive")
	output := fs.String("output", "", "output tar.gz path for --download")
	force := fs.Bool("force", false, "overwrite an existing output file for --download")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 1 {
		return cliConfigError("usage: readyci runs artifacts [--api-url URL] [--token TOKEN] [--download] [--output FILE] [--force] RUN_ID", nil)
	}
	runID := fs.Arg(0)
	if *download {
		return downloadArtifactsToFile(artifactDownloadOptions{
			APIURL:   *apiURL,
			APIToken: *apiToken,
			RunID:    runID,
			Output:   *output,
			Force:    *force,
		})
	}
	client, err := authenticatedClient(*apiURL, *apiToken)
	if err != nil {
		return err
	}
	ctx, stop := signalContext()
	defer stop()
	resp, err := client.ListArtifacts(ctx, runID)
	if err != nil {
		return err
	}
	if len(resp.Artifacts) == 0 {
		fmt.Printf("no artifacts for run %s\n", resp.RunID)
		return nil
	}
	fmt.Printf("artifacts for run %s\n", resp.RunID)
	for _, artifact := range resp.Artifacts {
		fmt.Printf("%s %s size_bytes=%d expires_at=%s\n",
			artifact.ID, artifact.Name, artifact.SizeBytes, artifact.ExpiresAt.Format(time.RFC3339))
	}
	fmt.Printf("download: readyci runs artifacts --download %s\n", resp.RunID)
	return nil
}
