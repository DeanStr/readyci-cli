package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/verificationplan"
)

const setupSveltePnpmIntegrityPin = "10.33.4+sha512.1c67b3b359b2d408119ba1ed289f34b8fc3c6873412bec6fd264fbdc82489e510fcbecb9ce9d22dae7f3b76269d8441046014bdca53b9979cd7a561ad631b800"

func TestSetupPinnedPackageManagerMetadataSurvivesWorkflowImport(t *testing.T) {
	var provenance []struct {
		Repository, Commit, SHA256 string
		SourcePath                 string `json:"source_path"`
		SourceURL                  string `json:"source_url"`
		FixturePath                string `json:"fixture_path"`
	}
	root := "testdata/setup-package-manager"
	metadata, err := os.ReadFile(filepath.Join(root, "provenance.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(metadata, &provenance); err != nil {
		t.Fatal(err)
	}
	if len(provenance) != 2 {
		t.Fatalf("expected both pinned source repositories, got %d", len(provenance))
	}
	for _, fixture := range provenance {
		t.Run(fixture.Repository, func(t *testing.T) {
			if len(fixture.Commit) != 40 || fixture.SourcePath != "package.json" || fixture.SourceURL != "https://raw.githubusercontent.com/"+fixture.Repository+"/"+fixture.Commit+"/package.json" {
				t.Fatalf("fixture lost pinned upstream provenance: %+v", fixture)
			}
			data, err := os.ReadFile(filepath.Join(root, fixture.FixturePath))
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprintf("%x", sha256.Sum256(data)) != fixture.SHA256 {
				t.Fatal("pinned package.json fixture changed")
			}
			repo := t.TempDir()
			writeFileForRepoMapTest(t, filepath.Join(repo, "package.json"), string(data))
			pin := map[string]string{"sveltejs/svelte": setupSveltePnpmIntegrityPin, "vuejs/core": "12.11.2"}[fixture.Repository]
			if pin == "" {
				t.Fatalf("unexpected fixture repository %s", fixture.Repository)
			}
			requirements, notes := setupDeclaredRuntimeRequirements(repo, []string{"pnpm test"})
			if len(notes) != 0 || requirements["pnpm"] != pin {
				t.Fatalf("package manager declaration lost integrity/version: %v notes=%v", requirements, notes)
			}
			writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/tests.yml"), "jobs:\n  tests:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: pnpm/action-setup@v4\n      - run: pnpm install --frozen-lockfile\n      - run: pnpm test\n")
			candidates := setupImportCandidates(repo, "github-actions")
			if len(candidates) != 1 || len(candidates[0].Unsupported) > 0 || candidates[0].RuntimeRequirements["pnpm"] != pin {
				t.Fatalf("package.json fallback lost the exact package manager pin: %+v", candidates)
			}
			template, err := setupDetectedTemplate(repo, "fixture", setupOptions{Profile: "remote-ci", Image: "ubuntu-24.04-ci"}, candidates)
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := config.ParseProjectConfig([]byte(template), "imported")
			if err != nil || !preflightapi.RuntimeRequirementImplies(cfg.Checks["tests"].RuntimeRequirements["pnpm"], pin) {
				t.Fatalf("generated config lost integrity/version: cfg=%+v err=%v", cfg, err)
			}
			plan, err := verificationplan.ResolveCheck("tests", cfg, verificationplan.ResolveOptions{})
			if err != nil || plan.Run.RuntimeRequirements["pnpm"] != cfg.Checks["tests"].RuntimeRequirements["pnpm"] || len(plan.Run.Steps) != 2 {
				t.Fatalf("resolved plan lost integrity/version or upstream steps: plan=%+v err=%v", plan, err)
			}
			goOnly, notes := setupDeclaredRuntimeRequirements(repo, []string{"go test ./..."})
			if len(notes) != 0 || len(goOnly) != 1 || goOnly["go"] != "installed" {
				t.Fatalf("unrelated Node declaration constrained a Go-only scope: %v notes=%v", goOnly, notes)
			}
		})
	}
}

func TestSetupPnpmWorkflowVersionPreservesRepositoryIntegrity(t *testing.T) {
	for _, version := range []string{"10", "10.33.4", setupSveltePnpmIntegrityPin} {
		t.Run(version, func(t *testing.T) {
			repo := t.TempDir()
			writeFileForRepoMapTest(t, filepath.Join(repo, "package.json"), `{"packageManager":"pnpm@`+setupSveltePnpmIntegrityPin+`"}`)
			job := repoWorkflowJobYAML{Steps: []repoWorkflowStepYAML{{Uses: "pnpm/action-setup@v4", With: map[string]any{"version": version}}}}
			workflow, _, _, problems := setupWorkflowRuntimeRequirements(repo, job)
			if len(problems) > 0 {
				t.Fatalf("literal pnpm action was blocked: %v", problems)
			}
			candidate := repoImportCandidate{Source: "github-actions", ID: "github-actions:tests.yml:tests", Name: "tests", Command: "pnpm test", RuntimeRequirements: workflow}
			template := setupImportedTemplateForRoot(repo, setupOptions{Profile: "remote-ci"}, "fixture", candidate)
			cfg, err := config.ParseProjectConfig([]byte(template), "imported")
			if err != nil || cfg.Checks["tests"].RuntimeRequirements["pnpm"] != setupSveltePnpmIntegrityPin {
				t.Fatalf("workflow version erased repository integrity: cfg=%+v err=%v", cfg, err)
			}
			if !preflightapi.RuntimeRequirementImplies(cfg.Checks["tests"].RuntimeRequirements["pnpm"], setupSveltePnpmIntegrityPin) || preflightapi.RuntimeRequirementImplies("10.33.4", setupSveltePnpmIntegrityPin) {
				t.Fatal("version-only compatibility was mistaken for package integrity")
			}
		})
	}
}

// Conflicting or unprovable package-manager pins are never sealed; they are
// disclosed as review notes and the remaining pin stays enforced.
func TestSetupPnpmIntegrityConflictsAreReviewNotes(t *testing.T) {
	for _, test := range []struct{ version, want, note string }{
		{"11.15.1", "11.15.1", "package.json (packageManager)"},
		{"10.33.4+sha512." + strings.Repeat("f", 128), setupSveltePnpmIntegrityPin, "no approved Ready CI pnpm archive matches"},
	} {
		t.Run(test.version, func(t *testing.T) {
			repo := t.TempDir()
			writeFileForRepoMapTest(t, filepath.Join(repo, "package.json"), `{"packageManager":"pnpm@`+setupSveltePnpmIntegrityPin+`"}`)
			writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/tests.yml"), "jobs:\n  tests:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: pnpm/action-setup@v4\n        with:\n          version: '"+test.version+"'\n      - run: pnpm test\n")
			candidates := setupImportCandidates(repo, "github-actions")
			if len(candidates) != 1 || len(candidates[0].Unsupported) != 0 {
				t.Fatalf("package-manager declaration blocked import: %+v", candidates)
			}
			template := setupImportedTemplateForRoot(repo, setupOptions{Profile: "remote-ci"}, "fixture", candidates[0])
			cfg, err := config.ParseProjectConfig([]byte(template), "imported")
			if err != nil || cfg.Checks["tests"].RuntimeRequirements["pnpm"] != test.want || !strings.Contains(template, test.note) {
				t.Fatalf("conflicting pin was sealed or hidden: cfg=%+v err=%v\n%s", cfg.Checks["tests"], err, template)
			}
		})
	}
}

func TestSetupPackageManagerMetadataRequiresSupportedCompleteIntegrity(t *testing.T) {
	for _, value := range []string{
		"pnpm@10.33.4+sha512.abcd",
		"pnpm@10.33.4+sha256." + strings.Repeat("a", 64),
		"pnpm@10.33+sha512." + strings.Repeat("a", 128),
		"pnpm@10.33.4+sha512." + strings.Repeat("A", 128),
		"pnpm@10.33.4+build.metadata",
		"npm@12.0.0+sha512." + strings.Repeat("a", 128),
		"yarn@4.9.2+sha512." + strings.Repeat("a", 128),
	} {
		repo := t.TempDir()
		data, err := json.Marshal(map[string]string{"packageManager": value})
		if err != nil {
			t.Fatal(err)
		}
		writeFileForRepoMapTest(t, filepath.Join(repo, "package.json"), string(data))
		if requirements, notes := setupDeclaredRuntimeRequirements(repo, []string{"pnpm test"}); len(notes) == 0 {
			t.Fatalf("unsupported package manager metadata became version-only selection: value=%s requirements=%v", value, requirements)
		}
		if strings.HasPrefix(value, "pnpm@") {
			version := strings.TrimPrefix(value, "pnpm@")
			if requirements, unresolved, notes, _ := setupWorkflowRuntimeRequirements(repo, repoWorkflowJobYAML{Steps: []repoWorkflowStepYAML{{Uses: "pnpm/action-setup@v4", With: map[string]any{"version": version}}}}); len(requirements) != 0 || len(unresolved) != 1 || len(notes) != 0 {
				t.Fatalf("unsupported action integrity became version-only selection: value=%s requirements=%v notes=%v", value, requirements, notes)
			}
		}
	}
}
