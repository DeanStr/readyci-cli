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

func TestRunsTestsRetainsCompressedReportMemberAndCountDiagnostics(t *testing.T) {
	result := preflightapi.RunTestResults{TestReportIndex: preflightapi.TestReportIndex{
		State: "partial", Truncated: true,
		Results: []preflightapi.StructuredTestResult{{Test: "case", Status: "failed", ReportPath: "reports.junit.tar.gz", ReportMemberPath: "nested/TEST-case.xml"}},
		Reports: []preflightapi.TestReportSummary{{Path: "reports.junit.tar.gz", MemberPath: "nested/TEST-case.xml", State: "partial", CountMismatch: &preflightapi.TestReportCountMismatch{DeclaredTests: 860, ObservedTests: 1300, MismatchedSuites: 1}}},
	}}
	output, err := captureStdoutForCLITest(t, func() error { printRunTestResults(result); return nil })
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"report: reports.junit.tar.gz", "member: nested/TEST-case.xml", "declared=860 observed=1300 disagreeing_suites=1", "Result index is partial"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output=%q missing %q", output, expected)
		}
	}
}

func TestRunsTestsDistinguishesRetainedAndObservedCounts(t *testing.T) {
	for _, complete := range []bool{true, false} {
		t.Run(map[bool]string{true: "complete", false: "lower-bounds"}[complete], func(t *testing.T) {
			result := preflightapi.RunTestResults{TestReportIndex: preflightapi.TestReportIndex{
				State: "partial", Truncated: true, Counts: preflightapi.TestResultCounts{Passed: 1, Failed: 1},
				Selection: &preflightapi.TestReportSelection{ReportsObserved: 2, ReportsOmitted: 1, ResultsObserved: 3, ResultsOmitted: 1, ObservedCounts: preflightapi.TestResultCounts{Passed: 1, Failed: 2}, ScanComplete: complete},
			}}
			output, err := captureStdoutForCLITest(t, func() error { printRunTestResults(result); return nil })
			if err != nil {
				t.Fatal(err)
			}
			for _, expected := range []string{"reported_tests: 1 passed, 1 failed", "observed_reports=2 omitted_reports=1 observed_tests=3 omitted_tests=1", "observed_tests: 1 passed, 2 failed", "Result index is partial"} {
				if !strings.Contains(output, expected) {
					t.Fatalf("output=%q missing %q", output, expected)
				}
			}
			if strings.Contains(output, "counts are lower bounds") == complete {
				t.Fatalf("incorrect completeness in output=%q", output)
			}
		})
	}
}

func TestRunsTestsUsesScopedEndpointAndRetainsDiagnosticState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/runs/run_test/tests" || r.URL.Query().Get("status") != "failed" || r.Header.Get("Authorization") != "Bearer pfl_tests" {
			t.Errorf("unexpected request: %s", r.URL)
		}
		writeCLITestJSON(w, 200, preflightapi.RunTestResults{RunID: "run_test", TestReportIndex: preflightapi.TestReportIndex{State: "invalid", Reports: []preflightapi.TestReportSummary{{Path: "junit.xml", State: "invalid"}}, Results: []preflightapi.StructuredTestResult{}, Selection: &preflightapi.TestReportSelection{ReportsObserved: 1, ScanComplete: false}}})
	}))
	defer server.Close()
	output, err := captureStdoutForCLITest(t, func() error {
		return runsTestsCommand([]string{"--api-url", server.URL, "--token", "pfl_tests", "--status", "failed", "--json", "run_test"})
	})
	if err != nil {
		t.Fatal(err)
	}
	var result preflightapi.RunTestResults
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatal(err)
	}
	if result.State != "invalid" || len(result.Results) != 0 || result.Selection == nil || result.Selection.ReportsObserved != 1 || result.Selection.ScanComplete {
		t.Fatalf("results=%+v", result)
	}
}

func TestAgentExplainReportsBoundedFailuresWithoutOverridingHostedDiagnosis(t *testing.T) {
	index := preflightapi.TestReportIndex{State: "parsed"}
	for i := 0; i < 60; i++ {
		index.Results = append(index.Results, preflightapi.StructuredTestResult{Test: "failure", Status: "failed", File: "tests/one.go", Attempt: 1})
	}
	detail := preflightapi.RunDetail{Summary: preflightapi.RunSummary{RunID: "run_test", Status: preflightapi.StatusFailed, Classification: preflightapi.ClassificationWorkerInternalError, FailureSummary: "runtime failure"}, Spec: preflightapi.RunSpec{RunID: "run_test", Command: "test"}, TestResults: &preflightapi.RunTestResults{RunID: "run_test", TestReportIndex: index}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/runs/run_test":
			writeCLITestJSON(w, 200, detail)
		case "/v1/runs/run_test/events":
			writeCLITestJSON(w, 200, preflightapi.RunEventsResponse{RunID: "run_test"})
		case "/v1/runs/run_test/artifacts":
			writeCLITestJSON(w, 200, preflightapi.ListArtifactsResponse{RunID: "run_test"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	report, err := buildAgentExplainReport(context.Background(), api.NewClient(server.URL), server.URL, "run_test")
	if err != nil {
		t.Fatal(err)
	}
	if report.Diagnosis.Kind != preflightapi.ClassificationWorkerInternalError || report.Repair.FailingTest != "failure" || report.TestResults == nil || len(report.TestResults.Results) != 50 || !report.TestResults.HasMore {
		t.Fatalf("report=%+v", report)
	}
}
