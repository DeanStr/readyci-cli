package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestPrintAgentReportsIncludeActionableFields(t *testing.T) {
	exitCode := 1
	verifyOutput, err := captureStdoutForCLITest(t, func() error {
		printAgentVerifyReport(agentVerifyReport{
			Status:         "complete",
			RunID:          "run_123",
			Check:          "go",
			Terminal:       true,
			ExitCode:       &exitCode,
			Classification: preflightapi.ClassificationTestFailed,
			Summary:        "Go tests failed.",
			ExplainCommand: "readyci agent explain run_123",
			LogsCommand:    "readyci logs run_123",
			Warnings:       []agentWarning{{Section: "affected.profiles", Message: "profile warning"}},
		})
		return nil
	})
	if err != nil {
		t.Fatalf("capture verify report: %v", err)
	}
	requireCLITestOutputContains(t, verifyOutput,
		"status: complete",
		"run_id: run_123",
		"check: go",
		"terminal: true",
		"exit_code: 1",
		"classification: test_failed",
		"explain: readyci agent explain run_123",
		"logs: readyci logs run_123",
		"warning[affected.profiles]: profile warning",
	)

	planOutput, err := captureStdoutForCLITest(t, func() error {
		printAgentPlanReport(agentPlanReport{
			Status:         "planned",
			Check:          "go",
			Reason:         "matched changed Go files",
			Command:        "go test ./...",
			ChangeMode:     "working-tree",
			RunnerSize:     "standard",
			TimeoutSeconds: 600,
			MatrixVariants: []string{"GO=1", "GO=2"},
			VerifyCommand:  "readyci verify --check go",
		})
		return nil
	})
	if err != nil {
		t.Fatalf("capture plan report: %v", err)
	}
	requireCLITestOutputContains(t, planOutput,
		"agent_plan: readyci",
		"status: planned",
		"reason: matched changed Go files",
		"matrix_variants: GO=1,GO=2",
		"verify: readyci verify --check go",
	)

	affectedPlanOutput, err := captureStdoutForCLITest(t, func() error {
		printAgentPlanReport(agentPlanReport{
			Status:     "ready",
			Mode:       "affected_rules",
			Reason:     "matched_changed_paths",
			ChangeMode: "working",
			Checks: []agentAffectedCheck{
				{Check: "go", Selected: true, MatchedPaths: []string{"cli/main.go"}},
				{Check: "ci", Selected: false, MatchedPaths: []string{"cli/main.go"}},
			},
			Warnings:      []agentWarning{{Section: "affected.profiles", Message: "profile warning"}},
			VerifyCommand: "readyci verify --json --progress=status",
		})
		return nil
	})
	if err != nil {
		t.Fatalf("capture affected plan report: %v", err)
	}
	requireCLITestOutputContains(t, affectedPlanOutput,
		"mode: affected_rules",
		"checks:",
		"  - go: selected (cli/main.go)",
		"  - ci: skipped (cli/main.go)",
		"warning[affected.profiles]: profile warning",
		"verify: readyci verify --json --progress=status",
	)
	if strings.Contains(affectedPlanOutput, "check: \n") || strings.Contains(affectedPlanOutput, "command: \n") || strings.Contains(affectedPlanOutput, "timeout_seconds: 0\n") {
		t.Fatalf("affected plan output includes misleading singleton fields:\n%s", affectedPlanOutput)
	}

	explainOutput, err := captureStdoutForCLITest(t, func() error {
		printAgentExplainReport(agentExplainReport{
			RunID:         "run_123",
			Status:        preflightapi.StatusFailed,
			Check:         "go",
			MatrixVariant: "GO=1",
			Terminal:      true,
			ExitCode:      &exitCode,
			Diagnosis: agentDiagnosis{
				Kind:    preflightapi.ClassificationTestFailed,
				Summary: "Go test failed in store_test.go.",
			},
			PolicyReasons:       []string{"network_mode default is denied"},
			RecommendedNextStep: "Fix the failing assertion.",
			LogsCommand:         "readyci logs run_123",
			RerunCommand:        "readyci run --rerun run_123",
		})
		return nil
	})
	if err != nil {
		t.Fatalf("capture explain report: %v", err)
	}
	requireCLITestOutputContains(t, explainOutput,
		"matrix_variant: GO=1",
		"classification: test_failed",
		"policy_reason: network_mode default is denied",
		"next: Fix the failing assertion.",
		"rerun: readyci run --rerun run_123",
	)
}

