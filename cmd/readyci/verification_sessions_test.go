package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/internal/syncbundlebuild"
	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func TestVerificationCommandDelegatesPreSessionJSONErrorToRootRenderer(t *testing.T) {
	output, err := captureStdoutForCLITest(t, func() error {
		return verificationCommand([]string{"--json", "--intent", "not-a-real-intent"})
	})
	var typed cliError
	if !errors.As(err, &typed) || typed.ExitCode != cliExitUsageOrConfig || typed.Classification != preflightapi.ClassificationConfigInvalid {
		t.Fatalf("verification error = %#v, want typed config error", err)
	}
	if output != "" {
		t.Fatalf("verification command wrote partial JSON before root rendering: %s", output)
	}
}

func TestPrepareVerificationSessionTreatsExplicitDependenciesAsWarm(t *testing.T) {
	_ = chdirCLITestRepo(t, "")
	if err := os.WriteFile(".readyci.yml", []byte(`version: 1
project_id: proj_dev
network:
  mode: none
  warm_mode: allowlist
  warm_allowlist: [proxy.golang.org, sum.golang.org]
affected:
  rules:
    - paths: ["**"]
      checks: [unit, ci]
checks:
  dependencies:
    command: go mod download
    cache:
      paths: [dependency-cache]
  unit:
    command: go test ./...
    cache:
      paths: [unit-cache]
  ci:
    command: go test ./...
    runner_size: large
    cache:
      paths: [ci-cache]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("main.go", []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitForCLITest(t, "init")
	runGitForCLITest(t, "config", "user.email", "test@example.com")
	runGitForCLITest(t, "config", "user.name", "Ready CI Test")
	runGitForCLITest(t, "add", ".readyci.yml", "main.go")
	runGitForCLITest(t, "commit", "-m", "baseline")
	if err := os.WriteFile("main.go", []byte("package main\n\nvar changed = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	options := verificationSessionOptions{
		APIURL: "http://localhost", ProjectID: "proj_dev", CheckName: "dependencies", RetryCount: -1,
	}
	prepared, err := prepareVerificationSession(t.Context(), options, agentIntentEditLoop)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Plans) != 0 || len(prepared.WarmPlans) != 2 ||
		prepared.WarmPlans[0].Options.LogicalChildID != "warm:dependencies:runner=large" ||
		prepared.WarmPlans[0].Options.RunnerSize != "large" ||
		!reflect.DeepEqual(prepared.WarmPlans[0].Options.CachePaths, []string{"unit-cache", "ci-cache"}) ||
		prepared.WarmPlans[1].Options.LogicalChildID != "warm:dependencies:runner=standard" ||
		prepared.WarmPlans[1].Options.RunnerSize != "standard" ||
		!reflect.DeepEqual(prepared.WarmPlans[1].Options.CachePaths, []string{"unit-cache", "ci-cache"}) ||
		prepared.WarmPlans[0].Options.NetworkMode != "allowlist" ||
		prepared.WarmPlans[0].Options.SecretsRequested == nil || *prepared.WarmPlans[0].Options.SecretsRequested ||
		!reflect.DeepEqual(prepared.WarmPlans[0].Options.NetworkAllowlist, []string{"proxy.golang.org", "sum.golang.org"}) {
		t.Fatalf("prepared explicit dependency warm = %+v", prepared)
	}
	report, err := buildAgentPlanReport("http://localhost", "proj_dev", "dependencies", "", agentIntentEditLoop, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if report.Policy == nil || report.Policy.NetworkMode != "allowlist" ||
		!reflect.DeepEqual(report.Policy.NetworkAllowlist, []string{"proxy.golang.org", "sum.golang.org"}) ||
		report.Policy.SecretsRequested || report.Plan == nil || len(report.Plan.Selected) != 2 ||
		report.Plan.Selected[0].Cost.RunnerSize != "large" ||
		report.Plan.Selected[1].Cost.RunnerSize != "standard" ||
		report.Plan.Selected[0].Needs.NetworkMode != "allowlist" {
		t.Fatalf("explicit dependency agent preview = %+v", report)
	}

	options.RequestSecrets = true
	if _, err := prepareVerificationSession(t.Context(), options, agentIntentEditLoop); err == nil ||
		!strings.Contains(err.Error(), "cannot be combined with the no-secrets dependency warm check") {
		t.Fatalf("explicit dependency secret request error = %v", err)
	}
	options.RequestSecrets = false

	prepared, err = prepareVerificationSession(t.Context(), options, agentIntentSecurity)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.WarmPlans) != 0 || len(prepared.Plans) != 1 || prepared.Plans[0].Options.NetworkMode != "none" {
		t.Fatalf("prepared security-sensitive dependency check = %+v", prepared)
	}

	if err := os.WriteFile(".readyci.yml", []byte(`version: 1
project_id: proj_dev
network:
  mode: none
  warm_mode: allowlist
  warm_allowlist: [proxy.golang.org, sum.golang.org]
checks:
  dependencies:
    command: go mod download
`), 0o644); err != nil {
		t.Fatal(err)
	}
	options.CheckName = ""
	prepared, err = prepareVerificationSession(t.Context(), options, agentIntentEditLoop)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.WarmPlans) != 0 || len(prepared.Plans) != 1 || prepared.Plans[0].Name != "dependencies" {
		t.Fatalf("inferred dependency selection was converted to warm-only: %+v", prepared)
	}

	if err := os.WriteFile(".readyci.yml", []byte(`version: 1
project_id: proj_dev
network:
  mode: none
  warm_mode: allowlist
  warm_allowlist: [proxy.golang.org, sum.golang.org]
affected:
  rules:
    - paths: ["**"]
      checks: [dependencies]
checks:
  dependencies:
    command: go mod download
`), 0o644); err != nil {
		t.Fatal(err)
	}
	options.CheckName = "dependencies"
	prepared, err = prepareVerificationSession(t.Context(), options, agentIntentEditLoop)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Plans) != 0 || len(prepared.WarmPlans) != 1 ||
		prepared.WarmPlans[0].Options.LogicalChildID != "warm:dependencies" {
		t.Fatalf("dependency-only affected warm = %+v", prepared)
	}
}

func TestClassifyVerificationCommandErrorUsesHTTPStatus(t *testing.T) {
	tests := []struct {
		status int
		want   string
	}{
		{400, preflightapi.ClassificationConfigInvalid},
		{401, preflightapi.ClassificationAuthMissing},
		{403, preflightapi.ClassificationAuthForbidden},
		{404, preflightapi.ClassificationConfigInvalid},
		{409, preflightapi.ClassificationStateConflict},
		{503, preflightapi.ClassificationPlatformInternalError},
	}
	for _, test := range tests {
		err := api.ResponseError{Operation: "POST /v1/verification-sessions", StatusCode: test.status, Status: "failed"}
		if got := classifyVerificationCommandError(err); got != test.want {
			t.Fatalf("status %d classification = %q, want %q", test.status, got, test.want)
		}
	}
}

func TestClassifyVerificationCommandErrorTreatsClientDeadlineAsTransportFailure(t *testing.T) {
	if got := classifyVerificationCommandError(context.DeadlineExceeded); got != preflightapi.ClassificationTransportFailure {
		t.Fatalf("deadline classification = %q, want %q", got, preflightapi.ClassificationTransportFailure)
	}
}

func TestApplyVerificationCommandErrorClassifiesCreatedSession(t *testing.T) {
	session := preflightapi.VerificationSession{
		SchemaVersion: preflightapi.VerificationSchemaVersion,
		State:         preflightapi.VerificationStateSealed,
		Verdict:       preflightapi.VerificationVerdictPending,
	}
	code := applyVerificationCommandError(&session, api.EventStreamError{Err: context.DeadlineExceeded})
	if session.Classification != "" {
		t.Fatalf("authoritative classification mutated to %q", session.Classification)
	}
	if session.ClientError == nil || session.ClientError.Classification != preflightapi.ClassificationVerificationUnknown {
		t.Fatalf("client error = %+v, want verification_unknown", session.ClientError)
	}
	if code != cliExitVerificationUnknown {
		t.Fatalf("exit code = %d, want %d", code, cliExitVerificationUnknown)
	}
}

func TestSessionsWatchJSONPreservesLatestReceiptOnPollingFailure(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/verification-sessions/session_watch" {
			http.NotFound(w, r)
			return
		}
		requests++
		if requests == 1 {
			writeCLITestJSON(w, http.StatusOK, preflightapi.VerificationSession{
				SchemaVersion: preflightapi.VerificationSchemaVersion,
				SessionID:     "session_watch",
				ProjectID:     "proj_dev",
				State:         preflightapi.VerificationStateSealed,
				Verdict:       preflightapi.VerificationVerdictPending,
			})
			return
		}
		w.Header().Set("Retry-After", "0")
		http.Error(w, "observation unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	output, err := captureStdoutForCLITest(t, func() error {
		return sessionsCommand([]string{"watch", "--api-url", server.URL, "--token", "pfl_watch", "--json", "session_watch"})
	})
	var exitErr exitCodeError
	if !errors.As(err, &exitErr) || exitErr.code != cliExitInternal {
		t.Fatalf("watch error = %v, want internal platform exit", err)
	}
	var receipt preflightapi.VerificationSession
	if decodeErr := json.Unmarshal([]byte(output), &receipt); decodeErr != nil {
		t.Fatalf("decode session receipt: %v\n%s", decodeErr, output)
	}
	if requests != 1+observationReadMaxAttempts || receipt.SessionID != "session_watch" || receipt.ClientError == nil ||
		receipt.ClientError.Code != "platform_internal" || receipt.ClientError.Classification != preflightapi.ClassificationPlatformInternalError {
		t.Fatalf("requests=%d receipt=%+v", requests, receipt)
	}
}

func TestSessionsWatchRechecksDetachedWorkspaceCurrentness(t *testing.T) {
	_ = chdirCLITestRepo(t, "")
	if err := os.WriteFile("tracked.txt", []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitForCLITest(t, "init")
	runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	runGitForCLITest(t, "add", "tracked.txt")
	runGitForCLITest(t, "commit", "-m", "initial")
	includeUntracked := true
	buildOptions := syncbundlebuild.BuildOptions{ChangeMode: syncbundle.ChangeModeWorking, IncludeUntracked: &includeUntracked}
	initialPath, initial, err := syncbundlebuild.BuildToTempWithOptions(t.Context(), ".", buildOptions)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(initialPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("tracked.txt", []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	recipe := preflightapi.VerificationRecipe{
		SchemaVersion: preflightapi.VerificationRecipeSchemaVersion,
		Rerunnable:    true,
		ChangeMode:    syncbundle.ChangeModeWorking,
		Source:        verificationSourceRecipe(buildOptions),
	}
	recipe.Digest, err = preflightapi.VerificationRecipeDigest(recipe)
	if err != nil {
		t.Fatal(err)
	}
	observedDigest := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session := preflightapi.VerificationSession{
			SchemaVersion: preflightapi.VerificationSchemaVersion,
			SessionID:     "session_detached",
			ProjectID:     "proj_dev",
			State:         preflightapi.VerificationStateTerminal,
			Verdict:       preflightapi.VerificationVerdictVerified,
			Currentness:   preflightapi.VerificationCurrentnessCurrent,
			Recipe:        recipe,
			Snapshot:      preflightapi.VerificationSnapshot{Digest: initial.Manifest.SnapshotDigest, ChangeMode: syncbundle.ChangeModeWorking},
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/verification-sessions/session_detached":
			writeCLITestJSON(w, http.StatusOK, session)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/verification-sessions/session_detached/stale":
			var request preflightapi.MarkVerificationSessionStaleRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			observedDigest = request.CurrentSnapshotDigest
			session.Currentness = preflightapi.VerificationCurrentnessStale
			session.CurrentSnapshotDigest = observedDigest
			writeCLITestJSON(w, http.StatusOK, session)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	output, err := captureStdoutForCLITest(t, func() error {
		return sessionsCommand([]string{"watch", "--api-url", server.URL, "--token", "pfl_watch", "--json", "session_detached"})
	})
	var exitErr exitCodeError
	if !errors.As(err, &exitErr) || exitErr.code != cliExitNonAuthoritative {
		t.Fatalf("watch error = %v, want non-authoritative exit", err)
	}
	if observedDigest == "" || observedDigest == initial.Manifest.SnapshotDigest || !strings.Contains(output, `"currentness": "stale"`) {
		t.Fatalf("initial=%q observed=%q output=%s", initial.Manifest.SnapshotDigest, observedDigest, output)
	}
}

func TestInterruptedVerificationCreateReconcilesAndCancelsAcceptedSession(t *testing.T) {
	createCalls := 0
	cancelCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/verification-sessions":
			createCalls++
			writeCLITestJSON(w, http.StatusOK, preflightapi.CreateVerificationSessionResponse{
				SchemaVersion: preflightapi.VerificationSchemaVersion,
				Session: preflightapi.VerificationSession{
					SchemaVersion: preflightapi.VerificationSchemaVersion,
					SessionID:     "session_reconciled",
					State:         preflightapi.VerificationStateSealed,
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/verification-sessions/session_reconciled/cancel":
			cancelCalls++
			writeCLITestJSON(w, http.StatusOK, preflightapi.VerificationSession{
				SchemaVersion: preflightapi.VerificationSchemaVersion,
				SessionID:     "session_reconciled",
				State:         preflightapi.VerificationStateTerminal,
				Verdict:       preflightapi.VerificationVerdictCancelled,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	request := preflightapi.CreateVerificationSessionRequest{ProjectID: "project_test", IdempotencyKey: "idem_reconcile"}
	session, interrupted := reconcileAndCancelInterruptedVerificationSession(server.URL, api.NewClient(server.URL, api.WithToken("pfl_test")), &request, preflightapi.VerificationSession{}, syscall.SIGINT, context.Canceled)
	if session.SessionID != "session_reconciled" || session.Verdict != preflightapi.VerificationVerdictCancelled || session.ClientInterruption == nil {
		t.Fatalf("reconciled session = %+v", session)
	}
	if interrupted.ResourceID != session.SessionID || interrupted.CancellationAcknowledged == nil || !*interrupted.CancellationAcknowledged || interrupted.CancellationError != nil {
		t.Fatalf("interruption = %+v", interrupted)
	}
	if createCalls != 1 || cancelCalls != 1 {
		t.Fatalf("create calls = %d, cancel calls = %d", createCalls, cancelCalls)
	}
	if !strings.Contains(session.ClientInterruption.StatusCommand, "--api-url "+server.URL) ||
		!strings.Contains(session.ClientInterruption.CancelCommand, "--api-url "+server.URL) {
		t.Fatalf("interruption commands do not preserve API URL: %+v", session.ClientInterruption)
	}
}

func TestRejectedVerificationUploadContractCancelsCreatedSession(t *testing.T) {
	cancelCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/verification-sessions/session_legacy/cancel" {
			http.NotFound(w, r)
			return
		}
		cancelCalls++
		writeCLITestJSON(w, http.StatusOK, preflightapi.VerificationSession{
			SchemaVersion: preflightapi.VerificationSchemaVersion,
			SessionID:     "session_legacy",
			State:         preflightapi.VerificationStateTerminal,
			Verdict:       preflightapi.VerificationVerdictCancelled,
		})
	}))
	defer server.Close()

	_, rejection := validateVerificationSourceUploadRequirements(preflightapi.CreateVerificationSessionResponse{
		Session: preflightapi.VerificationSession{
			SchemaVersion: preflightapi.VerificationSchemaVersion,
			SessionID:     "session_legacy",
			State:         preflightapi.VerificationStateSealed,
		},
		SourceUploadMode: preflightapi.VerificationSourceUploadModeDirect,
	}, false)
	cancelled, err := cancelVerificationSessionRejectedAfterCreation(
		api.NewClient(server.URL, api.WithToken("pfl_test")),
		preflightapi.VerificationSession{
			SchemaVersion: preflightapi.VerificationSchemaVersion,
			SessionID:     "session_legacy",
			State:         preflightapi.VerificationStateSealed,
		},
		rejection,
	)
	var cliErr cliError
	if !errors.As(err, &cliErr) || cliErr.Code != "platform_internal" || cliErr.Retryable {
		t.Fatalf("rejection error = %#v", err)
	}
	if cancelCalls != 1 || cancelled.State != preflightapi.VerificationStateTerminal || cancelled.Verdict != preflightapi.VerificationVerdictCancelled {
		t.Fatalf("cancel calls=%d session=%+v", cancelCalls, cancelled)
	}
}

func TestRejectedVerificationUploadContractSurfacesCancellationFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "cancel unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	rejection := cliError{
		Code: "verification_source_direct_upload_unavailable", ExitCode: cliExitUsageOrConfig,
		Classification: preflightapi.ClassificationPreconditionFailed, Message: "use a fresh idempotency key",
	}
	_, err := cancelVerificationSessionRejectedAfterCreation(
		api.NewClient(server.URL, api.WithToken("pfl_test")),
		preflightapi.VerificationSession{SessionID: "session_legacy", State: preflightapi.VerificationStateSealed},
		rejection,
	)
	var cliErr cliError
	if !errors.As(err, &cliErr) || cliErr.Retryable || !strings.Contains(cliErr.Message, "automatic cancellation") || !strings.Contains(cliErr.Message, "cancel unavailable") {
		t.Fatalf("cancellation failure = %#v", err)
	}
}

func TestVerificationNonAuthoritativeVerdictsUseDedicatedExitCode(t *testing.T) {
	for _, session := range []preflightapi.VerificationSession{
		{Verdict: preflightapi.VerificationVerdictChecksPassed},
		{Verdict: preflightapi.VerificationVerdictInsufficientEvidence},
		{Verdict: preflightapi.VerificationVerdictVerified, Currentness: preflightapi.VerificationCurrentnessStale},
	} {
		err := verificationVerdictExit(session)
		var exitErr exitCodeError
		if !errors.As(err, &exitErr) || exitErr.code != cliExitNonAuthoritative {
			t.Fatalf("verificationVerdictExit(%+v) = %v", session, err)
		}
	}
}

func TestExplicitDependencyWarmSourceConflictIsConfigInvalidForPlanAndVerify(t *testing.T) {
	_ = chdirCLITestRepo(t, "")
	if err := os.WriteFile(".readyci.yml", []byte(`version: 1
project_id: proj_dev
network:
  mode: none
  warm_mode: allowlist
  warm_allowlist: [proxy.golang.org]
affected:
  rules:
    - paths: ["**"]
      checks: [unit]
checks:
  dependencies:
    command: go mod download
    sync:
      include_untracked: true
  unit:
    command: go test ./...
    sync:
      include_untracked: false
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("main.txt", []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitForCLITest(t, "init")
	runGitForCLITest(t, "config", "user.email", "test@example.com")
	runGitForCLITest(t, "config", "user.name", "Ready CI Test")
	runGitForCLITest(t, "add", ".readyci.yml", "main.txt")
	runGitForCLITest(t, "commit", "-m", "baseline")
	if err := os.WriteFile("main.txt", []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	assertConfigInvalid := func(label string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), "different sync.exclude, reviewed_fixtures, or include_untracked") {
			t.Fatalf("%s error = %v, want source-filter conflict", label, err)
		}
		normalized := normalizeCLIError(err)
		if normalized.Code != "config_invalid" || normalized.Classification != preflightapi.ClassificationConfigInvalid || normalized.ExitCode != cliExitUsageOrConfig {
			t.Fatalf("%s normalized error = %+v, want config_invalid", label, normalized)
		}
	}

	_, err := buildAgentPlanReport("http://localhost", "proj_dev", "dependencies", "", agentIntentEditLoop, "", false)
	assertConfigInvalid("agent plan", err)
	_, err = prepareVerificationSession(t.Context(), verificationSessionOptions{
		APIURL: "http://localhost", ProjectID: "proj_dev", CheckName: "dependencies", RetryCount: -1,
	}, agentIntentEditLoop)
	assertConfigInvalid("verify preparation", err)
}

func TestExplicitDependencyWarmTargetConflictIsConfigInvalid(t *testing.T) {
	_ = chdirCLITestRepo(t, "")
	if err := os.WriteFile(".readyci.yml", []byte(`version: 1
project_id: proj_dev
network:
  mode: none
  warm_mode: allowlist
  warm_allowlist: [proxy.golang.org]
checks:
  dependencies:
    command: go mod download
  unit:
    command: go test ./...
    cache:
      size: 10GiB
  ci:
    command: make ci
    cache:
      size: 20GiB
`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := buildAgentPlanReport("http://localhost", "proj_dev", "dependencies", "", agentIntentEditLoop, "", false)
	if err == nil || !strings.Contains(err.Error(), "one cache lane size") {
		t.Fatalf("agent plan error = %v, want cache-lane conflict", err)
	}
	normalized := normalizeCLIError(err)
	if normalized.Code != "config_invalid" || normalized.Classification != preflightapi.ClassificationConfigInvalid || normalized.ExitCode != cliExitUsageOrConfig {
		t.Fatalf("normalized error = %+v, want config_invalid", normalized)
	}
}

func TestVerificationSuccessorPreservesPredecessorTaskID(t *testing.T) {
	if got, err := verificationTaskID("workspace-child", "", "task-stable"); err != nil || got != "task-stable" {
		t.Fatalf("task id = %q, err=%v", got, err)
	}
	if got, err := verificationTaskID("workspace-first", "", ""); err != nil || got != "workspace-first" {
		t.Fatalf("initial task id = %q, err=%v", got, err)
	}
	if _, err := verificationTaskID("workspace-child", "different", "task-stable"); !errors.Is(err, errVerificationTaskMismatch) {
		t.Fatalf("mismatch error = %v", err)
	}
}

func TestCompatibleAffectedSessionBuildOptionsRejectsDifferentWorkspacePolicies(t *testing.T) {
	include := true
	exclude := false
	base := affectedCheckPlan{Plan: checkRunPlan{
		Name: "go",
		Options: remoteRunOptions{
			SyncExclude:      []string{"dist/**", "tmp/**"},
			IncludeUntracked: &include,
		},
	}}
	compatible := affectedCheckPlan{Plan: checkRunPlan{
		Name: "web",
		Options: remoteRunOptions{
			SyncExclude:      []string{"tmp/**", "dist/**", "dist/**"},
			IncludeUntracked: &include,
		},
	}}
	options, err := compatibleAffectedSessionBuildOptions([]affectedCheckPlan{base, compatible}, syncbundlebuild.BuildOptions{ChangeMode: "working"})
	if err != nil {
		t.Fatal(err)
	}
	if options.IncludeUntracked == nil || !*options.IncludeUntracked || strings.Join(options.ExcludePatterns, ",") != "dist/**,tmp/**" {
		t.Fatalf("options = %+v", options)
	}

	differentExcludes := compatible
	differentExcludes.Plan.Name = "security"
	differentExcludes.Plan.Options.SyncExclude = []string{"tmp/**"}
	if _, err := compatibleAffectedSessionBuildOptions([]affectedCheckPlan{base, differentExcludes}, syncbundlebuild.BuildOptions{ChangeMode: "working"}); err == nil || !strings.Contains(err.Error(), "different sync.exclude") {
		t.Fatalf("different excludes error = %v", err)
	}

	differentUntracked := compatible
	differentUntracked.Plan.Name = "tracked-only"
	differentUntracked.Plan.Options.IncludeUntracked = &exclude
	if _, err := compatibleAffectedSessionBuildOptions([]affectedCheckPlan{base, differentUntracked}, syncbundlebuild.BuildOptions{ChangeMode: "working"}); err == nil || !strings.Contains(err.Error(), "different sync.exclude") {
		t.Fatalf("different include_untracked error = %v", err)
	}
}

func TestRegisteredPolicyBuildOptionsOverrideCandidateSourceFilter(t *testing.T) {
	includeCandidate := false
	fallback := syncbundlebuild.BuildOptions{
		ChangeMode:       "working",
		ExcludePatterns:  []string{"candidate-hidden/**"},
		IncludeUntracked: &includeCandidate,
	}
	active := &syncbundle.SourcePolicyBinding{
		Digest:                 "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		ExcludePatterns:        []string{"trusted-generated/**"},
		DefaultExcludePatterns: syncbundle.DefaultSourceExcludePatterns(),
		IncludeUntracked:       true,
		ProjectIgnoreUsed:      false,
	}
	options, err := registeredPolicyBuildOptions(fallback, active)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := options.ExcludePatterns, []string{"trusted-generated/**"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("registered excludes = %v, want %v", got, want)
	}
	if options.IncludeUntracked == nil || !*options.IncludeUntracked {
		t.Fatalf("registered include_untracked = %v, want true", options.IncludeUntracked)
	}
	if options.UseProjectIgnore == nil || *options.UseProjectIgnore {
		t.Fatalf("registered project ignore = %v, want disabled", options.UseProjectIgnore)
	}
	if !reflect.DeepEqual(options.DefaultExcludePatterns, syncbundle.DefaultSourceExcludePatterns()) {
		t.Fatalf("registered default excludes = %v, want trusted binding %v", options.DefaultExcludePatterns, syncbundle.DefaultSourceExcludePatterns())
	}
	if options.SourcePolicyDigest != active.Digest {
		t.Fatalf("source policy digest = %q, want %q", options.SourcePolicyDigest, active.Digest)
	}
}

func TestVerificationSourcePolicyUsesEffectiveChangeMode(t *testing.T) {
	options, err := normalizedVerificationSourceBuildOptions(syncbundlebuild.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if options.ChangeMode != syncbundle.ChangeModeWorking {
		t.Fatalf("default effective change mode = %q, want %q", options.ChangeMode, syncbundle.ChangeModeWorking)
	}
	options, err = normalizedVerificationSourceBuildOptions(syncbundlebuild.BuildOptions{ChangeMode: syncbundle.ChangeModeStaged})
	if err != nil {
		t.Fatal(err)
	}
	if options.ChangeMode != syncbundle.ChangeModeStaged {
		t.Fatalf("configured effective change mode = %q, want %q", options.ChangeMode, syncbundle.ChangeModeStaged)
	}
}

func TestVerificationNodesPreserveSerialMatrixDefault(t *testing.T) {
	nodes, groups, err := verificationNodesForPlans(nil, []checkRunPlan{{
		Name:    "test",
		Options: remoteRunOptions{Command: "true"},
		Matrix: config.MatrixConfig{
			Axes:     map[string][]string{"GO_VERSION": {"1.24", "1.25"}},
			FailFast: true,
		},
	}}, syncbundlebuild.BuildResult{})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 || len(groups) != 1 {
		t.Fatalf("nodes=%d groups=%+v", len(nodes), groups)
	}
	if groups[0].MaxParallel != 1 {
		t.Fatalf("default matrix max_parallel = %d, want 1", groups[0].MaxParallel)
	}
}

func TestVerificationNodesGateFirstCheckOnWarmSuccess(t *testing.T) {
	warm := checkRunPlan{Name: "warm", Options: remoteRunOptions{Command: "warm"}}
	checks := []checkRunPlan{
		{Name: "first", Options: remoteRunOptions{Command: "first"}},
		{Name: "second", Options: remoteRunOptions{Command: "second"}},
	}
	nodes, _, err := verificationNodesForPlans([]checkRunPlan{warm}, checks, syncbundlebuild.BuildResult{})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 3 {
		t.Fatalf("nodes = %+v", nodes)
	}
	if !reflect.DeepEqual(nodes[1].SuccessDependsOn, []string{nodes[0].ID}) {
		t.Fatalf("first verification success dependencies = %v, want warm node", nodes[1].SuccessDependsOn)
	}
	if nodes[2].Condition != preflightapi.VerificationNodeConditionAlways ||
		!reflect.DeepEqual(nodes[2].SuccessDependsOn, []string{nodes[0].ID}) {
		t.Fatalf("later verification condition = %q success dependencies = %v, want always plus warm gate", nodes[2].Condition, nodes[2].SuccessDependsOn)
	}
}

func TestVerificationCoverageDoesNotTreatExplicitCheckAsCoveringEveryChange(t *testing.T) {
	changed := []string{"auth.go", "docs/guide.md"}
	matched, uncovered := verificationCoverage("check", changed, changed)
	if len(matched) != 0 {
		t.Fatalf("matched = %v, want none for an untrusted explicit check", matched)
	}
	if !reflect.DeepEqual(uncovered, changed) {
		t.Fatalf("uncovered = %v, want %v", uncovered, changed)
	}

	matched, uncovered = verificationCoverage("affected_rules", changed, []string{"docs/guide.md"})
	if !reflect.DeepEqual(matched, []string{"docs/guide.md"}) ||
		!reflect.DeepEqual(uncovered, []string{"auth.go"}) {
		t.Fatalf("affected coverage matched=%v uncovered=%v", matched, uncovered)
	}
}

// Intent handling sets SecretsRequested on the resolved plan's options, after
// check resolution and before node compilation. A conversion that dropped it
// would silently stop --request-secrets from requesting anything, and would
// break --intent security-sensitive outright: that intent sets an explicit
// false, and the API rejects a security-sensitive node whose request is unset.
func TestVerificationNodesCarrySecretsRequestThroughPlanConversion(t *testing.T) {
	for _, test := range []struct {
		name    string
		request *bool
	}{
		{"requested", boolPtr(true)},
		{"explicitly declined", boolPtr(false)},
		{"unset", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			nodes, _, err := verificationNodesForPlans(nil, []checkRunPlan{{
				Name:    "test",
				Options: remoteRunOptions{Command: "true", SecretsRequested: test.request},
			}}, syncbundlebuild.BuildResult{})
			if err != nil {
				t.Fatal(err)
			}
			if len(nodes) != 1 {
				t.Fatalf("nodes = %d, want 1", len(nodes))
			}
			got := nodes[0].Run.SecretsRequested
			if test.request == nil {
				if got != nil {
					t.Fatalf("secrets request = %v, want unset", *got)
				}
				return
			}
			if got == nil || *got != *test.request {
				t.Fatalf("secrets request = %v, want %v", got, *test.request)
			}
		})
	}
}

// Matrix variants inject environment into the run, which reaches the generated
// command. A conversion that dropped it would produce nodes that ignore the
// variant they were expanded for.
func TestVerificationNodesCarryMatrixEnvironmentIntoTheCommand(t *testing.T) {
	nodes, _, err := verificationNodesForPlans(nil, []checkRunPlan{{
		Name:    "test",
		Options: remoteRunOptions{Command: "go test ./...", CommandAlias: "test"},
		Matrix:  config.MatrixConfig{Axes: map[string][]string{"GO_VERSION": {"1.24", "1.25"}}},
	}}, syncbundlebuild.BuildResult{})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(nodes))
	}
	for index, want := range []string{"1.24", "1.25"} {
		if !strings.Contains(nodes[index].Run.Command, "GO_VERSION="+want) {
			t.Fatalf("node %d command does not set GO_VERSION=%s:\n%s", index, want, nodes[index].Run.Command)
		}
	}
}

// The dependency warm-up assigns its own logical child id and name so the sealed
// graph says what the node is for. Conversion into the planner must carry them,
// or the node is renamed and collides with the user's check of the same name.
func TestVerificationNodesPreserveDependencyWarmIdentity(t *testing.T) {
	warm := checkRunPlan{Name: "rust-deps", Options: remoteRunOptions{
		Command: "cargo fetch", CommandAlias: "rust-deps",
		LogicalChildID: "warm:rust-deps", LogicalChildName: "warm rust-deps",
	}}
	verify := checkRunPlan{Name: "rust-deps", Options: remoteRunOptions{Command: "cargo test", CommandAlias: "rust-deps"}}
	nodes, _, err := verificationNodesForPlans([]checkRunPlan{warm}, []checkRunPlan{verify}, syncbundlebuild.BuildResult{})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(nodes))
	}
	if nodes[0].ID != "warm:rust-deps" || nodes[0].Name != "warm rust-deps" {
		t.Fatalf("warm node = id %q name %q, want the identity the warm-up assigned", nodes[0].ID, nodes[0].Name)
	}
	if nodes[1].ID != "rust-deps" {
		t.Fatalf("verify node id = %q, want it not displaced by the warm node", nodes[1].ID)
	}
}
