package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestAgentExplainAndResumeUseStructuredRunData(t *testing.T) {
	exitCode := 1
	successExitCode := 0
	fixed := time.Date(2026, 6, 22, 6, 7, 8, 0, time.UTC)
	events := []preflightapi.RunEvent{
		{Type: preflightapi.EventStderr, RunID: "run_fail", Timestamp: fixed, Data: "--- FAIL: TestStore\nplatform/internal/runs/store_test.go:42: expected queued, got failed\n"},
		{Type: preflightapi.EventCommandExited, RunID: "run_fail", Timestamp: fixed.Add(time.Second), ExitCode: &exitCode},
		{Type: preflightapi.EventRunFailed, RunID: "run_fail", Timestamp: fixed.Add(2 * time.Second), Message: "go test ./... failed", Status: preflightapi.StatusFailed, ExitCode: &exitCode},
	}
	successEvents := []preflightapi.RunEvent{
		{Type: preflightapi.EventStdout, RunID: "run_pass", Timestamp: fixed.Add(3 * time.Second), Data: "ok platform/internal/runs no error budget regressions\n"},
		{Type: preflightapi.EventCommandExited, RunID: "run_pass", Timestamp: fixed.Add(4 * time.Second), ExitCode: &successExitCode},
		{Type: preflightapi.EventRunFinished, RunID: "run_pass", Timestamp: fixed.Add(5 * time.Second), Status: preflightapi.StatusSucceeded, ExitCode: &successExitCode},
	}
	otherEvents := []preflightapi.RunEvent{
		{Type: preflightapi.EventStdout, RunID: "run_other", Timestamp: fixed.Add(6 * time.Second), Data: "ok platform/internal/api\n"},
		{Type: preflightapi.EventCommandExited, RunID: "run_other", Timestamp: fixed.Add(7 * time.Second), ExitCode: &successExitCode},
		{Type: preflightapi.EventRunFinished, RunID: "run_other", Timestamp: fixed.Add(8 * time.Second), Status: preflightapi.StatusSucceeded, ExitCode: &successExitCode},
	}
	detail := preflightapi.RunDetail{
		Summary: preflightapi.RunSummary{
			RunID:     "run_fail",
			ProjectID: "proj_story",
			Command:   "go test ./...",
			Status:    preflightapi.StatusFailed,
			ExitCode:  &exitCode,
			CreatedAt: fixed,
		},
		Spec: preflightapi.RunSpec{
			RunID:          "run_fail",
			ProjectID:      "proj_story",
			IdempotencyKey: "task-1:GO=1",
			Command:        "go test ./...",
			CommandAlias:   "touched [GO=1]",
		},
	}
	retryDetail := preflightapi.RunDetail{
		Summary: preflightapi.RunSummary{
			RunID:     "run_pass",
			ProjectID: "proj_story",
			Command:   "go test ./...",
			Status:    preflightapi.StatusSucceeded,
			ExitCode:  &successExitCode,
			CreatedAt: fixed.Add(time.Minute),
		},
		Spec: preflightapi.RunSpec{
			RunID:          "run_pass",
			ProjectID:      "proj_story",
			IdempotencyKey: "task-1:GO=1:__readyci_retry=2",
			Command:        "go test ./...",
			CommandAlias:   "touched [GO=1]",
		},
	}
	otherDetail := preflightapi.RunDetail{
		Summary: preflightapi.RunSummary{
			RunID:     "run_other",
			ProjectID: "proj_story",
			Command:   "go test ./...",
			Status:    preflightapi.StatusSucceeded,
			ExitCode:  &successExitCode,
			CreatedAt: fixed.Add(2 * time.Minute),
		},
		Spec: preflightapi.RunSpec{
			RunID:          "run_other",
			ProjectID:      "proj_story",
			IdempotencyKey: "task-1:GO=2",
			Command:        "go test ./...",
			CommandAlias:   "touched [GO=2]",
		},
	}
	var idempotencyLookups int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer pfl_story" {
			t.Fatalf("authorization = %q, want bearer token", got)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/by-idempotency-key":
			idempotencyLookups++
			if r.URL.Query().Get("project") != "proj_story" || r.URL.Query().Get("key") != "task-1" || r.URL.Query().Get("include_children") != "true" {
				t.Fatalf("resume query = %s, want project/key/include_children", r.URL.RawQuery)
			}
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunDetailsResponse{Runs: []preflightapi.RunDetail{detail, retryDetail, otherDetail}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_fail":
			writeCLITestJSON(w, http.StatusOK, detail)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_fail/events":
			if r.URL.Query().Get("format") != "json" {
				t.Fatalf("events query = %s, want format=json", r.URL.RawQuery)
			}
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunEventsResponse{RunID: "run_fail", Events: events})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_pass/events":
			if r.URL.Query().Get("format") != "json" {
				t.Fatalf("events query = %s, want format=json", r.URL.RawQuery)
			}
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunEventsResponse{RunID: "run_pass", Events: successEvents})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_other/events":
			if r.URL.Query().Get("format") != "json" {
				t.Fatalf("events query = %s, want format=json", r.URL.RawQuery)
			}
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunEventsResponse{RunID: "run_other", Events: otherEvents})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_fail/artifacts":
			http.Error(w, "missing runs:read", http.StatusForbidden)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	output, err := captureStdoutForCLITest(t, func() error {
		return agentExplainCommand([]string{"--api-url", server.URL, "--token", "pfl_story", "--json", "run_fail"})
	})
	if err != nil {
		t.Fatalf("agent explain: %v", err)
	}
	var explain agentExplainReport
	if err := json.Unmarshal([]byte(output), &explain); err != nil {
		t.Fatalf("decode agent explain: %v\n%s", err, output)
	}
	baseRerun := "readyci verify --json --progress=status --api-url " + shellQuoteArg(server.URL) + " --check touched"
	expectedRerun := baseRerun + " --request-secrets"
	if explain.Diagnosis.Kind != "test_failed" || explain.Check != "touched" || explain.MatrixVariant != "GO=1" || explain.RerunCommand != expectedRerun {
		t.Fatalf("agent explain diagnosis = %+v rerun=%q, want legacy secret-preserving rerun touched", explain.Diagnosis, explain.RerunCommand)
	}
	if explain.SchemaVersion != agentSchemaVersion || explain.Repair.Classification != preflightapi.ClassificationTestFailed || explain.Repair.AgentAction != preflightapi.AgentActionFixCode || explain.Repair.PrimaryFile != "platform/internal/runs/store_test.go" || explain.Repair.FailingTest != "TestStore" {
		t.Fatalf("agent explain repair = schema %q packet %+v, want structured repair packet", explain.SchemaVersion, explain.Repair)
	}
	if wantLogs := "readyci runs logs --api-url " + shellQuoteArg(server.URL) + " --tail 200 --include-lifecycle --json run_fail"; explain.LogsCommand != wantLogs {
		t.Fatalf("agent explain logs command = %q, want %q", explain.LogsCommand, wantLogs)
	}
	if explain.Diagnosis.PrimaryFile != "platform/internal/runs/store_test.go" {
		t.Fatalf("primary file = %q, want store test path", explain.Diagnosis.PrimaryFile)
	}
	if len(explain.Evidence) == 0 || !strings.Contains(explain.Evidence[0].Text, "TestStore") {
		t.Fatalf("evidence = %+v, want failing test output", explain.Evidence)
	}
	if len(explain.Warnings) != 1 || explain.Warnings[0].Section != "artifacts" || !strings.Contains(explain.Warnings[0].Message, "missing runs:read") {
		t.Fatalf("warnings = %+v, want artifact lookup warning", explain.Warnings)
	}

	secretsRequested := false
	detail.Spec.SecretsRequested = &secretsRequested
	output, err = captureStdoutForCLITest(t, func() error {
		return agentExplainCommand([]string{"--api-url", server.URL, "--token", "pfl_story", "--json", "run_fail"})
	})
	if err != nil {
		t.Fatalf("agent explain explicit no-secret run: %v", err)
	}
	if err := json.Unmarshal([]byte(output), &explain); err != nil {
		t.Fatalf("decode no-secret agent explain: %v\n%s", err, output)
	}
	if explain.RerunCommand != baseRerun || explain.Repair.RerunCommand != baseRerun {
		t.Fatalf("explicit no-secret rerun commands top=%q repair=%q, want %q", explain.RerunCommand, explain.Repair.RerunCommand, baseRerun)
	}

	secretsRequested = true
	detail.Spec.SecretsRequested = &secretsRequested
	output, err = captureStdoutForCLITest(t, func() error {
		return agentExplainCommand([]string{"--api-url", server.URL, "--token", "pfl_story", "--json", "run_fail"})
	})
	if err != nil {
		t.Fatalf("agent explain secret run: %v", err)
	}
	if err := json.Unmarshal([]byte(output), &explain); err != nil {
		t.Fatalf("decode secret agent explain: %v\n%s", err, output)
	}
	expectedSecretRerun := baseRerun + " --request-secrets"
	if explain.RerunCommand != expectedSecretRerun || explain.Repair.RerunCommand != expectedSecretRerun {
		t.Fatalf("secret rerun commands top=%q repair=%q, want %q", explain.RerunCommand, explain.Repair.RerunCommand, expectedSecretRerun)
	}

	output, err = captureStdoutForCLITest(t, func() error {
		return agentResumeCommand([]string{"--api-url", server.URL, "--token", "pfl_story", "--project", "proj_story", "--json", "task-1"})
	})
	if err != nil {
		t.Fatalf("agent resume: %v", err)
	}
	var resumed agentVerifyReport
	if err := json.Unmarshal([]byte(output), &resumed); err != nil {
		t.Fatalf("decode agent resume: %v\n%s", err, output)
	}
	if idempotencyLookups != 1 || resumed.Status != preflightapi.StatusSucceeded || resumed.Classification != "success" || len(resumed.Runs) != 2 {
		t.Fatalf("resume lookup count=%d report=%+v, want two passing current matrix runs", idempotencyLookups, resumed)
	}
	if resumed.SchemaVersion != agentSchemaVersion {
		t.Fatalf("resume schema version = %q, want %q", resumed.SchemaVersion, agentSchemaVersion)
	}
	if resumed.Checks == nil {
		t.Fatalf("resume checks = nil, want empty array in JSON contract")
	}
	if resumed.Runs[0].RunID != "run_pass" || resumed.Runs[0].MatrixVariant != "GO=1" || resumed.Runs[1].RunID != "run_other" {
		t.Fatalf("resume runs = %+v, want retry run for GO=1 and GO=2 run", resumed.Runs)
	}
	if wantExplain := "readyci agent explain --api-url " + shellQuoteArg(server.URL) + " --json run_pass"; resumed.Runs[0].ExplainCommand != wantExplain {
		t.Fatalf("resume explain command = %q, want %q", resumed.Runs[0].ExplainCommand, wantExplain)
	}
	if wantLogs := "readyci runs logs --api-url " + shellQuoteArg(server.URL) + " --tail 200 --include-lifecycle --json run_pass"; resumed.Runs[0].LogsCommand != wantLogs {
		t.Fatalf("resume logs command = %q, want %q", resumed.Runs[0].LogsCommand, wantLogs)
	}
	if len(resumed.Runs[0].Evidence) != 0 {
		t.Fatalf("successful run evidence = %+v, want none", resumed.Runs[0].Evidence)
	}
}

