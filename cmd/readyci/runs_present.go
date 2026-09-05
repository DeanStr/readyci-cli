package main

import (
	"fmt"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func printRunSummary(summary preflightapi.RunSummary) {
	fmt.Printf("run_id: %s\n", summary.RunID)
	fmt.Printf("project_id: %s\n", summary.ProjectID)
	fmt.Printf("region: %s\n", preflightapi.NormalizeRegion(summary.Region))
	fmt.Printf("status: %s\n", summary.Status)
	fmt.Printf("worker_id: %s\n", valueOrDash(summary.WorkerID))
	fmt.Printf("runner_size: %s\n", summary.RunnerSize)
	if summary.ConcurrencyGroup != "" {
		fmt.Printf("concurrency_group: %s\n", summary.ConcurrencyGroup)
		fmt.Printf("concurrency_limit: %d\n", summary.ConcurrencyLimit)
	}
	fmt.Printf("network_mode: %s\n", summary.NetworkMode)
	fmt.Printf("docker: %t\n", summary.Docker)
	fmt.Printf("cache_mode: %s\n", summary.CacheMode)
	if summary.ExitCode != nil {
		fmt.Printf("exit_code: %d\n", *summary.ExitCode)
	}
	printRunClassification(summary)
	fmt.Printf("created_at: %s\n", summary.CreatedAt.Format(time.RFC3339))
	fmt.Printf("events: %d\n", summary.EventCount)
	fmt.Printf("command: %s\n", summary.Command)
}

func printRunDetail(detail preflightapi.RunDetail) {
	summary := detail.Summary
	fmt.Printf("run_id: %s\n", summary.RunID)
	fmt.Printf("project_id: %s\n", summary.ProjectID)
	fmt.Printf("region: %s\n", preflightapi.NormalizeRegion(summary.Region))
	fmt.Printf("status: %s\n", summary.Status)
	fmt.Printf("worker_id: %s\n", valueOrDash(summary.WorkerID))
	fmt.Printf("runner_size: %s\n", summary.RunnerSize)
	if summary.ConcurrencyGroup != "" {
		fmt.Printf("concurrency_group: %s\n", summary.ConcurrencyGroup)
		fmt.Printf("concurrency_limit: %d\n", summary.ConcurrencyLimit)
	}
	fmt.Printf("network_mode: %s\n", summary.NetworkMode)
	fmt.Printf("docker: %t\n", summary.Docker)
	fmt.Printf("cache_mode: %s\n", summary.CacheMode)
	fmt.Printf("cache_lane: %s\n", valueOrDash(summary.CacheLaneID))
	if summary.ExitCode != nil {
		fmt.Printf("exit_code: %d\n", *summary.ExitCode)
	}
	printRunClassification(summary)
	fmt.Printf("created_at: %s\n", summary.CreatedAt.Format(time.RFC3339))
	fmt.Printf("queued_at: %s\n", timeOrDash(summary.QueuedAt))
	fmt.Printf("claimed_at: %s\n", timeOrDash(summary.ClaimedAt))
	fmt.Printf("lease_expires_at: %s\n", timeOrDash(summary.LeaseExpiresAt))
	fmt.Printf("completed_at: %s\n", timeOrDash(summary.CompletedAt))
	fmt.Printf("events: %d\n", summary.EventCount)
	fmt.Printf("command: %s\n", detail.Spec.Command)
	for _, reason := range detail.PolicyReasons {
		fmt.Printf("policy_reason: %s\n", reason)
	}
	printRunSecretPolicy(detail.SecretPolicy)
}

func printRunSecretPolicy(policy *preflightapi.RunSecretPolicyEvaluation) {
	if policy == nil {
		return
	}
	fmt.Printf("secret_policy_requested: %t\n", policy.Requested)
	fmt.Printf("secret_policy_eligible: %t\n", policy.Eligible)
	if policy.CommandHash != "" {
		fmt.Printf("secret_policy_command_hash: %s\n", policy.CommandHash)
	}
	for _, reason := range policy.Reasons {
		fmt.Printf("secret_policy_reason: %s\n", reason)
	}
}

func printRunClassification(summary preflightapi.RunSummary) {
	if summary.Classification == "" {
		return
	}
	fmt.Printf("classification: %s\n", summary.Classification)
	fmt.Printf("retryable: %t\n", summary.Retryable)
	if summary.AgentAction != "" {
		fmt.Printf("agent_action: %s\n", summary.AgentAction)
	}
	if summary.FailureSummary != "" && summary.Classification != preflightapi.ClassificationSuccess {
		fmt.Printf("failure_summary: %s\n", summary.FailureSummary)
	}
	if summary.PrimaryFile != "" {
		fmt.Printf("primary_file: %s\n", summary.PrimaryFile)
	}
}
