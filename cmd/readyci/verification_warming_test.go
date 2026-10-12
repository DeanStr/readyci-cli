package main

import (
	"os"
	"testing"

	"github.com/DeanStr/readyci-cli/internal/syncbundlebuild"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestExplicitVerificationPreparesItsOwnCache(t *testing.T) {
	chdirCLITestRepo(t, "verification-warming")
	runGitForCLITest(t, "init")
	if err := os.WriteFile(".readyci.yml", []byte(`project_id: proj_dev
network:
  mode: none
  warm_mode: allowlist
  warm_allowlist: [proxy.golang.org]
cache:
  paths: [.deps]
checks:
  dependencies:
    command: prepare-deps
  unit:
    command: test-offline
`), 0644); err != nil {
		t.Fatal(err)
	}
	opts := verificationSessionOptions{APIURL: "http://localhost", CheckName: "unit"}
	prepared, err := prepareVerificationSession(t.Context(), opts, agentIntentEditLoop)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.WarmPlans) != 1 {
		t.Fatalf("explicit verification has %d warm prerequisites, want 1", len(prepared.WarmPlans))
	}
	nodes, _, err := verificationNodesForPlans(prepared.WarmPlans, prepared.Plans, syncbundlebuild.BuildResult{})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 || nodes[0].Phase != preflightapi.VerificationNodePhaseWarm ||
		nodes[0].Run.NetworkMode != "allowlist" || nodes[1].Run.NetworkMode != "none" ||
		len(nodes[1].SuccessDependsOn) != 1 || nodes[1].SuccessDependsOn[0] != nodes[0].ID {
		t.Fatalf("warming must gate the offline check in the same session: %+v", nodes)
	}
	preview, err := buildAgentPlanReport("http://localhost", "proj_dev", "unit", "", agentIntentEditLoop, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Warnings) == 0 {
		t.Fatal("preview must disclose automatic preparation and its network policy")
	}
	secure, err := prepareVerificationSession(t.Context(), opts, agentIntentSecurity)
	if err != nil {
		t.Fatal(err)
	}
	if len(secure.WarmPlans) != 0 || secure.Plans[0].Options.NetworkMode != "none" {
		t.Fatal("security-sensitive verification must remain offline")
	}
}
