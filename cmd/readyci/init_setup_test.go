package main

import (
	"errors"
	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitCommandWritesRemoteCITemplate(t *testing.T) {
	chdirCLITestRepo(t, "Airline Project")

	if err := initCommand(nil); err != nil {
		t.Fatalf("init command: %v", err)
	}
	data, err := os.ReadFile(".readyci.yml")
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}
	configText := string(data)
	for _, want := range []string{
		`project_id: "airline-project"`,
		"command: make check",
		"timeout: 20m",
		`# image: "ubuntu-24.04-ci-docker"`,
	} {
		if !strings.Contains(configText, want) {
			t.Fatalf("generated config missing %q:\n%s", want, configText)
		}
	}
	for _, notWant := range []string{"go-postgres", "vite-react", "apps/web/.next/cache", "playwright-report", "- .env*\n"} {
		if strings.Contains(configText, notWant) {
			t.Fatalf("generated starter leaked smart detection %q:\n%s", notWant, configText)
		}
	}
	if strings.Contains(configText, "\nimage:") {
		t.Fatalf("generated default config pins an image:\n%s", configText)
	}
	loadedConfig, _, err := config.LoadProjectConfig(".")
	if err != nil {
		t.Fatalf("load generated config: %v", err)
	}
	if loadedConfig.Image != preflightapi.DefaultImage {
		t.Fatalf("generated default config image = %q, want %q", loadedConfig.Image, preflightapi.DefaultImage)
	}
	if err := initCommand(nil); err == nil {
		t.Fatal("init command overwrote existing config without --force")
	}
	if err := os.Remove(".readyci.yml"); err != nil {
		t.Fatalf("remove generated config: %v", err)
	}
	if err := os.WriteFile(".readyci.yaml", []byte("version: 1\n"), 0o644); err != nil {
		t.Fatalf("write alternate config: %v", err)
	}
	if err := initCommand(nil); err == nil {
		t.Fatal("init command ignored existing .readyci.yaml")
	}
	if err := initCommand([]string{"--force"}); err != nil {
		t.Fatalf("init command force overwrite .readyci.yaml: %v", err)
	}
	if _, err := os.Stat(".readyci.yml"); !os.IsNotExist(err) {
		t.Fatalf("force with .readyci.yaml created .readyci.yml: %v", err)
	}
	forcedData, err := os.ReadFile(".readyci.yaml")
	if err != nil {
		t.Fatalf("read force-overwritten .readyci.yaml: %v", err)
	}
	if !strings.Contains(string(forcedData), `project_id: "airline-project"`) {
		t.Fatalf("force-overwritten config missing generated template:\n%s", forcedData)
	}
}

func TestGeneratedAPIURLCommandsNormalizeSensitiveComponents(t *testing.T) {
	rawAPIURL := " https://user:pass@api.readyci.test/base?token=secret#fragment "

	for name, command := range map[string]string{
		"flag":   "readyci check" + apiURLFlag(rawAPIURL) + " ci",
		"export": readyCIAPIEnvCommand(rawAPIURL, "<token>"),
	} {
		if !strings.Contains(command, "https://api.readyci.test/base") {
			t.Fatalf("%s command did not contain normalized API URL: %s", name, command)
		}
		for _, notWant := range []string{"user:pass", "token=secret", "#fragment"} {
			if strings.Contains(command, notWant) {
				t.Fatalf("%s command leaked %q: %s", name, notWant, command)
			}
		}
	}
}

func TestInitCommandCanPinConfiguredImage(t *testing.T) {
	chdirCLITestRepo(t, "")

	if err := initCommand([]string{"--image", " ubuntu-24.04-ci-docker "}); err != nil {
		t.Fatalf("init command with image: %v", err)
	}
	data, err := os.ReadFile(".readyci.yml")
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}
	if !strings.Contains(string(data), `image: "ubuntu-24.04-ci-docker"`) {
		t.Fatalf("generated config did not pin requested image:\n%s", data)
	}
}

func TestDetectedTemplateUsesMakeRuleTargetsNotAssignments(t *testing.T) {
	repo := chdirCLITestRepo(t, "make-detect")
	if err := os.WriteFile(filepath.Join(repo, "Makefile"), []byte("check := go test ./...\ncheck-ci:\n\tgo test ./...\n"), 0o644); err != nil {
		t.Fatalf("write Makefile: %v", err)
	}

	template := detectedProjectTemplateWithProfile(repo, "make-detect", "", "remote-ci")
	if strings.Contains(template, "command: \"make check\"") {
		t.Fatalf("detected template treated variable assignment as target:\n%s", template)
	}
	if !strings.Contains(template, "command: \"make check-ci\"") {
		t.Fatalf("detected template missing check-ci target:\n%s", template)
	}
}