func TestAgentRunReportFromDetailPreservesLegacySecretRequest(t *testing.T) {
	exitCode := 1
	fixed := time.Date(2026, 6, 22, 6, 7, 8, 0, time.UTC)
	detail := preflightapi.RunDetail{
		Summary: preflightapi.RunSummary{
			RunID:     "run_fail",
			ProjectID: "proj_story",
			Command:   "go test ./...",
			Status:    preflightapi.StatusFailed,
			ExitCode:  &exitCode,
			CreatedAt: fixed,
		},
		Spec: preflightapi.RunSpec{
			RunID:        "run_fail",
			ProjectID:    "proj_story",
			Command:      "go test ./...",
			CommandAlias: "touched",
		},
	}
	events := []preflightapi.RunEvent{
		{Type: preflightapi.EventRunFailed, RunID: "run_fail", Timestamp: fixed, Message: "go test ./... failed", Status: preflightapi.StatusFailed, ExitCode: &exitCode},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_fail/events" {
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunEventsResponse{RunID: "run_fail", Events: events})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	client := api.NewClient(server.URL, api.WithToken("pfl_story"))

	report, err := agentRunReportFromDetail(context.Background(), client, server.URL, detail)
	if err != nil {
		t.Fatalf("agent run report from nil secret detail: %v", err)
	}
	if !report.SecretsRequested {
		t.Fatalf("nil secrets_requested report = %+v, want legacy requested", report)
	}
	secretsRequested := false
	detail.Spec.SecretsRequested = &secretsRequested
	report, err = agentRunReportFromDetail(context.Background(), client, server.URL, detail)
	if err != nil {
		t.Fatalf("agent run report from false secret detail: %v", err)
	}
	if report.SecretsRequested {
		t.Fatalf("false secrets_requested report = %+v, want not requested", report)
	}
	secretsRequested = true
	report, err = agentRunReportFromDetail(context.Background(), client, server.URL, detail)
	if err != nil {
		t.Fatalf("agent run report from true secret detail: %v", err)
	}
	if !report.SecretsRequested {
		t.Fatalf("true secrets_requested report = %+v, want requested", report)
	}
}

