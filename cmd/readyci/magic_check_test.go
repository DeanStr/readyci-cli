package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupInferredGoRepo(t *testing.T) string {
	t.Helper()
	return setupInferredGoRepoWithChange(t, true)
}

func setupCleanInferredGoRepo(t *testing.T) string {
	t.Helper()
	return setupInferredGoRepoWithChange(t, false)
}

func setupInferredGoRepoWithChange(t *testing.T, changed bool) string {
	t.Helper()
	t.Setenv("READYCI_API_URL", "")
	t.Setenv("READYCI_API_TOKEN", "")
	t.Setenv("READYCI_PROJECT_ID", "")
	repo := chdirCLITestRepo(t, "magic-go")
	runGitForCLITest(t, "init")
	runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/magic\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package magic\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if !changed {
		return repo
	}
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package magic\n\nfunc Changed() {}\n"), 0o644); err != nil {
		t.Fatalf("change main.go: %v", err)
	}
	return repo
}

func TestCheckInfersAffectedGoCheckWithoutConfig(t *testing.T) {
	setupInferredGoRepo(t)

	output, err := captureStdoutForCLITest(t, func() error {
		return checkCommand([]string{"--dry-run"})
	})
	if err != nil {
		t.Fatalf("check dry-run inferred config: %v\n%s", err, output)
	}
	requireCLITestOutputContains(t, output,
		"affected: dry-run",
		"config: <inferred .readyci.yml>",
		"check: go",
		"command: go test ./...",
		"reason: main.go matched **/*.go",
	)
}

func TestCheckInfersFromRepoRootWhenRunFromSubdirectory(t *testing.T) {
	repo := setupInferredGoRepo(t)
	subdir := filepath.Join(repo, "cmd", "tool")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	if err := os.Chdir(subdir); err != nil {
		t.Fatalf("chdir subdir: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return checkCommand([]string{"--dry-run"})
	})
	if err != nil {
		t.Fatalf("check dry-run inferred from subdir: %v\n%s", err, output)
	}
	requireCLITestOutputContains(t, output,
		"affected: dry-run",
		"config: <inferred .readyci.yml>",
		"project_id: magic-go",
		"check: go",
		"command: go test ./...",
	)
}

func TestCheckInfersStarterConfigWithoutDetectorSignals(t *testing.T) {
	t.Setenv("READYCI_API_URL", "")
	t.Setenv("READYCI_API_TOKEN", "")
	t.Setenv("READYCI_PROJECT_ID", "")
	repo := chdirCLITestRepo(t, "docs-only")
	if err := os.MkdirAll(filepath.Join(repo, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "docs", "guide.md"), []byte("# Guide\n"), 0o644); err != nil {
		t.Fatalf("write guide: %v", err)
	}
	runGitForCLITest(t, "init")
	runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")

	output, err := captureStdoutForCLITest(t, func() error {
		return checkCommand([]string{"--dry-run"})
	})
	if err != nil {
		t.Fatalf("check dry-run inferred starter config: %v\n%s", err, output)
	}
	requireCLITestOutputContains(t, output,
		"check: dry-run",
		"name: ci",
		"config: <inferred .readyci.yml>",
		"command: make check",
	)
}

func TestCheckFallsBackToDetectedCheckWhenInferredAffectedHasNoChanges(t *testing.T) {
	setupCleanInferredGoRepo(t)

	output, err := captureStdoutForCLITest(t, func() error {
		return checkCommand([]string{"--dry-run"})
	})
	if err != nil {
		t.Fatalf("check dry-run clean inferred config: %v\n%s", err, output)
	}
	requireCLITestOutputContains(t, output,
		"check: dry-run",
		"name: go",
		"config: <inferred .readyci.yml>",
		"command: go test ./...",
	)
}

func TestAgentPlanInfersAffectedGoCheckWithoutConfig(t *testing.T) {
	setupInferredGoRepo(t)

	output, err := captureStdoutForCLITest(t, func() error {
		return agentPlanCommand([]string{"--json"})
	})
	if err != nil {
		t.Fatalf("agent plan inferred config: %v\n%s", err, output)
	}
	var report agentPlanReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode agent plan: %v\n%s", err, output)
	}
	if report.Mode != "affected_rules" || report.Status != "ready" || report.Check != "go" || len(report.Checks) != 1 || report.Checks[0].Check != "go" {
		t.Fatalf("agent inferred plan = %+v, want selected go affected check", report)
	}
	if !strings.Contains(report.Reason, "Inferred checks from repository signals") {
		t.Fatalf("agent inferred reason = %q, want inference note", report.Reason)
	}
}

func TestStartReportsInferredPlanAsJSON(t *testing.T) {
	setupInferredGoRepo(t)

	output, err := captureStdoutForCLITest(t, func() error {
		return startCommand([]string{"--json"})
	})
	if err != nil {
		t.Fatalf("start --json inferred config: %v\n%s", err, output)
	}
	var report startReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode start report: %v\n%s", err, output)
	}
	if !report.Inferred || report.ConfigPath != inferredConfigPath || report.ProjectID != "magic-go" || report.Selected != "affected checks" {
		t.Fatalf("start report = %+v, want inferred affected plan for magic-go", report)
	}
	if !contains(report.Checks, "go") {
		t.Fatalf("start checks = %v, want go", report.Checks)
	}
	if len(report.Signals) == 0 {
		t.Fatalf("start signals empty, want detector explanation")
	}
}

