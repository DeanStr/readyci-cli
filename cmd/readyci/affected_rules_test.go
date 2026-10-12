package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestDependencyWarmPlansForAffectedRequiresExplicitDependenciesCheck(t *testing.T) {
	repo := t.TempDir()
	cfg := config.DefaultProjectConfig()
	cfg.Network.Mode = "none"
	cfg.Network.WarmMode = "allowlist"
	cfg.Network.WarmAllowlist = []string{"crates.io"}
	cfg.Checks = map[string]config.CheckConfig{
		"rust-deps": {Command: "cargo fetch --locked"},
		"db":        {Command: "cargo test --features db"},
		"db-make":   {Command: "make check-ci-db"},
		"docs":      {Command: "markdownlint README.md"},
		"trust":     {Command: "make internal/trust-check"},
		"db-steps": {
			Steps: []config.StepConfig{{
				Name:    "db",
				Command: "cargo test --features db",
			}},
		},
	}
	dbPlan, err := resolveCheckRunPlan("db", cfg, checkResolveOptions{RetryCount: -1})
	if err != nil {
		t.Fatalf("resolve db: %v", err)
	}
	dbStepsPlan, err := resolveCheckRunPlan("db-steps", cfg, checkResolveOptions{RetryCount: -1})
	if err != nil {
		t.Fatalf("resolve db-steps: %v", err)
	}
	rustDepsPlan, err := resolveCheckRunPlan("rust-deps", cfg, checkResolveOptions{RetryCount: -1})
	if err != nil {
		t.Fatalf("resolve rust-deps: %v", err)
	}
	dbMakePlan, err := resolveCheckRunPlan("db-make", cfg, checkResolveOptions{RetryCount: -1})
	if err != nil {
		t.Fatalf("resolve db-make: %v", err)
	}
	docsPlan, err := resolveCheckRunPlan("docs", cfg, checkResolveOptions{RetryCount: -1})
	if err != nil {
		t.Fatalf("resolve docs: %v", err)
	}
	docsWarm, err := dependencyWarmPlansForAffected(repo, []affectedCheckPlan{{Plan: docsPlan}}, cfg, checkResolveOptions{RetryCount: -1})
	if err != nil {
		t.Fatalf("docs warm plans: %v", err)
	}
	if len(docsWarm) != 0 {
		t.Fatalf("docs warm plans = %+v, want none", docsWarm)
	}
	trustPlan, err := resolveCheckRunPlan("trust", cfg, checkResolveOptions{RetryCount: -1})
	if err != nil {
		t.Fatalf("resolve trust: %v", err)
	}
	trustWarm, err := dependencyWarmPlansForAffected(repo, []affectedCheckPlan{{Plan: trustPlan}}, cfg, checkResolveOptions{RetryCount: -1})
	if err != nil {
		t.Fatalf("trust warm plans: %v", err)
	}
	if len(trustWarm) != 0 {
		t.Fatalf("trust warm plans = %+v, want none for embedded rust substring", trustWarm)
	}
	dbMakeWarm, err := dependencyWarmPlansForAffected(repo, []affectedCheckPlan{{Plan: dbMakePlan}}, cfg, checkResolveOptions{RetryCount: -1})
	if err != nil {
		t.Fatalf("db-make warm plans: %v", err)
	}
	if len(dbMakeWarm) != 0 {
		t.Fatalf("db-make warm plans = %+v, want none without Rust evidence", dbMakeWarm)
	}
	dbWarm, err := dependencyWarmPlansForAffected(repo, []affectedCheckPlan{{Plan: dbPlan}}, cfg, checkResolveOptions{RetryCount: -1})
	if err != nil {
		t.Fatalf("db warm plans: %v", err)
	}
	if len(dbWarm) != 0 {
		t.Fatalf("db warm plans = %+v, want none without an explicit dependencies check", dbWarm)
	}
	dbWarmAllowlist, err := dependencyWarmPlansForAffected(repo, []affectedCheckPlan{{Plan: dbPlan}}, cfg, checkResolveOptions{
		RetryCount:       -1,
		NetworkMode:      "allowlist",
		NetworkAllowlist: "crates.io",
	})
	if err != nil {
		t.Fatalf("db warm plans with allowlist override: %v", err)
	}
	if len(dbWarmAllowlist) != 0 {
		t.Fatalf("db warm plans with run override = %+v, want none without an explicit dependencies check", dbWarmAllowlist)
	}
	dbWarmOpen, err := dependencyWarmPlansForAffected(repo, []affectedCheckPlan{{Plan: dbPlan}}, cfg, checkResolveOptions{RetryCount: -1, Open: true})
	if err != nil {
		t.Fatalf("db warm plans with open override: %v", err)
	}
	if len(dbWarmOpen) != 0 {
		t.Fatalf("db warm plans with open override = %+v, want none without an explicit dependencies check", dbWarmOpen)
	}
	dbStepsWarm, err := dependencyWarmPlansForAffected(repo, []affectedCheckPlan{{Plan: dbStepsPlan}}, cfg, checkResolveOptions{RetryCount: -1})
	if err != nil {
		t.Fatalf("db-steps warm plans: %v", err)
	}
	if len(dbStepsWarm) != 0 {
		t.Fatalf("db-steps warm plans = %+v, want none without an explicit dependencies check", dbStepsWarm)
	}
	rustDepsWarm, err := dependencyWarmPlansForAffected(repo, []affectedCheckPlan{{Plan: rustDepsPlan}, {Plan: dbPlan}}, cfg, checkResolveOptions{RetryCount: -1})
	if err != nil {
		t.Fatalf("rust-deps selected warm plans: %v", err)
	}
	if len(rustDepsWarm) != 0 {
		t.Fatalf("rust-deps selected warm plans = %+v, want no inferred legacy warm check", rustDepsWarm)
	}
}

