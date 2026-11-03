package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestSetupTranslatesStaticWorkflowRuntimeActions(t *testing.T) {
	for _, tc := range []struct{ name, action, inputs, run, runtime, version string }{
		{"Go", "actions/setup-go@v5", "go-version: '1.25'", "go test -race ./...", "go", "1.25"},
		{"Node", "actions/setup-node@v4", "node-version: '24'", "npm ci && npm test", "node", "24"},
		{"Python", "actions/setup-python@v5", "python-version: '3.12'", "python -m pip install -r requirements-dev.txt && python -m pytest tests", "python", "3.12"},
		{"pnpm", "pnpm/action-setup@v4", "version: '10.11.0'", "pnpm install --frozen-lockfile && pnpm test", "pnpm", "10.11.0"},
		{"Go npm comparator", "actions/setup-go@v5", "go-version: '>1.25'", "go test ./...", "go", ">=1.26.0"},
		{"Python npm comparator", "actions/setup-python@v5", "python-version: '>3.12'", "python -m unittest discover", "python", ">=3.13.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/ci.yml"), "jobs:\n  tests:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: "+tc.action+"\n        with:\n          "+tc.inputs+"\n      - run: "+tc.run+"\n")
			candidates := setupImportCandidates(repo, "github-actions")
			if len(candidates) != 1 || len(candidates[0].Unsupported) != 0 || len(candidates[0].AutomaticUnsupported) != 0 {
				t.Fatalf("static runtime action requires manual translation: %+v", candidates)
			}
			template, err := setupDetectedTemplate(repo, "fixture", setupOptions{Profile: "remote-ci"}, candidates)
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := config.ParseProjectConfig([]byte(template), "generated")
			if err != nil {
				t.Fatal(err)
			}
			check := cfg.Checks["tests"]
			if check.RuntimeRequirements[tc.runtime] != tc.version || check.Command != "" || len(check.Steps) != 1 {
				t.Fatalf("runtime or test scope was lost: %+v", check)
			}
			step := check.Steps[0]
			if step.Command != "bash -e -c "+shellQuoteArg(tc.run) || len(step.Env) != 1 || step.Env["CI"] != "true" || step.Always {
				t.Fatalf("imported command, shell, environment, or failure behavior changed: %+v", step)
			}
			if tc.runtime == "python" && !strings.Contains(strings.Join(check.Prepare, "\n"), "python3 -m venv") {
				t.Fatal("setup-python did not prepare the genuine python alias")
			}
		})
	}
}

func TestSetupWorkflowCheckLatestRetainsNumericRequirements(t *testing.T) {
	for _, tc := range []struct{ name, action, inputs, run, runtime, required, matching, other, file, contents string }{
		{"Node", "actions/setup-node@v4", "node-version: '22'", "npm ci && npm test", "node", "22", "22.23.1", "24.18.0", "package.json", `{"engines":{"node":">=20"},"scripts":{"test":"node --test"}}`},
		{"Go", "actions/setup-go@v5", "go-version: '1.25'", "go test ./...", "go", "1.25", "1.25.13", "1.26.0", "", ""},
		{"Python", "actions/setup-python@v5", "python-version: '3.12'", "python -m unittest discover", "python", "3.12", "3.12.1", "3.13.0", "", ""},
		{"Node version file", "actions/setup-node@v4", "node-version-file: .nvmrc", "npm test", "node", "22", "22.23.1", "24.18.0", ".nvmrc", "22\n"},
		{"Go version file", "actions/setup-go@v5", "go-version-file: go.mod", "go test ./...", "go", "1.25", "1.25.13", "1.26.0", "go.mod", "module fixture\ngo 1.25\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			if tc.file != "" {
				writeFileForRepoMapTest(t, filepath.Join(repo, tc.file), tc.contents)
			}
			writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/ci.yml"), "jobs:\n  tests:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: "+tc.action+"\n        with:\n          "+tc.inputs+"\n          check-latest: true\n      - run: "+tc.run+"\n")
			candidates := setupImportCandidates(repo, "github-actions")
			if len(candidates) != 1 || len(candidates[0].Unsupported) != 0 || candidates[0].RuntimeRequirements[tc.runtime] != tc.required {
				t.Fatalf("check-latest lost the numeric workflow requirement: %+v", candidates)
			}
			template, err := setupDetectedTemplate(repo, "fixture", setupOptions{Profile: "remote-ci"}, candidates)
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := config.ParseProjectConfig([]byte(template), "check-latest")
			if err != nil {
				t.Fatal(err)
			}
			requirement := cfg.Checks["tests"].RuntimeRequirements[tc.runtime]
			if !preflightapi.RuntimeVersionSatisfies(tc.matching, requirement) || preflightapi.RuntimeVersionSatisfies(tc.other, requirement) {
				t.Fatalf("generated requirement changed the selected runtime series: %q", requirement)
			}
			if !strings.Contains(template, "check-latest freshness is not guaranteed") || strings.Contains(template, "is not emitted") {
				t.Fatalf("generated configuration misrepresents runtime selection or freshness:\n%s", template)
			}
		})
	}
}

