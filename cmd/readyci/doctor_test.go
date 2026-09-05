package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestDoctorHealthClientHasTimeout(t *testing.T) {
	if timeout := doctorHealthClient().Timeout; timeout <= 0 {
		t.Fatalf("doctor health client timeout = %s, want positive", timeout)
	}
}

func TestDoctorHealthUsesNormalizedAPIURL(t *testing.T) {
	cliTestWorkingDirectoryMu.Lock()
	defer cliTestWorkingDirectoryMu.Unlock()

	repo := t.TempDir()
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore wd: %v", err)
		}
	}()

	var gotPath, gotQuery, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/api/healthz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	rawAPIURL := strings.Replace(server.URL, "http://", "http://user:pass@", 1) + "/api?token=secret#fragment"
	report := buildDoctorReport(context.Background(), doctorOptions{APIURL: rawAPIURL})
	if !containsDoctorDiagnostic(report.Diagnostics, "api", "ok") {
		t.Fatalf("diagnostics = %+v, want normalized api health ok", report.Diagnostics)
	}
	if gotPath != "/api/healthz" || gotQuery != "" || gotAuth != "" {
		t.Fatalf("health request path/query/auth = %q/%q/%q, want normalized path without query/auth", gotPath, gotQuery, gotAuth)
	}
	if report.APIURL != normalizeAPIURL(rawAPIURL) {
		t.Fatalf("report api_url = %q, want normalized %q", report.APIURL, normalizeAPIURL(rawAPIURL))
	}
}

