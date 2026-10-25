package verificationplan

import (
	"reflect"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestEnvironmentRequirementsSurviveResolutionWarmAndSealedNodes(t *testing.T) {
	cfg, err := config.ParseProjectConfig([]byte("version: 1\nenvironment_requirements: [network:ipv6-loopback]\nchecks:\n  test:\n    command: cargo test\n    environment_requirements: [filesystem:btrfs]\n"), "requirements.yml")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := ResolveCheck("test", cfg, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{preflightapi.EnvironmentFilesystemBtrfs, preflightapi.EnvironmentIPv6Loopback}
	if !reflect.DeepEqual(plan.Run.EnvironmentRequirements, want) {
		t.Fatalf("project/check requirements not combined: %v", plan.Run.EnvironmentRequirements)
	}
	warm, err := DependencyWarmPlansForTargets(CheckPlan{Name: "dependencies", Run: RunSpec{Command: "cargo fetch", EnvironmentRequirements: []string{preflightapi.EnvironmentFilesystemMinix}}}, []CheckPlan{plan})
	if err != nil || len(warm) != 1 {
		t.Fatalf("warm=%v err=%v", warm, err)
	}
	if !reflect.DeepEqual(warm[0].Run.EnvironmentRequirements, []string{preflightapi.EnvironmentFilesystemBtrfs, preflightapi.EnvironmentFilesystemMinix, preflightapi.EnvironmentIPv6Loopback}) {
		t.Fatalf("installer lost preparation/consumer placement requirements: %v", warm[0].Run.EnvironmentRequirements)
	}
	nodes, _, err := NodesForPlans(warm, []CheckPlan{plan}, BundleIdentity{}, ClientIdentity{})
	if err != nil || len(nodes) != 2 {
		t.Fatalf("nodes=%v err=%v", nodes, err)
	}
	if !reflect.DeepEqual(nodes[1].Run.EnvironmentRequirements, want) {
		t.Fatalf("consumer lost requirements or inherited installer-only capability: %v", nodes[1].Run.EnvironmentRequirements)
	}
	nodes[1].Run.EnvironmentRequirements[0] = preflightapi.EnvironmentIPv6Interface
	if !reflect.DeepEqual(plan.Run.EnvironmentRequirements, want) {
		t.Fatal("sealed requirements alias the resolved plan")
	}
}

func TestEnvironmentRequirementsStayWithTheirRuntimeWarmGroup(t *testing.T) {
	warm := CheckPlan{Name: "dependencies", Run: RunSpec{
		Command: "go mod download", CommandAlias: "dependencies",
		EnvironmentRequirements: []string{preflightapi.EnvironmentIPv6Loopback},
	}}
	targets := []CheckPlan{
		{Name: "btrfs", Run: RunSpec{
			Command: "go test ./...", CommandAlias: "btrfs",
			RuntimeRequirements:     map[string]string{"go": "1.24"},
			EnvironmentRequirements: []string{preflightapi.EnvironmentFilesystemBtrfs},
			CachePaths:              []string{".cache/go124"}, CacheLaneSizeBytes: 1 << 20,
		}},
		{Name: "plain", Run: RunSpec{
			Command: "go test ./...", CommandAlias: "plain",
			RuntimeRequirements: map[string]string{"go": "1.25"},
			CachePaths:          []string{".cache/go125"}, CacheLaneSizeBytes: 1 << 20,
		}},
	}
	warmPlans, err := DependencyWarmPlansForTargets(warm, targets)
	if err != nil || len(warmPlans) != 2 {
		t.Fatalf("runtime warm groups=%+v, err=%v", warmPlans, err)
	}
	warmIDs := map[string]string{}
	for _, plan := range warmPlans {
		if len(plan.DependencyWarmTargets) != 1 {
			t.Fatalf("warm group does not own one target: %+v", plan)
		}
		target := plan.DependencyWarmTargets[0]
		want := []string{preflightapi.EnvironmentIPv6Loopback}
		if target == "btrfs" {
			want = []string{preflightapi.EnvironmentFilesystemBtrfs, preflightapi.EnvironmentIPv6Loopback}
		}
		if !reflect.DeepEqual(plan.Run.EnvironmentRequirements, want) {
			t.Fatalf("warm group for %s inherited unrelated environment requirements: got %v, want %v", target, plan.Run.EnvironmentRequirements, want)
		}
		if !reflect.DeepEqual(plan.Run.CachePaths, []string{".cache/go124", ".cache/go125"}) {
			t.Fatalf("warm group %s lost the complete cache-path union: %v", target, plan.Run.CachePaths)
		}
		warmIDs[target] = plan.LogicalChildID
	}
	nodes, _, err := NodesForPlans(warmPlans, targets, BundleIdentity{}, ClientIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range nodes {
		if node.Phase == preflightapi.VerificationNodePhaseWarm {
			if len(node.SuccessDependsOn) != 0 {
				t.Fatalf("independent warm group acquired another prepared filesystem: %+v", node)
			}
			continue
		}
		if !reflect.DeepEqual(node.SuccessDependsOn, []string{warmIDs[node.ID]}) {
			t.Fatalf("consumer %s does not bind only its own warm group: %v", node.ID, node.SuccessDependsOn)
		}
	}
	reversed, err := DependencyWarmPlansForTargets(warm, []CheckPlan{targets[1], targets[0]})
	if err != nil {
		t.Fatal(err)
	}
	for _, plan := range reversed {
		if plan.LogicalChildID != warmIDs[plan.DependencyWarmTargets[0]] {
			t.Fatalf("warm node identity depends on target order: %+v", plan)
		}
	}
	warmPlans[0].Run.EnvironmentRequirements[0] = preflightapi.EnvironmentFilesystemMinix
	if !reflect.DeepEqual(warm.Run.EnvironmentRequirements, []string{preflightapi.EnvironmentIPv6Loopback}) ||
		!reflect.DeepEqual(targets[0].Run.EnvironmentRequirements, []string{preflightapi.EnvironmentFilesystemBtrfs}) {
		t.Fatal("specialized warm requirements alias their input declarations")
	}
}