func TestDetectedTemplatePreservesNestedMakeTargetDirectory(t *testing.T) {
	repo := chdirCLITestRepo(t, "nested-make-detect")
	if err := os.MkdirAll(filepath.Join(repo, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "sub", "Makefile"), []byte("check:\n\tgo test ./...\n"), 0o644); err != nil {
		t.Fatalf("write nested Makefile: %v", err)
	}

	template := detectedProjectTemplateWithProfile(repo, "nested-make-detect", "", "remote-ci")
	if !strings.Contains(template, `command: "make -C sub check"`) {
		t.Fatalf("detected template did not preserve nested Makefile target directory:\n%s", template)
	}
	if strings.Contains(template, `command: "make check"`) {
		t.Fatalf("detected template emitted root make command for nested target:\n%s", template)
	}
}

func TestDetectedTemplateKeepsExampleEnvFilesSyncable(t *testing.T) {
	repo := chdirCLITestRepo(t, "env-detect")
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.test/env\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	template := detectedProjectTemplateWithProfile(repo, "env-detect", "", "remote-ci")
	if strings.Contains(template, "- .env*\n") {
		t.Fatalf("detected template excludes all .env* files:\n%s", template)
	}
	if !strings.Contains(template, "- .env\n") || !strings.Contains(template, "- .env.*.local\n") {
		t.Fatalf("detected template missing enumerated env excludes:\n%s", template)
	}
}

func TestDetectedTemplateAddsAffectedCoverageForEveryGeneratedCheck(t *testing.T) {
	repo := chdirCLITestRepo(t, "mixed-detect")
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.test/mixed\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "Cargo.toml"), []byte("[workspace]\nmembers = [\"crates/core\"]\n"), 0o644); err != nil {
		t.Fatalf("write Cargo.toml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "package.json"), []byte(`{"scripts":{"check":"vitest run"}}`), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}

	template := detectedProjectTemplateWithProfile(repo, "mixed-detect", "", "remote-ci")
	cfg, err := config.ParseProjectConfig([]byte(template), ".readyci.yml")
	if err != nil {
		t.Fatalf("parse generated template: %v\n%s", err, template)
	}
	for _, want := range []string{"go", "rust", "node"} {
		if _, ok := cfg.Checks[want]; !ok {
			t.Fatalf("generated checks = %v, missing %s\n%s", sortedCheckNames(cfg.Checks), want, template)
		}
		if !affectedRulesContainCheck(cfg.Affected.Rules, want) {
			t.Fatalf("generated affected rules = %+v, missing check %s\n%s", cfg.Affected.Rules, want, template)
		}
		if !containsString([]string(cfg.Affected.UnmatchedChecks), want) {
			t.Fatalf("generated unmatched checks = %v, missing safe default %s\n%s", cfg.Affected.UnmatchedChecks, want, template)
		}
	}
	selected, _, err := selectChecksForAffectedRules(cfg, map[string][]string{
		"go":   {"main.go"},
		"rust": {"crates/core/src/lib.rs"},
		"node": {"src/App.tsx"},
	})
	if err != nil {
		t.Fatalf("select generated affected rules: %v", err)
	}
	for _, want := range []string{"go", "rust", "node"} {
		if !containsString(selected, want) {
			t.Fatalf("selected checks = %v, missing %s", selected, want)
		}
	}
	selected, _, err = selectChecksForAffectedRules(cfg, map[string][]string{
		"node": {"web/src/App.tsx"},
	})
	if err != nil {
		t.Fatalf("select generated nested Node affected rules: %v", err)
	}
	if !containsString(selected, "node") {
		t.Fatalf("selected checks for nested Node workspace path = %v, want node", selected)
	}
	selected, _, err = selectChecksForAffectedRules(cfg, map[string][]string{
		"go": {"tooling/unknown.conf"}, "rust": {"tooling/unknown.conf"}, "node": {"tooling/unknown.conf"},
	})
	if err != nil {
		t.Fatalf("select generated unmatched checks: %v", err)
	}
	for _, want := range []string{"go", "rust", "node"} {
		if !containsString(selected, want) {
			t.Fatalf("selected unmatched checks = %v, missing %s", selected, want)
		}
	}
}

func TestDetectedTemplateUsesRustWorkspaceCommand(t *testing.T) {
	repo := chdirCLITestRepo(t, "rust-detect")
	if err := os.WriteFile(filepath.Join(repo, "Cargo.toml"), []byte("[package]\nname = \"root\"\nversion = \"0.1.0\"\n[workspace]\nmembers = [\"crates/core\"]\n"), 0o644); err != nil {
		t.Fatalf("write Cargo.toml: %v", err)
	}

	template := detectedProjectTemplateWithProfile(repo, "rust-detect", "", "remote-ci")
	cfg, err := config.ParseProjectConfig([]byte(template), ".readyci.yml")
	if err != nil {
		t.Fatalf("parse generated template: %v\n%s", err, template)
	}
	if got := cfg.Checks["rust"].Command; got != "cargo test --workspace --all-targets" {
		t.Fatalf("rust command = %q, want workspace-wide test\n%s", got, template)
	}
}

func TestInitCommandStrictProfileUsesNetworkNone(t *testing.T) {
	chdirCLITestRepo(t, "")

	if err := initCommand([]string{"--profile", "strict"}); err != nil {
		t.Fatalf("init command with strict profile: %v", err)
	}
	data, err := os.ReadFile(".readyci.yml")
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}
	if !strings.Contains(string(data), "network:\n  mode: none\n") {
		t.Fatalf("strict generated config did not default network to none:\n%s", data)
	}
}

