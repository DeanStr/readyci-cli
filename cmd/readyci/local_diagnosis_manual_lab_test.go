package main

import (
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestLocalPrimaryFailureSurvivesAlwaysStepOutput(t *testing.T) {
	code := 1
	analysis := localAnalyzeRunFailure(preflightapi.StatusFailed, "make ci", &code, []preflightapi.RunEvent{
		{ID: 1, Type: preflightapi.EventStderr, Phase: "command", Step: "check", Data: "ModuleNotFoundError: No module named 'requests'\n"},
		{ID: 2, Type: preflightapi.EventStderr, Phase: "command", Step: "check", ExitCode: &code, Message: preflightapi.CommandFailureScopeMessage},
		{ID: 3, Type: preflightapi.EventStdout, Phase: "command", Step: "cleanup", Data: "cleanup completed\n"},
		{ID: 4, Type: preflightapi.EventCommandExited, ExitCode: &code},
	}, "")
	if analysis.Classification != preflightapi.ClassificationDependencySetupFailed || !strings.Contains(analysis.Summary, "requests") {
		t.Fatalf("cleanup displaced the primary failure: %+v", analysis)
	}
}

func TestLocalManualLabFailureDiagnosis(t *testing.T) {
	for _, tc := range []struct {
		name, command, output, classification, action, summary string
		code                                                   int
	}{
		{
			name: "cpython skipped import before failing socket test", command: "xvfb-run make ci", code: 2,
			output: "test_winsound skipped -- No module named 'winsound'\n" +
				"ERROR: testStream (test.test_socket.BasicVSOCKTest.testStream)\n" +
				"OSError: [Errno 19] No such device\nFAILED (errors=1)\n",
			classification: preflightapi.ClassificationTestFailed, action: preflightapi.AgentActionFixCode, summary: "testStream",
		},
		{
			name: "uv terminal filesystem prerequisite after package warning", command: "prepare-filesystems && cargo nextest run", code: 32,
			output: "\x1b[0;1;31m/usr/lib/tmpfiles.d/systemd-network.conf:10: Failed to resolve user 'systemd-network': No such process\x1b[0m\n" +
				"Creating btrfs filesystem\nmount: /btrfs: unknown filesystem type 'btrfs'.\n",
			classification: preflightapi.ClassificationEnvironmentSetupFailed, action: preflightapi.AgentActionReportSetup, summary: "unknown filesystem type 'btrfs'",
		},
		{
			name: "curl endpoint command before actual socket error", command: "make test-ci", code: 2,
			output: "../src/curl --resolve example.com:42881:[::1] http://example.com:42881/1324\n" +
				"02:33:34.328305 * Immediate connect fail for ::1: Cannot assign requested address\n" +
				"curl: (7) Failed to connect to example.com:42881 after 0 ms: Could not connect to server\n",
			classification: preflightapi.ClassificationTestFailed, action: preflightapi.AgentActionFixCode, summary: "Cannot assign requested address",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			analysis := localAnalyzeRunFailure(preflightapi.StatusFailed, tc.command, &tc.code, []preflightapi.RunEvent{
				{ID: 1, Type: preflightapi.EventStdout, Phase: "command", Data: tc.output},
				{ID: 2, Type: preflightapi.EventCommandExited, ExitCode: &tc.code},
			}, "")
			if analysis.Classification != tc.classification || analysis.AgentAction != tc.action || !strings.Contains(analysis.Summary, tc.summary) || analysis.PrimaryFile != "" {
				t.Fatalf("analysis = %+v, want %s / %s / %q without a primary source file", analysis, tc.classification, tc.action, tc.summary)
			}
		})
	}
}

func TestLocalManualLabDiagnosisPrefersFailedStep(t *testing.T) {
	code := 1
	for _, tc := range []struct{ name, earlier, final, command, classification, path, summary string }{
		{
			"recovered warning before unittest failure", "src/install.js:20: error: recovered optional plugin failure\n",
			"ERROR: testStream (tests.SocketTests.testStream)\nOSError: [Errno 19] No such device\nFAILED (errors=1)\n",
			"make ci", preflightapi.ClassificationTestFailed, "", "testStream",
		},
		{
			"recovered optional import before compiler error", "ModuleNotFoundError: No module named 'optional_plugin'\n",
			"src/main.go:42: undefined: missing\n", "prepare-optional-plugin; go build ./...", preflightapi.ClassificationBuildFailed, "src/main.go", "undefined: missing",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			analysis := localAnalyzeRunFailure(preflightapi.StatusFailed, tc.command, &code, []preflightapi.RunEvent{
				{ID: 1, Type: preflightapi.EventStderr, Phase: "command", Step: "optional-setup", Data: tc.earlier},
				{ID: 2, Type: preflightapi.EventStderr, Phase: "command", Step: "check", Data: tc.final},
				{ID: 3, Type: preflightapi.EventCommandExited, ExitCode: &code},
			}, "")
			if analysis.Classification != tc.classification || analysis.PrimaryFile != tc.path || !strings.Contains(analysis.Summary, tc.summary) {
				t.Fatalf("analysis = %+v, want %s / %q / %q", analysis, tc.classification, tc.path, tc.summary)
			}
		})
	}
}

