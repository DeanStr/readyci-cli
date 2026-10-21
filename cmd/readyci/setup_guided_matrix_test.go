package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

func TestGuidedSetupMatrixOverlaySavesExactPreviewAndScope(t *testing.T) {
	options, runs := newGuidedSetupReviewFixture(t)
	writeGuidedMatrixOverlayFixture(t)
	selected := guidedMatrixOverlayCandidate(t)
	previewOptions := options
	previewOptions.ImportCandidate = selected.ID
	preview, err := buildSetupReport(previewOptions)
	if err != nil {
		t.Fatal(err)
	}
	if preview.ImportScope == nil || len(preview.ImportScope.Omitted) != 2 {
		t.Fatalf("preview lost the blocked integration job or Windows sibling: %+v", preview.ImportScope)
	}
	reportOutput, err := captureStdoutForCLITest(t, func() error {
		printSetupReport(preview)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reportOutput, `selected_matrix: flags="-race", os="ubuntu-latest"`) || !strings.Contains(reportOutput, `matrix: flags="", os="windows-latest"`) {
		t.Fatalf("report hides concrete selected or omitted matrix values: %s", reportOutput)
	}
	var output bytes.Buffer
	if err := guidedSetup(options, strings.NewReader(selected.ID+"\ny\nn\n"), &output, false); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(".readyci.yml")
	if err != nil {
		t.Fatal(err)
	}
	if string(saved) != preview.ProposedConfig || !strings.Contains(output.String(), string(saved)) {
		t.Fatalf("guided selection did not save the exact explicitly previewed configuration:\n%s", saved)
	}
	cfg, _, err := config.LoadProjectConfig(".")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Checks) != 1 {
		t.Fatalf("selection changed check scope: %+v", cfg.Checks)
	}
	for _, check := range cfg.Checks {
		if check.RuntimeRequirements["go"] != "1.25.13" || check.Timeout != "10m" || len(check.Steps) != 1 || check.Steps[0].Env["GOFLAGS"] != "-race" || check.Steps[0].Command != "bash -e -c 'go test ./...'" {
			t.Fatalf("selected overlay changed runtime, environment, deadline or test command: %+v", check)
		}
	}
	for _, omitted := range preview.ImportScope.Omitted {
		if !strings.Contains(string(saved), omitted.ID) {
			t.Fatalf("saved configuration dropped omitted scope %q", omitted.ID)
		}
	}
	if runs.Load() != 0 {
		t.Fatal("declining remote execution submitted a run")
	}
}

func TestGuidedSetupMatrixOverlayDeclineLeavesConfigurationAbsent(t *testing.T) {
	options, runs := newGuidedSetupReviewFixture(t)
	writeGuidedMatrixOverlayFixture(t)
	selected := guidedMatrixOverlayCandidate(t)
	var output bytes.Buffer
	if err := guidedSetup(options, strings.NewReader(selected.ID+"\nn\n"), &output, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(".readyci.yml"); !os.IsNotExist(err) || runs.Load() != 0 {
		t.Fatal("declining the selected configuration wrote or executed it")
	}
}

func writeGuidedMatrixOverlayFixture(t *testing.T) {
	t.Helper()
	writeFileForRepoMapTest(t, filepath.Join(".github", "workflows", "tests.yml"), `jobs:
  integration:
    runs-on: ubuntu-latest
    services:
      postgres:
        image: postgres:16
    steps:
      - run: go test -tags=integration ./...
  test:
    runs-on: ${{ matrix.os }}
    timeout-minutes: 10
    env:
      GOFLAGS: ${{ matrix.flags }}
    strategy:
      matrix:
        os: [ubuntu-latest, windows-latest]
        include:
          - flags: ''
          - os: ubuntu-latest
            flags: -race
    steps:
      - uses: actions/setup-go@v5
        with:
          go-version: '1.25.13'
      - run: go test ./...
`)
}

func guidedMatrixOverlayCandidate(t *testing.T) repoImportCandidate {
	t.Helper()
	candidates := setupImportCandidates(".", "github-actions")
	for _, candidate := range candidates {
		if candidate.ParentJobID != "" && candidate.Matrix["os"] == "ubuntu-latest" && candidate.Matrix["flags"] == "-race" {
			return candidate
		}
	}
	t.Fatalf("no concrete Ubuntu overlay candidate: %+v", candidates)
	return repoImportCandidate{}
}