func TestInitCommandQuotesYAMLProjectID(t *testing.T) {
	chdirCLITestRepo(t, "NULL")

	if err := initCommand(nil); err != nil {
		t.Fatalf("init command: %v", err)
	}
	data, err := os.ReadFile(".readyci.yml")
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}
	if !strings.Contains(string(data), `project_id: "null"`) {
		t.Fatalf("generated config did not quote YAML null literal:\n%s", data)
	}
	cfg, _, err := config.LoadProjectConfig(".")
	if err != nil {
		t.Fatalf("load generated config: %v", err)
	}
	if cfg.ProjectID != "null" {
		t.Fatalf("project id = %q, want null", cfg.ProjectID)
	}
}

func TestSetupReportDoesNotWriteConfigByDefault(t *testing.T) {
	t.Setenv("READYCI_CONFIG_DIR", t.TempDir())
	chdirCLITestRepo(t, "")

	report, err := buildSetupReport(setupOptions{APIURL: "https://api.readyci.test", ProjectID: "airline"})
	if err != nil {
		t.Fatalf("build setup report: %v", err)
	}
	if report.ConfigStatus != "missing" || report.AuthStatus != "missing" {
		t.Fatalf("setup report = %+v, want missing config/auth", report)
	}
	if _, err := os.Stat(".readyci.yml"); !os.IsNotExist(err) {
		t.Fatalf("setup wrote config without --write-config: %v", err)
	}
	if !containsString(report.Next, "readyci setup --write-config --detect --api-url https://api.readyci.test --project airline") {
		t.Fatalf("setup next commands = %+v, want guided setup command", report.Next)
	}
	if !containsString(report.Next, "readyci init --detect --print") {
		t.Fatalf("setup next commands = %+v, want inspect-only init command", report.Next)
	}
	if !containsString(report.Next, "export READYCI_API_URL=https://api.readyci.test READYCI_API_TOKEN='<token>'") {
		t.Fatalf("setup next commands = %+v, want quoted token placeholder", report.Next)
	}
	if !containsString(report.Next, "readyci run --api-url https://api.readyci.test --project airline 'go test ./...'") {
		t.Fatalf("setup next commands = %+v, want self-contained run command", report.Next)
	}
	if !containsString(report.Next, "readyci runs latest --api-url https://api.readyci.test --project airline") {
		t.Fatalf("setup next commands = %+v, want self-contained latest command", report.Next)
	}
}

func TestSetupReportCanWriteDetectedConfig(t *testing.T) {
	t.Setenv("READYCI_CONFIG_DIR", t.TempDir())
	chdirCLITestRepo(t, "")
	if err := os.WriteFile("go.mod", []byte("module example.test/ready\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	report, err := buildSetupReport(setupOptions{
		APIURL:      "https://api.readyci.test",
		ProjectID:   "airline",
		WriteConfig: true,
		Detect:      true,
	})
	if err != nil {
		t.Fatalf("build setup report: %v", err)
	}
	if report.ConfigStatus != "ok" || report.ConfigPath == "" || report.Check != "go" {
		t.Fatalf("setup report = %+v, want conservative go config", report)
	}
	loaded, _, err := config.LoadProjectConfig(".")
	if err != nil {
		t.Fatalf("load generated config: %v", err)
	}
	if loaded.ProjectID != "airline" || loaded.Checks["go"].Command != "go test ./..." {
		t.Fatalf("generated config = %+v, want airline go starter", loaded)
	}
	if !containsString(report.Warnings, "smart local config detection moved to hosted analysis; run readyci plan --recommend --suggest-config") {
		t.Fatalf("warnings = %+v, want hosted analyzer warning", report.Warnings)
	}
	if !containsString(report.Next, "readyci check --api-url https://api.readyci.test --project airline --open go") {
		t.Fatalf("setup next commands = %+v, want self-contained check command", report.Next)
	}
}

