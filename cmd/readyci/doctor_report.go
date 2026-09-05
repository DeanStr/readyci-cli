package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

type doctorOptions struct {
	APIURL     string
	APIToken   string
	AdminToken string
	ProjectID  string
	CheckName  string
	Repo       bool
}

type doctorReport struct {
	APIURL           string                  `json:"api_url"`
	ProjectID        string                  `json:"project_id"`
	ProjectIDSource  string                  `json:"project_id_source"`
	Check            string                  `json:"check,omitempty"`
	Diagnostics      []doctorDiagnostic      `json:"diagnostics"`
	Repo             *repoDoctorReport       `json:"repo,omitempty"`
	WorkerCapability *workerCapabilityReport `json:"worker_capability,omitempty"`
	Next             []string                `json:"next,omitempty"`
	Summary          doctorSummary           `json:"summary"`
}

type doctorDiagnostic struct {
	Section string   `json:"section"`
	Level   string   `json:"level"`
	Message string   `json:"message"`
	Details []string `json:"details,omitempty"`
	Hints   []string `json:"hints,omitempty"`
}

type doctorSummary struct {
	Failures int `json:"failures"`
	Warnings int `json:"warnings"`
}

type repoDoctorReport struct {
	Status          string               `json:"status"`
	UndefinedChecks []repoUndefinedCheck `json:"undefined_checks,omitempty"`
	UndefinedMake   []repoUndefinedMake  `json:"undefined_make_targets,omitempty"`
	Unreferenced    []string             `json:"unreferenced_checks,omitempty"`
	Harness         *repoHarnessReport   `json:"harness,omitempty"`
}