func TestDoctorReportCapturesUnclaimableWorkerReasons(t *testing.T) {
	cliTestWorkingDirectoryMu.Lock()
	defer cliTestWorkingDirectoryMu.Unlock()

	repo := t.TempDir()
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore wd: %v", err)
		}
	}()
	runGitForCLITest(t, "init")
	runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	if err := os.WriteFile("README.md", []byte("doctor\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	if err := os.WriteFile(".readyci.yml", []byte(`
project_id: airline
checks:
  ci:
    command: make ci
    docker: true
    network_mode: none
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")

	now := time.Now().UTC()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			w.WriteHeader(http.StatusOK)
		case "/v1/account":
			writeCLITestJSON(w, http.StatusOK, preflightapi.AccountUsageResponse{
				TeamID: "team_airline",
				Plan:   preflightapi.AccountPlanSummary{ID: "solo_19"},
			})
		case "/v1/projects/airline/settings":
			writeCLITestJSON(w, http.StatusOK, preflightapi.ProjectSettings{ProjectID: "airline", GitHubRepository: "readyci/airline"})
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

	report := buildDoctorReport(context.Background(), doctorOptions{
		APIURL:     server.URL,
		APIToken:   "pfl_user",
		AdminToken: "admin_worker",
		CheckName:  "ci",
	})
	if report.Summary.Failures != 1 {
		t.Fatalf("doctor failures = %d, want 1; report=%+v", report.Summary.Failures, report)
	}
	if report.WorkerCapability == nil || report.WorkerCapability.Claimable {
		t.Fatalf("worker capability = %+v, want unclaimable", report.WorkerCapability)
	}
	if !containsSubstring(report.WorkerCapability.Reasons, "docker is not advertised") {
		t.Fatalf("worker reasons = %v, want docker mismatch", report.WorkerCapability.Reasons)
	}
	if !containsSubstring(report.WorkerCapability.Reasons, "network mode none") {
		t.Fatalf("worker reasons = %v, want network mismatch", report.WorkerCapability.Reasons)
	}
	if !containsString(report.Next, "readyci admin workers --api-url "+server.URL+" --token '<admin-token>'") {
		t.Fatalf("next commands = %+v, want admin workers command", report.Next)
	}
}

func TestDoctorReportRequiresUploadCapableWorker(t *testing.T) {
	cliTestWorkingDirectoryMu.Lock()
	defer cliTestWorkingDirectoryMu.Unlock()

	repo := t.TempDir()
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore wd: %v", err)
		}
	}()
	runGitForCLITest(t, "init")
	runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	if err := os.WriteFile("README.md", []byte("doctor upload\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	if err := os.WriteFile(".readyci.yml", []byte(`
project_id: airline
checks:
  ci:
    command: make ci
    network_mode: none
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")

	now := time.Now().UTC()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			w.WriteHeader(http.StatusOK)
		case "/v1/account":
			writeCLITestJSON(w, http.StatusOK, preflightapi.AccountUsageResponse{
				TeamID: "team_airline",
				Plan:   preflightapi.AccountPlanSummary{ID: "solo_19"},
			})
		case "/v1/projects/airline/settings":
			writeCLITestJSON(w, http.StatusOK, preflightapi.ProjectSettings{ProjectID: "airline"})
		case "/v1/admin/workers":
			writeCLITestJSON(w, http.StatusOK, preflightapi.AdminListWorkersResponse{Workers: []preflightapi.AdminWorkerSummary{
				{
					ID:                   "worker-no-upload",
					SupportedRunnerSizes: []string{"standard"},
					SupportedImages:      []string{preflightapi.DefaultImage},
					NetworkModes:         []string{"none"},
					MaxActiveRuns:        2,
					LastSeenAt:           &now,
				},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	report := buildDoctorReport(context.Background(), doctorOptions{
		APIURL:     server.URL,
		APIToken:   "pfl_user",
		AdminToken: "admin_worker",
		CheckName:  "ci",
	})
	if report.Summary.Failures != 1 {
		t.Fatalf("doctor failures = %d, want 1; report=%+v", report.Summary.Failures, report)
	}
	if report.WorkerCapability == nil || !report.WorkerCapability.Claimable || report.WorkerCapability.UploadCapable {
		t.Fatalf("worker capability = %+v, want claimable but not upload-capable", report.WorkerCapability)
	}
	if !containsSubstring(report.WorkerCapability.UploadReasons, "bundle upload URL is not advertised") {
		t.Fatalf("upload reasons = %v, want missing bundle upload URL", report.WorkerCapability.UploadReasons)
	}
	if !containsDoctorDiagnostic(report.Diagnostics, "workers", "fail") {
		t.Fatalf("diagnostics = %+v, want worker upload failure", report.Diagnostics)
	}
}

func TestDoctorCoverageReportsGoPostgresGap(t *testing.T) {
	cliTestWorkingDirectoryMu.Lock()
	defer cliTestWorkingDirectoryMu.Unlock()

	repo := t.TempDir()
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore wd: %v", err)
		}
	}()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/db\nrequire github.com/lib/pq v1.10.0\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "store_test.go"), []byte(`package db

import (
	"os"
	"testing"
)

func TestStore(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("missing database")
	}
}
`), 0o644); err != nil {
		t.Fatalf("write store test: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
project_id: dbdemo
checks:
  go:
    command: go test ./...
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return doctorCoverageCommand([]string{"--json"})
	})
	if err != nil {
		t.Fatalf("doctor coverage: %v", err)
	}
	var report coverageReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode coverage report: %v\n%s", err, output)
	}
	if report.Status != "warn" || len(report.PossiblySkipped) != 1 {
		t.Fatalf("coverage report = %+v, want one warning gap", report)
	}
	gap := report.PossiblySkipped[0]
	if gap.SuggestedProfile != "" || gap.SuggestedCheck != "" || containsString(report.SuggestedProfiles, "go-postgres") {
		t.Fatalf("coverage gap = %+v profiles=%v, want hosted-only database suggestion omitted", gap, report.SuggestedProfiles)
	}
	if !containsSubstring(gap.Signals, "database URL") {
		t.Fatalf("coverage gap signals = %v, want database env signal", gap.Signals)
	}
}

