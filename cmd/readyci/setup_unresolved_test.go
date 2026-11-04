package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

type setupUnresolvedShape struct {
	name     string
	files    map[string]string
	args     []string
	check    string
	kinds    []string
	subjects []string
	declared string
}

// Each shape is a common repository whose selected command setup can generate
// and the runner can accept, but whose intended environment static inspection
// cannot confirm. The plain Go repository is the control: nothing is left
// unresolved.
var setupUnresolvedShapes = []setupUnresolvedShape{
	{
		name:  "Makefile check delegating to Gradle",
		files: map[string]string{"Makefile": "check:\n\t./gradlew check\n", "gradlew": "#!/bin/sh\nexit 0\n", "build.gradle": ""},
		check: "ci", kinds: []string{setupUnresolvedOpaqueEntrypoint}, subjects: []string{"check"},
	},
	{
		// Java is a supported runtime family; the image's JDK is required.
		name:  "Maven workflow",
		files: map[string]string{"pom.xml": "<project/>\n", ".github/workflows/ci.yml": setupAdvisoryWorkflow("      - run: mvn -B test\n")},
		check: "test",
	},
	{
		name:  "Ruby workflow with a version file",
		files: map[string]string{"Gemfile": "source 'https://rubygems.org'\n", ".ruby-version": "3.3.0\n", ".github/workflows/ci.yml": setupAdvisoryWorkflow("      - run: bundle exec rake test\n")},
		args:  []string{"--candidate", "github-actions:.github/workflows/ci.yml:test"},
		check: "test", kinds: []string{setupUnresolvedRuntimeFamilyUnsupported}, subjects: []string{"ruby"}, declared: "3.3.0",
	},
	{
		name:  "stable Rust channel",
		files: map[string]string{"rust-toolchain.toml": "[toolchain]\nchannel = \"stable\"\n", "Cargo.toml": "[package]\nname = \"fixture\"\nversion = \"0.1.0\"\nedition = \"2021\"\n", "src/main.rs": "fn main() {}\n"},
		check: "rust", kinds: []string{setupUnresolvedRuntimeDeclarationNotApplied}, subjects: []string{"rust"}, declared: "stable",
	},
	{
		name:  "nvmrc lts/*",
		files: map[string]string{"package.json": setupAdvisoryNodeTest, "package-lock.json": "{}\n", ".nvmrc": "lts/*\n"},
		check: "node", kinds: []string{setupUnresolvedRuntimeDeclarationNotApplied}, subjects: []string{"node"}, declared: "lts/*",
	},
	{
		name:  "setup-node lts/* workflow",
		files: map[string]string{"package.json": setupAdvisoryNodeTest, "package-lock.json": "{}\n", ".github/workflows/ci.yml": setupAdvisoryWorkflow("      - uses: actions/setup-node@v4\n        with:\n          node-version: lts/*\n      - run: npm ci\n      - run: npm test\n")},
		check: "test", kinds: []string{setupUnresolvedRuntimeDeclarationNotApplied}, subjects: []string{"node"}, declared: "lts/*",
	},
	{
		name:  "Python workflow without dependency installation",
		files: map[string]string{"pyproject.toml": "[project]\nname = \"fixture\"\nversion = \"0\"\nrequires-python = \"~=3.9\"\n", ".github/workflows/ci.yml": setupAdvisoryWorkflow("      - run: python -m pytest\n")},
		check: "test", kinds: []string{setupUnresolvedDependencyPreparationUnknown, setupUnresolvedRuntimeDeclarationNotApplied}, subjects: []string{"python"}, declared: "~=3.9",
	},
	{
		name:  "Python tests behind a Makefile",
		files: map[string]string{"Makefile": "test:\n\tpython -m pytest\n", "requirements-dev.txt": "pytest\n"},
		check: "test", kinds: []string{setupUnresolvedOpaqueEntrypoint}, subjects: []string{"test"},
	},
	{
		name:  "repository test script",
		files: map[string]string{"scripts/test.sh": "#!/bin/sh\nexit 0\n", ".github/workflows/ci.yml": setupAdvisoryWorkflow("      - run: ./scripts/test.sh\n")},
		args:  []string{"--candidate", "github-actions:.github/workflows/ci.yml:test"},
		check: "test", kinds: []string{setupUnresolvedOpaqueEntrypoint}, subjects: []string{"scripts/test.sh"},
	},
	{
		name:  "plain Go module",
		files: map[string]string{"go.mod": "module example.test/fixture\n\ngo 1.24\n", "fixture_test.go": "package fixture\n"},
		check: "go",
	},
}