func buildDoctorReport(ctx context.Context, options doctorOptions) doctorReport {
	apiURL := normalizeAPIURL(options.APIURL)
	projectID := strings.TrimSpace(options.ProjectID)
	checkName := strings.TrimSpace(options.CheckName)
	projectIDSource := "provided"
	if projectID == "" {
		projectIDSource = "default"
	}
	report := doctorReport{
		APIURL:          apiURL,
		ProjectID:       projectID,
		ProjectIDSource: projectIDSource,
		Check:           checkName,
	}

	if _, err := exec.LookPath("git"); err != nil {
		report.warn("git", "git is not on PATH", "install git and run doctor from a repository checkout")
	} else if _, err := gitOutput(ctx, "status", "--porcelain=v1"); err != nil {
		report.warn("git", fmt.Sprintf("current directory is not a git workspace: %v", err), "run readyci doctor from the project repository root")
	} else {
		report.ok("git", "git workspace detected")
	}

	var cfg config.ProjectConfig
	var path string
	var selectedPlan *checkRunPlan
	cfg, path, err := config.LoadProjectConfig(".")
	if err != nil {
		report.fail("config", fmt.Sprintf("config could not be loaded: %v", err), "run readyci config validate after fixing the YAML")
	} else if path == "" {
		report.warn("config", "no .readyci.yml found; defaults will be used", "run readyci setup --write-config --detect to create a project config", "run readyci init --detect --print to inspect generated config without writing it")
	} else if err := validateProjectConfigForConfigPath(cfg, path); err != nil {
		report.fail("config", fmt.Sprintf("config invalid: %v", err), "run readyci config explain to see the resolved config")
	} else {
		report.ok("config", "config valid: "+path)
		if affectedAuditConfigured(cfg) {
			if audit, err := buildAffectedAuditReport(".", cfg, path, true, false, true); err != nil {
				report.warn("affected", "affected audit could not run: "+err.Error(), "run readyci affected audit --strict --duplicates")
			} else if audit.Status == "ok" {
				report.ok("affected", fmt.Sprintf("affected audit ok: %d rule(s), %d ignored file(s)", audit.Rules, len(audit.IgnoredFiles)))
			} else {
				var details []string
				if len(audit.UnmatchedFiles) > 0 {
					details = append(details, fmt.Sprintf("%d unmatched source-like file(s)", len(audit.UnmatchedFiles)))
				}
				if len(audit.DuplicateSelections) > 0 {
					details = append(details, fmt.Sprintf("%d duplicate affected selection(s)", len(audit.DuplicateSelections)))
				}
				report.addDiagnostic("affected", "warn", "affected audit needs attention", details, []string{"run readyci affected audit --strict --duplicates", "run readyci affected suggest to generate profile/rule suggestions"})
				report.Summary.Warnings++
			}
		}
		if projectID == "" {
			projectID = cfg.ProjectID
			if projectID != "" {
				projectIDSource = "config"
			}
		}
	}
	if checkName != "" {
		if path == "" {
			report.fail("check", "--check requires .readyci.yml or .readyci.yaml")
		} else {
			plan, err := resolveCheckRunPlan(checkName, cfg, checkResolveOptions{
				ProjectID:  projectID,
				RetryCount: -1,
			})
			if err != nil {
				report.fail("check", fmt.Sprintf("check %s could not be resolved: %v", checkName, err))
			} else {
				selectedPlan = &plan
			}
		}
	}
	if projectID == "" {
		projectID = "proj_dev"
		projectIDSource = "default"
	}
	if selectedPlan != nil && strings.TrimSpace(selectedPlan.Options.ProjectID) == "" {
		selectedPlan.Options.ProjectID = projectID
	}
	report.ProjectID = projectID
	report.ProjectIDSource = projectIDSource
	if options.Repo {
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
		addHarnessDiagnostics(&report, harness)
		report.Next = doctorNextCommands(report)
		return report
	}

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

	projectHomeRegion := ""
	token, err := effectiveAPIToken(options.APIURL, options.APIToken)
	if err != nil {
		report.fail("auth", fmt.Sprintf("auth token could not be loaded: %v", err), "run readyci login"+apiURLFlag(apiURL)+" or set READYCI_API_TOKEN")
	} else if token == "" {
		report.warn("auth", "no API token found; run readyci login or set READYCI_API_TOKEN", "for agents, prefer a scoped project token in READYCI_API_TOKEN")
	} else {
		client := api.NewClient(options.APIURL, api.WithToken(token))
		usage, err := client.AccountUsage(ctx)
		if err != nil {
			report.fail("auth", fmt.Sprintf("account API check failed: %v", err), "verify the token has account:read, or run readyci auth status")
		} else {
			report.ok("auth", fmt.Sprintf("authenticated as team %s on plan %s", usage.TeamID, usage.Plan.ID))
		}
		if settings, err := client.ProjectSettings(ctx, projectID); err != nil {
			report.warn("project", fmt.Sprintf("project settings unavailable for %s: %v", projectID, err), "run readyci project set --project "+shellQuoteArg(projectID)+" --github-repo OWNER/REPO")
		} else {
			projectHomeRegion = preflightapi.NormalizeRegion(settings.HomeRegion)
			report.ok("project", fmt.Sprintf("project %s configured (github=%s, secrets=%d)", settings.ProjectID, valueOrDash(settings.GitHubRepository), len(settings.Secrets)))
		}
	}
	if selectedPlan != nil {
		if selectedPlan.RegionDefaulted && projectHomeRegion != "" {
			selectedPlan.DiagnosticRegion = projectHomeRegion
		}
		report.ok("check", fmt.Sprintf("check %s resolves to image=%s region=%s size=%s network=%s docker=%t",
			selectedPlan.Name,
			defaultString(selectedPlan.Options.Image, preflightapi.DefaultImage),
			checkPlanDiagnosticRegion(*selectedPlan),
			defaultString(selectedPlan.Options.RunnerSize, "standard"),
			selectedPlan.Options.NetworkMode,
			selectedPlan.Options.Docker,
		))
	}

	if strings.TrimSpace(options.AdminToken) != "" {
		resp, err := api.NewClient(options.APIURL, api.WithToken(options.AdminToken)).AdminListWorkers(ctx)
		if err != nil {
			report.warn("workers", fmt.Sprintf("worker inventory unavailable: %v", err), "provide an admin token if you want worker capability checks")
		} else if len(resp.Workers) == 0 {
			report.warn("workers", "no workers registered", "start readyci-worker and rerun readyci doctor --check "+shellQuoteArg(valueOrDash(checkName)))
		} else {
			report.ok("workers", fmt.Sprintf("%d worker(s) registered", len(resp.Workers)))
			if selectedPlan != nil {
				capability := checkWorkerCapability(*selectedPlan, resp.Workers)
				report.WorkerCapability = &capability
				if capability.UploadCapable {
					report.ok("workers", fmt.Sprintf("check %s has a fresh upload-capable worker: %s", selectedPlan.Name, strings.Join(capability.MatchingUploadWorkers, ",")))
				} else if capability.Claimable {
					report.failWithDetails("workers", fmt.Sprintf("check %s is claimable, but no matching worker can receive a source bundle upload", selectedPlan.Name), capability.UploadReasons, []string{"configure worker-local bundle upload or start a fresh worker that advertises an upload URL and the required capabilities"})
				} else {
					report.failWithDetails("workers", fmt.Sprintf("check %s is not claimable by registered workers", selectedPlan.Name), capability.Reasons, []string{"adjust the check region/image/network/docker settings or start a fresh worker that advertises the required capabilities"})
				}
			}
		}
	} else if selectedPlan != nil {
		if capability, err := publicWorkerCapability(ctx, options.APIURL, token, *selectedPlan); err == nil {
			report.WorkerCapability = &capability
			if capability.UploadCapable {
				report.ok("workers", fmt.Sprintf("check %s has %d matching upload-capable worker(s)", selectedPlan.Name, capability.MatchingUploadWorkerCount))
			} else if capability.Claimable {
				report.failWithDetails("workers", fmt.Sprintf("check %s is claimable, but no matching worker can receive a source bundle upload", selectedPlan.Name), capability.UploadReasons, []string{"configure worker-local bundle upload or start a fresh worker that advertises an upload URL and the required capabilities"})
			} else {
				report.failWithDetails("workers", fmt.Sprintf("check %s is not claimable by registered workers", selectedPlan.Name), capability.Reasons, []string{"adjust the check region/image/network/docker settings or start a fresh worker that advertises the required capabilities"})
			}
		} else {
			report.warn("workers", "worker capability check skipped because no admin token was provided and public check failed: "+err.Error(), "rerun with --admin-token TOKEN if this API version does not expose public worker capability checks")
		}
	}
	report.Next = doctorNextCommands(report)
	return report
}

