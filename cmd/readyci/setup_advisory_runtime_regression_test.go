package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

const setupAdvisoryNodeTest = `{"name":"fixture","scripts":{"test":"vitest run"}}`

func setupAdvisoryWorkflow(steps string) string {
	return "on: push\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v4\n" + steps
}

// Each repository shape is a common, valid project whose runtime declaration
// setup cannot translate. Setup must keep proposing the command it proposed
// before declarations were inspected, without sealing the declaration, and
// disclose the skipped declaration as a review note.
func TestSetupUntranslatableRuntimeDeclarationsAreReviewNotes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		files   map[string]string
		check   string
		command string
		runtime string
		note    string
		args    []string
	}{
		{
			name:    "Makefile check delegating to Gradle",
			files:   map[string]string{"Makefile": "check:\n\t./gradlew check\n", "gradlew": "#!/bin/sh\nexit 0\n", "build.gradle": ""},
			check:   "ci",
			command: "make -f Makefile -- check",
			runtime: "java",
		},
		{
			name:    "Maven workflow",
			files:   map[string]string{"pom.xml": "<project/>\n", ".github/workflows/ci.yml": setupAdvisoryWorkflow("      - run: mvn -B test\n")},
			check:   "test",
			command: "bash -e -c 'mvn -B test'",
			runtime: "java",
			note:    "java (used by the selected command) is not emitted as a java runtime requirement",
		},
		{
			name:    "Ruby workflow",
			files:   map[string]string{"Gemfile": "source 'https://rubygems.org'\n", ".ruby-version": "3.3.0\n", ".github/workflows/ci.yml": setupAdvisoryWorkflow("      - run: bundle exec rake test\n")},
			check:   "test",
			command: "bash -e -c 'bundle exec rake test'",
			runtime: "ruby",
			note:    `.ruby-version \"3.3.0\" is not emitted as a ruby runtime requirement (ruby is not a supported runtime requirement family)`,
			// Rake is not a recognized test entrypoint, so the job is selected explicitly.
			args: []string{"--candidate", "github-actions:.github/workflows/ci.yml:test"},
		},
		{
			name:    ".NET workflow",
			files:   map[string]string{"app.sln": "", ".github/workflows/ci.yml": setupAdvisoryWorkflow("      - run: dotnet test\n")},
			check:   "test",
			command: "bash -e -c 'dotnet test'",
			runtime: "dotnet",
			note:    "dotnet (used by the selected command) is not emitted as a dotnet runtime requirement",
		},
		{
			name:    "npm script running Bun",
			files:   map[string]string{"package.json": `{"name":"fixture","scripts":{"test":"bun test"}}`, "package-lock.json": "{}\n"},
			check:   "node",
			command: "npm ci && npm run test",
			runtime: "bun",
			note:    "bun (used by the selected command) is not emitted as a bun runtime requirement",
		},
		{
			name:    "stable Rust channel",
			files:   map[string]string{"rust-toolchain.toml": "[toolchain]\nchannel = \"stable\"\n", "Cargo.toml": "[package]\nname = \"fixture\"\nversion = \"0.1.0\"\nedition = \"2021\"\n", "src/main.rs": "fn main() {}\n"},
			check:   "rust",
			command: "cargo test --workspace --all-targets",
			runtime: "rust",
			note:    `rust-toolchain.toml (channel) \"stable\" is not emitted as a rust runtime requirement (moving, named or variant selectors`,
		},
		{
			name:    "nvmrc lts/*",
			files:   map[string]string{"package.json": setupAdvisoryNodeTest, "package-lock.json": "{}\n", ".nvmrc": "lts/*\n"},
			check:   "node",
			command: "npm ci && npm run test",
			runtime: "node",
			note:    `.nvmrc \"lts/*\" is not emitted as a node runtime requirement`,
		},
		{
			name:    "nvmrc lts/iron",
			files:   map[string]string{"package.json": setupAdvisoryNodeTest, "package-lock.json": "{}\n", ".nvmrc": "lts/iron\n"},
			check:   "node",
			command: "npm ci && npm run test",
			runtime: "node",
			note:    `.nvmrc \"lts/iron\" is not emitted as a node runtime requirement`,
		},
		{
			name:    "nvmrc node",
			files:   map[string]string{"package.json": setupAdvisoryNodeTest, "package-lock.json": "{}\n", ".nvmrc": "node\n"},
			check:   "node",
			command: "npm ci && npm run test",
			runtime: "node",
			note:    `.nvmrc \"node\" is not emitted as a node runtime requirement`,
		},
		{
			name:    "any Node engine",
			files:   map[string]string{"package.json": `{"name":"fixture","engines":{"node":"*"},"scripts":{"test":"vitest run"}}`, "package-lock.json": "{}\n"},
			check:   "node",
			command: "npm ci && npm run test",
			runtime: "node",
			note:    `package.json (engines.node) \"*\" is not emitted as a node runtime requirement (unsupported version syntax`,
		},
		{
			name:    "compatible-release Python",
			files:   map[string]string{"pyproject.toml": "[project]\nname = \"fixture\"\nversion = \"0\"\nrequires-python = \"~=3.9\"\n", ".github/workflows/ci.yml": setupAdvisoryWorkflow("      - run: python -m pip install -e . && python -m pytest\n")},
			check:   "test",
			command: "bash -e -c 'python -m pip install -e . && python -m pytest'",
			runtime: "python",
			note:    `pyproject.toml (requires-python) \"~=3.9\" is not emitted as a python runtime requirement (unsupported version syntax`,
		},
		{
			name:    "excluded Python series",
			files:   map[string]string{"pyproject.toml": "[project]\nname = \"fixture\"\nversion = \"0\"\nrequires-python = \"!=3.8.*\"\n", ".github/workflows/ci.yml": setupAdvisoryWorkflow("      - run: python -m pip install -e . && python -m pytest\n")},
			check:   "test",
			command: "bash -e -c 'python -m pip install -e . && python -m pytest'",
			runtime: "python",
			note:    `pyproject.toml (requires-python) \"!=3.8.*\" is not emitted as a python runtime requirement (unsupported version syntax`,
		},
		{
			name:    "Corepack pin without an approved archive",
			files:   map[string]string{"package.json": `{"name":"fixture","packageManager":"pnpm@9.15.0+sha512.` + strings.Repeat("a", 128) + `","scripts":{"test":"vitest run"}}`, "pnpm-lock.yaml": "lockfileVersion: '9.0'\n"},
			check:   "node",
			command: "pnpm install --frozen-lockfile && pnpm test",
			runtime: "pnpm",
			note:    "no approved Ready CI pnpm archive matches this integrity pin",
		},
		{
			name:    "setup-node lts/*",
			files:   map[string]string{"package.json": setupAdvisoryNodeTest, "package-lock.json": "{}\n", ".github/workflows/ci.yml": setupAdvisoryWorkflow("      - uses: actions/setup-node@v4\n        with:\n          node-version: lts/*\n      - run: npm ci\n      - run: npm test\n")},
			check:   "test",
			command: "bash -e -c 'npm test'",
			runtime: "node",
			note:    `actions/setup-node node-version \"lts/*\" is not emitted as a node runtime requirement (moving, named or variant selectors are not resolved by setup); the runner image's installed node is used. Review it, or pin it with readyci setup --runtime node=VERSION.`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := chdirCLITestRepo(t, "advisory-runtime")
			for path, content := range tc.files {
				writeFileForRepoMapTest(t, path, content)
			}
			if tc.files["gradlew"] != "" {
				if err := os.Chmod("gradlew", 0o755); err != nil {
					t.Fatal(err)
				}
			}
			printed, err := captureStdoutForCLITest(t, func() error {
				return setupCommand(append([]string{"--print", "--api-url", "http://127.0.0.1:9"}, tc.args...))
			})
			if err != nil {
				t.Fatalf("runtime declaration failed setup in %s: %v", repo, err)
			}
			cfg, err := config.ParseProjectConfig([]byte(printed), "advisory runtime")
			if err != nil {
				t.Fatal(err)
			}
			check, ok := cfg.Checks[tc.check]
			if !ok {
				t.Fatalf("check %q missing:\n%s", tc.check, printed)
			}
			commands := []string{check.Command}
			for _, step := range check.Steps {
				commands = append(commands, step.Command)
			}
			if !containsString(commands, tc.command) {
				t.Fatalf("setup changed the proposed command: %v\n%s", commands, printed)
			}
			if got := check.RuntimeRequirements[tc.runtime]; got != "" && got != "installed" {
				t.Fatalf("untranslatable %s declaration was sealed as %q:\n%s", tc.runtime, got, printed)
			}
			if tc.note != "" && (!strings.Contains(printed, "# - \"Runtime review: ") || !strings.Contains(printed, tc.note)) {
				t.Fatalf("missing review note %q:\n%s", tc.note, printed)
			}
		})
	}
}

