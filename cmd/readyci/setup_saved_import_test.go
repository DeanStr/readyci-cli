package main

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

// setupImportedCandidates lists the import candidates a generated
// configuration records, one per imported check.
func setupImportedCandidates(t *testing.T, template string) []string {
	t.Helper()
	cfg, err := config.ParseProjectConfig([]byte(template), "generated configuration")
	if err != nil {
		t.Fatalf("parse generated configuration: %v\n%s", err, template)
	}
	var candidates []string
	for _, name := range sortedCheckNames(cfg.Checks) {
		if imported := cfg.Checks[name].ImportedFrom; imported != nil {
			candidates = append(candidates, imported.Candidate)
		}
	}
	return candidates
}

func setupUnresolvedShapeNamed(t *testing.T, name string) setupUnresolvedShape {
	t.Helper()
	for _, shape := range setupUnresolvedShapes {
		if shape.name == name {
			return shape
		}
	}
	t.Fatalf("no unresolved shape %q", name)
	return setupUnresolvedShape{}
}

// setupSavedImportFixture writes a repository whose only CI workflow selects
// Node with setup-node lts/*, and returns authenticated setup arguments.
func setupSavedImportFixture(t *testing.T) []string {
	t.Helper()
	writeSetupUnresolvedShape(t, setupUnresolvedShapeNamed(t, "setup-node lts/* workflow"))
	server := setupAdvisoryCapabilityServer(t)
	return []string{"--api-url", server.URL, "--token", "pfl_unresolved_fixture"}
}

func writeSetupConfigForTest(t *testing.T, args ...string) {
	t.Helper()
	if output, err := captureStdoutForCLITest(t, func() error {
		return setupCommand(append([]string{"--write-config"}, args...))
	}); err != nil {
		t.Fatalf("setup --write-config failed: %v\n%s", err, output)
	}
}

// Saving an imported configuration changes nothing about the repository or
// its tests, so it must not change what setup reports as unresolved. The
// workflow's setup-node lts/* selector is not part of the saved check; a
// preview of the saved configuration re-reads it from the candidate the check
// records it was imported from.
func TestSetupSavedImportKeepsWorkflowSelectorItems(t *testing.T) {
	auth := setupSavedImportFixture(t)
	proposed := setupUnresolvedPreview(t, auth...)
	if proposed.ConfigStatus != "missing" || proposed.Environment.Status != "review_required" ||
		len(proposed.Environment.Unresolved) != 1 || proposed.Environment.Unresolved[0].Declared != "lts/*" {
		t.Fatalf("proposal does not report the lts/* selector: %+v", proposed.Environment)
	}
	writeSetupConfigForTest(t, auth...)

	saved := setupUnresolvedPreview(t, auth...)
	if saved.ConfigStatus != "ok" || saved.ProposedConfig != "" || saved.ImportScope != nil || saved.Check != proposed.Check {
		t.Fatalf("second preview did not read the saved configuration: %+v", saved)
	}
	if saved.Environment.Status != "review_required" || saved.Environment.WorkerCapability == nil {
		t.Fatalf("saved configuration reported %q although nothing changed: %+v", saved.Environment.Status, saved.Environment)
	}
	if !reflect.DeepEqual(saved.Environment.Unresolved, proposed.Environment.Unresolved) {
		t.Fatalf("saved unresolved items = %+v, want the proposal's %+v", saved.Environment.Unresolved, proposed.Environment.Unresolved)
	}
	if want := "readiness: configuration valid and runner compatible, but the unresolved item listed is unconfirmed until the first run produces test feedback."; !strings.Contains(setupHumanPreviewForTest(t, auth...), want) {
		t.Fatalf("human preview of the saved configuration lacks %q", want)
	}
}

func setupHumanPreviewForTest(t *testing.T, args ...string) string {
	t.Helper()
	output, err := captureStdoutForCLITest(t, func() error {
		return setupCommand(append([]string{"--preview"}, args...))
	})
	if err != nil {
		t.Fatalf("setup preview failed: %v\n%s", err, output)
	}
	return output
}

// The recorded candidate may disappear: the workflow is deleted, the job is
// renamed, or it stops looking like a verification job. The saved check still
// runs as written, so the preview neither fails nor invents items.
func TestSetupSavedImportWithoutItsCandidateContributesNothing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(t *testing.T)
	}{
		{name: "workflow deleted", mutate: func(t *testing.T) {
			if err := os.Remove(".github/workflows/ci.yml"); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "job renamed", mutate: func(t *testing.T) {
			workflow, err := os.ReadFile(".github/workflows/ci.yml")
			if err != nil {
				t.Fatal(err)
			}
			writeFileForRepoMapTest(t, ".github/workflows/ci.yml", strings.Replace(string(workflow), "\n  test:\n", "\n  unit:\n", 1))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth := setupSavedImportFixture(t)
			writeSetupConfigForTest(t, auth...)
			tc.mutate(t)
			saved := setupUnresolvedPreview(t, auth...)
			if saved.ConfigStatus != "ok" || saved.Environment.Status != "ready" || len(saved.Environment.Unresolved) != 0 || len(saved.Environment.Blockers) != 0 {
				t.Fatalf("missing candidate changed the saved check's review: %+v", saved.Environment)
			}
		})
	}
}

