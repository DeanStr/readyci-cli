package verificationplan

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

func TestComponentInstallersCannotOverwriteEachOthersCachedPaths(t *testing.T) {
	cfg := config.DefaultProjectConfig()
	cfg.Network = config.NetworkConfig{WarmMode: "allowlist", WarmAllowlist: []string{"registry.npmjs.org"}}
	cfg.Cache.Paths = []string{"node_modules"}
	cfg.Checks = map[string]config.CheckConfig{
		"prepare-a": {Command: "printf a > node_modules/prepared"},
		"prepare-b": {Command: "printf b > node_modules/prepared"},
		"test-a":    {Command: `test "$(cat node_modules/prepared)" = a`, DependencyCheck: "prepare-a"},
		"test-b":    {Command: `test "$(cat node_modules/prepared)" = b`, DependencyCheck: "prepare-b"},
	}
	var consumers []CheckPlan
	for _, name := range []string{"test-a", "test-b"} {
		plan, err := ResolveCheck(name, cfg, ResolveOptions{})
		if err != nil {
			t.Fatal(err)
		}
		consumers = append(consumers, plan)
	}
	warm, err := DependencyWarmPlansForChecks(cfg, consumers, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	nodes, _, err := NodesForPlans(warm, consumers, BundleIdentity{}, ClientIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()
	for _, node := range nodes {
		workspace := t.TempDir()
		command := exec.Command("bash", "-c", strings.ReplaceAll(node.Run.Command, "/cache", filepath.ToSlash(cache)))
		command.Dir = workspace
		command.Env = append(os.Environ(), "READYCI_CACHE_LANE_ID=shared", "READYCI_WORKSPACE_DIR="+workspace)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v %s", node.ID, err, output)
		}
	}
	// A later explicit skip-warm run must address the same component cache.
	for _, plan := range consumers {
		command, err := CommandForRunSpec(plan.Run)
		if err != nil {
			t.Fatal(err)
		}
		workspace := t.TempDir()
		run := exec.Command("bash", "-c", strings.ReplaceAll(command, "/cache", filepath.ToSlash(cache)))
		run.Dir = workspace
		run.Env = append(os.Environ(), "READYCI_CACHE_LANE_ID=shared", "READYCI_WORKSPACE_DIR="+workspace)
		if output, err := run.CombinedOutput(); err != nil {
			t.Fatalf("standalone %s: %v %s", plan.Name, err, output)
		}
	}
}

const componentDependencyConfig = `network:
  mode: none
  warm_mode: allowlist
  warm_allowlist: [registry.npmjs.org, crates.io]
checks:
  dependencies:
    command: cargo fetch --locked
    runtime_requirements: {rust: "1.90"}
    sync: {exclude: [apps/web]}
  web-dependencies:
    command: pnpm install --frozen-lockfile
    runtime_requirements: {node: "24", pnpm: "11"}
    sync: {exclude: [apps/server]}
  web-test:
    command: pnpm test
    dependency_check: web-dependencies
    runtime_requirements: {node: "24"}
    sync: {exclude: [apps/server]}
    cache: {paths: [node_modules]}
  rust-test:
    command: cargo test --offline
    runtime_requirements: {rust: "1.90"}
    sync: {exclude: [apps/web]}
`

func TestComponentDependencyPreparation(t *testing.T) {
	cfg, err := config.ParseProjectConfig([]byte(componentDependencyConfig), "component dependencies")
	if err != nil {
		t.Fatal(err)
	}
	// Alphabetically earlier installers must never become the recommended
	// verification check just because this project has no standard test alias.
	recommendation, err := RecommendCheck(cfg, "", "", "edit-loop")
	if err != nil || recommendation.Check != "rust-test" {
		t.Fatalf("installer recommended as verification: %+v, %v", recommendation, err)
	}
	web, err := ResolveCheck("web-test", cfg, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, leaked := web.Run.CacheRuntimeRequirements["rust"]; leaked {
		t.Fatal("web cache inherited unrelated Rust preparation")
	}
	rust, err := ResolveCheck("rust-test", cfg, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	warm, err := DependencyWarmPlansForChecks(cfg, []CheckPlan{web, rust}, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(warm) != 2 {
		t.Fatalf("warm plans: %+v", warm)
	}
	nodes, _, err := NodesForPlans(warm, []CheckPlan{web, rust}, BundleIdentity{}, ClientIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range nodes {
		if node.Phase != "verify" {
			continue
		}
		want := "warm:dependencies"
		if node.ID == "web-test" {
			want = "warm:web-dependencies"
		}
		if node.Run.NetworkMode != "none" || len(node.SuccessDependsOn) != 1 || node.SuccessDependsOn[0] != want {
			t.Fatalf("consumer lost its offline preparation binding: %+v", node)
		}
	}
}

func TestDependencyCheckRejectsInvalidReferences(t *testing.T) {
	for _, declaration := range []string{
		"test: {command: test, dependency_check: missing}",
		"test: {command: test, dependency_check: test}",
		"test: {command: test, dependency_check: warm}\n  warm: {command: install, dependency_check: other}\n  other: {command: install}",
	} {
		if _, err := config.ParseProjectConfig([]byte("checks:\n  "+declaration+"\n"), "invalid dependencies"); err == nil || !strings.Contains(err.Error(), "dependency_check") {
			t.Fatalf("invalid dependency reference accepted: %s: %v", declaration, err)
		}
	}
}
