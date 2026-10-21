package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

func TestAffectedPathsFromManifestExpandsFromRepositoryRoot(t *testing.T) {
	repo := chdirCLITestRepo(t, "paths-from")
	runGitForCLITest(t, "init")
	runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	if err := os.MkdirAll(filepath.Join(repo, "scripts"), 0o755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "scripts", "ignored-test-targets.txt"), []byte("alpha\n# ignored\nbeta\n\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
affected:
  rules:
    - paths_from:
        file: scripts/ignored-test-targets.txt
        template: tests/${line}.rs
      checks: [rust]
  tests:
    - path: tests/alpha.rs
      checks: [rust]
checks:
  rust:
    command: cargo test --workspace
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	subdir := filepath.Join(repo, "nested")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	if err := os.Chdir(subdir); err != nil {
		t.Fatalf("chdir subdir: %v", err)
	}

	cfg, _, err := config.LoadProjectConfig(subdir)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	expanded, warnings, err := expandAffectedConfig(cfg)
	if err != nil {
		t.Fatalf("expand affected config: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	if len(expanded.Affected.Rules) != 1 {
		t.Fatalf("rules = %d, want 1", len(expanded.Affected.Rules))
	}
	paths := expanded.Affected.Rules[0].Paths
	if !sameStringSet(paths, []string{"tests/alpha.rs", "tests/beta.rs"}) {
		t.Fatalf("paths_from expanded to %v", paths)
	}
	if source := expanded.Affected.Rules[0].PathSources["tests/alpha.rs"]; source.File != "scripts/ignored-test-targets.txt" || source.Line != 1 {
		t.Fatalf("alpha path source = %+v, want manifest line 1", source)
	}
	if err := validateAffectedRulesConfig(expanded); err != nil {
		t.Fatalf("validate expanded affected rules: %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir root: %v", err)
	}
	explainOutput, err := captureStdoutForCLITest(t, func() error {
		return affectedExplainCommand([]string{"--json", "tests/alpha.rs"})
	})
	if err != nil {
		t.Fatalf("affected explain: %v\n%s", err, explainOutput)
	}
	var explain affectedExplainReport
	if err := json.Unmarshal([]byte(explainOutput), &explain); err != nil {
		t.Fatalf("decode explain: %v\n%s", err, explainOutput)
	}
	if len(explain.Paths) != 1 || len(explain.Paths[0].Reasons) != 1 {
		t.Fatalf("explain paths = %+v, want one sourced reason", explain.Paths)
	}
	reason := explain.Paths[0].Reasons[0]
	if reason.SourceFile != "scripts/ignored-test-targets.txt" || reason.SourceLine != 1 || reason.SourceTemplate != "tests/${line}.rs" {
		t.Fatalf("explain source = %+v, want manifest provenance", reason)
	}
	auditOutput, err := captureStdoutForCLITest(t, func() error {
		return affectedAuditCommand([]string{"--json"})
	})
	if err != nil {
		t.Fatalf("affected audit: %v\n%s", err, auditOutput)
	}
	var audit affectedAuditReport
	if err := json.Unmarshal([]byte(auditOutput), &audit); err != nil {
		t.Fatalf("decode audit: %v\n%s", err, auditOutput)
	}
	if len(audit.PathSources) != 2 || audit.PathSources[0].SourceFile != "scripts/ignored-test-targets.txt" {
		t.Fatalf("audit path sources = %+v, want manifest provenance", audit.PathSources)
	}
}

func TestAffectedPathsFromResolvesFromConfigDirectoryOutsideGit(t *testing.T) {
	t.Setenv("READYCI_API_URL", "")
	t.Setenv("READYCI_PROJECT_ID", "")

	cliTestWorkingDirectoryMu.Lock()
	defer cliTestWorkingDirectoryMu.Unlock()
	repo := t.TempDir()
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore wd: %v", err)
		}
	}()
	if err := os.MkdirAll(filepath.Join(repo, "scripts"), 0o755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "scripts", "targets.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
affected:
  rules:
    - paths_from:
        file: scripts/targets.txt
        template: tests/${line}.rs
      checks: [fixtures]
checks:
  fixtures:
    command: echo fixtures
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	subdir := filepath.Join(repo, "nested", "work")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	if err := os.Chdir(subdir); err != nil {
		t.Fatalf("chdir subdir: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return configValidateCommand(nil)
	})
	if err != nil {
		t.Fatalf("config validate from subdir: %v\n%s", err, output)
	}
	requireCLITestOutputContains(t, output, "status: ok")
}

func TestDoctorUsesConfigDirectoryForPathsFromValidationOutsideGit(t *testing.T) {
	t.Setenv("READYCI_API_URL", "")
	t.Setenv("READYCI_PROJECT_ID", "")

	cliTestWorkingDirectoryMu.Lock()
	defer cliTestWorkingDirectoryMu.Unlock()
	repo := t.TempDir()
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore wd: %v", err)
		}
	}()
	if err := os.MkdirAll(filepath.Join(repo, "scripts"), 0o755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "scripts", "targets.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
affected:
  rules:
    - paths_from:
        file: scripts/targets.txt
        template: tests/${line}.rs
      checks: [fixtures]
checks:
  fixtures:
    command: echo fixtures
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	subdir := filepath.Join(repo, "nested", "work")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	if err := os.Chdir(subdir); err != nil {
		t.Fatalf("chdir subdir: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return doctorCommand([]string{"--repo", "--json"})
	})
	if err != nil {
		t.Fatalf("doctor repo from subdir: %v\n%s", err, output)
	}
	if strings.Contains(output, "scripts/targets.txt: no such file") {
		t.Fatalf("doctor resolved paths_from from nested cwd: %s", output)
	}
}

