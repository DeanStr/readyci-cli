package preflightapi

import "github.com/DeanStr/readyci-cli/pkg/artifactbundle"

// TestReportIndex is diagnostic data derived by the service from the retained
// archive. A parsed report is not an execution or verification verdict.
type TestReportIndex struct {
	State     string                 `json:"state"`
	Reports   []TestReportSummary    `json:"reports"`
	Results   []StructuredTestResult `json:"results"`
	Counts    TestResultCounts       `json:"counts"`
	Truncated bool                   `json:"truncated"`
	// Collection distinguishes known collection omissions from an empty index.
	// OmittedReportCount counts path-based candidates, not confirmed JUnit files
	// or tests. Counts describes retained indexed test results only.
	Collection *artifactbundle.Retention `json:"collection,omitempty"`
}

type TestReportSummary struct {
	Path       string `json:"path"`
	MemberPath string `json:"member_path,omitempty"`
	State      string `json:"state"`
	Message    string `json:"message,omitempty"`
	Tests      int    `json:"tests"`
	// ContentSHA256 and SizeBytes identify complete uncompressed XML bytes.
	// Path is the retained artifact file; MemberPath identifies XML inside an
	// explicitly named JUnit archive. Tests counts retained indexed cases only.
	ContentSHA256 string                   `json:"content_sha256,omitempty"`
	SizeBytes     int64                    `json:"size_bytes,omitempty"`
	CountMismatch *TestReportCountMismatch `json:"count_mismatch,omitempty"`
}

// TestReportCountMismatch describes the first disagreeing suite, not the whole
// report or run. Observed testcase outcomes remain diagnostic; no cases are
// invented to match untrusted declared totals.
type TestReportCountMismatch struct {
	DeclaredTests    int `json:"declared_tests"`
	ObservedTests    int `json:"observed_tests"`
	MismatchedSuites int `json:"mismatched_suites"`
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
