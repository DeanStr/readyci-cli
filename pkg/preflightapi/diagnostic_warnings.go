package preflightapi

const (
	WarningExecutionReportConflict = "execution_report_conflict"
	AgentActionInspectTestReport   = "inspect_test_report"
	TestReportConflictMessage      = "The command exited successfully, but its retained test report contains failures or errors. Check whether the command suppresses the test runner's exit status or retained a report from an earlier execution."
)

// DiagnosticWarning requests follow-up without changing an execution verdict.
// Report-derived warnings describe retained diagnostics, not proven code defects.
type DiagnosticWarning struct {
	Code           string `json:"code,omitempty"`
	Section        string `json:"section,omitempty"`
	Message        string `json:"message"`
	Action         string `json:"action,omitempty"`
	RunID          string `json:"run_id,omitempty"`
	ArtifactID     string `json:"artifact_id,omitempty"`
	ArtifactSHA256 string `json:"artifact_sha256,omitempty"`
}

func TestReportExecutionWarnings(status string, results *RunTestResults) []DiagnosticWarning {
	if status != StatusSucceeded || results == nil || results.Counts.Failed <= 0 && results.Counts.Errors <= 0 {
		return nil
	}
	return []DiagnosticWarning{{
		Code: WarningExecutionReportConflict, Section: "test_results",
		Message: TestReportConflictMessage, Action: AgentActionInspectTestReport,
		RunID: results.RunID, ArtifactID: results.ArtifactID, ArtifactSHA256: results.ArtifactSHA256,
	}}
}