func TestPrintAffectedReportsIncludeDetailsAndSamples(t *testing.T) {
	auditOutput, err := captureStdoutForCLITest(t, func() error {
		printAffectedAuditReport(affectedAuditReport{
			ConfigPath: "/repo/.readyci.yml",
			Profiles:   []string{"default", "ci"},
			Rules:      2,
			Status:     "warn",
			Tests: []affectedAuditTestReport{{
				Paths:          []string{"cli/main.go"},
				ExpectedChecks: []string{"go"},
				ActualChecks:   []string{"go", "lint"},
				Status:         "mismatch",
				Error:          "extra lint check selected",
			}},
			DuplicateSelections: []affectedDuplicateSelection{{
				Path:           "cli/main.go",
				Aggregate:      "all",
				SubsumedChecks: []string{"go", "lint"},
			}},
			Examples: []affectedAuditExample{{
				Paths:  []string{"web/src/App.tsx"},
				Checks: []string{"web"},
			}},
			Warnings:       []string{"unmatched files remain"},
			UnmatchedFiles: samplePaths("unmatched", 22),
			IgnoredFiles:   []string{"vendor/generated.go"},
		})
		return nil
	})
	if err != nil {
		t.Fatalf("capture audit report: %v", err)
	}
	requireCLITestOutputContains(t, auditOutput,
		"affected: audit",
		"profiles: default, ci",
		"test: 1",
		"expected: go",
		"actual: go, lint",
		"error: extra lint check selected",
		"duplicate_selection: cli/main.go selects all and go, lint",
		"unmatched_source_file: ... 2 more",
		"ignored_source_file: vendor/generated.go",
		"warning: unmatched files remain",
	)

	explainOutput, err := captureStdoutForCLITest(t, func() error {
		printAffectedExplainReport(affectedExplainReport{
			ConfigPath: "/repo/.readyci.yml",
			Status:     "ok",
			Paths: []affectedExplainPath{
				{
					Path:   "cli/main.go",
					Checks: []string{"go"},
					Reasons: []affectedRuleMatch{{
						Rule:    1,
						Pattern: "cli/**",
						Path:    "cli/main.go",
					}},
					SkippedChecks: []affectedSkippedCheck{{
						Name:       "all",
						Reason:     "subsumed by go",
						SubsumedBy: []string{"go"},
					}},
				},
				{
					Path:       "vendor/generated.go",
					WhyNot:     "ignored",
					IgnoredBy:  "vendor/**",
					FilterHits: []string{"excluded by audit_ignore"},
				},
			},
		})
		return nil
	})
	if err != nil {
		t.Fatalf("capture explain report: %v", err)
	}
	requireCLITestOutputContains(t, explainOutput,
		"affected: explain",
		"path: cli/main.go",
		"reason: cli/main.go matched cli/** (rule 1)",
		"skipped_check: all",
		"subsumed_by: go",
		"path: vendor/generated.go",
		"checks: none",
		"ignored_by: vendor/**",
		"detail: excluded by audit_ignore",
	)

	fallbackOutput, err := captureStdoutForCLITest(t, func() error {
		printAffectedExplainReport(affectedExplainReport{
			Status: "ok",
			Paths: []affectedExplainPath{{
				Path:   "README.md",
				Checks: []string{"ci"},
				Reasons: []affectedRuleMatch{{
					Pattern: "affected.unmatched_checks",
					Path:    "README.md",
				}},
			}},
		})
		return nil
	})
	if err != nil {
		t.Fatalf("capture fallback explain report: %v", err)
	}
	requireCLITestOutputContains(t, fallbackOutput, "reason: README.md selected by affected.unmatched_checks")
	if strings.Contains(fallbackOutput, "rule 0") {
		t.Fatalf("fallback explain output cites nonexistent rule zero:\n%s", fallbackOutput)
	}
}