func TestDependencyWarmPlansForAffectedUsesGenericDependencyCheck(t *testing.T) {
	cfg := config.DefaultProjectConfig()
	cfg.Network.Mode = "none"
	cfg.Network.WarmMode = "allowlist"
	cfg.Network.WarmAllowlist = []string{"proxy.golang.org", "registry.npmjs.org"}
	cfg.Checks = map[string]config.CheckConfig{
		"dependencies": {Command: "go mod download && pnpm install --ignore-scripts"},
		"go":           {Command: "go test ./..."},
		"docs":         {Command: "markdownlint README.md"},
	}
	goPlan, err := resolveCheckRunPlan("go", cfg, checkResolveOptions{RetryCount: -1})
	if err != nil {
		t.Fatal(err)
	}
	warm, err := dependencyWarmPlansForAffected(t.TempDir(), []affectedCheckPlan{{Plan: goPlan}}, cfg, checkResolveOptions{RetryCount: -1})
	if err != nil {
		t.Fatal(err)
	}
	if len(warm) != 1 || warm[0].Plan.Name != "dependencies" || warm[0].Plan.Options.LogicalChildID != "warm:dependencies" ||
		warm[0].Plan.Options.NetworkMode != "allowlist" ||
		!reflect.DeepEqual(warm[0].Plan.Options.NetworkAllowlist, []string{"proxy.golang.org", "registry.npmjs.org"}) {
		t.Fatalf("generic dependency warm plan = %+v", warm)
	}

	dependenciesPlan, err := resolveCheckRunPlan("dependencies", cfg, checkResolveOptions{RetryCount: -1})
	if err != nil {
		t.Fatal(err)
	}
	warm, err = dependencyWarmPlansForAffected(t.TempDir(), []affectedCheckPlan{{Plan: dependenciesPlan}}, cfg, checkResolveOptions{RetryCount: -1})
	if err != nil {
		t.Fatal(err)
	}
	if len(warm) != 0 {
		t.Fatalf("selected dependencies check recursively warmed itself: %+v", warm)
	}

	docsPlan, err := resolveCheckRunPlan("docs", cfg, checkResolveOptions{RetryCount: -1})
	if err != nil {
		t.Fatal(err)
	}
	warm, err = dependencyWarmPlansForAffected(t.TempDir(), []affectedCheckPlan{{Plan: docsPlan}}, cfg, checkResolveOptions{RetryCount: -1})
	if err != nil {
		t.Fatal(err)
	}
	if len(warm) != 1 || warm[0].Plan.Name != "dependencies" {
		t.Fatalf("explicit warm request for docs = %+v, want the configured dependencies node", warm)
	}
}

