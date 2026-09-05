package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

func affectedCargoCommand(args []string) error {
	fs := flag.NewFlagSet("affected cargo", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOutput := fs.Bool("json", false, "print machine-readable JSON")
	checkPrefix := fs.String("check-prefix", "rust-", "prefix for suggested package check names")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci affected cargo [--check-prefix PREFIX] [--json]", nil)
	}
	ctx, stop := signalContext()
	defer stop()
	report, err := buildCargoAffectedReport(ctx, ".", *checkPrefix)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSONOutput(report)
	}
	printCargoAffectedReport(report)
	return nil
}

type cargoMetadata struct {
	Packages         []cargoMetadataPackage `json:"packages"`
	WorkspaceMembers []string               `json:"workspace_members"`
	WorkspaceRoot    string                 `json:"workspace_root"`
}

type cargoMetadataPackage struct {
	ID           string                    `json:"id"`
	Name         string                    `json:"name"`
	ManifestPath string                    `json:"manifest_path"`
	Dependencies []cargoMetadataDependency `json:"dependencies"`
}

type cargoMetadataDependency struct {
	Name   string  `json:"name"`
	Source *string `json:"source"`
	Path   string  `json:"path"`
}

func buildCargoAffectedReport(ctx context.Context, cwd string, checkPrefix string) (cargoAffectedReport, error) {
	cmd := exec.CommandContext(ctx, "cargo", "metadata", "--format-version", "1", "--no-deps")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return cargoAffectedReport{}, fmt.Errorf("cargo metadata: %w: %s", err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return cargoAffectedReport{}, fmt.Errorf("cargo metadata: %w", err)
	}
	meta, err := parseCargoMetadata(out)
	if err != nil {
		return cargoAffectedReport{}, err
	}
	workspacePrefix, err := cargoWorkspaceGitPrefix(ctx, cwd, meta.WorkspaceRoot)
	if err != nil {
		return cargoAffectedReport{}, err
	}
	return buildCargoAffectedReportFromParsedMetadata(meta, checkPrefix, workspacePrefix)
}

func buildCargoAffectedReportFromMetadata(data []byte, checkPrefix string) (cargoAffectedReport, error) {
	meta, err := parseCargoMetadata(data)
	if err != nil {
		return cargoAffectedReport{}, err
	}
	return buildCargoAffectedReportFromParsedMetadata(meta, checkPrefix, "")
}

func parseCargoMetadata(data []byte) (cargoMetadata, error) {
	var meta cargoMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return cargoMetadata{}, fmt.Errorf("parse cargo metadata: %w", err)
	}
	if strings.TrimSpace(meta.WorkspaceRoot) == "" {
		return cargoMetadata{}, errors.New("cargo metadata did not include workspace_root")
	}
	return meta, nil
}

func cargoWorkspaceGitPrefix(ctx context.Context, cwd string, workspaceRoot string) (string, error) {
	gitRoot, err := gitOutput(ctx, "-C", cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("git rev-parse --show-toplevel: %w", err)
	}
	gitRootAbs, err := filepath.Abs(gitRoot)
	if err != nil {
		return "", err
	}
	workspaceAbs, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(gitRootAbs, workspaceAbs)
	if err != nil {
		return "", err
	}
	rel = filepath.Clean(rel)
	if rel == "." {
		return "", nil
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("cargo workspace root %s is outside git root %s", workspaceAbs, gitRootAbs)
	}
	return normalizeAffectedPath(rel), nil
}

