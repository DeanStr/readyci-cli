package main

import (
	"fmt"
	"strings"
)

func printAgentVerifyReport(report agentVerifyReport) {
	fmt.Printf("status: %s\n", report.Status)
	if report.Intent != "" {
		fmt.Printf("intent: %s\n", report.Intent)
	}
	if report.RunID != "" {
		fmt.Printf("run_id: %s\n", report.RunID)
	}
	if report.Check != "" {
		fmt.Printf("check: %s\n", report.Check)
	}
	fmt.Printf("classification: %s\n", report.Classification)
	fmt.Printf("terminal: %t\n", report.Terminal)
	if report.ExitCode != nil {
		fmt.Printf("exit_code: %d\n", *report.ExitCode)
	}
	fmt.Printf("summary: %s\n", report.Summary)
	if report.ExplainCommand != "" {
		fmt.Printf("explain: %s\n", report.ExplainCommand)
	}
	if report.LogsCommand != "" {
		fmt.Printf("logs: %s\n", report.LogsCommand)
	}
	printAgentWarnings(report.Warnings)
}

func printAgentPlanReport(report agentPlanReport) {
	fmt.Println("agent_plan: readyci")
	fmt.Printf("status: %s\n", report.Status)
	if report.Intent != "" {
		fmt.Printf("intent: %s\n", report.Intent)
	}
	if report.Mode == "affected_rules" {
		fmt.Printf("mode: %s\n", report.Mode)
		fmt.Printf("reason: %s\n", report.Reason)
		fmt.Printf("change_mode: %s\n", report.ChangeMode)
		if len(report.Checks) == 0 {
			fmt.Println("checks: none")
		} else {
			fmt.Println("checks:")
			for _, check := range report.Checks {
				state := "skipped"
				if check.Selected {
					state = "selected"
				}
				fmt.Printf("  - %s: %s", check.Check, state)
				if len(check.MatchedPaths) > 0 {
					fmt.Printf(" (%s)", strings.Join(check.MatchedPaths, ", "))
				}
				fmt.Println()
			}
		}
		printAgentWarnings(report.Warnings)
		printAgentPolicy(report.Policy)
		fmt.Printf("verify: %s\n", report.VerifyCommand)
		return
	}
	fmt.Printf("check: %s\n", report.Check)
	fmt.Printf("reason: %s\n", report.Reason)
	fmt.Printf("command: %s\n", report.Command)
	fmt.Printf("change_mode: %s\n", report.ChangeMode)
	fmt.Printf("runner_size: %s\n", report.RunnerSize)
	fmt.Printf("timeout_seconds: %d\n", report.TimeoutSeconds)
	if len(report.MatrixVariants) > 0 {
		fmt.Printf("matrix_variants: %s\n", strings.Join(report.MatrixVariants, ","))
	}
	printAgentWarnings(report.Warnings)
	printAgentPolicy(report.Policy)
	fmt.Printf("verify: %s\n", report.VerifyCommand)
}

func printAgentPolicy(policy *agentPolicyReport) {
	if policy == nil {
		return
	}
	fmt.Printf("network_mode: %s\n", policy.NetworkMode)
	if len(policy.NetworkAllowlist) > 0 {
		fmt.Printf("network_allowlist: %s\n", strings.Join(policy.NetworkAllowlist, ","))
	}
	if len(policy.TokenScopesRequired) > 0 {
		fmt.Printf("token_scopes_required: %s\n", strings.Join(policy.TokenScopesRequired, ","))
	}
	if policy.UploadPreviewCommand != "" {
		fmt.Printf("upload_preview: %s\n", policy.UploadPreviewCommand)
	}
	for _, reason := range policy.PolicyReasons {
		fmt.Printf("policy_reason: %s\n", reason)
	}
}

func printAgentWarnings(warnings []agentWarning) {
	for _, warning := range warnings {
		if warning.Section != "" {
			fmt.Printf("warning[%s]: %s\n", warning.Section, warning.Message)
		} else {
			fmt.Printf("warning: %s\n", warning.Message)
		}
	}
}

func printAgentExplainReport(report agentExplainReport) {
	fmt.Printf("run_id: %s\n", report.RunID)
	fmt.Printf("status: %s\n", report.Status)
	if report.Check != "" {
		fmt.Printf("check: %s\n", report.Check)
	}
	if report.MatrixVariant != "" {
		fmt.Printf("matrix_variant: %s\n", report.MatrixVariant)
	}
	fmt.Printf("terminal: %t\n", report.Terminal)
	if report.ExitCode != nil {
		fmt.Printf("exit_code: %d\n", *report.ExitCode)
	}
	fmt.Printf("classification: %s\n", report.Diagnosis.Kind)
	fmt.Printf("summary: %s\n", report.Diagnosis.Summary)
	if report.RuntimeDiagnostics != nil {
		printRuntimeDiagnosticSummary(*report.RuntimeDiagnostics)
	}
	if report.Timings != nil {
		printRunTimings(*report.Timings)
	}
	if report.TestResults != nil {
		failures := *report.TestResults
		failures.Results = nil
		for _, test := range report.TestResults.Results {
			if test.Status == "failed" || test.Status == "error" {
				failures.Results = append(failures.Results, test)
			}
		}
		printRunTestResults(failures)
	}
	if report.Repair.FailingTest != "" {
		fmt.Printf("failing_test: %s\n", sanitizeRunLogsSummaryText(report.Repair.FailingTest))
	}
	if report.Repair.PrimaryFile != "" {
		fmt.Printf("investigate_file: %s\n", sanitizeRunLogsSummaryText(report.Repair.PrimaryFile))
	}
	if report.Repair.ConfidenceNote != "" {
		fmt.Printf("classification_confidence: %s\n", report.Repair.ConfidenceNote)
	}
	if len(report.Repair.Evidence) > 0 {
		fmt.Println("evidence:")
		for _, item := range report.Repair.Evidence {
			if item.Path != "" {
				fmt.Printf("  %s: %s\n", sanitizeRunLogsSummaryText(item.Path), sanitizeRunLogsSummaryText(item.Text))
			} else {
				fmt.Printf("  %s\n", sanitizeRunLogsSummaryText(item.Text))
			}
		}
	}
	for _, reason := range report.PolicyReasons {
		fmt.Printf("policy_reason: %s\n", reason)
	}
	fmt.Printf("next: %s\n", report.RecommendedNextStep)
	for _, pack := range report.Repair.EvidencePacks {
		if pack.PreviewCommand != "" {
			fmt.Printf("evidence_preview[%s]: %s\n", sanitizeRunLogsSummaryText(pack.Kind), pack.PreviewCommand)
		}
	}
	fmt.Printf("logs: %s\n", report.LogsCommand)
	if report.RerunCommand != "" {
		fmt.Printf("rerun: %s\n", report.RerunCommand)
	}
	printAgentWarnings(report.Warnings)
	if report.LogTruncated {
		fmt.Println("warning: log evidence is truncated; additional context may be available in retained artifacts.")
	}
}
