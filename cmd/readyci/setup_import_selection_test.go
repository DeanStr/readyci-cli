package main

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupImportSelectionCandidates() []repoImportCandidate {
	parent := "github-actions:.github/workflows/tests.yml:test"
	return []repoImportCandidate{
		{ID: parent, Source: "github-actions", Name: "test", Command: "go test ./...", TestEntrypoint: true, Unsupported: []string{"matrix strategy requires explicit Ready CI matrix configuration"}},
		{ID: parent + ":matrix:linux", ParentJobID: parent, Source: "github-actions", Name: "test", Command: "go test ./...", TestEntrypoint: true, Matrix: map[string]string{"os": "ubuntu-latest", "go": "1.27"}},
		{ID: parent + ":matrix:windows", ParentJobID: parent, Source: "github-actions", Name: "test", Command: "go test ./...", TestEntrypoint: true, Matrix: map[string]string{"os": "windows-latest", "go": "1.27"}, Unsupported: []string{"non-Ubuntu runner requires explicit configuration"}},
		{ID: "github-actions:.github/workflows/tests.yml:integration", Source: "github-actions", Name: "integration", Command: "go test -tags=integration ./...", TestEntrypoint: true, Unsupported: []string{"service topology requires explicit Ready CI services configuration"}},
	}
}

func TestSetupImportSelectionShowsConcreteVariantsAndRepromptsBlockedChoice(t *testing.T) {
	candidates := setupImportSelectionCandidates()
	var output bytes.Buffer
	selected, err := selectSetupImport(setupOptions{}, candidates, bufio.NewReader(strings.NewReader("2\n1\n")), &output, false)
	if err != nil || selected == nil || selected.ID != candidates[1].ID {
		t.Fatalf("supported matrix child was not selected: selected=%+v err=%v", selected, err)
	}
	text := output.String()
	for _, want := range []string{
		"Upstream matrix job: test [" + candidates[0].ID + "]; 2 concrete variants listed.",
		"1. test [" + candidates[1].ID + "]",
		`Matrix: go="1.27", os="ubuntu-latest"`,
		"this candidate requires manual configuration: non-Ubuntu runner",
		"Selected scope: " + candidates[1].ID,
		"Only the selected candidate is imported.",
		"Not imported: " + candidates[2].ID,
		"Not imported: " + candidates[3].ID,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("selection output lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "1. test ["+candidates[0].ID+"]") || strings.Count(text, "Choose an existing command") != 2 {
		t.Fatalf("aggregate parent consumed a choice or blocked choice ended selection:\n%s", text)
	}
	if strings.Contains(text, "Not imported: "+candidates[0].ID+"\n") {
		t.Fatal("selected matrix parent was incorrectly described as wholly omitted")
	}
}

func TestSetupImportSelectionAcceptsExactIDsWithoutImportingAggregateParent(t *testing.T) {
	candidates := setupImportSelectionCandidates()
	var output bytes.Buffer
	answers := candidates[0].ID + "\n" + candidates[1].ID + "\n"
	selected, err := selectSetupImport(setupOptions{}, candidates, bufio.NewReader(strings.NewReader(answers)), &output, false)
	if err != nil || selected == nil || selected.ID != candidates[1].ID {
		t.Fatalf("exact child ID was not selected after blocked parent: selected=%+v err=%v", selected, err)
	}
	if !strings.Contains(output.String(), "this candidate requires manual configuration: matrix strategy") {
		t.Fatalf("aggregate parent blocker disappeared:\n%s", output.String())
	}
	_, err = selectSetupImport(setupOptions{ImportCandidate: candidates[0].ID}, candidates, nil, &output, true)
	if err == nil || !strings.Contains(err.Error(), "matrix strategy") {
		t.Fatalf("explicit aggregate import did not retain strict blocker: %v", err)
	}
}

func TestSetupImportSelectionAutomaticAmbiguityUsesLanguageDefaultWithoutDroppingScope(t *testing.T) {
	candidates := setupImportSelectionCandidates()
	var output bytes.Buffer
	selected, err := selectSetupImport(setupOptions{}, candidates, bufio.NewReader(strings.NewReader("\n")), &output, false)
	if err != nil || selected != nil {
		t.Fatalf("automatic ambiguity imported an upstream job: selected=%+v err=%v", selected, err)
	}
	if !strings.Contains(output.String(), "Automatic selection uses the detected language default") || !strings.Contains(output.String(), "multiple upstream test jobs") {
		t.Fatalf("automatic selection did not report the uncovered upstream scope:\n%s", output.String())
	}
	selected, err = selectSetupImport(setupOptions{}, candidates, nil, &output, true)
	if err != nil || selected != nil {
		t.Fatalf("noninteractive setup silently chose a matrix child: selected=%+v err=%v", selected, err)
	}
	selection := automaticSetupSelection(candidates)
	if len(selection.NotImported) != 2 || selection.NotImported[0].ID != candidates[0].ID || selection.NotImported[1].ID != candidates[3].ID {
		t.Fatalf("language default did not report each upstream job once: %+v", selection.NotImported)
	}
}

func TestSetupImportSelectionEOFNeverExecutesRepositoryCommands(t *testing.T) {
	repo := t.TempDir()
	marker := filepath.Join(repo, "unexpected-execution")
	writeFileForRepoMapTest(t, filepath.Join(repo, ".github", "workflows", "tests.yml"), `jobs:
  test:
    runs-on: ${{ matrix.os }}
    strategy:
      matrix:
        os: [ubuntu-latest, windows-latest]
    steps:
      - run: touch `+marker+` && go test ./...
`)
	candidates := setupImportCandidates(repo, "github-actions")
	if len(candidates) != 3 {
		t.Fatalf("fixture lacks aggregate and two concrete variants: %+v", candidates)
	}
	var output bytes.Buffer
	selected, err := selectSetupImport(setupOptions{}, candidates, bufio.NewReader(strings.NewReader("")), &output, false)
	if err == nil || selected != nil || !strings.Contains(err.Error(), "input ended before command selection") {
		t.Fatalf("EOF did not abort selection: selected=%+v err=%v", selected, err)
	}
	for _, path := range []string{marker, filepath.Join(repo, ".readyci.yml")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("selection executed or wrote repository source: %s err=%v", path, err)
		}
	}
}

func TestSetupImportSelectionRetainsSingleConcreteScope(t *testing.T) {
	candidate := repoImportCandidate{ID: "github-actions:tests.yml:unit", Source: "github-actions", Name: "unit", Command: "go test ./...", TestEntrypoint: true}
	var output bytes.Buffer
	selected, err := selectSetupImport(setupOptions{}, []repoImportCandidate{candidate}, bufio.NewReader(strings.NewReader("\n")), &output, false)
	if err != nil || selected == nil || selected.ID != candidate.ID || !strings.Contains(output.String(), "Selected scope: "+candidate.ID) {
		t.Fatalf("single supported automatic scope was not retained: selected=%+v err=%v output=%s", selected, err, output.String())
	}
}
