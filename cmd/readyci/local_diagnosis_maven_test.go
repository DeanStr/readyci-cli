package main

import (
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func sparkFailedMavenTestEvents() []preflightapi.RunEvent {
	exit := 1
	events := []preflightapi.RunEvent{
		{ID: 11, Type: preflightapi.EventStdout, Phase: "command", Data: "PythonWorkerFactorySuite:\n/usr/bin/python3: Error while finding module specification for 'pyspark.testing.non_existing_worker_module' (ModuleNotFoundError: No module named 'pyspark')\n- createSimpleWorker() fails with a timeout error if worker does not connect back\n"},
		{ID: 12, Type: preflightapi.EventStdout, Phase: "command", Data: "RocksDBBackendFsHistoryProviderSuite:\n- end event reparse applies the line length limit *** FAILED ***\n  java.io.IOException: No space left on device\n"},
		{ID: 13, Type: preflightapi.EventStdout, Phase: "command", Data: "[ERROR] Failed to execute goal org.scalatest:scalatest-maven-plugin:2.2.0:test (test) on project spark-core_2.13: There are test failures -> [Help 1]\n"},
		{ID: 14, Type: preflightapi.EventCommandExited, ExitCode: &exit},
	}
	for i := range events[:len(events)-1] {
		events[i].Step = "unit-tests"
	}
	return events
}
func TestLocalMavenTestFailureOutranksExpectedWorkerModuleError(t *testing.T) {
	exit := 1
	got := localAnalyzeRunFailureWithNetworkMode(preflightapi.StatusFailed, "./build/mvn -pl core test -fae", &exit, sparkFailedMavenTestEvents(), "", "default")
	if got.Classification != preflightapi.ClassificationTestFailed {
		t.Fatalf("classification=%s summary=%q, want completed Maven test failure rather than a passed negative worker-module diagnostic", got.Classification, got.Summary)
	}
	if !strings.Contains(got.Summary, "scalatest-maven-plugin") || strings.Contains(got.Summary, "Missing Python") || got.PrimaryFile != "" {
		t.Fatalf("misleading summary/file: %+v", got)
	}
	if len(got.Evidence) < 2 || len(got.Evidence) > 12 || got.Evidence[0].EventID != 13 || got.Evidence[1].EventID != 12 {
		t.Fatalf("missing runner evidence: %+v", got.Evidence)
	}
}

func TestLocalMavenTestFailurePreservesTerminalFailurePrecedence(t *testing.T) {
	for _, tc := range []struct{ name, status, reason, want string }{
		{"timeout", preflightapi.StatusTimedOut, "", preflightapi.ClassificationTimeout},
		{"cancelled", preflightapi.StatusCancelled, "", preflightapi.ClassificationCancelled},
		{"denied", preflightapi.StatusDenied, "", preflightapi.ClassificationPolicyDenied},
		{"containment", preflightapi.StatusFailed, "firecracker shutdown failed: remove firecracker cgroup: device or resource busy", preflightapi.ClassificationWorkerInternalError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exit := 1
			got := localAnalyzeRunFailureWithNetworkMode(tc.status, "./build/mvn -pl core test -fae", &exit, sparkFailedMavenTestEvents(), tc.reason, "default")
			if got.Classification != tc.want {
				t.Fatalf("got %+v, want %s", got, tc.want)
			}
		})
	}
}

func TestLocalMavenTestFailureDoesNotHidePreparationOrQuotedText(t *testing.T) {
	for _, tc := range []struct{ name, command, phase string }{
		{"real preparation failure", "python prepare.py", "prepare"},
		{"submitted fixture", "printf '%s' '- named test *** FAILED ***\n[ERROR] Failed to execute goal org.scalatest:scalatest-maven-plugin:2.2.0:test (test) on project example: There are test failures'", "command"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exit := 1
			command := tc.command
			events := []preflightapi.RunEvent{
				{Type: preflightapi.EventStderr, Phase: tc.phase, Data: "ModuleNotFoundError: No module named 'pyspark'\n"},
				{Type: preflightapi.EventCommandExited, ExitCode: &exit},
			}
			got := localAnalyzeRunFailureWithNetworkMode(preflightapi.StatusFailed, command, &exit, events, "", "default")
			if got.Classification != preflightapi.ClassificationDependencySetupFailed {
				t.Fatalf("got %+v", got)
			}
		})
	}
}
