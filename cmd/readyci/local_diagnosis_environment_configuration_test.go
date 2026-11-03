package main

import (
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

const missingDatabaseURLDiagnostic = "FATAL: DATABASE_URL must be configured before tests start"

func TestLocalDiagnosisClassifiesMissingEnvironmentConfigurationBeforeTests(t *testing.T) {
	for _, command := range []string{"npm test", "pytest"} {
		t.Run(command, func(t *testing.T) {
			exitCode := 1
			events := []preflightapi.RunEvent{{ID: 9, Type: preflightapi.EventStderr, Phase: "command", Data: missingDatabaseURLDiagnostic + "\n"}}
			analysis := localAnalyzeRunFailure(preflightapi.StatusFailed, command, &exitCode, events, "")
			if analysis.Classification != preflightapi.ClassificationEnvironmentSetupFailed || analysis.AgentAction != preflightapi.AgentActionReportSetup ||
				analysis.Retryable || analysis.PrimaryFile != "" {
				t.Fatalf("analysis = %+v, want environment_setup_failed/report_setup", analysis)
			}
			if want := "The test command exited before reporting any test failure: required environment variable DATABASE_URL is not configured."; analysis.Summary != want {
				t.Fatalf("summary = %q, want %q", analysis.Summary, want)
			}
			if len(analysis.Evidence) == 0 || analysis.Evidence[0].Text != missingDatabaseURLDiagnostic || analysis.Evidence[0].EventID != 9 {
				t.Fatalf("evidence = %+v", analysis.Evidence)
			}
		})
	}
}

func TestLocalDiagnosisKeepsTestFailuresThatMentionEnvironmentVariables(t *testing.T) {
	exitCode := 1
	for name, output := range map[string]string{
		"go test failure":   "--- FAIL: TestConfig (0.00s)\n    config_test.go:12: DATABASE_URL must be configured\nFAIL\n",
		"pytest failure":    "E       KeyError: 'DATABASE_URL'\n<frozen os>:714: KeyError\nFAILED tests/test_env.py::test_db - KeyError: 'DATABASE_URL'\n",
		"assertion failure": "E   AssertionError: DATABASE_URL is not set\n",
		"skip notice":       "DATABASE_URL is not set; skipping integration tests\nnpm ERR! Test failed.\n",
	} {
		t.Run(name, func(t *testing.T) {
			analysis := localAnalyzeRunFailure(preflightapi.StatusFailed, "npm test", &exitCode, []preflightapi.RunEvent{{Type: preflightapi.EventStderr, Phase: "command", Data: output}}, "")
			if analysis.Classification != preflightapi.ClassificationTestFailed {
				t.Fatalf("analysis = %+v, want test_failed", analysis)
			}
		})
	}
	analysis := localAnalyzeRunFailure(preflightapi.StatusFailed, "npm test", &exitCode,
		[]preflightapi.RunEvent{{Type: preflightapi.EventStderr, Phase: "command", Data: missingDatabaseURLDiagnostic + "\n"}}, "log stream failed: connection reset")
	if analysis.Classification != preflightapi.ClassificationTestFailed {
		t.Fatalf("worker failure reason displaced by repository output: %+v", analysis)
	}
}

func TestLocalDiagnosisTestCommandWithoutTestOutputDoesNotClaimTestsFailed(t *testing.T) {
	exitCode := 1
	analysis := localAnalyzeRunFailure(preflightapi.StatusFailed, "npm test", &exitCode, nil, "")
	if analysis.Classification != preflightapi.ClassificationTestFailed || analysis.Summary != "The test command failed without reporting a test failure." {
		t.Fatalf("analysis = %+v", analysis)
	}
}

func TestHostedMissingEnvironmentConfigurationKeepsLocalEvidence(t *testing.T) {
	events := []preflightapi.RunEvent{
		{ID: 4, Type: preflightapi.EventStdout, Phase: "command", Data: "> app@1.0.0 test\n> node test/run.js\n"},
		{ID: 5, Type: preflightapi.EventStderr, Phase: "command", Data: missingDatabaseURLDiagnostic + "\n"},
	}
	summary := "The test command exited before reporting any test failure: required environment variable DATABASE_URL is not configured."
	result := remoteRunResult{
		Status: preflightapi.StatusFailed, ExitCode: 1, Events: events,
		Classification: preflightapi.ClassificationEnvironmentSetupFailed, AgentAction: preflightapi.AgentActionReportSetup, FailureSummary: summary,
	}
	analysis := analyzeRemoteRunFailure(result, nil, "npm test")
	if analysis.Classification != preflightapi.ClassificationEnvironmentSetupFailed || analysis.AgentAction != preflightapi.AgentActionReportSetup || analysis.Summary != summary {
		t.Fatalf("hosted verdict changed: %+v", analysis)
	}
	if len(analysis.Evidence) == 0 || analysis.Evidence[0].EventID != 5 || analysis.Evidence[0].Text != missingDatabaseURLDiagnostic {
		t.Fatalf("local evidence did not agree with hosted classification: %+v", analysis.Evidence)
	}
}