func TestDependencyWarmPlansForAffectedSupportsMixedRunnerSizes(t *testing.T) {
	cfg := config.DefaultProjectConfig()
	cfg.Network.Mode = "none"
	cfg.Network.WarmMode = "allowlist"
	cfg.Network.WarmAllowlist = []string{"proxy.golang.org"}
	cfg.Checks = map[string]config.CheckConfig{
		"dependencies": {Command: "go mod download"},
		"unit":         {Command: "go test ./..."},
		"ci":           {Command: "make ci", RunnerSize: "large"},
	}
	unit, err := resolveCheckRunPlan("unit", cfg, checkResolveOptions{RetryCount: -1})
	if err != nil {
		t.Fatal(err)
	}
	ci, err := resolveCheckRunPlan("ci", cfg, checkResolveOptions{RetryCount: -1})
	if err != nil {
		t.Fatal(err)
	}
	warm, err := dependencyWarmPlansForAffected(
		t.TempDir(),
		[]affectedCheckPlan{{Plan: unit}, {Plan: ci}},
		cfg,
		checkResolveOptions{RetryCount: -1},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(warm) != 2 || warm[0].Plan.Options.RunnerSize != "large" ||
		warm[0].Plan.Options.LogicalChildID != "warm:dependencies:runner=large" ||
		warm[1].Plan.Options.RunnerSize != "standard" ||
		warm[1].Plan.Options.LogicalChildID != "warm:dependencies:runner=standard" {
		t.Fatalf("mixed-runner dependency warm plans = %+v", warm)
	}
}

func TestDependencyWarmPlansForAffectedSkipsPolicyForNoPersistentTarget(t *testing.T) {
	cfg := config.DefaultProjectConfig()
	cfg.Network.Mode = "none"
	cfg.Checks = map[string]config.CheckConfig{
		"dependencies": {Command: "go mod download"},
		"ephemeral": {
			Command:   "go test ./...",
			CacheMode: "ephemeral",
		},
	}
	dependencies, err := resolveCheckRunPlan("dependencies", cfg, checkResolveOptions{RetryCount: -1})
	if err != nil {
		t.Fatal(err)
	}
	ephemeral, err := resolveCheckRunPlan("ephemeral", cfg, checkResolveOptions{RetryCount: -1})
	if err != nil {
		t.Fatal(err)
	}
	for _, selected := range [][]affectedCheckPlan{
		{{Plan: dependencies}},
		{{Plan: ephemeral}},
		{{Plan: dependencies}, {Plan: ephemeral}},
	} {
		warm, err := dependencyWarmPlansForAffected(t.TempDir(), selected, cfg, checkResolveOptions{RetryCount: -1})
		if err != nil {
			t.Fatalf("no-op dependency warm selection %+v: %v", affectedPlanNames(selected), err)
		}
		if len(warm) != 0 {
			t.Fatalf("no-op dependency warm selection %+v produced plans: %+v", affectedPlanNames(selected), warm)
		}
	}
}

func TestDependencyWarmPlansForAffectedMatchesEverySelectedCacheQuota(t *testing.T) {
	cfg := config.DefaultProjectConfig()
	cfg.Network.Mode = "none"
	cfg.Network.WarmMode = "allowlist"
	cfg.Network.WarmAllowlist = []string{"proxy.golang.org"}
	cfg.Cache.Size = "10GiB"
	cfg.Checks = map[string]config.CheckConfig{
		"dependencies": {Command: "go mod download"},
		"unit": {
			Command: "go test ./...",
			Cache:   config.CacheConfig{Size: "20GiB"},
		},
		"integration": {
			Command: "go test -tags=integration ./...",
			Cache:   config.CacheConfig{Size: "40GiB"},
		},
	}
	unit, err := resolveCheckRunPlan("unit", cfg, checkResolveOptions{RetryCount: -1})
	if err != nil {
		t.Fatal(err)
	}
	integration, err := resolveCheckRunPlan("integration", cfg, checkResolveOptions{RetryCount: -1})
	if err != nil {
		t.Fatal(err)
	}
	warm, err := dependencyWarmPlansForAffected(
		t.TempDir(),
		[]affectedCheckPlan{{Plan: unit}, {Plan: integration}},
		cfg,
		checkResolveOptions{RetryCount: -1},
	)
	if err == nil || !strings.Contains(err.Error(), "must use one cache lane size") {
		t.Fatalf("multiple cache quota warm plans = %+v error = %v, want fail-fast configuration error", warm, err)
	}
}

func TestDependencyWarmPlansForAffectedUsesSelectedCacheKeyNamespace(t *testing.T) {
	cfg := config.DefaultProjectConfig()
	cfg.Network.Mode = "none"
	cfg.Network.WarmMode = "allowlist"
	cfg.Network.WarmAllowlist = []string{"proxy.golang.org"}
	cfg.Cache.KeyFiles = []string{"go.work.sum"}
	cfg.Checks = map[string]config.CheckConfig{
		"dependencies": {Command: "go mod download"},
		"unit": {
			Command: "go test ./...",
			Cache:   config.CacheConfig{KeyFiles: []string{"cli/go.sum", "./cli/go.sum"}},
		},
	}
	unit, err := resolveCheckRunPlan("unit", cfg, checkResolveOptions{RetryCount: -1})
	if err != nil {
		t.Fatal(err)
	}
	unit.Options.Region = "eu-west"
	unit.Options.Image = "target-image"
	unit.Options.RunnerSize = "large"
	unit.Options.Docker = true
	unit.Options.WorkerSelector = map[string]string{"pool": "trusted"}
	unit.Options.CachePaths = []string{"~/.cache/go", "./build/cache"}
	warm, err := dependencyWarmPlansForAffected(
		t.TempDir(),
		[]affectedCheckPlan{{Plan: unit}},
		cfg,
		checkResolveOptions{RetryCount: -1},
	)
	if err != nil {
		t.Fatal(err)
	}
	wantKeyFiles := []string{"go.work.sum", "cli/go.sum"}
	wantCachePaths := []string{"~/.cache/go", "build/cache"}
	if len(warm) != 1 || !reflect.DeepEqual(warm[0].Plan.Options.CacheKeyFiles, wantKeyFiles) ||
		!reflect.DeepEqual(warm[0].Plan.Options.CachePaths, wantCachePaths) ||
		warm[0].Plan.Options.Region != "eu-west" || warm[0].Plan.Options.Image != "target-image" ||
		warm[0].Plan.Options.RunnerSize != "large" || warm[0].Plan.Options.Docker ||
		!reflect.DeepEqual(warm[0].Plan.Options.WorkerSelector, map[string]string{
			"pool": "trusted", preflightapi.DockerCapabilitySystemLabel: "true",
		}) {
		t.Fatalf("dependency warm target contract = %+v, want key files %v, cache paths %v, and target placement", warm[0].Plan.Options, wantKeyFiles, wantCachePaths)
	}
}

func TestDependencyWarmPlansForAffectedDoesNotInferMakeWrapperWarmChecks(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "Makefile"), []byte(`
READYCI ?= readyci

define remote-readyci-rust-check
	@$(READYCI) check $(READYCI_CHECK_FLAGS) rust-deps
	@$(READYCI) check $(READYCI_CHECK_FLAGS) $(1)
endef

remote-check-db:
	$(call remote-readyci-rust-check,db)
`), 0o644); err != nil {
		t.Fatalf("write Makefile: %v", err)
	}
	cfg := config.DefaultProjectConfig()
	cfg.Network.Mode = "none"
	cfg.Network.WarmMode = "allowlist"
	cfg.Network.WarmAllowlist = []string{"crates.io"}
	cfg.Checks = map[string]config.CheckConfig{
		"rust-deps": {Command: "cargo fetch --locked"},
		"db":        {Command: "make check-ci-db"},
	}
	dbPlan, err := resolveCheckRunPlan("db", cfg, checkResolveOptions{RetryCount: -1})
	if err != nil {
		t.Fatalf("resolve db: %v", err)
	}
	dbWarm, err := dependencyWarmPlansForAffected(repo, []affectedCheckPlan{{Plan: dbPlan}}, cfg, checkResolveOptions{RetryCount: -1})
	if err != nil {
		t.Fatalf("db warm plans: %v", err)
	}
	if len(dbWarm) != 0 {
		t.Fatalf("db warm plans = %+v, want no inferred Make wrapper warm check", dbWarm)
	}
}

