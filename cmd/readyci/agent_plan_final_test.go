package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestAgentPlanFinalGoalUsesRecommendedFullCheckBeforeAffectedRules(t *testing.T) {
	t.Setenv("READYCI_API_URL", "")
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
	runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	if err := os.MkdirAll(filepath.Join(repo, "platform"), 0o755); err != nil {
		t.Fatalf("mkdir platform: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
project_id: proj_story
affected:
  rules:
    - paths: ["platform/**"]
      checks: ["go"]
checks:
  go:
    command: go test ./...
  full:
    command: make ci
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "platform", "api.go"), []byte("package platform\n"), 0o644); err != nil {
		t.Fatalf("write platform file: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, "platform", "api.go"), []byte("package platform\n// changed\n"), 0o644); err != nil {
		t.Fatalf("change platform file: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return agentPlanCommand([]string{"--json", "--goal", "final handoff"})
	})
	if err != nil {
		t.Fatalf("agent final plan: %v\n%s", err, output)
	}
	var report agentPlanReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode final plan: %v\n%s", err, output)
	}
	if report.Mode != "check" || report.Check != "full" || report.VerifyCommand != "readyci verify --json --progress=status --project proj_story --changes working --check full" {
		t.Fatalf("final plan = %+v, want full check plan", report)
	}
	if report.Plan == nil || report.Plan.Goal != "final handoff" || len(report.Plan.Selected) != 1 || report.Plan.Selected[0].Name != "full" {
		t.Fatalf("final plan planner = %+v, want selected full with goal", report.Plan)
	}
}

func TestAgentPlanFinalHandoffIntentUsesBroadCheckBeforeAffectedAlias(t *testing.T) {
	t.Setenv("READYCI_API_URL", "")
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
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
project_id: proj_story
checks:
  affected:
    command: make affected
  touched:
    command: make touched
  ci:
    command: make ci
  full:
    command: make full
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return agentPlanCommand([]string{"--json", "--intent", "final-handoff"})
	})
	if err != nil {
		t.Fatalf("agent final-handoff plan: %v\n%s", err, output)
	}
	var report agentPlanReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode final-handoff plan: %v\n%s", err, output)
	}
	wantVerify := "readyci verify --json --progress=status --project proj_story --intent final-handoff --changes working --check full"
	if report.Intent != agentIntentFinalHandoff || report.Mode != "check" || report.Check != "full" || report.VerifyCommand != wantVerify {
		t.Fatalf("final-handoff plan = %+v, want broad full check with verify %q", report, wantVerify)
	}
}

func TestAgentPlanAffectedRulesIncludesPlannerAndProfileWarnings(t *testing.T) {
	t.Setenv("READYCI_API_URL", "")
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
	runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	if err := os.MkdirAll(filepath.Join(repo, "scripts"), 0o755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
project_id: proj_story
affected:
  profiles:
    - docs
    - shell
checks:
  shell-syntax:
    command: bash -n scripts/check.sh
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "scripts", "check.sh"), []byte("#!/usr/bin/env bash\n"), 0o755); err != nil {
		t.Fatalf("write shell script: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, "scripts", "check.sh"), []byte("#!/usr/bin/env bash\necho changed\n"), 0o755); err != nil {
		t.Fatalf("change shell script: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return agentPlanCommand([]string{"--json", "--goal", "verify changed files"})
	})
	if err != nil {
		t.Fatalf("agent affected plan with warning: %v\n%s", err, output)
	}
	var report agentPlanReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode affected plan with warning: %v\n%s", err, output)
	}
	if report.Mode != "affected_rules" || report.Plan == nil || len(report.Plan.Selected) != 1 || report.Plan.Selected[0].Name != "shell-syntax" {
		t.Fatalf("affected planner = %+v report=%+v, want selected shell-syntax", report.Plan, report)
	}
	if len(report.Warnings) != 1 || report.Warnings[0].Section != "affected.profiles" || !strings.Contains(report.Warnings[0].Message, `affected profile "docs" did not match`) {
		t.Fatalf("warnings = %+v, want structured docs profile warning", report.Warnings)
	}
}

func TestAgentVerifyAffectedRulesSkippedSuccessDoesNotRequireToken(t *testing.T) {
	t.Setenv("READYCI_API_TOKEN", "")
	t.Setenv("READYCI_CONFIG_DIR", t.TempDir())

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
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("readyci\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
affected:
  rules:
    - paths: ["apps/web/**"]
      checks: ["web"]
checks:
  web:
    command: make web
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("updated\n"), 0o644); err != nil {
		t.Fatalf("update readme: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return agentVerifyCommand([]string{"--json"})
	})
	if err != nil {
		t.Fatalf("agent verify no-op affected rules: %v\n%s", err, output)
	}
	var report agentVerifyReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode no-op affected verify: %v\n%s", err, output)
	}
	if report.Status != "skipped" || report.Mode != "affected_rules" || report.Reason != "no_affected_rules_matched" || report.Classification != preflightapi.ClassificationSuccess || report.AgentAction != preflightapi.AgentActionNone || report.CLIExitCode != cliExitSuccess {
		t.Fatalf("no-op affected verify = %+v, want skipped success", report)
	}
	if report.SchemaVersion != agentSchemaVersion || report.Intent != agentIntentEditLoop || report.Policy == nil || len(report.Policy.TokenScopesRequired) != 0 {
		t.Fatalf("no-op affected contract = schema %q intent %q policy %+v, want versioned no-token skip", report.SchemaVersion, report.Intent, report.Policy)
	}
	if len(report.Checks) != 0 || len(report.Runs) != 0 {
		t.Fatalf("no-op affected checks/runs = %+v/%+v, want empty arrays", report.Checks, report.Runs)
	}
}

