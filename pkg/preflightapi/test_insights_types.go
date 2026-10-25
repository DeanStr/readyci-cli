package preflightapi

import "time"

// Supported analysis windows for project test insights. Test reports are
// retained with their artifact, so no window exceeds artifact availability.
var TestInsightWindowDays = []int{7, 14, 30}

const DefaultTestInsightWindowDays = 14

// ProjectTestInsightsResponse aggregates structured test results across a
// project's recent runs. Histories are keyed by StructuredTestResult.ID, check
// alias, and matrix variant. Each list is capped; TestsObserved, FlakyCount and
// FailingCount count these distinct histories before the cap.
type ProjectTestInsightsResponse struct {
	ProjectID     string        `json:"project_id"`
	WindowDays    int           `json:"window_days"`
	WindowStart   time.Time     `json:"window_start"`
	RunsAnalyzed  int           `json:"runs_analyzed"`
	RunsTruncated bool          `json:"runs_truncated"`
	TestsObserved int           `json:"tests_observed"`
	FlakyCount    int           `json:"flaky_count"`
	FailingCount  int           `json:"failing_count"`
	Flaky         []TestInsight `json:"flaky"`
	Slowest       []TestInsight `json:"slowest"`
	Failing       []TestInsight `json:"failing"`
}

// TestInsight summarizes one test's observations for one check and matrix
// variant in the window. ID remains the structured test result ID. Failed counts
// failed and error results. FlakyRetries counts run groups in which the test
// failed on one attempt and passed on a later attempt of the same check; Flips
// counts pass/fail changes between consecutive observations.
type TestInsight struct {
	ID                 string    `json:"id"`
	Suite              string    `json:"suite,omitempty"`
	Class              string    `json:"class,omitempty"`
	Test               string    `json:"test"`
	File               string    `json:"file,omitempty"`
	Check              string    `json:"check,omitempty"`
	MatrixVariant      string    `json:"matrix_variant,omitempty"`
	Runs               int       `json:"runs"`
	Passed             int       `json:"passed"`
	Failed             int       `json:"failed"`
	Skipped            int       `json:"skipped"`
	FlakyRetries       int       `json:"flaky_retries"`
	Flips              int       `json:"flips"`
	MedianDurationMS   *int64    `json:"median_duration_ms,omitempty"`
	P95DurationMS      *int64    `json:"p95_duration_ms,omitempty"`
	MaxDurationMS      *int64    `json:"max_duration_ms,omitempty"`
	LastStatus         string    `json:"last_status"`
	LastRunID          string    `json:"last_run_id"`
	LastSeenAt         time.Time `json:"last_seen_at"`
	LastFailureMessage string    `json:"last_failure_message,omitempty"`
}
