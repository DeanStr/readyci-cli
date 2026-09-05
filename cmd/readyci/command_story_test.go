package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestAdminStorageCleanupRejectsZeroLimit(t *testing.T) {
	err := adminStorageCleanupExpiredArtifactsCommand([]string{"--api-url", "http://127.0.0.1:1", "--token", "admin", "--limit", "0"})
	if err == nil || !strings.Contains(err.Error(), "--limit must be positive") {
		t.Fatalf("cleanup zero limit error = %v, want positive limit error", err)
	}
}

func TestAdminStorageCleanupCacheLanesRejectsZeroAges(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "duplicate min age",
			args: []string{"--api-url", "http://127.0.0.1:1", "--token", "admin", "--min-age-days", "0"},
			want: "--min-age-days must be positive",
		},
		{
			name: "non-home min age",
			args: []string{"--api-url", "http://127.0.0.1:1", "--token", "admin", "--non-home-min-age-days", "0"},
			want: "--non-home-min-age-days must be positive",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := adminStorageCleanupCacheLanesCommand(tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("cleanup cache lanes zero age error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestAdminStorageCleanupSendsMinimumAge(t *testing.T) {
	var got preflightapi.AdminCleanupExpiredArtifactsRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/admin/storage/cleanup-expired-artifacts" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		writeCLITestJSON(w, http.StatusOK, preflightapi.AdminCleanupExpiredArtifactsResponse{DryRun: true})
	}))
	defer server.Close()

	if _, err := captureStdoutForCLITest(t, func() error {
		return adminStorageCleanupExpiredArtifactsCommand([]string{
			"--api-url", server.URL,
			"--token", "admin_storage",
			"--dry-run",
			"--limit", "5",
			"--min-age-days", "30",
			"--include-malformed-artifacts",
			"--include-stale-uploads",
			"--include-run-logs",
			"--log-min-age-days", "45",
		})
	}); err != nil {
		t.Fatalf("cleanup minimum age command: %v", err)
	}
	if !got.DryRun || got.Limit != 5 || got.MinAgeSeconds != 30*24*60*60 || !got.IncludeMalformedArtifacts || !got.IncludeStaleUploads || !got.IncludeRunLogs || got.LogMinAgeSeconds != 45*24*60*60 {
		t.Fatalf("cleanup request = %+v, want dry-run limit 5 min age 30 days with upload/log cleanup", got)
	}
}

func TestAdminStorageCleanupCacheLanesSendsPolicy(t *testing.T) {
	fixed := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	var got preflightapi.AdminCleanupCacheLanesRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/admin/storage/cleanup-cache-lanes" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if gotAuth := r.Header.Get("Authorization"); gotAuth != "Bearer admin_storage" {
			t.Fatalf("authorization = %q, want bearer token", gotAuth)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		writeCLITestJSON(w, http.StatusOK, preflightapi.AdminCleanupCacheLanesResponse{
			DryRun:     got.DryRun,
			Candidates: 1,
			Lanes: []preflightapi.CacheLaneCleanupCandidate{{
				ID:         "lane_old",
				ProjectID:  "proj_story",
				WorkerID:   "worker_west",
				Region:     "us-west-2",
				HomeRegion: "us-east-1",
				LaneIndex:  1,
				Status:     "available",
				QuotaBytes: 1024,
				LastUsedAt: &fixed,
				Reason:     "stale_non_home_region_lane",
			}},
		})
	}))
	defer server.Close()

	output, err := captureStdoutForCLITest(t, func() error {
		return adminStorageCleanupCacheLanesCommand([]string{
			"--api-url", server.URL,
			"--token", "admin_storage",
			"--dry-run",
			"--limit", "7",
			"--min-age-days", "21",
			"--non-home-min-age-days", "3",
			"--keep-lanes-per-project-region", "2",
		})
	})
	if err != nil {
		t.Fatalf("cleanup cache lanes command: %v", err)
	}
	if !got.DryRun || got.Limit != 7 || got.MinAgeSeconds != 21*24*60*60 || got.NonHomeMinAgeSeconds != 3*24*60*60 || got.KeepLanesPerProjectRegion != 2 {
		t.Fatalf("cleanup cache lanes request = %+v, want dry-run policy flags", got)
	}
	if !strings.Contains(output, "candidate_lanes: 1") || !strings.Contains(output, "cache_lane: lane_old project=proj_story worker=worker_west region=us-west-2 home_region=us-east-1 index=1 reason=stale_non_home_region_lane quota_bytes=1024") {
		t.Fatalf("cleanup cache lanes output missing candidate:\n%s", output)
	}
}

func TestCheckDryRunUsesProjectHomeRegionForDiagnostics(t *testing.T) {
	t.Setenv("READYCI_PROJECT_ID", "")
	cliTestWorkingDirectoryMu.Lock()
	defer cliTestWorkingDirectoryMu.Unlock()

	repo := t.TempDir()
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore wd: %v", err)
		}
	}()
	runGitForCLITest(t, "init")
	if err := os.WriteFile(".readyci.yml", []byte(`
project_id: airline
checks:
  ci:
    command: make ci
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	settingsCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects/airline/settings":
			settingsCalled = true
			writeCLITestJSON(w, http.StatusOK, preflightapi.ProjectSettings{
				ProjectID:  "airline",
				HomeRegion: "us-east-1",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	output, err := captureStdoutForCLITest(t, func() error {
		return checkCommand([]string{"--api-url", server.URL, "--token", "pfl_user", "--dry-run", "ci"})
	})
	if err != nil {
		t.Fatalf("check dry-run: %v\n%s", err, output)
	}
	if !settingsCalled {
		t.Fatal("check dry-run did not look up project settings")
	}
	if !strings.Contains(output, "region: us-east-1") {
		t.Fatalf("check dry-run output = %q, want project home region", output)
	}
}

func TestAdminStorageCleanupReportsFailedObjectsAsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/admin/storage/cleanup-expired-artifacts" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer admin_storage" {
			t.Fatalf("authorization = %q, want bearer token", got)
		}
		var req preflightapi.AdminCleanupExpiredArtifactsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.DryRun || req.Limit != 2 {
			t.Fatalf("request = %+v, want delete limit 2", req)
		}
		writeCLITestJSON(w, http.StatusOK, preflightapi.AdminCleanupExpiredArtifactsResponse{
			ExpiredArtifacts: 1,
			FailedObjects:    []string{"run_artifact: timeout"},
		})
	}))
	defer server.Close()

	output, err := captureStdoutForCLITest(t, func() error {
		return adminStorageCleanupExpiredArtifactsCommand([]string{
			"--api-url", server.URL,
			"--token", "admin_storage",
			"--limit", "2",
		})
	})
	if err == nil || !strings.Contains(err.Error(), "failed to delete 1 artifact object") {
		t.Fatalf("cleanup failed-object error = %v, want failure; output=%s", err, output)
	}
	if !strings.Contains(output, "failed_object: run_artifact: timeout") {
		t.Fatalf("cleanup output missing failed object:\n%s", output)
	}
}
