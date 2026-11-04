package verificationplan

import (
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

func TestDependencyInstallerCanExcludeCompileTimeAssets(t *testing.T) {
	cfg, err := config.ParseProjectConfig([]byte(`network:
  mode: none
  warm_mode: allowlist
  warm_allowlist: [github.com, index.crates.io, static.crates.io]
checks:
  dependencies:
    command: cargo fetch --locked
    sync: {exclude: [apps/backend/assets]}
  rust-test:
    command: cargo test --offline
    sync: {exclude: [apps/web/public]}
`), "airline dependency scopes")
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := ResolveCheck("rust-test", cfg, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	warm, err := DependencyWarmPlansForChecks(cfg, []CheckPlan{consumer}, ResolveOptions{})
	if err != nil {
		t.Fatalf("installer cannot omit assets needed only during compilation: %v", err)
	}
	nodes, _, err := NodesForPlans(warm, []CheckPlan{consumer}, BundleIdentity{}, ClientIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 || nodes[0].Run.NetworkMode != "allowlist" || nodes[1].Run.NetworkMode != "none" || len(nodes[1].SuccessDependsOn) != 1 || nodes[1].SuccessDependsOn[0] != nodes[0].ID {
		t.Fatalf("source optimization lost preparation ordering or network boundaries: %+v", nodes)
	}
	if len(warm[0].Run.SyncExclude) != 1 || warm[0].Run.SyncExclude[0] != "apps/backend/assets" || consumer.Run.SyncExclude[0] != "apps/web/public" {
		t.Fatal("planner replaced the explicitly declared source views")
	}
}
