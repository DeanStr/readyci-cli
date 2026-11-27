package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestProjectSecretsAndCacheCommandsExerciseHTTPStories(t *testing.T) {
	fixed := time.Date(2026, 6, 22, 1, 2, 3, 0, time.UTC)
	var projectReq preflightapi.UpsertProjectSettingsRequest
	var secretReq preflightapi.UpsertProjectSecretRequest
	snapshot := "sha256:" + strings.Repeat("b", 64)
	trustedCommandHash := preflightapi.TrustedSecretCommandApproval(preflightapi.TrustedSecretCommandHash("printf ok"), snapshot)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer pfl_story" {
			t.Fatalf("authorization = %q, want bearer token", got)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/projects/proj_story/settings":
			writeCLITestJSON(w, http.StatusOK, preflightapi.ProjectSettings{
				ProjectID:           "proj_story",
				HomeRegion:          "us-east-1",
				AvailableRegions:    []string{"default", "us-east-1"},
				GitHubRepository:    "readyci/story",
				GitHubDefaultBranch: "main",
				TrustedSecretCommandHashes: []string{
					trustedCommandHash,
				},
				Secrets: []preflightapi.ProjectSecretSummary{{Name: "READYCI_TOKEN", CreatedAt: fixed, UpdatedAt: fixed}},
			})
		case r.Method == http.MethodPut && r.URL.Path == "/v1/projects/proj_story/settings":
			projectReq = preflightapi.UpsertProjectSettingsRequest{}
			if err := json.NewDecoder(r.Body).Decode(&projectReq); err != nil {
				t.Fatalf("decode project request: %v", err)
			}
			writeCLITestJSON(w, http.StatusOK, preflightapi.ProjectSettings{
				ProjectID:                  "proj_story",
				HomeRegion:                 stringPtrDeref(projectReq.HomeRegion),
				AvailableRegions:           []string{"default", "us-east-1"},
				GitHubRepository:           stringPtrDeref(projectReq.GitHubRepository),
				GitHubDefaultBranch:        stringPtrDeref(projectReq.GitHubDefaultBranch),
				TrustedSecretCommandHashes: projectReq.AddTrustedSecretCommandHashes,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/projects/proj_story/secrets":
			writeCLITestJSON(w, http.StatusOK, preflightapi.ListProjectSecretsResponse{
				ProjectID: "proj_story",
				Secrets: []preflightapi.ProjectSecretSummary{
					{Name: "READYCI_TOKEN", RequiresApproval: true, CreatedAt: fixed, UpdatedAt: fixed},
					{Name: "NPM_TOKEN", CreatedAt: fixed, UpdatedAt: fixed},
				},
			})
		case r.Method == http.MethodPut && r.URL.Path == "/v1/projects/proj_story/secrets/READYCI_TOKEN":
			secretReq = preflightapi.UpsertProjectSecretRequest{}
			if err := json.NewDecoder(r.Body).Decode(&secretReq); err != nil {
				t.Fatalf("decode secret request: %v", err)
			}
			writeCLITestJSON(w, http.StatusOK, preflightapi.UpsertProjectSecretResponse{
				Secret: preflightapi.ProjectSecretSummary{Name: "READYCI_TOKEN", RequiresApproval: secretReq.RequiresApproval == nil || *secretReq.RequiresApproval, CreatedAt: fixed, UpdatedAt: fixed},
			})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/projects/proj_story/secrets/READYCI_TOKEN":
			writeCLITestJSON(w, http.StatusOK, preflightapi.DeleteProjectSecretResponse{ProjectID: "proj_story", Name: "READYCI_TOKEN", Deleted: true})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/projects/proj_story/cache":
			writeCLITestJSON(w, http.StatusOK, preflightapi.CacheUsageResponse{
				ProjectID: "proj_story",
				Lanes: []preflightapi.CacheLaneInfo{{
					ID:         "cache_lane_story",
					WorkerID:   "worker_story",
					LaneIndex:  2,
					Status:     "ready",
					QuotaBytes: 1024,
				}},
				TotalQuotaBytes: 1024,
			})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/projects/proj_story/cache":
			writeCLITestJSON(w, http.StatusOK, preflightapi.PurgeCacheResponse{ProjectID: "proj_story", PurgedLanes: 1})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	baseArgs := []string{"--api-url", server.URL, "--token", "pfl_story", "--project", "proj_story"}
	requireCLITestCommandOutputContains(t, "project get", func() error { return projectGetCommand(baseArgs) }, "project_id: proj_story", "home_region: us-east-1", "available_regions: default, us-east-1", "github_repository: readyci/story", "trusted_secret_command_hashes: 1", "trusted_secret_command_hash: "+trustedCommandHash, "secrets: 1")

	requireCLITestCommandOutputContains(t, "project set", func() error {
		return projectSetCommand(append(baseArgs, "--home-region", "us-east-1", "--github-repo", "readyci/story-updated", "--default-branch", "trunk", "--trusted-secret-command", "printf ok", "--trusted-secret-snapshot", snapshot))
	}, "updated project proj_story", "home_region: us-east-1", "available_regions: default, us-east-1", "github_repository: readyci/story-updated", "github_default_branch: trunk", "trusted_secret_command_hashes: 1", "trusted_secret_command_hash: "+trustedCommandHash)
	if projectReq.GitHubRepository == nil || *projectReq.GitHubRepository != "readyci/story-updated" ||
		projectReq.GitHubDefaultBranch == nil || *projectReq.GitHubDefaultBranch != "trunk" ||
		projectReq.HomeRegion == nil || *projectReq.HomeRegion != "us-east-1" {
		t.Fatalf("project request = %+v, want repo, branch, and home region", projectReq)
	}
	if len(projectReq.AddTrustedSecretCommandHashes) != 1 || projectReq.AddTrustedSecretCommandHashes[0] != trustedCommandHash {
		t.Fatalf("project trusted hashes = %+v, want %s", projectReq.AddTrustedSecretCommandHashes, trustedCommandHash)
	}

	secondApproval := preflightapi.TrustedSecretCommandApproval(preflightapi.TrustedSecretCommandHash("printf ok"), "sha256:"+strings.Repeat("c", 64))
	requireCLITestCommandOutputContains(t, "project set multiple snapshots", func() error {
		return projectSetCommand(append(baseArgs, "--trusted-secret-command-hash", trustedCommandHash, "--trusted-secret-command-hash", secondApproval))
	}, "trusted_secret_command_hashes: 2")
	if !slices.Equal(projectReq.AddTrustedSecretCommandHashes, []string{trustedCommandHash, secondApproval}) || projectReq.ClearTrustedSecretCommandHashes {
		t.Fatalf("multiple approvals = %+v", projectReq)
	}
	requireCLITestCommandOutputContains(t, "project set revoke approval", func() error {
		return projectSetCommand(append(baseArgs, "--remove-trusted-secret-command-hash", trustedCommandHash))
	}, "updated project proj_story")
	if !slices.Equal(projectReq.RemoveTrustedSecretCommandHashes, []string{trustedCommandHash}) || projectReq.ClearTrustedSecretCommandHashes || projectReq.AddTrustedSecretCommandHashes != nil {
		t.Fatalf("targeted revocation = %+v", projectReq)
	}
	if err := projectSetCommand(append(baseArgs, "--remove-trusted-secret-command-hash", preflightapi.TrustedSecretCommandHash("printf ok"))); err == nil {
		t.Fatal("revocation accepted a bare command hash")
	}
	// Each approval has one spelling: a command with its snapshot, or a pair.
	projectReq = preflightapi.UpsertProjectSettingsRequest{}
	for name, args := range map[string][]string{
		"bare hash":              {"--trusted-secret-command-hash", preflightapi.TrustedSecretCommandHash("printf ok")},
		"bare hash and snapshot": {"--trusted-secret-command-hash", preflightapi.TrustedSecretCommandHash("printf ok"), "--trusted-secret-snapshot", snapshot},
		"pair and snapshot":      {"--trusted-secret-command-hash", secondApproval, "--trusted-secret-snapshot", snapshot},
		"command alone":          {"--trusted-secret-command", "printf ok"},
		"snapshot alone":         {"--trusted-secret-snapshot", snapshot},
	} {
		if err := projectSetCommand(append(baseArgs, args...)); err == nil {
			t.Fatalf("%s: approval accepted", name)
		}
	}
	if projectReq.AddTrustedSecretCommandHashes != nil {
		t.Fatalf("rejected approval reached the service: %+v", projectReq)
	}
	requireCLITestCommandOutputContains(t, "project set clear trust hashes", func() error {
		return projectSetCommand(append(baseArgs, "--clear-trusted-secret-command-hashes"))
	}, "updated project proj_story", "trusted_secret_command_hashes: 0")
	if !projectReq.ClearTrustedSecretCommandHashes || projectReq.AddTrustedSecretCommandHashes != nil || projectReq.RemoveTrustedSecretCommandHashes != nil {
		t.Fatalf("clear trusted hashes request = %+v, want only the clear operation", projectReq)
	}

	requireCLITestCommandOutputContains(t, "secrets list", func() error { return secretsListCommand(baseArgs) }, "secrets for project proj_story", "READYCI_TOKEN                    requires_approval=true", "NPM_TOKEN                        requires_approval=false")

	t.Setenv("READYCI_STORY_SECRET", "from-env-secret")
	requireCLITestCommandOutputContains(t, "secrets set", func() error {
		return secretsSetCommand(append(baseArgs, "--from-env", "READYCI_STORY_SECRET", "READYCI_TOKEN"))
	}, "saved secret READYCI_TOKEN for project proj_story; requires_approval=true")
	if secretReq.Value == nil || *secretReq.Value != "from-env-secret" || secretReq.RequiresApproval != nil {
		t.Fatalf("secret request = %+v, want the env value and the stored approval rule", secretReq)
	}
	requireCLITestCommandOutputContains(t, "secrets set approval only", func() error {
		return secretsSetCommand(append(baseArgs, "--requires-approval=false", "READYCI_TOKEN"))
	}, "saved secret READYCI_TOKEN for project proj_story; requires_approval=false")
	if secretReq.Value != nil || secretReq.RequiresApproval == nil || *secretReq.RequiresApproval {
		t.Fatalf("approval-only request = %+v, want no value and requires_approval false", secretReq)
	}
	t.Setenv("READYCI_STORY_EMPTY", "")
	if err := secretsSetCommand(append(baseArgs, "--requires-approval=false", "--from-env", "READYCI_STORY_EMPTY", "READYCI_TOKEN")); err == nil || !strings.Contains(err.Error(), "secret value is required") {
		t.Fatalf("empty env with approval flag error = %v, want required value", err)
	}
	secretReq = preflightapi.UpsertProjectSecretRequest{}
	if err := secretsSetCommand(append(baseArgs, "READYCI_TOKEN", "--requires-approval=false")); err == nil || !strings.Contains(err.Error(), "flags must come before NAME") || secretReq.Value != nil {
		t.Fatalf("flag after NAME = %v (request %+v), want rejection before any request", err, secretReq)
	}
	if err := secretsSetCommand(append(baseArgs, "--requires-approval=false", "--value", "", "READYCI_TOKEN")); err == nil || !strings.Contains(err.Error(), "secret value is required") {
		t.Fatalf("empty --value with approval flag error = %v, want required value", err)
	}
	if err := secretsSetCommand(append(baseArgs, "READYCI_TOKEN")); err == nil || !strings.Contains(err.Error(), "secret value is required") {
		t.Fatalf("valueless secrets set error = %v, want required value", err)
	}
	if err := secretsDeleteCommand(append(baseArgs, "READYCI_TOKEN")); err == nil || !strings.Contains(err.Error(), "--reason is required") {
		t.Fatalf("reasonless secrets delete error = %v, want required reason", err)
	}

	requireCLITestCommandOutputContains(t, "secrets delete", func() error {
		return secretsDeleteCommand(append(baseArgs, "--reason", "story cleanup", "READYCI_TOKEN"))
	}, "deleted secret READYCI_TOKEN from project proj_story")

	requireCLITestCommandOutputContains(t, "cache ls", func() error { return cacheListCommand(baseArgs) }, "cache lanes for project proj_story", "cache_lane_story", "worker=worker_story", "quota_bytes=1024")

	requireCLITestCommandOutputContains(t, "cache purge", func() error { return cachePurgeCommand(baseArgs) }, "purged 1 cache lane(s) for project proj_story")
}
