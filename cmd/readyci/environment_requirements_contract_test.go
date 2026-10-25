package main

import (
	"encoding/json"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DeanStr/readyci-cli/internal/syncbundlebuild"
	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

func TestEnvironmentRequirementsReachRemoteRequestAndSealedPlan(t *testing.T) {
	cfg, err := config.ParseProjectConfig([]byte("version: 1\nenvironment_requirements: [network:ipv6-loopback]\nchecks:\n  unit:\n    command: cargo test\n    environment_requirements: [filesystem:btrfs]\n"), "requirements.yml")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := resolveCheckRunPlan("unit", cfg, checkResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{preflightapi.EnvironmentFilesystemBtrfs, preflightapi.EnvironmentIPv6Loopback}
	request := buildRemoteRunRequest(plan.Options, syncbundlebuild.BuildResult{}, "cargo test", "project", "requirements")
	if !reflect.DeepEqual(request.EnvironmentRequirements, want) {
		t.Fatalf("request lost guest requirements: %v", request.EnvironmentRequirements)
	}
	nodes, _, err := verificationNodesForPlans(nil, []checkRunPlan{plan}, syncbundlebuild.BuildResult{})
	if err != nil || len(nodes) != 1 {
		t.Fatalf("nodes=%v err=%v", nodes, err)
	}
	if !reflect.DeepEqual(nodes[0].Run.EnvironmentRequirements, want) {
		t.Fatalf("sealed node lost guest requirements: %v", nodes[0].Run.EnvironmentRequirements)
	}
	request.EnvironmentRequirements[0] = preflightapi.EnvironmentFilesystemMinix
	nodes[0].Run.EnvironmentRequirements[1] = preflightapi.EnvironmentIPv6Interface
	if !reflect.DeepEqual(plan.Options.EnvironmentRequirements, want) {
		t.Fatal("request or node mutations changed resolved requirements")
	}
}

func TestEnvironmentRequirementsValidateAgentSessionSchema(t *testing.T) {
	nodes, _, err := verificationNodesForPlans(nil, []checkRunPlan{{Name: "unit", Options: remoteRunOptions{Command: "cargo test", EnvironmentRequirements: []string{preflightapi.EnvironmentFilesystemBtrfs}}}}, syncbundlebuild.BuildResult{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	session := preflightapi.VerificationSession{
		SchemaVersion: preflightapi.VerificationSchemaVersion, SessionID: "group_environment", IdempotencyKey: "environment-schema", ProjectID: "proj_dev",
		State: preflightapi.VerificationStateSealed, Verdict: preflightapi.VerificationVerdictPending, Currentness: preflightapi.VerificationCurrentnessCurrent,
		Assurance: preflightapi.VerificationAssurance{Scope: preflightapi.VerificationAssuranceScopeConfiguredChecks, Execution: preflightapi.VerificationExecutionAssurancePending, Evaluator: preflightapi.VerificationEvaluatorAssuranceUnassessed},
		Recipe:    preflightapi.VerificationRecipe{SchemaVersion: preflightapi.VerificationRecipeSchemaVersion, Rerunnable: true, ChangeMode: "working", Source: &preflightapi.VerificationSourceRecipe{IncludeUntracked: true, UseProjectIgnore: true}},
		Snapshot:  preflightapi.VerificationSnapshot{Digest: "sha256:" + strings.Repeat("a", 64), ChangeMode: "working", ChangedFiles: []string{}},
		Coverage:  preflightapi.VerificationCoverage{MatchedFiles: []string{}, IgnoredFiles: []string{}, UncoveredFiles: []string{}},
		Plan:      preflightapi.VerificationPlan{Authority: preflightapi.VerificationPlanAuthorityClientProposed, Nodes: nodes},
		Nodes:     []preflightapi.VerificationNode{}, CreatedAt: now, UpdatedAt: now,
	}
	normalized, err := preflightapi.NormalizeCreateVerificationSessionRequest(preflightapi.CreateVerificationSessionRequest{
		ProjectID: session.ProjectID, IdempotencyKey: session.IdempotencyKey,
		Recipe: session.Recipe, Snapshot: session.Snapshot, Coverage: session.Coverage, Plan: session.Plan,
	})
	if err != nil {
		t.Fatal(err)
	}
	session.Recipe, session.Plan = normalized.Recipe, normalized.Plan
	encoded, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	validateAgentJSONSchema(t, encoded)
	session.Plan.Nodes[0].Run.EnvironmentRequirements = []string{"network:ipv6-egress"}
	encoded, err = json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	path, err := filepath.Abs(filepath.Join("..", "..", "schema", "readyci.verification.v0alpha1.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	schema, err := jsonschema.Compile((&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String())
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(decoded); err == nil {
		t.Fatal("agent schema accepted unsupported capability in a sealed execution request")
	}
}
