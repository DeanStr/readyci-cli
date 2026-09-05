package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestUsageCommandPrintsAccountUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/account" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer pfl_usage" {
			t.Fatalf("authorization = %q, want bearer token", got)
		}
		writeCLITestJSON(w, http.StatusOK, preflightapi.AccountUsageResponse{
			TeamID: "team_usage",
			Plan: preflightapi.AccountPlanSummary{
				ID:                         "solo_19",
				Name:                       "Solo",
				MonthlyPriceCents:          1900,
				IncludedStandardMinutes:    5000,
				IncludedComputeSlotMinutes: 5000,
				MaxActiveRuns:              2,
				MaxActiveComputeSlots:      2,
				MaxQueuedRuns:              20,
				CacheQuotaBytes:            53687091200,
				DockerEnabled:              true,
				NetworkModes:               []string{"none", "default", "allowlist"},
			},
			Usage: preflightapi.AccountUsageSummary{
				StandardRunnerMinutes: 125.5,
				ComputeSlotMinutes:    125.5,
				ActiveAPITokens:       2,
				ProjectCount:          3,
			},
		})
	}))
	defer server.Close()

	previousStdout := os.Stdout
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdout: %v", err)
	}
	os.Stdout = writeEnd
	defer func() {
		os.Stdout = previousStdout
	}()
	if err := usageCommand([]string{"--api-url", server.URL, "--token", "pfl_usage"}); err != nil {
		t.Fatalf("usage command: %v", err)
	}
	if err := writeEnd.Close(); err != nil {
		t.Fatalf("close stdout pipe: %v", err)
	}
	output, err := io.ReadAll(readEnd)
	if err != nil {
		t.Fatalf("read stdout pipe: %v", err)
	}
	if err := readEnd.Close(); err != nil {
		t.Fatalf("close stdout read pipe: %v", err)
	}
	text := string(output)
	for _, want := range []string{"team_id: team_usage", "plan: solo_19", "compute_slot_minutes: 125.50", "max_active_compute_slots: 2", "network_modes: none,default,allowlist"} {
		if !strings.Contains(text, want) {
			t.Fatalf("usage output missing %q:\n%s", want, text)
		}
	}
}

func TestRunOutputHelpersPrintSecretPolicyAndSeconds(t *testing.T) {
	output, err := captureStdoutForCLITest(t, func() error {
		printRunSecretPolicy(&preflightapi.RunSecretPolicyEvaluation{
			Requested:   true,
			Eligible:    false,
			CommandHash: "sha256:test",
			Reasons:     []string{"command hash mismatch"},
		})
		return nil
	})
	if err != nil {
		t.Fatalf("print secret policy: %v", err)
	}
	requireCLITestOutputContains(t, output,
		"secret_policy_requested: true",
		"secret_policy_eligible: false",
		"secret_policy_command_hash: sha256:test",
		"secret_policy_reason: command hash mismatch",
	)
	if got := secondsLabel(0); got != "" {
		t.Fatalf("secondsLabel(0) = %q, want empty", got)
	}
	if got := secondsLabel(15); got != "15s" {
		t.Fatalf("secondsLabel(15) = %q, want 15s", got)
	}
}

func TestAccountAndProjectFallbackHelpers(t *testing.T) {
	plan := preflightapi.AccountPlanSummary{
		IncludedStandardMinutes: 100,
		MaxActiveRuns:           3,
	}
	if got := accountIncludedComputeSlotMinutes(plan); got != 200 {
		t.Fatalf("included slot minutes fallback = %d, want 200", got)
	}
	if got := accountPlanActiveSlotCapacity(plan); got != 6 {
		t.Fatalf("active slot fallback = %d, want 6", got)
	}
	plan.IncludedComputeSlotMinutes = 42
	plan.MaxActiveComputeSlots = 7
	if got := accountIncludedComputeSlotMinutes(plan); got != 42 {
		t.Fatalf("explicit included slot minutes = %d, want 42", got)
	}
	if got := accountComputeSlotMinuteAllowance(plan, preflightapi.AccountUsageSummary{BonusComputeSlotMinutes: 8}); got != 50 {
		t.Fatalf("compute slot minute allowance = %.2f, want 50", got)
	}
	if got := accountPlanActiveSlotCapacity(plan); got != 7 {
		t.Fatalf("explicit active slots = %d, want 7", got)
	}
	usage := preflightapi.AccountUsageSummary{StandardRunnerMinutes: 12.5}
	if got := accountComputeSlotMinutes(usage); got != 25 {
		t.Fatalf("compute slot fallback = %.2f, want 25", got)
	}
	usage.ComputeSlotMinutes = 9.5
	if got := accountComputeSlotMinutes(usage); got != 9.5 {
		t.Fatalf("explicit compute slot minutes = %.2f, want 9.5", got)
	}
	workerSummary := preflightapi.AdminWorkerSummary{MaxActiveRuns: 4}
	if got := workerActiveSlotCapacity(workerSummary); got != 8 {
		t.Fatalf("worker slot fallback = %d, want 8", got)
	}
	workerSummary.MaxActiveComputeSlots = 11
	if got := workerActiveSlotCapacity(workerSummary); got != 11 {
		t.Fatalf("worker explicit slots = %d, want 11", got)
	}

	if got, err := effectiveProjectID("https://api.readyci.test", " owner/repo "); err != nil || got != "owner/repo" {
		t.Fatalf("effective explicit project = %q/%v", got, err)
	}
	if got, err := effectiveProjectID("https://api.readyci.test", "owner\nrepo"); err == nil || !strings.Contains(err.Error(), "project_id must not contain control characters") {
		t.Fatalf("effective explicit project with control = %q/%v, want rejection", got, err)
	}
	if got := displayRunName(remoteRunOptions{CommandAlias: "unit"}); got != "unit" {
		t.Fatalf("display alias = %q, want unit", got)
	}
	longCommand := strings.Repeat("x", 80)
	if got := displayRunName(remoteRunOptions{Command: longCommand}); len(got) > 64 || !strings.HasPrefix(got, strings.Repeat("x", 20)) {
		t.Fatalf("display command = %q, want truncated command", got)
	}
}

