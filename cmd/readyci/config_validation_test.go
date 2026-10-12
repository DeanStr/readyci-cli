package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func TestConfigExplainReportIncludesInvalidStatus(t *testing.T) {
	projectIncludeUntracked := true
	checkIncludeUntracked := false
	cfg := config.ProjectConfig{
		Sync: config.SyncConfig{
			IncludeUntracked: &projectIncludeUntracked,
			Exclude:          []string{"project-only.txt"},
		},
		Checks: map[string]config.CheckConfig{
			"bad": {
				Changes: "workng",
				Sync: config.SyncConfig{
					IncludeUntracked: &checkIncludeUntracked,
					Exclude:          []string{"check-only.txt"},
				},
			},
		},
	}
	report := buildConfigExplainReport(cfg, ".readyci.yml")
	if report.Status != "invalid" || report.Error == "" {
		t.Fatalf("report status/error = %s/%q, want invalid", report.Status, report.Error)
	}
	if len(report.Checks) != 1 || report.Checks[0].Error == "" {
		t.Fatalf("check errors = %+v, want per-check error", report.Checks)
	}
	if report.Checks[0].IncludeUntracked {
		t.Fatalf("invalid check include_untracked = true, want effective false")
	}
	if !contains(report.Checks[0].SyncExclude, "project-only.txt") || !contains(report.Checks[0].SyncExclude, "check-only.txt") {
		t.Fatalf("invalid check sync_exclude = %v, want project and check excludes", report.Checks[0].SyncExclude)
	}
}

func TestConfigExplainReportRejectsProfileOnlyConfigWithNoGeneratedRules(t *testing.T) {
	cfg := config.ProjectConfig{
		Checks: map[string]config.CheckConfig{
			"custom": {Command: "make custom"},
		},
		Affected: config.AffectedConfig{Profiles: []string{"shell"}},
	}
	report := buildConfigExplainReport(cfg, ".readyci.yml")
	if report.Status != "invalid" || !strings.Contains(report.Error, `affected profile "shell" did not match any configured check names`) {
		t.Fatalf("report status/error = %s/%q, want invalid profile expansion", report.Status, report.Error)
	}
	if len(report.AffectedProfiles) != 1 || report.AffectedProfiles[0] != "shell" {
		t.Fatalf("affected profiles = %v, want original profile preserved", report.AffectedProfiles)
	}
}

func TestConfigExplainReportShowsDefaultTimeout(t *testing.T) {
	cfg := config.ProjectConfig{
		Checks: map[string]config.CheckConfig{
			"ci": {
				Command: "make ci",
				When: config.WhenConfig{Changes: config.WhenChangesConfig{
					Include: []string{"**/*.go"},
					Exclude: []string{"generated/**"},
				}},
			},
			"lint": {Command: "make lint"},
		},
		Affected: config.AffectedConfig{Rules: []config.AffectedRule{
			{Paths: []string{"**/*.go"}, Exclude: []string{"generated/**"}, Checks: config.CommandList{"ci"}},
		}, UnmatchedChecks: config.CommandList{"lint"}, AuditIgnore: []string{"legacy/**"}, Subsumes: map[string]config.CommandList{"ci": {"lint"}}},
	}
	report := buildConfigExplainReport(cfg, ".readyci.yml")
	if report.TimeoutSeconds != defaultRunTimeoutSeconds {
		t.Fatalf("project timeout = %d, want %d", report.TimeoutSeconds, defaultRunTimeoutSeconds)
	}
	if len(report.AffectedRules) != 1 || report.AffectedRules[0].Paths[0] != "**/*.go" || report.AffectedRules[0].Checks[0] != "ci" {
		t.Fatalf("affected rules = %+v, want go->ci rule", report.AffectedRules)
	}
	if len(report.AffectedRules[0].Exclude) != 1 || report.AffectedRules[0].Exclude[0] != "generated/**" {
		t.Fatalf("affected rule exclude = %+v, want generated/**", report.AffectedRules[0].Exclude)
	}
	if len(report.AffectedAuditIgnore) != 1 || report.AffectedAuditIgnore[0] != "legacy/**" {
		t.Fatalf("affected audit ignore = %v, want legacy/**", report.AffectedAuditIgnore)
	}
	if len(report.AffectedSubsumes["ci"]) != 1 || report.AffectedSubsumes["ci"][0] != "lint" {
		t.Fatalf("affected subsumes = %+v, want ci -> lint", report.AffectedSubsumes)
	}
	if len(report.AffectedUnmatched) != 1 || report.AffectedUnmatched[0] != "lint" {
		t.Fatalf("affected unmatched checks = %v, want lint", report.AffectedUnmatched)
	}
	if len(report.Checks) != 2 {
		t.Fatalf("checks = %d, want 2", len(report.Checks))
	}
	if report.Checks[0].TimeoutSeconds != defaultRunTimeoutSeconds {
		t.Fatalf("check timeout = %d, want %d", report.Checks[0].TimeoutSeconds, defaultRunTimeoutSeconds)
	}
	if report.Checks[0].When == nil || report.Checks[0].When.Changes == nil || strings.Join(report.Checks[0].When.Changes.Include, ",") != "**/*.go" || strings.Join(report.Checks[0].When.Changes.Exclude, ",") != "generated/**" {
		t.Fatalf("check when = %+v, want changes include/exclude", report.Checks[0].When)
	}
}