func TestStartReportsFallbackCheckWhenInferredAffectedHasNoChanges(t *testing.T) {
	setupCleanInferredGoRepo(t)

	output, err := captureStdoutForCLITest(t, func() error {
		return startCommand([]string{"--json"})
	})
	if err != nil {
		t.Fatalf("start --json clean inferred config: %v\n%s", err, output)
	}
	var report startReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode start report: %v\n%s", err, output)
	}
	if !report.Inferred || report.Selected != "go" {
		t.Fatalf("start clean report = %+v, want inferred fallback go check", report)
	}
}

func TestStartReportsStarterConfigWithoutDetectorSignals(t *testing.T) {
	t.Setenv("READYCI_API_URL", "")
	t.Setenv("READYCI_API_TOKEN", "")
	t.Setenv("READYCI_PROJECT_ID", "")
	repo := chdirCLITestRepo(t, "docs-only-start")
	if err := os.MkdirAll(filepath.Join(repo, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "docs", "guide.md"), []byte("# Guide\n"), 0o644); err != nil {
		t.Fatalf("write guide: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return startCommand([]string{"--json"})
	})
	if err != nil {
		t.Fatalf("start --json inferred starter config: %v\n%s", err, output)
	}
	var report startReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode start report: %v\n%s", err, output)
	}
	if !report.Inferred || report.ConfigPath != inferredConfigPath || report.ProjectID != "docs-only-start" || report.Selected != "ci" {
		t.Fatalf("start report = %+v, want inferred starter plan for docs-only-start", report)
	}
	if !contains(report.Checks, "ci") {
		t.Fatalf("start checks = %v, want ci", report.Checks)
	}
	if len(report.Signals) != 1 || report.Signals[0] != "conservative local starter" {
		t.Fatalf("start signals = %v, want conservative local starter", report.Signals)
	}
}

func TestStartWriteConfigHonorsExplicitProject(t *testing.T) {
	setupInferredGoRepo(t)

	output, err := captureStdoutForCLITest(t, func() error {
		return startCommand([]string{"--json", "--write-config", "--project", "owner/repo"})
	})
	if err != nil {
		t.Fatalf("start --write-config --project: %v\n%s", err, output)
	}
	var report startReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode start report: %v\n%s", err, output)
	}
	if report.ProjectID != "owner/repo" || report.Written != ".readyci.yml" {
		t.Fatalf("start report = %+v, want explicit project written to .readyci.yml", report)
	}
	data, err := os.ReadFile(".readyci.yml")
	if err != nil {
		t.Fatalf("read written config: %v", err)
	}
	if !strings.Contains(string(data), `project_id: "owner/repo"`) {
		t.Fatalf("written config missing explicit project id:\n%s", data)
	}
}

func TestStartRejectsUnsafeProjectID(t *testing.T) {
	err := startCommand([]string{"--json", "--project", "owner\nrepo"})
	if err == nil || !strings.Contains(err.Error(), "project_id must not contain control characters") {
		t.Fatalf("start unsafe project err = %v, want project_id rejection", err)
	}
}

func TestStartWriteConfigPreservesFallbackSelectionForPreview(t *testing.T) {
	setupCleanInferredGoRepo(t)

	output, err := captureStdoutForCLITest(t, func() error {
		return startCommand([]string{"--write-config"})
	})
	if err != nil {
		t.Fatalf("start --write-config clean inferred config: %v\n%s", err, output)
	}
	requireCLITestOutputContains(t, output,
		"selected: go",
		"check: dry-run",
		"name: go",
		"command: go test ./...",
	)
	if strings.Contains(output, "affected: dry-run") {
		t.Fatalf("start preview used affected path after reporting fallback:\n%s", output)
	}
}

func TestStartWriteConfigFromSubdirectoryWritesRepoRootConfig(t *testing.T) {
	repo := setupInferredGoRepo(t)
	subdir := filepath.Join(repo, "cmd", "tool")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	if err := os.Chdir(subdir); err != nil {
		t.Fatalf("chdir subdir: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return startCommand([]string{"--json", "--write-config", "--project", "owner/repo"})
	})
	if err != nil {
		t.Fatalf("start --write-config from subdir: %v\n%s", err, output)
	}
	var report startReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode start report: %v\n%s", err, output)
	}
	if report.ProjectID != "owner/repo" || report.Written != ".readyci.yml" {
		t.Fatalf("start report = %+v, want root write for owner/repo", report)
	}
	if _, err := os.Stat(filepath.Join(repo, ".readyci.yml")); err != nil {
		t.Fatalf("root config was not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(subdir, ".readyci.yml")); !os.IsNotExist(err) {
		t.Fatalf("subdir config exists or stat failed unexpectedly: %v", err)
	}
}

