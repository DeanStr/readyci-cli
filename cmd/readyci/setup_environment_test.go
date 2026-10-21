package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestSetupEnvironmentPreservesRuntimeRequirementsWithoutResolvingAliases(t *testing.T) {
	root := t.TempDir()
	for path, value := range map[string]string{
		".nvmrc": "20.19.0\n", "web/.node-version": "lts/*\n", "unrelated/.nvmrc": "99.0.0\n",
		"package.json":        `{"engines":{"node":">=20"},"packageManager":"pnpm@9.15.0"}`,
		"go.mod":              "module example.test/requirements\ngo 1.25.13\ntoolchain go1.25.14\n",
		"Cargo.toml":          "[package]\nname = \"example\"\nrust-version = \"1.96\"\n",
		"rust-toolchain.toml": "[toolchain]\nchannel = \"nightly-2026-10-17\"\n",
		"global.json":         `{"sdk":{"version":"11.0.100-rc.1.26420.103"}}`,
	} {
		writeFileForRepoMapTest(t, filepath.Join(root, path), value)
	}
	requirements, warnings := setupRuntimeRequirements(root, []string{"cd web && npm test", "go test ./...", "cargo test", "dotnet test"})
	if len(warnings) > 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	encoded, _ := json.Marshal(requirements)
	for _, wanted := range []string{"20.19.0", "lts/*", "pnpm@9.15.0", "1.25.13", "go1.25.14", "1.96", "nightly-2026-10-17", "11.0.100-rc.1.26420.103"} {
		if !strings.Contains(string(encoded), wanted) {
			t.Fatalf("missing requirement %s in %s", wanted, encoded)
		}
	}
	if strings.Contains(string(encoded), "99.0.0") {
		t.Fatal("requirements included an unrelated subproject")
	}
}

func TestSetupRuntimeRequirementsDoNotExecuteOrReadOutsideSource(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(outside, ".nvmrc"), "99.0.0\n")
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Skip(err)
	}
	marker := filepath.Join(root, "executed")
	writeFileForRepoMapTest(t, filepath.Join(root, ".nvmrc"), "$(touch "+marker+")\n")
	requirements, warnings := setupRuntimeRequirements(root, []string{"cd linked && npm test", "cd ../outside && npm test"})
	if len(requirements) != 0 || len(warnings) < 1 || strings.Contains(warnings[0], marker) {
		t.Fatalf("unsafe declaration was inspected or exposed: requirements=%+v warnings=%v", requirements, warnings)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("runtime declaration executed locally")
	}
}

func TestSetupEnvironmentPreflightsResolvedPolicyFields(t *testing.T) {
	chdirCLITestRepo(t, "environment-policy")
	var observed preflightapi.WorkerCapabilityCheckRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/workers/capability" {
			t.Errorf("unexpected API request %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&observed); err != nil {
			t.Error(err)
		}
		writeCLITestJSON(w, http.StatusOK, preflightapi.WorkerCapabilityCheckResponse{
			Policy:  &preflightapi.WorkerCapabilityPolicy{Allowed: false, StatusCode: http.StatusBadRequest, Code: "request_invalid", Message: "network_mode is not allowed for this team"},
			Reasons: []string{"policy: network_mode is not allowed for this team"},
		})
	}))
	defer server.Close()
	trueValue := true
	cfg := config.ProjectConfig{
		Image: "ubuntu-24.04-ci-docker", RunnerSize: "large", TimeoutSeconds: 1234,
		Cache: config.CacheConfig{Mode: "cache-only", Size: "10GiB"}, Network: config.NetworkConfig{Mode: "default"},
		Checks: map[string]config.CheckConfig{"ci": {Command: "echo tests", Docker: &trueValue}},
	}
	report := setupReport{ConfigStatus: "ok", AuthStatus: "ok", Check: "ci", ProjectID: "environment-policy"}
	reconcileSetupEnvironment(setupOptions{APIURL: server.URL, APIToken: "pfl_environment_fixture"}, cfg, &report)
	if observed.Spec.TimeoutSeconds != 1234 || observed.Spec.CacheMode != "cache-only" || observed.Spec.CacheLaneSizeBytes != 10<<30 || observed.Spec.NetworkMode != "default" || !observed.Spec.Docker || observed.Spec.RunnerSize != "large" {
		t.Fatalf("preflight omitted resolved limits: %+v", observed.Spec)
	}
	if observed.Spec.Region != "" {
		t.Fatalf("defaulted region bypassed server project-home resolution: %q", observed.Spec.Region)
	}
	if report.Environment == nil || report.Environment.Status != "blocked" {
		t.Fatalf("policy denial was not exposed: %+v", report.Environment)
	}
	err := normalizeCLIError(validateSetupEnvironment(report.Environment))
	if err.Classification != preflightapi.ClassificationConfigInvalid || err.ExitCode != cliExitUsageOrConfig {
		t.Fatalf("policy denial lost API classification: %+v", err)
	}
}

