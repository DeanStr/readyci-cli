package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

func TestAffectedProfilesSelectConfiguredChecks(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(repo, "scripts", "check.sh"), []byte("#!/usr/bin/env bash\necho ok\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
affected:
  profiles:
    - shell
checks:
  shell-syntax:
    command: bash -n scripts/check.sh
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, "scripts", "check.sh"), []byte("#!/usr/bin/env bash\necho changed\n"), 0o755); err != nil {
		t.Fatalf("modify script: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return affectedCommand([]string{"--dry-run", "--json"})
	})
	if err != nil {
		t.Fatalf("affected dry-run: %v", err)
	}
	var report affectedDryRunReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode affected report: %v\n%s", err, output)
	}
	if len(report.SelectedChecks) != 1 || report.SelectedChecks[0].Name != "shell-syntax" {
		t.Fatalf("selected checks = %+v, want shell-syntax", report.SelectedChecks)
	}
}

func TestAffectedBaselineProfilesSelectConfiguredChecks(t *testing.T) {
	cfg := config.ProjectConfig{
		Checks: map[string]config.CheckConfig{
			"go":           {Command: "go test ./..."},
			"docs":         {Command: "markdownlint docs README.md"},
			"ci":           {Command: "readyci config validate"},
			"shell-syntax": {Command: "bash -n scripts/check.sh"},
		},
		Affected: config.AffectedConfig{
			Profiles: []string{"docs", "go", "readyci", "shell"},
		},
	}
	expanded, warnings, err := expandAffectedConfig(cfg)
	if err != nil {
		t.Fatalf("expand profiles: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	if len(expanded.Affected.Rules) == 0 {
		t.Fatalf("expanded rules empty for baseline profile names")
	}
	selected, _, err := selectChecksForAffectedRules(expanded, map[string][]string{
		"go":           {"main.go"},
		"docs":         {"README.md"},
		"ci":           {".readyci.yml"},
		"shell-syntax": {"scripts/check.sh"},
	})
	if err != nil {
		t.Fatalf("select baseline profiles: %v", err)
	}
	for _, want := range []string{"go", "docs", "ci", "shell-syntax"} {
		if !containsString(selected, want) {
			t.Fatalf("selected = %v, missing %s", selected, want)
		}
	}
}

func TestHostedAffectedProfilesAreNotExpandedLocally(t *testing.T) {
	cfg := config.ProjectConfig{
		Checks: map[string]config.CheckConfig{
			"web": {Command: "pnpm web-check"},
		},
		Affected: config.AffectedConfig{
			Profiles: []string{"vite-react"},
		},
	}
	_, _, err := expandAffectedConfig(cfg)
	if err == nil ||
		!strings.Contains(err.Error(), `unknown affected profile "vite-react"`) ||
		!strings.Contains(err.Error(), "readyci plan --recommend --suggest-config") ||
		!strings.Contains(err.Error(), "convert it to explicit affected.rules") {
		t.Fatalf("expand hosted profile err = %v, want hosted profile migration hint", err)
	}
}

func TestAffectedProfilesMatchNestedModuleManifests(t *testing.T) {
	cases := []struct {
		name    string
		profile string
		check   string
		command string
		paths   []string
	}{
		{
			name:    "go",
			profile: "go",
			check:   "go",
			command: "go test ./...",
			paths:   []string{"cli/go.mod", "platform/go.sum"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.ProjectConfig{
				Checks: map[string]config.CheckConfig{
					tc.check: {Command: tc.command},
				},
				Affected: config.AffectedConfig{
					Profiles: []string{tc.profile},
				},
			}
			expanded, warnings, err := expandAffectedConfig(cfg)
			if err != nil {
				t.Fatalf("expand profile %q: %v", tc.profile, err)
			}
			if len(warnings) != 0 {
				t.Fatalf("warnings = %v, want none", warnings)
			}
			for _, changedPath := range tc.paths {
				selected, _, err := selectChecksForAffectedRules(expanded, map[string][]string{
					tc.check: {changedPath},
				})
				if err != nil {
					t.Fatalf("select affected rules for %s: %v", changedPath, err)
				}
				if strings.Join(selected, ",") != tc.check {
					t.Fatalf("selected = %v, want only %s for %s", selected, tc.check, changedPath)
				}
			}
		})
	}
}

func TestAffectedProfilesDoNotDuplicateRootManifestReasons(t *testing.T) {
	cfg := config.ProjectConfig{
		Checks: map[string]config.CheckConfig{
			"go": {Command: "go test ./..."},
		},
		Affected: config.AffectedConfig{
			Profiles: []string{"go"},
		},
	}
	expanded, warnings, err := expandAffectedConfig(cfg)
	if err != nil {
		t.Fatalf("expand profile: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	_, reasons, err := selectChecksForAffectedRules(expanded, map[string][]string{
		"go": {"go.mod"},
	})
	if err != nil {
		t.Fatalf("select affected rules: %v", err)
	}
	seen := map[string]bool{}
	for _, reason := range reasons["go"] {
		key := reason.Pattern + "\x00" + reason.Path
		if seen[key] {
			t.Fatalf("duplicate reason for root manifest: %+v", reasons["go"])
		}
		seen[key] = true
		if reason.Path != "go.mod" {
			t.Fatalf("reason path = %q, want go.mod", reason.Path)
		}
	}
	if len(reasons["go"]) != 1 {
		t.Fatalf("reasons = %+v, want one root manifest reason", reasons["go"])
	}
}

func TestAffectedCommandRejectsStagedAndChangesTogether(t *testing.T) {
	err := affectedCommand([]string{"--staged", "--changes", "staged", "--dry-run"})
	if err == nil || !strings.Contains(err.Error(), "use either --staged or --changes") {
		t.Fatalf("affected staged+changes error = %v, want conflict error", err)
	}
}

func TestAffectedProfilesWithoutMatchingChecksError(t *testing.T) {
	cfg := config.ProjectConfig{
		Affected: config.AffectedConfig{Profiles: []string{"shell"}},
		Checks: map[string]config.CheckConfig{
			"custom": {Command: "make custom"},
		},
	}
	err := executeAffectedRules(".", cfg, ".readyci.yml", checkResolveOptions{}, checkExecutionOptions{DryRun: true})
	if err == nil || !strings.Contains(err.Error(), `affected profile "shell" did not match any configured check names`) {
		t.Fatalf("execute affected profile without matching checks error = %v", err)
	}
}

func TestAffectedExplainRejectsProfileOnlyConfigWithNoGeneratedRules(t *testing.T) {
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
affected:
  profiles:
    - shell
checks:
  custom:
    command: make custom
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	err = affectedExplainCommand([]string{"apps/web/src/App.tsx"})
	if err == nil || !strings.Contains(err.Error(), `affected profile "shell" did not match any configured check names`) {
		t.Fatalf("affected explain error = %v, want profile expansion error", err)
	}
}