func writeSetupUnresolvedShape(t *testing.T, shape setupUnresolvedShape) {
	t.Helper()
	chdirCLITestRepo(t, "unresolved")
	t.Setenv("READYCI_CONFIG_DIR", t.TempDir())
	t.Setenv("READYCI_API_TOKEN", "")
	for path, content := range shape.files {
		writeFileForRepoMapTest(t, path, content)
	}
	for _, executable := range []string{"gradlew", "scripts/test.sh"} {
		if shape.files[executable] != "" {
			if err := os.Chmod(executable, 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func setupUnresolvedPreview(t *testing.T, args ...string) setupReport {
	t.Helper()
	output, err := captureStdoutForCLITest(t, func() error {
		return setupCommand(append([]string{"--preview", "--json"}, args...))
	})
	if err != nil {
		t.Fatalf("setup preview failed: %v\n%s", err, output)
	}
	var report setupReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode setup preview: %v\n%s", err, output)
	}
	return report
}

func checkSetupUnresolvedShape(t *testing.T, shape setupUnresolvedShape, report setupReport) {
	t.Helper()
	environment := report.Environment
	if environment == nil || report.Check != shape.check {
		t.Fatalf("preview selected %q, want %q: %+v", report.Check, shape.check, report)
	}
	if got := setupUnresolvedKinds(environment.Unresolved); !reflect.DeepEqual(got, shape.kinds) {
		t.Fatalf("unresolved kinds = %v, want %v: %+v", got, shape.kinds, environment.Unresolved)
	}
	// The JSON has one home for each review note: the unresolved item.
	if setupWarningsRepeatUnresolvedNotes(environment) {
		t.Fatalf("environment warnings repeat unresolved notes: %v", environment.Warnings)
	}
	for _, candidate := range report.ImportCandidates {
		for _, note := range append(append([]string(nil), candidate.RuntimeNotes...), candidate.Requirements...) {
			for _, item := range candidate.Unresolved {
				if note == item.Note() {
					t.Fatalf("candidate %s repeats unresolved note %q", candidate.ID, note)
				}
			}
		}
	}
	for _, item := range environment.Unresolved {
		if item.Check != shape.check || item.Source == "" || item.Detail == "" || !containsString(shape.subjects, item.Subject) {
			t.Fatalf("unresolved item lacks its check, subject, source or detail: %+v", item)
		}
		// The generated configuration comment comes from the same item.
		if !strings.Contains(report.ProposedConfig, strings.TrimSuffix(strings.TrimPrefix(jsonQuoteForSetupTest(item.Note()), `"`), `"`)) {
			t.Fatalf("note for %+v is missing from the generated configuration:\n%s", item, report.ProposedConfig)
		}
		if item.Kind == setupUnresolvedRuntimeDeclarationNotApplied && item.Subject != "python" && item.Declared != shape.declared {
			t.Fatalf("dropped declaration lost its declared value: %+v", item)
		}
	}
	if shape.declared != "" {
		found := false
		for _, item := range environment.Unresolved {
			found = found || item.Declared == shape.declared
		}
		if !found {
			t.Fatalf("no item retains the declared value %q: %+v", shape.declared, environment.Unresolved)
		}
	}
}

func jsonQuoteForSetupTest(value string) string {
	// Generated configuration comments quote notes with strconv.Quote, which
	// matches JSON escaping for these ASCII notes.
	data, _ := json.Marshal(value)
	return string(data)
}

func TestSetupPreviewListsUnresolvedItemsForEachRepositoryShape(t *testing.T) {
	for _, shape := range setupUnresolvedShapes {
		t.Run(shape.name, func(t *testing.T) {
			writeSetupUnresolvedShape(t, shape)
			report := setupUnresolvedPreview(t, append([]string{"--api-url", "http://127.0.0.1:9"}, shape.args...)...)
			checkSetupUnresolvedShape(t, shape, report)
			// Without a capability preflight nothing is ready.
			if report.Environment.Status != "review_required" || report.Environment.RuntimeStatus != "not_checked" {
				t.Fatalf("offline preview claimed environment state: %+v", report.Environment)
			}
			// The selected candidate carries the same items as the environment.
			if report.ImportScope != nil {
				candidate := setupUnresolvedForCheck(report.ImportScope.Selected.Unresolved, shape.check)
				if !reflect.DeepEqual(mergeSetupUnresolved(candidate, report.Environment.Unresolved), mergeSetupUnresolved(report.Environment.Unresolved)) {
					t.Fatalf("candidate items %+v are not in the environment %+v", candidate, report.Environment.Unresolved)
				}
			}
		})
	}
}

// A compatible runner establishes milestone two only. The environment is ready
// only when nothing is unresolved; runtime_status keeps describing runner
// compatibility alone.
func TestSetupAuthenticatedPreviewIsReadyOnlyWithoutUnresolvedItems(t *testing.T) {
	for _, shape := range setupUnresolvedShapes {
		t.Run(shape.name, func(t *testing.T) {
			writeSetupUnresolvedShape(t, shape)
			server := setupAdvisoryCapabilityServer(t)
			report := setupUnresolvedPreview(t, append([]string{"--api-url", server.URL, "--token", "pfl_unresolved_fixture"}, shape.args...)...)
			checkSetupUnresolvedShape(t, shape, report)
			environment := report.Environment
			wantStatus := "ready"
			if len(shape.kinds) > 0 {
				wantStatus = "review_required"
			}
			if environment.Status != wantStatus || environment.WorkerCapability == nil || len(environment.Blockers) != 0 || validateSetupEnvironment(environment) != nil {
				t.Fatalf("status = %q, want %q without blocking: %+v", environment.Status, wantStatus, environment)
			}
			if environment.RuntimeStatus != "compatible" && environment.RuntimeStatus != "not_required" {
				t.Fatalf("runtime_status no longer describes runner compatibility: %q", environment.RuntimeStatus)
			}
		})
	}
}

func TestSetupMakeEntrypointNoteInGeneratedConfiguration(t *testing.T) {
	writeSetupUnresolvedShape(t, setupUnresolvedShapes[0])
	printed, err := captureStdoutForCLITest(t, func() error {
		return setupCommand([]string{"--print", "--api-url", "http://127.0.0.1:9"})
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`# - "Runtime review: Makefile target \"check\" was not inspected by setup;`,
		"runtimes and dependency preparation it needs are unconfirmed until the first run produces test feedback",
		"prepare: commands or a dependency_check",
		`command: "make -f Makefile -- check"`,
	} {
		if !strings.Contains(printed, want) {
			t.Fatalf("generated configuration lacks %q:\n%s", want, printed)
		}
	}
}

func TestSetupOpaqueEntrypointsParseMakeAndScripts(t *testing.T) {
	root := t.TempDir()
	writeFileForRepoMapTest(t, root+"/web/GNUmakefile", "test:\n\ttrue\n")
	writeFileForRepoMapTest(t, root+"/package.json", `{"scripts":{"test":"make check","postinstall":"make build"}}`)
	for _, tc := range []struct {
		command  string
		subjects []string
		sources  []string
	}{
		{command: "make -f Makefile -- check", subjects: []string{"check"}, sources: []string{"Makefile"}},
		{command: "make -C web -j 4 test lint VERBOSE=1", subjects: []string{"test", "lint"}, sources: []string{"web/GNUmakefile", "web/GNUmakefile"}},
		{command: "cd web && make", subjects: []string{"default goal"}, sources: []string{"web/GNUmakefile"}},
		{command: "CI=true bash -e -c 'make --file=ci.mk test'", subjects: []string{"test"}, sources: []string{"ci.mk"}},
		{command: "bash -eo pipefail scripts/ci.sh", subjects: []string{"scripts/ci.sh"}, sources: []string{"scripts/ci.sh"}},
		{command: "cd tools && ./run-tests", subjects: []string{"tools/run-tests"}, sources: []string{"tools/run-tests"}},
		// The package test script runs Make; the conditional install hook does not count.
		{command: "npm ci && npm test", subjects: []string{"check"}, sources: []string{"Makefile"}},
		{command: "go test ./... && ./gradlew check && node_modules/.bin/jest && bash -c 'echo ok'"},
		{command: setupPythonVenvPreparation},
	} {
		items := setupOpaqueEntrypoints(root, []string{tc.command})
		var subjects, sources []string
		for _, item := range items {
			if item.Kind != setupUnresolvedOpaqueEntrypoint || !strings.Contains(item.Detail, "was not inspected by setup") {
				t.Fatalf("%s: unexpected item %+v", tc.command, item)
			}
			subjects, sources = append(subjects, item.Subject), append(sources, item.Source)
		}
		if !reflect.DeepEqual(subjects, tc.subjects) || !reflect.DeepEqual(sources, tc.sources) {
			t.Fatalf("%s: subjects=%v sources=%v, want %v %v", tc.command, subjects, sources, tc.subjects, tc.sources)
		}
	}
}

func TestSetupPreviewTextStatesMilestones(t *testing.T) {
	for _, tc := range []struct {
		name          string
		shape         setupUnresolvedShape
		authenticated bool
		want          string
	}{
		{name: "compatible runner with unresolved item", shape: setupUnresolvedShapes[0], authenticated: true, want: "readiness: configuration generated and runner compatible, but the unresolved item listed is unconfirmed until the first run produces test feedback."},
		{name: "offline with unresolved items", shape: setupUnresolvedShapes[6], want: "readiness: configuration generated; runner compatibility not checked until you sign in; the 2 unresolved items listed are unconfirmed until the first run produces test feedback."},
		{name: "compatible runner without unresolved items", shape: setupUnresolvedShapes[len(setupUnresolvedShapes)-1], authenticated: true, want: "readiness: configuration generated and runner compatible; the first run confirms that the tests produce feedback."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeSetupUnresolvedShape(t, tc.shape)
			args := []string{"--preview", "--api-url", "http://127.0.0.1:9"}
			if tc.authenticated {
				server := setupAdvisoryCapabilityServer(t)
				args = []string{"--preview", "--api-url", server.URL, "--token", "pfl_unresolved_fixture"}
			}
			output, err := captureStdoutForCLITest(t, func() error { return setupCommand(append(args, tc.shape.args...)) })
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output, tc.want) {
				t.Fatalf("preview lacks milestone sentence %q:\n%s", tc.want, output)
			}
			if len(tc.shape.kinds) > 0 && !strings.Contains(output, "  unresolved ("+tc.shape.kinds[0]+"): ") {
				t.Fatalf("preview does not list unresolved items:\n%s", output)
			}
			// Each item prints once, although the selected candidate carries it too.
			if got := strings.Count(output, "  unresolved ("); got != len(tc.shape.kinds) || strings.Contains(output, "review: Runtime review:") || strings.Contains(output, "review: Preparation review:") {
				t.Fatalf("preview printed %d unresolved lines or repeated a note, want %d:\n%s", got, len(tc.shape.kinds), output)
			}
		})
	}
}

// Guided setup restates the milestones next to the run prompt and still offers
// to run: the first run is how test feedback is established.
func TestGuidedSetupStatesUnresolvedItemsBeforeOfferingToRun(t *testing.T) {
	t.Setenv("READYCI_CONFIG_DIR", t.TempDir())
	chdirCLITestRepo(t, "guided-unresolved")
	runGitForCLITest(t, "init")
	runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	writeFileForRepoMapTest(t, "Makefile", "check:\n\t./gradlew check\n")
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "fixture")
	var runs atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/account":
			writeCLITestJSON(w, http.StatusOK, preflightapi.AccountUsageResponse{TeamID: "team_test"})
		case "/v1/runs":
			runs.Add(1)
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	})
	server := httptest.NewServer(successfulExecutionTestHandler(t, handler))
	defer server.Close()
	var output bytes.Buffer
	if _, err := captureStdoutForCLITest(t, func() error {
		return guidedSetup(setupOptions{APIURL: server.URL, APIToken: "pfl_execution_fixture", Profile: "remote-ci", Detect: true}, strings.NewReader("\ny\nn\n"), &output, false)
	}); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	readiness := strings.Index(text, "Readiness: configuration generated and runner compatible, but the unresolved item listed is unconfirmed until the first run produces test feedback.")
	prompt := strings.Index(text, "Run this check remotely?")
	if readiness < 0 || prompt < readiness {
		t.Fatalf("guided setup did not state unresolved items before offering to run:\n%s", text)
	}
	if runs.Load() != 0 {
		t.Fatal("declined run started execution")
	}
}
