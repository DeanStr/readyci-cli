package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

const omittedReportsFixture = `{"run_id":"run_retention","project_id":"readyci","attempt":1,"state":"partial","reports":[],"results":[],"counts":{"passed":0,"failed":0,"errors":0,"skipped":0},"truncated":false,"collection":{"schema_version":"readyci.artifact_retention.v1","matched_file_count":3,"retained_file_count":1,"omitted_file_count":2,"omitted_report_count":1,"omitted_files":[{"path":"reports/large.xml","size_bytes":1000000,"reason":"bundle_limit"},{"path":"logs/large.log","size_bytes":2000000,"reason":"bundle_limit"}]}}`

func TestRunsTestsExplainsOmittedReports(t *testing.T) {
	var results preflightapi.RunTestResults
	if err := json.Unmarshal([]byte(omittedReportsFixture), &results); err != nil {
		t.Fatal(err)
	}
	output, err := captureStdoutForCLITest(t, func() error {
		printRunTestResults(results)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"omitted=2", "omitted_reports=1", "reports/large.xml", "counts cover retained reports only"} {
		if !strings.Contains(output, want) {
			t.Fatalf("missing %q in retention explanation:\n%s", want, output)
		}
	}
}

func TestRunsTestsDistinguishesParserLimitsFromCollectionLoss(t *testing.T) {
	for _, truncated := range []bool{false, true} {
		name := "collection-only"
		if truncated {
			name = "collection-and-parser-limit"
		}
		t.Run(name, func(t *testing.T) {
			var results preflightapi.RunTestResults
			if err := json.Unmarshal([]byte(omittedReportsFixture), &results); err != nil {
				t.Fatal(err)
			}
			results.Truncated = truncated
			output, err := captureStdoutForCLITest(t, func() error {
				printRunTestResults(results)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output, "omitted=2") {
				t.Fatalf("missing collection loss: %s", output)
			}
			if strings.Contains(output, "Result index is partial") != truncated {
				t.Fatalf("parser limit was confused with collection loss: %s", output)
			}
			if truncated && !strings.Contains(output, "retained original reports") {
				t.Fatalf("missing retained report guidance: %s", output)
			}
		})
	}
}

func TestAgentExplainPreservesExecutionAndMissingReportEvidence(t *testing.T) {
	var results preflightapi.RunTestResults
	if err := json.Unmarshal([]byte(omittedReportsFixture), &results); err != nil {
		t.Fatal(err)
	}
	one := 1
	detail := preflightapi.RunDetail{
		Summary:     preflightapi.RunSummary{RunID: "run_retention", Status: preflightapi.StatusFailed, ExitCode: &one, Classification: preflightapi.ClassificationTestFailed, FailureSummary: "The application assertion failed."},
		Spec:        preflightapi.RunSpec{RunID: "run_retention", Command: "run-tests"},
		TestResults: &results,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/runs/run_retention":
			writeCLITestJSON(w, http.StatusOK, detail)
		case "/v1/runs/run_retention/events":
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunEventsResponse{})
		case "/v1/runs/run_retention/artifacts":
			writeCLITestJSON(w, http.StatusOK, preflightapi.ListArtifactsResponse{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	report, err := buildAgentExplainReport(context.Background(), api.NewClient(server.URL), server.URL, detail.Summary.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if report.Diagnosis.Kind != preflightapi.ClassificationTestFailed || report.ExitCode == nil || *report.ExitCode != 1 {
		t.Fatalf("artifact omission changed execution outcome: %+v", report)
	}
	if len(report.Warnings) != 1 || report.Warnings[0].Code != "artifact_collection_incomplete" || !strings.Contains(report.Warnings[0].Message, "not retained") {
		t.Fatalf("missing report-retention warning: %+v", report.Warnings)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	validateAgentJSONSchema(t, encoded)
}
