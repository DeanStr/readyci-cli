package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestAgentCommandRejectsRemovedAliases(t *testing.T) {
	for _, alias := range []string{"recommend-check", "skill"} {
		err := agentCommand([]string{alias})
		if err == nil || !strings.Contains(err.Error(), "unknown agent command") {
			t.Fatalf("agent alias %q error = %v, want unknown command", alias, err)
		}
	}
}

func TestAgentPlanCommandUsesRecommendedCheck(t *testing.T) {
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
	if err := os.WriteFile(".readyci.yml", []byte(`
project_id: proj_story
checks:
  touched:
    command: go test ./...
    changes: working
  full:
    command: make ci
    timeout: 30m
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return agentCommand([]string{"plan", "--json", "--goal", "verify changed files before PR"})
	})
	if err != nil {
		t.Fatalf("agent plan: %v", err)
	}
	var report agentPlanReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode agent plan: %v\n%s", err, output)
	}
	if report.Check != "touched" || report.ProjectID != "proj_story" || report.VerifyCommand != "readyci verify --json --progress=status --project proj_story --changes working --check touched" {
		t.Fatalf("agent plan = %+v, want touched check for proj_story", report)
	}
	if report.SchemaVersion != agentSchemaVersion || report.Intent != agentIntentEditLoop {
		t.Fatalf("agent plan contract = schema %q intent %q, want %s/%s", report.SchemaVersion, report.Intent, agentSchemaVersion, agentIntentEditLoop)
	}
	if report.Estimated == nil || report.Estimated.TimeoutSeconds == 0 {
		t.Fatalf("agent plan estimated cost = %+v, want populated timeout", report.Estimated)
	}

	output, err = captureStdoutForCLITest(t, func() error {
		return agentCommand([]string{"plan", "--json", "--api-url", "https://api.override.test", "--project", "proj_override", "--changes", "all", "--goal", "verify changed files before PR"})
	})
	if err != nil {
		t.Fatalf("agent plan with overrides: %v", err)
	}
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode overridden agent plan: %v\n%s", err, output)
	}
	wantVerify := "readyci verify --json --progress=status --api-url https://api.override.test --project proj_override --changes all --check touched"
	if report.ProjectID != "proj_override" || report.ChangeMode != "all" || report.VerifyCommand != wantVerify {
		t.Fatalf("overridden agent plan = %+v, want verify command %q", report, wantVerify)
	}
	if report.Policy == nil || report.Policy.NetworkMode != preflightapi.DefaultNetworkMode || len(report.Policy.TokenScopesRequired) == 0 || !strings.Contains(report.Policy.UploadPreviewCommand, "--check touched") {
		t.Fatalf("agent plan policy = %+v, want default network and sync preview", report.Policy)
	}
}

func TestAgentVerifyCheckRequestsNoSecrets(t *testing.T) {
	t.Setenv("READYCI_API_TOKEN", "")
	t.Setenv("READYCI_CONFIG_DIR", t.TempDir())

	repo := chdirCLITestRepo(t, "agent-verify-check-no-secrets")
	runGitForCLITest(t, "init")
	runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
project_id: proj_story
checks:
  touched:
    command: go test ./...
    changes: working
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("readyci\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")

	server, created := newWarmRunCLITestServer(t, "run_agent_check_")
	output, err := captureStdoutForCLITest(t, func() error {
		return agentVerifyCommand([]string{
			"--api-url", server.URL,
			"--token", "pfl_agent_check",
			"--check", "touched",
			"--idempotency-key", "agent-check",
			"--json",
		})
	})
	if err != nil {
		t.Fatalf("agent verify check: %v\n%s", err, output)
	}
	var report agentVerifyReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode agent verify check report: %v\n%s", err, output)
	}
	if report.Policy == nil || report.Policy.SecretsRequested || report.Policy.SecretsAuthorized {
		t.Fatalf("agent verify check policy = %+v, want no-secret policy", report.Policy)
	}
	createdRequests := *created
	if len(createdRequests) != 1 {
		t.Fatalf("created requests = %d, want one check run: %+v", len(createdRequests), createdRequests)
	}
	requireExplicitNoSecretCreateRequests(t, createdRequests)
}

func TestAgentVerifyCheckCanRequestSecretsExplicitly(t *testing.T) {
	t.Setenv("READYCI_API_TOKEN", "")
	t.Setenv("READYCI_CONFIG_DIR", t.TempDir())

	repo := chdirCLITestRepo(t, "agent-verify-check-request-secrets")
	runGitForCLITest(t, "init")
	runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
project_id: proj_story
checks:
  touched:
    command: go test ./...
    changes: working
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("readyci\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")

	server, created := newWarmRunCLITestServer(t, "run_agent_secret_check_")
	output, err := captureStdoutForCLITest(t, func() error {
		return agentVerifyCommand([]string{
			"--api-url", server.URL,
			"--token", "pfl_agent_check",
			"--check", "touched",
			"--idempotency-key", "agent-check-secrets",
			"--request-secrets",
			"--json",
		})
	})
	if err != nil {
		t.Fatalf("agent verify check with secrets: %v\n%s", err, output)
	}
	var report agentVerifyReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode agent verify check report: %v\n%s", err, output)
	}
	if report.Policy == nil || !report.Policy.SecretsRequested || !containsString(report.Policy.TokenScopesRequired, "runs:create_with_secrets") {
		t.Fatalf("agent verify secret policy = %+v, want requested secrets and scope", report.Policy)
	}
	createdRequests := *created
	if len(createdRequests) != 1 {
		t.Fatalf("created requests = %d, want one check run: %+v", len(createdRequests), createdRequests)
	}
	if createdRequests[0].SecretsRequested == nil || !*createdRequests[0].SecretsRequested {
		t.Fatalf("created request secrets_requested = %+v, want explicit true", createdRequests[0].SecretsRequested)
	}
}

func TestAgentPlanRequestSecretsAddsPolicyAndVerifyFlag(t *testing.T) {
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
	if err := os.WriteFile(".readyci.yml", []byte(`
project_id: proj_story
checks:
  touched:
    command: go test ./...
    changes: working
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return agentPlanCommand([]string{"--json", "--check", "touched", "--request-secrets"})
	})
	if err != nil {
		t.Fatalf("agent plan with request-secrets: %v", err)
	}
	var report agentPlanReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode agent plan: %v\n%s", err, output)
	}
	if report.Policy == nil || !report.Policy.SecretsRequested || !containsString(report.Policy.TokenScopesRequired, "runs:create_with_secrets") {
		t.Fatalf("agent plan policy = %+v, want requested secrets", report.Policy)
	}
	if !strings.Contains(report.VerifyCommand, "--request-secrets") {
		t.Fatalf("verify command = %q, want --request-secrets", report.VerifyCommand)
	}
}

