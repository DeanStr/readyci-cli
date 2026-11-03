package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func writeSetupRuntimeOverrideFixture(t *testing.T, version, inputs string) {
	t.Helper()
	chdirCLITestRepo(t, "runtime-override")
	t.Setenv("READYCI_CONFIG_DIR", t.TempDir())
	t.Setenv("READYCI_API_TOKEN", "")
	writeFileForRepoMapTest(t, ".github/workflows/ci.yml", `jobs:
  tests:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-node@49933ea5288caeca8642d1e84afbd3f7d6820020
        with:
          node-version: '`+version+`'
`+inputs+`      - run: node --test
`)
	writeFileForRepoMapTest(t, "package.json", `{"engines":{"node":">=24.18.0 <25"}}`)
}

// A moving selector is never resolved by setup or by the service. It stays a
// review note, leaves the image's installed Node in use, and an explicit
// --runtime choice replaces it with ordinary numeric policy.
func TestSetupMovingNodeSelectorIsReviewedAndExplicitRuntimeReplacesIt(t *testing.T) {
	writeSetupRuntimeOverrideFixture(t, "lts/*", "")
	t.Setenv("READYCI_API_URL", "http://127.0.0.1:9")
	implicit, err := captureStdoutForCLITest(t, func() error {
		return setupCommand([]string{"--print", "--project", "fixture"})
	})
	if err != nil {
		t.Fatalf("moving selector blocked setup: %v", err)
	}
	cfg, err := config.ParseProjectConfig([]byte(implicit), "moving selector")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Checks["tests"].RuntimeRequirements["node"]; got != ">=24.18.0,<25" || !strings.Contains(implicit, `actions/setup-node node-version \"lts/*\" is not emitted as a node runtime requirement`) || !strings.Contains(implicit, "--runtime node=VERSION") {
		t.Fatalf("moving selector was resolved or its review note is missing: %q\n%s", got, implicit)
	}
	printed, err := captureStdoutForCLITest(t, func() error {
		return setupCommand([]string{"--print", "--project", "fixture", "--runtime", "node=24"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(printed, "User-selected node 24") || !strings.Contains(printed, "does not track") || strings.Contains(printed, "is not emitted") || strings.Count(printed, "runtime_pins:") != 1 {
		t.Fatalf("saved check must disclose the user choice with ordinary execution fields: %s", printed)
	}
	// The pin is recorded only as import provenance, so a later preview of
	// the saved check knows the selector was replaced; execution uses
	// runtime_requirements alone.
	if pinned, err := config.ParseProjectConfig([]byte(printed), "user pin"); err != nil || pinned.Checks["tests"].ImportedFrom == nil || pinned.Checks["tests"].ImportedFrom.RuntimePins["node"] != "24" || len(pinned.Checks["tests"].ImportedFrom.RuntimePins) != 1 {
		t.Fatalf("saved check does not record the runtime pin with its import: %v\n%s", err, printed)
	}
	preview, err := captureStdoutForCLITest(t, func() error {
		return setupCommand([]string{"--preview", "--json", "--project", "fixture", "--runtime", "node=24"})
	})
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		ProposedConfig string `json:"proposed_config"`
		ImportScope    struct {
			Selected struct {
				RuntimePins  map[string]string     `json:"runtime_pins"`
				RuntimeNotes []string              `json:"runtime_notes"`
				Unresolved   []setupUnresolvedItem `json:"unresolved"`
			} `json:"selected"`
		} `json:"import_scope"`
		Next []string `json:"next"`
	}
	if err := json.Unmarshal([]byte(preview), &report); err != nil {
		t.Fatal(err)
	}
	if report.ProposedConfig != printed || report.ImportScope.Selected.RuntimePins["node"] != "24" || len(report.ImportScope.Selected.RuntimeNotes) != 0 || len(report.ImportScope.Selected.Unresolved) != 0 || !strings.Contains(strings.Join(report.Next, "\n"), "--runtime node=24") {
		t.Fatalf("preview lost the explicit choice: %s", preview)
	}
	report.ImportScope.Selected.Unresolved = nil
	unpinned, err := captureStdoutForCLITest(t, func() error {
		return setupCommand([]string{"--preview", "--json", "--project", "fixture"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(unpinned), &report); err != nil {
		t.Fatal(err)
	}
	if items := report.ImportScope.Selected.Unresolved; len(items) != 1 || items[0].Kind != setupUnresolvedRuntimeDeclarationNotApplied || items[0].Declared != "lts/*" || len(report.ImportScope.Selected.RuntimeNotes) != 0 {
		t.Fatalf("preview JSON lost the moving selector unresolved item: %s", unpinned)
	}
	if _, err := os.Stat(".readyci.yml"); !os.IsNotExist(err) {
		t.Fatal("preview wrote configuration")
	}
	_, err = captureStdoutForCLITest(t, func() error {
		return setupCommand([]string{"--write-config", "--project", "fixture", "--runtime", "node=24"})
	})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(".readyci.yml")
	if err != nil || string(saved) != printed {
		t.Fatalf("saved bytes differ from preview: %v", err)
	}
	cfg, err = config.ParseProjectConfig(saved, "user pin")
	if err != nil {
		t.Fatal(err)
	}
	requirement := cfg.Checks["tests"].RuntimeRequirements["node"]
	if !preflightapi.RuntimeVersionSatisfies("24.18.0", requirement) || preflightapi.RuntimeVersionSatisfies("24.17.0", requirement) || preflightapi.RuntimeVersionSatisfies("25.0.0", requirement) {
		t.Fatalf("source constraints were lost: %q", requirement)
	}
	_, err = captureStdoutForCLITest(t, func() error {
		return setupCommand([]string{"--preview", "--runtime", "node=24"})
	})
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("existing configuration must require explicit regeneration: %v", err)
	}
	unchanged, err := os.ReadFile(".readyci.yml")
	if err != nil || string(unchanged) != string(saved) {
		t.Fatal("runtime override changed existing configuration")
	}
}

func TestSetupRuntimeOverrideReplacesEveryUntranslatedNodeSelector(t *testing.T) {
	for _, test := range []struct{ name, version, inputs string }{
		{"lts alias", "lts/*", ""},
		{"named alias", "nightly", ""},
		{"latest selection", "24", "          check-latest: true\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			writeSetupRuntimeOverrideFixture(t, test.version, test.inputs)
			printed, err := captureStdoutForCLITest(t, func() error {
				return setupCommand([]string{"--print", "--runtime", "node=24"})
			})
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := config.ParseProjectConfig([]byte(printed), "pinned selector")
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.Checks["tests"].RuntimeRequirements["node"]; got != "24,>=24.18.0,<25" || strings.Contains(printed, "is not emitted") {
				t.Fatalf("explicit runtime did not replace the untranslated selector: %q\n%s", got, printed)
			}
		})
	}
}

// A repository compatibility declaration that conflicts with the explicit
// choice is disclosed, not sealed and not a setup failure.
func TestSetupRuntimeOverrideReviewsConflictingRepositoryDeclaration(t *testing.T) {
	writeSetupRuntimeOverrideFixture(t, "lts/*", "")
	printed, err := captureStdoutForCLITest(t, func() error {
		return setupCommand([]string{"--print", "--runtime", "node=22"})
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.ParseProjectConfig([]byte(printed), "conflicting declaration")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Checks["tests"].RuntimeRequirements["node"]; got != "22" || !strings.Contains(printed, `package.json (engines.node) \">=24.18.0 <25\" is not emitted as a node runtime requirement (it conflicts`) {
		t.Fatalf("explicit choice was not kept or the conflict was hidden: %q\n%s", got, printed)
	}
}

func TestSetupRuntimeOverrideRejectsIncompatibleRequirementsBeforeWriting(t *testing.T) {
	for _, test := range []struct {
		name, version, inputs, pin string
	}{
		{"numeric action constraint", "22", "", "node=24"},
		{"numeric action constraint with check-latest", "22", "          check-latest: true\n", "node=24"},
		{"unsupported input", "lts/*", "          architecture: arm64\n", "node=24"},
		{"sequential selections", "lts/*", "      - uses: actions/setup-node@v4\n        with: {node-version: '24'}\n", "node=24"},
		{"unused selection", "24", "", "python=3.12"},
	} {
		t.Run(test.name, func(t *testing.T) {
			writeSetupRuntimeOverrideFixture(t, test.version, test.inputs)
			_, err := captureStdoutForCLITest(t, func() error {
				return setupCommand([]string{"--write-config", "--runtime", test.pin})
			})
			if err == nil {
				t.Fatal("incompatible selection was accepted")
			}
			if _, err := os.Stat(".readyci.yml"); !os.IsNotExist(err) {
				t.Fatal("incompatible selection wrote configuration")
			}
		})
	}
}

func TestSetupRuntimeOverrideKeepsIndependentRuntimePins(t *testing.T) {
	writeSetupRuntimeOverrideFixture(t, "lts/*", `      - uses: pnpm/action-setup@v4
        with: {version: '10'}
      - uses: actions/setup-go@v6
        with: {go-version: '1.25'}
      - uses: actions/setup-python@v6
        with: {python-version: '3.12'}
`)
	printed, err := captureStdoutForCLITest(t, func() error {
		return setupCommand([]string{"--print", "--runtime", "node=24", "--runtime", "pnpm=10.33.4", "--runtime", "go=1.25.13", "--runtime", "python=3.12.3"})
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.ParseProjectConfig([]byte(printed), "multiple user pins")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Checks["tests"].RuntimeRequirements; !preflightapi.RuntimeVersionSatisfies("24.18.0", got["node"]) || !preflightapi.RuntimeVersionSatisfies("10.33.4", got["pnpm"]) || preflightapi.RuntimeVersionSatisfies("10.33.5", got["pnpm"]) {
		t.Fatalf("runtime pins were ignored or conflated: %v", got)
	}
	for runtime, version := range map[string]string{"go": "1.25.13", "python": "3.12.3"} {
		if !preflightapi.RuntimeRequirementImplies(cfg.Checks["tests"].RuntimeRequirements[runtime], version) {
			t.Fatalf("%s pin was lost: %v", runtime, cfg.Checks["tests"].RuntimeRequirements)
		}
	}
}

func TestGuidedSetupRuntimeOverrideSavesReviewedPin(t *testing.T) {
	options, runs := newGuidedSetupReviewFixture(t)
	writeFileForRepoMapTest(t, ".github/workflows/ci.yml", `jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: 'lts/*'}
      - run: node --test
`)
	options.ImportCandidate = "github-actions:.github/workflows/ci.yml:test"
	options.RuntimePins = map[string]string{"node": "24"}
	var output bytes.Buffer
	if err := guidedSetup(options, strings.NewReader("y\nn\n"), &output, false); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(".readyci.yml")
	if err != nil || !strings.Contains(output.String(), string(saved)) || !strings.Contains(string(saved), "User-selected node 24") || runs.Load() != 0 {
		t.Fatalf("guided setup lost the reviewed pin or executed after refusal: %v\n%s", err, output.String())
	}
}

func TestSetupRuntimeOverrideRejectsInvalidFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--runtime", ""}, {"--runtime", "node"}, {"--runtime", "node=lts/*"},
		{"--runtime", "node=installed"}, {"--runtime", "node=>=24"}, {"--runtime", "node=024"},
		{"--runtime", "node=24.1.2.3"}, {"--runtime", "ruby=3.4"},
		{"--runtime", "node=24", "--runtime", "node=22"},
		{"--runtime", "node=24", "--detect=false"},
		{"--runtime", "node=24", "--helper", "shell-syntax"},
		{"--runtime", "node=24", "--import", "make"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			chdirCLITestRepo(t, "invalid-runtime")
			_, err := captureStdoutForCLITest(t, func() error { return setupCommand(append([]string{"--print"}, args...)) })
			if err == nil || normalizeCLIError(err).ExitCode != 2 {
				t.Fatalf("invalid flags did not produce a configuration error: %v", err)
			}
		})
	}
}
