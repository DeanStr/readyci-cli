package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestClientEndpointWrappersBuildExpectedRequests(t *testing.T) {
	type seenRequest struct {
		Method string
		Path   string
		Query  string
		Auth   string
		Body   string
	}
	var seen []seenRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		seen = append(seen, seenRequest{
			Method: r.Method,
			Path:   strings.Split(r.RequestURI, "?")[0],
			Query:  r.URL.RawQuery,
			Auth:   r.Header.Get("Authorization"),
			Body:   string(body),
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, WithToken(" token_test "))
	ctx := context.Background()
	githubRepository := "readyci/app"
	calls := []struct {
		name       string
		run        func() error
		method     string
		path       string
		queryParts []string
		bodyParts  []string
	}{
		{
			name: "create run",
			run: func() error {
				_, err := client.CreateRun(ctx, preflightapi.CreateRunRequest{ProjectID: "proj_dev", Command: "go test ./..."})
				return err
			},
			method:    http.MethodPost,
			path:      "/v1/runs",
			bodyParts: []string{`"project_id":"proj_dev"`, `"command":"go test ./..."`},
		},
		{
			name: "complete upload",
			run: func() error {
				_, err := client.CompleteUploadWithRequest(ctx, "run/with space", preflightapi.CompleteUploadRequest{})
				return err
			},
			method:    http.MethodPost,
			path:      "/v1/runs/run%2Fwith%20space/upload-complete",
			bodyParts: []string{"{}"},
		},
		{
			name:       "cache usage",
			run:        func() error { _, err := client.CacheUsage(ctx, "proj with space"); return err },
			method:     http.MethodGet,
			path:       "/v1/projects/proj%20with%20space/cache",
			queryParts: nil,
		},
		{
			name:   "purge cache",
			run:    func() error { _, err := client.PurgeCache(ctx, "proj with space"); return err },
			method: http.MethodDelete,
			path:   "/v1/projects/proj%20with%20space/cache",
		},
		{
			name:   "project settings",
			run:    func() error { _, err := client.ProjectSettings(ctx, "proj dev"); return err },
			method: http.MethodGet,
			path:   "/v1/projects/proj%20dev/settings",
		},
		{
			name: "update project settings",
			run: func() error {
				_, err := client.UpdateProjectSettings(ctx, "proj dev", preflightapi.UpsertProjectSettingsRequest{GitHubRepository: &githubRepository})
				return err
			},
			method:    http.MethodPut,
			path:      "/v1/projects/proj%20dev/settings",
			bodyParts: []string{`"github_repository":"readyci/app"`},
		},
		{
			name:   "list project secrets",
			run:    func() error { _, err := client.ListProjectSecrets(ctx, "proj dev"); return err },
			method: http.MethodGet,
			path:   "/v1/projects/proj%20dev/secrets",
		},
		{
			name: "upsert project secret",
			run: func() error {
				_, err := client.UpsertProjectSecret(ctx, "proj dev", "READYCI TOKEN", "secret")
				return err
			},
			method:    http.MethodPut,
			path:      "/v1/projects/proj%20dev/secrets/READYCI%20TOKEN",
			bodyParts: []string{`"value":"secret"`},
		},
		{
			name: "delete project secret",
			run: func() error {
				_, err := client.DeleteProjectSecret(ctx, "proj dev", "READYCI TOKEN", "test cleanup")
				return err
			},
			method:    http.MethodDelete,
			path:      "/v1/projects/proj%20dev/secrets/READYCI%20TOKEN",
			bodyParts: []string{`"reason":"test cleanup"`},
		},
		{
			name:   "account usage",
			run:    func() error { _, err := client.AccountUsage(ctx); return err },
			method: http.MethodGet,
			path:   "/v1/account",
		},
		{
			name: "worker capability",
			run: func() error {
				_, err := client.WorkerCapability(ctx, preflightapi.WorkerCapabilityCheckRequest{Spec: preflightapi.RunSpec{RunID: "run_1"}})
				return err
			},
			method:    http.MethodPost,
			path:      "/v1/workers/capability",
			bodyParts: []string{`"run_id":"run_1"`},
		},
		{
			name: "create user token",
			run: func() error {
				_, err := client.CreateUserAPIToken(ctx, preflightapi.CreateAPITokenRequest{Name: "CLI"})
				return err
			},
			method:    http.MethodPost,
			path:      "/v1/account/tokens",
			bodyParts: []string{`"name":"CLI"`},
		},
		{
			name:       "list runs",
			run:        func() error { _, err := client.ListRuns(ctx, "proj dev", preflightapi.StatusRunning, 25); return err },
			method:     http.MethodGet,
			path:       "/v1/runs",
			queryParts: []string{"project=proj+dev", "status=running", "limit=25"},
		},
		{
			name:   "get run",
			run:    func() error { _, err := client.GetRun(ctx, "run 1"); return err },
			method: http.MethodGet,
			path:   "/v1/runs/run%201",
		},
		{
			name:   "get run group",
			run:    func() error { _, err := client.GetRunGroup(ctx, "group 1"); return err },
			method: http.MethodGet,
			path:   "/v1/run-groups/group%201",
		},
		{
			name:       "latest run group",
			run:        func() error { _, err := client.LatestRunGroup(ctx, "proj dev"); return err },
			method:     http.MethodGet,
			path:       "/v1/run-groups/latest",
			queryParts: []string{"project_id=proj+dev"},
		},
		{
			name: "cancel run group",
			run: func() error {
				_, err := client.CancelRunGroup(ctx, "group 1", preflightapi.CancelRunGroupRequest{Reason: "operator"})
				return err
			},
			method:    http.MethodPost,
			path:      "/v1/run-groups/group%201/cancel",
			bodyParts: []string{`"reason":"operator"`},
		},
		{
			name:       "get run by idempotency",
			run:        func() error { _, err := client.GetRunByIdempotencyKey(ctx, "proj dev", "key/1"); return err },
			method:     http.MethodGet,
			path:       "/v1/runs/by-idempotency-key",
			queryParts: []string{"project=proj+dev", "key=key%2F1"},
		},
		{
			name:       "list runs by idempotency",
			run:        func() error { _, err := client.ListRunsByIdempotencyKey(ctx, "proj dev", "key/1"); return err },
			method:     http.MethodGet,
			path:       "/v1/runs/by-idempotency-key",
			queryParts: []string{"project=proj+dev", "key=key%2F1", "include_children=true"},
		},
		{
			name:       "list run events page",
			run:        func() error { _, err := client.ListRunEventsPage(ctx, "run 1", 42, 7); return err },
			method:     http.MethodGet,
			path:       "/v1/runs/run%201/events",
			queryParts: []string{"format=json", "cursor=42", "limit=7"},
		},
		{
			name:       "list run events tail",
			run:        func() error { _, err := client.ListRunEventsTail(ctx, "run 1", 5, true, true, true); return err },
			method:     http.MethodGet,
			path:       "/v1/runs/run%201/events",
			queryParts: []string{"format=json", "tail=5", "errors_only=true", "logs_only=true", "include_lifecycle=true"},
		},
		{
			name:      "exchange cli auth code",
			run:       func() error { _, err := client.ExchangeCLIAuthCode(ctx, "code", "laptop"); return err },
			method:    http.MethodPost,
			path:      "/v1/auth/cli/exchange",
			bodyParts: []string{`"code":"code"`, `"name":"laptop"`},
		},
		{
			name:   "revoke current cli token",
			run:    func() error { _, err := client.RevokeCurrentCLIToken(ctx); return err },
			method: http.MethodDelete,
			path:   "/v1/auth/cli/token",
		},
		{
			name: "admin list runs",
			run: func() error {
				_, err := client.AdminListRuns(ctx, "proj dev", preflightapi.StatusFailed, 10)
				return err
			},
			method:     http.MethodGet,
			path:       "/v1/admin/runs",
			queryParts: []string{"project=proj+dev", "status=failed", "limit=10"},
		},
		{
			name:   "admin get run",
			run:    func() error { _, err := client.AdminGetRun(ctx, "run 1"); return err },
			method: http.MethodGet,
			path:   "/v1/admin/runs/run%201",
		},
		{
			name:       "admin events page",
			run:        func() error { _, err := client.AdminListEventsPage(ctx, "run 1", 42, 7); return err },
			method:     http.MethodGet,
			path:       "/v1/admin/runs/run%201/events",
			queryParts: []string{"cursor=42", "limit=7"},
		},
		{
			name:   "list artifacts",
			run:    func() error { _, err := client.ListArtifacts(ctx, "run 1"); return err },
			method: http.MethodGet,
			path:   "/v1/runs/run%201/artifacts",
		},
		{
			name:      "admin cancel run",
			run:       func() error { _, err := client.AdminCancelRun(ctx, "run 1", "operator"); return err },
			method:    http.MethodPost,
			path:      "/v1/admin/runs/run%201/cancel",
			bodyParts: []string{`"reason":"operator"`},
		},
		{
			name:   "admin list tokens",
			run:    func() error { _, err := client.AdminListAPITokens(ctx); return err },
			method: http.MethodGet,
			path:   "/v1/admin/tokens",
		},
		{
			name: "admin create token",
			run: func() error {
				_, err := client.AdminCreateAPIToken(ctx, preflightapi.CreateAPITokenRequest{Name: "Support"})
				return err
			},
			method:    http.MethodPost,
			path:      "/v1/admin/tokens",
			bodyParts: []string{`"name":"Support"`},
		},
		{
			name:   "admin revoke token",
			run:    func() error { _, err := client.AdminRevokeAPIToken(ctx, "token 1"); return err },
			method: http.MethodDelete,
			path:   "/v1/admin/tokens/token%201",
		},
		{
			name:   "admin list teams",
			run:    func() error { _, err := client.AdminListTeams(ctx); return err },
			method: http.MethodGet,
			path:   "/v1/admin/teams",
		},
		{
			name:   "admin team detail",
			run:    func() error { _, err := client.AdminTeamDetail(ctx, "team 1"); return err },
			method: http.MethodGet,
			path:   "/v1/admin/teams/team%201",
		},
		{
			name:      "admin disable team",
			run:       func() error { _, err := client.AdminDisableTeam(ctx, "team 1", "fraud"); return err },
			method:    http.MethodPost,
			path:      "/v1/admin/teams/team%201/disable",
			bodyParts: []string{`"reason":"fraud"`},
		},
		{
			name:      "admin release billing hold",
			run:       func() error { _, err := client.AdminReleaseBillingHold(ctx, "team 1", "paid"); return err },
			method:    http.MethodPost,
			path:      "/v1/admin/teams/team%201/release-billing-hold",
			bodyParts: []string{`"reason":"paid"`},
		},
		{
			name: "admin billing evidence",
			run: func() error {
				_, err := client.AdminBillingDisputeEvidence(ctx, "team 1", "stripe", "dp/1")
				return err
			},
			method:     http.MethodGet,
			path:       "/v1/admin/teams/team%201/billing-evidence",
			queryParts: []string{"provider=stripe", "dispute_id=dp%2F1"},
		},
		{
			name:      "admin cancel team runs",
			run:       func() error { _, err := client.AdminCancelTeamRuns(ctx, "team 1", "fraud"); return err },
			method:    http.MethodPost,
			path:      "/v1/admin/teams/team%201/cancel-runs",
			bodyParts: []string{`"reason":"fraud"`},
		},
		{
			name:   "admin list workers",
			run:    func() error { _, err := client.AdminListWorkers(ctx); return err },
			method: http.MethodGet,
			path:   "/v1/admin/workers",
		},
		{
			name:      "admin drain worker",
			run:       func() error { _, err := client.AdminDrainWorker(ctx, "worker 1", "maint"); return err },
			method:    http.MethodPost,
			path:      "/v1/admin/workers/worker%201/drain",
			bodyParts: []string{`"reason":"maint"`},
		},
		{
			name:      "admin undrain worker",
			run:       func() error { _, err := client.AdminUndrainWorker(ctx, "worker 1"); return err },
			method:    http.MethodPost,
			path:      "/v1/admin/workers/worker%201/undrain",
			bodyParts: []string{"{}"},
		},
		{
			name:      "admin delete worker",
			run:       func() error { _, err := client.AdminDeleteWorker(ctx, "worker 1", "retired"); return err },
			method:    http.MethodDelete,
			path:      "/v1/admin/workers/worker%201",
			bodyParts: []string{`"reason":"retired"`},
		},
		{
			name:   "admin queue status",
			run:    func() error { _, err := client.AdminQueueStatus(ctx); return err },
			method: http.MethodGet,
			path:   "/v1/admin/queue/status",
		},
		{
			name:   "admin queue explain",
			run:    func() error { _, err := client.AdminQueueExplain(ctx, "run 1"); return err },
			method: http.MethodGet,
			path:   "/v1/admin/queue/explain/run%201",
		},
		{
			name:   "admin storage audit",
			run:    func() error { _, err := client.AdminStorageAudit(ctx); return err },
			method: http.MethodGet,
			path:   "/v1/admin/storage/audit",
		},
		{
			name: "admin cleanup expired artifacts",
			run: func() error {
				_, err := client.AdminCleanupExpiredArtifacts(ctx, preflightapi.AdminCleanupExpiredArtifactsRequest{DryRun: true, Limit: 5})
				return err
			},
			method:    http.MethodPost,
			path:      "/v1/admin/storage/cleanup-expired-artifacts",
			bodyParts: []string{`"dry_run":true`, `"limit":5`},
		},
		{
			name: "admin cleanup cache lanes",
			run: func() error {
				_, err := client.AdminCleanupCacheLanes(ctx, preflightapi.AdminCleanupCacheLanesRequest{DryRun: true, Limit: 5, KeepLanesPerProjectRegion: 1})
				return err
			},
			method:    http.MethodPost,
			path:      "/v1/admin/storage/cleanup-cache-lanes",
			bodyParts: []string{`"dry_run":true`, `"limit":5`, `"keep_lanes_per_project_region":1`},
		},
		{
			name:   "admin cleanup expired artifacts status",
			run:    func() error { _, err := client.AdminCleanupExpiredArtifactsStatus(ctx); return err },
			method: http.MethodGet,
			path:   "/v1/admin/storage/cleanup-expired-artifacts/status",
		},
		{
			name: "admin cancel cleanup expired artifacts",
			run: func() error {
				_, err := client.AdminCancelCleanupExpiredArtifacts(ctx, preflightapi.AdminCancelCleanupExpiredArtifactsRequest{Reason: "pause"})
				return err
			},
			method:    http.MethodPost,
			path:      "/v1/admin/storage/cleanup-expired-artifacts/cancel",
			bodyParts: []string{`"reason":"pause"`},
		},
	}

	for _, call := range calls {
		t.Run(call.name, func(t *testing.T) {
			seen = nil
			if err := call.run(); err != nil {
				t.Fatalf("call failed: %v", err)
			}
			if len(seen) != 1 {
				t.Fatalf("server saw %d requests, want 1", len(seen))
			}
			got := seen[0]
			if got.Method != call.method || got.Path != call.path {
				t.Fatalf("request = %s %s, want %s %s", got.Method, got.Path, call.method, call.path)
			}
			if got.Auth != "Bearer token_test" {
				t.Fatalf("Authorization = %q, want bearer token", got.Auth)
			}
			for _, want := range call.queryParts {
				if !strings.Contains(got.Query, want) {
					t.Fatalf("query %q missing %q", got.Query, want)
				}
			}
			for _, want := range call.bodyParts {
				if !strings.Contains(got.Body, want) {
					t.Fatalf("body %q missing %q", got.Body, want)
				}
			}
		})
	}
}

func TestNewClientNormalizesAPIBaseURLBeforeRequests(t *testing.T) {
	var gotPath string
	var gotQuery string
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"team_id":"team_test","plan":{"id":"dev"}}`))
	}))
	defer server.Close()

	baseURL := strings.Replace(server.URL, "http://", "http://user:pass@", 1) + "/api?token=secret#fragment"
	usage, err := NewClient(baseURL).AccountUsage(context.Background())
	if err != nil {
		t.Fatalf("account usage with decorated base URL: %v", err)
	}
	if usage.TeamID != "team_test" {
		t.Fatalf("usage team = %q, want team_test", usage.TeamID)
	}
	if gotPath != "/api/v1/account" {
		t.Fatalf("path = %q, want /api/v1/account", gotPath)
	}
	if gotQuery != "" {
		t.Fatalf("query = %q, want stripped API base query", gotQuery)
	}
	if gotAuth != "" {
		t.Fatalf("authorization = %q, want no userinfo-derived auth header", gotAuth)
	}
}

func TestClientUsesConfiguredHostHeader(t *testing.T) {
	t.Setenv("READYCI_API_HOST_HEADER", "api.readyci.test")

	var gotHost string
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, WithToken("token_test"))
	if err := client.doJSON(context.Background(), http.MethodGet, "/v1/test", nil, nil); err != nil {
		t.Fatal(err)
	}
	if gotHost != "api.readyci.test" {
		t.Fatalf("Host = %q, want api.readyci.test", gotHost)
	}
	if gotAuth != "Bearer token_test" {
		t.Fatalf("Authorization = %q, want bearer token", gotAuth)
	}
}

func TestClientUsesConfiguredForwardedForHeader(t *testing.T) {
	t.Setenv("READYCI_API_FORWARDED_FOR", "198.51.100.24")

	var gotForwardedFor string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotForwardedFor = r.Header.Get("X-Forwarded-For")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, WithToken("token_test"))
	if err := client.doJSON(context.Background(), http.MethodGet, "/v1/test", nil, nil); err != nil {
		t.Fatal(err)
	}
	if gotForwardedFor != "198.51.100.24" {
		t.Fatalf("X-Forwarded-For = %q, want 198.51.100.24", gotForwardedFor)
	}
}

func TestClientIgnoresRemovedLegacyHeaderEnvironment(t *testing.T) {
	t.Setenv("PRE"+"FLIGHT_API_HOST_HEADER", "legacy-host.example")
	t.Setenv("PRE"+"FLIGHT_API_FORWARDED_FOR", "198.51.100.99")
	t.Setenv("READYCI_API_HOST_HEADER", "")
	t.Setenv("READYCI_API_FORWARDED_FOR", "")

	client := NewClient("https://api.readyci.test")
	if client.hostHeader != "" || client.forwardedFor != "" {
		t.Fatalf("removed legacy environment affected client: host=%q forwarded_for=%q", client.hostHeader, client.forwardedFor)
	}
}