func TestPrintAffectedSuggestCargoAndDryRunReports(t *testing.T) {
	suggestOutput, err := captureStdoutForCLITest(t, func() error {
		printAffectedSuggestReport(affectedSuggestReport{
			ConfigPath: "/repo/.readyci.yml",
			Profiles:   []string{"default"},
			Rules: []affectedSuggestRule{{
				Paths:  []string{"cli/**"},
				Checks: []string{"go"},
			}},
			Subsumes:    map[string][]string{"all": {"go", "web"}},
			Tests:       []affectedSuggestTest{{Path: "cli/main.go", Checks: []string{"go"}}},
			AuditIgnore: []string{"vendor/**"},
			Warnings:    []string{"review suggested rules"},
		})
		return nil
	})
	if err != nil {
		t.Fatalf("capture suggest report: %v", err)
	}
	requireCLITestOutputContains(t, suggestOutput,
		"affected: suggest",
		"profiles: default",
		"rules: 1",
		"tests: 1",
		"audit_ignore: 1",
		"warning: review suggested rules",
		"snippet:",
		"affected:",
		"subsumes:",
	)

	cargoOutput, err := captureStdoutForCLITest(t, func() error {
		printCargoAffectedReport(cargoAffectedReport{
			WorkspaceRoot: "/repo",
			Packages: []cargoAffectedPackage{{
				Name:  "readyci-core",
				Check: "cargo-readyci-core",
			}},
			Rules: []cargoAffectedRuleSuggestion{{
				Paths:  []string{"crates/readyci-core/**"},
				Checks: []string{"cargo-readyci-core"},
			}},
			Warnings: []string{"workspace has unpublished crates"},
		})
		return nil
	})
	if err != nil {
		t.Fatalf("capture cargo report: %v", err)
	}
	requireCLITestOutputContains(t, cargoOutput,
		"affected: cargo",
		"workspace_root: /repo",
		"warning: workspace has unpublished crates",
		"command: cargo test -p readyci-core",
		"- crates/readyci-core/**",
		"- cargo-readyci-core",
	)

	dryRunOutput, err := captureStdoutForCLITest(t, func() error {
		printAffectedDryRunReport(affectedDryRunReport{
			ConfigPath:   "/repo/.readyci.yml",
			ProjectID:    "proj_dev",
			ChangeMode:   "working-tree",
			ChangedFiles: samplePaths("changed", 21),
			SelectedChecks: []affectedDryRunCheck{{
				Name:               "go",
				Command:            "go test ./...",
				Image:              "ubuntu-24.04-base",
				Region:             "default",
				RunnerSize:         "standard",
				TimeoutSeconds:     600,
				NetworkMode:        "none",
				Docker:             true,
				ChangeMode:         "working-tree",
				MatrixVariantCount: 2,
				Reasons: []affectedRuleMatch{{
					Rule:    2,
					Pattern: "cli/**",
					Path:    "cli/main.go",
				}},
			}},
			SkippedChecks: []affectedSkippedCheck{{
				Name:         "all",
				Status:       "skipped",
				Reason:       "subsumed",
				SubsumedBy:   []string{"go"},
				MatchedPaths: []string{"cli/main.go"},
			}},
		})
		return nil
	})
	if err != nil {
		t.Fatalf("capture dry-run report: %v", err)
	}
	requireCLITestOutputContains(t, dryRunOutput,
		"affected: dry-run",
		"project_id: proj_dev",
		"changed_files: 21",
		"changed_file: ... 1 more",
		"check: go",
		"matrix_variants: 2",
		"reason: cli/main.go matched cli/** (rule 2)",
		"skipped_check: all",
		"subsumed_by: go",
		"matched_path: cli/main.go",
	)
}

