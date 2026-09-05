package main

import (
	"strings"
	"testing"
)

func TestCargoAffectedReportUsesReverseDependents(t *testing.T) {
	metadata := []byte(`{
  "workspace_root": "/repo",
  "workspace_members": ["path+file:///repo/crates/core#readyci-core@0.1.0", "path+file:///repo/crates/app#readyci-app@0.1.0"],
  "packages": [
    {
      "id": "path+file:///repo/crates/core#readyci-core@0.1.0",
      "name": "readyci-core",
      "manifest_path": "/repo/crates/core/Cargo.toml",
      "dependencies": []
    },
    {
      "id": "path+file:///repo/crates/app#readyci-app@0.1.0",
      "name": "readyci-app",
      "manifest_path": "/repo/crates/app/Cargo.toml",
      "dependencies": [{"name": "readyci-core", "source": null, "path": "/repo/crates/core"}]
    }
  ]
}`)
	report, err := buildCargoAffectedReportFromMetadata(metadata, "rust-")
	if err != nil {
		t.Fatalf("cargo affected report: %v", err)
	}
	if len(report.Rules) != 3 {
		t.Fatalf("rules = %+v, want workspace plus two package rules", report.Rules)
	}
	if !contains(report.Rules[0].Paths, "Cargo.toml") || !contains(report.Rules[0].Paths, "Cargo.lock") {
		t.Fatalf("workspace rule paths = %v, want Cargo.toml and Cargo.lock", report.Rules[0].Paths)
	}
	if got := strings.Join(report.Rules[0].Checks, ","); got != "rust-readyci-app,rust-readyci-core" {
		t.Fatalf("workspace checks = %q, want all package checks", got)
	}
	var coreRule cargoAffectedRuleSuggestion
	for _, rule := range report.Rules {
		if len(rule.Paths) == 1 && rule.Paths[0] == "crates/core/**" {
			coreRule = rule
		}
	}
	if got := strings.Join(coreRule.Checks, ","); got != "rust-readyci-core,rust-readyci-app" {
		t.Fatalf("core checks = %q, want core and app checks", got)
	}
}

func TestCargoAffectedReportDisambiguatesSanitizedPackageNameCollisions(t *testing.T) {
	metadata := []byte(`{
  "workspace_root": "/repo",
  "workspace_members": ["path+file:///repo/crates/foo-bar#foo-bar@0.1.0", "path+file:///repo/crates/foo_bar#foo_bar@0.1.0"],
  "packages": [
    {
      "id": "path+file:///repo/crates/foo-bar#foo-bar@0.1.0",
      "name": "foo-bar",
      "manifest_path": "/repo/crates/foo-bar/Cargo.toml",
      "dependencies": []
    },
    {
      "id": "path+file:///repo/crates/foo_bar#foo_bar@0.1.0",
      "name": "foo_bar",
      "manifest_path": "/repo/crates/foo_bar/Cargo.toml",
      "dependencies": []
    }
  ]
}`)
	report, err := buildCargoAffectedReportFromMetadata(metadata, "rust-")
	if err != nil {
		t.Fatalf("cargo affected report: %v", err)
	}
	if len(report.Packages) != 2 {
		t.Fatalf("packages = %+v, want two packages", report.Packages)
	}
	checksByName := map[string]string{}
	for _, pkg := range report.Packages {
		checksByName[pkg.Name] = pkg.Check
	}
	if checksByName["foo-bar"] != "rust-foo-bar" || checksByName["foo_bar"] != "rust-foo-bar-2" {
		t.Fatalf("package checks = %+v, want disambiguated check names", checksByName)
	}
	for _, rule := range report.Rules {
		if contains(rule.Paths, "crates/foo-bar/**") && strings.Join(rule.Checks, ",") != "rust-foo-bar" {
			t.Fatalf("foo-bar rule checks = %v, want rust-foo-bar", rule.Checks)
		}
		if contains(rule.Paths, "crates/foo_bar/**") && strings.Join(rule.Checks, ",") != "rust-foo-bar-2" {
			t.Fatalf("foo_bar rule checks = %v, want rust-foo-bar-2", rule.Checks)
		}
	}
	if len(report.Warnings) == 0 || !strings.Contains(report.Warnings[0], "both map to check") {
		t.Fatalf("warnings = %v, want collision warning", report.Warnings)
	}
}

func TestCargoAffectedReportIncludesRootPackageBuildScript(t *testing.T) {
	metadata := []byte(`{
  "workspace_root": "/repo",
  "workspace_members": ["path+file:///repo#readyci-root@0.1.0"],
  "packages": [
    {
      "id": "path+file:///repo#readyci-root@0.1.0",
      "name": "readyci-root",
      "manifest_path": "/repo/Cargo.toml",
      "dependencies": []
    }
  ]
}`)
	report, err := buildCargoAffectedReportFromMetadata(metadata, "rust-")
	if err != nil {
		t.Fatalf("cargo affected report: %v", err)
	}
	var rootPackageRule cargoAffectedRuleSuggestion
	for _, rule := range report.Rules {
		if contains(rule.Paths, "build.rs") {
			rootPackageRule = rule
		}
	}
	if len(rootPackageRule.Paths) == 0 {
		t.Fatalf("rules = %+v, want root package rule with build.rs", report.Rules)
	}
	if contains(rootPackageRule.Paths, "Cargo.toml") || contains(rootPackageRule.Paths, "Cargo.lock") {
		t.Fatalf("root package rule paths = %v, want workspace files only in workspace-wide rule", rootPackageRule.Paths)
	}
	if got := strings.Join(rootPackageRule.Checks, ","); got != "rust-readyci-root" {
		t.Fatalf("root build.rs checks = %q, want root package check", got)
	}
}

func TestCargoAffectedReportPrefixesSubdirectoryWorkspaceRules(t *testing.T) {
	meta := cargoMetadata{
		WorkspaceRoot:    "/repo/backend",
		WorkspaceMembers: []string{"path+file:///repo/backend/crates/core#readyci-core@0.1.0"},
		Packages: []cargoMetadataPackage{
			{
				ID:           "path+file:///repo/backend/crates/core#readyci-core@0.1.0",
				Name:         "readyci-core",
				ManifestPath: "/repo/backend/crates/core/Cargo.toml",
			},
		},
	}
	report, err := buildCargoAffectedReportFromParsedMetadata(meta, "rust-", "backend")
	if err != nil {
		t.Fatalf("cargo affected report: %v", err)
	}
	if len(report.Rules) != 2 {
		t.Fatalf("rules = %+v, want workspace and package rules", report.Rules)
	}
	if !contains(report.Rules[0].Paths, "backend/Cargo.toml") || !contains(report.Rules[0].Paths, "backend/Cargo.lock") {
		t.Fatalf("workspace rule paths = %v, want backend-prefixed Cargo files", report.Rules[0].Paths)
	}
	if got := strings.Join(report.Rules[1].Paths, ","); got != "backend/crates/core/**" {
		t.Fatalf("package rule paths = %q, want backend/crates/core/**", got)
	}
	if len(report.Packages) != 1 || report.Packages[0].Path != "backend/crates/core" {
		t.Fatalf("packages = %+v, want git-root-relative package path", report.Packages)
	}
}
