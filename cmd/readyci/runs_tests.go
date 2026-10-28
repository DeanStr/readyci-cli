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
	if result.Collection.RequiredOmissions() {
		collection := result.Collection
		fmt.Printf("artifact_collection: retained=%d omitted=%d omitted_reports=%d\n", collection.RetainedFileCount, collection.OmittedFileCount, collection.OmittedReportCount)
		fmt.Println("Reported counts cover retained reports only; omitted files are unavailable.")
		for _, file := range collection.OmittedFiles {
			fmt.Printf("  omitted: %s (%d bytes; %s)\n", sanitizeRunLogsSummaryText(file.Path), file.SizeBytes, sanitizeRunLogsSummaryText(file.Reason))
		}
		if collection.OmissionsTruncated {
			fmt.Println("The omitted-file list is shortened; omission counts include all selected files.")
		}
	}
	fmt.Printf("reported_tests: %d passed, %d failed, %d errors, %d skipped\n", result.Counts.Passed, result.Counts.Failed, result.Counts.Errors, result.Counts.Skipped)
	if selection := result.Selection; selection != nil && (selection.ReportsOmitted > 0 || selection.ResultsOmitted > 0 || !selection.ScanComplete) {
		fmt.Printf("test_index_selection: observed_reports=%d omitted_reports=%d observed_tests=%d omitted_tests=%d scan_complete=%t\n", selection.ReportsObserved, selection.ReportsOmitted, selection.ResultsObserved, selection.ResultsOmitted, selection.ScanComplete)
		counts := selection.ObservedCounts
		fmt.Printf("observed_tests: %d passed, %d failed, %d errors, %d skipped\n", counts.Passed, counts.Failed, counts.Errors, counts.Skipped)
		if !selection.ScanComplete {
			fmt.Println("Observed and omitted counts are lower bounds; invalid or limited reports could not be fully inspected.")
		}
	}
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
		if test.ReportMemberPath != "" {
			fmt.Printf("    member: %s\n", sanitizeRunLogsSummaryText(test.ReportMemberPath))
		}
	}
	for _, report := range result.Reports {
		if report.State != "parsed" {
			fmt.Printf("  report %s: %s · %s\n", sanitizeRunLogsSummaryText(report.Path), report.State, report.Message)
			if report.MemberPath != "" {
				fmt.Printf("    member: %s\n", sanitizeRunLogsSummaryText(report.MemberPath))
			}
			if report.CountMismatch != nil {
				counts := report.CountMismatch
				fmt.Printf("    first inconsistent suite: declared=%d observed=%d disagreeing_suites=%d\n", counts.DeclaredTests, counts.ObservedTests, counts.MismatchedSuites)
			}
		}
	}
	if result.Truncated {
		fmt.Println("Result index is partial; inspect retained original reports for additional details.")
	}
	if result.HasMore {
		fmt.Println("More reported failures are available through readyci runs tests.")
	}
	if result.SnapshotDigest != "" {
		fmt.Printf("snapshot: %s\n", result.SnapshotDigest)
	}
}