func TestConfigExplainReportShowsResourceDefaultRunnerSize(t *testing.T) {
	cfg := config.ProjectConfig{
		RunnerSize: "small",
		Resources: config.ResourceConfig{
			Default: config.ResourceDefaults{Size: "large"},
		},
		Checks: map[string]config.CheckConfig{
			"ci": {Command: "make ci"},
		},
	}
	report := buildConfigExplainReport(cfg, ".readyci.yml")
	if report.RunnerSize != "large" {
		t.Fatalf("project runner size = %q, want resources.default.size large", report.RunnerSize)
	}
}

func TestConfigExplainReportIncludesServices(t *testing.T) {
	cfg := config.ProjectConfig{
		Services: map[string]config.ServiceConfig{
			"postgres": {
				Type:    "postgres",
				Version: "16",
				Env:     map[string]string{"POSTGRES_DB": "readyci", "POSTGRES_PASSWORD": "readyci"},
				Health:  config.ServiceHealthConfig{Command: "pg_isready -U postgres", Timeout: "30s", Interval: "2s"},
			},
		},
		Checks: map[string]config.CheckConfig{
			"db": {Command: "go test ./...", Docker: boolPtr(true), Services: []string{"postgres"}},
		},
	}
	report := buildConfigExplainReport(cfg, ".readyci.yml")
	if report.Status != "ok" {
		t.Fatalf("report status/error = %s/%q, want ok", report.Status, report.Error)
	}
	if len(report.Services) != 1 || report.Services[0].Name != "postgres" || report.Services[0].Env["POSTGRES_DB"] != "readyci" {
		t.Fatalf("services = %+v, want postgres definition", report.Services)
	}
	if len(report.Checks) != 1 || len(report.Checks[0].Services) != 1 || report.Checks[0].Services[0] != "postgres" {
		t.Fatalf("check services = %+v, want postgres", report.Checks)
	}
}

