package main

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/internal/syncbundlebuild"
	"github.com/DeanStr/readyci-cli/pkg/config"
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
    runtime_requirements: {go: "1.27"}
  unit:
    command: test-offline
    runtime_requirements: {node: "22"}
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
	cacheMappings := func(command string) []string {
		var mappings []string
		for _, line := range strings.Split(command, "\n") {
			if strings.HasPrefix(line, "  __preflight_link_cache_path ") {
				mappings = append(mappings, line)
			}
		}
		return mappings
	}
	preparedMappings := cacheMappings(nodes[0].Run.Command)
	if len(preparedMappings) == 0 {
		t.Fatal("preparation omitted configured cache paths")
	}
	opts.SkipWarm = true
	skipped, err := prepareVerificationSession(t.Context(), opts, agentIntentEditLoop)
	if err != nil {
		t.Fatal(err)
	}
	for _, offline := range []preparedVerificationSession{secure, skipped} {
		offlineNodes, _, err := verificationNodesForPlans(offline.WarmPlans, offline.Plans, syncbundlebuild.BuildResult{})
		if err != nil {
			t.Fatal(err)
		}
		if len(offlineNodes) != 1 || !reflect.DeepEqual(cacheMappings(offlineNodes[0].Run.Command), preparedMappings) {
			t.Fatalf("omitting preparation lost its cache namespace: %+v", offlineNodes)
		}
		if offlineNodes[0].Run.RuntimeRequirements["go"] != "" {
			t.Fatal("offline consumer inherited a preparation-only SDK")
		}
	}
}

func TestVerificationPreparationRetainsEachRuntimeGroup(t *testing.T) {
	cfg, err := config.ParseProjectConfig([]byte(`project_id: proj_dev
network:
  mode: none
  warm_mode: allowlist
  warm_allowlist: [registry.npmjs.org]
checks:
  dependencies:
    command: npm ci
  node22:
    command: npm test
    runtime_requirements: {node: "22"}
  node24:
    command: npm test
    runtime_requirements: {node: "24"}
`), "runtime-groups")
	if err != nil {
		t.Fatal(err)
	}
	var selected []affectedCheckPlan
	var targets []checkRunPlan
	for _, name := range []string{"node22", "node24"} {
		plan, err := resolveCheckRunPlan(name, cfg, checkResolveOptions{RetryCount: -1})
		if err != nil {
			t.Fatal(err)
		}
		targets = append(targets, plan)
		selected = append(selected, affectedCheckPlan{Plan: plan})
	}
	resolved, err := dependencyWarmPlansForAffected(".", selected, cfg, checkResolveOptions{RetryCount: -1})
	if err != nil || len(resolved) != 2 {
		t.Fatalf("runtime preparations=%+v err=%v", resolved, err)
	}
	var warm []checkRunPlan
	for _, plan := range resolved {
		warm = append(warm, plan.Plan)
	}
	nodes, _, err := verificationNodesForPlans(warm, targets, syncbundlebuild.BuildResult{})
	if err != nil {
		t.Fatal(err)
	}
	for _, consumer := range nodes[2:] {
		if len(consumer.SuccessDependsOn) != 1 {
			t.Fatalf("consumer must bind exactly one runtime group: %+v", consumer)
		}
		for _, preparation := range nodes[:2] {
			if preparation.ID == consumer.SuccessDependsOn[0] && preparation.Run.RuntimeRequirements["node"] != consumer.Run.RuntimeRequirements["node"] {
				t.Fatalf("CLI lost preparation's runtime selection: %+v for %+v", preparation, consumer)
			}
		}
	}
}