func TestAgentVerifyAffectedRulesSkippedIncludesProfileWarnings(t *testing.T) {
	t.Setenv("READYCI_API_TOKEN", "")
	t.Setenv("READYCI_CONFIG_DIR", t.TempDir())

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
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
affected:
  profiles:
    - docs
    - shell
checks:
  shell-syntax:
    command: bash -n scripts/check.sh
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("readyci\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("updated\n"), 0o644); err != nil {
		t.Fatalf("update readme: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return agentVerifyCommand([]string{"--json"})
	})
	if err != nil {
		t.Fatalf("agent verify skipped with warning: %v\n%s", err, output)
	}
	var report agentVerifyReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode skipped warning report: %v\n%s", err, output)
	}
	if report.Status != "skipped" || report.Mode != "affected_rules" || report.Classification != preflightapi.ClassificationSuccess {
		t.Fatalf("skipped warning report = %+v, want successful affected skip", report)
	}
	if len(report.Warnings) != 1 || report.Warnings[0].Section != "affected.profiles" || !strings.Contains(report.Warnings[0].Message, `affected profile "docs" did not match`) {
		t.Fatalf("warnings = %+v, want structured docs profile warning", report.Warnings)
	}
}

func TestAgentVerifyPrePRSkippedIncludesSupplementalWhenDiagnostics(t *testing.T) {
	t.Setenv("READYCI_API_TOKEN", "")
	t.Setenv("READYCI_CONFIG_DIR", t.TempDir())
	t.Setenv("READYCI_WEB_ENABLED", "")
	t.Setenv("READYCI_BROWSER_E2E_ENABLED", "")

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
	if err := os.MkdirAll(filepath.Join(repo, "web", "src", "pages"), 0o755); err != nil {
		t.Fatalf("mkdir web: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
project_id: proj_story
affected:
  rules:
    - paths: ["web/src/**"]
      checks: ["web"]
checks:
  web:
    command: pnpm web:check
    when:
      env:
        READYCI_WEB_ENABLED: "yes"
  web-e2e:
    command: pnpm web:test:e2e
    when:
      env:
        READYCI_BROWSER_E2E_ENABLED: "yes"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "web", "src", "pages", "Login.tsx"), []byte("export function Login() { return null }\n"), 0o644); err != nil {
		t.Fatalf("write page: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, "web", "src", "pages", "Login.tsx"), []byte("export function Login() { return <main /> }\n"), 0o644); err != nil {
		t.Fatalf("change page: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return agentVerifyCommand([]string{"--json", "--intent", "pre-pr"})
	})
	if err != nil {
		t.Fatalf("agent verify skipped pre-pr: %v\n%s", err, output)
	}
	var report agentVerifyReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode skipped pre-pr verify: %v\n%s", err, output)
	}
	if report.Status != "skipped" || report.Mode != "affected_rules" || report.Classification != preflightapi.ClassificationSuccess {
		t.Fatalf("skipped pre-pr verify = %+v, want successful affected skip", report)
	}
	var sawWeb bool
	for _, check := range report.Checks {
		switch check.Check {
		case "web":
			if !check.Selected && contains(check.Reasons, "skipped because when.env.READYCI_WEB_ENABLED") {
				sawWeb = true
			}
		case "web-e2e":
			t.Fatalf("checks = %+v, local pre-pr planner should not add supplemental web-e2e diagnostics", report.Checks)
		}
	}
	if !sawWeb {
		t.Fatalf("checks = %+v, want web skipped diagnostics", report.Checks)
	}
	if len(report.Runs) != 0 {
		t.Fatalf("runs = %+v, want no remote runs for skipped verify", report.Runs)
	}
}

func TestAgentVerifyFinalGoalDoesNotReturnAffectedSkipWithoutToken(t *testing.T) {
	t.Setenv("READYCI_API_TOKEN", "")
	t.Setenv("READYCI_CONFIG_DIR", t.TempDir())

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
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
affected:
  rules:
    - paths: ["apps/web/**"]
      checks: ["web"]
checks:
  web:
    command: make web
  full:
    command: make ci
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("readyci\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("updated\n"), 0o644); err != nil {
		t.Fatalf("update readme: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return agentVerifyCommand([]string{"--json", "--goal", "final handoff"})
	})
	if err == nil {
		t.Fatalf("agent final verify returned nil error, want missing token error\n%s", output)
	}
	var report agentVerifyReport
	if decodeErr := json.Unmarshal([]byte(output), &report); decodeErr != nil {
		t.Fatalf("decode final verify auth report: %v\n%s", decodeErr, output)
	}
	if report.Status == "skipped" || report.Mode == "affected_rules" || report.Classification != preflightapi.ClassificationAuthMissing {
		t.Fatalf("final verify report = %+v, want auth_missing check path instead of affected skip", report)
	}
}

