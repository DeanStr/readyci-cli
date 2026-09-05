package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

func buildRepoImportReport(source string, repo repoMapReport) repoImportReport {
	report := repoImportReport{Source: source, Mode: "preview", Candidates: []repoImportCandidate{}}
	for _, candidate := range inferRepoImportCandidates(repo) {
		if candidate.Source == source {
			report.Candidates = append(report.Candidates, candidate)
		}
	}
	for _, ref := range repo.CheckReferences {
		if source == "make" && ref.Source == "make" {
			report.References = append(report.References, ref)
		}
		if source == "github-actions" && strings.HasPrefix(ref.Source, "github-actions") {
			report.References = append(report.References, ref)
		}
	}
	for _, undefined := range repo.UndefinedChecks {
		for _, ref := range undefined.References {
			if source == "make" && ref.Source == "make" || source == "github-actions" && strings.HasPrefix(ref.Source, "github-actions") {
				report.Warnings = append(report.Warnings, "undefined check "+undefined.Check+" referenced from "+formatRefLocation(ref))
			}
		}
	}
	return report
}

func printRepoMapReport(report repoMapReport) {
	fmt.Println("repo_map: readyci")
	fmt.Printf("status: %s\n", report.Status)
	if report.ConfigPath != "" {
		fmt.Printf("config_path: %s\n", report.ConfigPath)
	}
	if len(report.Checks) > 0 {
		fmt.Println("checks:")
		for _, check := range report.Checks {
			fmt.Printf("  %s\n", check)
		}
	}
	if report.Cargo != nil {
		fmt.Println("cargo:")
		for _, pkg := range report.Cargo.Packages {
			fmt.Printf("  %s: check=%s path=%s\n", pkg.Name, pkg.Check, pkg.Path)
			if len(pkg.Dependents) > 0 {
				fmt.Printf("    dependents: %s\n", strings.Join(pkg.Dependents, ", "))
			}
		}
	}
	if len(report.NodePackages) > 0 {
		fmt.Println("node:")
		for _, node := range report.NodePackages {
			fmt.Printf("  %s: script=%s package_manager=%s\n", node.Path, node.PreferredScript, valueOrDash(node.PackageManager))
			if len(node.ContractScripts) > 0 {
				fmt.Printf("    contracts: %s\n", strings.Join(node.ContractScripts, ", "))
			}
		}
	}
	if len(report.UndefinedChecks) > 0 {
		fmt.Println("undefined_checks:")
		for _, undefined := range report.UndefinedChecks {
			fmt.Printf("  %s\n", undefined.Check)
			for _, ref := range undefined.References {
				fmt.Printf("    from: %s\n", formatRefLocation(ref))
			}
		}
	}
	if len(report.UndefinedMake) > 0 {
		fmt.Println("undefined_make_targets:")
		for _, undefined := range report.UndefinedMake {
			fmt.Printf("  %s\n", undefined.Target)
			for _, ref := range undefined.References {
				fmt.Printf("    from: %s\n", formatMakeRefLocation(ref))
			}
		}
	}
	if len(report.Unreferenced) > 0 {
		fmt.Println("unreferenced_checks:")
		for _, check := range report.Unreferenced {
			fmt.Printf("  %s\n", check)
		}
	}
}

func printRepoImportReport(report repoImportReport) {
	fmt.Printf("import: %s\n", report.Source)
	fmt.Println("mode: preview")
	fmt.Println("note: advisory only; no config was written")
	if len(report.Candidates) == 0 {
		fmt.Println("candidates: none")
	} else {
		fmt.Println("candidates:")
		for _, candidate := range report.Candidates {
			fmt.Printf("  %s", candidate.Name)
			if candidate.Check != "" {
				fmt.Printf(": check=%s", candidate.Check)
			}
			if candidate.Command != "" {
				fmt.Printf(": command=%s", candidate.Command)
			}
			fmt.Printf(" confidence=%.2f\n", candidate.Confidence)
			if candidate.Reason != "" {
				fmt.Printf("    reason: %s\n", candidate.Reason)
			}
		}
	}
	for _, warning := range report.Warnings {
		fmt.Printf("warning: %s\n", warning)
	}
}

func formatRefLocation(ref repoCheckReference) string {
	location := ref.Path
	if ref.Line > 0 {
		location += ":" + fmt.Sprint(ref.Line)
	}
	if ref.Context != "" {
		location += " (" + ref.Context + ")"
	}
	return location
}

func formatMakeRefLocation(ref repoMakeReference) string {
	location := ref.Path
	if ref.Line > 0 {
		location += ":" + fmt.Sprint(ref.Line)
	}
	if ref.Context != "" {
		location += " (" + ref.Context + ")"
	}
	return location
}

func repoMapSignalStrings(report repoMapReport) []string {
	var signals []string
	for _, signal := range report.Signals {
		text := signal.Kind
		if signal.Path != "" {
			text += " at " + signal.Path
		}
		if len(signal.Evidence) > 0 {
			text += ": " + strings.Join(signal.Evidence, ", ")
		}
		signals = append(signals, text)
	}
	return signals
}

func verificationSignalsWithRepoMap(cwd string, cfg config.ProjectConfig, configPath string, base []string, enabled bool) []string {
	out := append([]string(nil), base...)
	if !enabled {
		return out
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	repo := buildRepoMapReport(ctx, repoMapRoot(cwd, configPath), cfg, configPath)
	return uniqueStrings(append(out, repoMapSignalStrings(repo)...))
}
