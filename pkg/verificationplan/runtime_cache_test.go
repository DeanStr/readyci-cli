package verificationplan

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestRuntimeGroupsKeepSeparateDependenciesOnTheSameCacheLane(t *testing.T) {
	for _, keyed := range []bool{false, true} {
		name := "unkeyed"
		var keyFiles []string
		if keyed {
			name, keyFiles = "lockfile", []string{"package-lock.json"}
		}
		t.Run(name, func(t *testing.T) {
			cache := t.TempDir()
			var targets []CheckPlan
			for _, version := range []string{"22", "24"} {
				targets = append(targets, CheckPlan{Name: "node" + version, Run: RunSpec{
					Command:             `test "$(cat node_modules/runtime)" = ` + version,
					RuntimeRequirements: map[string]string{"node": version},
					CachePaths:          []string{"node_modules"}, CacheKeyFiles: keyFiles,
				}})
			}
			targets = append(targets, CheckPlan{Name: "compatible22", Run: RunSpec{
				Command:             `test "$(cat node_modules/runtime)" = 22`,
				RuntimeRequirements: map[string]string{"node": ">=22,<23"},
				CachePaths:          []string{"node_modules"}, CacheKeyFiles: keyFiles,
			}})
			warm, err := DependencyWarmPlansForTargets(CheckPlan{Name: "dependencies", Run: RunSpec{RuntimeRequirements: map[string]string{"go": "1.27"}}}, targets)
			if err != nil {
				t.Fatal(err)
			}
			for i := range warm {
				selected, err := preflightapi.ResolveRuntimePreparation(preflightapi.RunSpec{
					RuntimeRequirements: map[string]string{"node": warm[i].Run.RuntimeRequirements["node"]},
				}, preflightapi.RuntimeInventory{"node": {"22.23.1", "24.18.0"}}, nil)
				if err != nil {
					t.Fatal(err)
				}
				major, _, _ := strings.Cut(selected.Versions["node"], ".")
				// Installers can unlink and recreate the mapped directory rather
				// than writing through its original cache symlink.
				warm[i].Run.Command = "rm -rf node_modules; mkdir node_modules; printf '%s' " + ShellQuoteArg(major) + " > node_modules/runtime"
			}
			nodes, _, err := NodesForPlans(warm, targets, BundleIdentity{}, ClientIdentity{})
			if err != nil {
				t.Fatal(err)
			}
			for _, node := range nodes {
				workspace := t.TempDir()
				if err := os.WriteFile(filepath.Join(workspace, "package-lock.json"), []byte("same-lockfile"), 0600); err != nil {
					t.Fatal(err)
				}
				// Both preparations and consumers reuse the same physical lane,
				// exactly as a worker with both installed Node versions can do.
				command := strings.ReplaceAll(node.Run.Command, "/cache", filepath.ToSlash(cache))
				run := exec.Command("bash", "-c", command)
				run.Dir = workspace
				run.Env = append(os.Environ(), "READYCI_CACHE_LANE_ID=shared-lane", "READYCI_WORKSPACE_DIR="+workspace)
				if output, err := run.CombinedOutput(); err != nil {
					t.Fatalf("%s (%s) consumed another runtime's prepared dependencies: %v %s", node.ID, node.Phase, err, output)
				}
				if node.Phase == preflightapi.VerificationNodePhaseWarm && len(node.SuccessDependsOn) != 0 {
					t.Fatal("independent runtime preparations unexpectedly share a success chain")
				}
			}
		})
	}
}

func TestSeparateWarmSessionCacheIdentityDoesNotDependOnSelectedChecks(t *testing.T) {
	for _, sdk := range []bool{false, true} {
		name := "consumer-runtimes-only"
		var preparationRequirements map[string]string
		if sdk {
			name, preparationRequirements = "preparation-only-sdk", map[string]string{"go": "1.27"}
		}
		t.Run(name, func(t *testing.T) {
			cfg := config.ProjectConfig{Cache: config.CacheConfig{Paths: []string{"node_modules"}}, Checks: map[string]config.CheckConfig{
				"dependencies": {Command: "printf '%s' prepared > node_modules/ready", RuntimeRequirements: preparationRequirements},
				"node22":       {Command: `test "$(cat node_modules/ready)" = prepared`, RuntimeRequirements: map[string]string{"node": "22"}},
				"broad":        {Command: `test "$(cat node_modules/ready)" = prepared`, RuntimeRequirements: map[string]string{"node": ">=22"}},
			}}
			var targets []CheckPlan
			for _, name := range []string{"node22", "broad"} {
				plan, err := ResolveCheck(name, cfg, ResolveOptions{})
				if err != nil {
					t.Fatal(err)
				}
				targets = append(targets, plan)
			}
			warmPlan, err := ResolveCheck("dependencies", cfg, ResolveOptions{})
			if err != nil {
				t.Fatal(err)
			}
			warm, err := DependencyWarmPlansForTargets(warmPlan, targets)
			if err != nil {
				t.Fatal(err)
			}
			nodes, _, err := NodesForPlans(warm, nil, BundleIdentity{}, ClientIdentity{})
			if err != nil {
				t.Fatal(err)
			}
			for _, target := range targets {
				// Each offline consumer is compiled in a separate session with no
				// warm nodes and only one selected check.
				cold, _, err := NodesForPlans(nil, []CheckPlan{target}, BundleIdentity{}, ClientIdentity{})
				if err != nil {
					t.Fatal(err)
				}
				nodes = append(nodes, cold...)
			}
			cache := t.TempDir()
			for _, node := range nodes {
				workspace := t.TempDir()
				run := exec.Command("bash", "-c", strings.ReplaceAll(node.Run.Command, "/cache", filepath.ToSlash(cache)))
				run.Dir = workspace
				run.Env = append(os.Environ(), "READYCI_CACHE_LANE_ID=same-lane", "READYCI_WORKSPACE_DIR="+workspace)
				if output, err := run.CombinedOutput(); err != nil {
					t.Fatalf("separate %s consumer lost prepared cache: %v %s", node.ID, err, output)
				}
			}
		})
	}
}
