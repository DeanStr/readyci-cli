package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/verificationplan"
)

// Approving a check by name must produce the pair the control plane computes
// for the planned run, including the cache wrapper, for every matrix variant.
func TestProjectSetApprovesANamedCheckAsItIsPlanned(t *testing.T) {
	const configYAML = "version: 1\nproject_id: proj_story\ncache:\n  mode: cache-only\n  paths: [~/.cache/go-build]\nnetwork:\n  mode: none\nchecks:\n  integration:\n    command: go test ./...\n    matrix:\n      GOFLAGS: [-race, -count=1]\n"
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, ".readyci.yml"), []byte(configYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(workspace)
	snapshot := "sha256:" + strings.Repeat("d", 64)

	cfg, err := config.ParseProjectConfig([]byte(configYAML), "proj_story")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := verificationplan.ResolveCheck("integration", cfg, verificationplan.ResolveOptions{ProjectID: "proj_story"})
	if err != nil {
		t.Fatal(err)
	}
	secretsRequested := true
	plan.Run.SecretsRequested = &secretsRequested
	nodes, _, err := verificationplan.NodesForPlans(nil, []verificationplan.CheckPlan{plan}, verificationplan.BundleIdentity{}, verificationplan.ClientIdentity{})
	if err != nil || len(nodes) != 2 {
		t.Fatalf("planned nodes = %d, %v; want one per matrix variant", len(nodes), err)
	}
	var want []string
	for _, node := range nodes {
		hash, err := verificationplan.SecretApprovalCommandHash(node.Run)
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, preflightapi.TrustedSecretCommandApproval(hash, snapshot))
	}
	if want[0] == want[1] || slices.Contains(want, preflightapi.TrustedSecretCommandApproval(preflightapi.TrustedSecretCommandHash("go test ./..."), snapshot)) {
		t.Fatalf("variant approvals %v must differ from each other and from the command text", want)
	}

	var projectReq preflightapi.UpsertProjectSettingsRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/v1/projects/proj_story/settings" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&projectReq); err != nil {
			t.Fatalf("decode project request: %v", err)
		}
		writeCLITestJSON(w, http.StatusOK, preflightapi.ProjectSettings{ProjectID: "proj_story", TrustedSecretCommandHashes: projectReq.AddTrustedSecretCommandHashes})
	}))
	defer server.Close()
	baseArgs := []string{"--api-url", server.URL, "--token", "pfl_story", "--project", "proj_story"}

	requireCLITestCommandOutputContains(t, "project set approve check", func() error {
		return projectSetCommand(append(baseArgs, "--trusted-secret-check", "integration", "--trusted-secret-snapshot", snapshot))
	}, "trusted_secret_command_hashes: 2")
	if !slices.Equal(projectReq.AddTrustedSecretCommandHashes, want) {
		t.Fatalf("approvals = %v, want %v", projectReq.AddTrustedSecretCommandHashes, want)
	}

	projectReq = preflightapi.UpsertProjectSettingsRequest{}
	for name, args := range map[string][]string{
		"unknown check":    {"--trusted-secret-check", "missing", "--trusted-secret-snapshot", snapshot},
		"check alone":      {"--trusted-secret-check", "integration"},
		"malformed digest": {"--trusted-secret-check", "integration", "--trusted-secret-snapshot", "sha256:short"},
	} {
		if err := projectSetCommand(append(baseArgs, args...)); err == nil {
			t.Fatalf("%s: approval accepted", name)
		}
	}
	// A matrix that excludes every variant plans nothing; approving it must not
	// report success.
	emptyMatrix := t.TempDir()
	if err := os.WriteFile(filepath.Join(emptyMatrix, ".readyci.yml"), []byte("version: 1\nproject_id: proj_story\nchecks:\n  integration:\n    command: go test ./...\n    matrix:\n      axes:\n        GOFLAGS: [-race]\n      exclude:\n        - GOFLAGS: -race\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(emptyMatrix)
	if err := projectSetCommand(append(baseArgs, "--trusted-secret-check", "integration", "--trusted-secret-snapshot", snapshot)); err == nil || !strings.Contains(err.Error(), "nothing to approve") {
		t.Fatalf("approval of a check with no variants = %v, want nothing to approve", err)
	}
	t.Chdir(t.TempDir())
	if err := projectSetCommand(append(baseArgs, "--trusted-secret-check", "integration", "--trusted-secret-snapshot", snapshot)); err == nil || !strings.Contains(err.Error(), ".readyci.yml") {
		t.Fatalf("approval without .readyci.yml = %v, want a config error", err)
	}
	if projectReq.AddTrustedSecretCommandHashes != nil {
		t.Fatalf("rejected approval reached the service: %+v", projectReq)
	}
}
