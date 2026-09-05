package main

import (
	"fmt"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func groupProjectID(apiURL, value string) (string, error) {
	return effectiveProjectID(apiURL, value)
}

func printRunGroupDetail(detail preflightapi.RunGroupDetail) {
	summary := detail.Summary
	fmt.Printf("run_group_id: %s\n", summary.RunGroupID)
	fmt.Printf("project_id: %s\n", summary.ProjectID)
	fmt.Printf("kind: %s\n", summary.Kind)
	if summary.Name != "" {
		fmt.Printf("name: %s\n", summary.Name)
	}
	fmt.Printf("status: %s\n", summary.Status)
	fmt.Printf("children: %d\n", summary.ChildCount)
	if summary.LatestAttemptCount > 0 {
		fmt.Printf("latest_attempts: %d\n", summary.LatestAttemptCount)
	}
	if summary.FailedChildCount > 0 {
		fmt.Printf("failed_children: %d\n", summary.FailedChildCount)
	}
	for _, child := range detail.Children {
		fmt.Printf("child: %s\n", child.RunID)
		fmt.Printf("  logical_child: %s\n", child.LogicalChildID)
		if child.CommandAlias != "" {
			fmt.Printf("  check: %s\n", child.CommandAlias)
		}
		if child.MatrixVariant != "" {
			fmt.Printf("  matrix_variant: %s\n", child.MatrixVariant)
		}
		fmt.Printf("  attempt: %d\n", child.Attempt)
		fmt.Printf("  latest_attempt: %t\n", child.LatestAttempt)
		if child.RetryOfRunID != "" {
			fmt.Printf("  retry_of_run_id: %s\n", child.RetryOfRunID)
		}
		fmt.Printf("  status: %s\n", child.Status)
		if child.ExitCode != nil {
			fmt.Printf("  exit_code: %d\n", *child.ExitCode)
		}
		if child.Classification != "" {
			fmt.Printf("  classification: %s\n", child.Classification)
		}
		for _, reason := range child.PolicyReasons {
			fmt.Printf("  policy_reason: %s\n", reason)
		}
	}
}
