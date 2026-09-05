package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestCheckDryRunWhyIncludesVerificationPlan(t *testing.T) {
	setupInferredGoRepo(t)

	output, err := captureStdoutForCLITest(t, func() error {
		return checkCommand([]string{"--dry-run", "--why"})
	})
	if err != nil {
		t.Fatalf("check dry-run --why: %v\n%s", err, output)
	}
	requireCLITestOutputContains(t, output,
		"affected: dry-run",
		"verification: plan",
		"mode: affected_rules",
		"selected:",
		"  - go: Selected go because main.go matched **/*.go (rule 1).",
		"why:",
		"No Ready CI config was found",
	)
}

func TestCheckDryRunWhyJSONIncludesTypedPlan(t *testing.T) {
	setupInferredGoRepo(t)

	output, err := captureStdoutForCLITest(t, func() error {
		return checkCommand([]string{"--dry-run", "--why", "--json"})
	})
	if err != nil {
		t.Fatalf("check dry-run --why --json: %v\n%s", err, output)
	}
	var report affectedDryRunReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode affected dry-run: %v\n%s", err, output)
	}
	if report.Plan == nil {
		t.Fatalf("plan missing from dry-run report: %+v", report)
	}
	if report.Plan.Mode != "affected_rules" || !report.Plan.Inferred || len(report.Plan.Selected) != 1 || report.Plan.Selected[0].Name != "go" {
		t.Fatalf("plan = %+v, want inferred affected go selection", report.Plan)
	}
	if len(report.Plan.Signals) == 0 || len(report.Plan.Signals[0].Evidence) == 0 {
		t.Fatalf("plan signals = %+v, want detector evidence", report.Plan.Signals)
	}
	if report.Plan.Confidence <= 0 || report.Plan.Confidence > 1 {
		t.Fatalf("plan confidence = %v, want normalized score", report.Plan.Confidence)
	}
}

func TestCheckCandidatesFromConfigUseResourceDefaults(t *testing.T) {
	cfg := config.ProjectConfig{
		Resources: config.ResourceConfig{
			Default: config.ResourceDefaults{Size: "large", Timeout: "45m", Docker: boolPtr(true)},
		},
		Checks: map[string]config.CheckConfig{
			"ci": {Command: "make ci"},
		},
	}
	candidates := checkCandidatesFromConfig(cfg, "ci")
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v, want one", candidates)
	}
	candidate := candidates[0]
	if candidate.Cost.RunnerSize != "large" || candidate.Cost.TimeoutSeconds != 2700 || !candidate.Needs.Docker {
		t.Fatalf("candidate = %+v, want resource default size/timeout/docker", candidate)
	}
}

func TestCheckWhyJSONActualRunIncludesPlanAndCreatedRun(t *testing.T) {
	repo := chdirCLITestRepo(t, "check-why-json-run")
	runGitForCLITest(t, "init")
	runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
project_id: airline
checks:
  ci:
    command: go test ./...
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs":
			writeCLITestJSON(w, http.StatusCreated, preflightapi.CreateRunResponse{
				RunID:     "run_json_why",
				UploadURL: serverURL + "/upload/run_json_why",
				EventsURL: serverURL + "/events/run_json_why",
				Status:    preflightapi.StatusAwaitingUpload,
			})
		case r.Method == http.MethodPut && r.URL.Path == "/upload/run_json_why":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte("{}\n"))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs/run_json_why/upload-complete":
			writeCLITestJSON(w, http.StatusOK, preflightapi.CompleteUploadResponse{RunID: "run_json_why", Status: preflightapi.StatusQueued})
		case r.Method == http.MethodGet && r.URL.Path == "/events/run_json_why":
			writeCLITestFinishedEvent(w)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	output, err := captureStdoutForCLITest(t, func() error {
		return checkCommand([]string{"--api-url", server.URL, "--token", "pfl_json", "--why", "--json", "ci"})
	})
	if err != nil && !isSuccessfulExit(err) {
		t.Fatalf("check --why --json: %v\n%s", err, output)
	}
	var report checkExecutionReport
	if decodeErr := json.Unmarshal([]byte(output), &report); decodeErr != nil {
		t.Fatalf("decode execution report: %v\n%s", decodeErr, output)
	}
	if report.Plan == nil || len(report.Plan.Selected) != 1 || report.Plan.Selected[0].Name != "ci" {
		t.Fatalf("execution plan = %+v, want selected ci", report.Plan)
	}
	if len(report.CreatedRuns) != 1 || report.CreatedRuns[0].RunID != "run_json_why" || report.CreatedRuns[0].Check != "ci" {
		t.Fatalf("created runs = %+v, want ci run", report.CreatedRuns)
	}
	if report.Result.Status != preflightapi.StatusSucceeded || report.Result.Classification != preflightapi.ClassificationSuccess || report.Result.ExitCode != 0 {
		t.Fatalf("result = %+v, want succeeded success", report.Result)
	}
}

