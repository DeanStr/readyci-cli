package preflightapi

import "testing"

func TestTestReportExecutionWarningsPreserveObservedOmittedFailures(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		counts       TestResultCounts
		selection    *TestReportSelection
		warn         bool
	}{
		{"retained failure", StatusSucceeded, TestResultCounts{Failed: 1}, nil, true},
		{"omitted failure", StatusSucceeded, TestResultCounts{Passed: 1}, &TestReportSelection{ObservedCounts: TestResultCounts{Passed: 1, Failed: 1}, ScanComplete: true}, true},
		{"observed error in incomplete scan", StatusSucceeded, TestResultCounts{}, &TestReportSelection{ObservedCounts: TestResultCounts{Errors: 1}, ScanComplete: false}, true},
		{"omission without observed failure", StatusSucceeded, TestResultCounts{Passed: 1}, &TestReportSelection{ReportsOmitted: 1, ResultsOmitted: 1, ScanComplete: false}, false},
		{"failed execution", StatusFailed, TestResultCounts{}, &TestReportSelection{ObservedCounts: TestResultCounts{Failed: 1}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := RunTestResults{RunID: "run_observed", ArtifactID: "archive_observed", ArtifactSHA256: "archive-sha", TestReportIndex: TestReportIndex{Counts: tc.counts, Selection: tc.selection}}
			warnings := TestReportExecutionWarnings(tc.status, &result)
			if (len(warnings) != 0) != tc.warn {
				t.Fatalf("warnings=%+v, want warning=%v", warnings, tc.warn)
			}
			if tc.warn && (len(warnings) != 1 || warnings[0].Code != WarningExecutionReportConflict || warnings[0].Action != AgentActionInspectTestReport || warnings[0].RunID != result.RunID || warnings[0].ArtifactID != result.ArtifactID || warnings[0].ArtifactSHA256 != result.ArtifactSHA256) {
				t.Fatalf("lost conflict provenance: %+v", warnings)
			}
		})
	}
}
