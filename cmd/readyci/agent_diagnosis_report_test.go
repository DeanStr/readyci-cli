package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestAgentReportProcessExitCodeContract(t *testing.T) {
	for name, tt := range map[string]struct {
		report agentVerifyReport
		want   int
	}{
		"success": {
			report: agentVerifyReport{Status: preflightapi.StatusSucceeded, Terminal: true, Classification: preflightapi.ClassificationSuccess, ExitCode: intPtr(0)},
			want:   cliExitSuccess,
		},
		"remote failure collapses raw exit": {
			report: agentVerifyReport{Status: preflightapi.StatusFailed, Terminal: true, Classification: preflightapi.ClassificationTestFailed, ExitCode: intPtr(17)},
			want:   cliExitRemoteFailure,
		},
		"cache storage exceeded": {
			report: agentVerifyReport{Status: preflightapi.StatusFailed, Terminal: true, Classification: preflightapi.ClassificationCacheStorageExceeded, ExitCode: intPtr(1)},
			want:   cliExitRemoteFailure,
		},
		"config": {
			report: agentVerifyReport{Status: "error", Terminal: true, Classification: preflightapi.ClassificationConfigInvalid},
			want:   cliExitUsageOrConfig,
		},
		"auth": {
			report: agentVerifyReport{Status: "error", Terminal: true, Classification: preflightapi.ClassificationAuthForbidden},
			want:   cliExitAuthOrPolicy,
		},
		"policy denied": {
			report: agentVerifyReport{Status: preflightapi.StatusDenied, Terminal: true, Classification: preflightapi.ClassificationPolicyDenied},
			want:   cliExitAuthOrPolicy,
		},
		"unknown": {
			report: agentVerifyReport{Status: "unknown", Terminal: false, Classification: preflightapi.ClassificationVerificationUnknown},
			want:   cliExitVerificationUnknown,
		},
		"rate limited": {
			report: agentVerifyReport{Status: "error", Terminal: true, Classification: preflightapi.ClassificationRateLimited},
			want:   cliExitTemporaryUnavailable,
		},
		"precondition failed": {
			report: agentVerifyReport{Status: "error", Terminal: true, Classification: preflightapi.ClassificationPreconditionFailed},
			want:   cliExitUsageOrConfig,
		},
		"internal": {
			report: agentVerifyReport{Status: "error", Terminal: true, Classification: preflightapi.ClassificationWorkerInternalError},
			want:   cliExitInternal,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := agentReportProcessExitCode(tt.report); got != tt.want {
				t.Fatalf("agentReportProcessExitCode(%+v) = %d, want %d", tt.report, got, tt.want)
			}
		})
	}
}

func TestAgentCommandErrorReportMapsClassificationToActionAndExit(t *testing.T) {
	err := errors.New("config file is invalid")
	report := agentCommandErrorReport(preflightapi.ClassificationConfigInvalid, err)
	if report.Status != "error" || !report.Terminal || report.Classification != preflightapi.ClassificationConfigInvalid {
		t.Fatalf("error report state = %+v", report)
	}
	if report.Summary != err.Error() || report.Error != err.Error() {
		t.Fatalf("error report text = summary %q error %q", report.Summary, report.Error)
	}
	if report.CLIExitCode != cliExitUsageOrConfig || report.AgentAction != preflightapi.AgentActionReportSetup {
		t.Fatalf("config error report = %+v, want config exit/action", report)
	}

	report = agentCommandErrorReport(preflightapi.ClassificationWorkerUnavailable, errors.New("no worker"))
	if !report.Retryable || report.AgentAction != preflightapi.AgentActionWait || report.CLIExitCode != cliExitTemporaryUnavailable {
		t.Fatalf("worker error report = %+v, want retryable wait temporary-unavailable exit", report)
	}
}