func TestSetupEnvironmentDistinguishesUnavailableWorkers(t *testing.T) {
	environment := &setupEnvironmentReport{Status: "blocked", Blockers: []string{"image is not advertised"}, WorkerCapability: &workerCapabilityReport{Policy: &preflightapi.WorkerCapabilityPolicy{Allowed: true}}}
	err := normalizeCLIError(validateSetupEnvironment(environment))
	if err.Classification != preflightapi.ClassificationWorkerUnavailable || err.ExitCode != cliExitTemporaryUnavailable || !err.Retryable {
		t.Fatalf("worker unavailability was misclassified: %+v", err)
	}
}

func TestSetupEnvironmentUsesSessionSourceUploadAdmission(t *testing.T) {
	for _, scenario := range []struct {
		name           string
		claimable      bool
		allowed        bool
		wantBlocker    string
		classification string
		exitCode       int
	}{
		{name: "claimable worker without direct upload URL", claimable: true, allowed: true},
		{name: "worker unavailable", allowed: true, wantBlocker: "image is not advertised", classification: preflightapi.ClassificationWorkerUnavailable, exitCode: cliExitTemporaryUnavailable},
		{name: "policy denied despite claimable worker", claimable: true, wantBlocker: "runner policy denies request", classification: preflightapi.ClassificationConfigInvalid, exitCode: cliExitUsageOrConfig},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			chdirCLITestRepo(t, "environment-session-source")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/workers/capability" {
					t.Errorf("unexpected API request %s", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				policy := &preflightapi.WorkerCapabilityPolicy{Allowed: scenario.allowed}
				if !scenario.allowed {
					policy.StatusCode, policy.Code, policy.Message = http.StatusBadRequest, "request_invalid", "runner policy denies request"
				}
				writeCLITestJSON(w, http.StatusOK, preflightapi.WorkerCapabilityCheckResponse{
					Claimable: scenario.claimable, Policy: policy,
					Reasons: []string{"image is not advertised"}, UploadReasons: []string{"bundle upload URL is not advertised"},
				})
			}))
			defer server.Close()
			cfg := config.ProjectConfig{Checks: map[string]config.CheckConfig{"ci": {Command: "go test ./...", RuntimeRequirements: map[string]string{"go": "installed"}}}}
			report := setupReport{ConfigStatus: "ok", AuthStatus: "ok", Check: "ci", ProjectID: "environment-session-source"}
			reconcileSetupEnvironment(setupOptions{APIURL: server.URL, APIToken: "pfl_environment_fixture"}, cfg, &report)
			if report.Environment == nil {
				t.Fatal("setup omitted environment preflight")
			}
			err := validateSetupEnvironment(report.Environment)
			if scenario.wantBlocker == "" {
				if err != nil || report.Environment.Status == "blocked" || len(report.Environment.Blockers) != 0 {
					t.Fatalf("session object-store upload incorrectly requires a worker upload URL: report=%+v error=%v", report.Environment, err)
				}
				return
			}
			if err == nil || report.Environment.Status != "blocked" || !strings.Contains(err.Error(), scenario.wantBlocker) {
				t.Fatalf("setup omitted required admission blocker %q: report=%+v error=%v", scenario.wantBlocker, report.Environment, err)
			}
			if strings.Contains(strings.Join(report.Environment.Blockers, "; "), "bundle upload URL") {
				t.Fatalf("worker-local upload diagnostics leaked into session admission: %+v", report.Environment)
			}
			cliErr := normalizeCLIError(err)
			if cliErr.Classification != scenario.classification || cliErr.ExitCode != scenario.exitCode {
				t.Fatalf("wrong admission error: %+v", cliErr)
			}
		})
	}
}