func TestAgentRequestSecretsRejectedForSecuritySensitiveIntent(t *testing.T) {
	if err := validateAgentSecretRequestForIntent(agentIntentSecurity, true); err == nil {
		t.Fatal("security-sensitive intent accepted --request-secrets")
	}
}

func TestAgentPlanUsesAffectedRulesByDefault(t *testing.T) {
	t.Setenv("READYCI_API_URL", "")
	t.Setenv("READYCI_PROJECT_ID", "")
	t.Setenv("READYCI_API_TOKEN", "")

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
		return agentPlanCommand([]string{"--json"})
	})
	if err != nil {
		t.Fatalf("agent affected plan: %v", err)
	}
	var report agentPlanReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode affected plan: %v\n%s", err, output)
	}
	if report.Mode != "affected_rules" || report.Status != "ready" || len(report.Checks) != 1 || report.Checks[0].Check != "go" {
		t.Fatalf("affected plan = %+v, want selected go check", report)
	}
	if report.VerifyCommand != "readyci verify --json --progress=status --project proj_story --changes working" {
		t.Fatalf("affected verify command = %q", report.VerifyCommand)
	}
}

func TestAgentPlanPreCommitIntentUsesStagedAffectedRules(t *testing.T) {
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
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "platform", "api.go"), []byte("package platform\n"), 0o644); err != nil {
		t.Fatalf("write platform file: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, "platform", "api.go"), []byte("package platform\n// staged\n"), 0o644); err != nil {
		t.Fatalf("change platform file: %v", err)
	}
	runGitForCLITest(t, "add", filepath.Join("platform", "api.go"))

	output, err := captureStdoutForCLITest(t, func() error {
		return agentPlanCommand([]string{"--json", "--intent", "pre-commit"})
	})
	if err != nil {
		t.Fatalf("agent pre-commit plan: %v\n%s", err, output)
	}
	var report agentPlanReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode pre-commit plan: %v\n%s", err, output)
	}
	wantVerify := "readyci verify --json --progress=status --project proj_story --intent pre-commit --changes staged"
	if report.SchemaVersion != agentSchemaVersion || report.Intent != agentIntentPreCommit || report.ChangeMode != "staged" || report.VerifyCommand != wantVerify {
		t.Fatalf("pre-commit plan = %+v, want staged intent plan with verify %q", report, wantVerify)
	}
	if report.Policy == nil || report.Policy.NetworkMode != preflightapi.DefaultNetworkMode || report.Policy.UploadPreviewCommand != "readyci sync --dry-run --json --changes staged --check go" {
		t.Fatalf("pre-commit policy = %+v, want staged sync preview", report.Policy)
	}
}

