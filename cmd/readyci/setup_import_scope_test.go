package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExplicitImportPreviewShowsSelectedConfigurationAndOmittedScope(t *testing.T) {
	options, runs := newGuidedSetupReviewFixture(t)
	writeFileForRepoMapTest(t, filepath.Join(".github", "workflows", "tests.yml"), `jobs:
  unit:
    runs-on: ubuntu-latest
    steps:
      - run: go test ./...
  integration:
    runs-on: ubuntu-latest
    services:
      postgres:
        image: postgres:16
    steps:
      - run: go test -tags=integration ./...
`)
	options.ImportSource = "github-actions"
	options.ImportCandidate = "github-actions:.github/workflows/tests.yml:unit"
	report, err := buildSetupReport(options)
	if err != nil {
		t.Fatal(err)
	}
	if report.SelectionStatus != "selected" || report.Check != "unit" || len(report.Checks) != 1 || report.Checks[0] != "unit" {
		t.Fatalf("explicit preview reports automatic selection instead of the reviewed candidate: selection=%q check=%q checks=%v", report.SelectionStatus, report.Check, report.Checks)
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	var proposed string
	if err := json.Unmarshal(document["proposed_config"], &proposed); err != nil {
		t.Fatalf("selected preview has no proposed configuration: %v", err)
	}
	if !strings.Contains(proposed, "go test ./...") || !strings.Contains(proposed, "Not imported") || !strings.Contains(proposed, "github-actions:.github/workflows/tests.yml:integration") {
		t.Fatalf("selected configuration omits command or uncovered scope: %s", proposed)
	}
	var scope struct {
		Selected repoImportCandidate   `json:"selected"`
		Omitted  []repoImportCandidate `json:"omitted"`
	}
	if err := json.Unmarshal(document["import_scope"], &scope); err != nil {
		t.Fatal(err)
	}
	if scope.Selected.ID != options.ImportCandidate || len(scope.Omitted) != 1 || scope.Omitted[0].ID != "github-actions:.github/workflows/tests.yml:integration" || len(scope.Omitted[0].Unsupported) == 0 {
		t.Fatalf("selected or blocked omitted scope disappeared: %+v", scope)
	}
	if _, err := os.Stat(".readyci.yml"); !os.IsNotExist(err) || runs.Load() != 0 {
		t.Fatal("preview wrote configuration or submitted execution")
	}
}

func TestExplicitImportPrintNeedsNoAuthenticationOrWrites(t *testing.T) {
	chdirCLITestRepo(t, "print-import")
	t.Setenv("READYCI_CONFIG_DIR", t.TempDir())
	writeFileForRepoMapTest(t, "Makefile", "unit:\n\tgo test ./...\n")
	output, err := captureStdoutForCLITest(t, func() error {
		return setupCommand([]string{"--print", "--candidate", "make:Makefile:unit", "--project", "print-import"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "make -f Makefile -- unit") || !strings.Contains(output, "# Selected: \"make:Makefile:unit\"") {
		t.Fatalf("missing selected config or scope: %s", output)
	}
	if _, err := os.Stat(".readyci.yml"); !os.IsNotExist(err) {
		t.Fatal("printing an import wrote configuration")
	}
}

func TestImportScopeRetainsOtherMatrixVariantsAndBlockedJobs(t *testing.T) {
	parent := repoImportCandidate{ID: "github-actions:ci.yml:test", Source: "github-actions"}
	selected := repoImportCandidate{ID: parent.ID + ":matrix:linux", ParentJobID: parent.ID, Source: parent.Source, Matrix: map[string]string{"os": "ubuntu-latest"}}
	windows := repoImportCandidate{ID: parent.ID + ":matrix:windows", ParentJobID: parent.ID, Source: parent.Source, Matrix: map[string]string{"os": "windows-latest"}, Unsupported: []string{"unsupported runner"}}
	integration := repoImportCandidate{ID: "github-actions:ci.yml:integration", Source: parent.Source, Unsupported: []string{"service topology"}}
	scope := selectedSetupImportScope(selected, []repoImportCandidate{parent, selected, windows, integration})
	if len(scope.Omitted) != 2 || scope.Omitted[0].ID != windows.ID || scope.Omitted[1].ID != integration.ID {
		t.Fatalf("matrix or blocked job scope dropped: %+v", scope)
	}
	malicious := repoImportCandidate{ID: "job\nchecks:\n  injected: yes", Source: "github-actions"}
	text := appendSetupImportScope("version: 1\n", selectedSetupImportScope(selected, []repoImportCandidate{selected, malicious}))
	for _, line := range strings.Split(strings.TrimSuffix(strings.TrimPrefix(text, "version: 1\n"), "\n"), "\n") {
		if line != "" && !strings.HasPrefix(line, "# ") {
			t.Fatalf("repository metadata escaped YAML comments: %q", line)
		}
	}
}

func TestSourceFilteredImportStillDisclosesUpstreamJobs(t *testing.T) {
	options, _ := newGuidedSetupReviewFixture(t)
	writeFileForRepoMapTest(t, "Makefile", "unit:\n\tgo test ./...\n")
	writeFileForRepoMapTest(t, filepath.Join(".github", "workflows", "integration.yml"), `jobs:
  integration:
    runs-on: ubuntu-latest
    services:
      postgres:
        image: postgres:16
    steps:
      - run: go test -tags=integration ./...
`)
	options.ImportSource, options.ImportCandidate = "make", "make:Makefile:unit"
	report, err := buildSetupReport(options)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.ImportCandidates) != 1 || report.ImportCandidates[0].Source != "make" {
		t.Fatalf("selection filter was not retained: %+v", report.ImportCandidates)
	}
	if report.ImportScope == nil || len(report.ImportScope.Omitted) != 1 || report.ImportScope.Omitted[0].ID != "github-actions:.github/workflows/integration.yml:integration" {
		t.Fatalf("source filter erased omitted upstream scope: %+v", report.ImportScope)
	}
	if !strings.Contains(report.ProposedConfig, "github-actions:.github/workflows/integration.yml:integration") {
		t.Fatal("source-filtered configuration lost omitted workflow provenance")
	}
}