// The preview JSON carries the same review notes through the environment
// warnings and structured unresolved items. A declaration never blocks the
// environment, but it keeps a compatible runner from being reported ready.
func TestSetupPreviewReportsRuntimeReviewNotesWithoutBlocking(t *testing.T) {
	chdirCLITestRepo(t, "advisory-preview")
	t.Setenv("READYCI_CONFIG_DIR", t.TempDir())
	writeFileForRepoMapTest(t, "package.json", setupAdvisoryNodeTest)
	writeFileForRepoMapTest(t, "package-lock.json", "{}\n")
	writeFileForRepoMapTest(t, ".nvmrc", "lts/*\n")
	server := setupAdvisoryCapabilityServer(t)
	output, err := captureStdoutForCLITest(t, func() error {
		return setupCommand([]string{"--preview", "--json", "--api-url", server.URL, "--token", "pfl_advisory_fixture"})
	})
	if err != nil {
		t.Fatal(err)
	}
	var report setupReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatal(err)
	}
	if report.Environment == nil || report.Environment.Status != "review_required" || report.Environment.RuntimeStatus != "compatible" || len(report.Environment.Blockers) != 0 ||
		!strings.Contains(strings.Join(setupUnresolvedNotes(report.Environment.Unresolved), "\n"), `Runtime review: .nvmrc "lts/*" is not emitted as a node runtime requirement`) ||
		setupWarningsRepeatUnresolvedNotes(report.Environment) ||
		!strings.Contains(report.ProposedConfig, `# - "Runtime review: .nvmrc`) {
		t.Fatalf("preview lost the review note, blocked, or claimed readiness: %s", output)
	}
	want := setupUnresolvedItem{Kind: setupUnresolvedRuntimeDeclarationNotApplied, Check: "node", Subject: "node", Source: ".nvmrc", Declared: "lts/*"}
	if items := report.Environment.Unresolved; len(items) != 1 || items[0].Kind != want.Kind || items[0].Check != want.Check || items[0].Subject != want.Subject || items[0].Source != want.Source || items[0].Declared != want.Declared {
		t.Fatalf("unresolved item = %+v, want %+v", items, want)
	}
}