func TestAgentPlanPrePRIntentAddsSupplementalBrowserChecks(t *testing.T) {
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
  web-e2e:
    command: pnpm web:test:e2e
  ci:
    command: make ci
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
		return agentPlanCommand([]string{"--json", "--intent", "pre-pr"})
	})
	if err != nil {
		t.Fatalf("agent pre-pr plan: %v\n%s", err, output)
	}
	var report agentPlanReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode pre-pr plan: %v\n%s", err, output)
	}
	checks := agentPlanCheckNames(report.Checks)
	if !contains(checks, "web") || contains(checks, "web-e2e") {
		t.Fatalf("pre-pr selected checks = %v, want only affected-rule web check", checks)
	}
	if !strings.Contains(report.VerifyCommand, "--intent pre-pr") {
		t.Fatalf("pre-pr verify command = %q, want intent flag", report.VerifyCommand)
	}
}

func TestAgentPlanAffectedRulesUseCheckWhenChanges(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
project_id: proj_story
checks:
  go:
    command: go test ./...
    when:
      changes:
        include:
          - "**/*.go"
        exclude:
          - "vendor/**"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "vendor"), 0o755); err != nil {
		t.Fatalf("mkdir vendor: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "vendor", "skip.go"), []byte("package vendor\n"), 0o644); err != nil {
		t.Fatalf("write vendor source: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("change source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "vendor", "skip.go"), []byte("package vendor\n\nfunc Skip() {}\n"), 0o644); err != nil {
		t.Fatalf("change vendor source: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return agentPlanCommand([]string{"--json"})
	})
	if err != nil {
		t.Fatalf("agent plan: %v\n%s", err, output)
	}
	var report agentPlanReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode agent plan: %v\n%s", err, output)
	}
	if report.Status != "ready" || report.Reason != "matched_changed_paths" {
		t.Fatalf("agent plan status=%q reason=%q, want ready matched_changed_paths", report.Status, report.Reason)
	}
	if len(report.Checks) != 1 || report.Checks[0].Check != "go" || !report.Checks[0].Selected {
		t.Fatalf("agent checks = %+v, want selected go", report.Checks)
	}
	if got := strings.Join(report.Checks[0].MatchedPaths, ","); got != "main.go" {
		t.Fatalf("matched paths = %q, want main.go only", got)
	}
}
