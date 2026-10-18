package preflightapi

// TestReportIndex is diagnostic data derived by the service from the retained
// archive. A parsed report is not an execution or verification verdict.
type TestReportIndex struct {
	State     string                 `json:"state"`
	Reports   []TestReportSummary    `json:"reports"`
	Results   []StructuredTestResult `json:"results"`
	Counts    TestResultCounts       `json:"counts"`
	Truncated bool                   `json:"truncated"`
}

type TestReportSummary struct {
	Path    string `json:"path"`
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
	Tests   int    `json:"tests"`
}

type TestResultCounts struct {
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Errors  int `json:"errors"`
	Skipped int `json:"skipped"`
}

type RunTestResults struct {
	SessionID           string `json:"session_id,omitempty"`
	ExecutionDigest     string `json:"execution_digest,omitempty"`
	ResultFilter        string `json:"result_filter,omitempty"`
	HasMore             bool   `json:"has_more"`
	RunID               string `json:"run_id"`
	ProjectID           string `json:"project_id"`
	SnapshotDigest      string `json:"snapshot_digest,omitempty"`
	Check               string `json:"check,omitempty"`
	MatrixVariant       string `json:"matrix_variant,omitempty"`
	Attempt             int    `json:"attempt"`
	WorkerReleaseDigest string `json:"worker_release_digest,omitempty"`
	ArtifactID          string `json:"artifact_id,omitempty"`
	ArtifactSHA256      string `json:"artifact_sha256,omitempty"`
	DownloadURL         string `json:"download_url,omitempty"`
	TestReportIndex
}

func (r RunTestResults) Failures(limit int) RunTestResults {
	results := make([]StructuredTestResult, 0)
	r.ResultFilter = "failures"
	for _, test := range r.Results {
		if test.Status != "failed" && test.Status != "error" {
			continue
		}
		if len(results) < limit {
			results = append(results, test)
		} else {
			r.HasMore = true
		}
	}
	r.Results = results
	return r
}
