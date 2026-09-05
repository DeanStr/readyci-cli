package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildCargoAffectedReportUsesCargoMetadataAndGitPrefix(t *testing.T) {
	repo := t.TempDir()
	workspace := filepath.Join(repo, "rust")
	if err := os.MkdirAll(filepath.Join(workspace, "core", "src"), 0o755); err != nil {
		t.Fatalf("create core dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workspace, "api", "src"), 0o755); err != nil {
		t.Fatalf("create api dir: %v", err)
	}
	runGitForCargoTest(t, repo, "init")
	runGitForCargoTest(t, repo, "config", "user.email", "dev@example.test")
	runGitForCargoTest(t, repo, "config", "user.name", "Dev")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("repo"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	runGitForCargoTest(t, repo, "add", ".")
	runGitForCargoTest(t, repo, "commit", "-m", "init")

	fakeBin := t.TempDir()
	writeFakeCargo(t, fakeBin, workspace)
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	report, err := buildCargoAffectedReport(context.Background(), workspace, "rust-")
	if err != nil {
		t.Fatalf("build cargo affected report: %v", err)
	}
	if report.WorkspaceRoot != workspace {
		t.Fatalf("workspace root = %q, want %q", report.WorkspaceRoot, workspace)
	}
	if len(report.Packages) != 2 {
		t.Fatalf("packages = %#v, want 2 packages", report.Packages)
	}
	core := findCargoPackage(t, report, "core-lib")
	if core.Check != "rust-core-lib" {
		t.Fatalf("core check = %q, want rust-core-lib", core.Check)
	}
	if core.Path != "rust/core" {
		t.Fatalf("core path = %q, want rust/core", core.Path)
	}
	if strings.Join(core.Dependents, ",") != "api" {
		t.Fatalf("core dependents = %#v, want api", core.Dependents)
	}
	if !cargoReportHasRule(report, []string{"rust/core/**"}, []string{"rust-core-lib", "rust-api"}) {
		t.Fatalf("core rule with dependent check missing from %#v", report.Rules)
	}
	if !cargoReportHasRule(report, []string{"rust/Cargo.toml", "rust/Cargo.lock"}, []string{"rust-api", "rust-core-lib"}) {
		t.Fatalf("workspace rule with git prefix missing from %#v", report.Rules)
	}
}

func TestAffectedCargoCommandPrintsJSONReport(t *testing.T) {
	repo := t.TempDir()
	workspace := filepath.Join(repo, "rust")
	if err := os.MkdirAll(filepath.Join(workspace, "api", "src"), 0o755); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	runGitForCargoTest(t, repo, "init")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("repo"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	runGitForCargoTest(t, repo, "add", ".")

	fakeBin := t.TempDir()
	writeFakeCargo(t, fakeBin, workspace)
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Chdir(workspace)

	output, err := captureStdoutForCLITest(t, func() error {
		return affectedCargoCommand([]string{"--check-prefix", "ci-", "--json"})
	})
	if err != nil {
		t.Fatalf("affected cargo command: %v", err)
	}
	var report cargoAffectedReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("parse output JSON: %v\n%s", err, output)
	}
	if report.CheckPrefix != "ci-" {
		t.Fatalf("check prefix = %q, want ci-", report.CheckPrefix)
	}
	if !cargoReportHasRule(report, []string{"rust/api/**"}, []string{"ci-api"}) {
		t.Fatalf("api rule missing from %#v", report.Rules)
	}
}

func writeFakeCargo(t *testing.T, dir string, workspace string) {
	t.Helper()
	coreManifest := filepath.ToSlash(filepath.Join(workspace, "core", "Cargo.toml"))
	apiManifest := filepath.ToSlash(filepath.Join(workspace, "api", "Cargo.toml"))
	coreDir := filepath.ToSlash(filepath.Join(workspace, "core"))
	workspaceRoot := filepath.ToSlash(workspace)
	script := `#!/bin/sh
if [ "$1" != "metadata" ]; then
  echo "unexpected cargo command" >&2
  exit 2
fi
cat <<'JSON'
{
  "workspace_root": "` + workspaceRoot + `",
  "workspace_members": ["core 0.1.0 (path+file:///core)", "api 0.1.0 (path+file:///api)"],
  "packages": [
    {
      "id": "core 0.1.0 (path+file:///core)",
      "name": "core-lib",
      "manifest_path": "` + coreManifest + `",
      "dependencies": []
    },
    {
      "id": "api 0.1.0 (path+file:///api)",
      "name": "api",
      "manifest_path": "` + apiManifest + `",
      "dependencies": [
        {"name": "core-lib", "source": null, "path": "` + coreDir + `"}
      ]
    }
  ]
}
JSON
`
	if err := os.WriteFile(filepath.Join(dir, "cargo"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake cargo: %v", err)
	}
}

func runGitForCargoTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func findCargoPackage(t *testing.T, report cargoAffectedReport, name string) cargoAffectedPackage {
	t.Helper()
	for _, pkg := range report.Packages {
		if pkg.Name == name {
			return pkg
		}
	}
	t.Fatalf("package %q not found in %#v", name, report.Packages)
	return cargoAffectedPackage{}
}

func cargoReportHasRule(report cargoAffectedReport, paths []string, checks []string) bool {
	for _, rule := range report.Rules {
		if stringSlicesContainAll(rule.Paths, paths) && stringSlicesContainAll(rule.Checks, checks) {
			return true
		}
	}
	return false
}

func stringSlicesContainAll(values []string, wants []string) bool {
	present := make(map[string]bool, len(values))
	for _, value := range values {
		present[value] = true
	}
	for _, want := range wants {
		if !present[want] {
			return false
		}
	}
	return true
}
