package main

import (
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestLocalDiagnosisEvidencePriorities(t *testing.T) {
	exitCode := 1

	missingModule := localAnalyzeRunFailure(preflightapi.StatusFailed, "pytest -q", &exitCode, []preflightapi.RunEvent{{
		Type: preflightapi.EventStdout,
		Data: "ModuleNotFoundError: No module named 'freezegun'\n",
	}}, "command exited with code 2")
	if !strings.Contains(missingModule.Summary, "freezegun") {
		t.Fatalf("missing module summary = %q, want module name despite generic failure reason", missingModule.Summary)
	}

	goSum := localAnalyzeRunFailure(preflightapi.StatusFailed, "go test ./...", &exitCode, []preflightapi.RunEvent{{Type: preflightapi.EventStderr, Data: "go: gopkg.in/check.v1: missing go.sum entry for go.mod file; to add it:\n\tgo mod download gopkg.in/check.v1\nreadyci: retained failed workspace at /cache/failed-runs/run_go_sum/workspace.tar.gz\n"}}, "")
	if !strings.Contains(goSum.Summary, "missing go.sum entry") || strings.Contains(goSum.Summary, "readyci: retained") {
		t.Fatalf("go sum summary = %q, want go.sum evidence without Ready CI retention noise", goSum.Summary)
	}

	terseDownload := localAnalyzeRunFailure(preflightapi.StatusFailed, "pnpm install", &exitCode, []preflightapi.RunEvent{{
		Type: preflightapi.EventStderr,
		Data: "ERR_PNPM_FETCH_404 package metadata not found - 404\n",
	}}, "")
	if terseDownload.Classification != preflightapi.ClassificationDependencyDownloadFailed || terseDownload.Summary != "A dependency download failed." {
		t.Fatalf("terse dependency analysis = %+v, want dependency-specific fallback summary", terseDownload)
	}

	dockerRegistry := localAnalyzeRunFailure(preflightapi.StatusFailed, "make check-ci-db-parallel", &exitCode, []preflightapi.RunEvent{{
		Type: preflightapi.EventStdout,
		Data: "failed to pull the image 'postgres:16-alpine': Head \"https://registry-1.docker.io/v2/library/postgres/manifests/16-alpine\": dial tcp: lookup registry-1.docker.io on 127.0.0.53:53: server misbehaving\n",
	}, {
		Type:     preflightapi.EventCommandExited,
		ExitCode: &exitCode,
	}}, "artifact upload failed: umount rootfs artifacts: target is busy")
	if !strings.Contains(dockerRegistry.Summary, "registry-1.docker.io") || strings.Contains(dockerRegistry.Summary, "artifact upload failed") {
		t.Fatalf("Docker registry summary = %q, want registry evidence over secondary artifact failure", dockerRegistry.Summary)
	}
	networkNone := localAnalyzeRunFailureWithNetworkMode(preflightapi.StatusFailed, "make check-ci-db-parallel", &exitCode, []preflightapi.RunEvent{{
		Type: preflightapi.EventStdout,
		Data: "failed to pull the image 'postgres:16-alpine': Head \"https://registry-1.docker.io/v2/library/postgres/manifests/16-alpine\": dial tcp: lookup registry-1.docker.io on 127.0.0.53:53: server misbehaving\n",
	}}, "", preflightapi.DefaultNetworkMode)
	if networkNone.Classification != preflightapi.ClassificationNetworkNotAllowed || networkNone.Retryable || !strings.Contains(networkNone.Summary, "network_mode: none") {
		t.Fatalf("network-none Docker registry analysis = %+v, want actionable network policy diagnosis", networkNone)
	}
	tlsFailure := localAnalyzeRunFailureWithNetworkMode(preflightapi.StatusFailed, "docker pull postgres:16", &exitCode, []preflightapi.RunEvent{{
		Type: preflightapi.EventStderr,
		Data: strings.Join([]string{
			"failed to pull the image: Head \"https://registry-1.docker.io/v2/library/postgres/manifests/16\"",
			"tls: failed to verify certificate: x509: certificate signed by unknown authority",
		}, "\n") + "\n",
	}}, "", preflightapi.DefaultNetworkMode)
	if tlsFailure.Classification != preflightapi.ClassificationDependencyDownloadFailed || !tlsFailure.Retryable || tlsFailure.AgentAction != preflightapi.AgentActionRetry {
		t.Fatalf("TLS registry analysis = %+v, want retryable dependency failure", tlsFailure)
	}
	multilineTestcontainers := localAnalyzeRunFailureWithNetworkMode(preflightapi.StatusFailed, "go test ./...", &exitCode, []preflightapi.RunEvent{{
		Type: preflightapi.EventStderr,
		Data: strings.Join([]string{
			"--- FAIL: TestDatabase (0.01s)",
			"setup failed: failed to pull the image 'postgres:16-alpine'",
			"failed to do request: Head \"https://registry-1.docker.io/v2/library/postgres/manifests/16-alpine\"",
			"dial tcp: lookup registry-1.docker.io on 127.0.0.53:53: server misbehaving",
		}, "\n") + "\n",
	}}, "", preflightapi.DefaultNetworkMode)
	if multilineTestcontainers.Classification != preflightapi.ClassificationNetworkNotAllowed || multilineTestcontainers.Retryable {
		t.Fatalf("multiline Testcontainers analysis = %+v, want network policy diagnosis", multilineTestcontainers)
	}
	quotedTransport := localAnalyzeRunFailureWithNetworkMode(preflightapi.StatusFailed, "go test ./...", &exitCode, []preflightapi.RunEvent{{
		Type: preflightapi.EventStdout,
		Data: "registry_test.go:42: AssertionError: expected registry-1.docker.io pull to fail with server misbehaving, got success\n",
	}}, "", preflightapi.DefaultNetworkMode)
	if quotedTransport.Classification != preflightapi.ClassificationTestFailed {
		t.Fatalf("quoted transport analysis = %+v, want test failure", quotedTransport)
	}
	separateAssertion := localAnalyzeRunFailureWithNetworkMode(preflightapi.StatusFailed, "go test ./...", &exitCode, []preflightapi.RunEvent{{
		Type: preflightapi.EventStdout,
		Data: strings.Join([]string{
			"registry request to https://registry.npmjs.org/pkg failed: getaddrinfo EAI_AGAIN registry.npmjs.org",
			"registry_test.go:51: AssertionError: expected cached fallback, got error",
		}, "\n") + "\n",
	}}, "", preflightapi.DefaultNetworkMode)
	if separateAssertion.Classification != preflightapi.ClassificationTestFailed || !strings.Contains(separateAssertion.Summary, "cached fallback") {
		t.Fatalf("separate assertion analysis = %+v, want actionable test failure", separateAssertion)
	}
	wrapperAssertion := localAnalyzeRunFailureWithNetworkMode(preflightapi.StatusFailed, "make check", &exitCode, []preflightapi.RunEvent{{
		Type: preflightapi.EventStdout,
		Data: strings.Join([]string{
			"registry request to https://registry.npmjs.org/pkg failed: getaddrinfo EAI_AGAIN registry.npmjs.org",
			"registry_test.go:58: AssertionError: expected retry, got failure",
		}, "\n") + "\n",
	}}, "", preflightapi.DefaultNetworkMode)
	if wrapperAssertion.Classification != preflightapi.ClassificationTestFailed || wrapperAssertion.Retryable {
		t.Fatalf("wrapper assertion analysis = %+v, want non-retryable test failure", wrapperAssertion)
	}
	applicationPayload := localAnalyzeRunFailure(preflightapi.StatusFailed, "make check", &exitCode, []preflightapi.RunEvent{{
		Type: preflightapi.EventStderr,
		Data: "request handler rejected fixture: payload too large\n",
	}}, "")
	if applicationPayload.Classification == preflightapi.ClassificationSyncTooLarge {
		t.Fatalf("application payload analysis = %+v, must not be classified as oversized sync", applicationPayload)
	}
	commandExitCode := 0
	artifactFailureReason := "artifact upload failed: put artifacts: 503 unavailable"
	primaryArtifactFailure := localAnalyzeRunFailure(preflightapi.StatusFailed, "npm ci && npm test", &exitCode, []preflightapi.RunEvent{{
		Type: preflightapi.EventStderr,
		Data: "npm error request to https://registry.npmjs.org/pkg failed: getaddrinfo EAI_AGAIN registry.npmjs.org\n",
	}, {
		Type:     preflightapi.EventCommandExited,
		ExitCode: &commandExitCode,
	}}, artifactFailureReason)
	if primaryArtifactFailure.Classification != preflightapi.ClassificationSyncFailed || !primaryArtifactFailure.Retryable || primaryArtifactFailure.Summary != artifactFailureReason {
		t.Fatalf("primary artifact analysis = %+v, want sync failure", primaryArtifactFailure)
	}
	primaryArtifactWithoutCommandEvent := localAnalyzeRunFailure(preflightapi.StatusFailed, "npm test", &exitCode, []preflightapi.RunEvent{{
		Type: preflightapi.EventStderr,
		Data: "warning: recovered expected error from optional probe\n",
	}}, artifactFailureReason)
	if primaryArtifactWithoutCommandEvent.Classification != preflightapi.ClassificationSyncFailed || !primaryArtifactWithoutCommandEvent.Retryable || primaryArtifactWithoutCommandEvent.Summary != artifactFailureReason {
		t.Fatalf("primary artifact analysis without command event = %+v, want sync failure", primaryArtifactWithoutCommandEvent)
	}
	timedOutWithSecondaryDelivery := localAnalyzeRunFailure(preflightapi.StatusTimedOut, "npm test", &exitCode, nil, "run timed out; log delivery incomplete: upload failed with status 503")
	if timedOutWithSecondaryDelivery.Classification != preflightapi.ClassificationTimeout || timedOutWithSecondaryDelivery.Summary != "run timed out" {
		t.Fatalf("timed-out analysis = %+v, want primary timeout reason", timedOutWithSecondaryDelivery)
	}
	for _, failureReason := range []string{
		"failure K; LoG DeLiVeRy InCoMpLeTe: upload failed with status 503",
		"failure K; ArTiFaCt UpLoAd FaIlEd: target is busy",
	} {
		if got := localPrimaryWorkerFailureReason(failureReason, nil); got != "failure K" {
			t.Fatalf("localPrimaryWorkerFailureReason(%q) = %q, want Unicode primary reason", failureReason, got)
		}
	}
	tooLargeReason := "artifact upload failed: PUT artifacts failed: 413 Request Entity Too Large"
	tooLargeArtifact := localAnalyzeRunFailure(preflightapi.StatusFailed, "npm test", &exitCode, []preflightapi.RunEvent{{
		Type:     preflightapi.EventCommandExited,
		ExitCode: &commandExitCode,
	}}, tooLargeReason)
	if tooLargeArtifact.Classification != preflightapi.ClassificationArtifactLimitExceeded || tooLargeArtifact.Retryable || tooLargeArtifact.AgentAction != preflightapi.AgentActionReduceScope || tooLargeArtifact.Summary != tooLargeReason {
		t.Fatalf("too-large artifact analysis = %+v, want reduce-scope diagnosis", tooLargeArtifact)
	}
	reachableRegistry := localAnalyzeRunFailureWithNetworkMode(preflightapi.StatusFailed, "docker pull missing/image", &exitCode, []preflightapi.RunEvent{{
		Type: preflightapi.EventStderr,
		Data: "failed to pull the image: 404 Not Found from https://registry-1.docker.io/v2/missing/image/manifests/latest\n",
	}}, "", preflightapi.DefaultNetworkMode)
	if reachableRegistry.Classification != preflightapi.ClassificationDependencyDownloadFailed || !reachableRegistry.Retryable {
		t.Fatalf("reachable registry analysis = %+v, want dependency failure", reachableRegistry)
	}

	startup := "Karma server started at http://0.0.0.0:9876/"
	lintEvidence := localAnalyzeRunFailure(preflightapi.StatusFailed, "npm run lint", &exitCode, []preflightapi.RunEvent{{
		Type: preflightapi.EventStdout,
		Data: startup + "\nsrc/common.js:53:2 lint error\n",
	}}, startup)
	if lintEvidence.PrimaryFile != "src/common.js" || !strings.Contains(lintEvidence.Summary, "src/common.js:53:2") {
		t.Fatalf("lint analysis = %+v, want URL ignored and lint evidence preferred", lintEvidence)
	}

	configFailure := localAnalyzeRunFailure(preflightapi.StatusFailed, "readyci config validate", &exitCode, []preflightapi.RunEvent{{
		Type: preflightapi.EventStderr,
		Data: "yaml: unmarshal errors:\n  line 4: field unknown not found\n",
	}}, "")
	if configFailure.Classification != preflightapi.ClassificationConfigInvalid {
		t.Fatalf("config analysis = %+v, want config_invalid", configFailure)
	}
	applicationConfig := localAnalyzeRunFailure(preflightapi.StatusFailed, "make build", &exitCode, []preflightapi.RunEvent{{
		Type: preflightapi.EventStderr,
		Data: "unknown config key database.timeout\n",
	}}, "")
	if applicationConfig.Classification != preflightapi.ClassificationBuildFailed {
		t.Fatalf("application config analysis = %+v, want build_failed", applicationConfig)
	}

	incompatibleAssertion := localAnalyzeRunFailure(preflightapi.StatusFailed, "pytest -q", &exitCode, []preflightapi.RunEvent{{
		Type: preflightapi.EventStdout,
		Data: "tests/test_dependencies.py:42: AssertionError: expected compatible dependencies, but you have package Y which is incompatible\n",
	}}, "")
	if !strings.Contains(incompatibleAssertion.Summary, "which is incompatible") || len(incompatibleAssertion.Evidence) != 1 {
		t.Fatalf("incompatible assertion analysis = %+v, want assertion summary and evidence", incompatibleAssertion)
	}

	noiseAssertion := localAnalyzeRunFailure(preflightapi.StatusFailed, "pytest -q", &exitCode, []preflightapi.RunEvent{{
		Type: preflightapi.EventStdout,
		Data: "tests/test_output.py:12: AssertionError: expected 'warning: deprecated', got ''\n",
	}}, "")
	if !strings.Contains(noiseAssertion.Summary, "warning: deprecated") || len(noiseAssertion.Evidence) != 1 {
		t.Fatalf("noise assertion analysis = %+v, want assertion summary and evidence", noiseAssertion)
	}

	pytestInternal := localAnalyzeRunFailure(preflightapi.StatusFailed, "pytest -q", &exitCode, []preflightapi.RunEvent{{Type: preflightapi.EventStdout, Data: strings.Join([]string{
		"INTERNALERROR> Traceback (most recent call last):",
		"INTERNALERROR>   File \"/workspace/.venv/lib/python3.12/site-packages/_pytest/main.py\", line 330, in wrap_session",
		"INTERNALERROR>     session.exitstatus = doit(config, session) or 0",
		"INTERNALERROR> pytest.PytestUnknownMarkWarning: Unknown pytest.mark.no_cover - is this a typo?",
	}, "\n")}}, "")
	if !strings.Contains(pytestInternal.Summary, "PytestUnknownMarkWarning") || strings.Contains(pytestInternal.Summary, "_pytest/main.py") {
		t.Fatalf("pytest internal summary = %q, want root warning without traceback frame", pytestInternal.Summary)
	}

	rustOutput := strings.Repeat("[loco-app] test tests::panic_outcome_marks_failure ... ok\n", 20) +
		"[loco-app-it] thread 'review_scoped_airline_routes_run_under_app_role' panicked at apps/loco-app-it/tests/it_review_scoped_routes_sqlx.rs:251:6:\n" +
		"[loco-app-it] called `Result::unwrap()` on an `Err` value: duplicate key value violates unique constraint\n" +
		"[loco-app-it] test result: FAILED. 0 passed; 1 failed\n"
	rustFailure := localAnalyzeRunFailure(preflightapi.StatusFailed, "make check-ci-db-parallel", &exitCode, []preflightapi.RunEvent{{
		Type: preflightapi.EventStdout,
		Data: rustOutput,
	}}, "")
	if rustFailure.Classification != preflightapi.ClassificationTestFailed ||
		rustFailure.PrimaryFile != "apps/loco-app-it/tests/it_review_scoped_routes_sqlx.rs" ||
		strings.HasSuffix(rustFailure.Summary, "... ok") ||
		(!strings.Contains(rustFailure.Summary, "duplicate key") && !strings.Contains(rustFailure.Summary, "panicked at")) {
		t.Fatalf("Rust panic analysis = %+v, want terminal failure evidence", rustFailure)
	}
}

func TestLocalEvidenceFiltersNoiseAndTruncatesLines(t *testing.T) {
	noisy := make([]string, 0, 32)
	for i := 0; i < 24; i++ {
		noisy = append(noisy, "setup line with no diagnostic signal")
	}
	longFailure := "platform/internal/runs/store_test.go:42: " + strings.Repeat("expected queued got failed ", 40)
	noisy = append(noisy, longFailure)
	evidence := localEvidenceFromEvents([]preflightapi.RunEvent{{
		Type: preflightapi.EventStderr,
		Data: strings.Join(noisy, "\n"),
	}}, 5)
	if len(evidence) != 1 {
		t.Fatalf("evidence length = %d, want one filtered failure line: %+v", len(evidence), evidence)
	}
	if evidence[0].Path != "platform/internal/runs/store_test.go" {
		t.Fatalf("evidence path = %q, want store test path", evidence[0].Path)
	}
	if len(evidence[0].Text) > 500 {
		t.Fatalf("evidence text length = %d, want <= 500", len(evidence[0].Text))
	}

	storageEvidence := localEvidenceFromEvents([]preflightapi.RunEvent{{
		Type: preflightapi.EventStderr,
		Data: "error writing /cache/failed-runs/run_storage/workspace.tar.gz: no space left on device\n",
	}}, 5)
	if len(storageEvidence) != 1 || !strings.Contains(storageEvidence[0].Text, "no space left on device") {
		t.Fatalf("storage evidence = %+v, want meaningful failed-workspace error", storageEvidence)
	}
}
