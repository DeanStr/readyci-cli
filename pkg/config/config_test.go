package config

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

func TestLoadProjectConfigFindsNamedChecks(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".readyci.yml"), []byte(`
project_id: airline
image: ubuntu-24.04-ci
cache:
  paths:
    - ~/.cargo
  key_files:
    - Cargo.lock
services:
  postgres:
    type: postgres
    version: "16"
    env:
      POSTGRES_DB: readyci
      POSTGRES_PASSWORD: readyci
    health:
      command: pg_isready -U postgres
      timeout: 30s
      interval: 2s
    logs:
      - /var/log/postgresql/*.log
  redis:
    type: container
    image: redis:7-alpine
    ports:
      - 6379/tcp
    health:
      command: redis-cli ping
affected:
  profiles:
    - shell
  unmatched_checks:
    - touched
  audit_ignore:
    - legacy/**
  subsumes:
    web:
      - lint
  rules:
    - paths:
        - apps/web/src/**
      exclude:
        - apps/web/src/generated/**
      checks:
        - web
        - lint
  tests:
    - path: apps/web/src/app.tsx
      checks:
        - web
        - lint
checks:
  touched: make check-touched
  web: make web-check
  lint: make lint
  staged:
    command: make check-staged
    concurrency_group: db
    concurrency_limit: 1
    prepare:
      - scripts/bootstrap-web-deps.sh
    post: scripts/collect-artifacts.sh
    always:
      - scripts/cleanup.sh
    changes: staged
    cache:
      paths:
        - apps/web/.next/cache
      key_files:
        - apps/web/pnpm-lock.yaml
    sync:
      include_untracked: false
      exclude:
        - local-only.txt
    keep_failed: true
    services:
      - postgres
  stepped:
    steps:
      - name: deps
        command: npm ci
        env:
          NODE_ENV: test
      - name: test
        command: npm test
        timeout: 5m
      - name: cleanup
        command: rm -rf tmp
        always: true
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	child := filepath.Join(root, "nested")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatalf("mkdir child: %v", err)
	}

	cfg, path, err := LoadProjectConfig(child)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if path != filepath.Join(root, ".readyci.yml") {
		t.Fatalf("path = %q, want root config", path)
	}
	if cfg.ProjectID != "airline" || cfg.Image != "ubuntu-24.04-ci" {
		t.Fatalf("project defaults = %+v", cfg)
	}
	if got := cfg.Checks["touched"].Command; got != "make check-touched" {
		t.Fatalf("string check command = %q", got)
	}
	staged := cfg.Checks["staged"]
	if staged.Changes != "staged" || !staged.KeepFailed {
		t.Fatalf("staged check = %+v", staged)
	}
	if staged.ConcurrencyGroup != "db" || !staged.ConcurrencyGroupSet || staged.ConcurrencyLimit != 1 || !staged.ConcurrencyLimitSet {
		t.Fatalf("staged concurrency = %+v, want db/1", staged)
	}
	if got := staged.Cache.Paths; len(got) != 1 || got[0] != "apps/web/.next/cache" {
		t.Fatalf("staged cache paths = %v", got)
	}
	if got := cfg.Cache.KeyFiles; len(got) != 1 || got[0] != "Cargo.lock" {
		t.Fatalf("project cache key files = %v", got)
	}
	if got := cfg.Affected.Rules; len(got) != 1 || len(got[0].Paths) != 1 || got[0].Paths[0] != "apps/web/src/**" {
		t.Fatalf("affected rules paths = %+v", cfg.Affected.Rules)
	}
	if got := cfg.Affected.Rules[0].Exclude; len(got) != 1 || got[0] != "apps/web/src/generated/**" {
		t.Fatalf("affected rules exclude = %+v", got)
	}
	if got := cfg.Affected.Profiles; len(got) != 1 || got[0] != "shell" {
		t.Fatalf("affected profiles = %v", got)
	}
	if got := []string(cfg.Affected.UnmatchedChecks); len(got) != 1 || got[0] != "touched" {
		t.Fatalf("affected unmatched checks = %v", got)
	}
	if got := cfg.Affected.AuditIgnore; len(got) != 1 || got[0] != "legacy/**" {
		t.Fatalf("affected audit ignore = %v", got)
	}
	if got := []string(cfg.Affected.Subsumes["web"]); len(got) != 1 || got[0] != "lint" {
		t.Fatalf("affected subsumes = %+v", cfg.Affected.Subsumes)
	}
	if got := []string(cfg.Affected.Rules[0].Checks); len(got) != 2 || got[0] != "web" || got[1] != "lint" {
		t.Fatalf("affected rules checks = %v", got)
	}
	if got := cfg.Affected.Tests; len(got) != 1 || got[0].Path != "apps/web/src/app.tsx" {
		t.Fatalf("affected tests = %+v", got)
	}
	if got := []string(cfg.Affected.Tests[0].Checks); len(got) != 2 || got[0] != "web" || got[1] != "lint" {
		t.Fatalf("affected test checks = %v", got)
	}
	if got := staged.Cache.KeyFiles; len(got) != 1 || got[0] != "apps/web/pnpm-lock.yaml" {
		t.Fatalf("staged cache key files = %v", got)
	}
	if staged.Sync.IncludeUntracked == nil || *staged.Sync.IncludeUntracked {
		t.Fatalf("staged sync include_untracked = %v, want explicit false", staged.Sync.IncludeUntracked)
	}
	if got := staged.Sync.Exclude; len(got) != 1 || got[0] != "local-only.txt" {
		t.Fatalf("staged sync exclude = %v", got)
	}
	if got := []string(staged.Prepare); len(got) != 1 || got[0] != "scripts/bootstrap-web-deps.sh" {
		t.Fatalf("staged prepare = %v", got)
	}
	if got := []string(staged.Post); len(got) != 1 || got[0] != "scripts/collect-artifacts.sh" {
		t.Fatalf("staged post = %v", got)
	}
	if got := []string(staged.Always); len(got) != 1 || got[0] != "scripts/cleanup.sh" {
		t.Fatalf("staged always = %v", got)
	}
	postgres := cfg.Services["postgres"]
	if postgres.Type != "postgres" || postgres.Version != "16" || postgres.Env["POSTGRES_DB"] != "readyci" {
		t.Fatalf("postgres service = %+v", postgres)
	}
	if postgres.Health.Command != "pg_isready -U postgres" || postgres.Health.Timeout != "30s" || postgres.Health.Interval != "2s" {
		t.Fatalf("postgres health = %+v", postgres.Health)
	}
	if got := postgres.Logs; len(got) != 1 || got[0] != "/var/log/postgresql/*.log" {
		t.Fatalf("postgres logs = %v", got)
	}
	redis := cfg.Services["redis"]
	if redis.Type != "container" || redis.Image != "redis:7-alpine" || len(redis.Ports) != 1 || redis.Ports[0] != "6379/tcp" {
		t.Fatalf("redis service = %+v", redis)
	}
	if got := staged.Services; len(got) != 1 || got[0] != "postgres" {
		t.Fatalf("staged services = %v", got)
	}
	stepped := cfg.Checks["stepped"]
	if stepped.Command != "" || len(stepped.Steps) != 3 {
		t.Fatalf("stepped check = %+v, want steps-only check", stepped)
	}
	if stepped.Steps[0].Name != "deps" || stepped.Steps[0].Env["NODE_ENV"] != "test" || !stepped.Steps[2].Always {
		t.Fatalf("stepped steps = %+v, want env and always", stepped.Steps)
	}
}

func TestLoadProjectConfigPreservesExplicitZeroCheckRetries(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".readyci.yml"), []byte(`
retries: 2
checks:
  no-retry:
    command: go test ./...
    retries: 0
  inherited:
    command: go test ./...
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, _, err := LoadProjectConfig(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	noRetry := cfg.Checks["no-retry"]
	if !noRetry.RetriesSet || noRetry.Retries != 0 {
		t.Fatalf("explicit zero retries = %+v, want set zero", noRetry)
	}
	inherited := cfg.Checks["inherited"]
	if inherited.RetriesSet {
		t.Fatalf("missing retries marked as set: %+v", inherited)
	}
}

func TestParseProjectConfigSupportsStructuredMatrix(t *testing.T) {
	cfg, err := ParseProjectConfig([]byte(`
checks:
  smoke:
    command: make smoke
    matrix:
      axes:
        OS: [linux]
      include:
        - OS: macos
          ARCH: arm64
      exclude:
        - OS: linux
          ARCH: s390x
      fail_fast: true
      max_parallel: 2
`), ".readyci.yml")
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	matrix := cfg.Checks["smoke"].Matrix
	if matrix.Axes["OS"][0] != "linux" || matrix.Include[0]["ARCH"] != "arm64" || matrix.Exclude[0]["ARCH"] != "s390x" || !matrix.FailFast || matrix.MaxParallel != 2 {
		t.Fatalf("matrix = %+v, want structured matrix fields", matrix)
	}
}

func TestParseProjectConfigKeepsLegacyReservedMatrixAxisNames(t *testing.T) {
	cfg, err := ParseProjectConfig([]byte(`
checks:
  smoke:
    command: make smoke
    matrix:
      axes: [legacy-axis]
      include: [legacy]
      exclude: [old]
      fail_fast: ["yes"]
      max_parallel: ["2"]
`), ".readyci.yml")
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	matrix := cfg.Checks["smoke"].Matrix
	for key, want := range map[string]string{
		"axes":         "legacy-axis",
		"include":      "legacy",
		"exclude":      "old",
		"fail_fast":    "yes",
		"max_parallel": "2",
	} {
		if matrix.Axes[key][0] != want {
			t.Fatalf("matrix axis %s = %+v, want %q in %+v", key, matrix.Axes[key], want, matrix)
		}
	}
	if len(matrix.Include) != 0 || len(matrix.Exclude) != 0 || matrix.FailFast || matrix.MaxParallel != 0 {
		t.Fatalf("matrix = %+v, want legacy reserved axes", matrix)
	}
}

func TestReadyCISchemaIncludesRetriesAndMatrix(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "schema", "readyci.schema.json"))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("decode schema: %v", err)
	}
	properties := schema["properties"].(map[string]any)
	if _, ok := properties["retries"]; !ok {
		t.Fatal("schema is missing project-level retries")
	}
	if _, ok := properties["concurrency_group"]; !ok {
		t.Fatal("schema is missing project-level concurrency_group")
	}
	if _, ok := properties["concurrency_limit"]; !ok {
		t.Fatal("schema is missing project-level concurrency_limit")
	}
	if _, ok := properties["affected"]; !ok {
		t.Fatal("schema is missing project-level affected rules")
	}
	if _, ok := properties["services"]; !ok {
		t.Fatal("schema is missing project-level services")
	}
	defs := schema["$defs"].(map[string]any)
	if _, ok := defs["service"]; !ok {
		t.Fatal("schema is missing service definition")
	}
	if _, ok := defs["affectedRule"]; !ok {
		t.Fatal("schema is missing affectedRule definition")
	}
	if _, ok := defs["affectedTest"]; !ok {
		t.Fatal("schema is missing affectedTest definition")
	}
	affected := properties["affected"].(map[string]any)
	affectedProperties := affected["properties"].(map[string]any)
	for _, key := range []string{"profiles", "rules", "unmatched_checks", "subsumes", "tests", "audit_ignore"} {
		if _, ok := affectedProperties[key]; !ok {
			t.Fatalf("schema is missing affected.%s", key)
		}
	}
	profiles := affectedProperties["profiles"].(map[string]any)
	profileItems := profiles["items"].(map[string]any)
	profileEnums := profileItems["enum"].([]any)
	foundShell := false
	for _, value := range profileEnums {
		if value == "shell" {
			foundShell = true
			break
		}
	}
	if !foundShell {
		t.Fatal("schema affected.profiles enum is missing shell")
	}
	affectedRule := defs["affectedRule"].(map[string]any)
	affectedRuleProperties := affectedRule["properties"].(map[string]any)
	if _, ok := affectedRuleProperties["exclude"]; !ok {
		t.Fatal("schema is missing affected.rules[].exclude")
	}
	check := defs["check"].(map[string]any)
	checkVariants := check["oneOf"].([]any)
	checkObject := checkVariants[1].(map[string]any)
	checkProperties := checkObject["properties"].(map[string]any)
	for _, key := range []string{"retries", "concurrency_group", "concurrency_limit", "matrix", "sync", "prepare", "post", "always", "services"} {
		if _, ok := checkProperties[key]; !ok {
			t.Fatalf("schema is missing check-level %s", key)
		}
	}
	cache := properties["cache"].(map[string]any)
	cacheProperties := cache["properties"].(map[string]any)
	for _, key := range []string{"size", "key_files"} {
		if _, ok := cacheProperties[key]; !ok {
			t.Fatalf("schema is missing project-level cache.%s", key)
		}
	}
	checkCache := checkProperties["cache"].(map[string]any)
	checkCacheProperties := checkCache["properties"].(map[string]any)
	for _, key := range []string{"size", "key_files"} {
		if _, ok := checkCacheProperties[key]; !ok {
			t.Fatalf("schema is missing check-level cache.%s", key)
		}
	}
}