func TestTouchedWarmDependenciesWarmsSelectedCheckPath(t *testing.T) {
	repo := chdirCLITestRepo(t, "touched-warm-selected-check")
	runGitForCLITest(t, "init")
	runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	if err := os.WriteFile(filepath.Join(repo, "Cargo.toml"), []byte("[package]\nname = \"demo\"\nversion = \"0.1.0\"\nedition = \"2021\"\n"), 0o644); err != nil {
		t.Fatalf("write Cargo.toml: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
project_id: airline
network:
  mode: none
  warm_mode: allowlist
  warm_allowlist: [crates.io]
checks:
  dependencies:
    command: cargo fetch --locked
  db:
    command: cargo test --features db
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var serverURL string
	var created []preflightapi.CreateRunRequest
	server := httptest.NewServer(successfulExecutionTestHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs":
			var req preflightapi.CreateRunRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode create request: %v", err)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			created = append(created, req)
			runID := "run_selected_warm_" + req.CommandAlias
			writeCLITestJSON(w, http.StatusCreated, preflightapi.CreateRunResponse{
				RunID:     runID,
				UploadURL: serverURL + "/upload/" + runID,
				EventsURL: serverURL + "/events/" + runID,
				Status:    preflightapi.StatusAwaitingUpload,
			})
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/upload/"):
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte("{}\n"))
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/runs/") && strings.HasSuffix(r.URL.Path, "/upload-complete"):
			runID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/runs/"), "/upload-complete")
			writeCLITestJSON(w, http.StatusOK, preflightapi.CompleteUploadResponse{RunID: runID, Status: preflightapi.StatusQueued})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/events/"):
			writeCLITestFinishedEvent(w)
		default:
			http.NotFound(w, r)
		}
	})))
	defer server.Close()
	serverURL = server.URL

	err := affectedCommand([]string{"--check", "touched",
		"--api-url", server.URL,
		"--token", "pfl_warm",
		"--check", "db",
		"--network-mode", "allowlist",
		"--network-allowlist", "crates.io",
	})
	if err != nil && !isSuccessfulExit(err) {
		t.Fatalf("affected automatic preparation: %v", err)
	}
	if len(created) != 2 {
		t.Fatalf("created runs = %d, want warm and selected check: %+v", len(created), created)
	}
	if created[0].CommandAlias != "dependencies" || created[0].NetworkMode != "allowlist" {
		t.Fatalf("warm create request = %+v, want dependencies with allowlist network", created[0])
	}
	if !reflect.DeepEqual(created[0].NetworkAllowlist, []string{"crates.io"}) {
		t.Fatalf("warm create request allowlist = %v, want crates.io", created[0].NetworkAllowlist)
	}
	if created[1].CommandAlias != "db" || created[1].NetworkMode != "allowlist" {
		t.Fatalf("selected create request = %+v, want db with allowlist network", created[1])
	}
	if len(created[1].NetworkAllowlist) != 1 || created[1].NetworkAllowlist[0] != "crates.io" {
		t.Fatalf("selected create request allowlist = %v, want crates.io", created[1].NetworkAllowlist)
	}
}