func mergeDoctorStatuses(statuses ...string) string {
	merged := "ok"
	for _, status := range statuses {
		switch status {
		case "fail":
			return "fail"
		case "warn":
			merged = "warn"
		}
	}
	return merged
}

func (report *doctorReport) ok(section, message string) {
	report.addDiagnostic(section, "ok", message, nil, nil)
}

func (report *doctorReport) warn(section, message string, hints ...string) {
	report.addDiagnostic(section, "warn", message, nil, hints)
	report.Summary.Warnings++
}

func (report *doctorReport) fail(section, message string, hints ...string) {
	report.addDiagnostic(section, "fail", message, nil, hints)
	report.Summary.Failures++
}

func (report *doctorReport) failWithDetails(section, message string, details, hints []string) {
	report.addDiagnostic(section, "fail", message, details, hints)
	report.Summary.Failures++
}

func (report *doctorReport) addDiagnostic(section, level, message string, details, hints []string) {
	report.Diagnostics = append(report.Diagnostics, doctorDiagnostic{
		Section: section,
		Level:   level,
		Message: message,
		Details: append([]string(nil), details...),
		Hints:   append([]string(nil), hints...),
	})
}

func printDoctorReport(report doctorReport) {
	fmt.Println("doctor: readyci")
	fmt.Printf("api_url: %s\n", report.APIURL)
	fmt.Printf("project_id: %s\n", report.ProjectID)
	fmt.Printf("project_id_source: %s\n", report.ProjectIDSource)
	if report.Check != "" {
		fmt.Printf("check: %s\n", report.Check)
	}
	printed := map[string]bool{}
	for _, section := range []string{"git", "config", "affected", "check", "repo", "harness", "api", "auth", "project", "workers"} {
		if printDoctorSection(report.Diagnostics, section) {
			printed[section] = true
		}
	}
	for _, diagnostic := range report.Diagnostics {
		if printed[diagnostic.Section] {
			continue
		}
		if printDoctorSection(report.Diagnostics, diagnostic.Section) {
			printed[diagnostic.Section] = true
		}
	}
	if len(report.Next) > 0 {
		fmt.Println("next:")
		for _, command := range report.Next {
			fmt.Printf("  %s\n", command)
		}
	}
	fmt.Printf("summary: %d failure(s), %d warning(s)\n", report.Summary.Failures, report.Summary.Warnings)
}

func printDoctorSection(diagnostics []doctorDiagnostic, section string) bool {
	var sectionDiagnostics []doctorDiagnostic
	for _, diagnostic := range diagnostics {
		if diagnostic.Section == section {
			sectionDiagnostics = append(sectionDiagnostics, diagnostic)
		}
	}
	if len(sectionDiagnostics) == 0 {
		return false
	}
	fmt.Printf("%s:\n", section)
	for _, diagnostic := range sectionDiagnostics {
		fmt.Printf("  %s: %s\n", diagnostic.Level, diagnostic.Message)
		for _, detail := range diagnostic.Details {
			fmt.Printf("  detail: %s\n", detail)
		}
		for _, hint := range diagnostic.Hints {
			fmt.Printf("  hint: %s\n", hint)
		}
	}
	return true
}

