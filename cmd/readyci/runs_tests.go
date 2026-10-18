package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func runsTestsCommand(args []string) error {
	fs := flag.NewFlagSet("runs tests", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	status := fs.String("status", "", "filter by passed, failed, error, or skipped")
	jsonOutput := fs.Bool("json", false, "print machine-readable test results")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 1 {
		return cliConfigError("usage: readyci runs tests [--status STATUS] [--json] RUN_ID", nil)
	}
	switch *status {
	case "", "passed", "failed", "error", "skipped":
	default:
		return cliConfigError("invalid test status", nil)
	}
	client, err := authenticatedClient(*apiURL, *apiToken)
	if err != nil {
		return err
	}
	ctx, stop := signalContext()
	defer stop()
	result, err := client.GetRunTestResults(ctx, fs.Arg(0), *status)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSONOutput(result)
	}
	printRunTestResults(result)
	return nil
}

func printRunTestResults(result preflightapi.RunTestResults) {
	fmt.Printf("test_reports: %s\n", result.State)
	fmt.Printf("reported_tests: %d passed, %d failed, %d errors, %d skipped\n", result.Counts.Passed, result.Counts.Failed, result.Counts.Errors, result.Counts.Skipped)
	for _, test := range result.Results {
		duration := "duration unavailable"
		if test.DurationMS != nil {
			duration = fmt.Sprintf("%dms", *test.DurationMS)
		}
		fmt.Printf("  %s %s (%s)\n", test.Status, sanitizeRunLogsSummaryText(test.Test), duration)
		if test.File != "" {
			fmt.Printf("    %s:%d\n", sanitizeRunLogsSummaryText(test.File), test.Line)
		}
		if test.Message != "" {
			fmt.Printf("    %s\n", sanitizeRunLogsSummaryText(test.Message))
		}
		fmt.Printf("    report: %s\n", sanitizeRunLogsSummaryText(test.ReportPath))
	}
	for _, report := range result.Reports {
		if report.State != "parsed" {
			fmt.Printf("  report %s: %s · %s\n", sanitizeRunLogsSummaryText(report.Path), report.State, report.Message)
		}
	}
	if result.Truncated {
		fmt.Println("Result index is partial; inspect the retained original reports.")
	}
	if result.HasMore {
		fmt.Println("More reported failures are available through readyci runs tests.")
	}
	if result.SnapshotDigest != "" {
		fmt.Printf("snapshot: %s\n", result.SnapshotDigest)
	}
}
