package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

type workerCapabilityReport struct {
	Claimable                 bool     `json:"claimable"`
	UploadCapable             bool     `json:"upload_capable"`
	FreshWorkerCount          int      `json:"fresh_worker_count,omitempty"`
	MatchingWorkerCount       int      `json:"matching_worker_count,omitempty"`
	MatchingUploadWorkerCount int      `json:"matching_upload_worker_count,omitempty"`
	MatchingWorkers           []string `json:"matching_workers,omitempty"`
	MatchingUploadWorkers     []string `json:"matching_upload_workers,omitempty"`
	Reasons                   []string `json:"reasons,omitempty"`
	UploadReasons             []string `json:"upload_reasons,omitempty"`
}

func checkWorkerCapability(plan checkRunPlan, workers []preflightapi.AdminWorkerSummary) workerCapabilityReport {
	var report workerCapabilityReport
	now := time.Now().UTC()
	for _, worker := range workers {
		if workerSeenRecently(worker, now) {
			report.FreshWorkerCount++
		}
		reasons := workerClaimCapabilityReasons(plan, worker, now)
		if len(reasons) == 0 {
			report.Claimable = true
			report.MatchingWorkers = append(report.MatchingWorkers, worker.ID)
			report.MatchingWorkerCount++
		} else {
			report.Reasons = append(report.Reasons, fmt.Sprintf("%s: %s", worker.ID, strings.Join(reasons, "; ")))
		}
		uploadReasons := workerUploadCapabilityReasons(plan, worker, now)
		if len(uploadReasons) == 0 {
			report.UploadCapable = true
			report.MatchingUploadWorkers = append(report.MatchingUploadWorkers, worker.ID)
			report.MatchingUploadWorkerCount++
		} else {
			report.UploadReasons = append(report.UploadReasons, fmt.Sprintf("%s: %s", worker.ID, strings.Join(uploadReasons, "; ")))
		}
	}
	sort.Strings(report.MatchingWorkers)
	sort.Strings(report.MatchingUploadWorkers)
	sort.Strings(report.Reasons)
	sort.Strings(report.UploadReasons)
	return report
}

func publicWorkerCapability(ctx context.Context, apiURL, token string, plan checkRunPlan) (workerCapabilityReport, error) {
	if strings.TrimSpace(token) == "" {
		return workerCapabilityReport{}, errors.New("no API token found")
	}
	spec := preflightapi.RunSpec{
		ProjectID:      plan.Options.ProjectID,
		Command:        plan.Options.Command,
		Region:         checkPlanDiagnosticRegion(plan),
		Image:          plan.Options.Image,
		RunnerSize:     plan.Options.RunnerSize,
		NetworkMode:    plan.Options.NetworkMode,
		Docker:         plan.Options.Docker,
		Services:       cloneRunServices(plan.Options.Services),
		WorkerSelector: copyStringMap(plan.Options.WorkerSelector),
	}
	resp, err := api.NewClient(apiURL, api.WithToken(token)).WorkerCapability(ctx, preflightapi.WorkerCapabilityCheckRequest{Spec: spec})
	if err != nil {
		return workerCapabilityReport{}, err
	}
	return workerCapabilityReport{
		Claimable:                 resp.Claimable,
		UploadCapable:             resp.UploadCapable,
		FreshWorkerCount:          resp.FreshWorkerCount,
		MatchingWorkerCount:       resp.MatchingWorkerCount,
		MatchingUploadWorkerCount: resp.MatchingUploadWorkerCount,
		Reasons:                   append([]string(nil), resp.Reasons...),
		UploadReasons:             append([]string(nil), resp.UploadReasons...),
	}, nil
}

func workerClaimCapabilityReasons(plan checkRunPlan, worker preflightapi.AdminWorkerSummary, now time.Time) []string {
	var reasons []string
	if worker.Draining {
		reasons = append(reasons, "worker is draining")
	}
	if !workerSeenRecently(worker, now) {
		reasons = append(reasons, "worker was not seen recently")
	}
	region := checkPlanDiagnosticRegion(plan)
	if preflightapi.NormalizeRegion(worker.Region) != region {
		reasons = append(reasons, "region "+region+" does not match worker region "+preflightapi.NormalizeRegion(worker.Region))
	}
	maxActiveSlots := workerActiveSlotCapacity(worker)
	if maxActiveSlots <= 0 {
		reasons = append(reasons, "max active compute slots is zero")
	}
	runnerSize := defaultString(plan.Options.RunnerSize, preflightapi.DefaultRunnerSize)
	slotCost := preflightapi.RunnerSizeSlotCost(runnerSize)
	if slotCost > maxActiveSlots {
		reasons = append(reasons, fmt.Sprintf("runner size %s costs %d slot(s), worker advertises %d", runnerSize, slotCost, maxActiveSlots))
	}
	if !containsString(worker.SupportedRunnerSizes, runnerSize) {
		reasons = append(reasons, "runner size "+runnerSize+" is not advertised")
	}
	image := defaultString(plan.Options.Image, preflightapi.DefaultImage)
	if !containsString(worker.SupportedImages, image) {
		reasons = append(reasons, "image "+image+" is not advertised")
	}
	if remoteRunRequiresDocker(plan.Options) {
		if !worker.SupportsDocker {
			reasons = append(reasons, "docker is not advertised")
		} else if !containsString(worker.SupportedDockerImages, image) {
			reasons = append(reasons, "docker image "+image+" is not advertised")
		}
	}
	if !containsString(worker.NetworkModes, plan.Options.NetworkMode) {
		reasons = append(reasons, "network mode "+plan.Options.NetworkMode+" is not advertised")
	}
	for _, key := range sortedStringMapKeys(plan.Options.WorkerSelector) {
		if worker.Labels[key] != plan.Options.WorkerSelector[key] {
			reasons = append(reasons, "worker label "+key+"="+plan.Options.WorkerSelector[key]+" is not advertised")
		}
	}
	return reasons
}

func remoteRunRequiresDocker(options remoteRunOptions) bool {
	return options.Docker || len(options.Services) > 0
}

func workerUploadCapabilityReasons(plan checkRunPlan, worker preflightapi.AdminWorkerSummary, now time.Time) []string {
	reasons := workerClaimCapabilityReasons(plan, worker, now)
	if strings.TrimSpace(worker.BundleUploadBaseURL) == "" {
		reasons = append(reasons, "bundle upload URL is not advertised")
	}
	return reasons
}

func workerSeenRecently(worker preflightapi.AdminWorkerSummary, now time.Time) bool {
	return worker.LastSeenAt != nil && !worker.LastSeenAt.IsZero() && !worker.LastSeenAt.Before(now.Add(-doctorFreshWorkerWindow))
}

func affectedAuditConfigured(cfg config.ProjectConfig) bool {
	return len(cfg.Affected.Profiles) > 0 || len(cfg.Affected.Rules) > 0
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