func TestAgentVerifyAffectedRulesRunsSelectedChecks(t *testing.T) {
	t.Setenv("READYCI_API_TOKEN", "")
	t.Setenv("READYCI_CONFIG_DIR", t.TempDir())

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
	for _, dir := range []string{"platform", "web"} {
		if err := os.MkdirAll(filepath.Join(repo, dir), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
project_id: proj_story
affected:
  rules:
    - paths: ["platform/**"]
      checks: ["go"]
    - paths: ["web/**"]
      checks: ["web"]
checks:
  go:
    command: go test ./...
  web:
    command: pnpm test
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "platform", "api.go"), []byte("package platform\n"), 0o644); err != nil {
		t.Fatalf("write platform file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "web", "app.ts"), []byte("export {}\n"), 0o644); err != nil {
		t.Fatalf("write web file: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, "platform", "api.go"), []byte("package platform\n// changed\n"), 0o644); err != nil {
		t.Fatalf("change platform file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "web", "app.ts"), []byte("export const changed = true\n"), 0o644); err != nil {
		t.Fatalf("change web file: %v", err)
	}

	var serverURL string
	var createRequests []preflightapi.CreateRunRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs":
			var req preflightapi.CreateRunRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode create run: %v", err)
			}
			createRequests = append(createRequests, req)
			runID := fmt.Sprintf("run_%d", len(createRequests))
			writeCLITestJSON(w, http.StatusCreated, preflightapi.CreateRunResponse{
				RunID:     runID,
				UploadURL: serverURL + "/upload/" + runID,
				EventsURL: serverURL + "/events/" + runID,
				Status:    preflightapi.StatusAwaitingUpload,
			})
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/upload/"):
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte("{}\n"))
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/runs/run_") && strings.HasSuffix(r.URL.Path, "/upload-complete"):
			runID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/runs/"), "/upload-complete")
			writeCLITestJSON(w, http.StatusOK, preflightapi.CompleteUploadResponse{RunID: runID, Status: preflightapi.StatusQueued})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/events/run_"):
			exitCode := 0
			event := preflightapi.RunEvent{Type: preflightapi.EventRunFinished, Status: preflightapi.StatusSucceeded, ExitCode: &exitCode}
			data, _ := json.Marshal(event)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write(append(append([]byte("data: "), data...), '\n', '\n'))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	output, err := captureStdoutForCLITest(t, func() error {
		return agentVerifyCommand([]string{"--api-url", server.URL, "--token", "pfl_story", "--json", "--idempotency-key", "task-affected"})
	})
	if err != nil {
		t.Fatalf("agent verify affected rules: %v\n%s", err, output)
	}
	var report agentVerifyReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode affected verify: %v\n%s", err, output)
	}
	if report.Mode != "affected_rules" || report.Status != preflightapi.StatusSucceeded || report.Classification != preflightapi.ClassificationSuccess || report.CLIExitCode != cliExitSuccess {
		t.Fatalf("affected verify report = %+v, want succeeded affected_rules", report)
	}
	if len(report.Checks) != 2 || len(report.Runs) != 2 || len(createRequests) != 2 {
		t.Fatalf("affected verify counts checks=%d runs=%d creates=%d, want 2 each", len(report.Checks), len(report.Runs), len(createRequests))
	}
	requireExplicitNoSecretCreateRequests(t, createRequests)
	gotKeys := []string{createRequests[0].IdempotencyKey, createRequests[1].IdempotencyKey}
	wantKeys := []string{"task-affected:check=go", "task-affected:check=web"}
	if strings.Join(gotKeys, ",") != strings.Join(wantKeys, ",") {
		t.Fatalf("affected child idempotency keys = %v, want %v", gotKeys, wantKeys)
	}

	createRequests = nil
	output, err = captureStdoutForCLITest(t, func() error {
		return agentVerifyCommand([]string{"--api-url", server.URL, "--token", "pfl_story", "--json", "--idempotency-key", "task-affected-secrets", "--request-secrets"})
	})
	if err != nil {
		t.Fatalf("agent verify affected rules with secrets: %v\n%s", err, output)
	}
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode affected verify with secrets: %v\n%s", err, output)
	}
	if report.Policy == nil || !report.Policy.SecretsRequested || !containsString(report.Policy.TokenScopesRequired, "runs:create_with_secrets") {
		t.Fatalf("affected verify secret policy = %+v, want requested secrets and scope", report.Policy)
	}
	if len(createRequests) != 2 {
		t.Fatalf("secret create requests = %d, want 2: %+v", len(createRequests), createRequests)
	}
	for _, req := range createRequests {
		if req.SecretsRequested == nil || !*req.SecretsRequested {
			t.Fatalf("affected secret request %s secrets_requested = %+v, want explicit true", req.CommandAlias, req.SecretsRequested)
		}
	}
}