func TestReadyCISchemaServiceValidationMatchesConfigRules(t *testing.T) {
	schema := compileReadyCISchema(t)
	health := map[string]any{"command": "true"}
	validConfigs := []map[string]any{
		{"services": map[string]any{"postgres": map[string]any{"type": "postgres", "health": health}}},
		{"services": map[string]any{"redis": map[string]any{"type": "container", "image": "redis:7-alpine", "ports": []any{"6379/tcp"}, "health": health}}},
		{"services": map[string]any{"redis": map[string]any{"image": "redis:7-alpine", "ports": []any{"6379/tcp"}, "health": health}}},
	}
	for _, cfg := range validConfigs {
		if err := schema.Validate(cfg); err != nil {
			t.Fatalf("schema rejected valid service config %#v: %v", cfg, err)
		}
	}

	invalidConfigs := []map[string]any{
		{"services": map[string]any{"svc": map[string]any{"health": health}}},
		{"services": map[string]any{"svc": map[string]any{"type": "container", "health": health}}},
		{"services": map[string]any{"svc": map[string]any{"type": "container", "image": "redis:7-alpine", "version": "7", "health": health}}},
		{"services": map[string]any{"svc": map[string]any{"type": "container", "image": "https://example.invalid/redis", "health": health}}},
		{"services": map[string]any{"svc": map[string]any{"type": "container", "image": "Redis:7-alpine", "health": health}}},
		{"services": map[string]any{"svc": map[string]any{"type": "container", "image": "redis:", "health": health}}},
		{"services": map[string]any{"svc": map[string]any{"type": "container", "image": "redis@", "health": health}}},
		{"services": map[string]any{"svc": map[string]any{"type": "container", "image": "redis@sha256:", "health": health}}},
		{"services": map[string]any{"svc": map[string]any{"type": "container", "image": "redis:7:extra", "health": health}}},
		{"services": map[string]any{"svc": map[string]any{"type": "postgres", "image": "postgres:16", "health": health}}},
		{"services": map[string]any{"svc": map[string]any{"type": "postgres", "ports": []any{"5432/tcp"}, "health": health}}},
		{"services": map[string]any{"svc": map[string]any{"type": "postgres", "health": health, "logs": []any{" \t "}}}},
		{"services": map[string]any{"svc": map[string]any{"type": "postgres", "health": health, "logs": []any{"/var/log/postgres\nsecret.log"}}}},
	}
	for _, cfg := range invalidConfigs {
		if err := schema.Validate(cfg); err == nil {
			t.Fatalf("schema accepted invalid service config %#v", cfg)
		}
	}
}