func TestAffectedPathsFromRejectsUnknownKeys(t *testing.T) {
	repo := chdirCLITestRepo(t, "paths-from-unknown-key")
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
affected:
  rules:
    - paths_from:
        file: scripts/ignored-test-targets.txt
        temlate: tests/${line}.rs
      checks: [rust]
checks:
  rust:
    command: cargo test --workspace
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	_, _, err := config.LoadProjectConfig(repo)
	if err == nil || !strings.Contains(err.Error(), "temlate") {
		t.Fatalf("load config error = %v, want unknown paths_from key", err)
	}
}

func TestAffectedPathsFromRejectsAbsoluteExpandedPaths(t *testing.T) {
	repo := chdirCLITestRepo(t, "paths-from-absolute-entry")
	if err := os.MkdirAll(filepath.Join(repo, "scripts"), 0o755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "scripts", "targets.txt"), []byte("/tmp/repo/tests/foo.rs\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
affected:
  rules:
    - paths_from:
        file: scripts/targets.txt
      checks: [fixtures]
checks:
  fixtures:
    command: echo fixtures
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return configValidateCommand(nil)
	})
	if err == nil || !strings.Contains(err.Error(), "must be repository-relative") {
		t.Fatalf("config validate error = %v output=%s, want absolute expanded path rejection", err, output)
	}
}

func TestAffectedPathsFromRejectsTraversalExpandedPaths(t *testing.T) {
	repo := chdirCLITestRepo(t, "paths-from-traversal-entry")
	if err := os.MkdirAll(filepath.Join(repo, "scripts"), 0o755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "scripts", "targets.txt"), []byte("tests/../src/foo.rs\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
affected:
  rules:
    - paths_from:
        file: scripts/targets.txt
      checks: [fixtures]
checks:
  fixtures:
    command: echo fixtures
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return configValidateCommand(nil)
	})
	if err == nil || !strings.Contains(err.Error(), "must not escape the repository") {
		t.Fatalf("config validate error = %v output=%s, want traversal expanded path rejection", err, output)
	}
}

func TestAffectedPathsFromRejectsSymlinkManifest(t *testing.T) {
	repo := chdirCLITestRepo(t, "paths-from-symlink")
	outside := filepath.Join(t.TempDir(), "outside-targets.txt")
	if err := os.WriteFile(outside, []byte("secret-local-path\n"), 0o644); err != nil {
		t.Fatalf("write outside manifest: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "scripts"), 0o755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, "scripts", "targets.txt")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
affected:
  rules:
    - paths_from:
        file: scripts/targets.txt
        template: tests/${line}.rs
      checks: [fixtures]
checks:
  fixtures:
    command: echo fixtures
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return configValidateCommand(nil)
	})
	if err == nil || !strings.Contains(err.Error(), "must not contain symlinks") {
		t.Fatalf("config validate error = %v output=%s, want symlink rejection", err, output)
	}
	if strings.Contains(output, "secret-local-path") || strings.Contains(err.Error(), "secret-local-path") {
		t.Fatalf("paths_from leaked symlink target contents: err=%v output=%s", err, output)
	}
}

func TestAffectedPathsFromRejectsGitControlManifest(t *testing.T) {
	repo := chdirCLITestRepo(t, "paths-from-git-control")
	runGitForCLITest(t, "init")
	secretURL := "https://token@example.invalid/repo.git"
	runGitForCLITest(t, "config", "remote.origin.url", secretURL)
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
affected:
  rules:
    - paths_from:
        file: .git/config
      checks: [fixtures]
checks:
  fixtures:
    command: echo fixtures
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return configValidateCommand(nil)
	})
	if err == nil || !strings.Contains(err.Error(), "must not target git control metadata") {
		t.Fatalf("config validate error = %v output=%s, want git control rejection", err, output)
	}
	if strings.Contains(output, secretURL) || strings.Contains(err.Error(), secretURL) {
		t.Fatalf("paths_from leaked git config contents: err=%v output=%s", err, output)
	}
}

