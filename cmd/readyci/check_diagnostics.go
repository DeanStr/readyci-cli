package main

import (
	"context"
	"strings"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func applyProjectHomeRegionForDiagnostics(ctx context.Context, plan checkRunPlan, apiURL, token string) (checkRunPlan, error) {
	if !plan.RegionDefaulted {
		return plan, nil
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return plan, nil
	}
	projectID := defaultString(plan.Options.ProjectID, "proj_dev")
	settings, err := api.NewClient(apiURL, api.WithToken(token)).ProjectSettings(ctx, projectID)
	if err != nil {
		return plan, err
	}
	region := preflightapi.NormalizeRegion(settings.HomeRegion)
	if err := preflightapi.ValidateRegion(region); err != nil {
		return plan, err
	}
	plan.DiagnosticRegion = region
	return plan, nil
}

func applyProjectHomeRegionForDiagnosticsBestEffort(ctx context.Context, plan checkRunPlan, apiURL, token string) checkRunPlan {
	resolved, err := applyProjectHomeRegionForDiagnostics(ctx, plan, apiURL, token)
	if err != nil {
		return plan
	}
	return resolved
}

func applyProjectHomeRegionsForAffectedDiagnosticsBestEffort(ctx context.Context, plans []affectedCheckPlan, report affectedDryRunReport, apiURL, token string) ([]affectedCheckPlan, affectedDryRunReport) {
	token = strings.TrimSpace(token)
	if token == "" {
		return plans, report
	}
	client := api.NewClient(apiURL, api.WithToken(token))
	regionsByProject := make(map[string]string)
	for i := range plans {
		if !plans[i].Plan.RegionDefaulted {
			continue
		}
		projectID := defaultString(plans[i].Plan.Options.ProjectID, report.ProjectID)
		projectID = defaultString(projectID, "proj_dev")
		region, ok := regionsByProject[projectID]
		if !ok {
			settings, err := client.ProjectSettings(ctx, projectID)
			if err != nil {
				continue
			}
			region = preflightapi.NormalizeRegion(settings.HomeRegion)
			if err := preflightapi.ValidateRegion(region); err != nil {
				continue
			}
			regionsByProject[projectID] = region
		}
		plans[i].Plan.DiagnosticRegion = region
		if i < len(report.SelectedChecks) && report.SelectedChecks[i].Name == plans[i].Plan.Name {
			report.SelectedChecks[i].Region = region
			continue
		}
		for j := range report.SelectedChecks {
			if report.SelectedChecks[j].Name == plans[i].Plan.Name {
				report.SelectedChecks[j].Region = region
				break
			}
		}
	}
	return plans, report
}