func samplePaths(prefix string, count int) []string {
	paths := make([]string, 0, count)
	for index := 0; index < count; index++ {
		paths = append(paths, fmt.Sprintf("%s/%02d.go", prefix, index+1))
	}
	return paths
}

func TestPrintPathSampleOmitsEmptyAndLimitsOutput(t *testing.T) {
	output, err := captureStdoutForCLITest(t, func() error {
		printPathSample("path", nil, 3)
		printPathSample("path", []string{"a", "b", "c", "d"}, 3)
		return nil
	})
	if err != nil {
		t.Fatalf("capture path sample: %v", err)
	}
	if strings.Contains(output, "<nil>") {
		t.Fatalf("empty path sample printed unexpected data:\n%s", output)
	}
	requireCLITestOutputContains(t, output, "path: a", "path: b", "path: c", "path: ... 1 more")
}

func TestPrintVerificationSessionExplainsIncompleteResults(t *testing.T) {
	for _, tc := range []struct {
		name, verdict, classification, currentness string
		uncovered                                  []string
		want, absent                               []string
	}{
		{name: "stale passing result", verdict: "verified", currentness: "stale", want: []string{"currentness: stale", "does not describe your current workspace", "readyci sessions rerun"}},
		{name: "partial check", verdict: "checks_passed", classification: "partial_verification", currentness: "current", uncovered: []string{"platform/session.go"}, want: []string{"Selected checks passed", "uncovered_file: platform/session.go", "readyci verify"}, absent: []string{"--check"}},
		{name: "empty partial check", verdict: "insufficient_evidence", classification: "partial_verification", want: []string{"did not establish workspace verification", "readyci verify"}, absent: []string{"Selected checks passed"}},
		{name: "stale coverage gap", verdict: "insufficient_evidence", classification: "harness_gap", currentness: "stale", uncovered: []string{"platform/session.go"}, want: []string{"currentness: stale", "uncovered_file: platform/session.go", "owner or admin", "Rerunning alone"}, absent: []string{"next_command:"}},
		{name: "no registered policy", verdict: "checks_passed", classification: "client_proposed_plan", want: []string{"owner or admin", "registered verification policy"}, absent: []string{"next_command:"}},
		{name: "untrusted evidence", verdict: "checks_passed", classification: "untrusted_execution_evidence", want: []string{"execution evidence", "operator"}, absent: []string{"next_command:"}},
		{name: "current verified", verdict: "verified", currentness: "current", absent: []string{"next:", "next_command:", "incomplete"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := preflightapi.VerificationSession{SessionID: "session_123", State: "terminal", Verdict: tc.verdict, Classification: tc.classification, Currentness: tc.currentness, Recipe: recoveryTestRecipe(t), Coverage: preflightapi.VerificationCoverage{UncoveredFiles: tc.uncovered}}
			output, err := captureStdoutForCLITest(t, func() error { printVerificationSession(session, ""); return nil })
			if err != nil {
				t.Fatal(err)
			}
			requireCLITestOutputContains(t, output, tc.want...)
			for _, absent := range tc.absent {
				if strings.Contains(output, absent) {
					t.Errorf("unexpected %q in output:\n%s", absent, output)
				}
			}
		})
	}
}