func TestBrowserAndReviewableOutputsResolveIntoRunPlan(t *testing.T) {
	cfg := config.ProjectConfig{
		Checks: map[string]config.CheckConfig{
			"web-e2e": {
				Command:       "pnpm --filter web test:e2e",
				Browser:       "chromium",
				Prepare:       []string{"pnpm install --frozen-lockfile"},
				ArtifactPaths: []string{"playwright-report/**"},
				Outputs:       config.CheckOutputsConfig{Reviewable: []string{"tests/fixtures/**"}},
			},
		},
	}
	plan, err := resolveCheckRunPlan("web-e2e", cfg, checkResolveOptions{RetryCount: -1})
	if err != nil {
		t.Fatalf("resolve check: %v", err)
	}
	if len(plan.Options.PrepareCommands) != 2 {
		t.Fatalf("prepare commands = %v, want user prepare then chromium install", plan.Options.PrepareCommands)
	}
	if plan.Options.PrepareCommands[0] != "pnpm install --frozen-lockfile" || !strings.Contains(plan.Options.PrepareCommands[1], "playwright install chromium") || strings.Contains(plan.Options.PrepareCommands[1], "--with-deps") {
		t.Fatalf("prepare commands = %v, want browser setup after user prepare", plan.Options.PrepareCommands)
	}
	if !containsString(plan.Options.ArtifactPaths, "tests/fixtures/**") {
		t.Fatalf("artifact paths = %v, want reviewable output retained", plan.Options.ArtifactPaths)
	}
}

func TestAffectedSummaryCountsUntrackedFiles(t *testing.T) {
	repo := chdirCLITestRepo(t, "affected-summary-untracked")
	runGitForCLITest(t, "init")
	runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
affected:
  rules:
    - paths: ["scratch/**"]
      checks: [scratch]
checks:
  scratch:
    command: echo scratch
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
		return affectedCommand([]string{"--summary"})
	})
	if err != nil {
		t.Fatalf("affected summary: %v\n%s", err, output)
	}
	requireCLITestOutputContains(t, output, "change_mode: working changed_files: 1 untracked_files: 1")
}

func TestDryRunArtifactProducerWarningsAndSummaryOutput(t *testing.T) {
	report := checkDryRunReport{
		Check:          "fixtures",
		Command:        "cargo test --workspace",
		Image:          "ubuntu-24.04-base",
		RunnerSize:     "standard",
		TimeoutSeconds: 600,
		NetworkMode:    "default",
		ArtifactPaths:  []string{"target/stale-dashboard.md"},
		Sync: syncDryRunReport{
			ChangedFilesCount: 2,
			UntrackedCount:    1,
		},
	}
	report.Warnings = artifactProducerWarnings(report)
	if len(report.Warnings) != 1 || !strings.Contains(report.Warnings[0], "target/stale-dashboard.md") {
		t.Fatalf("warnings = %v, want stale artifact warning", report.Warnings)
	}
	output, err := captureStdoutForCLITest(t, func() error {
		printCheckDryRunSummary(report)
		return nil
	})
	if err != nil {
		t.Fatalf("summary output: %v", err)
	}
	requireCLITestOutputContains(t, output, "check: fixtures", "artifacts: 1", "changed_files: 2 untracked_files: 1", "warnings:")
}

func TestSuggestedDoctorRunCommandIsRepoAware(t *testing.T) {
	repo := chdirCLITestRepo(t, "doctor-suggestions")
	if err := os.WriteFile(filepath.Join(repo, "Cargo.toml"), []byte("[workspace]\nmembers = []\n"), 0o644); err != nil {
		t.Fatalf("write Cargo.toml: %v", err)
	}
	if got := suggestedDoctorRunCommand(repo); got != "cargo test --workspace" {
		t.Fatalf("rust doctor command = %q", got)
	}
	if err := os.Remove(filepath.Join(repo, "Cargo.toml")); err != nil {
		t.Fatalf("remove Cargo.toml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "package.json"), []byte(`{"packageManager":"pnpm@10.0.0","scripts":{"check":"pnpm -r test"}}`), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "pnpm-lock.yaml"), []byte("lockfileVersion: 9\n"), 0o644); err != nil {
		t.Fatalf("write pnpm lock: %v", err)
	}
	if got := suggestedDoctorRunCommand(repo); got != "pnpm run check" {
		t.Fatalf("node doctor command = %q", got)
	}
	if err := os.Remove(filepath.Join(repo, "pnpm-lock.yaml")); err != nil {
		t.Fatalf("remove pnpm lock: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "package.json"), []byte(`{"scripts":{"build":"vite build"}}`), 0o644); err != nil {
		t.Fatalf("write npm package.json: %v", err)
	}
	if got := suggestedDoctorRunCommand(repo); got != "npm run build" {
		t.Fatalf("npm doctor command = %q", got)
	}
}