func setupRustAffectedWarmCLITestRepo(t *testing.T, name string) string {
	t.Helper()
	repo := chdirCLITestRepo(t, name)
	runGitForCLITest(t, "init")
	runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	if err := os.MkdirAll(filepath.Join(repo, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "Cargo.toml"), []byte("[package]\nname = \"demo\"\nversion = \"0.1.0\"\nedition = \"2021\"\n"), 0o644); err != nil {
		t.Fatalf("write Cargo.toml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src", "lib.rs"), []byte("pub fn initial() {}\n"), 0o644); err != nil {
		t.Fatalf("write lib.rs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
project_id: airline
network:
  mode: none
  warm_mode: allowlist
  warm_allowlist: [crates.io]
affected:
  rules:
    - paths: ["src/**"]
      checks: db
checks:
  dependencies:
    command: cargo fetch --locked
  db:
    command: cargo test --features db
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, "src", "lib.rs"), []byte("pub fn changed() {}\n"), 0o644); err != nil {
		t.Fatalf("change lib.rs: %v", err)
	}
	return repo
}

func newWarmRunCLITestServer(t *testing.T, runPrefix string) (*httptest.Server, *[]preflightapi.CreateRunRequest) {
	t.Helper()
	var serverURL string
	var created []preflightapi.CreateRunRequest
	server := httptest.NewServer(successfulExecutionTestHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs":
			var req preflightapi.CreateRunRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode create request: %v", err)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			created = append(created, req)
			runID := runPrefix + req.CommandAlias
			writeCLITestJSON(w, http.StatusCreated, preflightapi.CreateRunResponse{
				RunID:     runID,
				UploadURL: serverURL + "/upload/" + runID,
				EventsURL: serverURL + "/events/" + runID,
				Status:    preflightapi.StatusAwaitingUpload,
			})
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/upload/"):
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte("{}\n"))
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/runs/") && strings.HasSuffix(r.URL.Path, "/upload-complete"):
			runID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/runs/"), "/upload-complete")
			writeCLITestJSON(w, http.StatusOK, preflightapi.CompleteUploadResponse{RunID: runID, Status: preflightapi.StatusQueued})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/events/"):
			writeCLITestFinishedEvent(w)
		default:
			http.NotFound(w, r)
		}
	})))
	serverURL = server.URL
	t.Cleanup(server.Close)
	return server, &created
}