func TestPrintAgentExplainSurfacesFailureEvidence(t *testing.T) {
	report := agentExplainReport{
		RunID: "run_123", Check: "api-tests", Status: "failed", Terminal: true,
		Diagnosis: agentDiagnosis{Kind: "test_failed", Action: "fix_code", Confidence: 0.9},
		Repair: agentRepairPacket{
			FailingTest: "TestExpiredSession", PrimaryFile: "session_test.go",
			ConfidenceNote: "high: classification came from structured run data or strong log signals",
			Evidence:       []agentEvidence{{Path: "session_test.go", Text: "session_test.go:84: expected 401, received 200"}},
		},
		LogsCommand: "readyci runs logs run_123",
	}
	output, err := captureStdoutForCLITest(t, func() error { printAgentExplainReport(report); return nil })
	if err != nil {
		t.Fatal(err)
	}
	requireCLITestOutputContains(t, output, "failing_test: TestExpiredSession", "investigate_file: session_test.go", "expected 401, received 200", "classification_confidence:")
	if strings.Index(output, "expected 401") > strings.Index(output, "logs:") {
		t.Fatal("failure evidence should precede the full-log command")
	}
}

func TestVerificationRecoveryPreservesRequestContext(t *testing.T) {
	session := preflightapi.VerificationSession{
		SessionID: "session_123", ProjectID: "project_123", Intent: "pre-commit", State: "terminal",
		Verdict: "checks_passed", Classification: "partial_verification",
		Recipe:   recoveryTestRecipe(t),
		Coverage: preflightapi.VerificationCoverage{UncoveredFiles: samplePaths("remaining", 12)},
	}
	session.Recipe.Check = "web"
	session.Recipe.ChangeMode = "staged"
	session.Recipe.RequestSecrets = true
	session.Recipe.RetryProvided = true
	var err error
	session.Recipe.Digest, err = preflightapi.VerificationRecipeDigest(session.Recipe)
	if err != nil {
		t.Fatal(err)
	}
	output, err := captureStdoutForCLITest(t, func() error { printVerificationSession(session, "http://127.0.0.1:4321"); return nil })
	if err != nil {
		t.Fatal(err)
	}
	requireCLITestOutputContains(t, output, "--api-url http://127.0.0.1:4321", "--project project_123", "--intent pre-commit", "--changes staged", "--predecessor session_123", "--request-secrets", "--retry 0", "uncovered_file: ... 2 more")
	for _, absent := range []string{"--check", "--idempotency-key", "remaining/11.go"} {
		if strings.Contains(output, absent) {
			t.Errorf("unexpected %q in output:\n%s", absent, output)
		}
	}
}

func TestVerificationRecoveryDoesNotAdviseOnPendingOrExecutionSessions(t *testing.T) {
	for _, session := range []preflightapi.VerificationSession{
		{State: "sealed", Verdict: "pending"},
		{State: "terminal", Verdict: "verified", Currentness: "stale", Recipe: preflightapi.VerificationRecipe{Purpose: preflightapi.ExecutionPurpose}},
	} {
		output, err := captureStdoutForCLITest(t, func() error { printVerificationSession(session, ""); return nil })
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(output, "next:") || strings.Contains(output, "incomplete:") {
			t.Fatalf("unexpected verification advice:\n%s", output)
		}
	}
}

func TestAgentExplainReportsIncompleteEvidence(t *testing.T) {
	report := agentExplainReport{
		Repair:   agentRepairPacket{Evidence: []agentEvidence{{Path: "session_test.go", Text: "expected 401\nreceived 200\x1b[31m"}}, EvidencePacks: []agentEvidencePack{{Kind: "playwright_trace", PreviewCommand: "readyci agent artifacts plan run_123"}}},
		Warnings: []agentWarning{{Section: "artifacts", Message: "artifact listing failed"}}, LogTruncated: true,
	}
	output, err := captureStdoutForCLITest(t, func() error { printAgentExplainReport(report); return nil })
	if err != nil {
		t.Fatal(err)
	}
	requireCLITestOutputContains(t, output, "session_test.go: expected 401 received 200", "evidence_preview[playwright_trace]:", "warning[artifacts]: artifact listing failed", "log evidence is truncated")
	if strings.Contains(output, "\x1b") || strings.Contains(output, "failing_test:") || strings.Contains(output, "investigate_file:") {
		t.Fatalf("unexpected escape or invented diagnosis:\n%s", output)
	}
}