func TestReadyCISchemaArtifactPatternsMatchConfigRules(t *testing.T) {
	schema := compileReadyCISchema(t)
	validConfigs := []map[string]any{
		{"artifacts": map[string]any{"paths": []any{"reports/**", "./coverage.out", ".readyci/../.readyci/failed-workspace.tar.gz"}}},
		{"checks": map[string]any{"ci": map[string]any{"command": "go test ./...", "artifact_paths": []any{"dist/**"}}}},
		{"checks": map[string]any{"ci": map[string]any{"command": "go test ./...", "outputs": map[string]any{"reviewable": []any{"reports/junit.xml"}}}}},
	}
	for _, cfg := range validConfigs {
		if err := schema.Validate(cfg); err != nil {
			t.Fatalf("schema rejected valid artifact config %#v: %v", cfg, err)
		}
	}

	invalidPatterns := []string{
		"../outside/**",
		"./../outside/**",
		"/tmp/report.txt",
		`./C:\tmp\report.txt`,
		`C:\tmp\report.txt`,
		`\\server\share\report.txt`,
		".git/config",
		"./.git/config",
		"reports/.Git/config",
		"reports/../report.txt",
		"reports/../.readyci/failed-workspace.tar.gz",
		"reports/\nsecret.txt",
	}
	for _, pattern := range invalidPatterns {
		for _, cfg := range []map[string]any{
			{"artifacts": map[string]any{"paths": []any{pattern}}},
			{"checks": map[string]any{"ci": map[string]any{"command": "go test ./...", "artifact_paths": []any{pattern}}}},
			{"checks": map[string]any{"ci": map[string]any{"command": "go test ./...", "outputs": map[string]any{"reviewable": []any{pattern}}}}},
		} {
			if err := schema.Validate(cfg); err == nil {
				t.Fatalf("schema accepted invalid artifact pattern %q in config %#v", pattern, cfg)
			}
		}
	}
}

func compileReadyCISchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	schemaPath, err := filepath.Abs(filepath.Join("..", "..", "schema", "readyci.schema.json"))
	if err != nil {
		t.Fatalf("resolve schema path: %v", err)
	}
	schemaURL := (&url.URL{Scheme: "file", Path: filepath.ToSlash(schemaPath)}).String()
	schema, err := jsonschema.Compile(schemaURL)
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}
	return schema
}

func TestLoadProjectConfigUsesDefaultsWhenMissing(t *testing.T) {
	cfg, path, err := LoadProjectConfig(t.TempDir())
	if err != nil {
		t.Fatalf("load missing config: %v", err)
	}
	if path != "" {
		t.Fatalf("path = %q, want empty", path)
	}
	if cfg.Image != "ubuntu-24.04-ci" || cfg.RunnerSize != "standard" || cfg.CacheMode != "cache-only" {
		t.Fatalf("defaults = %+v", cfg)
	}
}

func TestLoadProjectConfigPrefersReadyCIOverLegacyPreflight(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".preflight.yml"), []byte("project_id: legacy\n"), 0o644); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".readyci.yml"), []byte("project_id: ready\n"), 0o644); err != nil {
		t.Fatalf("write ready config: %v", err)
	}
	cfg, path, err := LoadProjectConfig(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if path != filepath.Join(root, ".readyci.yml") {
		t.Fatalf("path = %q, want .readyci.yml", path)
	}
	if cfg.ProjectID != "ready" {
		t.Fatalf("project id = %q, want ready", cfg.ProjectID)
	}
}