func TestGuidedSetupEnvironmentPolicyStopsBeforeSourcePreview(t *testing.T) {
	root := chdirCLITestRepo(t, "environment-no-upload")
	t.Setenv("READYCI_CONFIG_DIR", t.TempDir())
	writeFileForRepoMapTest(t, filepath.Join(root, ".readyci.yml"), "version: 1\nproject_id: environment-no-upload\nchecks:\n  ci:\n    command: echo tests\nnetwork:\n  mode: default\n")
	capabilityCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/account":
			writeCLITestJSON(w, http.StatusOK, preflightapi.AccountUsageResponse{TeamID: "team_environment"})
		case "/v1/workers/capability":
			capabilityCalls++
			writeCLITestJSON(w, http.StatusOK, preflightapi.WorkerCapabilityCheckResponse{
				Policy:  &preflightapi.WorkerCapabilityPolicy{Allowed: false, StatusCode: http.StatusBadRequest, Code: "request_invalid", Message: "network_mode is not allowed for this team"},
				Reasons: []string{"policy: network_mode is not allowed for this team"},
			})
		default:
			t.Errorf("setup continued beyond environment preflight: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	err := guidedSetup(setupOptions{APIURL: server.URL, APIToken: "pfl_environment_fixture", ProjectID: "environment-no-upload"}, strings.NewReader(""), io.Discard, true)
	if err == nil || !strings.Contains(err.Error(), "network_mode is not allowed") || capabilityCalls != 1 {
		t.Fatalf("guided setup did not stop at authoritative preflight: calls=%d error=%v", capabilityCalls, err)
	}
}

func TestSetupRequiresEffectivePolicyBeforeUpload(t *testing.T) {
	for _, scenario := range []string{"missing policy", "unavailable endpoint", "explicit denied run"} {
		t.Run(scenario, func(t *testing.T) {
			root := chdirCLITestRepo(t, "environment-contract")
			t.Setenv("READYCI_CONFIG_DIR", t.TempDir())
			writeFileForRepoMapTest(t, filepath.Join(root, ".readyci.yml"), "version: 1\nproject_id: environment-contract\nchecks:\n  ci:\n    command: go test ./...\n    runtime_requirements: {go: installed}\n")
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				switch r.URL.Path {
				case "/v1/account":
					writeCLITestJSON(w, http.StatusOK, preflightapi.AccountUsageResponse{})
				case "/v1/workers/capability":
					if scenario == "unavailable endpoint" {
						writeCLITestJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "capability service unavailable"})
					} else if scenario == "missing policy" {
						writeCLITestJSON(w, http.StatusOK, preflightapi.WorkerCapabilityCheckResponse{Claimable: true, UploadCapable: true})
					} else {
						writeCLITestJSON(w, http.StatusOK, preflightapi.WorkerCapabilityCheckResponse{Policy: &preflightapi.WorkerCapabilityPolicy{Allowed: false, StatusCode: http.StatusBadRequest, Code: "request_invalid", Message: "runner policy denies request"}, Reasons: []string{"runner policy denies request"}})
					}
				default:
					t.Errorf("setup proceeded to source or execution despite unavailable policy: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			_, err := captureStdoutForCLITest(t, func() error {
				return setupCommand([]string{"--api-url", server.URL, "--token", "pfl_environment_fixture", "--run"})
			})
			if err == nil || requests != 2 {
				t.Fatalf("setup must stop before source preparation: requests=%d error=%v", requests, err)
			}
			if scenario == "unavailable endpoint" && !normalizeCLIError(err).Retryable {
				t.Fatalf("temporary preflight error lost retry semantics: %v", err)
			}
		})
	}
}

func TestSetupEnvironmentRequiresEveryDeclaredRuntimeToBeSealed(t *testing.T) {
	for _, test := range []struct {
		name         string
		requirements map[string]string
		blocked      bool
	}{{"other runtime only", map[string]string{"node": "24"}, true}, {"weaker Python bound", map[string]string{"python": ">=3.8"}, true}, {"stronger exact Python", map[string]string{"python": "3.12.3"}, false}} {
		t.Run(test.name, func(t *testing.T) {
			root := chdirCLITestRepo(t, "runtime-requirement-binding")
			writeFileForRepoMapTest(t, filepath.Join(root, "pyproject.toml"), "[project]\nrequires-python = '>=3.9,<4'\n")
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				writeCLITestJSON(w, http.StatusOK, preflightapi.WorkerCapabilityCheckResponse{Claimable: true, Policy: &preflightapi.WorkerCapabilityPolicy{Allowed: true}, ScratchSizeBytes: 16 << 30})
			}))
			defer server.Close()
			cfg := config.ProjectConfig{Checks: map[string]config.CheckConfig{"test": {Command: "python3 -m pytest", RuntimeRequirements: test.requirements}}}
			report := setupReport{ConfigStatus: "ok", AuthStatus: "ok", Check: "test", ProjectID: "runtime-requirement-binding"}
			reconcileSetupEnvironment(setupOptions{APIURL: server.URL, APIToken: "pfl_fixture"}, cfg, &report)
			if got := report.Environment.Status == "blocked"; got != test.blocked {
				t.Fatalf("environment%+v", report.Environment)
			}
			if test.blocked && calls != 0 {
				t.Fatal("unsealed source runtime reached remote admission")
			}
			if !test.blocked && (calls != 1 || report.Environment.RuntimeStatus != "compatible") {
				t.Fatalf("stronger supported configuration was rejected %+v", report.Environment)
			}
		})
	}
}