// A runtime selector setup cannot translate is advisory: the candidate stays
// importable, no requirement is emitted for it, and the generated
// configuration carries a review note.
func TestSetupWorkflowRuntimeSelectorsWithoutNumericTranslationAreReviewNotes(t *testing.T) {
	for _, tc := range []struct{ name, action, inputs, runtime string }{
		{"moving alias", "actions/setup-node@v4", "node-version: lts/*", "node"},
		{"named alias", "actions/setup-go@v5", "go-version: stable", "go"},
		{"implicit default", "actions/setup-node@v4", "", "node"},
		{"missing version file", "actions/setup-go@v5", "go-version-file: go.mod", "go"},
		{"custom version file", "actions/setup-go@v5", "go-version-file: sdk/go.mod", "go"},
		{"ambiguous YAML float", "actions/setup-python@v5", "python-version: 3.10", "python"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/ci.yml"), "jobs:\n  tests:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: "+tc.action+"\n        with:\n          "+tc.inputs+"\n      - run: go test ./...\n")
			candidates := setupImportCandidates(repo, "github-actions")
			if len(candidates) != 1 || len(candidates[0].Unsupported) != 0 || candidates[0].RuntimeRequirements[tc.runtime] != "" || len(candidates[0].Unresolved) != 1 || len(candidates[0].RuntimeNotes) != 0 {
				t.Fatalf("untranslated selector was emitted or blocked import: %+v", candidates)
			}
			template, err := setupDetectedTemplate(repo, "fixture", setupOptions{Profile: "remote-ci"}, candidates)
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := config.ParseProjectConfig([]byte(template), "generated")
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.Checks["tests"].RuntimeRequirements[tc.runtime]; got != "" && got != "installed" || !strings.Contains(template, "# - \"Runtime review: "+strings.Split(tc.action, "@")[0]) {
				t.Fatalf("untranslated selector became a requirement or lost its note: %q\n%s", got, template)
			}
		})
	}
}

func TestSetupWorkflowRuntimeActionsKeepUnsupportedSemanticsExplicit(t *testing.T) {
	for _, tc := range []struct{ name, action, inputs string }{
		{"dynamic", "actions/setup-node@v4", "node-version: '${{ vars.NODE_VERSION }}'"},
		{"unknown input", "actions/setup-python@v5", "python-version: '3.12'\n          architecture: arm64"},
		{"pnpm install sideeffect", "pnpm/action-setup@v4", "version: '10.11.0'\n          run_install: true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/ci.yml"), "jobs:\n  tests:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: "+tc.action+"\n        with:\n          "+tc.inputs+"\n      - run: go test ./...\n")
			candidates := setupImportCandidates(repo, "github-actions")
			if len(candidates) != 1 || len(candidates[0].Unsupported) == 0 {
				t.Fatalf("unsupported action semantics were silently omitted: %+v", candidates)
			}
			if template, err := setupDetectedTemplate(repo, "fixture", setupOptions{Profile: "remote-ci"}, candidates); err == nil || template != "" {
				t.Fatalf("unsupported runtime action was automatically selected: %q %v", template, err)
			}
		})
	}
}

