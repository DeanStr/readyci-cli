package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestAdminCommandStoriesExerciseTokenTeamWorkerAndStorageControls(t *testing.T) {
	fixed := time.Date(2026, 6, 22, 3, 4, 5, 0, time.UTC)
	revokedAt := fixed.Add(time.Minute)
	exitCode := 0
	var createdToken preflightapi.CreateAPITokenRequest
	var cancelRunReq preflightapi.CancelRunRequest
	var disableTeamReq preflightapi.AdminDisableTeamRequest
	var cancelTeamReq preflightapi.AdminCancelTeamRunsRequest
	var drainReq preflightapi.AdminDrainWorkerRequest
	var deleteWorkerReq preflightapi.AdminDeleteWorkerRequest
	var cleanupReq preflightapi.AdminCleanupExpiredArtifactsRequest
	var cleanupCancelReq preflightapi.AdminCancelCleanupExpiredArtifactsRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer admin_story" {
			t.Fatalf("authorization = %q, want admin bearer token", got)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/runs":
			if r.URL.Query().Get("project") != "proj_story" || r.URL.Query().Get("status") != "succeeded" || r.URL.Query().Get("limit") != "2" {
				t.Fatalf("admin runs query = %s, want project/status/limit", r.URL.RawQuery)
			}
			writeCLITestJSON(w, http.StatusOK, preflightapi.ListRunsResponse{Runs: []preflightapi.RunSummary{{
				RunID:      "run_story",
				ProjectID:  "proj_story",
				Command:    "go test ./...",
				Status:     preflightapi.StatusSucceeded,
				RunnerSize: preflightapi.DefaultRunnerSize,
				CacheMode:  "cache-only",
				ExitCode:   &exitCode,
				CreatedAt:  fixed,
				EventCount: 4,
			}}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/runs/run_story":
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunDetail{
				Summary: preflightapi.RunSummary{
					RunID:      "run_story",
					ProjectID:  "proj_story",
					Command:    "go test ./...",
					Status:     preflightapi.StatusSucceeded,
					RunnerSize: preflightapi.DefaultRunnerSize,
					CacheMode:  "cache-only",
					ExitCode:   &exitCode,
					CreatedAt:  fixed,
					EventCount: 4,
				},
				Spec: preflightapi.RunSpec{RunID: "run_story", ProjectID: "proj_story", Command: "go test ./...", RunnerSize: preflightapi.DefaultRunnerSize},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/runs/run_story/events":
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunEventsResponse{
				RunID: "run_story",
				Events: []preflightapi.RunEvent{{
					Type:      preflightapi.EventStdout,
					RunID:     "run_story",
					Timestamp: fixed,
					Data:      "admin log line\n",
				}},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/admin/runs/run_story/cancel":
			if err := json.NewDecoder(r.Body).Decode(&cancelRunReq); err != nil {
				t.Fatalf("decode cancel run request: %v", err)
			}
			writeCLITestJSON(w, http.StatusOK, preflightapi.CancelRunResponse{RunID: "run_story", Status: preflightapi.StatusCancelled})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/tokens":
			writeCLITestJSON(w, http.StatusOK, preflightapi.ListAPITokensResponse{Tokens: []preflightapi.APITokenSummary{{
				ID:        "token_story",
				UserID:    "user_story",
				Name:      "Story token",
				Scopes:    []string{"runs:create", "runs:read"},
				CreatedAt: fixed,
			}}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/admin/tokens":
			if err := json.NewDecoder(r.Body).Decode(&createdToken); err != nil {
				t.Fatalf("decode token create request: %v", err)
			}
			writeCLITestJSON(w, http.StatusOK, preflightapi.CreateAPITokenResponse{
				Token:  preflightapi.APITokenSummary{ID: "token_created", UserID: "user_story", Name: createdToken.Name, Scopes: createdToken.Scopes, CreatedAt: fixed},
				Secret: "pfl_story_secret",
			})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/admin/tokens/token_story":
			writeCLITestJSON(w, http.StatusOK, preflightapi.RevokeAPITokenResponse{
				Token: preflightapi.APITokenSummary{ID: "token_story", UserID: "user_story", Name: "Story token", Scopes: []string{"runs:read"}, RevokedAt: &revokedAt, CreatedAt: fixed},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/teams":
			writeCLITestJSON(w, http.StatusOK, preflightapi.AdminListTeamsResponse{Teams: []preflightapi.AdminTeamSummary{{
				ID:              "team_story",
				Name:            "Story Team",
				Plan:            "dev",
				QueuedRuns:      2,
				ActiveRuns:      1,
				ActiveAPITokens: 3,
				CreatedAt:       fixed,
				UpdatedAt:       fixed,
			}}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/admin/teams/team_story/disable":
			if err := json.NewDecoder(r.Body).Decode(&disableTeamReq); err != nil {
				t.Fatalf("decode disable team request: %v", err)
			}
			writeCLITestJSON(w, http.StatusOK, preflightapi.AdminDisableTeamResponse{
				Team:          preflightapi.AdminTeamSummary{ID: "team_story", Name: "Story Team", Plan: "dev", DisabledAt: &fixed, CreatedAt: fixed, UpdatedAt: fixed},
				CancelledRuns: []string{"run_cancelled_1"},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/admin/teams/team_story/cancel-runs":
			if err := json.NewDecoder(r.Body).Decode(&cancelTeamReq); err != nil {
				t.Fatalf("decode cancel team request: %v", err)
			}
			writeCLITestJSON(w, http.StatusOK, preflightapi.AdminCancelTeamRunsResponse{TeamID: "team_story", CancelledRuns: []string{"run_cancelled_1", "run_cancelled_2"}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/workers":
			writeCLITestJSON(w, http.StatusOK, preflightapi.AdminListWorkersResponse{Workers: []preflightapi.AdminWorkerSummary{{
				ID:                   "worker_story",
				Runtime:              "fake",
				SupportedRunnerSizes: []string{"standard"},
				SupportedImages:      []string{preflightapi.DefaultImage},
				NetworkModes:         []string{"none"},
				MaxActiveRuns:        2,
				BundleUploadBaseURL:  "http://100.64.0.10:19080",
				LastSeenAt:           &fixed,
				CreatedAt:            fixed,
				UpdatedAt:            fixed,
			}}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/admin/workers/worker_story/drain":
			if err := json.NewDecoder(r.Body).Decode(&drainReq); err != nil {
				t.Fatalf("decode drain request: %v", err)
			}
			writeCLITestJSON(w, http.StatusOK, preflightapi.AdminDrainWorkerResponse{
				Worker: preflightapi.AdminWorkerSummary{ID: "worker_story", Runtime: "fake", Draining: true, DrainReason: drainReq.Reason, MaxActiveRuns: 2, CreatedAt: fixed, UpdatedAt: fixed},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/admin/workers/worker_story/undrain":
			writeCLITestJSON(w, http.StatusOK, preflightapi.AdminDrainWorkerResponse{
				Worker: preflightapi.AdminWorkerSummary{ID: "worker_story", Runtime: "fake", Draining: false, MaxActiveRuns: 2, CreatedAt: fixed, UpdatedAt: fixed},
			})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/admin/workers/worker_story":
			if err := json.NewDecoder(r.Body).Decode(&deleteWorkerReq); err != nil {
				t.Fatalf("decode delete worker request: %v", err)
			}
			writeCLITestJSON(w, http.StatusOK, preflightapi.AdminDeleteWorkerResponse{WorkerID: "worker_story", Deleted: true, ClearedRunReferences: 1, DeletedCacheLanes: 2})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/queue/status":
			writeCLITestJSON(w, http.StatusOK, preflightapi.AdminQueueStatusResponse{
				AwaitingUploadRuns:             1,
				StaleAwaitingUploadRuns:        1,
				OldestAwaitingUploadAgeSeconds: 901,
				QueuedRuns:                     2,
				ScheduledRuns:                  3,
				RunningRuns:                    4,
				StaleLeases:                    1,
				FreshWorkers:                   5,
				StaleWorkers:                   6,
				PlatformEvents:                 7,
				PlatformDiscards:               8,
				PlatformErrors:                 9,
				UpdatedAt:                      fixed,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/queue/explain/run_story":
			writeCLITestJSON(w, http.StatusOK, preflightapi.AdminQueueExplainResponse{
				RunID:      "run_story",
				ProjectID:  "proj_story",
				Status:     preflightapi.StatusQueued,
				QueueState: "blocked_by_worker_capability",
				QueuedAt:   &fixed,
				Reasons:    []string{"no fresh worker currently advertises all required run capabilities"},
				WorkerCapability: &preflightapi.WorkerCapabilityCheckResponse{
					Claimable:           false,
					UploadCapable:       false,
					FreshWorkerCount:    0,
					MatchingWorkerCount: 0,
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/storage/audit":
			writeCLITestJSON(w, http.StatusOK, preflightapi.AdminStorageAuditResponse{
				UploadStoreRoot:          "/tmp/uploads",
				ArtifactStoreRoot:        "/tmp/artifacts",
				AwaitingUploadRuns:       1,
				QueuedRuns:               2,
				ArtifactRecords:          3,
				ExpiredArtifactRecords:   1,
				MalformedArtifactRecords: 1,
				TerminalRunsWithBundles:  4,
				RequeuedArtifactRecords:  5,
				QueuedWorkerLocalBundles: 1,
				PendingCacheLaneDeletes:  6,
				WorkersNotSeenRecently:   7,
				Notes:                    []string{"story audit note"},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/admin/storage/cleanup-expired-artifacts":
			if err := json.NewDecoder(r.Body).Decode(&cleanupReq); err != nil {
				t.Fatalf("decode cleanup request: %v", err)
			}
			writeCLITestJSON(w, http.StatusOK, preflightapi.AdminCleanupExpiredArtifactsResponse{
				DryRun:                    cleanupReq.DryRun,
				ExpiredArtifacts:          1,
				MalformedArtifactRecords:  1,
				ExpiredUploads:            1,
				ExpiredLogEvents:          2,
				DeletedArtifacts:          0,
				DeletedMalformedArtifacts: 0,
				DeletedObjects:            0,
				DeletedUploads:            0,
				DeletedLogEvents:          0,
				Artifacts: []preflightapi.ArtifactSummary{{
					ID:        "artifact_expired",
					RunID:     "run_story",
					Name:      "coverage",
					Path:      "coverage/lcov.info",
					SizeBytes: 42,
					ExpiresAt: fixed,
				}},
				Uploads: []preflightapi.UploadObjectSummary{{
					RunID:     "run_upload_story",
					ObjectKey: "memory://uploads/run_upload_story",
					SizeBytes: 99,
					CreatedAt: fixed.Add(-72 * time.Hour),
					ExpiresAt: fixed,
				}},
				Job: &preflightapi.MaintenanceJobSummary{
					ID:              "maint_story",
					Type:            preflightapi.MaintenanceJobTypeExpiredArtifacts,
					Status:          preflightapi.MaintenanceJobStatusSucceeded,
					DryRun:          cleanupReq.DryRun,
					StartedAt:       fixed.Add(-time.Minute),
					FinishedAt:      &fixed,
					ProgressCurrent: 5,
					ProgressTotal:   5,
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/storage/cleanup-expired-artifacts/status":
			writeCLITestJSON(w, http.StatusOK, preflightapi.AdminCleanupExpiredArtifactsStatusResponse{
				Job: &preflightapi.MaintenanceJobSummary{
					ID:               "maint_story",
					Type:             preflightapi.MaintenanceJobTypeExpiredArtifacts,
					Status:           preflightapi.MaintenanceJobStatusSucceeded,
					DryRun:           true,
					StartedAt:        fixed.Add(-time.Minute),
					FinishedAt:       &fixed,
					ProgressCurrent:  5,
					ProgressTotal:    5,
					DeletedArtifacts: 0,
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/admin/storage/cleanup-expired-artifacts/cancel":
			if err := json.NewDecoder(r.Body).Decode(&cleanupCancelReq); err != nil {
				t.Fatalf("decode cleanup cancel request: %v", err)
			}
			writeCLITestJSON(w, http.StatusOK, preflightapi.AdminCancelCleanupExpiredArtifactsResponse{
				Job: &preflightapi.MaintenanceJobSummary{
					ID:              "maint_story",
					Type:            preflightapi.MaintenanceJobTypeExpiredArtifacts,
					Status:          preflightapi.MaintenanceJobStatusRunning,
					DryRun:          false,
					StartedAt:       fixed.Add(-time.Minute),
					ProgressCurrent: 2,
					ProgressTotal:   5,
					Metadata: map[string]string{
						"cancel_requested": "true",
						"cancel_reason":    "story cleanup cancel",
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adminArgs := []string{"--api-url", server.URL, "--token", "admin_story"}
	requireCLITestCommandOutputContains(t, "admin runs", func() error {
		return adminRunsCommand(append(adminArgs, "--project", "proj_story", "--status", "succeeded", "--limit", "2"))
	}, "run_story", "proj_story", "succeeded")

	requireCLITestCommandOutputContains(t, "admin inspect", func() error { return adminInspectCommand(append(adminArgs, "run_story")) }, "run_id: run_story", "project_id: proj_story", "command: go test ./...")

	requireCLITestCommandOutputContains(t, "admin events", func() error { return adminEventsCommand(append(adminArgs, "run_story")) }, `"type":"stdout"`, `"run_id":"run_story"`)

	requireCLITestCommandOutputContains(t, "admin logs", func() error { return adminLogsCommand(append(adminArgs, "run_story")) }, "admin log line")

	requireCLITestCommandOutputContains(t, "admin cancel", func() error {
		return adminCancelCommand(append(adminArgs, "--reason", "story cancel", "run_story"))
	}, "run_story status=cancelled")
	if cancelRunReq.Reason != "story cancel" {
		t.Fatalf("cancel reason = %q, want story cancel", cancelRunReq.Reason)
	}

	requireCLITestCommandOutputContains(t, "admin tokens list", func() error { return adminTokensListCommand(adminArgs) }, "token_story", "user_story", "runs:create,runs:read")

	requireCLITestCommandOutputContains(t, "admin tokens create", func() error {
		return adminTokensCreateCommand(append(adminArgs, "--name", "Story CLI", "--team", "team_story", "--email", "user@example.test", "--role", "admin", "--scopes", "runs:create,runs:read"))
	}, "token_id: token_created", "secret: pfl_story_secret", "scopes: runs:create,runs:read")
	if createdToken.Name != "Story CLI" || createdToken.TeamID != "team_story" || createdToken.UserEmail != "user@example.test" || createdToken.Role != "admin" {
		t.Fatalf("created token request = %+v", createdToken)
	}

	requireCLITestCommandOutputContains(t, "admin tokens revoke", func() error { return adminTokensRevokeCommand(append(adminArgs, "token_story")) }, "token_story revoked_at=2026-06-22T03:05:05Z")

	requireCLITestCommandOutputContains(t, "admin teams list", func() error { return adminTeamsListCommand(adminArgs) }, "team_story", "Story Team", "active")

	requireCLITestCommandOutputContains(t, "admin teams disable", func() error {
		return adminTeamsDisableCommand(append(adminArgs, "--reason", "incident", "team_story"))
	}, "team_story disabled_at=2026-06-22T03:04:05Z cancelled_runs=1")
	if disableTeamReq.Reason != "incident" {
		t.Fatalf("disable reason = %q, want incident", disableTeamReq.Reason)
	}

	requireCLITestCommandOutputContains(t, "admin teams cancel-runs", func() error {
		return adminTeamsCancelRunsCommand(append(adminArgs, "--reason", "maintenance", "team_story"))
	}, "team_story cancelled_runs=2", "run_cancelled_1", "run_cancelled_2")
	if cancelTeamReq.Reason != "maintenance" {
		t.Fatalf("cancel team reason = %q, want maintenance", cancelTeamReq.Reason)
	}

	requireCLITestCommandOutputContains(t, "admin workers list", func() error { return adminWorkersListCommand(adminArgs) }, "worker_story", "fake", "true", "none")

	requireCLITestCommandOutputContains(t, "admin workers drain", func() error {
		return adminWorkersDrainCommand(append(adminArgs, "--reason", "maintenance", "worker_story"))
	}, "worker_story draining=true reason=maintenance")
	if drainReq.Reason != "maintenance" {
		t.Fatalf("drain reason = %q, want maintenance", drainReq.Reason)
	}

	requireCLITestCommandOutputContains(t, "admin workers undrain", func() error { return adminWorkersUndrainCommand(append(adminArgs, "worker_story")) }, "worker_story draining=false")

	requireCLITestCommandOutputContains(t, "admin workers delete", func() error {
		return adminWorkersDeleteCommand(append(adminArgs, "--reason", "retired", "worker_story"))
	}, "worker_story deleted=true cleared_run_references=1 deleted_cache_lanes=2")
	if deleteWorkerReq.Reason != "retired" {
		t.Fatalf("delete worker reason = %q, want retired", deleteWorkerReq.Reason)
	}

	requireCLITestCommandOutputContains(t, "admin queue status", func() error { return adminQueueStatusCommand(adminArgs) }, "queued_runs: 2", "stale_awaiting_upload_runs: 1", "oldest_awaiting_upload_age_seconds: 901", "stale_leases: 1", "fresh_workers: 5", "platform_events: 7", "platform_event_discards: 8", "platform_event_errors: 9")

	requireCLITestCommandOutputContains(t, "admin queue explain", func() error {
		return adminQueueExplainCommand(append(adminArgs, "run_story"))
	}, "run_id: run_story", "queue_state: blocked_by_worker_capability", "reason: no fresh worker currently advertises all required run capabilities", "claimable: false")

	requireCLITestCommandOutputContains(t, "admin storage audit", func() error { return adminStorageAuditCommand(adminArgs) }, "upload_store: /tmp/uploads", "expired_artifact_records: 1", "malformed_artifact_records: 1", "queued_worker_local_bundles: 1", "note: story audit note")

	requireCLITestCommandOutputContains(t, "admin storage cleanup", func() error {
		return adminStorageCleanupExpiredArtifactsCommand(append(adminArgs, "--dry-run", "--limit", "4", "--min-age-days", "2", "--include-malformed-artifacts", "--include-stale-uploads", "--include-run-logs", "--log-min-age-days", "14"))
	}, "dry_run: true", "expired_artifacts: 1", "malformed_artifact_records: 1", "expired_uploads: 1", "expired_log_events: 2", "job_id: maint_story", "job_status: succeeded", "artifact: artifact_expired run=run_story path=coverage/lcov.info", "upload: run=run_upload_story object=memory://uploads/run_upload_story")
	if !cleanupReq.DryRun || cleanupReq.Limit != 4 || cleanupReq.MinAgeSeconds != 2*24*60*60 || !cleanupReq.IncludeMalformedArtifacts || !cleanupReq.IncludeStaleUploads || !cleanupReq.IncludeRunLogs || cleanupReq.LogMinAgeSeconds != 14*24*60*60 {
		t.Fatalf("cleanup request = %+v, want dry-run limit 4 min age 2 days with upload/log cleanup", cleanupReq)
	}

	requireCLITestCommandOutputContains(t, "admin storage cleanup status", func() error {
		return adminStorageCleanupStatusCommand(adminArgs)
	}, "job_id: maint_story", "job_type: expired_artifacts", "job_status: succeeded", "job_progress: 5/5")

	requireCLITestCommandOutputContains(t, "admin storage cleanup cancel", func() error {
		return adminStorageCleanupCancelCommand(append(adminArgs, "--reason", "story cleanup cancel"))
	}, "job_id: maint_story", "job_status: running", "job_progress: 2/5", "job_cancel_requested: true", "job_cancel_reason: story cleanup cancel")
	if cleanupCancelReq.Reason != "story cleanup cancel" {
		t.Fatalf("cleanup cancel reason = %q, want story cleanup cancel", cleanupCancelReq.Reason)
	}
}

func stringPtrDeref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