func TestSetupUnversionedRuntimePresenceReachesAuthoritativeMatching(t *testing.T) {
	chdirCLITestRepo(t, "runtime-presence-matching")
	var observed preflightapi.WorkerCapabilityCheckRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&observed); err != nil {
			t.Error(err)
		}
		writeCLITestJSON(w, http.StatusOK, preflightapi.WorkerCapabilityCheckResponse{Policy: &preflightapi.WorkerCapabilityPolicy{Allowed: true}, Reasons: []string{"runtime python requires installed; selected image advertises []"}})
	}))
	defer server.Close()
	cfg := config.ProjectConfig{Checks: map[string]config.CheckConfig{"test": {Command: "python3 -m pytest", RuntimeRequirements: map[string]string{"python": "installed"}}}}
	report := setupReport{ConfigStatus: "ok", AuthStatus: "ok", Check: "test", ProjectID: "runtime-presence-matching"}
	reconcileSetupEnvironment(setupOptions{APIURL: server.URL, APIToken: "pfl_fixture"}, cfg, &report)
	if observed.Spec.RuntimeRequirements["python"] != "installed" || report.Environment.Status != "blocked" || report.Environment.RuntimeStatus == "compatible" {
		t.Fatalf("presence was not enforced in admission request=%+v environment=%+v", observed, report.Environment)
	}
	if err := normalizeCLIError(validateSetupEnvironment(report.Environment)); err.ExitCode != cliExitTemporaryUnavailable {
		t.Fatalf("wrong missing-runtime result %+v", err)
	}
}

func TestSetupEnvironmentDisclosesExactApprovedGoSDKPreparation(t *testing.T) {
	root := chdirCLITestRepo(t, "go-sdk-preparation")
	writeFileForRepoMapTest(t, filepath.Join(root, "go.mod"), "module example.com/tests\ngo 1.27.0\n")
	writeFileForRepoMapTest(t, filepath.Join(root, ".go-version"), "1.27.2\n")
	var artifact preflightapi.RuntimeArchive
	for _, candidate := range preflightapi.GoRuntimeCatalog() {
		if candidate.Version == "1.27.2" {
			artifact = candidate
		}
	}
	var observed preflightapi.WorkerCapabilityCheckRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&observed); err != nil {
			t.Error(err)
		}
		writeCLITestJSON(w, http.StatusOK, preflightapi.WorkerCapabilityCheckResponse{Claimable: true, Policy: &preflightapi.WorkerCapabilityPolicy{Allowed: true}, RuntimePreparation: []preflightapi.RuntimeArchive{artifact}})
	}))
	defer server.Close()
	cfg := config.ProjectConfig{TimeoutSeconds: 300, Network: config.NetworkConfig{Mode: "allowlist", Allowlist: []string{"dl.google.com"}}, Checks: map[string]config.CheckConfig{"go": {Command: "go test ./...", RuntimeRequirements: map[string]string{"go": ">=1.27.0,1.27.2"}}}}
	report := setupReport{ConfigStatus: "ok", AuthStatus: "ok", Check: "go", ProjectID: "go-sdk-preparation"}
	reconcileSetupEnvironment(setupOptions{APIURL: server.URL, APIToken: "pfl_fixture"}, cfg, &report)
	if report.Environment.Status != "ready" || report.Environment.RuntimeStatus != "preparation_required" || len(report.Environment.RuntimePreparation) != 1 || report.Environment.RuntimePreparation[0] != artifact {
		t.Fatalf("approved SDK not disclosed %+v", report.Environment)
	}
	if observed.Spec.Command != "go test ./..." || observed.Spec.TimeoutSeconds != 300 || len(observed.Spec.NetworkAllowlist) != 1 || observed.Spec.NetworkAllowlist[0] != "dl.google.com" {
		t.Fatalf("SDK preparation changed test scope, budget or network policy %+v", observed.Spec)
	}
}
