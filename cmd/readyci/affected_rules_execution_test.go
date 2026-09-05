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

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func TestAffectedCommandUsesConfiguredPathRules(t *testing.T) {
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
project_id: airline
affected:
  rules:
    - paths:
        - apps/web/src/**
      checks:
        - web-typecheck
        - web-test
    - paths: ["docs/**"]
      checks: docs-lint
checks:
  affected:
    command: make legacy-affected
  web-typecheck:
    command: make web-typecheck
    changes: staged
  web-test:
    command: make web-test
    matrix:
      NODE_VERSION: ["22", "24", "26"]
  docs-lint:
    command: make docs-lint
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.MkdirAll(filepath.Join(repo, "apps", "web", "src"), 0o755); err != nil {
		t.Fatalf("mkdir web src: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "apps", "web", "src", "app.tsx"), []byte("export {}\n"), 0o644); err != nil {
		t.Fatalf("write web file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "docs", "guide.md"), []byte("# Guide\n"), 0o644); err != nil {
		t.Fatalf("write docs file: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return affectedCommand([]string{"--dry-run", "--json"})
	})
	if err != nil {
		t.Fatalf("affected rules dry-run: %v", err)
	}
	var report affectedDryRunReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode affected rules report: %v\n%s", err, output)
	}
	got := make([]string, 0, len(report.SelectedChecks))
	reasons := make(map[string][]affectedRuleMatch)
	for _, check := range report.SelectedChecks {
		got = append(got, check.Name)
		reasons[check.Name] = check.Reasons
	}
	want := []string{"web-typecheck", "web-test", "docs-lint"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("selected checks = %v, want %v", got, want)
	}
	if !contains(report.ChangedFiles, "apps/web/src/app.tsx") || !contains(report.ChangedFiles, "docs/guide.md") {
		t.Fatalf("changed files = %v, want web and docs paths", report.ChangedFiles)
	}
	if len(reasons["web-typecheck"]) != 1 || reasons["web-typecheck"][0].Pattern != "apps/web/src/**" {
		t.Fatalf("web reasons = %+v", reasons["web-typecheck"])
	}
	if report.SelectedChecks[0].ChangeMode != syncbundle.ChangeModeWorking {
		t.Fatalf("selected check change mode = %q, want affected working mode", report.SelectedChecks[0].ChangeMode)
	}
	if len(reasons["docs-lint"]) != 1 || reasons["docs-lint"][0].Path != "docs/guide.md" {
		t.Fatalf("docs reasons = %+v", reasons["docs-lint"])
	}
	if report.Sync.CompressedBytes != 0 || report.Sync.ExpandedBytes != 0 || report.Sync.BaselineCount != 0 || report.Sync.WorkspaceCount != 0 {
		t.Fatalf("affected rules sync report built a bundle: %+v", report.Sync)
	}
}

func TestAffectedDryRunUsesProjectHomeRegionForDiagnostics(t *testing.T) {
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
	if err := os.MkdirAll(filepath.Join(repo, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src", "app.go"), []byte("package app\n"), 0o644); err != nil {
		t.Fatalf("write app: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
project_id: airline
affected:
  rules:
    - paths: ["src/**"]
      checks: ci
checks:
  ci:
    command: make ci
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, "src", "app.go"), []byte("package app\n\nconst Ready = true\n"), 0o644); err != nil {
		t.Fatalf("update app: %v", err)
	}

	settingsCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects/airline/settings":
			settingsCalled = true
			writeCLITestJSON(w, http.StatusOK, preflightapi.ProjectSettings{
				ProjectID:  "airline",
				HomeRegion: "us-east-1",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	output, err := captureStdoutForCLITest(t, func() error {
		return affectedCommand([]string{"--api-url", server.URL, "--token", "pfl_user", "--dry-run", "--json"})
	})
	if err != nil {
		t.Fatalf("affected dry-run: %v\n%s", err, output)
	}
	if !settingsCalled {
		t.Fatal("affected dry-run did not look up project settings")
	}
	var report affectedDryRunReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode affected report: %v\n%s", err, output)
	}
	if len(report.SelectedChecks) != 1 || report.SelectedChecks[0].Region != "us-east-1" {
		t.Fatalf("selected checks = %+v, want project home region", report.SelectedChecks)
	}
}

func TestAffectedWatchWhyKeepsDiagnosticContextAlive(t *testing.T) {
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
	if err := os.MkdirAll(filepath.Join(repo, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src", "app.go"), []byte("package app\n"), 0o644); err != nil {
		t.Fatalf("write app: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
project_id: airline
affected:
  rules:
    - paths: ["src/**"]
      checks: ci
checks:
  ci:
    command: make ci
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, "src", "app.go"), []byte("package app\n\nconst Ready = true\n"), 0o644); err != nil {
		t.Fatalf("update app: %v", err)
	}
	cfg, path, err := config.LoadProjectConfig(".")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	settingsCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects/airline/settings":
			settingsCalled = true
			writeCLITestJSON(w, http.StatusOK, preflightapi.ProjectSettings{
				ProjectID:  "airline",
				HomeRegion: "us-east-1",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	_, err = captureStdoutForCLITest(t, func() error {
		return executeAffectedRules(".", cfg, path, checkResolveOptions{}, checkExecutionOptions{
			APIURL:        server.URL,
			APIToken:      "pfl_user",
			Watch:         true,
			Why:           true,
			WatchInterval: 0,
		})
	})
	if err == nil || !strings.Contains(err.Error(), "--watch-interval must be positive") {
		t.Fatalf("affected watch --why error = %v, want watch interval validation", err)
	}
	if !settingsCalled {
		t.Fatal("affected watch --why did not look up project settings before canceling diagnostic context")
	}
}

func TestAffectedRulesDoNotOverrideExplicitCheck(t *testing.T) {
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
    - paths: ["**"]
      checks: web
checks:
  touched:
    command: make check-touched
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
		return affectedCommand([]string{"--check", "touched", "--dry-run", "--json"})
	})
	if err != nil {
		t.Fatalf("affected explicit dry-run: %v", err)
	}
	var report checkDryRunReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode explicit affected report: %v\n%s", err, output)
	}
	if report.Check != "touched" {
		t.Fatalf("selected check = %q, want explicit touched", report.Check)
	}
}

func TestAffectedRulesHonorCheckLevelSync(t *testing.T) {
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
sync:
  include_untracked: false
affected:
  rules:
    - paths: ["scratch/**"]
      checks:
        - scratch
        - strict
checks:
  scratch:
    command: make scratch
    sync:
      include_untracked: true
  strict:
    command: make strict
    sync:
      include_untracked: false
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("readyci\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.MkdirAll(filepath.Join(repo, "scratch"), 0o755); err != nil {
		t.Fatalf("mkdir scratch: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "scratch", "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatalf("write untracked scratch file: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return affectedCommand([]string{"--dry-run", "--json"})
	})
	if err != nil {
		t.Fatalf("affected rules dry-run: %v", err)
	}
	var report affectedDryRunReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode affected rules report: %v\n%s", err, output)
	}
	var got []string
	for _, check := range report.SelectedChecks {
		got = append(got, check.Name)
	}
	if strings.Join(got, ",") != "scratch" {
		t.Fatalf("selected checks = %v, want scratch only", got)
	}
	if !contains(report.ChangedFiles, "scratch/new.txt") {
		t.Fatalf("changed files = %v, want scratch/new.txt", report.ChangedFiles)
	}
	if !report.Sync.IncludeUntracked {
		t.Fatalf("sync include_untracked = false, want true because selected rule checks include untracked")
	}
}

func TestAffectedRulesIncludeUntrackedForInheritedWorkingChecks(t *testing.T) {
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
	configPath := filepath.Join(repo, ".readyci.yml")
	if err := os.WriteFile(configPath, []byte(`
sync:
  include_untracked: false
affected:
  rules:
    - paths:
        - .github/**
      checks:
        - ci
checks:
  ci:
    command: make ci
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("readyci\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.MkdirAll(filepath.Join(repo, ".github", "workflows"), 0o755); err != nil {
		t.Fatalf("mkdir workflows: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".github", "workflows", "new.yml"), []byte("name: New\n"), 0o644); err != nil {
		t.Fatalf("write untracked workflow: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return affectedCommand([]string{"--dry-run", "--json"})
	})
	if err != nil {
		t.Fatalf("affected rules dry-run: %v", err)
	}
	var report affectedDryRunReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode affected rules report: %v\n%s", err, output)
	}
	if len(report.SelectedChecks) != 1 || report.SelectedChecks[0].Name != "ci" {
		t.Fatalf("selected checks = %+v, want ci", report.SelectedChecks)
	}
	if !contains(report.ChangedFiles, ".github/workflows/new.yml") || !report.Sync.IncludeUntracked {
		t.Fatalf("changed/sync = %v/%t, want untracked workflow included", report.ChangedFiles, report.Sync.IncludeUntracked)
	}

	cfg, path, err := config.LoadProjectConfig(repo)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	cleanFingerprint, err := affectedWorkspaceFingerprint(context.Background(), repo, cfg, checkResolveOptions{})
	if err != nil {
		t.Fatalf("clean affected watch fingerprint: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".github", "workflows", "new.yml"), []byte("name: Updated\n"), 0o644); err != nil {
		t.Fatalf("update untracked workflow: %v", err)
	}
	changedFingerprint, err := affectedWorkspaceFingerprint(context.Background(), repo, cfg, checkResolveOptions{})
	if err != nil {
		t.Fatalf("changed affected watch fingerprint: %v", err)
	}
	if changedFingerprint == cleanFingerprint {
		t.Fatal("affected watch fingerprint did not change for untracked file included by affected working-check default")
	}
	plans, _, err := buildAffectedRulePlans(context.Background(), repo, cfg, path, checkResolveOptions{})
	if err != nil {
		t.Fatalf("build affected plans: %v", err)
	}
	if len(plans) != 1 || plans[0].Plan.Options.IncludeUntracked == nil || !*plans[0].Plan.Options.IncludeUntracked {
		t.Fatalf("plan include_untracked = %+v, want ci upload to include untracked", plans)
	}
}