func TestSetupWorkflowRuntimeFilesAndRepositoryDeclarationsAreConjoined(t *testing.T) {
	repo := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(repo, "go.mod"), "module fixture\ngo 1.24\ntoolchain go1.25.13\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/ci.yml"), "jobs:\n  tests:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/setup-go@v5\n        with:\n          go-version-file: go.mod\n      - run: go test ./...\n")
	candidates := setupImportCandidates(repo, "github-actions")
	if len(candidates) != 1 || candidates[0].RuntimeRequirements["go"] != "1.25.13" || len(candidates[0].Unsupported) != 0 {
		t.Fatalf("Go action version-file was not preserved: %+v", candidates)
	}
	template, err := setupDetectedTemplate(repo, "fixture", setupOptions{Profile: "remote-ci"}, candidates)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.ParseProjectConfig([]byte(template), "generated")
	if err != nil {
		t.Fatal(err)
	}
	requirement := cfg.Checks["tests"].RuntimeRequirements["go"]
	if !preflightapi.RuntimeVersionSatisfies("1.25.13", requirement) || preflightapi.RuntimeVersionSatisfies("1.26.0", requirement) || preflightapi.RuntimeVersionSatisfies("1.24.0", requirement) {
		t.Fatalf("workflow pin or repository requirements lost: %q", requirement)
	}
}

func TestSetupWorkflowRuntimeFilesDoNotFollowSymlinks(t *testing.T) {
	repo := t.TempDir()
	outside := filepath.Join(t.TempDir(), ".nvmrc")
	writeFileForRepoMapTest(t, outside, "24\n")
	if err := os.Symlink(outside, filepath.Join(repo, ".nvmrc")); err != nil {
		t.Fatal(err)
	}
	writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/ci.yml"), "jobs:\n  tests:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/setup-node@v4\n        with:\n          node-version-file: .nvmrc\n      - run: npm test\n")
	candidates := setupImportCandidates(repo, "github-actions")
	if len(candidates) != 1 || candidates[0].RuntimeRequirements["node"] != "" || !strings.Contains(strings.Join(setupUnresolvedNotes(candidates[0].Unresolved), ";"), "node-version-file .nvmrc") {
		t.Fatalf("symlinked runtime declaration was imported: %+v", candidates)
	}
}

func TestSetupOpaqueHarnessKeepsActionRuntimeAndRepositoryMinimum(t *testing.T) {
	repo := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(repo, "go.mod"), "module fixture\ngo 1.24\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/ci.yml"), "jobs:\n  tests:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/setup-go@v5\n        with:\n          go-version: '1.25'\n      - run: ./scripts/test\n")
	template, err := setupDetectedTemplate(repo, "fixture", setupOptions{Profile: "remote-ci"}, setupImportCandidates(repo, "github-actions"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.ParseProjectConfig([]byte(template), "generated")
	if err != nil {
		t.Fatal(err)
	}
	requirement := cfg.Checks["tests"].RuntimeRequirements["go"]
	if !strings.Contains(requirement, ">=1.24") || !preflightapi.RuntimeVersionSatisfies("1.25.13", requirement) || preflightapi.RuntimeVersionSatisfies("1.26.0", requirement) {
		t.Fatalf("opaque harness lost action or repository requirement: %q", requirement)
	}
}

func TestSetupImportedPythonScopeDoesNotRequireUnrelatedNodeEngine(t *testing.T) {
	repo := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), "init:\n\tpython -m pip install -r requirements-dev.txt\ntest:\n\tpython -m pytest tests\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, "requirements-dev.txt"), "pytest\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, "pyproject.toml"), "[project]\nrequires-python = '>=3.10'\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, "package.json"), `{"engines":{"node":">=999"}}`)
	candidates := setupImportCandidates(repo, "make")
	if len(candidates) != 1 {
		t.Fatalf("candidates=%+v", candidates)
	}
	template := setupImportedTemplateForRoot(repo, setupOptions{Profile: "remote-ci"}, "fixture", candidates[0])
	cfg, err := config.ParseProjectConfig([]byte(template), "generated")
	if err != nil {
		t.Fatal(err)
	}
	// Make recipes are opaque: neither the Python recipe nor the unrelated
	// frontend declaration becomes a runtime requirement.
	if requirements := cfg.Checks["test"].RuntimeRequirements; len(requirements) != 0 {
		t.Fatalf("Make import inferred runtimes from recipes or unrelated declarations: %v", requirements)
	}
}