func TestConfigExplainJSONReturnsNonZeroForInvalidConfig(t *testing.T) {
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
	if err := os.WriteFile(".readyci.yml", []byte(`
checks:
  bad:
    changes: workng
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	previousStdout := os.Stdout
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdout: %v", err)
	}
	os.Stdout = writeEnd
	defer func() {
		os.Stdout = previousStdout
	}()
	commandErr := configExplainCommand([]string{"--json"})
	if err := writeEnd.Close(); err != nil {
		t.Fatalf("close stdout pipe: %v", err)
	}
	output, err := io.ReadAll(readEnd)
	if err != nil {
		t.Fatalf("read stdout pipe: %v", err)
	}
	if err := readEnd.Close(); err != nil {
		t.Fatalf("close stdout read pipe: %v", err)
	}
	var exitErr exitCodeError
	if !errors.As(commandErr, &exitErr) || exitErr.code != 1 {
		t.Fatalf("config explain --json error = %#v, want exit code 1", commandErr)
	}
	if !strings.Contains(string(output), `"status": "invalid"`) {
		t.Fatalf("config explain --json output = %s, want invalid status", output)
	}
}

func TestSecretLikeSyncPathFlagsEnvPrefixes(t *testing.T) {
	for _, path := range []string{
		".env",
		".env.local",
		".envrc",
		".envrc.local",
		".npmrc.local",
		"dir/.env.production",
	} {
		if !secretLikeSyncPath(path) {
			t.Fatalf("secretLikeSyncPath(%q) = false, want true", path)
		}
	}
	for _, path := range []string{"src/env.ts", ".env.example", "dir/app.env.example", ".npmrc.example", "dir/app.npmrc.example"} {
		if secretLikeSyncPath(path) {
			t.Fatalf("secretLikeSyncPath treated %q as secret-like", path)
		}
	}
}

func TestCheckCommandRejectsNegativeRetryFlag(t *testing.T) {
	err := checkCommand([]string{"--retry", "-2", "touched"})
	if err == nil || !strings.Contains(err.Error(), "retry count must be non-negative") {
		t.Fatalf("checkCommand negative retry error = %v, want non-negative retry error", err)
	}
}

func TestNormalizeCachePathRejectsGitMetadata(t *testing.T) {
	if _, err := normalizeCachePaths([]string{".git/objects"}); err == nil {
		t.Fatal("cache path normalization accepted .git path")
	}
}

func TestParseTimeoutSecondsRejectsNegativeConfigValue(t *testing.T) {
	if _, err := parseTimeoutSeconds(-1, ""); err == nil {
		t.Fatal("parseTimeoutSeconds accepted negative timeout_seconds")
	}
	if _, err := parseTimeoutSeconds(-1, "5m"); err == nil {
		t.Fatal("parseTimeoutSeconds accepted negative timeout_seconds shadowed by timeout text")
	}
}

func TestValidateProjectConfigRejectsInvalidEnums(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.ProjectConfig
		want string
	}{
		{
			name: "top-level cache mode",
			cfg:  config.ProjectConfig{CacheMode: "warm"},
			want: "cache_mode",
		},
		{
			name: "nested cache mode",
			cfg:  config.ProjectConfig{Cache: config.CacheConfig{Mode: "warm"}},
			want: "cache.mode",
		},
		{
			name: "nested cache size",
			cfg:  config.ProjectConfig{Cache: config.CacheConfig{Size: "30XB"}},
			want: "cache.size",
		},
		{
			name: "top-level artifact path escaping workspace",
			cfg: config.ProjectConfig{
				Artifacts: config.ArtifactConfig{Paths: []string{"../outside/**"}},
			},
			want: "artifacts.paths",
		},
		{
			name: "default resource timeout",
			cfg: config.ProjectConfig{
				Resources: config.ResourceConfig{
					Default: config.ResourceDefaults{Timeout: "nope"},
				},
			},
			want: "resources.default.timeout",
		},
		{
			name: "check resource timeout",
			cfg: config.ProjectConfig{
				Resources: config.ResourceConfig{
					Checks: map[string]config.ResourceDefaults{
						"ci": {Timeout: "nope"},
					},
				},
			},
			want: "resources.checks.ci.timeout",
		},
		{
			name: "check changes",
			cfg: config.ProjectConfig{Checks: map[string]config.CheckConfig{
				"ci": {Command: "make ci", Changes: "workng"},
			}},
			want: `check "ci": invalid change mode`,
		},
		{
			name: "check cache mode",
			cfg: config.ProjectConfig{Checks: map[string]config.CheckConfig{
				"ci": {Command: "make ci", CacheMode: "warm"},
			}},
			want: `check "ci" cache_mode`,
		},
		{
			name: "check nested cache mode",
			cfg: config.ProjectConfig{Checks: map[string]config.CheckConfig{
				"ci": {Command: "make ci", Cache: config.CacheConfig{Mode: "warm"}},
			}},
			want: `check "ci" cache.mode`,
		},
		{
			name: "check nested cache size",
			cfg: config.ProjectConfig{Checks: map[string]config.CheckConfig{
				"ci": {Command: "make ci", Cache: config.CacheConfig{Size: "30XB"}},
			}},
			want: `check "ci" cache.size`,
		},
		{
			name: "check artifact path control character",
			cfg: config.ProjectConfig{Checks: map[string]config.CheckConfig{
				"ci": {Command: "make ci", ArtifactPaths: []string{"coverage\nsecret/**"}},
			}},
			want: `check "ci" artifact_paths`,
		},
		{
			name: "check reviewable artifact git control path",
			cfg: config.ProjectConfig{Checks: map[string]config.CheckConfig{
				"ci": {Command: "make ci", Outputs: config.CheckOutputsConfig{Reviewable: []string{".git/config"}}},
			}},
			want: `check "ci" outputs.reviewable`,
		},
		{
			name: "affected empty paths",
			cfg: config.ProjectConfig{
				Checks: map[string]config.CheckConfig{
					"ci": {Command: "make ci"},
				},
				Affected: config.AffectedConfig{Rules: []config.AffectedRule{
					{Checks: config.CommandList{"ci"}},
				}},
			},
			want: "affected.rules[0] must include at least one path pattern",
		},
		{
			name: "affected duplicate profile",
			cfg: config.ProjectConfig{
				Checks: map[string]config.CheckConfig{
					"ci": {Command: "make ci"},
				},
				Affected: config.AffectedConfig{Profiles: []string{"readyci", "readyci"}},
			},
			want: `affected.profiles contains duplicate profile "readyci"`,
		},
		{
			name: "affected profile without matching checks",
			cfg: config.ProjectConfig{
				Checks: map[string]config.CheckConfig{
					"custom": {Command: "make custom"},
				},
				Affected: config.AffectedConfig{Profiles: []string{"shell"}},
			},
			want: `affected profile "shell" did not match any configured check names`,
		},
		{
			name: "affected unknown check",
			cfg: config.ProjectConfig{
				Checks: map[string]config.CheckConfig{
					"ci": {Command: "make ci"},
				},
				Affected: config.AffectedConfig{Rules: []config.AffectedRule{
					{Paths: []string{"**"}, Checks: config.CommandList{"missing"}},
				}},
			},
			want: `affected.rules[0].checks references unknown check "missing"`,
		},
		{
			name: "affected unmatched check requires rules",
			cfg: config.ProjectConfig{
				Checks:   map[string]config.CheckConfig{"ci": {Command: "make ci"}},
				Affected: config.AffectedConfig{UnmatchedChecks: config.CommandList{"ci"}},
			},
			want: "affected.unmatched_checks requires affected.rules",
		},
		{
			name: "affected unmatched check must exist",
			cfg: config.ProjectConfig{
				Checks: map[string]config.CheckConfig{"ci": {Command: "make ci"}},
				Affected: config.AffectedConfig{
					Rules:           []config.AffectedRule{{Paths: []string{"**/*.go"}, Checks: config.CommandList{"ci"}}},
					UnmatchedChecks: config.CommandList{"missing"},
				},
			},
			want: `affected.unmatched_checks references unknown check "missing"`,
		},
		{
			name: "affected invalid pattern",
			cfg: config.ProjectConfig{
				Checks: map[string]config.CheckConfig{
					"ci": {Command: "make ci"},
				},
				Affected: config.AffectedConfig{Rules: []config.AffectedRule{
					{Paths: []string{"["}, Checks: config.CommandList{"ci"}},
				}},
			},
			want: "invalid path pattern",
		},
		{
			name: "affected escaping pattern",
			cfg: config.ProjectConfig{
				Checks: map[string]config.CheckConfig{
					"ci": {Command: "make ci"},
				},
				Affected: config.AffectedConfig{Rules: []config.AffectedRule{
					{Paths: []string{"docs/../secret/**"}, Checks: config.CommandList{"ci"}},
				}},
			},
			want: "must not escape the repository",
		},
		{
			name: "affected padded path",
			cfg: config.ProjectConfig{
				Checks: map[string]config.CheckConfig{
					"ci": {Command: "make ci"},
				},
				Affected: config.AffectedConfig{Rules: []config.AffectedRule{
					{Paths: []string{" docs/** "}, Checks: config.CommandList{"ci"}},
				}},
			},
			want: "whitespace-padded path pattern",
		},
		{
			name: "affected rule exclude invalid pattern",
			cfg: config.ProjectConfig{
				Checks: map[string]config.CheckConfig{
					"ci": {Command: "make ci"},
				},
				Affected: config.AffectedConfig{Rules: []config.AffectedRule{
					{Paths: []string{"docs/**"}, Exclude: []string{"["}, Checks: config.CommandList{"ci"}},
				}},
			},
			want: "affected.rules[0].exclude: invalid path pattern",
		},
		{
			name: "affected subsumes unknown child",
			cfg: config.ProjectConfig{
				Checks: map[string]config.CheckConfig{
					"web": {Command: "make web"},
				},
				Affected: config.AffectedConfig{
					Subsumes: map[string]config.CommandList{"web": {"missing"}},
				},
			},
			want: `affected.subsumes.web references unknown check "missing"`,
		},
		{
			name: "affected subsumes cycle",
			cfg: config.ProjectConfig{
				Checks: map[string]config.CheckConfig{
					"a": {Command: "make a"},
					"b": {Command: "make b"},
				},
				Affected: config.AffectedConfig{
					Subsumes: map[string]config.CommandList{"a": {"b"}, "b": {"a"}},
				},
			},
			want: "affected.subsumes contains a cycle",
		},
		{
			name: "affected audit ignore invalid pattern",
			cfg: config.ProjectConfig{
				Affected: config.AffectedConfig{AuditIgnore: []string{"["}},
			},
			want: "affected.audit_ignore[0]: invalid path pattern",
		},
		{
			name: "affected audit ignore padded pattern",
			cfg: config.ProjectConfig{
				Affected: config.AffectedConfig{AuditIgnore: []string{" legacy/** "}},
			},
			want: "affected.audit_ignore[0] contains a whitespace-padded path pattern",
		},
		{
			name: "affected padded check",
			cfg: config.ProjectConfig{
				Checks: map[string]config.CheckConfig{
					"ci": {Command: "make ci"},
				},
				Affected: config.AffectedConfig{Rules: []config.AffectedRule{
					{Paths: []string{"docs/**"}, Checks: config.CommandList{" ci "}},
				}},
			},
			want: "whitespace-padded check",
		},
		{
			name: "unsupported service type",
			cfg: config.ProjectConfig{
				Services: map[string]config.ServiceConfig{
					"redis": {Type: "redis", Version: "7", Health: config.ServiceHealthConfig{Command: "redis-cli ping"}},
				},
			},
			want: "services.redis.type must be postgres or container",
		},
		{
			name: "unsupported service version",
			cfg: config.ProjectConfig{
				Services: map[string]config.ServiceConfig{
					"postgres": {Type: "postgres", Version: "14", Health: config.ServiceHealthConfig{Command: "pg_isready"}},
				},
			},
			want: "services.postgres.version must be one of 15, 16, 17",
		},
		{
			name: "service secret env syntax",
			cfg: config.ProjectConfig{
				Services: map[string]config.ServiceConfig{
					"postgres": {
						Type:    "postgres",
						Version: "16",
						Env:     map[string]string{"POSTGRES_PASSWORD": "${{ secrets.POSTGRES_PASSWORD }}"},
						Health:  config.ServiceHealthConfig{Command: "pg_isready"},
					},
				},
			},
			want: "must be a literal non-secret value",
		},
		{
			name: "service secret env syntax without spaces",
			cfg: config.ProjectConfig{
				Services: map[string]config.ServiceConfig{
					"postgres": {
						Type:    "postgres",
						Version: "16",
						Env:     map[string]string{"POSTGRES_PASSWORD": "${{secrets.POSTGRES_PASSWORD}}"},
						Health:  config.ServiceHealthConfig{Command: "pg_isready"},
					},
				},
			},
			want: "services.postgres.env.POSTGRES_PASSWORD must be a literal non-secret value",
		},
		{
			name: "service env value control character",
			cfg: config.ProjectConfig{
				Services: map[string]config.ServiceConfig{
					"postgres": {
						Type:    "postgres",
						Version: "16",
						Env:     map[string]string{"POSTGRES_PASSWORD": "post\ngres"},
						Health:  config.ServiceHealthConfig{Command: "pg_isready"},
					},
				},
			},
			want: "services.postgres.env.POSTGRES_PASSWORD must not contain control characters",
		},
		{
			name: "service health bare number timeout",
			cfg: config.ProjectConfig{
				Services: map[string]config.ServiceConfig{
					"postgres": {
						Type:    "postgres",
						Version: "16",
						Health:  config.ServiceHealthConfig{Command: "pg_isready", Timeout: "60"},
					},
				},
			},
			want: "services.postgres.health.timeout: must be a positive duration",
		},
		{
			name: "service health command NUL byte",
			cfg: config.ProjectConfig{
				Services: map[string]config.ServiceConfig{
					"postgres": {
						Type:    "postgres",
						Version: "16",
						Health:  config.ServiceHealthConfig{Command: "pg_isready\x00echo ok"},
					},
				},
			},
			want: "services.postgres.health.command contains a NUL byte",
		},
		{
			name: "too many services",
			cfg: config.ProjectConfig{
				Services: oversizedServiceConfigs(preflightapi.MaxRunServices + 1),
				Checks: map[string]config.CheckConfig{
					"ci": {Command: "make ci", Docker: boolPtr(true), Services: serviceConfigNames(preflightapi.MaxRunServices + 1)},
				},
			},
			want: fmt.Sprintf("services must contain at most %d entries", preflightapi.MaxRunServices),
		},
		{
			name: "too many service env vars",
			cfg: config.ProjectConfig{
				Services: map[string]config.ServiceConfig{
					"postgres": {
						Type:    "postgres",
						Version: "16",
						Env:     oversizedServiceEnv(preflightapi.MaxRunServiceEnvVars + 1),
						Health:  config.ServiceHealthConfig{Command: "pg_isready"},
					},
				},
			},
			want: fmt.Sprintf(`service "postgres" env must contain at most %d entries`, preflightapi.MaxRunServiceEnvVars),
		},
		{
			name: "too many service logs",
			cfg: config.ProjectConfig{
				Services: map[string]config.ServiceConfig{
					"postgres": {
						Type:    "postgres",
						Version: "16",
						Health:  config.ServiceHealthConfig{Command: "pg_isready"},
						Logs:    oversizedServiceLogs(preflightapi.MaxRunServiceLogs + 1),
					},
				},
			},
			want: fmt.Sprintf(`service "postgres" logs must contain at most %d entries`, preflightapi.MaxRunServiceLogs),
		},
		{
			name: "service log path control character",
			cfg: config.ProjectConfig{
				Services: map[string]config.ServiceConfig{
					"postgres": {
						Type:    "postgres",
						Version: "16",
						Health:  config.ServiceHealthConfig{Command: "pg_isready"},
						Logs:    []string{"/var/log/postgres\nsecret.log"},
					},
				},
			},
			want: "services.postgres.logs[0] must not contain control characters",
		},
		{
			name: "check unknown service",
			cfg: config.ProjectConfig{
				Checks: map[string]config.CheckConfig{
					"ci": {Command: "make ci", Docker: boolPtr(true), Services: []string{"postgres"}},
				},
			},
			want: `check "ci" references unknown service "postgres"`,
		},
		{
			name: "check worker selector duplicate after trim",
			cfg: config.ProjectConfig{
				Checks: map[string]config.CheckConfig{
					"ci": {Command: "make ci", WorkerSelector: map[string]string{"role": "linux", " role ": "arm64"}},
				},
			},
			want: `worker_selector label key "role" is duplicated after trimming whitespace`,
		},
		{
			name: "check service without docker",
			cfg: config.ProjectConfig{
				Services: map[string]config.ServiceConfig{
					"postgres": {Type: "postgres", Version: "16", Health: config.ServiceHealthConfig{Command: "pg_isready"}},
				},
				Checks: map[string]config.CheckConfig{
					"ci": {Command: "make ci", Services: []string{"postgres"}},
				},
			},
			want: `check "ci" uses services and must set docker: true`,
		},
		{
			name: "project id control character",
			cfg: config.ProjectConfig{
				ProjectID: "airline\nshadow",
			},
			want: `project_id must not contain control characters`,
		},
		{
			name: "check steps duplicate name",
			cfg: config.ProjectConfig{
				Checks: map[string]config.CheckConfig{
					"ci": {Steps: []config.StepConfig{
						{Name: "test", Command: "go test ./..."},
						{Name: "test", Command: "go test ./platform/..."},
					}},
				},
			},
			want: `check "ci" steps contains duplicate step "test"`,
		},
		{
			name: "check command and steps",
			cfg: config.ProjectConfig{
				Checks: map[string]config.CheckConfig{
					"ci": {
						Command: "go test ./...",
						Steps: []config.StepConfig{{
							Name:    "test",
							Command: "go test ./...",
						}},
					},
				},
			},
			want: `check "ci" cannot set both command and steps`,
		},
		{
			name: "check step empty command",
			cfg: config.ProjectConfig{
				Checks: map[string]config.CheckConfig{
					"ci": {Steps: []config.StepConfig{{Name: "test"}}},
				},
			},
			want: `check "ci" steps[0].command is required`,
		},
		{
			name: "check command NUL byte",
			cfg: config.ProjectConfig{
				Checks: map[string]config.CheckConfig{
					"ci": {Command: "go test ./...\x00echo ok"},
				},
			},
			want: `check "ci" command contains a NUL byte`,
		},
		{
			name: "check step command NUL byte",
			cfg: config.ProjectConfig{
				Checks: map[string]config.CheckConfig{
					"ci": {Steps: []config.StepConfig{{Name: "test", Command: "go test ./...\x00echo ok"}}},
				},
			},
			want: `check "ci" steps[0].command contains a NUL byte`,
		},
		{
			name: "check step invalid timeout",
			cfg: config.ProjectConfig{
				Checks: map[string]config.CheckConfig{
					"ci": {Steps: []config.StepConfig{{Name: "test", Command: "go test ./...", Timeout: "soon"}}},
				},
			},
			want: `check "ci" steps[0].timeout`,
		},
		{
			name: "check step env value control character",
			cfg: config.ProjectConfig{
				Checks: map[string]config.CheckConfig{
					"ci": {Steps: []config.StepConfig{{
						Name:    "test",
						Command: "go test ./...",
						Env:     map[string]string{"READYCI_MODE": "test\nshadow"},
					}}},
				},
			},
			want: `check "ci" steps[0].env.READYCI_MODE must not contain control characters`,
		},
		{
			name: "check step secret env syntax",
			cfg: config.ProjectConfig{
				Checks: map[string]config.CheckConfig{
					"ci": {Steps: []config.StepConfig{{
						Name:    "test",
						Command: "go test ./...",
						Env:     map[string]string{"READYCI_TOKEN": "${{secrets.READYCI_TOKEN}}"},
					}}},
				},
			},
			want: `check "ci" steps[0].env.READYCI_TOKEN must be a literal non-secret value`,
		},
		{
			name: "when env value control character",
			cfg: config.ProjectConfig{
				Checks: map[string]config.CheckConfig{
					"ci": {
						Command: "go test ./...",
						When: config.WhenConfig{
							Env: map[string]string{"READYCI_EVENT": "push\nshadow"},
						},
					},
				},
			},
			want: `check "ci" when.env.READYCI_EVENT must not contain control characters`,
		},
		{
			name: "matrix axis value control character",
			cfg: config.ProjectConfig{
				Checks: map[string]config.CheckConfig{
					"ci": {
						Command: "go test ./...",
						Matrix: config.MatrixConfig{
							Axes: map[string][]string{"GO_VERSION": {"1.25\nshadow"}},
						},
					},
				},
			},
			want: `matrix variable GO_VERSION value must not contain control characters`,
		},
		{
			name: "matrix include value control character",
			cfg: config.ProjectConfig{
				Checks: map[string]config.CheckConfig{
					"ci": {
						Command: "go test ./...",
						Matrix: config.MatrixConfig{
							Include: []map[string]string{{"GO_VERSION": "1.25\nshadow"}},
						},
					},
				},
			},
			want: `matrix.include: matrix variable GO_VERSION value must not contain control characters`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateProjectConfig(tt.cfg)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("validateProjectConfig error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func oversizedServiceConfigs(count int) map[string]config.ServiceConfig {
	services := make(map[string]config.ServiceConfig, count)
	for index := 0; index < count; index++ {
		name := fmt.Sprintf("postgres_%d", index)
		services[name] = config.ServiceConfig{
			Type:    "postgres",
			Version: "16",
			Health:  config.ServiceHealthConfig{Command: "pg_isready"},
		}
	}
	return services
}

func serviceConfigNames(count int) []string {
	names := make([]string, 0, count)
	for index := 0; index < count; index++ {
		names = append(names, fmt.Sprintf("postgres_%d", index))
	}
	return names
}

func oversizedServiceEnv(count int) map[string]string {
	env := make(map[string]string, count)
	for index := 0; index < count; index++ {
		env[fmt.Sprintf("POSTGRES_VAR_%d", index)] = "readyci"
	}
	return env
}

func oversizedServiceLogs(count int) []string {
	logs := make([]string, 0, count)
	for index := 0; index < count; index++ {
		logs = append(logs, fmt.Sprintf("/var/log/postgres/%d.log", index))
	}
	return logs
}

func TestValidateProjectConfigAcceptsSchemaValidStepNames(t *testing.T) {
	cfg := config.ProjectConfig{
		Checks: map[string]config.CheckConfig{
			"ci": {Steps: []config.StepConfig{{Name: "_setup", Command: "go test ./..."}}},
		},
	}
	if err := validateProjectConfig(cfg); err != nil {
		t.Fatalf("validateProjectConfig rejected schema-valid step name: %v", err)
	}
}

func TestValidateProjectConfigAllowsUnmatchedChecksWithWhenChangesRules(t *testing.T) {
	cfg := config.ProjectConfig{
		Checks: map[string]config.CheckConfig{
			"go": {
				Command: "go test ./...",
				When: config.WhenConfig{Changes: config.WhenChangesConfig{
					Include: []string{"**/*.go"},
				}},
			},
			"ci": {Command: "make check"},
		},
		Affected: config.AffectedConfig{UnmatchedChecks: config.CommandList{"ci"}},
	}
	if err := validateProjectConfig(cfg); err != nil {
		t.Fatalf("validateProjectConfig rejected synthesized when.changes rule: %v", err)
	}
}

func TestProjectCacheModeUsesNestedConfigFallback(t *testing.T) {
	cfg := config.ProjectConfig{Cache: config.CacheConfig{Mode: "ephemeral"}}
	if got := projectCacheMode(cfg); got != "ephemeral" {
		t.Fatalf("projectCacheMode nested = %q, want ephemeral", got)
	}
	cfg.CacheMode = "cache-only"
	if got := projectCacheMode(cfg); got != "cache-only" {
		t.Fatalf("projectCacheMode legacy override = %q, want cache-only", got)
	}
}

func TestCheckCommandUsesProjectConfigAndChangeMode(t *testing.T) {
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
	runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
	runGitForCLITest(t, "config", "user.name", "Preflight")
	if err := os.WriteFile(filepath.Join(repo, "staged.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write staged: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, "staged.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatalf("update staged: %v", err)
	}
	runGitForCLITest(t, "add", "staged.txt")
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte(`
project_id: airline
cache:
  paths:
    - ~/.cargo
  key_files:
    - go.mod
checks:
  touched:
    command: make check-touched
    prepare:
      - scripts/bootstrap.sh
    changes: staged
    docker: true
    keep_failed: true
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var created preflightapi.CreateRunRequest
	var createBody []byte
	var uploaded []byte
	var serverURL string
	server := httptest.NewServer(successfulExecutionTestHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs":
			var err error
			createBody, err = io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read create request: %v", err)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if err := json.Unmarshal(createBody, &created); err != nil {
				t.Errorf("decode create request: %v", err)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeCLITestJSON(w, http.StatusCreated, preflightapi.CreateRunResponse{
				RunID:     "run_check",
				UploadURL: serverURL + "/upload",
				EventsURL: serverURL + "/events",
				Status:    preflightapi.StatusAwaitingUpload,
			})
		case r.Method == http.MethodPut && r.URL.Path == "/upload":
			var err error
			uploaded, err = io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read upload: %v", err)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte("{}\n"))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs/run_check/upload-complete":
			writeCLITestJSON(w, http.StatusOK, preflightapi.CompleteUploadResponse{RunID: "run_check", Status: preflightapi.StatusQueued})
		case r.Method == http.MethodGet && r.URL.Path == "/events":
			exitCode := 0
			event := preflightapi.RunEvent{Type: preflightapi.EventRunFinished, ExitCode: &exitCode}
			data, _ := json.Marshal(event)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: run_finished\n"))
			_, _ = w.Write(append(append([]byte("data: "), data...), '\n', '\n'))
		default:
			http.NotFound(w, r)
		}
	})))
	defer server.Close()
	serverURL = server.URL

	err = checkCommand([]string{"--api-url", server.URL, "--timeout", "45m", "touched"})
	var exitErr exitCodeError
	if err != nil && (!errors.As(err, &exitErr) || exitErr.code != 0) {
		t.Fatalf("checkCommand error = %v, want exit code 0", err)
	}
	if created.ProjectID != "airline" || created.CommandAlias != "touched" {
		t.Fatalf("created request = %+v", created)
	}
	if !created.Docker || created.NetworkMode != "none" {
		t.Fatalf("docker/network = %t/%s, want true/none", created.Docker, created.NetworkMode)
	}
	if created.TimeoutSeconds != 2700 {
		t.Fatalf("timeout seconds = %d, want 2700", created.TimeoutSeconds)
	}
	if bytes.Contains(createBody, []byte(`"change_mode"`)) {
		t.Fatalf("create request sent change_mode despite bundle manifest carrying it: %s", createBody)
	}
	if bytes.Contains(createBody, []byte(`"region"`)) {
		t.Fatalf("create request sent region despite no CLI/config/check region: %s", createBody)
	}
	if len(created.ArtifactPaths) != 1 || created.ArtifactPaths[0] != preflightapi.FailedWorkspaceArtifactPath {
		t.Fatalf("artifact paths = %v, want failed workspace artifact", created.ArtifactPaths)
	}
	for _, want := range []string{"scripts/bootstrap.sh", "make check-touched", "__preflight_setup_cache_paths", "__preflight_compute_cache_key go.mod", "failed-runs"} {
		if !strings.Contains(created.Command, want) {
			t.Fatalf("created command missing %q:\n%s", want, created.Command)
		}
	}
	manifest, err := syncbundle.ReadManifest(bytes.NewReader(uploaded))
	if err != nil {
		t.Fatalf("read uploaded manifest: %v", err)
	}
	if manifest.ChangeMode != syncbundle.ChangeModeStaged {
		t.Fatalf("change mode = %q, want staged", manifest.ChangeMode)
	}
	if len(manifest.ChangedFiles) != 1 || manifest.ChangedFiles[0] != "staged.txt" {
		t.Fatalf("changed files = %v, want staged.txt", manifest.ChangedFiles)
	}
}