func buildCargoAffectedReportFromParsedMetadata(meta cargoMetadata, checkPrefix string, workspacePrefix string) (cargoAffectedReport, error) {
	workspaceMembers := make(map[string]bool)
	for _, member := range meta.WorkspaceMembers {
		workspaceMembers[member] = true
	}
	if len(workspaceMembers) == 0 {
		for _, pkg := range meta.Packages {
			workspaceMembers[pkg.ID] = true
		}
	}
	byName := make(map[string]cargoMetadataPackage)
	byDir := make(map[string]string)
	for _, pkg := range meta.Packages {
		if workspaceMembers[pkg.ID] {
			byName[pkg.Name] = pkg
			byDir[filepath.Clean(filepath.Dir(pkg.ManifestPath))] = pkg.Name
		}
	}
	if len(byName) == 0 {
		return cargoAffectedReport{}, errors.New("cargo metadata did not include workspace packages")
	}

	checkPrefix = strings.TrimSpace(checkPrefix)
	dependents := make(map[string][]string)
	for _, pkg := range byName {
		for _, dep := range pkg.Dependencies {
			if dep.Source != nil && strings.TrimSpace(*dep.Source) != "" {
				continue
			}
			depName := dep.Name
			if strings.TrimSpace(dep.Path) != "" {
				if pathName := byDir[filepath.Clean(dep.Path)]; pathName != "" {
					depName = pathName
				}
			}
			if _, ok := byName[depName]; !ok {
				continue
			}
			dependents[depName] = appendUniqueString(dependents[depName], pkg.Name)
		}
	}

	packageNames := make([]string, 0, len(byName))
	for name := range byName {
		packageNames = append(packageNames, name)
	}
	sort.Strings(packageNames)

	report := cargoAffectedReport{
		WorkspaceRoot: meta.WorkspaceRoot,
		CheckPrefix:   checkPrefix,
	}
	packageChecks := make(map[string]string, len(packageNames))
	usedCheckNames := make(map[string]bool, len(packageNames))
	seenBaseChecks := make(map[string]string, len(packageNames))
	for _, name := range packageNames {
		baseCheck := checkPrefix + sanitizeCheckName(name)
		check := uniqueCheckName(baseCheck, usedCheckNames)
		if previous := seenBaseChecks[baseCheck]; previous != "" && previous != name {
			report.Warnings = append(report.Warnings, fmt.Sprintf("packages %q and %q both map to check %q; generated %q for %q", previous, name, baseCheck, check, name))
		}
		seenBaseChecks[baseCheck] = name
		packageChecks[name] = check
	}
	var workspaceChecks []string
	for _, name := range packageNames {
		workspaceChecks = appendUniqueString(workspaceChecks, packageChecks[name])
	}
	if len(workspaceChecks) > 0 {
		report.Rules = append(report.Rules, cargoAffectedRuleSuggestion{
			Paths:  prefixCargoAffectedPaths(workspacePrefix, cargoWorkspaceAffectedPaths()),
			Checks: workspaceChecks,
		})
	}
	for _, name := range packageNames {
		pkg := byName[name]
		check := packageChecks[name]
		rel, err := filepath.Rel(meta.WorkspaceRoot, filepath.Dir(pkg.ManifestPath))
		if err != nil {
			return cargoAffectedReport{}, fmt.Errorf("package %s path: %w", name, err)
		}
		rel = normalizeAffectedPath(rel)
		if rel == "." {
			rel = ""
		}
		displayPath := prefixCargoAffectedPath(workspacePrefix, rel)
		if displayPath == "" {
			displayPath = "."
		}
		downstream := transitiveDependents(name, dependents)
		checks := []string{check}
		for _, dependent := range downstream {
			if dependentCheck := packageChecks[dependent]; dependentCheck != "" {
				checks = appendUniqueString(checks, dependentCheck)
			}
		}
		rulePaths := cargoPackageAffectedPaths(workspacePrefix, rel)
		report.Packages = append(report.Packages, cargoAffectedPackage{
			Name:       name,
			Check:      check,
			Path:       displayPath,
			Dependents: downstream,
		})
		report.Rules = append(report.Rules, cargoAffectedRuleSuggestion{
			Paths:  rulePaths,
			Checks: checks,
		})
	}
	sort.Strings(report.Warnings)
	return report, nil
}

func cargoWorkspaceAffectedPaths() []string {
	return []string{"Cargo.toml", "Cargo.lock", "rust-toolchain", "rust-toolchain.toml", ".cargo/**"}
}

func cargoPackageAffectedPaths(workspacePrefix string, rel string) []string {
	if rel == "" || rel == "." {
		return prefixCargoAffectedPaths(workspacePrefix, []string{"build.rs", "src/**", "tests/**", "benches/**", "examples/**"})
	}
	return []string{prefixCargoAffectedPath(workspacePrefix, rel) + "/**"}
}

func prefixCargoAffectedPaths(prefix string, paths []string) []string {
	prefixed := make([]string, 0, len(paths))
	for _, item := range paths {
		prefixed = append(prefixed, prefixCargoAffectedPath(prefix, item))
	}
	return prefixed
}

func prefixCargoAffectedPath(prefix string, item string) string {
	prefix = normalizeAffectedPath(prefix)
	if prefix == "." {
		prefix = ""
	}
	item = normalizeAffectedPath(item)
	if item == "." {
		item = ""
	}
	if prefix == "" {
		return item
	}
	if item == "" || item == "." {
		return prefix
	}
	return prefix + "/" + item
}

func transitiveDependents(name string, dependents map[string][]string) []string {
	seen := make(map[string]bool)
	var out []string
	var visit func(string)
	visit = func(current string) {
		next := append([]string(nil), dependents[current]...)
		sort.Strings(next)
		for _, dependent := range next {
			if seen[dependent] {
				continue
			}
			seen[dependent] = true
			out = append(out, dependent)
			visit(dependent)
		}
	}
	visit(name)
	return out
}
