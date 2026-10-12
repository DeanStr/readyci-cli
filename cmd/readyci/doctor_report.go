package main

import (
	"context"
	"fmt"
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

	probeDoctorGit(ctx, &report)
	var cfg config.ProjectConfig
	var path string
	var selectedPlan *checkRunPlan
	cfg, path, err := config.LoadProjectConfig(".")
	if err != nil {
		report.fail("config", fmt.Sprintf("config could not be loaded: %v", err), "run readyci config validate after fixing the YAML")
	} else if path == "" {
		report.warn("config", "no .readyci.yml found; defaults will be used", "run readyci setup --write-config --detect to create a project config", "run readyci setup --print to inspect generated config without writing it")
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
		probeDoctorRepository(ctx, cfg, path, &report)
		return report
	}

	probeDoctorHealth(ctx, apiURL, &report)
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
