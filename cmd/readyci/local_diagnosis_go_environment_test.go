package main

import (
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestLocalGoCrossInstallFailureAfterServiceStartWarning(t *testing.T) {
	code := 1
	const warning = "invoke-rc.d: policy-rc.d denied execution of start."
	const diagnostic = "go: cannot install cross-compiled binaries when GOBIN is set"
	events := []preflightapi.RunEvent{
		{ID: 1, Type: preflightapi.EventStdout, Phase: "command", Step: "build", Data: warning + "\nSetting up python3-pip ...\n"},
		{ID: 2, Type: preflightapi.EventStdout, Phase: "command", Step: "build", Data: "Build for dragonfly\n"},
		{ID: 3, Type: preflightapi.EventStderr, Phase: "command", Step: "build", Data: diagnostic + "\n"},
		{ID: 4, Type: preflightapi.EventCommandExited, ExitCode: &code},
	}
	analysis := localAnalyzeRunFailure(preflightapi.StatusFailed, "go install ./... && go test ./...", &code, events, "")
	if analysis.Classification != preflightapi.ClassificationEnvironmentSetupFailed || analysis.AgentAction != preflightapi.AgentActionReportSetup || analysis.Retryable || analysis.PrimaryFile != "" || analysis.Summary != diagnostic {
		t.Fatalf("analysis = %+v, want actual Go environment cause without a source repair target", analysis)
	}
	if code != 1 || *events[len(events)-1].ExitCode != 1 {
		t.Fatal("diagnosis changed the native command exit")
	}
	if !strings.HasPrefix(events[0].Data, warning+"\n") {
		t.Fatal("diagnosis changed the original service warning")
	}
}

func TestLocalGoEnvironmentFailurePreservesAuthoritativeStatus(t *testing.T) {
	code := 1
	events := []preflightapi.RunEvent{{Type: preflightapi.EventStderr, Phase: "command", Data: "go: cannot install cross-compiled binaries when GOBIN is set\n"}}
	for status, want := range map[string]string{
		preflightapi.StatusSucceeded: preflightapi.ClassificationSuccess,
		preflightapi.StatusTimedOut:  preflightapi.ClassificationTimeout,
		preflightapi.StatusCancelled: preflightapi.ClassificationCancelled,
		preflightapi.StatusDenied:    preflightapi.ClassificationPolicyDenied,
	} {
		analysis := localAnalyzeRunFailure(status, "go install ./...", &code, events, "")
		if analysis.Classification != want || code != 1 {
			t.Fatalf("status %s analysis=%+v exit=%d", status, analysis, code)
		}
	}
}

func localWrappedStorageRegressionEvents(code *int) []preflightapi.RunEvent {
	return []preflightapi.RunEvent{
		{ID: 11, Type: preflightapi.EventStdout, Phase: "command", Data: "--- FAIL: TestStorage (0.04s)\n    integrationtest_builder.go:207:\n        error:\n          got non-nil error\n        got:\n          e\"mkdir /tmp/hugo-integration-test1: no space left on device\"\n"},
		{ID: 12, Type: preflightapi.EventStderr, Phase: "command", Data: "Filesystem 1-blocks Used Available Capacity Mounted on\n/dev/vdc 33501757440 33484980224 0 100% /tmp\n"},
		{ID: 13, Type: preflightapi.EventStderr, Phase: "command", Message: preflightapi.CommandFailureScopeMessage, ExitCode: code},
		{ID: 14, Type: preflightapi.EventCommandExited, ExitCode: code},
	}
}

func TestLocalWrappedStorageFailureKeepsCauseAndCapacityEvidence(t *testing.T) {
	code := 1
	analysis := localAnalyzeRunFailure(preflightapi.StatusFailed, "go test ./...", &code, localWrappedStorageRegressionEvents(&code), "")
	if analysis.Classification != preflightapi.ClassificationEnvironmentSetupFailed || analysis.AgentAction != preflightapi.AgentActionReportSetup || analysis.Retryable || analysis.PrimaryFile != "" || code != 1 {
		t.Fatalf("analysis=%+v exit=%d, want nonretryable environment diagnosis without source repair", analysis, code)
	}
	if !strings.HasPrefix(analysis.Summary, "Observed mkdir /tmp/hugo-integration-test1:") || !strings.Contains(analysis.Summary, "/tmp reported zero available bytes. Inspect filesystem") {
		t.Fatalf("summary must distinguish observed evidence from root-cause proof: %q", analysis.Summary)
	}
	if len(analysis.Evidence) < 2 || analysis.Evidence[0].EventID != 11 || analysis.Evidence[1].EventID != 12 || analysis.Evidence[0].Path != "" || analysis.Evidence[1].Path != "" {
		t.Fatalf("actual-error and matching capacity locations were not retained first: %+v", analysis.Evidence)
	}
}

func TestLocalWrappedStorageFailurePreservesAuthoritativeStatus(t *testing.T) {
	code := 1
	for status, want := range map[string]string{
		preflightapi.StatusSucceeded: preflightapi.ClassificationSuccess,
		preflightapi.StatusTimedOut:  preflightapi.ClassificationTimeout,
		preflightapi.StatusCancelled: preflightapi.ClassificationCancelled,
		preflightapi.StatusDenied:    preflightapi.ClassificationPolicyDenied,
	} {
		analysis := localAnalyzeRunFailure(status, "go test ./...", &code, localWrappedStorageRegressionEvents(&code), "")
		if analysis.Classification != want || code != 1 {
			t.Fatalf("status %s analysis=%+v exit=%d", status, analysis, code)
		}
	}
}