func TestRunsLatestRejectsUnsafeProjectID(t *testing.T) {
	err := runsLatestCommand([]string{"--api-url", "https://api.readyci.test", "--token", "pfl_test", "--project", "owner\nrepo"})
	if err == nil || !strings.Contains(err.Error(), "project_id must not contain control characters") {
		t.Fatalf("runs latest unsafe project err = %v, want project_id rejection", err)
	}
}

func TestRunsStatusPrintsQueuedDiagnostics(t *testing.T) {
	t.Setenv("READYCI_API_TOKEN", "")
	t.Setenv("READYCI_ADMIN_TOKEN", "")

	createdAt := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/runs/run_queue" {
			http.NotFound(w, r)
			return
		}
		writeCLITestJSON(w, http.StatusOK, preflightapi.RunDetail{
			Summary: preflightapi.RunSummary{
				RunID:       "run_queue",
				ProjectID:   "airline",
				Command:     "make ci",
				Status:      preflightapi.StatusQueued,
				RunnerSize:  "standard",
				NetworkMode: "none",
				CacheMode:   "cache-only",
				CreatedAt:   createdAt,
			},
			Spec: preflightapi.RunSpec{
				RunID:        "run_queue",
				ProjectID:    "airline",
				Command:      "make ci",
				CommandAlias: "ci",
				RunnerSize:   "standard",
				NetworkMode:  "none",
				CacheMode:    "cache-only",
			},
		})
	}))
	defer server.Close()

	output, err := captureStdoutForCLITest(t, func() error {
		return runsStatusCommand([]string{"--api-url", server.URL, "--token", "pfl_run", "run_queue"})
	})
	if err != nil {
		t.Fatalf("runs status: %v", err)
	}
	for _, want := range []string{
		"diagnostics:",
		"run is queued and has not been claimed by a worker yet",
		"readyci doctor --api-url " + server.URL + " --project airline --check ci",
		"rerun runs status with --admin-token TOKEN",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("runs status output missing %q:\n%s", want, output)
		}
	}
}

func TestRunsStatusJSONIncludesQueuedDiagnostics(t *testing.T) {
	t.Setenv("READYCI_API_TOKEN", "")
	t.Setenv("READYCI_ADMIN_TOKEN", "")

	createdAt := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/runs/run_queue" {
			http.NotFound(w, r)
			return
		}
		writeCLITestJSON(w, http.StatusOK, preflightapi.RunDetail{
			Summary: preflightapi.RunSummary{
				RunID:       "run_queue",
				ProjectID:   "airline",
				Command:     "make ci",
				Status:      preflightapi.StatusQueued,
				RunnerSize:  "standard",
				NetworkMode: "none",
				CacheMode:   "cache-only",
				CreatedAt:   createdAt,
			},
			Spec: preflightapi.RunSpec{
				RunID:        "run_queue",
				ProjectID:    "airline",
				Command:      "make ci",
				CommandAlias: "ci",
				RunnerSize:   "standard",
				NetworkMode:  "none",
				CacheMode:    "cache-only",
			},
		})
	}))
	defer server.Close()

	output, err := captureStdoutForCLITest(t, func() error {
		return runsStatusCommand([]string{"--api-url", server.URL, "--token", "pfl_run", "--json", "run_queue"})
	})
	if err != nil {
		t.Fatalf("runs status --json: %v", err)
	}
	var report runStatusReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode runs status json: %v\n%s", err, output)
	}
	if report.Summary.RunID != "run_queue" || report.Spec.CommandAlias != "ci" {
		t.Fatalf("run status json identity = %+v", report)
	}
	if len(report.Diagnostics) == 0 || !containsDoctorDiagnostic(report.Diagnostics, "workers", "warn") {
		t.Fatalf("diagnostics = %+v, want queued worker warning", report.Diagnostics)
	}
}