func writeNodePackageForInitTest(t *testing.T, rel, label string) {
	t.Helper()
	if rel != "." && rel != "" {
		if err := os.MkdirAll(filepath.FromSlash(rel), 0o755); err != nil {
			t.Fatalf("mkdir node package %s: %v", rel, err)
		}
	}
	dir := "."
	if rel != "" {
		dir = filepath.FromSlash(rel)
	}
	data := `{"scripts":{"check":"vite build"},"devDependencies":{"vite":"latest","react":"latest"},"packageManager":"pnpm@10.0.0","name":"` + label + `"}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(data), 0o644); err != nil {
		t.Fatalf("write package.json %s: %v", rel, err)
	}
}

func TestSetupReportStrictProfileWritesNetworkNone(t *testing.T) {
	t.Setenv("READYCI_CONFIG_DIR", t.TempDir())
	chdirCLITestRepo(t, "")

	report, err := buildSetupReport(setupOptions{
		APIURL:      "https://api.readyci.test",
		ProjectID:   "airline",
		WriteConfig: true,
		Detect:      false,
		Profile:     "strict",
	})
	if err != nil {
		t.Fatalf("build setup report: %v", err)
	}
	if report.ConfigStatus != "ok" {
		t.Fatalf("setup report = %+v, want ok", report)
	}
	data, err := os.ReadFile(".readyci.yml")
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}
	if !strings.Contains(string(data), "network:\n  mode: none\n") {
		t.Fatalf("strict setup config did not default network to none:\n%s", data)
	}
}

func TestSetupWriteConfigAndHelpersHonorsDetectFalse(t *testing.T) {
	t.Setenv("READYCI_CONFIG_DIR", t.TempDir())
	chdirCLITestRepo(t, "")
	if err := os.WriteFile("go.mod", []byte("module example.test/ready\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	report, err := buildSetupReport(setupOptions{
		ProjectID:    "airline",
		WriteConfig:  true,
		WriteHelpers: true,
		Detect:       false,
	})
	if err != nil {
		t.Fatalf("build setup report: %v", err)
	}
	cfg, _, err := config.LoadProjectConfig(".")
	if err != nil {
		t.Fatalf("load generated config: %v", err)
	}
	if _, ok := cfg.Checks["go"]; ok {
		t.Fatalf("generated checks = %v, did not honor detect=false", sortedCheckNames(cfg.Checks))
	}
	for _, want := range []string{"ci", "shell-syntax", "ops-config"} {
		if _, ok := cfg.Checks[want]; !ok {
			t.Fatalf("generated checks = %v, missing %s", sortedCheckNames(cfg.Checks), want)
		}
	}
	if containsString(report.Warnings, "smart local config detection moved to hosted analysis; run readyci plan --recommend --suggest-config") {
		t.Fatalf("warnings = %+v, should not include hosted analyzer warning when detect=false", report.Warnings)
	}
	if !contains(report.Written, "scripts/readyci-check-shell-syntax.sh") || !contains(report.Written, "scripts/readyci-check-ops-config.sh") {
		t.Fatalf("written files = %v, want helper scripts", report.Written)
	}
}

func TestSetupWriteConfigAndHelpersKeepsStarterCheckWithoutProjectSignals(t *testing.T) {
	t.Setenv("READYCI_CONFIG_DIR", t.TempDir())
	chdirCLITestRepo(t, "")

	report, err := buildSetupReport(setupOptions{
		ProjectID:    "airline",
		WriteConfig:  true,
		WriteHelpers: true,
		Detect:       true,
	})
	if err != nil {
		t.Fatalf("build setup report: %v", err)
	}
	cfg, _, err := config.LoadProjectConfig(".")
	if err != nil {
		t.Fatalf("load generated config: %v", err)
	}
	if report.Check != "ci" {
		t.Fatalf("setup selected check = %q, want ci", report.Check)
	}
	for _, want := range []string{"ci", "shell-syntax", "ops-config"} {
		if _, ok := cfg.Checks[want]; !ok {
			t.Fatalf("generated checks = %v, missing %s", sortedCheckNames(cfg.Checks), want)
		}
	}
}

func TestSetupReportPreservesExplicitTouchedCheckInNextCommands(t *testing.T) {
	t.Setenv("READYCI_CONFIG_DIR", t.TempDir())
	chdirCLITestRepo(t, "")
	if err := os.WriteFile(".readyci.yml", []byte(`
project_id: airline
checks:
  affected:
    command: make check-affected
  touched:
    command: make check-touched
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	report, err := buildSetupReport(setupOptions{
		APIURL:    "https://api.readyci.test",
		ProjectID: "airline",
		CheckName: "touched",
	})
	if err != nil {
		t.Fatalf("build setup report: %v", err)
	}
	if report.Check != "touched" {
		t.Fatalf("setup check = %q, want touched", report.Check)
	}
	if !containsString(report.Next, "readyci affected --check touched --dry-run") {
		t.Fatalf("setup next commands = %+v, want touched dry-run to preserve explicit check", report.Next)
	}
	if !containsString(report.Next, "readyci affected --api-url https://api.readyci.test --project airline --check touched --open") {
		t.Fatalf("setup next commands = %+v, want touched run to preserve explicit check", report.Next)
	}
	if containsString(report.Next, "readyci affected --dry-run") {
		t.Fatalf("setup next commands = %+v, should not suggest generic affected for explicit touched", report.Next)
	}
}