func requireExplicitNoSecretCreateRequests(t *testing.T, requests []preflightapi.CreateRunRequest) {
	t.Helper()
	for _, req := range requests {
		if req.SecretsRequested == nil || *req.SecretsRequested {
			t.Fatalf("create request %s secrets_requested = %+v, want explicit false", req.CommandAlias, req.SecretsRequested)
		}
	}
}

func TestAffectedJSONActualRunWarmsDependenciesAndIncludesPlan(t *testing.T) {
	setupRustAffectedWarmCLITestRepo(t, "affected-json-warm-run")
	server, created := newWarmRunCLITestServer(t, "run_affected_json_")

	output, err := captureStdoutForCLITest(t, func() error {
		return affectedCommand([]string{"--api-url", server.URL, "--token", "pfl_warm_json", "--json"})
	})
	if err != nil && !isSuccessfulExit(err) {
		t.Fatalf("affected --json: %v\n%s", err, output)
	}
	var report executionSessionReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode execution report: %v\n%s", err, output)
	}
	if report.Plan == nil || len(report.Plan.Selected) != 1 || report.Plan.Selected[0].Name != "db" {
		t.Fatalf("execution plan = %+v, want selected db", report.Plan)
	}
	if len(report.Session.Plan.Nodes) != 2 || report.Session.Plan.Nodes[0].Phase != "warm" || report.Session.Plan.Nodes[1].Name != "db" {
		t.Fatalf("plan: %+v", report.Session.Plan)
	}
	if report.Session.Recipe.Check != "" {
		t.Fatal("affected rerun narrowed to one check")
	}
	createdRequests := *created
	if len(createdRequests) != 2 {
		t.Fatalf("created requests = %d, want warm and verify: %+v", len(createdRequests), createdRequests)
	}
	if createdRequests[0].CommandAlias != "dependencies" || createdRequests[0].NetworkMode != "allowlist" || !reflect.DeepEqual(createdRequests[0].NetworkAllowlist, []string{"crates.io"}) {
		t.Fatalf("warm create request = %+v, want dependencies with the configured allowlist warm profile", createdRequests[0])
	}
	if createdRequests[1].CommandAlias != "db" || createdRequests[1].NetworkMode != "none" {
		t.Fatalf("verify create request = %+v, want db with configured no network", createdRequests[1])
	}
}

func TestAffectedJSONRejectsWatchOnActualRun(t *testing.T) {
	err := affectedCommand([]string{"--json", "--watch"})
	if err == nil || !strings.Contains(err.Error(), "--json is not valid with --watch") {
		t.Fatalf("affected --json --watch error = %v, want watch/json rejection", err)
	}
}