func doctorNextCommands(report doctorReport) []string {
	var commands []string
	apiFlag := apiURLFlag(report.APIURL)
	if reportHasDiagnostic(report, "config", "warn", "no .readyci.yml") {
		commands = append(commands, doctorSetupCommand(report))
		commands = append(commands, "readyci init --detect --print")
	}
	if reportHasLevel(report, "config", "fail") {
		commands = append(commands, "readyci config validate")
	}
	if reportHasLevel(report, "auth", "warn") || reportHasLevel(report, "auth", "fail") {
		commands = append(commands, "readyci login"+apiFlag)
		commands = append(commands, readyCIAPIEnvCommand(report.APIURL, "<token>"))
	}
	if report.Check != "" {
		commands = append(commands, "readyci check --dry-run "+shellQuoteArg(report.Check))
		if reportHasDiagnostic(report, "workers", "warn", "skipped") {
			commands = append(commands, "readyci doctor"+apiFlag+" --project "+shellQuoteArg(report.ProjectID)+" --check "+shellQuoteArg(report.Check)+" --admin-token "+shellQuoteArg("<admin-token>"))
		}
		if report.WorkerCapability != nil && !report.WorkerCapability.UploadCapable {
			commands = append(commands, "readyci admin workers"+apiFlag+" --token "+shellQuoteArg("<admin-token>"))
		}
		if report.Summary.Failures == 0 && report.Summary.Warnings == 0 {
			commands = append(commands, "readyci check"+apiFlag+" --project "+shellQuoteArg(report.ProjectID)+" --open "+shellQuoteArg(report.Check))
		}
	} else if report.Summary.Failures == 0 && report.Summary.Warnings == 0 {
		commands = append(commands, "readyci run"+apiFlag+" --project "+shellQuoteArg(report.ProjectID)+" "+shellQuoteArg(suggestedDoctorRunCommand(".")))
	}
	return uniqueStrings(commands)
}

func suggestedDoctorRunCommand(cwd string) string {
	root, err := inferenceRoot(cwd)
	if err != nil || strings.TrimSpace(root) == "" {
		root = cwd
	}
	if fileExists(filepath.Join(root, "Cargo.toml")) {
		return "cargo test --workspace"
	}
	if fileExists(filepath.Join(root, "package.json")) {
		if command := suggestedNodeVerificationCommand(root); command != "" {
			return command
		}
	}
	if fileExists(filepath.Join(root, "go.work")) || fileExists(filepath.Join(root, "go.mod")) {
		return "go test ./..."
	}
	return "go test ./..."
}

func suggestedNodeVerificationCommand(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return ""
	}
	var pkg struct {
		Scripts        map[string]string `json:"scripts"`
		PackageManager string            `json:"packageManager"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return ""
	}
	manager := "npm"
	if strings.HasPrefix(pkg.PackageManager, "pnpm@") || fileExists(filepath.Join(root, "pnpm-lock.yaml")) {
		manager = "pnpm"
	} else if strings.HasPrefix(pkg.PackageManager, "yarn@") || fileExists(filepath.Join(root, "yarn.lock")) {
		manager = "yarn"
	}
	for _, script := range []string{"check", "test", "build"} {
		if strings.TrimSpace(pkg.Scripts[script]) != "" {
			return packageManagerScriptCommand(manager, script)
		}
	}
	return ""
}

func packageManagerScriptCommand(manager, script string) string {
	if script == "test" {
		return manager + " test"
	}
	return manager + " run " + script
}

func doctorSetupCommand(report doctorReport) string {
	command := "readyci setup --write-config --detect" + apiURLFlag(report.APIURL)
	if report.ProjectIDSource != "default" && strings.TrimSpace(report.ProjectID) != "" {
		command += " --project " + shellQuoteArg(report.ProjectID)
	}
	return command
}

func reportHasLevel(report doctorReport, section, level string) bool {
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Section == section && diagnostic.Level == level {
			return true
		}
	}
	return false
}

func reportHasDiagnostic(report doctorReport, section, level, containsText string) bool {
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Section == section && diagnostic.Level == level && strings.Contains(diagnostic.Message, containsText) {
			return true
		}
	}
	return false
}