func TestRunsStatusIncludesPolicyReasons(t *testing.T) {
	t.Setenv("READYCI_API_TOKEN", "")
	reason := "network_mode is not allowed by current project policy"
	createdAt := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/runs/run_denied" {
			http.NotFound(w, r)
			return
		}
		writeCLITestJSON(w, http.StatusOK, preflightapi.RunDetail{
			Summary: preflightapi.RunSummary{
				RunID:          "run_denied",
				ProjectID:      "airline",
				Command:        "make ci",
				Status:         preflightapi.StatusDenied,
				RunnerSize:     "standard",
				NetworkMode:    "default",
				CacheMode:      "ephemeral",
				Classification: preflightapi.ClassificationPolicyDenied,
				AgentAction:    preflightapi.AgentActionReportSetup,
				FailureSummary: "The run was denied by project policy.",
				CreatedAt:      createdAt,
			},
			Spec: preflightapi.RunSpec{
				RunID:       "run_denied",
				ProjectID:   "airline",
				Command:     "make ci",
				RunnerSize:  "standard",
				NetworkMode: "default",
				CacheMode:   "ephemeral",
			},
			PolicyReasons: []string{reason},
		})
	}))
	defer server.Close()

	output, err := captureStdoutForCLITest(t, func() error {
		return runsStatusCommand([]string{"--api-url", server.URL, "--token", "pfl_run", "run_denied"})
	})
	if err != nil {
		t.Fatalf("runs status: %v", err)
	}
	if !strings.Contains(output, "policy_reason: "+reason) {
		t.Fatalf("runs status output missing policy reason:\n%s", output)
	}

	jsonOutput, err := captureStdoutForCLITest(t, func() error {
		return runsStatusCommand([]string{"--api-url", server.URL, "--token", "pfl_run", "--json", "run_denied"})
	})
	if err != nil {
		t.Fatalf("runs status --json: %v", err)
	}
	var report runStatusReport
	if err := json.Unmarshal([]byte(jsonOutput), &report); err != nil {
		t.Fatalf("decode runs status json: %v\n%s", err, jsonOutput)
	}
	if len(report.PolicyReasons) != 1 || report.PolicyReasons[0] != reason {
		t.Fatalf("policy reasons = %+v, want %q", report.PolicyReasons, reason)
	}
}

func TestRunsStatusAdminTokenPrintsUnclaimableReasons(t *testing.T) {
	t.Setenv("READYCI_API_TOKEN", "")
	t.Setenv("READYCI_ADMIN_TOKEN", "")

	createdAt := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	now := time.Now().UTC()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/runs/run_queue":
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunDetail{
				Summary: preflightapi.RunSummary{
					RunID:       "run_queue",
					ProjectID:   "airline",
					Command:     "make ci",
					Status:      preflightapi.StatusQueued,
					RunnerSize:  "standard",
					NetworkMode: "none",
					Docker:      true,
					CacheMode:   "cache-only",
					CreatedAt:   createdAt,
				},
				Spec: preflightapi.RunSpec{
					RunID:        "run_queue",
					ProjectID:    "airline",
					Command:      "make ci",
					CommandAlias: "ci",
					Image:        preflightapi.DefaultImage,
					RunnerSize:   "standard",
					NetworkMode:  "none",
					Docker:       true,
					CacheMode:    "cache-only",
				},
			})
		case "/v1/admin/workers":
			writeCLITestJSON(w, http.StatusOK, preflightapi.AdminListWorkersResponse{Workers: []preflightapi.AdminWorkerSummary{
				{
					ID:                   "worker-default",
					SupportedRunnerSizes: []string{"standard"},
					SupportedImages:      []string{preflightapi.DefaultImage},
					NetworkModes:         []string{"default"},
					MaxActiveRuns:        2,
					BundleUploadBaseURL:  "http://100.64.0.10:19080",
					LastSeenAt:           &now,
				},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	output, err := captureStdoutForCLITest(t, func() error {
		return runsStatusCommand([]string{"--api-url", server.URL, "--token", "pfl_run", "--admin-token", "admin_run", "run_queue"})
	})
	if err != nil {
		t.Fatalf("runs status: %v", err)
	}
	for _, want := range []string{
		"fail: run is not claimable by registered workers",
		"docker is not advertised",
		"network mode none is not advertised",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("runs status output missing %q:\n%s", want, output)
		}
	}
}
