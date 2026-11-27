package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

// Affected selection resolves its checks with its own default change mode.
// Preparation shares their workspace, so it must use that mode rather than an
// installer's configured one, and every plan preview must name its network.
func TestAffectedPreparationUsesSelectedChangeModeAndReportsNetwork(t *testing.T) {
	chdirCLITestRepo(t, "preparation-change-mode")
	runGitForCLITest(t, "init")
	if err := os.WriteFile(".readyci.yml", []byte(`project_id: review
sync:
  include_untracked: true
network:
  mode: none
  warm_mode: allowlist
  warm_allowlist: [registry.npmjs.org]
checks:
  dependencies:
    command: npm ci
    changes: staged
  test:
    command: npm test
    changes: staged
affected:
  rules:
    - paths: ["**"]
      checks: [test]
`), 0644); err != nil {
		t.Fatal(err)
	}
	runGitForCLITest(t, "add", ".readyci.yml")
	runGitForCLITest(t, "-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "-m", "fixture")
	if err := os.WriteFile("test.js", []byte("test change"), 0644); err != nil {
		t.Fatal(err)
	}
	var requests []preflightapi.CreateVerificationSessionRequest
	server := captureExecutionRequests(t, &requests)

	report, err := buildAgentPlanReport(server.URL, "", "", "", "", "", false)
	if err != nil || report.Status != "ready" || report.Mode != "affected_rules" {
		t.Fatalf("agent plan = %+v, %v; want a ready affected-rules plan", report, err)
	}
	preparation := ""
	for _, warning := range report.Warnings {
		if warning.Section == "preparation" {
			preparation = warning.Message
		}
	}
	if !strings.Contains(preparation, "dependencies; network_mode=allowlist; network_allowlist=registry.npmjs.org") {
		t.Fatalf("agent plan warnings = %+v, want the preparation network", report.Warnings)
	}

	for _, args := range [][]string{
		{"affected", "--api-url", server.URL, "--dry-run", "--json"},
		{"check", "--api-url", server.URL, "--dry-run", "--json"},
	} {
		output, err := captureStdoutForCLITest(t, func() error { return run(args) })
		if err != nil || !json.Valid([]byte(output)) {
			t.Fatalf("%v: err=%v output=%s", args, err, output)
		}
	}

	// readyci verify plans locally before reading the registered policy.
	prepared, err := prepareVerificationSession(t.Context(), verificationSessionOptions{APIURL: server.URL}, agentIntentEditLoop)
	if err != nil || len(prepared.WarmPlans) == 0 || len(prepared.Plans) == 0 {
		t.Fatalf("verify preparation = %+v, %v; want preparation and its consumer", prepared, err)
	}
	for _, plan := range append(prepared.WarmPlans, prepared.Plans...) {
		if plan.Options.ChangeMode != prepared.Plans[0].Options.ChangeMode {
			t.Fatalf("verify plan %s change mode %q, consumer %q", plan.Name, plan.Options.ChangeMode, prepared.Plans[0].Options.ChangeMode)
		}
	}

	requests = nil
	if err := run([]string{"affected", "--api-url", server.URL}); err == nil || len(requests) != 1 {
		t.Fatalf("affected: err=%v requests=%d, want the session to reach creation", err, len(requests))
	}
	// The session's change mode governs every node, including preparation.
	warm := 0
	for _, node := range requests[0].Plan.Nodes {
		if node.Phase == preflightapi.VerificationNodePhaseWarm {
			warm++
		}
	}
	if warm == 0 || requests[0].Snapshot.ChangeMode != prepared.Plans[0].Options.ChangeMode {
		t.Fatalf("affected session = %d warm nodes, change mode %q; want preparation in %q", warm, requests[0].Snapshot.ChangeMode, prepared.Plans[0].Options.ChangeMode)
	}
}
