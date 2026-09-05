package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func usageCommand(args []string) error {
	fs := flag.NewFlagSet("usage", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	jsonOutput := fs.Bool("json", false, "print machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci usage [--api-url URL] [--token TOKEN] [--json]", nil)
	}
	client, err := authenticatedClient(*apiURL, *apiToken)
	if err != nil {
		return err
	}
	ctx, stop := signalContext()
	defer stop()
	usage, err := client.AccountUsage(ctx)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSONOutput(usage)
	}
	printAccountUsage(usage)
	return nil
}

func printAccountUsage(account preflightapi.AccountUsageResponse) {
	fmt.Printf("team_id: %s\n", account.TeamID)
	fmt.Printf("plan: %s\n", account.Plan.ID)
	fmt.Printf("plan_name: %s\n", account.Plan.Name)
	fmt.Printf("monthly_price_cents: %d\n", account.Plan.MonthlyPriceCents)
	includedSlotMinutes := accountIncludedComputeSlotMinutes(account.Plan)
	allowanceSlotMinutes := accountComputeSlotMinuteAllowance(account.Plan, account.Usage)
	maxActiveSlots := accountPlanActiveSlotCapacity(account.Plan)
	fmt.Printf("included_compute_slot_minutes: %d\n", includedSlotMinutes)
	fmt.Printf("bonus_compute_slot_minutes: %.2f\n", account.Usage.BonusComputeSlotMinutes)
	fmt.Printf("compute_slot_minute_allowance: %.2f\n", allowanceSlotMinutes)
	computeSlotMinutes := accountComputeSlotMinutes(account.Usage)
	fmt.Printf("compute_slot_minutes: %.2f\n", computeSlotMinutes)
	if allowanceSlotMinutes > 0 {
		percent := computeSlotMinutes / allowanceSlotMinutes * 100
		fmt.Printf("compute_slot_percent: %.1f\n", percent)
	}
	fmt.Printf("cache_gb_month: %.4f\n", account.Usage.CacheGBMonth)
	fmt.Printf("artifact_gb_month: %.4f\n", account.Usage.ArtifactGBMonth)
	fmt.Printf("active_api_tokens: %d\n", account.Usage.ActiveAPITokens)
	fmt.Printf("project_count: %d\n", account.Usage.ProjectCount)
	fmt.Printf("max_active_compute_slots: %d\n", maxActiveSlots)
	fmt.Printf("max_queued_runs: %d\n", account.Plan.MaxQueuedRuns)
	fmt.Printf("cache_quota_bytes: %d\n", account.Plan.CacheQuotaBytes)
	fmt.Printf("docker_enabled: %t\n", account.Plan.DockerEnabled)
	fmt.Printf("network_modes: %s\n", strings.Join(account.Plan.NetworkModes, ","))
	if account.Subscription != nil {
		fmt.Printf("subscription: %s\n", account.Subscription.ID)
		fmt.Printf("subscription_status: %s\n", account.Subscription.Status)
	}
}

func accountIncludedComputeSlotMinutes(plan preflightapi.AccountPlanSummary) int {
	if plan.IncludedComputeSlotMinutes > 0 {
		return plan.IncludedComputeSlotMinutes
	}
	return plan.IncludedStandardMinutes * preflightapi.RunnerSizeSlotCost(preflightapi.DefaultRunnerSize)
}

func accountComputeSlotMinutes(usage preflightapi.AccountUsageSummary) float64 {
	if usage.ComputeSlotMinutes > 0 {
		return usage.ComputeSlotMinutes
	}
	return usage.StandardRunnerMinutes * float64(preflightapi.RunnerSizeSlotCost(preflightapi.DefaultRunnerSize))
}

func accountComputeSlotMinuteAllowance(plan preflightapi.AccountPlanSummary, usage preflightapi.AccountUsageSummary) float64 {
	return float64(accountIncludedComputeSlotMinutes(plan)) + usage.BonusComputeSlotMinutes
}

func accountPlanActiveSlotCapacity(plan preflightapi.AccountPlanSummary) int {
	if plan.MaxActiveComputeSlots > 0 {
		return plan.MaxActiveComputeSlots
	}
	return plan.MaxActiveRuns * preflightapi.RunnerSizeSlotCost(preflightapi.DefaultRunnerSize)
}

func workerActiveSlotCapacity(worker preflightapi.AdminWorkerSummary) int {
	if worker.MaxActiveComputeSlots > 0 {
		return worker.MaxActiveComputeSlots
	}
	return worker.MaxActiveRuns * preflightapi.RunnerSizeSlotCost(preflightapi.DefaultRunnerSize)
}