func TestRecommendedNextStepCoversEveryAgentAction(t *testing.T) {
	tests := map[string]struct {
		action string
		check  string
		want   string
	}{
		"fix code":           {action: preflightapi.AgentActionFixCode, want: "Inspect the failure evidence and relevant source"},
		"retry":              {action: preflightapi.AgentActionRetry, want: "Retry once"},
		"retry or split":     {action: preflightapi.AgentActionRetryOrSplit, want: "Retry once"},
		"reduce scope":       {action: preflightapi.AgentActionReduceScope, want: "Reduce the sync scope"},
		"credentials":        {action: preflightapi.AgentActionAskForCredentials, want: "scoped Ready CI token"},
		"wait":               {action: preflightapi.AgentActionWait, want: "Wait briefly"},
		"wait infra":         {action: preflightapi.AgentActionWaitOrReportInfra, want: "Wait briefly"},
		"none":               {action: preflightapi.AgentActionNone, want: "No follow-up"},
		"default with check": {action: "inspect", check: "web smoke", want: "readyci verify --json --progress=status --api-url https://staging.readyci.test --check 'web smoke'"},
		"default no check":   {action: "inspect", want: "Inspect the logs and rerun Ready CI"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := recommendedNextStep(agentDiagnosis{Action: tt.action}, "https://staging.readyci.test", tt.check, false)
			if !strings.Contains(got, tt.want) {
				t.Fatalf("recommended next step = %q, want substring %q", got, tt.want)
			}
		})
	}
}

func TestRecommendedNextStepForCacheStorageExceeded(t *testing.T) {
	got := recommendedNextStep(agentDiagnosis{
		Kind:   preflightapi.ClassificationCacheStorageExceeded,
		Action: preflightapi.AgentActionReduceScope,
	}, "", "rust-deps", false)
	for _, want := range []string{"Prune or purge the Ready CI cache", "remove bulky cache paths"} {
		if !strings.Contains(got, want) {
			t.Fatalf("recommended next step = %q, want substring %q", got, want)
		}
	}
}

func TestNextActionsPreserveSecretRequestInRerunCommand(t *testing.T) {
	actions := nextActionsForRun(agentVerifyRunReport{
		APIURL:           "https://staging.readyci.test",
		SecretsRequested: true,
		Status:           preflightapi.StatusFailed,
		Check:            "web",
		Classification:   preflightapi.ClassificationTestFailed,
		AgentAction:      preflightapi.AgentActionRetry,
		Summary:          "retry this check",
	})
	for _, action := range actions {
		if action.Kind == "rerun" {
			want := "readyci verify --json --progress=status --api-url https://staging.readyci.test --check web --request-secrets"
			if action.Command != want {
				t.Fatalf("secret rerun action command = %q, want %q", action.Command, want)
			}
			return
		}
	}
	t.Fatalf("next actions = %+v, want rerun action", actions)
}

func TestRepairLogGuidanceUsesAvailableEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, action string
		evidence     []agentEvidence
		want         string
	}{
		{name: "actionable test failure", action: "fix_code", evidence: []agentEvidence{{Text: "expected 401, received 200"}}, want: "if the supplied failure evidence is insufficient"},
		{name: "test failure without evidence", action: "fix_code", want: "before deciding how to recover"},
		{name: "infrastructure failure", action: "wait_or_report_infra", evidence: []agentEvidence{{Text: "worker unavailable"}}, want: "before deciding how to recover"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			packet := buildAgentRepairPacket(agentDiagnosis{Action: tc.action}, "go test ./...", "", "readyci runs logs run_123", tc.evidence, nil, nil)
			if len(packet.Commands) != 1 || !strings.Contains(packet.Commands[0].Reason, tc.want) {
				t.Fatalf("unexpected recovery commands: %+v", packet.Commands)
			}
		})
	}
}

func TestNonTestFailuresDoNotRecommendInvestigatingTests(t *testing.T) {
	for _, kind := range []string{preflightapi.ClassificationLintFailed, preflightapi.ClassificationBuildFailed, preflightapi.ClassificationTypecheckFailed} {
		t.Run(kind, func(t *testing.T) {
			next := recommendedNextStep(agentDiagnosis{Kind: kind, Action: preflightapi.AgentActionFixCode}, "", "check", false)
			if strings.Contains(next, "failing test") {
				t.Fatalf("%s refers to a nonexistent failing test: %s", kind, next)
			}
			if !strings.Contains(next, "failure evidence") {
				t.Fatalf("missing investigation guidance: %s", next)
			}
		})
	}
}

func TestTestFailureRecommendsInspectingFailingTest(t *testing.T) {
	next := recommendedNextStep(agentDiagnosis{Kind: preflightapi.ClassificationTestFailed, Action: preflightapi.AgentActionFixCode}, "", "check", false)
	if !strings.Contains(next, "failing test and implementation") {
		t.Fatalf("missing test-specific guidance: %s", next)
	}
}