func TestAgentVerifyAffectedWarmsDependenciesAutomatically(t *testing.T) {
	setupRustAffectedWarmCLITestRepo(t, "agent-affected-warm-run")
	server, created := newWarmRunCLITestServer(t, "run_agent_warm_")

	output, err := captureStdoutForCLITest(t, func() error {
		return agentVerifyCommand([]string{
			"--api-url", server.URL,
			"--token", "pfl_agent_warm",
			"--project", "airline",
			"--idempotency-key", "agent-warm",
			"--json",
		})
	})
	if err != nil {
		t.Fatalf("agent verify affected warm: %v\n%s", err, output)
	}
	var report agentVerifyReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode agent verify report: %v\n%s", err, output)
	}
	if len(report.Runs) != 2 {
		t.Fatalf("agent runs = %+v, want warm and verify runs", report.Runs)
	}
	if report.Runs[0].Check != "dependencies" || report.Runs[0].Phase != "warm" {
		t.Fatalf("agent warm run = %+v, want dependencies warm phase", report.Runs[0])
	}
	if report.Runs[1].Check != "db" || report.Runs[1].Phase != "verify" {
		t.Fatalf("agent verify run = %+v, want db verify phase", report.Runs[1])
	}
	if report.RunID != "run_agent_warm_db" || !strings.Contains(report.LogsCommand, "run_agent_warm_db") || !strings.Contains(report.ExplainCommand, "run_agent_warm_db") {
		t.Fatalf("top-level run pointers = run_id %q logs %q explain %q, want verify db run", report.RunID, report.LogsCommand, report.ExplainCommand)
	}
	if report.Summary != "Ready CI check db passed." {
		t.Fatalf("summary = %q, want db-only success summary", report.Summary)
	}
	createdRequests := *created
	if len(createdRequests) != 2 {
		t.Fatalf("created requests = %d, want warm and verify: %+v", len(createdRequests), createdRequests)
	}
	requireExplicitNoSecretCreateRequests(t, createdRequests)
	if createdRequests[0].CommandAlias != "dependencies" || createdRequests[0].NetworkMode != "allowlist" || !reflect.DeepEqual(createdRequests[0].NetworkAllowlist, []string{"crates.io"}) {
		t.Fatalf("agent warm create request = %+v, want dependencies with the configured allowlist warm profile", createdRequests[0])
	}
	if createdRequests[0].IdempotencyKey != "agent-warm:check=warm:dependencies" {
		t.Fatalf("agent warm idempotency = %q, want warm child idempotency", createdRequests[0].IdempotencyKey)
	}
	if createdRequests[1].CommandAlias != "db" || createdRequests[1].NetworkMode != "none" {
		t.Fatalf("agent verify create request = %+v, want db with configured no network", createdRequests[1])
	}
	if createdRequests[1].IdempotencyKey != "agent-warm:check=db" {
		t.Fatalf("agent verify idempotency = %q, want db child idempotency", createdRequests[1].IdempotencyKey)
	}
}

func TestAgentVerifySecuritySensitiveSkipsDependencyWarmRun(t *testing.T) {
	setupRustAffectedWarmCLITestRepo(t, "agent-security-no-warm-run")
	server, created := newWarmRunCLITestServer(t, "run_agent_security_")

	output, err := captureStdoutForCLITest(t, func() error {
		return agentVerifyCommand([]string{
			"--api-url", server.URL,
			"--token", "pfl_agent_security",
			"--project", "airline",
			"--idempotency-key", "agent-security",
			"--intent", "security-sensitive",
			"--json",
		})
	})
	if err != nil {
		t.Fatalf("agent verify security-sensitive affected: %v\n%s", err, output)
	}
	var report agentVerifyReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode agent verify report: %v\n%s", err, output)
	}
	if len(report.Runs) != 1 {
		t.Fatalf("agent security-sensitive runs = %+v, want only verify run", report.Runs)
	}
	if report.Runs[0].Check != "db" || report.Runs[0].Phase != "verify" {
		t.Fatalf("agent security-sensitive run = %+v, want db verify phase", report.Runs[0])
	}
	if report.Policy == nil || report.Policy.NetworkMode != "none" {
		t.Fatalf("agent security-sensitive policy = %+v, want no-network policy", report.Policy)
	}
	createdRequests := *created
	if len(createdRequests) != 1 {
		t.Fatalf("created requests = %d, want only verify: %+v", len(createdRequests), createdRequests)
	}
	if createdRequests[0].CommandAlias != "db" || createdRequests[0].NetworkMode != "none" {
		t.Fatalf("agent security-sensitive create request = %+v, want db with no network", createdRequests[0])
	}
	requireExplicitNoSecretCreateRequests(t, createdRequests)
	if createdRequests[0].IdempotencyKey != "agent-security:check=db" {
		t.Fatalf("agent security-sensitive idempotency = %q, want verify child idempotency", createdRequests[0].IdempotencyKey)
	}
}
