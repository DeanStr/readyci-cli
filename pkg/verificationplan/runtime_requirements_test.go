package verificationplan

import (
	"reflect"
	"strconv"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestRuntimeRequirementsSurviveResolutionNodesAndWarmPlacement(t *testing.T) {
	cfg, err := config.ParseProjectConfig([]byte("version: 1\nchecks:\n  test:\n    command: go test ./...\n    runtime_requirements:\n      go: '>=1.25'\n"), "runtimefixture")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := ResolveCheck("test", cfg, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	warm, err := DependencyWarmPlansForTargets(CheckPlan{Name: "dependencies", Run: RunSpec{Command: "go mod download"}}, []CheckPlan{plan})
	if err != nil || len(warm) != 1 {
		t.Fatalf("warm=%v err=%v", warm, err)
	}
	for _, run := range []RunSpec{plan.Run, warm[0].Run} {
		if run.RuntimeRequirements["go"] != ">=1.25" {
			t.Fatalf("runtime requirement disappeared %v", run.RuntimeRequirements)
		}
	}
	nodes, _, err := NodesForPlans(warm, []CheckPlan{plan}, BundleIdentity{}, ClientIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range nodes {
		if !reflect.DeepEqual(node.Run.RuntimeRequirements, map[string]string{"go": ">=1.25"}) {
			t.Fatalf("sealed node omitted runtime requirements %v", node.Run)
		}
	}
	nodes[0].Run.RuntimeRequirements["go"] = "1.26"
	if plan.Run.RuntimeRequirements["go"] != ">=1.25" {
		t.Fatal("mutable node requirements shared with resolved plan")
	}
}
func TestRuntimeRequirementsPartitionWarmTargetsAndBindConsumers(t *testing.T) {
	targets := []CheckPlan{
		{Name: "node22", Run: RunSpec{Command: "node --test", CommandAlias: "node22", RuntimeRequirements: map[string]string{"node": "22"}}},
		{Name: "node24", Run: RunSpec{Command: "node --test", CommandAlias: "node24", RuntimeRequirements: map[string]string{"node": "24"}}},
		{Name: "node24-compatible", Run: RunSpec{Command: "node --test", CommandAlias: "node24-compatible", RuntimeRequirements: map[string]string{"node": ">=24"}}},
	}
	warmPlan := CheckPlan{Name: "dependencies", Run: RunSpec{Command: "npm ci"}}
	warm, err := DependencyWarmPlansForTargets(warmPlan, targets)
	if err != nil || len(warm) != 3 {
		t.Fatalf("independent runtime preparation = %v error=%v", warm, err)
	}
	nodes, _, err := NodesForPlans(warm, targets, BundleIdentity{}, ClientIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range nodes {
		if node.Phase == preflightapi.VerificationNodePhaseWarm {
			if len(node.SuccessDependsOn) != 0 {
				t.Fatalf("independent runtimes share a prepared cache: %+v", node)
			}
			continue
		}
		if len(node.SuccessDependsOn) != 1 {
			t.Fatalf("consumer must bind its own preparation: %+v", node)
		}
		var prerequisite *preflightapi.VerificationNodePlan
		for i := range nodes {
			if nodes[i].ID == node.SuccessDependsOn[0] {
				prerequisite = &nodes[i]
			}
		}
		version := "24.18.0"
		if node.ID == "node22" {
			version = "22.23.1"
		}
		if prerequisite == nil || !preflightapi.RuntimeVersionSatisfies(version, prerequisite.Run.RuntimeRequirements["node"]) {
			t.Fatalf("consumer %s bound incompatible preparation: %+v", node.ID, prerequisite)
		}
	}
	reversed := []CheckPlan{targets[2], targets[1], targets[0]}
	again, err := DependencyWarmPlansForTargets(warmPlan, reversed)
	if err != nil || !reflect.DeepEqual(warm, again) {
		t.Fatalf("input order changed preparation graph: %v %v", again, err)
	}
}

func TestRuntimeRequirementsWarmGroupingKeepsExplicitConstraintsAndDeduplicates(t *testing.T) {
	warm := CheckPlan{Name: "dependencies", Run: RunSpec{Command: "npm ci", RuntimeRequirements: map[string]string{"node": ">=22"}}}
	var targets []CheckPlan
	for i := range 40 {
		targets = append(targets, CheckPlan{Name: "consumer" + strconv.Itoa(i), Run: RunSpec{RuntimeRequirements: map[string]string{"node": "24"}}})
	}
	plans, err := DependencyWarmPlansForTargets(warm, targets)
	if err != nil || len(plans) != 1 || !preflightapi.RuntimeVersionSatisfies("24.18.0", plans[0].Run.RuntimeRequirements["node"]) {
		t.Fatalf("repeated consumers exhausted constraint grammar: %+v err=%v", plans, err)
	}
	warm.Run.RuntimeRequirements["node"] = "22"
	if _, err := DependencyWarmPlansForTargets(warm, targets); err == nil {
		t.Fatal("consumer bypassed the dependency command's explicit runtime constraint")
	}
}

func TestRuntimePreparationAndConsumerSelectTheSameVersion(t *testing.T) {
	targets := []CheckPlan{
		{Name: "node22", Run: RunSpec{Command: "node --test", CommandAlias: "node22", RuntimeRequirements: map[string]string{"node": "22"}}},
		{Name: "compatible", Run: RunSpec{Command: "node --test", CommandAlias: "compatible", RuntimeRequirements: map[string]string{"node": ">=22"}}},
	}
	warm, err := DependencyWarmPlansForTargets(CheckPlan{Name: "dependencies", Run: RunSpec{Command: "npm ci"}}, targets)
	if err != nil || len(warm) != 2 {
		t.Fatalf("compatible preparation = %+v err=%v", warm, err)
	}
	nodes, _, err := NodesForPlans(warm, targets, BundleIdentity{}, ClientIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	inventory := preflightapi.RuntimeInventory{"node": {"22.23.1", "24.18.0"}}
	versions := map[string]string{}
	for _, node := range nodes {
		selected, err := preflightapi.ResolveRuntimePreparation(preflightapi.RunSpec{RuntimeRequirements: node.Run.RuntimeRequirements}, inventory, nil)
		if err != nil {
			t.Fatal(err)
		}
		versions[node.ID] = selected.Versions["node"]
		if node.Phase == preflightapi.VerificationNodePhaseVerify && (len(node.SuccessDependsOn) != 1 || versions[node.SuccessDependsOn[0]] != selected.Versions["node"]) {
			t.Fatalf("node %s selected a different runtime from its preparation: versions=%v err=%v", node.ID, selected.Versions, err)
		}
	}
	if versions["node22"] != "22.23.1" || versions["compatible"] != "24.18.0" {
		t.Fatalf("another consumer changed a check's selected runtime: %v", versions)
	}
	if targets[1].Run.RuntimeRequirements["node"] != ">=22" {
		t.Fatal("planner mutated the policy's declared runtime requirements")
	}
}

func TestWarmConsumerDoesNotRequirePreparationOnlySDK(t *testing.T) {
	targets := []CheckPlan{{Name: "node", Run: RunSpec{
		Command: "node --test", RuntimeRequirements: map[string]string{"node": "22"}, CachePaths: []string{"node_modules"},
	}}}
	warm, err := DependencyWarmPlansForTargets(CheckPlan{Name: "dependencies", Run: RunSpec{
		Command: "prepare-deps", RuntimeRequirements: map[string]string{"go": "1.27"},
	}}, targets)
	if err != nil {
		t.Fatal(err)
	}
	nodes, _, err := NodesForPlans(warm, targets, BundleIdentity{}, ClientIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	// Go can be provisioned only during the network-enabled preparation. The
	// offline Node consumer does not need that SDK to load its dependencies.
	consumer := nodes[len(nodes)-1].Run
	selected, err := preflightapi.ResolveRuntimePreparation(preflightapi.RunSpec{
		RuntimeRequirements: consumer.RuntimeRequirements, NetworkMode: "none",
	}, preflightapi.RuntimeInventory{"node": {"22.23.1"}}, nil)
	if err != nil || selected.Versions["node"] != "22.23.1" {
		t.Fatalf("consumer inherited a preparation-only SDK: requirements=%v err=%v", consumer.RuntimeRequirements, err)
	}
}