func TestCheckWhyJSONActualRunKeepsMatrixCheckNamesStable(t *testing.T) {
	repo := chdirCLITestRepo(t, "check-why-json-matrix-run")
	runGitForCLITest(t, "init")
	runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
project_id: airline
checks:
  ci:
    command: go test ./...
    matrix:
      GO:
        - "1.25"
        - "1.26"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var serverURL string
	var created []preflightapi.CreateRunRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs":
			var req preflightapi.CreateRunRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode create request: %v", err)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			created = append(created, req)
			runID := fmt.Sprintf("run_json_matrix_%d", len(created))
			writeCLITestJSON(w, http.StatusCreated, preflightapi.CreateRunResponse{
				RunID:     runID,
				UploadURL: serverURL + "/upload/" + runID,
				EventsURL: serverURL + "/events/" + runID,
				Status:    preflightapi.StatusAwaitingUpload,
			})
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/upload/run_json_matrix_"):
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte("{}\n"))
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/runs/run_json_matrix_") && strings.HasSuffix(r.URL.Path, "/upload-complete"):
			runID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/runs/"), "/upload-complete")
			writeCLITestJSON(w, http.StatusOK, preflightapi.CompleteUploadResponse{RunID: runID, Status: preflightapi.StatusQueued})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/events/run_json_matrix_"):
			writeCLITestFinishedEvent(w)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	output, err := captureStdoutForCLITest(t, func() error {
		return checkCommand([]string{"--api-url", server.URL, "--token", "pfl_json", "--why", "--json", "ci"})
	})
	if err != nil && !isSuccessfulExit(err) {
		t.Fatalf("check --why --json matrix: %v\n%s", err, output)
	}
	var report checkExecutionReport
	if decodeErr := json.Unmarshal([]byte(output), &report); decodeErr != nil {
		t.Fatalf("decode execution report: %v\n%s", decodeErr, output)
	}
	if len(created) != 2 {
		t.Fatalf("created requests = %d, want 2: %+v", len(created), created)
	}
	for _, req := range created {
		if !strings.HasPrefix(req.CommandAlias, "ci [GO=") {
			t.Fatalf("remote command alias = %q, want matrix-decorated ci alias", req.CommandAlias)
		}
	}
	if len(report.CreatedRuns) != 2 {
		t.Fatalf("created runs = %+v, want 2 matrix runs", report.CreatedRuns)
	}
	for _, run := range report.CreatedRuns {
		if run.Check != "ci" {
			t.Fatalf("created run check = %q, want stable ci in %+v", run.Check, report.CreatedRuns)
		}
		if run.MatrixVariant == "" {
			t.Fatalf("created run missing matrix variant: %+v", run)
		}
	}
}

func TestCheckExecutionResultForPreCreateErrorIsNonZeroUnknown(t *testing.T) {
	result := checkExecutionResultFromRun(remoteRunResult{}, errors.New("build bundle failed"))
	if result.Status != "unknown" || result.ExitCode == 0 || result.Classification == "" {
		t.Fatalf("result = %+v, want nonzero unknown classified error", result)
	}
}

func TestCheckDryRunWhyJSONIncludesRepoMapSignalsForDefaultInferredCheck(t *testing.T) {
	repo := setupInferredGoRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "Makefile"), []byte("check-ci:\n\tgo test ./...\n"), 0o644); err != nil {
		t.Fatalf("write Makefile: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return checkCommand([]string{"--dry-run", "--why", "--json"})
	})
	if err != nil {
		t.Fatalf("check dry-run --why --json: %v\n%s", err, output)
	}
	var report affectedDryRunReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode affected dry-run: %v\n%s", err, output)
	}
	if report.Plan == nil {
		t.Fatalf("plan missing from dry-run report: %+v", report)
	}
	if !planSignalEvidenceContains(report.Plan.Signals, "make-wrappers at Makefile") {
		t.Fatalf("plan signals = %+v, want repo-map make-wrappers signal", report.Plan.Signals)
	}
}

func TestCheckDryRunWhyJSONIncludesRepoMapSignalsFromGitRoot(t *testing.T) {
	repo := setupInferredGoRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "Makefile"), []byte("check-ci:\n\tgo test ./...\n"), 0o644); err != nil {
		t.Fatalf("write Makefile: %v", err)
	}
	subdir := filepath.Join(repo, "cmd", "tool")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	if err := os.Chdir(subdir); err != nil {
		t.Fatalf("chdir subdir: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return checkCommand([]string{"--dry-run", "--why", "--json"})
	})
	if err != nil {
		t.Fatalf("check dry-run --why --json from subdir: %v\n%s", err, output)
	}
	var report affectedDryRunReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode affected dry-run: %v\n%s", err, output)
	}
	if report.Plan == nil || !planSignalEvidenceContains(report.Plan.Signals, "make-wrappers at Makefile") {
		t.Fatalf("plan signals = %+v, want root repo-map make-wrappers signal from subdir", report.Plan)
	}
}