func TestSetupReportOnlySuggestsWarmProfileWhenItCanMatch(t *testing.T) {
	t.Setenv("READYCI_CONFIG_DIR", t.TempDir())
	chdirCLITestRepo(t, "")
	if err := os.WriteFile(".readyci.yml", []byte(`
project_id: airline
checks:
  ci:
    command: make ci
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	report, err := buildSetupReport(setupOptions{APIURL: "https://api.readyci.test", ProjectID: "airline"})
	if err != nil {
		t.Fatalf("build setup report: %v", err)
	}
	if contains(report.Next, "readyci warm --profile dependencies --dry-run") {
		t.Fatalf("setup next commands = %+v, should not suggest unmatched warm profile", report.Next)
	}
	if contains(report.Next, "readyci affected audit --strict --duplicates") {
		t.Fatalf("setup next commands = %+v, should not suggest affected audit without rules/profiles", report.Next)
	}

	if err := os.WriteFile(".readyci.yml", []byte(`
project_id: airline
cache:
  paths:
    - node_modules
checks:
  ci:
    command: make ci
    network_mode: invalid
`), 0o644); err != nil {
		t.Fatalf("write invalid cached config: %v", err)
	}
	report, err = buildSetupReport(setupOptions{APIURL: "https://api.readyci.test", ProjectID: "airline"})
	if err != nil {
		t.Fatalf("build invalid cached setup report: %v", err)
	}
	if contains(report.Next, "readyci warm --profile dependencies --dry-run") {
		t.Fatalf("setup next commands = %+v, should not suggest warm profile for invalid config", report.Next)
	}

	if err := os.WriteFile(".readyci.yml", []byte(`
project_id: airline
cache:
  paths:
    - node_modules
checks:
  dependencies:
    command: pnpm install --frozen-lockfile
  ci:
    command: make ci
`), 0o644); err != nil {
		t.Fatalf("write cached config: %v", err)
	}
	report, err = buildSetupReport(setupOptions{APIURL: "https://api.readyci.test", ProjectID: "airline"})
	if err != nil {
		t.Fatalf("build cached setup report: %v", err)
	}
	if !contains(report.Next, "readyci warm --profile dependencies --dry-run") {
		t.Fatalf("setup next commands = %+v, want warm profile suggestion", report.Next)
	}
	if contains(report.Next, "readyci affected audit --strict --duplicates") {
		t.Fatalf("setup next commands = %+v, should not suggest affected audit without rules/profiles", report.Next)
	}

	if err := os.WriteFile(".readyci.yml", []byte(`
project_id: airline
checks:
  ci:
    command: make ci
affected:
  rules:
    - paths:
        - "**/*.go"
      checks:
        - ci
`), 0o644); err != nil {
		t.Fatalf("write affected config: %v", err)
	}
	report, err = buildSetupReport(setupOptions{APIURL: "https://api.readyci.test", ProjectID: "airline"})
	if err != nil {
		t.Fatalf("build affected setup report: %v", err)
	}
	if !contains(report.Next, "readyci affected audit --strict --duplicates") {
		t.Fatalf("setup next commands = %+v, want affected audit suggestion", report.Next)
	}
}

func TestSetupReportForceOverwritesInvalidConfig(t *testing.T) {
	t.Setenv("READYCI_CONFIG_DIR", t.TempDir())
	chdirCLITestRepo(t, "")
	if err := os.WriteFile(".readyci.yml", []byte("checks: ["), 0o644); err != nil {
		t.Fatalf("write invalid config: %v", err)
	}

	invalidReport, err := buildSetupReport(setupOptions{
		APIURL:    "https://api.readyci.test",
		ProjectID: "airline",
	})
	if err != nil {
		t.Fatalf("build setup report for invalid config: %v", err)
	}
	if invalidReport.ConfigStatus != "invalid" || len(invalidReport.Warnings) == 0 {
		t.Fatalf("invalid setup report = %+v, want invalid status with warning", invalidReport)
	}

	report, err := buildSetupReport(setupOptions{
		APIURL:      "https://api.readyci.test",
		ProjectID:   "airline",
		WriteConfig: true,
		Force:       true,
		Detect:      false,
	})
	if err != nil {
		t.Fatalf("build setup report: %v", err)
	}
	if report.ConfigStatus != "ok" {
		t.Fatalf("setup report = %+v, want ok after force rewrite", report)
	}
	if _, _, err := config.LoadProjectConfig("."); err != nil {
		t.Fatalf("load rewritten config: %v", err)
	}
}

func TestSetupReportRejectsUnsafeProjectID(t *testing.T) {
	chdirCLITestRepo(t, "")
	_, err := buildSetupReport(setupOptions{
		APIURL:    "https://api.readyci.test",
		ProjectID: "airline\nshadow",
	})
	if err == nil || !strings.Contains(err.Error(), "project_id must not contain control characters") {
		t.Fatalf("setup report unsafe project err = %v, want project_id rejection", err)
	}
}

func TestSetupCommandRejectsConflictingOutputFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "positional arg",
			args: []string{"extra"},
			want: "readyci setup",
		},
		{
			name: "unknown profile",
			args: []string{"--profile", "bespoke"},
			want: `unknown setup profile "bespoke"`,
		},
		{
			name: "makefile write helper",
			args: []string{"--makefile", "--write-helper"},
			want: "--makefile cannot be combined with --write-helper",
		},
		{
			name: "makefile write makefile",
			args: []string{"--makefile", "--write-makefile"},
			want: "--makefile cannot be combined with --write-makefile",
		},
		{
			name: "makefile write helpers",
			args: []string{"--makefile", "--write-helpers"},
			want: "--makefile cannot be combined with --write-helpers",
		},
		{
			name: "makefile write config",
			args: []string{"--makefile", "--write-config"},
			want: "--makefile cannot be combined with --write-config",
		},
		{
			name: "makefile json",
			args: []string{"--makefile", "--json"},
			want: "--json cannot be combined with --makefile",
		},
		{
			name: "write helper requires helper",
			args: []string{"--write-helper"},
			want: "--write-helper requires --helper NAME",
		},
		{
			name: "write helper write helpers",
			args: []string{"--helper", "shell-syntax", "--write-helper", "--write-helpers"},
			want: "use either --write-helper or --write-helpers",
		},
		{
			name: "helper write config",
			args: []string{"--helper", "shell-syntax", "--write-config"},
			want: "--helper cannot be combined with --write-config",
		},
		{
			name: "helper write makefile",
			args: []string{"--helper", "shell-syntax", "--write-makefile"},
			want: "--helper cannot be combined with --write-makefile",
		},
		{
			name: "helper write helpers",
			args: []string{"--helper", "shell-syntax", "--write-helpers"},
			want: "--helper cannot be combined with --write-helpers",
		},
		{
			name: "helper json",
			args: []string{"--helper", "shell-syntax", "--json"},
			want: "--json cannot be combined with --helper",
		},
		{
			name: "makefile helper",
			args: []string{"--makefile", "--helper", "shell-syntax"},
			want: "--makefile cannot be combined with --helper",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := setupCommand(tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("setupCommand(%v) error = %v, want %q", tt.args, err, tt.want)
			}
		})
	}
}

func TestSetupCommandPrintsMakefileAndHelperOutputs(t *testing.T) {
	repo := chdirCLITestRepo(t, "")
	t.Setenv("READYCI_PROJECT_ID", "ambient-project")
	runGitForCLITest(t, "init")
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte("project_id: airline\nchecks:\n  ci:\n    command: make ci\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	makefile, err := captureStdoutForCLITest(t, func() error {
		return setupCommand([]string{"--makefile", "--api-url", "https://api.readyci.test"})
	})
	if err != nil {
		t.Fatalf("setup --makefile: %v", err)
	}
	requireCLITestOutputContains(t, makefile,
		"READYCI_API_URL ?= https://api.readyci.test",
		"READYCI_PROJECT_ID ?= airline",
		"remote-check-ci:",
	)

	helper, err := captureStdoutForCLITest(t, func() error {
		return setupCommand([]string{"--helper", "shell-syntax"})
	})
	if err != nil {
		t.Fatalf("setup --helper: %v", err)
	}
	requireCLITestOutputContains(t, helper,
		"#!/usr/bin/env bash",
		"bash -n --",
		"CHECK_TOUCHED_FILES_FROM",
	)
}

func TestSetupCommandWritesMakefilePrefersConfigProjectOverAmbientEnv(t *testing.T) {
	repo := chdirCLITestRepo(t, "")
	t.Setenv("READYCI_PROJECT_ID", "ambient-project")
	runGitForCLITest(t, "init")
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte("project_id: airline\nchecks:\n  ci:\n    command: make ci\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return setupCommand([]string{"--write-makefile", "--api-url", "https://api.readyci.test"})
	})
	if err != nil {
		t.Fatalf("setup --write-makefile: %v\n%s", err, output)
	}
	makefile, err := os.ReadFile(filepath.Join(repo, "readyci.mk"))
	if err != nil {
		t.Fatalf("read readyci.mk: %v", err)
	}
	requireCLITestOutputContains(t, string(makefile), "READYCI_PROJECT_ID ?= airline")
	if strings.Contains(string(makefile), "READYCI_PROJECT_ID ?= ambient-project") {
		t.Fatalf("readyci.mk used ambient project instead of config project:\n%s", makefile)
	}
}

func TestSetupShellSyntaxHelperScript(t *testing.T) {
	path, content, err := setupHelperScript("shell-syntax")
	if err != nil {
		t.Fatalf("setup helper: %v", err)
	}
	if path != "scripts/readyci-check-shell-syntax.sh" {
		t.Fatalf("helper path = %q", path)
	}
	requireCLITestOutputContains(t, content,
		"CHECK_TOUCHED_FILES_FROM",
		"git ls-files -z --others --exclude-standard",
		"bash -n --",
	)
}

func TestSetupOpsConfigHelperScript(t *testing.T) {
	path, content, err := setupHelperScript("ops-config")
	if err != nil {
		t.Fatalf("setup ops helper: %v", err)
	}
	if path != "scripts/readyci-check-ops-config.sh" {
		t.Fatalf("helper path = %q", path)
	}
	requireCLITestOutputContains(t, content,
		"PyYAML is required",
		"root_ops_files",
		"docker-compose*.yml",
		"systemd unit shape",
		"Caddyfile",
	)
}

func TestSetupOpsConfigHelperCoversRootCaddyfile(t *testing.T) {
	chdirCLITestRepo(t, "")
	_, content, err := setupHelperScript("ops-config")
	if err != nil {
		t.Fatalf("setup ops helper: %v", err)
	}
	if err := os.WriteFile("Caddyfile", []byte(":8080 {\n\trespond \"ok\"\n}\n"), 0o644); err != nil {
		t.Fatalf("write Caddyfile: %v", err)
	}
	helperPath := filepath.Join(".", "readyci-check-ops-config.sh")
	if err := os.WriteFile(helperPath, []byte(content), 0o755); err != nil {
		t.Fatalf("write helper: %v", err)
	}
	out, err := exec.Command("bash", helperPath).CombinedOutput()
	if err != nil {
		t.Fatalf("run helper: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "no common ops/config") {
		t.Fatalf("helper skipped root Caddyfile:\n%s", out)
	}
}

func TestSetupOpsConfigHelperSkipsCaddyLoopWhenNoCaddyfilesExist(t *testing.T) {
	chdirCLITestRepo(t, "")
	_, content, err := setupHelperScript("ops-config")
	if err != nil {
		t.Fatalf("setup ops helper: %v", err)
	}
	if err := os.MkdirAll("deploy/systemd", 0o755); err != nil {
		t.Fatalf("mkdir deploy/systemd: %v", err)
	}
	if err := os.WriteFile(filepath.Join("deploy", "systemd", "readyci.service"), []byte("[Unit]\nDescription=readyci test\n[Service]\nExecStart=/usr/bin/true\n[Install]\nWantedBy=multi-user.target\n"), 0o644); err != nil {
		t.Fatalf("write systemd unit: %v", err)
	}
	helperPath := filepath.Join(".", "readyci-check-ops-config.sh")
	if err := os.WriteFile(helperPath, []byte(content), 0o755); err != nil {
		t.Fatalf("write helper: %v", err)
	}
	out, err := exec.Command("bash", helperPath).CombinedOutput()
	if err != nil {
		t.Fatalf("run helper without Caddyfiles: %v\n%s", err, out)
	}
}

func TestSetupMakefileScaffoldIncludesWarmReleaseShape(t *testing.T) {
	content := setupMakefileScaffold("https://api.readyci.test", "airline")
	requireCLITestOutputContains(t, content,
		"READYCI_RUN_FLAGS ?=",
		"READYCI_WARM_FLAGS ?=",
		"$(READYCI) affected",
		"$(READYCI) affected --staged",
		"remote-warm",
		"$(READYCI) warm --profile dependencies $(READYCI_WARM_FLAGS)",
		"--network-mode default --runner-size large --timeout 90m",
		"RUN_DB",
		"$(READYCI) check $(READYCI_CHECK_FLAGS) db",
	)
	if strings.Contains(content, "db-serial") {
		t.Fatalf("makefile scaffold invoked db-serial instead of generated db check:\n%s", content)
	}
}

func TestSetupWritesMakefileAndHelpers(t *testing.T) {
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

	output, err := captureStdoutForCLITest(t, func() error {
		return setupCommand([]string{"--write-makefile", "--write-helpers", "--project", "airline", "--api-url", "https://api.readyci.test"})
	})
	if err != nil {
		t.Fatalf("setup write generated files: %v\n%s", err, output)
	}
	requireCLITestOutputContains(t, output,
		"wrote: readyci.mk",
		"wrote: scripts/readyci-check-shell-syntax.sh",
		"wrote: scripts/readyci-check-ops-config.sh",
	)
	makefile, err := os.ReadFile(filepath.Join(repo, "readyci.mk"))
	if err != nil {
		t.Fatalf("read readyci.mk: %v", err)
	}
	requireCLITestOutputContains(t, string(makefile), "READYCI_PROJECT_ID ?= airline", "remote-warm")
	info, err := os.Stat(filepath.Join(repo, "scripts", "readyci-check-shell-syntax.sh"))
	if err != nil {
		t.Fatalf("stat helper: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("helper mode = %v, want executable", info.Mode().Perm())
	}
}

func TestSetupWriteConfigAndHelpersWiresHelperChecks(t *testing.T) {
	t.Setenv("READYCI_CONFIG_DIR", t.TempDir())
	chdirCLITestRepo(t, "")
	if err := os.MkdirAll("scripts", 0o755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}
	if err := os.WriteFile(filepath.Join("scripts", "lint.sh"), []byte("#!/usr/bin/env bash\n"), 0o755); err != nil {
		t.Fatalf("write shell script: %v", err)
	}
	if err := os.MkdirAll("deploy", 0o755); err != nil {
		t.Fatalf("mkdir deploy: %v", err)
	}
	if err := os.WriteFile(filepath.Join("deploy", "compose.yml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatalf("write deploy file: %v", err)
	}

	report, err := buildSetupReport(setupOptions{
		ProjectID:    "airline",
		WriteConfig:  true,
		WriteHelpers: true,
		Detect:       true,
	})
	if err != nil {
		t.Fatalf("build setup report: %v", err)
	}
	for _, want := range []string{".readyci.yml", "scripts/readyci-check-shell-syntax.sh", "scripts/readyci-check-ops-config.sh"} {
		if !contains(report.Written, want) {
			t.Fatalf("written files = %v, want %s", report.Written, want)
		}
	}
	cfg, _, err := config.LoadProjectConfig(".")
	if err != nil {
		t.Fatalf("load generated config: %v", err)
	}
	if cfg.Checks["shell-syntax"].Command != "scripts/readyci-check-shell-syntax.sh" {
		t.Fatalf("shell-syntax check = %+v, want helper command", cfg.Checks["shell-syntax"])
	}
	if cfg.Checks["ops-config"].Command != "scripts/readyci-check-ops-config.sh" {
		t.Fatalf("ops-config check = %+v, want helper command", cfg.Checks["ops-config"])
	}
	if len(cfg.Affected.Profiles) != 0 {
		t.Fatalf("affected profiles = %v, want explicit rules only", cfg.Affected.Profiles)
	}
	for _, want := range []string{"shell-syntax", "ops-config"} {
		if !affectedRulesContainCheck(cfg.Affected.Rules, want) {
			t.Fatalf("affected rules = %+v, missing %s", cfg.Affected.Rules, want)
		}
	}
	if err := validateProjectConfig(cfg); err != nil {
		t.Fatalf("validate generated config: %v", err)
	}
}

func TestWriteSetupHelperScriptForceSetsExecutableAndRejectsSymlink(t *testing.T) {
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

	path := filepath.Join(repo, "scripts", "readyci-check-shell-syntax.sh")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir helper dir: %v", err)
	}
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatalf("write existing helper: %v", err)
	}
	if err := writeSetupHelperScript("scripts/readyci-check-shell-syntax.sh", "#!/usr/bin/env bash\n", true); err != nil {
		t.Fatalf("force write helper: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat helper: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("helper mode = %o, want 755", got)
	}

	symlinkPath := filepath.Join(repo, "scripts", "symlink-helper.sh")
	if err := os.Symlink(path, symlinkPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := writeSetupHelperScript("scripts/symlink-helper.sh", "#!/usr/bin/env bash\n", true); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink write error = %v, want symlink refusal", err)
	}

	outside := t.TempDir()
	parentSymlinkPath := filepath.Join(repo, "linked-scripts")
	if err := os.Symlink(outside, parentSymlinkPath); err != nil {
		t.Skipf("directory symlink unavailable: %v", err)
	}
	if err := writeSetupHelperScript("linked-scripts/helper.sh", "#!/usr/bin/env bash\n", true); err == nil || !strings.Contains(err.Error(), "symlinked parent") {
		t.Fatalf("symlinked parent write error = %v, want symlinked parent refusal", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "helper.sh")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside helper stat error = %v, want not created", err)
	}
}