func TestStaleVerificationReplaysSupportedRecipe(t *testing.T) {
	recipe := preflightapi.VerificationRecipe{SchemaVersion: preflightapi.VerificationRecipeSchemaVersion, Rerunnable: true, ChangeMode: "working", Source: &preflightapi.VerificationSourceRecipe{}}
	var err error
	recipe.Digest, err = preflightapi.VerificationRecipeDigest(recipe)
	if err != nil {
		t.Fatal(err)
	}
	session := preflightapi.VerificationSession{SessionID: "session_123", State: "terminal", Verdict: "verified", Currentness: "stale", Recipe: recipe}
	output, err := captureStdoutForCLITest(t, func() error { printVerificationSession(session, "http://127.0.0.1:4321"); return nil })
	if err != nil {
		t.Fatal(err)
	}
	requireCLITestOutputContains(t, output, "verdict: verified", "currentness: stale", "next_command: readyci sessions rerun --api-url http://127.0.0.1:4321 session_123")
	if strings.Contains(output, "sessions retry") {
		t.Fatal("retry would repeat the stale source")
	}
	if verificationVerdictExit(session) == nil {
		t.Fatal("stale passing result must still exit nonzero")
	}
}

func TestVerificationRecoveryRejectsInvalidRecipes(t *testing.T) {
	for _, classification := range []string{"", preflightapi.ClassificationPartialVerification} {
		for _, invalid := range []string{"missing", "unsupported", "tampered", "non-rerunnable"} {
			t.Run(classification+"/"+invalid, func(t *testing.T) {
				recipe := recoveryTestRecipe(t)
				switch invalid {
				case "missing":
					recipe = preflightapi.VerificationRecipe{}
				case "unsupported":
					recipe.SchemaVersion = "future.v2"
				case "tampered":
					recipe.ChangeMode = "staged"
					recipe.RequestSecrets = true
				case "non-rerunnable":
					recipe.Rerunnable = false
				}
				session := preflightapi.VerificationSession{SessionID: "session_123", State: "terminal", Verdict: "verified", Currentness: "stale", Classification: classification, Recipe: recipe}
				if classification != "" {
					session.Verdict = "checks_passed"
				}
				output, err := captureStdoutForCLITest(t, func() error { printVerificationSession(session, ""); return nil })
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(output, "next_command:") {
					t.Fatalf("recovery used an invalid recipe:\n%s", output)
				}
				requireCLITestOutputContains(t, output, "recipe", "reviewed settings")
			})
		}
	}
}

func TestStaleFailureDoesNotRecommendBlindRerun(t *testing.T) {
	for _, verdict := range []string{"failed", "blocked", "insufficient_evidence"} {
		t.Run(verdict, func(t *testing.T) {
			session := preflightapi.VerificationSession{SessionID: "session_123", State: "terminal", Verdict: verdict, Currentness: "stale", Recipe: recoveryTestRecipe(t), TerminalReason: "Required check did not succeed"}
			output, err := captureStdoutForCLITest(t, func() error { printVerificationSession(session, ""); return nil })
			if err != nil {
				t.Fatal(err)
			}
			requireCLITestOutputContains(t, output, "currentness: stale", "Inspect")
			if strings.Contains(output, "next_command:") {
				t.Fatalf("rerun guidance ignored recorded failure:\n%s", output)
			}
		})
	}
}

func recoveryTestRecipe(t *testing.T) preflightapi.VerificationRecipe {
	t.Helper()
	recipe := preflightapi.VerificationRecipe{SchemaVersion: preflightapi.VerificationRecipeSchemaVersion, Rerunnable: true, ChangeMode: "working", Source: &preflightapi.VerificationSourceRecipe{}}
	var err error
	recipe.Digest, err = preflightapi.VerificationRecipeDigest(recipe)
	if err != nil {
		t.Fatal(err)
	}
	return recipe
}