func TestCheckDryRunWhyJSONPreservesAffectedServiceNeeds(t *testing.T) {
	t.Setenv("READYCI_API_URL", "")
	t.Setenv("READYCI_API_TOKEN", "")
	t.Setenv("READYCI_PROJECT_ID", "")
	repo := chdirCLITestRepo(t, "service-plan")
	runGitForCLITest(t, "init")
	runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	if err := os.MkdirAll(filepath.Join(repo, "db"), 0o755); err != nil {
		t.Fatalf("mkdir db: %v", err)
	}
	configData := []byte(`version: 1
project_id: service-plan
cache:
  mode: cache-only
services:
  postgres:
    type: postgres
    version: "16"
    health:
      command: pg_isready -U postgres
affected:
  rules:
    - paths:
        - db/**
      checks:
        - db
checks:
  db:
    command: go test ./...
    docker: true
    services:
      - postgres
    network_mode: none
`)
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), configData, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "db", "schema.sql"), []byte("-- initial\n"), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, "db", "schema.sql"), []byte("-- changed\n"), 0o644); err != nil {
		t.Fatalf("change schema: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return checkCommand([]string{"--dry-run", "--why", "--json"})
	})
	if err != nil {
		t.Fatalf("check dry-run --why --json: %v\n%s", err, output)
	}
	var report affectedDryRunReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode affected dry-run: %v\n%s", err, output)
	}
	if len(report.SelectedChecks) != 1 || report.SelectedChecks[0].Name != "db" {
		t.Fatalf("selected checks = %+v, want db", report.SelectedChecks)
	}
	if !contains(report.SelectedChecks[0].Services, "postgres") {
		t.Fatalf("selected check services = %+v, want postgres", report.SelectedChecks[0].Services)
	}
	if report.SelectedChecks[0].CacheMode != "cache-only" {
		t.Fatalf("selected check cache mode = %q, want cache-only", report.SelectedChecks[0].CacheMode)
	}
	if report.Plan == nil || len(report.Plan.Selected) != 1 {
		t.Fatalf("plan = %+v, want one selected check", report.Plan)
	}
	if !contains(report.Plan.Selected[0].Needs.Services, "postgres") {
		t.Fatalf("plan selected needs = %+v, want postgres service", report.Plan.Selected[0].Needs)
	}
	if report.Plan.Selected[0].Needs.CacheMode != "cache-only" || report.Plan.Selected[0].Needs.NetworkMode != "none" || !report.Plan.Selected[0].Needs.Docker {
		t.Fatalf("plan selected needs = %+v, want cache-only none docker", report.Plan.Selected[0].Needs)
	}
}

func planSignalEvidenceContains(signals []repoSignal, needle string) bool {
	for _, signal := range signals {
		for _, evidence := range signal.Evidence {
			if strings.Contains(evidence, needle) {
				return true
			}
		}
	}
	return false
}

func TestCheckFallbackWhyExplainsNoAffectedMatch(t *testing.T) {
	setupCleanInferredGoRepo(t)

	output, err := captureStdoutForCLITest(t, func() error {
		return checkCommand([]string{"--dry-run", "--why"})
	})
	if err != nil {
		t.Fatalf("check clean dry-run --why: %v\n%s", err, output)
	}
	requireCLITestOutputContains(t, output,
		"check: dry-run",
		"verification: plan",
		"mode: inferred_fallback",
		"No inferred affected checks matched changed files; selected the narrowest detected check.",
	)
}

func TestVerificationPlanPrintsSkippedChecks(t *testing.T) {
	output, err := captureStdoutForCLITest(t, func() error {
		printVerificationPlan(verificationPlan{
			Goal:       "default",
			Mode:       "affected_rules",
			Confidence: 0.9,
			Selected: []checkCandidate{{
				Name:   "web",
				Reason: "Selected web because web/src/App.tsx matched web/**.",
			}},
			Skipped: []skippedCheck{{
				Name:   "web-test",
				Reason: "subsumed by web",
			}},
			Explanation: []string{"Selected web because web/src/App.tsx matched web/**."},
		})
		return nil
	})
	if err != nil {
		t.Fatalf("capture verification plan: %v", err)
	}
	requireCLITestOutputContains(t, output,
		"verification: plan",
		"selected:",
		"skipped:",
		"  - web-test: subsumed by web",
	)
	if strings.Contains(output, "recommended_next:") {
		t.Fatalf("unexpected recommendation in output:\n%s", output)
	}
}
