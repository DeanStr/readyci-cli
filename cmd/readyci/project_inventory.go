package main

import (
	"path/filepath"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func projectAnalysisInventoryFromRepoMap(report repoMapReport) preflightapi.ProjectAnalysisInventory {
	inventory := preflightapi.ProjectAnalysisInventory{
		SchemaVersion: "readyci.project_inventory.v1",
		ProjectID:     report.ProjectID,
		ConfigPath:    redactedProjectAnalysisConfigPath(report.ConfigPath),
		Checks:        append([]string(nil), report.Checks...),
	}
	for _, signal := range report.Signals {
		inventory.Signals = append(inventory.Signals, preflightapi.ProjectAnalysisSignal{
			Kind:       signal.Kind,
			Path:       signal.Path,
			Confidence: signal.Confidence,
			Evidence:   append([]string(nil), signal.Evidence...),
		})
	}
	if report.Cargo != nil {
		for _, pkg := range report.Cargo.Packages {
			inventory.Packages = append(inventory.Packages, preflightapi.ProjectAnalysisPackage{
				Path:      pkg.Path,
				Ecosystem: "rust",
				Features:  []string{"cargo-workspace"},
			})
		}
	}
	for _, module := range report.GoModules {
		inventory.Packages = append(inventory.Packages, preflightapi.ProjectAnalysisPackage{
			Path:      module.Path,
			Ecosystem: "go",
			Features:  []string{"go-module"},
		})
	}
	for _, pkg := range report.NodePackages {
		var features []string
		if pkg.HasVite {
			features = append(features, "vite")
		}
		if pkg.HasReact {
			features = append(features, "react")
		}
		if pkg.HasPlaywright {
			features = append(features, "playwright")
		}
		inventory.Packages = append(inventory.Packages, preflightapi.ProjectAnalysisPackage{
			Path:           pkg.Path,
			Ecosystem:      "node",
			PackageManager: pkg.PackageManager,
			Lockfile:       pkg.Lockfile,
			Scripts:        append([]string(nil), pkg.Scripts...),
			Features:       features,
		})
	}
	if report.OpenAPI != nil {
		for _, source := range report.OpenAPI {
			inventory.Files = append(inventory.Files, preflightapi.ProjectAnalysisFileFact{Path: source.Source, Kind: "openapi"})
		}
	}
	for _, opsPath := range report.OpsPaths {
		inventory.Files = append(inventory.Files, preflightapi.ProjectAnalysisFileFact{Path: opsPath, Kind: "ops"})
	}
	for _, shellPath := range report.ShellPaths {
		inventory.Files = append(inventory.Files, preflightapi.ProjectAnalysisFileFact{Path: shellPath, Kind: "shell"})
	}
	return inventory
}

func redactedProjectAnalysisConfigPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || strings.HasPrefix(path, "<") {
		return path
	}
	path = filepath.ToSlash(path)
	if !filepath.IsAbs(path) {
		return path
	}
	return filepath.Base(path)
}