// A hand-written configuration that readyci check accepts must not be blocked
// by setup's environment reconciliation; repository declarations it does not
// seal are review notes.
func TestSetupRunAcceptsHandWrittenConfigurationWithUnsealedDeclarations(t *testing.T) {
	chdirCLITestRepo(t, "advisory-handwritten")
	t.Setenv("READYCI_CONFIG_DIR", t.TempDir())
	writeFileForRepoMapTest(t, "pyproject.toml", "[project]\nname = \"fixture\"\nversion = \"0\"\nrequires-python = \">=3.10\"\n")
	writeFileForRepoMapTest(t, "rust-toolchain.toml", "[toolchain]\nchannel = \"stable\"\n")
	writeFileForRepoMapTest(t, ".readyci.yml", "version: 1\nproject_id: advisory\nchecks:\n  test:\n    command: python3 -m pytest && cargo test\n")
	cfg, path, err := config.LoadProjectConfig(".")
	if err != nil || validateProjectConfigForConfigPath(cfg, path) != nil {
		t.Fatalf("readyci check rejects the fixture configuration: %v", err)
	}
	server := setupAdvisoryCapabilityServer(t)
	report, err := buildSetupReport(setupOptions{APIURL: server.URL, APIToken: "pfl_advisory_fixture", Detect: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.ConfigStatus != "ok" || report.Environment.Status != "review_required" || len(report.Environment.Blockers) != 0 || validateSetupEnvironment(report.Environment) != nil {
		t.Fatalf("hand-written configuration was blocked or reported ready: %+v", report.Environment)
	}
	if kinds := setupUnresolvedKinds(report.Environment.Unresolved); len(kinds) != 1 || kinds[0] != setupUnresolvedRuntimeDeclarationNotApplied || len(report.Environment.Unresolved) != 2 {
		t.Fatalf("unsealed declarations are not unresolved items: %+v", report.Environment.Unresolved)
	}
	notes := strings.Join(setupUnresolvedNotes(report.Environment.Unresolved), "\n")
	if setupWarningsRepeatUnresolvedNotes(report.Environment) {
		t.Fatalf("warnings repeat unresolved notes: %v", report.Environment.Warnings)
	}
	if !strings.Contains(notes, `repository runtime declarations narrow python to ">=3.10", but checks.test.runtime_requirements does not require python`) || !strings.Contains(notes, `rust-toolchain.toml (channel) "stable"`) {
		t.Fatalf("unsealed declarations were not reviewed: %v", report.Environment.Unresolved)
	}
}

func setupAdvisoryCapabilityServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/account":
			writeCLITestJSON(w, http.StatusOK, preflightapi.AccountUsageResponse{TeamID: "team_advisory"})
		case "/v1/workers/capability":
			writeCLITestJSON(w, http.StatusOK, preflightapi.WorkerCapabilityCheckResponse{Claimable: true, UploadCapable: true, Policy: &preflightapi.WorkerCapabilityPolicy{Allowed: true}})
		default:
			t.Errorf("unexpected setup request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}
