package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

const componentDependencyCLIConfig = `project_id: review
network:
  mode: none
  warm_mode: allowlist
  warm_allowlist: [registry.npmjs.org]
checks:
  dependencies:
    command: cargo fetch --locked
    sync: {exclude: [apps/web]}
  web-dependencies:
    command: pnpm install --frozen-lockfile
    sync: {exclude: [apps/server]}
  web-test:
    command: pnpm test
    dependency_check: web-dependencies
    sync: {exclude: [apps/server]}
affected:
  rules:
    - paths: ["apps/web/**"]
      checks: [web-test]
`

func TestComponentDependenciesShareOneExecutionSession(t *testing.T) {
	chdirCLITestRepo(t, "component-dependencies")
	runGitForCLITest(t, "init")
	if err := os.WriteFile(".readyci.yml", []byte(componentDependencyCLIConfig), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("apps/web", 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("apps/web/test.js", []byte("test change"), 0644); err != nil {
		t.Fatal(err)
	}
	var requests []preflightapi.CreateVerificationSessionRequest
	server := captureExecutionRequests(t, &requests)
	if err := run([]string{"check", "--api-url", server.URL, "web-test"}); err == nil {
		t.Fatal("fixture should stop after capturing creation")
	}
	if len(requests) != 1 || len(requests[0].Plan.Nodes) != 2 {
		t.Fatalf("one warm/check session required: %+v", requests)
	}
	warm, check := requests[0].Plan.Nodes[0], requests[0].Plan.Nodes[1]
	if warm.ID != "warm:web-dependencies" || !strings.Contains(warm.Run.Command, "pnpm install --frozen-lockfile") || strings.Contains(warm.Run.Command, "cargo fetch") || warm.Run.NetworkMode != "allowlist" || warm.Run.SecretsRequested == nil || *warm.Run.SecretsRequested {
		t.Fatalf("wrong component preparation: %+v", warm)
	}
	if check.Run.NetworkMode != "none" || len(check.SuccessDependsOn) != 1 || check.SuccessDependsOn[0] != warm.ID {
		t.Fatalf("offline check lost lane binding: %+v", check)
	}
	if strings.Join(requests[0].Recipe.Source.ExcludePatterns, ",") != "apps/server" {
		t.Fatalf("component source changed: %+v", requests[0].Recipe.Source)
	}

	_, err := prepareVerificationSession(context.Background(), verificationSessionOptions{APIURL: server.URL, CheckName: "web-dependencies", RequestSecrets: true}, agentIntentEditLoop)
	if err == nil || !strings.Contains(err.Error(), "no-secrets") {
		t.Fatalf("component installer accepted secrets: %v", err)
	}
	prepared, err := prepareVerificationSession(context.Background(), verificationSessionOptions{APIURL: server.URL, CheckName: "web-dependencies"}, agentIntentEditLoop)
	if err != nil || len(prepared.WarmPlans) != 1 || len(prepared.Plans) != 0 {
		t.Fatalf("explicit component preparation: %+v, %v", prepared, err)
	}
	report, err := buildAgentPlanReport(server.URL, "review", "web-dependencies", "", agentIntentEditLoop, "", false)
	if err != nil || report.Check != "web-dependencies" {
		t.Fatalf("component warm preview: %+v, %v", report, err)
	}
}

func TestSharedSourceMonorepoUsesOneAffectedSession(t *testing.T) {
	chdirCLITestRepo(t, "shared-source-components")
	runGitForCLITest(t, "init")
	runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	writeFileForRepoMapTest(t, ".readyci.yml", `project_id: review
sync: {include_untracked: true, exclude: [build-artifacts]}
network: {mode: none, warm_mode: allowlist, warm_allowlist: [registry.npmjs.org, index.crates.io, static.crates.io]}
checks:
  web-dependencies:
    command: pnpm --dir apps/web install --frozen-lockfile
  web-test:
    command: pnpm --dir apps/web test
    dependency_check: web-dependencies
  server-dependencies:
    command: cargo fetch --manifest-path apps/server/Cargo.toml --locked
  server-test:
    command: cargo test --manifest-path apps/server/Cargo.toml --offline
    dependency_check: server-dependencies
affected:
  rules:
    - paths: [apps/web/**]
      checks: [web-test]
    - paths: [apps/server/**]
      checks: [server-test]
`)
	runGitForCLITest(t, "add", ".readyci.yml")
	runGitForCLITest(t, "commit", "-m", "fixture")
	writeFileForRepoMapTest(t, "apps/web/test.js", "web change\n")
	writeFileForRepoMapTest(t, "apps/server/main.rs", "server change\n")
	cfg, _, err := config.LoadProjectConfig(".")
	if err != nil {
		t.Fatal(err)
	}
	if warning := sharedSourceScopeWarning(cfg); warning != "" {
		t.Fatal(warning)
	}
	var requests []preflightapi.CreateVerificationSessionRequest
	server := captureExecutionRequests(t, &requests)
	if err := checkCommand([]string{"--api-url", server.URL}); err == nil {
		t.Fatal("fixture should stop after capturing creation")
	}
	if len(requests) != 1 || len(requests[0].Plan.Nodes) != 4 {
		t.Fatalf("one session must contain both installers and tests: %+v", requests)
	}
	request := requests[0]
	if strings.Join(request.Recipe.Source.ExcludePatterns, ",") != "build-artifacts" || len(request.Snapshot.ChangedFiles) != 2 {
		t.Fatalf("shared upload scope changed: %+v %+v", request.Recipe.Source, request.Snapshot)
	}
	nodes := map[string]preflightapi.VerificationNodePlan{}
	for _, node := range request.Plan.Nodes {
		nodes[node.ID] = node
	}
	for _, component := range []string{"web", "server"} {
		warm := nodes["warm:"+component+"-dependencies"]
		check := nodes[component+"-test"]
		if warm.Phase != preflightapi.VerificationNodePhaseWarm || warm.Run.NetworkMode != "allowlist" || warm.Run.SecretsRequested == nil || *warm.Run.SecretsRequested || check.Run.NetworkMode != "none" || len(check.SuccessDependsOn) != 1 || check.SuccessDependsOn[0] != warm.ID {
			t.Fatalf("%s lost its component preparation contract: warm=%+v check=%+v", component, warm, check)
		}
	}
}

func TestSetupWarnsAboutMixedComponentSourcesWithoutChangingThem(t *testing.T) {
	chdirCLITestRepo(t, "mixed-source-guidance")
	writeFileForRepoMapTest(t, ".readyci.yml", componentDependencyCLIConfig)
	t.Setenv("READYCI_CONFIG_DIR", t.TempDir())
	t.Setenv("READYCI_API_TOKEN", "")
	report, err := buildSetupReport(setupOptions{APIURL: "http://127.0.0.1:1", ProjectID: "review", CheckName: "web-test"})
	if err != nil {
		t.Fatal(err)
	}
	if warning := strings.Join(report.Warnings, "\n"); !strings.Contains(warning, "shared top-level sync policy") || !strings.Contains(warning, "named check separately") {
		t.Fatalf("setup omitted actionable source guidance: %s", warning)
	}
	data, err := os.ReadFile(".readyci.yml")
	if err != nil || string(data) != componentDependencyCLIConfig {
		t.Fatal("setup silently rewrote component filters")
	}
}

func TestWarmDependencyProfileSelectsAffectedComponentInstallers(t *testing.T) {
	for _, tc := range []struct {
		name       string
		changed    []string
		installers []string
	}{
		{name: "web only", changed: []string{"apps/web/test.js"}, installers: []string{"web-dependencies"}},
		{name: "server only", changed: []string{"apps/server/main.rs"}, installers: []string{"dependencies"}},
		{name: "both", changed: []string{"apps/web/test.js", "apps/server/main.rs"}, installers: []string{"dependencies", "web-dependencies"}},
		{name: "no affected consumers"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chdirCLITestRepo(t, "warm-components")
			runGitForCLITest(t, "init")
			runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
			runGitForCLITest(t, "config", "user.name", "Ready CI")
			writeFileForRepoMapTest(t, ".readyci.yml", `project_id: review
network: {mode: none, warm_mode: allowlist, warm_allowlist: [registry.npmjs.org, crates.io]}
checks:
  dependencies:
    command: cargo fetch --locked
  web-dependencies:
    command: pnpm install --frozen-lockfile
  web-test:
    command: pnpm test
    dependency_check: web-dependencies
  rust-test:
    command: cargo test --offline
affected:
  rules:
    - paths: [apps/web/**]
      checks: [web-test]
    - paths: [apps/server/**]
      checks: [rust-test]
`)
			runGitForCLITest(t, "add", ".readyci.yml")
			runGitForCLITest(t, "commit", "-m", "fixture")
			for _, path := range tc.changed {
				writeFileForRepoMapTest(t, path, "changed\n")
			}
			output, err := captureStdoutForCLITest(t, func() error {
				return warmCommand([]string{"--profile", "dependencies", "--dry-run"})
			})
			if len(tc.installers) == 0 {
				if err == nil || !strings.Contains(err.Error(), "no affected consumer checks") {
					t.Fatalf("empty profile must fail clearly: output=%s err=%v", output, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("component warm preview: %v\n%s", err, output)
			}
			if strings.Count(output, "check: dry-run") != len(tc.installers) {
				t.Fatalf("profile included an unrelated installer or confirmation: %s", output)
			}
			var requests []preflightapi.CreateVerificationSessionRequest
			server := captureExecutionRequests(t, &requests)
			if err := warmCommand([]string{"--api-url", server.URL, "--profile", "dependencies"}); err == nil {
				t.Fatal("fixture should stop after capturing creation")
			}
			if len(requests) != 1 || len(requests[0].Plan.Nodes) != len(tc.installers) {
				t.Fatalf("wrong component warm session: %+v", requests)
			}
			for i, name := range tc.installers {
				node := requests[0].Plan.Nodes[i]
				if node.Run.CommandAlias != name || node.Phase != preflightapi.VerificationNodePhaseWarm {
					t.Fatalf("expected installer %s, got %+v", name, node)
				}
			}
			if tc.name == "web only" {
				err := warmCommand([]string{"--check", "dependencies", "--dry-run"})
				if err == nil || !strings.Contains(err.Error(), "no affected consumer checks") {
					t.Fatalf("explicit unrelated installer must still fail: %v", err)
				}
			}
		})
	}
}
