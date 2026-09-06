package main

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

func probeDoctorGit(ctx context.Context, report *doctorReport) {
	if _, err := exec.LookPath("git"); err != nil {
		report.warn("git", "git is not on PATH", "install git and run doctor from a repository checkout")
	} else if _, err := gitOutput(ctx, "status", "--porcelain=v1"); err != nil {
		report.warn("git", fmt.Sprintf("current directory is not a git workspace: %v", err), "run readyci doctor from the project repository root")
	} else {
		report.ok("git", "git workspace detected")
	}

}

func probeDoctorRepository(ctx context.Context, cfg config.ProjectConfig, path string, report *doctorReport) {
	repoCfg := cfg
	repoPath := path
	if path == "" {
		if loaded, loadErr := loadRepoMapConfig("."); loadErr == nil {
			repoCfg = loaded.Config
			repoPath = loaded.Path
		} else {
			report.warn("repo", "repo map config inference failed: "+loadErr.Error(), "run readyci plan --repo-map --json")
		}
	}
	repoRoot := repoMapRoot(".", repoPath)
	repo := buildRepoMapReport(ctx, repoRoot, repoCfg, repoPath)
	harness := buildRepoHarnessReport(repoRoot, repoCfg)
	report.Repo = &repoDoctorReport{
		Status:          mergeDoctorStatuses(repo.Status, harness.Status),
		UndefinedChecks: append([]repoUndefinedCheck(nil), repo.UndefinedChecks...),
		UndefinedMake:   append([]repoUndefinedMake(nil), repo.UndefinedMake...),
		Unreferenced:    append([]string(nil), repo.Unreferenced...),
		Harness:         &harness,
	}
	if path == "" {
		details := []string{"inferred repository map from local project signals"}
		if repoPath == inferredConfigPath {
			details = append(details, "using inferred Ready CI config for context")
		}
		report.addDiagnostic("repo", "warn", "no committed Ready CI config to validate yet", details, []string{"run readyci start --write-config", "run readyci plan --repo-map --json"})
		report.Summary.Warnings++
	} else if len(repo.UndefinedChecks) == 0 {
		report.ok("repo", "cross-file Ready CI references are consistent")
	} else {
		var details []string
		for _, undefined := range repo.UndefinedChecks {
			for _, ref := range undefined.References {
				details = append(details, undefined.Check+" referenced from "+formatRefLocation(ref))
			}
		}
		report.failWithDetails("repo", "cross-file Ready CI references include undefined checks", details, []string{"run readyci plan --repo-map --json", "add the missing check or update the Make/docs/GitHub Actions reference"})
	}
	if len(repo.UndefinedMake) > 0 {
		var details []string
		for _, undefined := range repo.UndefinedMake {
			for _, ref := range undefined.References {
				details = append(details, undefined.Target+" referenced from "+formatMakeRefLocation(ref))
			}
		}
		report.addDiagnostic("repo", "warn", "docs or AGENTS references include missing Make targets", details, []string{"run readyci plan --repo-map --json", "add the missing Make target or update the docs"})
		report.Summary.Warnings++
	}
	if len(repo.Unreferenced) > 0 {
		report.warn("repo", fmt.Sprintf("%d Ready CI check(s) are not referenced by affected rules, Make, docs, or GitHub Actions", len(repo.Unreferenced)), "run readyci plan --repo-map to inspect unreferenced checks")
	}
	addHarnessDiagnostics(report, harness)
	report.Next = doctorNextCommands(*report)
}

func probeDoctorHealth(ctx context.Context, apiURL string, report *doctorReport) {
	healthURL := strings.TrimRight(apiURL, "/") + "/healthz"
	healthCtx, cancelHealth := context.WithTimeout(ctx, doctorHealthTimeout)
	defer cancelHealth()
	req, err := http.NewRequestWithContext(healthCtx, http.MethodGet, healthURL, nil)
	if err != nil {
		report.fail("api", fmt.Sprintf("api health request could not be created: %v", err))
	} else if resp, err := doctorHealthClient().Do(req); err != nil {
		report.fail("api", fmt.Sprintf("api health failed: %v", err))
	} else {
		_ = resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			report.fail("api", "api health returned "+resp.Status)
		} else {
			report.ok("api", "api healthy: "+healthURL)
		}
	}

}