func TestStartWriteConfigForceFromSubdirectoryOverwritesExistingRootConfigName(t *testing.T) {
	repo := setupInferredGoRepo(t)
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yaml"), []byte(`
project_id: old
checks:
  old: make old
`), 0o644); err != nil {
		t.Fatalf("write root yaml config: %v", err)
	}
	subdir := filepath.Join(repo, "cmd", "tool")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subdir, ".readyci.yml"), []byte(`
project_id: nested
checks:
  nested: make nested
`), 0o644); err != nil {
		t.Fatalf("write nested config: %v", err)
	}
	if err := os.Chdir(subdir); err != nil {
		t.Fatalf("chdir subdir: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return startCommand([]string{"--json", "--write-config", "--force", "--project", "owner/repo"})
	})
	if err != nil {
		t.Fatalf("start --write-config --force from subdir: %v\n%s", err, output)
	}
	var report startReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode start report: %v\n%s", err, output)
	}
	if report.Written != ".readyci.yaml" {
		t.Fatalf("written path = %q, want existing root .readyci.yaml", report.Written)
	}
	rootData, err := os.ReadFile(filepath.Join(repo, ".readyci.yaml"))
	if err != nil {
		t.Fatalf("read root yaml config: %v", err)
	}
	rootText := string(rootData)
	if !strings.Contains(rootText, `project_id: "owner/repo"`) || strings.Contains(rootText, "make old") {
		t.Fatalf("root config was not regenerated correctly:\n%s", rootText)
	}
	if _, err := os.Stat(filepath.Join(repo, ".readyci.yml")); !os.IsNotExist(err) {
		t.Fatalf("unexpected root .readyci.yml stat: %v", err)
	}
	nestedData, err := os.ReadFile(filepath.Join(subdir, ".readyci.yml"))
	if err != nil {
		t.Fatalf("read nested config: %v", err)
	}
	if !strings.Contains(string(nestedData), "make nested") {
		t.Fatalf("nested config was unexpectedly overwritten:\n%s", nestedData)
	}
}

func TestStartWriteConfigForceFromSubdirectoryPreviewsWrittenRootConfig(t *testing.T) {
	repo := setupInferredGoRepo(t)
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yaml"), []byte(`
project_id: old
checks:
  old: make old
`), 0o644); err != nil {
		t.Fatalf("write root yaml config: %v", err)
	}
	subdir := filepath.Join(repo, "cmd", "tool")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subdir, ".readyci.yml"), []byte(`
project_id: nested
checks:
  nested: make nested
`), 0o644); err != nil {
		t.Fatalf("write nested config: %v", err)
	}
	if err := os.Chdir(subdir); err != nil {
		t.Fatalf("chdir subdir: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return startCommand([]string{"--write-config", "--force", "--project", "owner/repo"})
	})
	if err != nil {
		t.Fatalf("start --write-config --force preview from subdir: %v\n%s", err, output)
	}
	requireCLITestOutputContains(t, output,
		"wrote: .readyci.yaml",
		"affected: dry-run",
		"project_id: owner/repo",
		"check: go",
		"command: go test ./...",
	)
	if strings.Contains(output, "make nested") || strings.Contains(output, "name: nested") {
		t.Fatalf("start preview used nested config instead of written root config:\n%s", output)
	}
}

func TestStartWriteConfigForceOverwritesExistingConfig(t *testing.T) {
	setupInferredGoRepo(t)
	if err := os.WriteFile(".readyci.yml", []byte(`
project_id: old
checks:
  old: make old
`), 0o644); err != nil {
		t.Fatalf("write old config: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return startCommand([]string{"--json", "--write-config", "--force", "--project", "owner/repo"})
	})
	if err != nil {
		t.Fatalf("start --write-config --force: %v\n%s", err, output)
	}
	var report startReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode start report: %v\n%s", err, output)
	}
	if report.ProjectID != "owner/repo" || report.Written != ".readyci.yml" || !report.Inferred {
		t.Fatalf("start force report = %+v, want inferred overwrite for owner/repo", report)
	}
	data, err := os.ReadFile(".readyci.yml")
	if err != nil {
		t.Fatalf("read overwritten config: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, `project_id: "owner/repo"`) || strings.Contains(text, "make old") {
		t.Fatalf("force-overwritten config =\n%s", text)
	}
}

func TestStartJSONReportsEffectiveProjectOverrideWithExistingConfig(t *testing.T) {
	setupInferredGoRepo(t)
	if err := os.WriteFile(".readyci.yml", []byte(`
project_id: configured
checks:
  go: go test ./...
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return startCommand([]string{"--json", "--project", "override"})
	})
	if err != nil {
		t.Fatalf("start --json --project with config: %v\n%s", err, output)
	}
	var report startReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode start report: %v\n%s", err, output)
	}
	if report.ProjectID != "override" || report.Inferred {
		t.Fatalf("start report = %+v, want explicit project override with existing config", report)
	}
}