func TestAgentExplainIncludesPolicyReasons(t *testing.T) {
	reason := "network_mode is not allowed by current project policy"
	fixed := time.Date(2026, 6, 22, 6, 7, 8, 0, time.UTC)
	events := []preflightapi.RunEvent{{
		Type:      preflightapi.EventRunFailed,
		RunID:     "run_denied",
		Timestamp: fixed,
		Message:   reason,
		Status:    preflightapi.StatusDenied,
	}}
	detail := preflightapi.RunDetail{
		Summary: preflightapi.RunSummary{
			RunID:          "run_denied",
			ProjectID:      "proj_story",
			Command:        "make ci",
			Status:         preflightapi.StatusDenied,
			Classification: preflightapi.ClassificationPolicyDenied,
			AgentAction:    preflightapi.AgentActionReportSetup,
			FailureSummary: "The run was denied by project policy.",
			CreatedAt:      fixed,
		},
		Spec: preflightapi.RunSpec{
			RunID:       "run_denied",
			ProjectID:   "proj_story",
			Command:     "make ci",
			RunnerSize:  "standard",
			NetworkMode: "default",
		},
		PolicyReasons: []string{reason},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_denied":
			writeCLITestJSON(w, http.StatusOK, detail)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_denied/events":
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunEventsResponse{RunID: "run_denied", Events: events})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_denied/artifacts":
			writeCLITestJSON(w, http.StatusOK, preflightapi.ListArtifactsResponse{RunID: "run_denied"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	output, err := captureStdoutForCLITest(t, func() error {
		return agentExplainCommand([]string{"--api-url", server.URL, "--token", "pfl_story", "--json", "run_denied"})
	})
	if err != nil {
		t.Fatalf("agent explain: %v", err)
	}
	var explain agentExplainReport
	if err := json.Unmarshal([]byte(output), &explain); err != nil {
		t.Fatalf("decode agent explain: %v\n%s", err, output)
	}
	if len(explain.PolicyReasons) != 1 || explain.PolicyReasons[0] != reason {
		t.Fatalf("policy reasons = %+v, want %q", explain.PolicyReasons, reason)
	}
}

func TestAgentExplainIncludesEvidencePacksAndRicherRepair(t *testing.T) {
	exitCode := 1
	fixed := time.Date(2026, 6, 22, 6, 7, 8, 0, time.UTC)
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "test-results", "login"), 0o755); err != nil {
		t.Fatalf("mkdir test results: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(source, "playwright-report"), 0o755); err != nil {
		t.Fatalf("mkdir playwright report: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "test-results", "login", "trace.zip"), []byte("trace"), 0o644); err != nil {
		t.Fatalf("write trace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "playwright-report", "index.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatalf("write report: %v", err)
	}
	var archive bytes.Buffer
	buildArtifactArchiveForTest(t, source, []string{"test-results/**", "playwright-report/**"}, &archive)
	events := []preflightapi.RunEvent{
		{Type: preflightapi.EventStderr, RunID: "run_ui", Timestamp: fixed, Data: "Error: expect(locator).toBeVisible failed\nweb/tests/e2e/login.spec.ts:12:3\n"},
		{Type: preflightapi.EventCommandExited, RunID: "run_ui", Timestamp: fixed.Add(time.Second), ExitCode: &exitCode},
		{Type: preflightapi.EventRunFailed, RunID: "run_ui", Timestamp: fixed.Add(2 * time.Second), Message: "playwright failed", Status: preflightapi.StatusFailed, ExitCode: &exitCode},
	}
	detail := preflightapi.RunDetail{
		Summary: preflightapi.RunSummary{
			RunID:     "run_ui",
			ProjectID: "proj_story",
			Command:   "pnpm web:test:e2e",
			Status:    preflightapi.StatusFailed,
			ExitCode:  &exitCode,
			CreatedAt: fixed,
		},
		Spec: preflightapi.RunSpec{
			RunID:         "run_ui",
			ProjectID:     "proj_story",
			Command:       "pnpm web:test:e2e",
			CommandAlias:  "web-e2e",
			ArtifactPaths: []string{"test-results/**", "playwright-report/**"},
			NetworkMode:   "none",
		},
		PolicyReasons: []string{"network_mode none enforced by project policy"},
	}
	createdAt := fixed.Add(3 * time.Second)
	expiresAt := createdAt.Add(14 * 24 * time.Hour)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_ui":
			writeCLITestJSON(w, http.StatusOK, detail)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_ui/events":
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunEventsResponse{RunID: "run_ui", Events: events})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_ui/artifacts":
			writeCLITestJSON(w, http.StatusOK, preflightapi.ListArtifactsResponse{
				RunID: "run_ui",
				Artifacts: []preflightapi.ArtifactSummary{{
					ID:          "artifact_ui",
					RunID:       "run_ui",
					Name:        "artifacts.tar.gz",
					Path:        "artifacts.tar.gz",
					SizeBytes:   512,
					DownloadURL: "/v1/runs/run_ui/artifacts/archive",
					CreatedAt:   createdAt,
					ExpiresAt:   expiresAt,
				}},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_ui/artifacts/archive":
			_, _ = w.Write(archive.Bytes())
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	report, err := buildAgentExplainReport(context.Background(), api.NewClient(server.URL), server.URL, "run_ui")
	if err != nil {
		t.Fatalf("agent explain report: %v", err)
	}
	if len(report.EvidencePacks) != 2 {
		t.Fatalf("evidence packs = %+v, want test-results and playwright-report packs", report.EvidencePacks)
	}
	if !containsEvidencePackKind(report.EvidencePacks, "playwright_report") || !containsEvidencePackKind(report.EvidencePacks, "playwright_trace") {
		t.Fatalf("evidence packs = %+v, want playwright report and trace packs", report.EvidencePacks)
	}
	for _, pack := range report.EvidencePacks {
		if pack.ArtifactID != "artifact_ui" || !strings.Contains(pack.PreviewCommand, "readyci agent artifacts plan") {
			t.Fatalf("evidence pack = %+v, want artifact id and preview command", pack)
		}
	}
	if report.Repair.Summary == "" || report.Repair.RootCause == "" || report.Repair.ConfidenceNote == "" {
		t.Fatalf("repair packet = %+v, want summary/root cause/confidence note", report.Repair)
	}
	if len(report.Repair.TargetFiles) == 0 || report.Repair.TargetFiles[0].Path != "web/tests/e2e/login.spec.ts" {
		t.Fatalf("repair target files = %+v, want e2e spec path", report.Repair.TargetFiles)
	}
	if len(report.Repair.EvidencePacks) != len(report.EvidencePacks) {
		t.Fatalf("repair evidence packs = %+v, want copied top-level packs", report.Repair.EvidencePacks)
	}
	if !containsRepairCommandKind(report.Repair.Commands, "inspect_logs") || !containsRepairCommandKind(report.Repair.Commands, "inspect_evidence_pack") || !containsRepairCommandKind(report.Repair.Commands, "rerun") {
		t.Fatalf("repair commands = %+v, want logs/evidence/rerun commands", report.Repair.Commands)
	}
	if len(report.Repair.PolicyReasons) != 1 || report.Repair.PolicyReasons[0] != "network_mode none enforced by project policy" {
		t.Fatalf("repair policy reasons = %+v, want copied policy reason", report.Repair.PolicyReasons)
	}
}

func TestAgentExplainEvidencePacksReflectMatchedArtifactFiles(t *testing.T) {
	exitCode := 1
	fixed := time.Date(2026, 6, 22, 6, 7, 8, 0, time.UTC)
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "coverage"), 0o755); err != nil {
		t.Fatalf("mkdir coverage: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "coverage", "lcov.info"), []byte("TN:\n"), 0o644); err != nil {
		t.Fatalf("write coverage: %v", err)
	}
	var archive bytes.Buffer
	buildArtifactArchiveForTest(t, source, []string{"coverage/**"}, &archive)
	detail := preflightapi.RunDetail{
		Summary: preflightapi.RunSummary{
			RunID:     "run_coverage_only",
			ProjectID: "proj_story",
			Command:   "pnpm test",
			Status:    preflightapi.StatusFailed,
			ExitCode:  &exitCode,
			CreatedAt: fixed,
		},
		Spec: preflightapi.RunSpec{
			RunID:         "run_coverage_only",
			ProjectID:     "proj_story",
			Command:       "pnpm test",
			CommandAlias:  "web",
			ArtifactPaths: []string{"coverage/**", "test-results/**"},
			NetworkMode:   "none",
		},
	}
	events := []preflightapi.RunEvent{
		{Type: preflightapi.EventCommandExited, RunID: "run_coverage_only", Timestamp: fixed.Add(time.Second), ExitCode: &exitCode},
		{Type: preflightapi.EventRunFailed, RunID: "run_coverage_only", Timestamp: fixed.Add(2 * time.Second), Message: "tests failed", Status: preflightapi.StatusFailed, ExitCode: &exitCode},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_coverage_only":
			writeCLITestJSON(w, http.StatusOK, detail)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_coverage_only/events":
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunEventsResponse{RunID: "run_coverage_only", Events: events})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_coverage_only/artifacts":
			writeCLITestJSON(w, http.StatusOK, preflightapi.ListArtifactsResponse{
				RunID: "run_coverage_only",
				Artifacts: []preflightapi.ArtifactSummary{{
					ID:        "artifact_coverage",
					RunID:     "run_coverage_only",
					Name:      "artifacts.tar.gz",
					Path:      "artifacts.tar.gz",
					SizeBytes: int64(archive.Len()),
				}},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_coverage_only/artifacts/archive":
			_, _ = w.Write(archive.Bytes())
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	report, err := buildAgentExplainReport(context.Background(), api.NewClient(server.URL), server.URL, "run_coverage_only")
	if err != nil {
		t.Fatalf("agent explain report: %v", err)
	}
	if !containsEvidencePackKind(report.EvidencePacks, "coverage") {
		t.Fatalf("evidence packs = %+v, want coverage pack", report.EvidencePacks)
	}
	if containsEvidencePackKind(report.EvidencePacks, "test_results") {
		t.Fatalf("evidence packs = %+v, want no test_results pack when archive only matched coverage", report.EvidencePacks)
	}
}

func containsRepairCommandKind(commands []agentRepairCommand, kind string) bool {
	for _, command := range commands {
		if command.Kind == kind {
			return true
		}
	}
	return false
}

func TestAgentExplainUsesHostedDiagnosis(t *testing.T) {
	exitCode := 1
	fixed := time.Date(2026, 6, 22, 6, 7, 8, 0, time.UTC)
	detail := preflightapi.RunDetail{
		Summary: preflightapi.RunSummary{
			RunID:          "run_dep",
			ProjectID:      "proj_story",
			Command:        "npm ci",
			Status:         preflightapi.StatusFailed,
			ExitCode:       &exitCode,
			Classification: preflightapi.ClassificationDependencyDownloadFailed,
			Retryable:      true,
			AgentAction:    preflightapi.AgentActionRetry,
			FailureSummary: "Dependency download failed.",
			PrimaryFile:    "package-lock.json",
			CreatedAt:      fixed,
		},
		Spec: preflightapi.RunSpec{
			RunID:        "run_dep",
			ProjectID:    "proj_story",
			Command:      "npm ci",
			CommandAlias: "deps",
		},
	}
	events := []preflightapi.RunEvent{
		{Type: preflightapi.EventStderr, RunID: "run_dep", Timestamp: fixed, Data: "ELIFECYCLE Command failed with exit code 1\n"},
		{Type: preflightapi.EventRunFailed, RunID: "run_dep", Timestamp: fixed.Add(time.Second), Message: "npm ci failed", Status: preflightapi.StatusFailed, ExitCode: &exitCode},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_dep":
			writeCLITestJSON(w, http.StatusOK, detail)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_dep/events":
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunEventsResponse{RunID: "run_dep", Events: events})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_dep/artifacts":
			writeCLITestJSON(w, http.StatusOK, preflightapi.ListArtifactsResponse{RunID: "run_dep"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	report, err := buildAgentExplainReport(context.Background(), api.NewClient(server.URL), server.URL, "run_dep")
	if err != nil {
		t.Fatalf("agent explain report: %v", err)
	}
	if report.Diagnosis.Kind != preflightapi.ClassificationDependencyDownloadFailed || !report.Diagnosis.Retryable || report.Diagnosis.Action != preflightapi.AgentActionRetry || report.Diagnosis.Summary != "Dependency download failed." || report.Diagnosis.PrimaryFile != "package-lock.json" {
		t.Fatalf("diagnosis = %+v, want hosted dependency-download diagnosis", report.Diagnosis)
	}
	if report.Repair.Classification != preflightapi.ClassificationDependencyDownloadFailed || report.Repair.AgentAction != preflightapi.AgentActionRetry || report.Repair.PrimaryFile != "package-lock.json" {
		t.Fatalf("repair = %+v, want hosted diagnosis fields", report.Repair)
	}
}

func TestAgentExplainUsesObservedExitCodeWhenSummaryIsStale(t *testing.T) {
	exitCode := 1
	fixed := time.Date(2026, 6, 22, 6, 7, 8, 0, time.UTC)
	detail := preflightapi.RunDetail{
		Summary: preflightapi.RunSummary{
			RunID:     "run_stream",
			ProjectID: "proj_story",
			Command:   "go test ./...",
			Status:    preflightapi.StatusRunning,
			CreatedAt: fixed,
		},
		Spec: preflightapi.RunSpec{
			RunID:        "run_stream",
			ProjectID:    "proj_story",
			Command:      "go test ./...",
			CommandAlias: "touched",
		},
	}
	events := []preflightapi.RunEvent{
		{Type: preflightapi.EventStderr, RunID: "run_stream", Timestamp: fixed, Data: "--- FAIL: TestStore\nstore_test.go:42: expected queued, got failed\n"},
		{Type: preflightapi.EventCommandExited, RunID: "run_stream", Timestamp: fixed.Add(time.Second), ExitCode: &exitCode},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer pfl_story" {
			t.Fatalf("authorization = %q, want bearer token", got)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_stream":
			writeCLITestJSON(w, http.StatusOK, detail)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_stream/events":
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunEventsResponse{RunID: "run_stream", Events: events})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_stream/artifacts":
			writeCLITestJSON(w, http.StatusOK, preflightapi.ListArtifactsResponse{RunID: "run_stream"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	output, err := captureStdoutForCLITest(t, func() error {
		return agentExplainCommand([]string{"--api-url", server.URL, "--token", "pfl_story", "--json", "run_stream"})
	})
	if err != nil {
		t.Fatalf("agent explain stale summary: %v", err)
	}
	var report agentExplainReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode agent explain: %v\n%s", err, output)
	}
	if report.Status != preflightapi.StatusRunning || report.Terminal || report.ExitCode == nil || *report.ExitCode != 1 {
		t.Fatalf("agent explain report = %+v, want running non-terminal report with observed exit code 1", report)
	}
	if report.Diagnosis.Kind != preflightapi.ClassificationTestFailed {
		t.Fatalf("diagnosis = %+v, want test_failed", report.Diagnosis)
	}
}

func TestAgentResumeReturnsFailingExitCode(t *testing.T) {
	exitCode := 1
	fixed := time.Date(2026, 6, 22, 7, 8, 9, 0, time.UTC)
	events := []preflightapi.RunEvent{
		{Type: preflightapi.EventStderr, RunID: "run_fail", Timestamp: fixed, Data: "--- FAIL: TestStore\nplatform/internal/runs/store_test.go:42: expected queued, got failed\n"},
		{Type: preflightapi.EventCommandExited, RunID: "run_fail", Timestamp: fixed.Add(time.Second), ExitCode: &exitCode},
		{Type: preflightapi.EventRunFailed, RunID: "run_fail", Timestamp: fixed.Add(2 * time.Second), Message: "go test ./... failed", Status: preflightapi.StatusFailed, ExitCode: &exitCode},
	}
	detail := preflightapi.RunDetail{
		Summary: preflightapi.RunSummary{
			RunID:     "run_fail",
			ProjectID: "proj_story",
			Command:   "go test ./...",
			Status:    preflightapi.StatusFailed,
			ExitCode:  &exitCode,
			CreatedAt: fixed,
		},
		Spec: preflightapi.RunSpec{
			RunID:          "run_fail",
			ProjectID:      "proj_story",
			IdempotencyKey: "task-fail",
			Command:        "go test ./...",
			CommandAlias:   "touched",
		},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer pfl_story" {
			t.Fatalf("authorization = %q, want bearer token", got)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/by-idempotency-key":
			if r.URL.Query().Get("project") != "proj_story" || r.URL.Query().Get("key") != "task-fail" || r.URL.Query().Get("include_children") != "true" {
				t.Fatalf("resume query = %s, want project/key/include_children", r.URL.RawQuery)
			}
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunDetailsResponse{Runs: []preflightapi.RunDetail{detail}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_fail/events":
			if r.URL.Query().Get("format") != "json" {
				t.Fatalf("events query = %s, want format=json", r.URL.RawQuery)
			}
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunEventsResponse{RunID: "run_fail", Events: events})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	output, err := captureStdoutForCLITest(t, func() error {
		return agentResumeCommand([]string{"--api-url", server.URL, "--token", "pfl_story", "--project", "proj_story", "--json", "task-fail"})
	})
	var exitErr exitCodeError
	if !errors.As(err, &exitErr) || exitErr.code != 1 {
		t.Fatalf("agent resume error = %v, want exit code 1", err)
	}
	var report agentVerifyReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode agent resume report: %v\n%s", err, output)
	}
	if report.Status != preflightapi.StatusFailed || !report.Terminal || report.ExitCode == nil || *report.ExitCode != 1 || report.Classification != preflightapi.ClassificationTestFailed {
		t.Fatalf("agent resume report = %+v, want failed test report", report)
	}
}

func TestAgentResumeHonorsTerminalEventsWhenSummaryIsStale(t *testing.T) {
	exitCode := 124
	fixed := time.Date(2026, 6, 22, 7, 8, 9, 0, time.UTC)
	events := []preflightapi.RunEvent{
		{Type: preflightapi.EventRunTimedOut, RunID: "run_stale", Timestamp: fixed.Add(time.Second), Message: "timed out after 30m", Status: preflightapi.StatusTimedOut, ExitCode: &exitCode},
	}
	detail := preflightapi.RunDetail{
		Summary: preflightapi.RunSummary{
			RunID:     "run_stale",
			ProjectID: "proj_story",
			Command:   "go test ./...",
			Status:    preflightapi.StatusRunning,
			CreatedAt: fixed,
		},
		Spec: preflightapi.RunSpec{
			RunID:          "run_stale",
			ProjectID:      "proj_story",
			IdempotencyKey: "task-stale",
			Command:        "go test ./...",
			CommandAlias:   "touched",
		},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer pfl_story" {
			t.Fatalf("authorization = %q, want bearer token", got)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/by-idempotency-key":
			if r.URL.Query().Get("project") != "proj_story" || r.URL.Query().Get("key") != "task-stale" || r.URL.Query().Get("include_children") != "true" {
				t.Fatalf("resume query = %s, want project/key/include_children", r.URL.RawQuery)
			}
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunDetailsResponse{Runs: []preflightapi.RunDetail{detail}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_stale/events":
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunEventsResponse{RunID: "run_stale", Events: events})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	output, err := captureStdoutForCLITest(t, func() error {
		return agentResumeCommand([]string{"--api-url", server.URL, "--token", "pfl_story", "--project", "proj_story", "--json", "task-stale"})
	})
	var exitErr exitCodeError
	if !errors.As(err, &exitErr) || exitErr.code != cliExitRemoteFailure {
		t.Fatalf("agent resume stale summary error = %v, want exit code %d", err, cliExitRemoteFailure)
	}
	var report agentVerifyReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode agent resume report: %v\n%s", err, output)
	}
	if report.Status != preflightapi.StatusTimedOut || !report.Terminal || report.ExitCode == nil || *report.ExitCode != 124 || report.CLIExitCode != cliExitRemoteFailure || report.Classification != preflightapi.ClassificationTimeout {
		t.Fatalf("agent resume stale summary report = %+v, want timed_out report with exit code 124", report)
	}
}

func TestAgentResumeReturnsPendingWaitReport(t *testing.T) {
	fixed := time.Date(2026, 6, 22, 8, 9, 10, 0, time.UTC)
	detail := preflightapi.RunDetail{
		Summary: preflightapi.RunSummary{
			RunID:     "run_queue",
			ProjectID: "proj_story",
			Command:   "go test ./...",
			Status:    preflightapi.StatusQueued,
			CreatedAt: fixed,
		},
		Spec: preflightapi.RunSpec{
			RunID:          "run_queue",
			ProjectID:      "proj_story",
			IdempotencyKey: "task-pending",
			Command:        "go test ./...",
			CommandAlias:   "touched",
		},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer pfl_story" {
			t.Fatalf("authorization = %q, want bearer token", got)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/by-idempotency-key":
			if r.URL.Query().Get("project") != "proj_story" || r.URL.Query().Get("key") != "task-pending" || r.URL.Query().Get("include_children") != "true" {
				t.Fatalf("resume query = %s, want project/key/include_children", r.URL.RawQuery)
			}
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunDetailsResponse{Runs: []preflightapi.RunDetail{detail}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_queue/events":
			if r.URL.Query().Get("format") != "json" {
				t.Fatalf("events query = %s, want format=json", r.URL.RawQuery)
			}
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunEventsResponse{RunID: "run_queue"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	output, err := captureStdoutForCLITest(t, func() error {
		return agentResumeCommand([]string{"--api-url", server.URL, "--token", "pfl_story", "--project", "proj_story", "--json", "task-pending"})
	})
	if err != nil {
		t.Fatalf("agent resume pending returned error = %v, want nil", err)
	}
	var report agentVerifyReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode pending resume report: %v\n%s", err, output)
	}
	if report.Status != preflightapi.StatusQueued || report.Terminal || report.ExitCode != nil || report.Classification != preflightapi.ClassificationPending || report.Retryable {
		t.Fatalf("pending resume report = %+v, want queued pending wait report", report)
	}
	for _, action := range report.NextActions {
		if action.Kind == "rerun" {
			t.Fatalf("pending resume next actions included rerun: %+v", report.NextActions)
		}
	}
}
