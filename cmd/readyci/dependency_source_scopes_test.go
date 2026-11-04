package main

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/internal/syncbundlebuild"
	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

const reducedDependencyConfig = `project_id: review
network: {mode: none, warm_mode: allowlist, warm_allowlist: [github.com]}
checks:
  dependencies:
    command: cargo fetch --locked
    sync: {exclude: [assets]}
  rust-test:
    command: cargo test --offline
affected:
  rules:
    - paths: [src/**, assets/**]
      checks: [rust-test]
`

func TestDependencyAndConsumerExecuteDistinctViewsOfOneSnapshot(t *testing.T) {
	schema := diagnosticJSONSchema(t, "readyci.verification.v0alpha1.schema.json", "/$defs/verificationNodeSource")
	chdirCLITestRepo(t, "reduced-dependency-source")
	runGitForCLITest(t, "init")
	writeFileForRepoMapTest(t, ".readyci.yml", reducedDependencyConfig)
	writeFileForRepoMapTest(t, "Cargo.lock", "locked dependencies\n")
	writeFileForRepoMapTest(t, "src/main.rs", "source\n")
	writeFileForRepoMapTest(t, "assets/plane.png", "compile-time asset\n")
	writeFileForRepoMapTest(t, ".readyciignore", "private-data/\n")
	writeFileForRepoMapTest(t, "private-data/file.txt", "never upload\n")
	var requests []preflightapi.CreateVerificationSessionRequest
	server := captureExecutionRequests(t, &requests)
	if err := run([]string{"check", "--api-url", server.URL, "rust-test"}); err == nil {
		t.Fatal("fixture should stop after creation")
	}
	if len(requests) != 1 {
		t.Fatalf("expected one warm/test session: %+v", requests)
	}
	request, err := preflightapi.NormalizeCreateVerificationSessionRequest(requests[0])
	if err != nil {
		t.Fatal(err)
	}
	if request.Plan.Authority != preflightapi.VerificationPlanAuthorityClientProposed || len(request.Recipe.Source.SourceScopes) != 2 || len(request.Plan.Nodes) != 2 {
		t.Fatalf("wrong session: %+v", request)
	}
	warm, check := request.Plan.Nodes[0], request.Plan.Nodes[1]
	if warm.Source == nil || check.Source == nil || warm.Source.SnapshotDigest == check.Source.SnapshotDigest {
		t.Fatal("installer and compiler did not retain distinct sealed source views")
	}
	if slices.Contains(warm.Source.ChangedFiles, "assets/plane.png") || !slices.Contains(check.Source.ChangedFiles, "assets/plane.png") {
		t.Fatal("compile-time asset was omitted from tests or sent to preparation")
	}
	for _, node := range request.Plan.Nodes {
		if slices.Contains(node.Source.ChangedFiles, "private-data/file.txt") {
			t.Fatal("mixed source packaging bypassed .readyciignore")
		}
		encoded, err := json.Marshal(node.Source)
		if err != nil {
			t.Fatal(err)
		}
		var value any
		if err := json.Unmarshal(encoded, &value); err != nil {
			t.Fatal(err)
		}
		if err := schema.Validate(value); err != nil {
			t.Fatalf("sealed source does not satisfy the published schema: %v", err)
		}
	}
	if warm.Run.NetworkMode != "allowlist" || check.Run.NetworkMode != "none" || len(check.SuccessDependsOn) != 1 || check.SuccessDependsOn[0] != warm.ID {
		t.Fatal("source projection lost successful offline preparation binding")
	}
	// Reruns must reconstruct both views with current configuration and bytes.
	prepared, err := prepareExecutionRerun(preflightapi.VerificationSession{ProjectID: request.ProjectID, Recipe: request.Recipe}, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	var bytesDiscard strings.Builder
	built, err := syncbundlebuild.BuildWithOptions(context.Background(), ".", &bytesDiscard, prepared.BuildOptions)
	if err != nil {
		t.Fatal(err)
	}
	nodes, _, err := verificationNodesForPlans(prepared.WarmPlans, prepared.Plans, built)
	if err != nil || len(nodes) != 2 || nodes[0].Source == nil || nodes[1].Source == nil || nodes[0].Source.SnapshotDigest != warm.Source.SnapshotDigest {
		t.Fatalf("rerun lost source recipe: %+v %v", nodes, err)
	}
}

func TestDependencyViewsCannotMixChangeModes(t *testing.T) {
	cfg, err := config.ParseProjectConfig([]byte(reducedDependencyConfig), "source modes")
	if err != nil {
		t.Fatal(err)
	}
	warm, err := resolveCheckRunPlan("dependencies", cfg, checkResolveOptions{Changes: "staged"})
	if err != nil {
		t.Fatal(err)
	}
	check, err := resolveCheckRunPlan("rust-test", cfg, checkResolveOptions{Changes: "working"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executionSessionBuildOptions([]checkRunPlan{warm, check}, syncbundlebuild.BuildOptions{}); err == nil || !strings.Contains(err.Error(), "changes mode") {
		t.Fatalf("mixed change selection accepted: %v", err)
	}
}

func TestProjectIgnoreCanMakeDependencyViewsIdentical(t *testing.T) {
	chdirCLITestRepo(t, "dependency-views-project-ignore")
	runGitForCLITest(t, "init")
	writeFileForRepoMapTest(t, ".readyciignore", "assets\n")
	cfg, err := config.ParseProjectConfig([]byte(reducedDependencyConfig), "source views")
	if err != nil {
		t.Fatal(err)
	}
	var plans []checkRunPlan
	for _, name := range []string{"dependencies", "rust-test"} {
		plan, err := resolveCheckRunPlan(name, cfg, checkResolveOptions{})
		if err != nil {
			t.Fatal(err)
		}
		plans = append(plans, plan)
	}
	options, err := executionSessionBuildOptions(plans, syncbundlebuild.BuildOptions{})
	if err != nil || len(options.SourceScopes) != 0 || !slices.Contains(options.ExcludePatterns, "assets") || plans[0].SourcePolicy != nil || plans[1].SourcePolicy != nil {
		t.Fatalf("equivalent effective views were rejected or widened: %+v %v", options, err)
	}
}
