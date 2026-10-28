package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

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
      - uses: `+preflightapi.NodeRuntimeAliasCatalogMetadata().ReviewedActionRef+`
        with:
          node-version: '`+version+`'
`+inputs+`      - run: node --test
`)
	writeFileForRepoMapTest(t, "package.json", `{"engines":{"node":">=24.18.0 <25"}}`)
}

func TestSetupExplicitRuntimeSurvivesCatalogExpiry(t *testing.T) {
	withReviewedAliasClock(t, preflightapi.NodeRuntimeAliasCatalogMetadata().ValidBefore.Add(time.Hour))
	writeSetupRuntimeOverrideFixture(t, "lts/*", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "runtime alias catalog needs review", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	t.Setenv("READYCI_API_URL", server.URL)
	if _, err := captureStdoutForCLITest(t, func() error {
		return setupCommand([]string{"--print", "--project", "fixture"})
	}); err == nil || !strings.Contains(err.Error(), "catalog needs review") {
		t.Fatal("expired catalog must still block an implicit alias import")
	}
	printed, err := captureStdoutForCLITest(t, func() error {
		return setupCommand([]string{"--print", "--project", "fixture", "--runtime", "node=24"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(printed, "User-selected node 24") || !strings.Contains(printed, "does not track") || strings.Contains(printed, "runtime_resolutions:") || strings.Contains(printed, "runtime_pins:") {
		t.Fatalf("saved check must disclose the user choice with ordinary execution fields: %s", printed)
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
				RuntimePins        map[string]string                         `json:"runtime_pins"`
				RuntimeResolutions map[string]preflightapi.RuntimeResolution `json:"runtime_resolutions"`
			} `json:"selected"`
		} `json:"import_scope"`
		Next []string `json:"next"`
	}
	if err := json.Unmarshal([]byte(preview), &report); err != nil {
		t.Fatal(err)
	}
	if report.ProposedConfig != printed || report.ImportScope.Selected.RuntimePins["node"] != "24" || len(report.ImportScope.Selected.RuntimeResolutions) != 0 || !strings.Contains(strings.Join(report.Next, "\n"), "--runtime node=24") {
		t.Fatalf("preview lost the explicit choice or catalog independence: %s", preview)
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
	cfg, err := config.ParseProjectConfig(saved, "user pin")
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

func TestSetupRuntimeOverrideRejectsIncompatibleRequirementsBeforeWriting(t *testing.T) {
	withReviewedAliasClock(t, preflightapi.NodeRuntimeAliasCatalogMetadata().ValidBefore)
	for _, test := range []struct {
		name, version, inputs, pin string
	}{
		{"source constraint", "lts/*", "", "node=22"},
		{"numeric action constraint", "22", "", "node=24"},
		{"latest selection", "lts/*", "          check-latest: true\n", "node=24"},
		{"unsupported input", "lts/*", "          architecture: arm64\n", "node=24"},
		{"unsupported alias", "nightly", "", "node=24"},
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
	withReviewedAliasClock(t, preflightapi.NodeRuntimeAliasCatalogMetadata().ValidBefore)
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
	withReviewedAliasClock(t, preflightapi.NodeRuntimeAliasCatalogMetadata().ValidBefore)
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