func TestDoctorRepoReportsHarnessGaps(t *testing.T) {
	cliTestWorkingDirectoryMu.Lock()
	defer cliTestWorkingDirectoryMu.Unlock()

	repo := t.TempDir()
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore wd: %v", err)
		}
	}()
	runGitForCLITest(t, "init")
	runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	if err := os.WriteFile("README.md", []byte("harness doctor\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	if err := os.WriteFile(".readyci.yml", []byte(`
project_id: app
checks:
  web-e2e:
    command: pnpm playwright test
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")

	report := buildDoctorReport(context.Background(), doctorOptions{Repo: true})
	if report.Repo == nil || report.Repo.Harness == nil {
		t.Fatalf("repo doctor report = %+v, want harness report", report.Repo)
	}
	harness := report.Repo.Harness
	if harness.Status != "warn" || harness.AgentInstructionsOkay || harness.AffectedRulesEnabled || harness.EvidencePacksOkay {
		t.Fatalf("harness report = %+v, want warnings for instructions, affected rules, and evidence packs", harness)
	}
	if !containsDoctorDiagnostic(report.Diagnostics, "harness", "warn") {
		t.Fatalf("diagnostics = %+v, want harness warning", report.Diagnostics)
	}
	if !containsHarnessFinding(harness.Findings, "no portable Ready CI agent instructions") {
		t.Fatalf("harness findings = %+v, want missing agent instructions warning", harness.Findings)
	}
	if !containsHarnessFinding(harness.Findings, "browser or UI check does not retain") {
		t.Fatalf("harness findings = %+v, want missing evidence artifact warning", harness.Findings)
	}
}

func TestDoctorRepoStatusIncludesHarnessOnlyWarning(t *testing.T) {
	cliTestWorkingDirectoryMu.Lock()
	defer cliTestWorkingDirectoryMu.Unlock()

	repo := t.TempDir()
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore wd: %v", err)
		}
	}()
	runGitForCLITest(t, "init")
	runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	if err := os.MkdirAll("web", 0o755); err != nil {
		t.Fatalf("mkdir web: %v", err)
	}
	if err := os.WriteFile("README.md", []byte("Ready CI verifies browser checks.\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	if err := os.WriteFile(filepath.Join("web", "app.tsx"), []byte("export const app = true\n"), 0o644); err != nil {
		t.Fatalf("write web file: %v", err)
	}
	if err := os.WriteFile(".readyci.yml", []byte(`
project_id: app
affected:
  rules:
    - paths:
        - web/**
      checks:
        - web-e2e
checks:
  web-e2e:
    command: pnpm playwright test
    artifact_paths:
      - test-results/**
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")

	report := buildDoctorReport(context.Background(), doctorOptions{Repo: true})
	if report.Repo == nil || report.Repo.Harness == nil {
		t.Fatalf("repo doctor report = %+v, want harness report", report.Repo)
	}
	if report.Repo.Status != "warn" || report.Repo.Harness.Status != "warn" {
		t.Fatalf("repo doctor report = %+v, want repo status to reflect harness warning", report.Repo)
	}
	if len(report.Repo.UndefinedChecks) != 0 || len(report.Repo.UndefinedMake) != 0 || len(report.Repo.Unreferenced) != 0 {
		t.Fatalf("repo map diagnostics = undefined=%+v make=%+v unreferenced=%+v, want harness-only warning", report.Repo.UndefinedChecks, report.Repo.UndefinedMake, report.Repo.Unreferenced)
	}
	if !report.Repo.Harness.AffectedRulesEnabled || !report.Repo.Harness.EvidencePacksOkay {
		t.Fatalf("harness report = %+v, want only instruction warning", report.Repo.Harness)
	}
	if report.Repo.Harness.AgentInstructionsOkay {
		t.Fatalf("harness report = %+v, want missing portable agent instructions", report.Repo.Harness)
	}
}

func TestDoctorRepoDoesNotTreatCoverageAsBrowserEvidence(t *testing.T) {
	cfg := config.ProjectConfig{
		Artifacts: config.ArtifactConfig{Paths: []string{"coverage/**"}},
		Affected: config.AffectedConfig{Rules: []config.AffectedRule{{
			Paths:  []string{"web/**"},
			Checks: config.CommandList{"web-e2e"},
		}}},
		Checks: map[string]config.CheckConfig{
			"web-e2e": {Command: "pnpm playwright test"},
		},
	}
	report := buildRepoHarnessReport(t.TempDir(), cfg)
	if report.Status != "warn" || report.EvidencePacksOkay {
		t.Fatalf("harness report = %+v, want browser evidence warning when only coverage is retained", report)
	}
	if !containsHarnessFinding(report.Findings, "browser or UI check does not retain") {
		t.Fatalf("harness findings = %+v, want browser evidence finding", report.Findings)
	}
	if !containsString(report.EvidencePackKinds, "coverage") {
		t.Fatalf("evidence kinds = %+v, want coverage to remain reported as available evidence kind", report.EvidencePackKinds)
	}
}

func TestDoctorRepoDetectsStepBasedBrowserCheck(t *testing.T) {
	cfg := config.ProjectConfig{
		Affected: config.AffectedConfig{Rules: []config.AffectedRule{{
			Paths:  []string{"web/**"},
			Checks: config.CommandList{"smoke"},
		}}},
		Checks: map[string]config.CheckConfig{
			"smoke": {
				Steps: []config.StepConfig{{
					Name:    "browser smoke",
					Command: "pnpm playwright test",
				}},
			},
		},
	}
	report := buildRepoHarnessReport(t.TempDir(), cfg)
	if report.EvidencePacksOkay {
		t.Fatalf("harness report = %+v, want missing evidence warning for step-based browser check", report)
	}
	if len(report.EvidencePackChecks) != 1 || report.EvidencePackChecks[0].Check != "smoke" {
		t.Fatalf("evidence checks = %+v, want step-based smoke check included", report.EvidencePackChecks)
	}
	if !containsHarnessFinding(report.Findings, "browser or UI check does not retain") {
		t.Fatalf("harness findings = %+v, want browser evidence finding", report.Findings)
	}
}

func TestDoctorRepoHonorsReviewableBrowserEvidence(t *testing.T) {
	cfg := config.ProjectConfig{
		Affected: config.AffectedConfig{Rules: []config.AffectedRule{{
			Paths:  []string{"web/**"},
			Checks: config.CommandList{"web-e2e"},
		}}},
		Checks: map[string]config.CheckConfig{
			"web-e2e": {
				Command: "pnpm playwright test",
				Outputs: config.CheckOutputsConfig{
					Reviewable: []string{"playwright-report/**"},
				},
			},
		},
	}
	report := buildRepoHarnessReport(t.TempDir(), cfg)
	if !report.EvidencePacksOkay {
		t.Fatalf("harness report = %+v, want reviewable outputs to satisfy browser evidence", report)
	}
	if !containsString(report.EvidencePackKinds, "playwright_report") {
		t.Fatalf("evidence kinds = %+v, want playwright report from reviewable outputs", report.EvidencePackKinds)
	}
	if containsHarnessFinding(report.Findings, "browser or UI check does not retain") {
		t.Fatalf("harness findings = %+v, did not expect browser evidence finding", report.Findings)
	}
}

func TestDoctorRepoAcceptsJUnitBrowserEvidence(t *testing.T) {
	cfg := config.ProjectConfig{
		Affected: config.AffectedConfig{Rules: []config.AffectedRule{{
			Paths:  []string{"web/**"},
			Checks: config.CommandList{"web-e2e"},
		}}},
		Checks: map[string]config.CheckConfig{
			"web-e2e": {
				Command:       "pnpm playwright test",
				ArtifactPaths: []string{"test-results.xml"},
			},
		},
	}
	report := buildRepoHarnessReport(t.TempDir(), cfg)
	if !report.EvidencePacksOkay {
		t.Fatalf("harness report = %+v, want JUnit XML to satisfy retained browser evidence", report)
	}
	if !containsString(report.EvidencePackKinds, "junit") {
		t.Fatalf("evidence kinds = %+v, want junit", report.EvidencePackKinds)
	}
	if containsHarnessFinding(report.Findings, "browser or UI check does not retain") {
		t.Fatalf("harness findings = %+v, did not expect browser evidence finding", report.Findings)
	}
}

func TestDoctorRepoDoesNotTreatLogSubstringAsBrowserEvidence(t *testing.T) {
	cfg := config.ProjectConfig{
		Affected: config.AffectedConfig{Rules: []config.AffectedRule{{
			Paths:  []string{"web/**"},
			Checks: config.CommandList{"web-e2e"},
		}}},
		Checks: map[string]config.CheckConfig{
			"web-e2e": {
				Command:       "pnpm playwright test",
				ArtifactPaths: []string{"catalogs/**", "blogs/output.json"},
			},
		},
	}
	report := buildRepoHarnessReport(t.TempDir(), cfg)
	if report.EvidencePacksOkay {
		t.Fatalf("harness report = %+v, want missing evidence warning for unrelated logs substring paths", report)
	}
	if containsString(report.EvidencePackKinds, "logs") {
		t.Fatalf("evidence kinds = %+v, did not expect logs from catalogs/blogs", report.EvidencePackKinds)
	}
	if !containsHarnessFinding(report.Findings, "browser or UI check does not retain") {
		t.Fatalf("harness findings = %+v, want browser evidence finding", report.Findings)
	}
}

func TestDoctorRepoDetectsExplicitBrowserCheck(t *testing.T) {
	cfg := config.ProjectConfig{
		Affected: config.AffectedConfig{Rules: []config.AffectedRule{{
			Paths:  []string{"web/**"},
			Checks: config.CommandList{"smoke"},
		}}},
		Checks: map[string]config.CheckConfig{
			"smoke": {
				Command: "pnpm test",
				Browser: "chromium",
			},
		},
	}
	report := buildRepoHarnessReport(t.TempDir(), cfg)
	if report.EvidencePacksOkay {
		t.Fatalf("harness report = %+v, want missing evidence warning for explicit browser check", report)
	}
	if len(report.EvidencePackChecks) != 1 || report.EvidencePackChecks[0].Check != "smoke" {
		t.Fatalf("evidence checks = %+v, want explicit browser smoke check included", report.EvidencePackChecks)
	}
	if !containsHarnessFinding(report.Findings, "browser or UI check does not retain") {
		t.Fatalf("harness findings = %+v, want browser evidence finding", report.Findings)
	}
}

func TestPrintCoverageReportIncludesChecksGapsAndWarnings(t *testing.T) {
	report := coverageReport{
		Status:     "warn",
		ConfigPath: ".readyci.yml",
		ProjectID:  "airline",
		Checks: []coverageCheck{{
			Name:             "go",
			Command:          "go test ./...",
			GeneratedProfile: "go",
			Reasons:          []string{"uses TEST_DATABASE_URL", "imports database/sql"},
		}},
		PossiblySkipped: []coverageGap{{
			Target:  "postgres integration tests",
			Reason:  "database tests need TEST_DATABASE_URL",
			Signals: []string{"TEST_DATABASE_URL appears in tests"},
			Missing: []string{"hosted database test plan"},
		}},
		Warnings: []string{"coverage may miss integration-only branches"},
	}

	output, err := captureStdoutForCLITest(t, func() error {
		printCoverageReport(report)
		return nil
	})
	if err != nil {
		t.Fatalf("print coverage report: %v", err)
	}
	requireCLITestOutputContains(t, output,
		"coverage: readyci",
		"status: warn",
		"config_path: .readyci.yml",
		"project_id: airline",
		"checks:",
		"go: go test ./...",
		"profile: go",
		"reason: uses TEST_DATABASE_URL",
		"possibly_skipped:",
		"postgres integration tests: database tests need TEST_DATABASE_URL",
		"signal: TEST_DATABASE_URL appears in tests",
		"missing: hosted database test plan",
		"warning: coverage may miss integration-only branches",
	)
}

func TestDoctorReportFailRecordsFailureDiagnostic(t *testing.T) {
	var report doctorReport
	report.fail("auth", "token rejected", "run readyci login")
	if report.Summary.Failures != 1 {
		t.Fatalf("failures = %d, want 1", report.Summary.Failures)
	}
	if len(report.Diagnostics) != 1 {
		t.Fatalf("diagnostics = %+v, want one failure diagnostic", report.Diagnostics)
	}
	diagnostic := report.Diagnostics[0]
	if diagnostic.Section != "auth" || diagnostic.Level != "fail" || diagnostic.Message != "token rejected" {
		t.Fatalf("diagnostic = %+v, want auth failure", diagnostic)
	}
	if !containsString(diagnostic.Hints, "run readyci login") {
		t.Fatalf("diagnostic hints = %v, want login hint", diagnostic.Hints)
	}
}

func TestDoctorCoverageRecognizesGoPostgresCheck(t *testing.T) {
	cliTestWorkingDirectoryMu.Lock()
	defer cliTestWorkingDirectoryMu.Unlock()

	repo := t.TempDir()
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore wd: %v", err)
		}
	}()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/db\nrequire github.com/jackc/pgx/v5 v5.0.0\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "store_test.go"), []byte("package db\nconst dsn = \"READYCI_TEST_DATABASE_URL\"\n"), 0o644); err != nil {
		t.Fatalf("write store test: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
project_id: dbdemo
checks:
  go-postgres:
    command: go test ./...
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	report := buildCoverageReport(".")
	if report.Status != "ok" || len(report.PossiblySkipped) != 0 {
		t.Fatalf("coverage report = %+v, want ok with no gaps", report)
	}
}

func TestDoctorCoverageRecognizesFirstClassPostgresService(t *testing.T) {
	cliTestWorkingDirectoryMu.Lock()
	defer cliTestWorkingDirectoryMu.Unlock()

	repo := t.TempDir()
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore wd: %v", err)
		}
	}()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/db\nrequire github.com/jackc/pgx/v5 v5.0.0\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "store_test.go"), []byte("package db\nconst dsn = \"READYCI_TEST_DATABASE_URL\"\n"), 0o644); err != nil {
		t.Fatalf("write store test: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
project_id: dbdemo
services:
  postgres:
    type: postgres
    version: "16"
    env:
      POSTGRES_DB: readyci
      POSTGRES_PASSWORD: readyci
    health:
      command: pg_isready -U postgres
checks:
  go:
    command: go test ./...
    docker: true
    services:
      - postgres
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	report := buildCoverageReport(".")
	if report.Status != "ok" || len(report.PossiblySkipped) != 0 {
		t.Fatalf("coverage report = %+v, want ok with no gaps", report)
	}
}

func TestDoctorReportDoesNotWriteFallbackProjectIDIntoSetupHint(t *testing.T) {
	cliTestWorkingDirectoryMu.Lock()
	defer cliTestWorkingDirectoryMu.Unlock()

	repo := t.TempDir()
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore wd: %v", err)
		}
	}()
	runGitForCLITest(t, "init")
	runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	if err := os.WriteFile("README.md", []byte("doctor setup hint\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	report := buildDoctorReport(context.Background(), doctorOptions{APIURL: server.URL})
	if report.ProjectID != "proj_dev" || report.ProjectIDSource != "default" {
		t.Fatalf("project identity = %s/%s, want proj_dev/default", report.ProjectID, report.ProjectIDSource)
	}
	want := "readyci setup --write-config --detect --api-url " + server.URL
	if !containsString(report.Next, want) {
		t.Fatalf("next commands = %+v, want setup without project", report.Next)
	}
	for _, command := range report.Next {
		if strings.Contains(command, "--project proj_dev") {
			t.Fatalf("next command wrote fallback project id: %s", command)
		}
	}
}

func TestDoctorCommandDoesNotUseUserTokenForOptionalWorkerInventory(t *testing.T) {
	cliTestWorkingDirectoryMu.Lock()
	defer cliTestWorkingDirectoryMu.Unlock()

	t.Setenv("READYCI_API_TOKEN", "pfl_user")
	t.Setenv("READYCI_ADMIN_TOKEN", "")

	repo := t.TempDir()
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore wd: %v", err)
		}
	}()
	runGitForCLITest(t, "init")
	runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	if err := os.WriteFile(".readyci.yml", []byte(`
project_id: airline
checks:
  ci:
    command: make ci
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")

	adminCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			w.WriteHeader(http.StatusOK)
		case "/v1/account":
			writeCLITestJSON(w, http.StatusOK, preflightapi.AccountUsageResponse{
				TeamID: "team_airline",
				Plan:   preflightapi.AccountPlanSummary{ID: "solo_19"},
			})
		case "/v1/projects/airline/settings":
			writeCLITestJSON(w, http.StatusOK, preflightapi.ProjectSettings{ProjectID: "airline"})
		case "/v1/admin/workers":
			adminCalled = true
			http.Error(w, "admin should not be called", http.StatusForbidden)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	output, err := captureStdoutForCLITest(t, func() error {
		return doctorCommand([]string{"--api-url", server.URL, "--check", "ci"})
	})
	if err != nil {
		t.Fatalf("doctor command: %v\n%s", err, output)
	}
	if adminCalled {
		t.Fatal("doctor used READYCI_API_TOKEN for optional admin worker inventory")
	}
	if !strings.Contains(output, "worker capability check skipped because no admin token was provided") {
		t.Fatalf("doctor output missing worker skip warning:\n%s", output)
	}
}

func TestDoctorCommandUsesPublicWorkerCapability(t *testing.T) {
	t.Setenv("READYCI_PROJECT_ID", "")
	cliTestWorkingDirectoryMu.Lock()
	defer cliTestWorkingDirectoryMu.Unlock()

	repo := t.TempDir()
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore wd: %v", err)
		}
	}()
	runGitForCLITest(t, "init")
	if err := os.WriteFile(".readyci.yml", []byte(`
project_id: airline
checks:
  ci:
    command: make ci
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	publicCapabilityCalled := false
	adminCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			w.WriteHeader(http.StatusOK)
		case "/v1/account":
			writeCLITestJSON(w, http.StatusOK, preflightapi.AccountUsageResponse{
				TeamID: "team_airline",
				Plan:   preflightapi.AccountPlanSummary{ID: "solo_19"},
			})
		case "/v1/projects/airline/settings":
			writeCLITestJSON(w, http.StatusOK, preflightapi.ProjectSettings{ProjectID: "airline", HomeRegion: "us-east-1"})
		case "/v1/workers/capability":
			publicCapabilityCalled = true
			var req preflightapi.WorkerCapabilityCheckRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode worker capability request: %v", err)
			}
			if req.Spec.Region != "us-east-1" {
				t.Fatalf("worker capability region = %q, want project home region", req.Spec.Region)
			}
			writeCLITestJSON(w, http.StatusOK, preflightapi.WorkerCapabilityCheckResponse{
				Claimable:                 true,
				UploadCapable:             true,
				FreshWorkerCount:          1,
				MatchingWorkerCount:       1,
				MatchingUploadWorkerCount: 1,
			})
		case "/v1/admin/workers":
			adminCalled = true
			http.Error(w, "admin should not be called", http.StatusForbidden)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	output, err := captureStdoutForCLITest(t, func() error {
		return doctorCommand([]string{"--api-url", server.URL, "--token", "pfl_user", "--check", "ci"})
	})
	if err != nil {
		t.Fatalf("doctor command: %v\n%s", err, output)
	}
	if !publicCapabilityCalled {
		t.Fatal("doctor did not call public worker capability endpoint")
	}
	if adminCalled {
		t.Fatal("doctor used admin worker inventory without admin token")
	}
	requireCLITestOutputContains(t, output, "workers:", "ok: check ci has 1 matching upload-capable worker(s)")
}

func TestWorkerCapabilityChecksDockerImageAndNetwork(t *testing.T) {
	now := time.Now().UTC()
	plan := checkRunPlan{Options: remoteRunOptions{
		Image:       "ubuntu-24.04-ci",
		RunnerSize:  "standard",
		NetworkMode: "none",
		Docker:      true,
	}}
	report := checkWorkerCapability(plan, []preflightapi.AdminWorkerSummary{
		{
			ID:                   "worker-bad",
			SupportedRunnerSizes: []string{"standard"},
			SupportedImages:      []string{"ubuntu-24.04-ci"},
			NetworkModes:         []string{"default"},
			MaxActiveRuns:        1,
			BundleUploadBaseURL:  "http://100.64.0.10:19080",
			LastSeenAt:           &now,
		},
		{
			ID:                    "worker-good",
			SupportedRunnerSizes:  []string{"standard"},
			SupportedImages:       []string{"ubuntu-24.04-ci"},
			SupportedDockerImages: []string{"ubuntu-24.04-ci"},
			NetworkModes:          []string{"none"},
			SupportsDocker:        true,
			MaxActiveRuns:         1,
			BundleUploadBaseURL:   "http://100.64.0.11:19080",
			LastSeenAt:            &now,
		},
	})
	if !report.Claimable || !report.UploadCapable || len(report.MatchingWorkers) != 1 || report.MatchingWorkers[0] != "worker-good" {
		t.Fatalf("capability report = %+v", report)
	}
	if len(report.Reasons) != 1 || !strings.Contains(report.Reasons[0], "network mode none") {
		t.Fatalf("capability reasons = %+v", report.Reasons)
	}
}

func TestWorkerCapabilityTreatsServicesAsDockerRequirement(t *testing.T) {
	now := time.Now().UTC()
	plan := checkRunPlan{Options: remoteRunOptions{
		RunnerSize:  "standard",
		NetworkMode: "none",
		Services: []preflightapi.RunServiceSpec{{
			Name: "postgres",
			Type: "postgres",
		}},
	}}
	report := checkWorkerCapability(plan, []preflightapi.AdminWorkerSummary{{
		ID:                   "worker-plain",
		SupportedRunnerSizes: []string{"standard"},
		SupportedImages:      []string{preflightapi.DefaultImage},
		NetworkModes:         []string{"none"},
		MaxActiveRuns:        1,
		BundleUploadBaseURL:  "http://100.64.0.10:19080",
		LastSeenAt:           &now,
	}})
	if report.Claimable || !containsSubstring(report.Reasons, "docker is not advertised") {
		t.Fatalf("worker capability = %+v, want service run to require docker", report)
	}
}

func TestWorkerCapabilityRequiresFreshUploadWorker(t *testing.T) {
	now := time.Now().UTC()
	stale := now.Add(-doctorFreshWorkerWindow - time.Minute)
	plan := checkRunPlan{Options: remoteRunOptions{
		Image:       preflightapi.DefaultImage,
		RunnerSize:  "standard",
		NetworkMode: "none",
	}}
	report := checkWorkerCapability(plan, []preflightapi.AdminWorkerSummary{
		{
			ID:                   "worker-no-upload",
			SupportedRunnerSizes: []string{"standard"},
			SupportedImages:      []string{preflightapi.DefaultImage},
			NetworkModes:         []string{"none"},
			MaxActiveRuns:        1,
			LastSeenAt:           &now,
		},
		{
			ID:                   "worker-stale-upload",
			SupportedRunnerSizes: []string{"standard"},
			SupportedImages:      []string{preflightapi.DefaultImage},
			NetworkModes:         []string{"none"},
			MaxActiveRuns:        1,
			BundleUploadBaseURL:  "http://100.64.0.10:19080",
			LastSeenAt:           &stale,
		},
	})
	if !report.Claimable || report.UploadCapable {
		t.Fatalf("capability report = %+v, want claimable worker but no fresh upload-capable worker", report)
	}
	if len(report.MatchingWorkers) != 1 || report.MatchingWorkers[0] != "worker-no-upload" {
		t.Fatalf("matching workers = %v, want worker without upload URL to remain claimable", report.MatchingWorkers)
	}
	if !containsSubstring(report.UploadReasons, "bundle upload URL is not advertised") {
		t.Fatalf("upload reasons = %v, want missing bundle upload URL", report.UploadReasons)
	}
	if !containsSubstring(report.UploadReasons, "worker was not seen recently") {
		t.Fatalf("upload reasons = %v, want stale worker", report.UploadReasons)
	}
}

func containsHarnessFinding(findings []repoHarnessFinding, text string) bool {
	for _, finding := range findings {
		if strings.Contains(finding.Message, text) {
			return true
		}
	}
	return false
}
