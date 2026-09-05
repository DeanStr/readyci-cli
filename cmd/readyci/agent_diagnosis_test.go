package main

import (
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

type readyCILabDiagnosisCase struct {
	Name                   string                  `json:"name"`
	RunID                  string                  `json:"run_id"`
	LabReport              string                  `json:"lab_report"`
	Status                 string                  `json:"status"`
	Command                string                  `json:"command"`
	ExitCode               int                     `json:"exit_code"`
	Events                 []preflightapi.RunEvent `json:"events"`
	WantClassification     string                  `json:"want_classification"`
	WantAgentAction        string                  `json:"want_agent_action"`
	WantPrimaryFile        string                  `json:"want_primary_file"`
	WantSummaryContains    string                  `json:"want_summary_contains"`
	WantSummaryNotContains string                  `json:"want_summary_not_contains"`
}

func loadReadyCILabDiagnosisCorpus(t *testing.T) []readyCILabDiagnosisCase {
	t.Helper()
	data, err := os.ReadFile("../../pkg/preflightapi/testdata/diagnosis/readyci-lab-failures.json")
	if err != nil {
		t.Fatalf("read lab diagnosis corpus: %v", err)
	}
	var cases []readyCILabDiagnosisCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatalf("decode lab diagnosis corpus: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("lab diagnosis corpus is empty")
	}
	return cases
}

func TestAgentRecommendationAndDiagnosis(t *testing.T) {
	cfg := config.ProjectConfig{Checks: map[string]config.CheckConfig{
		"ci":      {Command: "make ci"},
		"full":    {Command: "make ci"},
		"go":      {Command: "go test ./..."},
		"touched": {Command: "go test ./..."},
	}}
	recommendation, err := recommendAgentCheck(cfg, "", "verify changed files before PR")
	if err != nil {
		t.Fatalf("recommend check: %v", err)
	}
	if recommendation.Check != "touched" {
		t.Fatalf("recommended check = %q, want touched", recommendation.Check)
	}
	recommendation, err = recommendAgentCheck(cfg, "", "final handoff")
	if err != nil {
		t.Fatalf("recommend final check: %v", err)
	}
	if recommendation.Check != "full" {
		t.Fatalf("final recommended check = %q, want full", recommendation.Check)
	}

	diagnosis := diagnoseRun(preflightapi.StatusFailed, "go test ./...", []preflightapi.RunEvent{
		{Type: preflightapi.EventStderr, Data: "--- FAIL: TestStore\nplatform/internal/runs/store_test.go:42: expected queued\n"},
	}, nil, "")
	if diagnosis.Kind != "test_failed" || diagnosis.Action != "fix_code" || diagnosis.PrimaryFile != "platform/internal/runs/store_test.go" {
		t.Fatalf("diagnosis = %+v, want test_failed/fix_code with primary file", diagnosis)
	}
	messageOnlyDiagnosis := diagnoseRun(preflightapi.StatusFailed, "go test ./...", []preflightapi.RunEvent{
		{Type: preflightapi.EventRunFailed, Message: "platform/internal/runs/store_test.go:42: expected queued"},
	}, nil, "")
	if messageOnlyDiagnosis.Kind != preflightapi.ClassificationTestFailed || messageOnlyDiagnosis.PrimaryFile != "platform/internal/runs/store_test.go" {
		t.Fatalf("message-only diagnosis = %+v, want test_failed with primary file", messageOnlyDiagnosis)
	}
	failureReasonOnlyDiagnosis := diagnoseRun(preflightapi.StatusFailed, "go test ./...", nil, nil, "platform/foo_test.go:42: expected ready, got failed")
	if failureReasonOnlyDiagnosis.Kind != preflightapi.ClassificationTestFailed || failureReasonOnlyDiagnosis.PrimaryFile != "platform/foo_test.go" {
		t.Fatalf("failure-reason-only diagnosis = %+v, want test_failed with primary file", failureReasonOnlyDiagnosis)
	}
	oomAssertionDiagnosis := diagnoseRun(preflightapi.StatusFailed, "go test ./...", []preflightapi.RunEvent{
		{Type: preflightapi.EventStderr, Data: "--- FAIL: TestAllocation\nexpected out of memory, got nil\n"},
	}, nil, "")
	if oomAssertionDiagnosis.Kind != preflightapi.ClassificationTestFailed || oomAssertionDiagnosis.Action != preflightapi.AgentActionFixCode {
		t.Fatalf("oom assertion diagnosis = %+v, want test_failed/fix_code", oomAssertionDiagnosis)
	}
	uploadExpiredDiagnosis := diagnoseRun(preflightapi.StatusUploadExpired, "make ci", nil, nil, "")
	if uploadExpiredDiagnosis.Kind != preflightapi.ClassificationSyncFailed || uploadExpiredDiagnosis.Action != preflightapi.AgentActionRetry {
		t.Fatalf("upload expired diagnosis = %+v, want sync_failed/retry", uploadExpiredDiagnosis)
	}
	deniedDiagnosis := diagnoseRun(preflightapi.StatusDenied, "make ci", nil, nil, "")
	if deniedDiagnosis.Kind != preflightapi.ClassificationPolicyDenied || deniedDiagnosis.Action != preflightapi.AgentActionReportSetup {
		t.Fatalf("denied diagnosis = %+v, want policy_denied/report_setup", deniedDiagnosis)
	}
	splinterDiagnosis := diagnoseRun(preflightapi.StatusFailed, "node scripts/splinter.js", []preflightapi.RunEvent{
		{Type: preflightapi.EventStderr, Data: "ELIFECYCLE Command failed with exit code 1.\n"},
	}, nil, "")
	if splinterDiagnosis.Kind != preflightapi.ClassificationInternalError || splinterDiagnosis.Action != preflightapi.AgentActionReportSetup {
		t.Fatalf("splinter diagnosis = %+v, want internal_error/report_setup", splinterDiagnosis)
	}
	if retryableClassification("worker_capability_mismatch") || retryableClassification("test_failed") {
		t.Fatalf("retryability taxonomy mismatch")
	}
	if actionForClassification(preflightapi.ClassificationSyncTooLarge) != preflightapi.AgentActionReduceScope {
		t.Fatalf("sync too large action = %s, want reduce_scope", actionForClassification(preflightapi.ClassificationSyncTooLarge))
	}
	if actionForClassification(preflightapi.ClassificationCacheStorageExceeded) != preflightapi.AgentActionReduceScope {
		t.Fatalf("cache storage exceeded action = %s, want reduce_scope", actionForClassification(preflightapi.ClassificationCacheStorageExceeded))
	}
	if got := rerunCommandForDiagnosis(preflightapi.StatusQueued, agentDiagnosis{Action: preflightapi.AgentActionWait}, "", "touched", false); got != "" {
		t.Fatalf("pending rerun command = %q, want empty", got)
	}
	if got := rerunCommandForDiagnosis(preflightapi.StatusSucceeded, agentDiagnosis{Action: preflightapi.AgentActionNone}, "", "touched", false); got != "" {
		t.Fatalf("success rerun command = %q, want empty", got)
	}
	if got := rerunCommandForDiagnosis(preflightapi.StatusFailed, agentDiagnosis{Action: preflightapi.AgentActionFixCode}, "", "touched", false); got != "readyci verify --json --progress=status --check touched" {
		t.Fatalf("failed rerun command = %q, want touched rerun", got)
	}
	if got := rerunCommandForDiagnosis(preflightapi.StatusFailed, agentDiagnosis{Action: preflightapi.AgentActionFixCode}, "https://staging.readyci.test", "touched", false); got != "readyci verify --json --progress=status --api-url https://staging.readyci.test --check touched" {
		t.Fatalf("staging rerun command = %q, want api-url preserved", got)
	}
	if got := rerunCommandForDiagnosis(preflightapi.StatusFailed, agentDiagnosis{Action: preflightapi.AgentActionFixCode}, "https://staging.readyci.test", "touched", true); got != "readyci verify --json --progress=status --api-url https://staging.readyci.test --check touched --request-secrets" {
		t.Fatalf("secret rerun command = %q, want api-url and request-secrets preserved", got)
	}
	retryOptions := optionsForAgentRetryAttempt(remoteRunOptions{IdempotencyKey: "task-1:GO=1"}, 2)
	if retryOptions.IdempotencyKey != "task-1:GO=1:__readyci_retry=2" {
		t.Fatalf("retry idempotency key = %q, want retry child key", retryOptions.IdempotencyKey)
	}
	current := latestAgentRunReportsByIdempotencyGroup("task-1", []agentVerifyRunReport{
		{RunID: "run_fail", IdempotencyKey: "task-1:GO=1", Status: preflightapi.StatusFailed, ExitCode: intPtr(1)},
		{RunID: "run_pass", IdempotencyKey: "task-1:GO=1:__readyci_retry=2", Status: preflightapi.StatusSucceeded, ExitCode: intPtr(0)},
	})
	if len(current) != 1 || current[0].RunID != "run_pass" {
		t.Fatalf("current idempotency reports = %+v, want latest retry only", current)
	}
	retryMatrix := latestAgentRunReportsByIdempotencyGroup("task-1", []agentVerifyRunReport{
		{RunID: "run_retry_1", IdempotencyKey: "task-1:retry=1", Status: preflightapi.StatusSucceeded, ExitCode: intPtr(0)},
		{RunID: "run_retry_2", IdempotencyKey: "task-1:retry=2", Status: preflightapi.StatusFailed, ExitCode: intPtr(1)},
	})
	if len(retryMatrix) != 2 || retryMatrix[0].RunID != "run_retry_1" || retryMatrix[1].RunID != "run_retry_2" {
		t.Fatalf("retry matrix reports = %+v, want distinct retry=1 and retry=2 variants", retryMatrix)
	}
	errorReport := agentRunReportFromResult("touched", "", remoteRunOptions{Command: "go test ./..."}, remoteRunResult{}, api.ResponseError{StatusCode: 401, Status: "Unauthorized"})
	if errorReport.ExitCode != nil || errorReport.Status != "error" || errorReport.Classification != "auth_missing" {
		t.Fatalf("non-exit error report = %+v, want auth error without remote exit code", errorReport)
	}
	stagingReport := agentRunReportFromResult("touched", "", remoteRunOptions{APIURL: "https://staging.readyci.test", Command: "go test ./..."}, remoteRunResult{RunID: "run_staging", Status: preflightapi.StatusFailed, ExitCode: 1}, exitCodeError{code: 1})
	if stagingReport.LogsCommand != "readyci runs logs --api-url https://staging.readyci.test --tail 200 --include-lifecycle --json run_staging" || stagingReport.ExplainCommand != "readyci agent explain --api-url https://staging.readyci.test --json run_staging" {
		t.Fatalf("staging follow-up commands = logs %q explain %q, want api-url preserved", stagingReport.LogsCommand, stagingReport.ExplainCommand)
	}
	serverDiagnosisReport := agentRunReportFromResult("touched", "", remoteRunOptions{Command: "pnpm install"}, remoteRunResult{
		RunID:          "run_server_diagnosis",
		Status:         preflightapi.StatusFailed,
		ExitCode:       1,
		Classification: preflightapi.ClassificationDependencyDownloadFailed,
		Retryable:      true,
		AgentAction:    preflightapi.AgentActionRetry,
		FailureSummary: "Dependency download failed.",
		Events: []preflightapi.RunEvent{{
			Type: preflightapi.EventStderr,
			Data: "ELIFECYCLE Command failed with exit code 1.\n",
		}},
	}, exitCodeError{code: 1})
	if serverDiagnosisReport.Classification != preflightapi.ClassificationDependencyDownloadFailed ||
		!serverDiagnosisReport.Retryable ||
		serverDiagnosisReport.AgentAction != preflightapi.AgentActionRetry ||
		serverDiagnosisReport.Summary != "Dependency download failed." {
		t.Fatalf("server diagnosis report = %+v, want hosted dependency diagnosis", serverDiagnosisReport)
	}
	streamEndedReport := agentRunReportFromResult(
		"touched",
		"",
		remoteRunOptions{Command: "go test ./..."},
		remoteRunResult{RunID: "run_stream", Status: preflightapi.StatusQueued},
		runStreamEndedError{runID: "run_stream", status: preflightapi.StatusQueued},
	)
	if streamEndedReport.Terminal || streamEndedReport.ExitCode != nil || streamEndedReport.Status != preflightapi.StatusQueued || streamEndedReport.AgentAction != preflightapi.AgentActionWait {
		t.Fatalf("stream-ended report = %+v, want queued wait without remote exit code", streamEndedReport)
	}
	unknownReport := agentRunReportFromResult("touched", "", remoteRunOptions{Command: "go test ./..."}, remoteRunResult{RunID: "run_unknown", Status: preflightapi.StatusRunning}, api.EventStreamError{Err: api.ResponseError{StatusCode: 503, Status: "unavailable"}})
	if unknownReport.Status != "unknown" || unknownReport.Terminal || unknownReport.ExitCode != nil || unknownReport.Classification != preflightapi.ClassificationVerificationUnknown {
		t.Fatalf("unknown recovery report = %+v, want verification_unknown without remote exit code", unknownReport)
	}
	tlsReport := agentRunReportFromResult("touched", "", remoteRunOptions{Command: "go test ./..."}, remoteRunResult{}, &url.Error{Op: "Get", URL: "https://api.readyci.test", Err: x509.UnknownAuthorityError{}})
	if tlsReport.Classification != preflightapi.ClassificationTransportFailure || tlsReport.AgentAction != preflightapi.AgentActionWaitOrReportInfra {
		t.Fatalf("tls error report = %+v, want transport_failure/wait_or_report_infra", tlsReport)
	}
	if got := classifyCommandError(api.ResponseError{StatusCode: 403, Status: "Forbidden"}); got != preflightapi.ClassificationAuthForbidden {
		t.Fatalf("scope auth error classification = %s, want auth_forbidden", got)
	}
	if got := classifyCommandError(errors.New("open testdata/private.json: permission denied")); got == preflightapi.ClassificationAuthForbidden {
		t.Fatalf("filesystem permission error classification = %s, want non-auth classification", got)
	}
	if got := classifyCommandError(api.ResponseError{StatusCode: http.StatusServiceUnavailable, Code: "worker_unavailable"}); got != preflightapi.ClassificationWorkerUnavailable {
		t.Fatalf("no upload worker classification = %s, want worker_unavailable", got)
	}
	actions := nextActionsForRun(agentVerifyRunReport{
		APIURL:         "https://readyci.example.test",
		Check:          "ci",
		Classification: preflightapi.ClassificationWorkerUnavailable,
		AgentAction:    preflightapi.AgentActionWait,
	})
	if len(actions) != 1 || actions[0].Kind != "diagnose" || actions[0].Command != "readyci doctor --json --api-url https://readyci.example.test --check ci" {
		t.Fatalf("worker unavailable next actions = %+v, want doctor diagnostic action", actions)
	}
}

func TestLocalAnalyzeRunFailureReadyCILabCorpus(t *testing.T) {
	for _, tc := range loadReadyCILabDiagnosisCorpus(t) {
		t.Run(tc.Name, func(t *testing.T) {
			exitCode := tc.ExitCode
			analysis := localAnalyzeRunFailure(tc.Status, tc.Command, &exitCode, tc.Events, "")
			if analysis.Classification != tc.WantClassification {
				t.Fatalf("run %s classification = %s, want %s", tc.RunID, analysis.Classification, tc.WantClassification)
			}
			if analysis.AgentAction != tc.WantAgentAction {
				t.Fatalf("run %s agent_action = %s, want %s", tc.RunID, analysis.AgentAction, tc.WantAgentAction)
			}
			if analysis.PrimaryFile != tc.WantPrimaryFile {
				t.Fatalf("run %s primary_file = %q, want %q", tc.RunID, analysis.PrimaryFile, tc.WantPrimaryFile)
			}
			if tc.WantSummaryContains != "" && !strings.Contains(analysis.Summary, tc.WantSummaryContains) {
				t.Fatalf("run %s summary = %q, want substring %q", tc.RunID, analysis.Summary, tc.WantSummaryContains)
			}
			if tc.WantSummaryNotContains != "" && strings.Contains(analysis.Summary, tc.WantSummaryNotContains) {
				t.Fatalf("run %s summary = %q, should not contain %q", tc.RunID, analysis.Summary, tc.WantSummaryNotContains)
			}
		})
	}
}