func TestLoadProjectConfigIgnoresLegacyPreflightConfig(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".preflight.yml"), []byte("project_id: legacy\n"), 0o644); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}
	cfg, path, err := LoadProjectConfig(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if path != "" {
		t.Fatalf("path = %q, want no config", path)
	}
	if cfg.ProjectID != "" {
		t.Fatalf("project id = %q, want default", cfg.ProjectID)
	}
}

func TestLoadProjectConfigUsesDefaultsForEmptyFile(t *testing.T) {
	for _, value := range []string{"", "# placeholder\n"} {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, ".readyci.yml"), []byte(value), 0o644); err != nil {
			t.Fatalf("write config: %v", err)
		}
		cfg, path, err := LoadProjectConfig(root)
		if err != nil {
			t.Fatalf("load config %q: %v", value, err)
		}
		if path != filepath.Join(root, ".readyci.yml") {
			t.Fatalf("path = %q, want config path", path)
		}
		if cfg.Image != "ubuntu-24.04-ci" || cfg.RunnerSize != "standard" || cfg.CacheMode != "cache-only" {
			t.Fatalf("defaults = %+v", cfg)
		}
	}
}

func TestLoadProjectConfigRejectsUnsupportedVersion(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".readyci.yml"), []byte("version: 99\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, _, err := LoadProjectConfig(root); err == nil {
		t.Fatal("LoadProjectConfig accepted unsupported version")
	}
}

func TestLoadProjectConfigRejectsUnsupportedGitignoreMode(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".readyci.yml"), []byte(`
sync:
  respect_gitignore: false
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, _, err := LoadProjectConfig(root); err == nil {
		t.Fatal("LoadProjectConfig accepted unsupported respect_gitignore=false")
	}
}

func TestLoadProjectConfigRejectsCommandsAlias(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".readyci.yml"), []byte(`
commands:
  ci:
    command: make legacy-ci
    sync:
      respect_gitignore: false
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, _, err := LoadProjectConfig(root); err == nil {
		t.Fatal("LoadProjectConfig accepted removed commands alias")
	}
}

func TestSyncExclusionsFailFastOnMalformedOrUnsupportedRules(t *testing.T) {
	for _, document := range []string{
		"version: 1\nsync:\n  exclude: ['[broken']\n",
		"version: 1\nchecks:\n  test:\n    command: echo ok\n    sync:\n      exclude: ['!secrets/**']\n",
	} {
		if _, err := ParseProjectConfig([]byte(document), "fixture.yml"); err == nil {
			t.Fatal("invalid exclusion config was accepted")
		}
	}
}