func TestLocalManualLabDiagnosisKeepsSidecarsOutOfPrimaryScope(t *testing.T) {
	code := 1
	analysis := localAnalyzeRunFailure(preflightapi.StatusFailed, "go build ./...", &code, []preflightapi.RunEvent{
		{ID: 1, Type: preflightapi.EventStderr, Phase: "command", Data: "src/main.go:42: undefined: missing\n"},
		{ID: 2, Type: preflightapi.EventStderr, Phase: "command", Service: "helper", Data: "ModuleNotFoundError: No module named 'sidecar_plugin'\n"},
		{ID: 3, Type: preflightapi.EventCommandExited, ExitCode: &code},
	}, "")
	if analysis.Classification != preflightapi.ClassificationBuildFailed || analysis.PrimaryFile != "src/main.go" || !strings.Contains(analysis.Summary, "undefined: missing") {
		t.Fatalf("sidecar replaced repository failure: %+v", analysis)
	}
}

func TestLocalLabFallbackDoesNotReplaceHostedDiagnosis(t *testing.T) {
	result := remoteRunResult{
		Status: preflightapi.StatusFailed, ExitCode: 32,
		Classification: preflightapi.ClassificationBuildFailed, AgentAction: preflightapi.AgentActionFixCode,
		FailureSummary: "Reviewed hosted failure", PrimaryFile: "src/authoritative.go", Retryable: true,
		Events: []preflightapi.RunEvent{{Type: preflightapi.EventStderr, Phase: "command", Data: "mount: /btrfs: unknown filesystem type 'btrfs'.\n"}},
	}
	analysis := analyzeRemoteRunFailure(result, nil, "prepare-filesystems && cargo test")
	if analysis.Classification != result.Classification || analysis.AgentAction != result.AgentAction || analysis.Retryable != result.Retryable || analysis.Summary != result.FailureSummary || analysis.PrimaryFile != result.PrimaryFile {
		t.Fatalf("fallback replaced hosted authority: %+v", analysis)
	}
}

func TestLocalLabPrimaryFileNormalization(t *testing.T) {
	for _, tc := range []struct{ text, path string }{
		{"curl --resolve example.com:42881:[::1] http://example.com:42881/1324", ""},
		{"curl: (7) Failed to connect to example.com:42881 after 0 ms", ""},
		{"dial tcp example.test:443: connection refused", ""},
		{"\x1b[31msrc/check.go:42: undefined: missing\x1b[0m", "src/check.go"},
		{"could not connect to src/example.com:42: invalid source token", "src/example.com"},
	} {
		if got := localPrimaryFile(tc.text); got != tc.path {
			t.Errorf("localPrimaryFile(%q) = %q, want %q", tc.text, got, tc.path)
		}
	}
}

func TestLocalSkippedImportAndCompilerDiagnosisPreservesChunkedOutput(t *testing.T) {
	const skipped = "\x1b[33mtest_winsound skipped -- No module named 'winsound'\x1b[0m\n"
	const compiler = "\x1b[31msrc/main.c:42: error: expected expression before '}' token\x1b[0m\n"
	code := 2
	for _, output := range []string{skipped, compiler} {
		for split := 1; split < len(output); split++ {
			chunks := []preflightapi.RunEvent{
				{ID: 11, Type: preflightapi.EventStdout, Phase: "command", Step: "check", Data: output[:split]},
				{ID: 12, Type: preflightapi.EventStderr, Phase: "command", Step: "check", Data: "ordinary progress\n"},
				{ID: 13, Type: preflightapi.EventStdout, Phase: "command", Step: "check", Data: output[split:]},
			}
			if output == skipped {
				chunks = append(chunks, preflightapi.RunEvent{ID: 14, Type: preflightapi.EventStderr, Phase: "command", Step: "check", Data: compiler})
			}
			chunks = append(chunks, preflightapi.RunEvent{Type: preflightapi.EventCommandExited, ExitCode: &code})
			got := localAnalyzeRunFailure(preflightapi.StatusFailed, "make ci", &code, chunks, "")
			if got.Classification != preflightapi.ClassificationBuildFailed || got.PrimaryFile != "src/main.c" || !strings.Contains(got.Summary, "expected expression") {
				t.Fatalf("split %d: %+v", split, got)
			}
			for _, item := range got.Evidence {
				if strings.Contains(item.Text, "winsound") || strings.Contains(item.Text, "\x1b") {
					t.Fatalf("unfiltered split record in evidence: %+v", item)
				}
			}
		}
	}
}

func TestLocalFatalImportDiagnosisPreservesChunkedOutput(t *testing.T) {
	const output = "ModuleNotFoundError: No module named 'required_plugin'\n"
	code := 1
	for split := 1; split < len(output); split++ {
		events := []preflightapi.RunEvent{
			{ID: 21, Type: preflightapi.EventStderr, Phase: "command", Data: output[:split]},
			{ID: 22, Type: preflightapi.EventStdout, Phase: "command", Data: "ordinary progress\n"},
			{ID: 23, Type: preflightapi.EventStderr, Phase: "command", Data: output[split:]},
			{Type: preflightapi.EventCommandExited, ExitCode: &code},
		}
		got := localAnalyzeRunFailure(preflightapi.StatusFailed, "python app.py", &code, events, "")
		if got.Classification != preflightapi.ClassificationDependencySetupFailed || !strings.Contains(got.Summary, "required_plugin") {
			t.Fatalf("split %d: %+v", split, got)
		}
		if len(got.Evidence) != 1 || got.Evidence[0].EventID != 21 || got.Evidence[0].Line != 1 {
			t.Fatalf("split %d evidence: %+v", split, got.Evidence)
		}
	}
}