// The items are re-read, not saved: when the upstream job's selector changes
// after the configuration is saved, the preview reports the current one.
func TestSetupSavedImportReportsTheWorkflowsCurrentSelector(t *testing.T) {
	auth := setupSavedImportFixture(t)
	writeSetupConfigForTest(t, auth...)
	workflow, err := os.ReadFile(".github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	writeFileForRepoMapTest(t, ".github/workflows/ci.yml", strings.Replace(string(workflow), "node-version: lts/*", "node-version: lts/iron", 1))
	saved := setupUnresolvedPreview(t, auth...)
	if items := saved.Environment.Unresolved; len(items) != 1 || items[0].Declared != "lts/iron" || items[0].Check != saved.Check || saved.Environment.Status != "review_required" {
		t.Fatalf("saved preview did not report the workflow's current selector: %+v", saved.Environment)
	}
	// Only the candidate ID is saved; KnownFields parsing rules out anything
	// else under imported_from, so no selector or note text can go stale.
	cfg, _, err := config.LoadProjectConfig(".")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cfg.Checks["test"].ImportedFrom, (&config.CheckImportConfig{Candidate: "github-actions:.github/workflows/ci.yml:test"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("imported_from = %+v, want %+v", got, want)
	}
}

// A --runtime choice replaces the selector, and the saved check records it
// with its import. The record counts only while the check's own requirement
// still applies the pin: removing the requirement from the check brings the
// selector back even though imported_from still lists the pin.
func TestSetupSavedImportCountsItsPinOnlyWhileTheCheckAppliesIt(t *testing.T) {
	auth := setupSavedImportFixture(t)
	pinned := append([]string{"--runtime", "node=24"}, auth...)
	if proposed := setupUnresolvedPreview(t, pinned...); proposed.Environment.Status != "ready" || len(proposed.Environment.Unresolved) != 0 {
		t.Fatalf("pinned proposal still reports the selector: %+v", proposed.Environment)
	}
	writeSetupConfigForTest(t, pinned...)
	cfg, _, err := config.LoadProjectConfig(".")
	if err != nil {
		t.Fatal(err)
	}
	check := cfg.Checks["test"]
	if want := (&config.CheckImportConfig{Candidate: "github-actions:.github/workflows/ci.yml:test", RuntimePins: map[string]string{"node": "24"}}); !reflect.DeepEqual(check.ImportedFrom, want) {
		t.Fatalf("imported_from = %+v, want %+v", check.ImportedFrom, want)
	}
	if check.RuntimeRequirements["node"] != "24" {
		t.Fatalf("saved check does not require the pinned node: %+v", check.RuntimeRequirements)
	}
	if saved := setupUnresolvedPreview(t, auth...); saved.Environment.Status != "ready" || len(saved.Environment.Unresolved) != 0 {
		t.Fatalf("saved preview reports a selector the pin replaced: %+v", saved.Environment)
	}

	saved, err := os.ReadFile(".readyci.yml")
	if err != nil {
		t.Fatal(err)
	}
	unpinned := strings.Replace(string(saved), "      node: \"24\"\n", "      node: \"installed\"\n", 1)
	if unpinned == string(saved) {
		t.Fatalf("saved configuration has no node pin to remove:\n%s", saved)
	}
	writeFileForRepoMapTest(t, ".readyci.yml", unpinned)
	preview := setupUnresolvedPreview(t, auth...)
	if preview.ConfigStatus != "ok" {
		t.Fatalf("edited configuration is not valid: %+v", preview)
	}
	if items := preview.Environment.Unresolved; preview.Environment.Status != "review_required" || len(items) != 1 ||
		items[0].Kind != setupUnresolvedRuntimeDeclarationNotApplied || items[0].Declared != "lts/*" {
		t.Fatalf("a recorded pin the check no longer applies still replaced the selector: %+v", preview.Environment)
	}
}

// What runs is the saved check, not a fresh import. When the workflow later
// declares an exact version, a fresh import would apply it, but the saved
// check still uses the runner's installed toolchain, so the preview reports
// the declaration the check does not apply.
func TestSetupSavedImportReportsAWorkflowDeclarationTheCheckDoesNotApply(t *testing.T) {
	auth := setupSavedImportFixture(t)
	writeSetupConfigForTest(t, auth...)
	workflow, err := os.ReadFile(".github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	writeFileForRepoMapTest(t, ".github/workflows/ci.yml", strings.Replace(string(workflow), "node-version: lts/*", "node-version: 26", 1))
	saved := setupUnresolvedPreview(t, auth...)
	items := saved.Environment.Unresolved
	if saved.Environment.Status != "review_required" || len(items) != 1 || items[0].Kind != setupUnresolvedRuntimeDeclarationNotApplied ||
		items[0].Subject != "node" || items[0].Declared != "26" || items[0].Check != saved.Check ||
		items[0].Source != "github-actions:.github/workflows/ci.yml:test" {
		t.Fatalf("saved preview did not report the workflow's new declaration: %+v", saved.Environment)
	}
	// Saving again applies the declaration, which resolves the item.
	writeSetupConfigForTest(t, append([]string{"--force"}, auth...)...)
	if again := setupUnresolvedPreview(t, auth...); again.Environment.Status != "ready" || len(again.Environment.Unresolved) != 0 {
		t.Fatalf("regenerated configuration still reports the declaration: %+v", again.Environment)
	}
}

// A repository declaration can narrow the runtime without anyone choosing a
// version for the selector. Only the pin replaces it, before and after saving.
func TestSetupImportedSelectorNeedsAnExactVersionInTheCheck(t *testing.T) {
	auth := setupSavedImportFixture(t)
	writeFileForRepoMapTest(t, "package.json", strings.Replace(setupAdvisoryNodeTest, "{", "{\"engines\":{\"node\":\">=20\"},", 1))
	for _, tc := range []struct {
		name   string
		args   []string
		status string
		items  int
	}{
		{name: "unpinned", status: "review_required", items: 1},
		{name: "pinned", args: []string{"--runtime", "node=24"}, status: "ready"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.Remove(".readyci.yml"); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			args := append(append([]string(nil), tc.args...), auth...)
			proposed := setupUnresolvedPreview(t, args...)
			writeSetupConfigForTest(t, args...)
			saved := setupUnresolvedPreview(t, auth...)
			for label, report := range map[string]setupReport{"proposal": proposed, "saved": saved} {
				if report.Environment.Status != tc.status || len(report.Environment.Unresolved) != tc.items {
					t.Fatalf("%s: status %q with %+v, want %q with %d items", label, report.Environment.Status, report.Environment.Unresolved, tc.status, tc.items)
				}
			}
		})
	}
}

// A hand-written check records no import, so it names no upstream job whose
// selectors could apply to it; the workflow beside it contributes nothing.
func TestSetupHandWrittenCheckGetsNoWorkflowSelectorItems(t *testing.T) {
	auth := setupSavedImportFixture(t)
	writeFileForRepoMapTest(t, ".readyci.yml", "version: 1\nproject_id: handwritten\nchecks:\n  test:\n    command: npm ci && npm test\n")
	saved := setupUnresolvedPreview(t, auth...)
	if saved.ConfigStatus != "ok" || saved.Check != "test" || saved.Environment.Status != "ready" || len(saved.Environment.Unresolved) != 0 {
		t.Fatalf("hand-written check inherited workflow selector items: %+v", saved.Environment)
	}
}

// The same holds for every repository shape: a preview of the saved
// configuration reports exactly the proposal's status and items.
func TestSetupSavedConfigurationKeepsEachShapesUnresolvedItems(t *testing.T) {
	for _, shape := range setupUnresolvedShapes {
		t.Run(shape.name, func(t *testing.T) {
			writeSetupUnresolvedShape(t, shape)
			server := setupAdvisoryCapabilityServer(t)
			auth := []string{"--api-url", server.URL, "--token", "pfl_unresolved_fixture"}
			proposed := setupUnresolvedPreview(t, append(append([]string(nil), auth...), shape.args...)...)
			writeSetupConfigForTest(t, append(append([]string(nil), auth...), shape.args...)...)
			saved := setupUnresolvedPreview(t, auth...)
			if saved.ConfigStatus != "ok" || saved.Check != proposed.Check || saved.Environment.Status != proposed.Environment.Status ||
				!reflect.DeepEqual(saved.Environment.Unresolved, proposed.Environment.Unresolved) {
				t.Fatalf("saving changed the review:\nproposed %s %+v\nsaved    %s %+v", proposed.Environment.Status, proposed.Environment.Unresolved, saved.Environment.Status, saved.Environment.Unresolved)
			}
		})
	}
}
