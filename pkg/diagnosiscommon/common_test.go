package diagnosiscommon

import (
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestClassificationPolicy(t *testing.T) {
	tests := []struct {
		classification string
		retryable      bool
		action         string
	}{
		{preflightapi.ClassificationSuccess, false, preflightapi.AgentActionNone},
		{preflightapi.ClassificationPending, false, preflightapi.AgentActionWait},
		{preflightapi.ClassificationTestFailed, false, preflightapi.AgentActionFixCode},
		{preflightapi.ClassificationLintFailed, false, preflightapi.AgentActionFixCode},
		{preflightapi.ClassificationBuildFailed, false, preflightapi.AgentActionFixCode},
		{preflightapi.ClassificationTypecheckFailed, false, preflightapi.AgentActionFixCode},
		{preflightapi.ClassificationDependencySetupFailed, false, preflightapi.AgentActionFixCode},
		{preflightapi.ClassificationEnvironmentSetupFailed, false, preflightapi.AgentActionReportSetup},
		{preflightapi.ClassificationAuthMissing, false, preflightapi.AgentActionAskForCredentials},
		{preflightapi.ClassificationAuthForbidden, false, preflightapi.AgentActionAskForCredentials},
		{preflightapi.ClassificationPolicyDenied, false, preflightapi.AgentActionReportSetup},
		{preflightapi.ClassificationStateConflict, false, preflightapi.AgentActionReportSetup},
		{preflightapi.ClassificationPreconditionFailed, false, preflightapi.AgentActionReportSetup},
		{preflightapi.ClassificationCapacityExhausted, true, preflightapi.AgentActionWait},
		{preflightapi.ClassificationBillingRequired, false, preflightapi.AgentActionReportSetup},
		{preflightapi.ClassificationBillingBlocked, false, preflightapi.AgentActionReportSetup},
		{preflightapi.ClassificationRateLimited, true, preflightapi.AgentActionWait},
		{preflightapi.ClassificationConfigInvalid, false, preflightapi.AgentActionReportSetup},
		{preflightapi.ClassificationProjectNotFound, false, preflightapi.AgentActionReportSetup},
		{preflightapi.ClassificationServiceSetupFailed, false, preflightapi.AgentActionReportSetup},
		{preflightapi.ClassificationNetworkNotAllowed, false, preflightapi.AgentActionReportSetup},
		{preflightapi.ClassificationTimeout, true, preflightapi.AgentActionRetryOrSplit},
		{preflightapi.ClassificationSyncTooLarge, false, preflightapi.AgentActionReduceScope},
		{preflightapi.ClassificationCacheStorageExceeded, false, preflightapi.AgentActionReduceScope},
		{preflightapi.ClassificationSyncFailed, true, preflightapi.AgentActionRetry},
		{preflightapi.ClassificationDependencyDownloadFailed, true, preflightapi.AgentActionRetry},
		{preflightapi.ClassificationWorkerUnavailable, true, preflightapi.AgentActionWait},
		{preflightapi.ClassificationWorkerCapabilityMismatch, false, preflightapi.AgentActionWaitOrReportInfra},
		{preflightapi.ClassificationTransportFailure, true, preflightapi.AgentActionWaitOrReportInfra},
		{preflightapi.ClassificationVerificationUnknown, true, preflightapi.AgentActionWaitOrReportInfra},
		{preflightapi.ClassificationWorkerInternalError, true, preflightapi.AgentActionWaitOrReportInfra},
		{preflightapi.ClassificationCancelled, false, preflightapi.AgentActionStop},
		{preflightapi.ClassificationInternalError, false, preflightapi.AgentActionReportSetup},
		{preflightapi.ClassificationPlatformInternalError, false, preflightapi.AgentActionReportSetup},
	}
	for _, tt := range tests {
		t.Run(tt.classification, func(t *testing.T) {
			if got := RetryableClassification(tt.classification); got != tt.retryable {
				t.Fatalf("RetryableClassification() = %t, want %t", got, tt.retryable)
			}
			if got := AgentActionForClassification(tt.classification); got != tt.action {
				t.Fatalf("AgentActionForClassification() = %q, want %q", got, tt.action)
			}
		})
	}
}

func TestEventMechanics(t *testing.T) {
	events := []preflightapi.RunEvent{
		{ID: 42, Type: preflightapi.EventStderr, Message: "command failed", DataBase64: "Zm9vL2Jhcl90ZXN0LmdvOjEyOiBmYWlsZWQK"},
		{Type: preflightapi.EventRunFailed, Message: "lifecycle message", Data: "private lifecycle data"},
	}
	if got := EventSearchText(events); got != "command failed\nfoo/bar_test.go:12: failed\n\nlifecycle message\n" {
		t.Fatalf("EventSearchText() = %q", got)
	}
	if got := EventSequence(events[0], 0); got != 42 {
		t.Fatalf("EventSequence() = %d, want 42", got)
	}
	if stream, ok := EventStream(events[0]); !ok || stream != "stderr" {
		t.Fatalf("EventStream() = %q, %t", stream, ok)
	}
}

func TestSharedRegistryRecognition(t *testing.T) {
	if !RegistryEgressReachabilityFailureText("lookup registry.npmjs.org: no such host") {
		t.Fatal("expected DNS failure to be recognized")
	}
	if RegistryEgressReachabilityFailureText("tests failed with an assertion") {
		t.Fatal("did not expect an assertion failure to be treated as egress")
	}
	if !RegistryLineFailureContextText("npm error request failed while getting package") {
		t.Fatal("expected package fetch context")
	}
	if !RegistryTransportFailureText("certificate signed by unknown authority") {
		t.Fatal("expected certificate transport failure")
	}
	if !DependencyRegistryMentioned("GET https://proxy.golang.org/mod/@v/list") {
		t.Fatal("expected Go registry host")
	}
	if !CommandHasDependencyDownloadContext("pnpm install --frozen-lockfile") {
		t.Fatal("expected package installation command")
	}
	if CommandHasDependencyDownloadContext("go test ./...") {
		t.Fatal("did not expect test-only command to imply a dependency download")
	}
}

func TestHostnameMentionedUsesTokenBoundaries(t *testing.T) {
	if !HostnameMentioned("https://proxy.golang.org/mod", "proxy.golang.org") {
		t.Fatal("expected exact hostname")
	}
	if HostnameMentioned("https://notproxy.golang.org/mod", "proxy.golang.org") {
		t.Fatal("did not expect hostname suffix match")
	}
}

func TestValidPrimaryFilePath(t *testing.T) {
	for _, path := range []string{"pkg/check_test.go", "web/src/App.tsx", `windows\\path\\file.go`} {
		if !ValidPrimaryFilePath(path) {
			t.Errorf("ValidPrimaryFilePath(%q) = false", path)
		}
	}
	for _, path := range []string{"", "../secret.txt", "pkg/../secret.txt", "https://example.com/a.go", "127.0.0.1"} {
		if ValidPrimaryFilePath(path) {
			t.Errorf("ValidPrimaryFilePath(%q) = true", path)
		}
	}
}

func TestDefiniteTestSuccessLine(t *testing.T) {
	for _, line := range []string{
		"test tests::panic_outcome_marks_failure ... ok",
		"[loco-app] test tests::panic_outcome_marks_failure ... ok",
		"\x1b[2m[loco-app]\x1b[0m test tests::panic_outcome_marks_failure ... \x1b[32mok\x1b[0m",
		"--- PASS: TestFailureRecovery (0.00s)",
		"test result: ok. 42 passed; 0 failed",
		"tests/test_failure.py::test_error_message PASSED [ 25%]",
		"ok  github.com/example/project/timeouts 3.786s coverage: 42.0% of statements",
		"ok\tgithub.com/example/project/error\t(cached)",
	} {
		if !DefiniteTestSuccessLine(line) {
			t.Errorf("DefiniteTestSuccessLine(%q) = false", line)
		}
	}
	if DefiniteTestSuccessLine("test result: FAILED. 1 failed") {
		t.Fatal("failed test result classified as success")
	}
	if DefiniteTestSuccessLine("test tests::example ... ok then failed during teardown") {
		t.Fatal("ambiguous trailing failure text classified as success")
	}
	if !DefiniteTestFailureText("[loco-app] test result: FAILED. 0 passed; 1 failed") {
		t.Fatal("Rust failure summary was not recognized")
	}
	if !DefiniteTestFailureText("\x1b[1m[loco-app]\x1b[0m test result: \x1b[31mFAILED\x1b[0m. 1 failed") {
		t.Fatal("colored Rust failure summary was not recognized")
	}
	if DefiniteTestFailureText(`assert.Equal(t, "test result: failed", output)`) {
		t.Fatal("assertion text was classified as a test-runner summary")
	}
}

func TestDefiniteTestNonFailureLine(t *testing.T) {
	for _, line := range []string{
		"✓  421 [mobile-chrome] › tests/e2e/check.spec.ts:43:3 › error recovery (2.7s)",
		"-  546 [mobile-chrome] › tests/e2e/check.spec.ts:860:3 › failure inspection",
		"\x1b[2m-  546 [chromium]\x1b[0m › tests/e2e/check.spec.ts:860:3 › failure inspection",
		"--- PASS: TestFailureRecovery (0.00s)",
		"test_winsound skipped -- No module named 'winsound'",
		"test_socket (tests.SocketTests.test_socket) ... skipped 'IPv6 is unavailable'",
	} {
		if !DefiniteTestNonFailureLine(line) {
			t.Errorf("DefiniteTestNonFailureLine(%q) = false", line)
		}
	}
	for _, line := range []string{
		"✘  421 [mobile-chrome] › tests/e2e/check.spec.ts:43:3 › error recovery (2.7s)",
		"- tests/config.yaml:12:3: invalid target",
		"- 546 [tool] tests/config.yaml:12:3: invalid target",
		"- 546 [tool] › tests/config.yaml:12:3: invalid target",
		"✓ setup completed but later failed",
		"ModuleNotFoundError: No module named 'skipped'",
		"ImportError: test was skipped because a module was not found",
	} {
		if DefiniteTestNonFailureLine(line) {
			t.Errorf("DefiniteTestNonFailureLine(%q) = true", line)
		}
	}
	input := "- 546 [chromium] › tests/check.spec.ts:12:3 › expected error\nactual version differs from required version\n"
	if got := FilterTestNonFailureLines(input); got != "\nactual version differs from required version\n" {
		t.Fatalf("FilterTestNonFailureLines() = %q", got)
	}
}

func TestPythonTestFailureRecords(t *testing.T) {
	for _, line := range []string{
		"ERROR: testStream (test.test_socket.BasicVSOCKTest.testStream)",
		"FAIL: test_equal (tests.Values.test_equal)",
		"FAILED (errors=1)",
		"FAILED (failures=2, errors=1, skipped=50)",
	} {
		if !DefiniteTestFailureText(line) {
			t.Errorf("DefiniteTestFailureText(%q) = false", line)
		}
	}
	for _, line := range []string{
		"Error: test environment not found",
		"expected ERROR: testStream (test.test_socket.BasicVSOCKTest.testStream), got nothing",
		"FAILED (errors=0)",
		"expected FAILED (errors=1) but succeeded",
	} {
		if DefiniteTestFailureText(line) {
			t.Errorf("DefiniteTestFailureText(%q) = true", line)
		}
	}
}
