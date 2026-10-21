package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

// These regressions retain the failure shapes observed in the popular50 audit:
// FastAPI 3070b5105fe02c4afdba5e3aea6c2cf2e3796c7f has no root Makefile;
// dotnet/runtime 3f6df375404f08acaf9162206aa59174d6b5fefb contains bundled
// zstd Makefiles; Svelte 40bb08482f4857d91221638dec544a532bc35389 has a
// non-test check script. The product must not infer repository-wide tests from
// an absent command, an unrelated component, or a build-only script.
func TestSetupSelectionDoesNotInventMakeCommand(t *testing.T) {
	repo := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(repo, "pyproject.toml"), "[project]\nname = 'app'\n")
	template := detectedProjectTemplateWithProfile(repo, "fixture", "", "remote-ci")
	cfg, err := config.ParseProjectConfig([]byte(template), "detected")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Checks) != 0 {
		t.Fatalf("unrecognized project received runnable guesses: %+v", cfg.Checks)
	}
}

func TestSetupSelectionLeavesNestedMakeTargetForExplicitImport(t *testing.T) {
	repo := t.TempDir()
	path := "src/native/external/zstd/Makefile"
	writeFileForRepoMapTest(t, filepath.Join(repo, filepath.FromSlash(path)), "check:\n\t./test-zstd\n")
	template := detectedProjectTemplateWithProfile(repo, "fixture", "", "remote-ci")
	cfg, err := config.ParseProjectConfig([]byte(template), "detected")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Checks) != 0 {
		t.Fatalf("component target became a repository default: %+v", cfg.Checks)
	}
	candidates := setupImportCandidates(repo, "make")
	if len(candidates) != 1 || candidates[0].ID != "make:"+path+":check" ||
		!strings.Contains(candidates[0].Command, "make -C src/native/external/zstd -f Makefile -- check") {
		t.Fatalf("explicit component import lost its source scope: %+v", candidates)
	}
}

func TestSetupSelectionPinnedPopular50Regressions(t *testing.T) {
	data, err := os.ReadFile("testdata/setup-selection/popular50.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Repository      string            `json:"repository"`
		Commit          string            `json:"commit"`
		Files           map[string]string `json:"files"`
		ExpectedCheck   string            `json:"expected_check"`
		ExpectedCommand string            `json:"expected_command"`
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Repository, func(t *testing.T) {
			if len(fixture.Commit) != 40 {
				t.Fatal("fixture must retain its exact upstream pin")
			}
			repo := t.TempDir()
			for name, content := range fixture.Files {
				writeFileForRepoMapTest(t, filepath.Join(repo, filepath.FromSlash(name)), content)
			}
			template := detectedProjectTemplateWithProfile(repo, "fixture", "", "remote-ci")
			cfg, err := config.ParseProjectConfig([]byte(template), "detected")
			if err != nil {
				t.Fatal(err)
			}
			if fixture.ExpectedCheck == "" {
				if len(cfg.Checks) != 0 {
					t.Fatalf("unexpected runnable guesses: %+v", cfg.Checks)
				}
			} else if got := cfg.Checks[fixture.ExpectedCheck].Command; got != fixture.ExpectedCommand {
				t.Fatalf("selected command = %q, want %q", got, fixture.ExpectedCommand)
			}
		})
	}
}

func TestSetupSelectionExecutesTheSelectedRootMakefile(t *testing.T) {
	repo := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), "check:\n\tgo test ./...\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, "GNUmakefile"), "all:\n\tprintf wrong-file\n")
	template := detectedProjectTemplateWithProfile(repo, "fixture", "", "remote-ci")
	cfg, err := config.ParseProjectConfig([]byte(template), "detected")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Checks["ci"].Command; got != "make -f Makefile -- check" {
		t.Fatalf("selected Makefile command = %q", got)
	}
}

func TestSetupSelectionDoesNotTreatNodeBuildAsTests(t *testing.T) {
	repo := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(repo, "package.json"), `{"scripts":{"build":"tsc -b"}}`)
	if template, ok := basicDetectedProjectTemplate(repo, "fixture", "", "remote-ci", false); ok {
		t.Fatalf("build-only project received automatic tests:\n%s", template)
	}
}

func TestSetupSelectionDoesNotEvaluateMakeDuringDetection(t *testing.T) {
	repo := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), "PROBE := $(shell touch detection-executed)\ncheck:\n\tgo test ./...\n")
	_ = detectedProjectTemplateWithProfile(repo, "fixture", "", "remote-ci")
	if _, err := os.Stat(filepath.Join(repo, "detection-executed")); !os.IsNotExist(err) {
		t.Fatalf("Make was evaluated during detection: %v", err)
	}
}

func TestSetupSelectionPrefersConcreteUpstreamTestJob(t *testing.T) {
	repo := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(repo, "go.mod"), "module example.test/fixture\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/ci.yml"), "jobs:\n  tests:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v4\n      - run: go mod download\n      - run: go test -race -tags=integration ./...\n        shell: bash\n")
	candidates := setupImportCandidates(repo, "")
	template, err := setupDetectedTemplate(repo, "fixture", setupOptions{Profile: "remote-ci"}, candidates)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.ParseProjectConfig([]byte(template), "detected")
	if err != nil {
		t.Fatal(err)
	}
	check := cfg.Checks["tests"]
	if len(cfg.Checks) != 1 || check.Command != "" || len(check.Steps) != 2 {
		t.Fatalf("upstream test job lost its command or preparation: %+v", cfg.Checks)
	}
	if check.Steps[0].Command != "bash -e -c 'go mod download'" || check.Steps[1].Command != "bash --noprofile --norc -eo pipefail -c 'go test -race -tags=integration ./...'" {
		t.Fatalf("upstream commands, order, or shell semantics changed: %+v", check.Steps)
	}
	for _, step := range check.Steps {
		if len(step.Env) != 1 || step.Env["CI"] != "true" || step.Always {
			t.Fatalf("upstream step environment or failure behavior changed: %+v", step)
		}
	}
	if !strings.Contains(template, "github-actions:.github/workflows/ci.yml:tests") {
		t.Fatal("selected command lost its source provenance")
	}
}

func TestSetupSelectionDoesNotGuessPastUpstreamRequirements(t *testing.T) {
	for _, tc := range []struct{ name, job, steps string }{
		{"runtime action moving alias", "", "      - uses: actions/setup-go@v5\n        with:\n          go-version: stable\n      - run: go test ./...\n"},
		{"matrix", "    strategy:\n      matrix:\n        go: [1.26, 1.27]\n", "      - run: go test ./...\n"},
		{"services", "    services:\n      db:\n        image: postgres:16\n", "      - run: go test ./...\n"},
		{"privileged preparation", "", "      - run: sudo apt-get install special-library\n      - run: go test ./...\n"},
		{"job name only", "", "      - run: printf 'go test ./...'\n"},
		{"component scope", "", "      - run: go test ./...\n        working-directory: component\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			writeFileForRepoMapTest(t, filepath.Join(repo, "go.mod"), "module example.test/fixture\n")
			writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/ci.yml"), "jobs:\n  test:\n    runs-on: ubuntu-latest\n"+tc.job+"    steps:\n"+tc.steps)
			if template, err := setupDetectedTemplate(repo, "fixture", setupOptions{Profile: "remote-ci"}, setupImportCandidates(repo, "")); err == nil || template != "" {
				t.Fatalf("guessed a command past upstream requirements: template=%q err=%v", template, err)
			}
		})
	}
}

func TestSetupSelectionRejectsAmbiguousUpstreamTestJobs(t *testing.T) {
	repo := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/ci.yml"), "jobs:\n  unit:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test ./...\n  integration:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test -tags=integration ./...\n")
	_, err := setupDetectedTemplate(repo, "fixture", setupOptions{Profile: "remote-ci"}, setupImportCandidates(repo, ""))
	if err == nil || !strings.Contains(err.Error(), "multiple upstream test jobs") {
		t.Fatalf("ambiguous selection: %v", err)
	}
}

func TestSetupSelectionDoesNotDropBlockedUpstreamTestJobs(t *testing.T) {
	for _, tc := range []struct{ name, integrationStep string }{
		{"recognized test command", "go test -tags=integration ./..."},
		{"opaque integration command", "./ci/integration"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/ci.yml"), "jobs:\n  unit:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test ./...\n  integration:\n    runs-on: ubuntu-latest\n    services:\n      db:\n        image: postgres:16\n    steps:\n      - run: "+tc.integrationStep+"\n")
			template, err := setupDetectedTemplate(repo, "fixture", setupOptions{Profile: "remote-ci"}, setupImportCandidates(repo, ""))
			if err == nil || template != "" || !strings.Contains(err.Error(), "multiple upstream test jobs") || !strings.Contains(err.Error(), ":integration") {
				t.Fatalf("silently selected partial upstream scope: template=%q err=%v", template, err)
			}
		})
	}
}

func TestSetupCommandRunsTestsUsesActualInvocation(t *testing.T) {
	for _, tc := range []struct {
		command string
		tests   bool
	}{
		{"python -m pip install pytest", false},
		{"python -m pip install unittest", false},
		{"python -c 'print(1)' -m pytest", false},
		{"python script.py -m pytest", false},
		{"python -m \"$MODULE\" pytest", false},
		{"python \"$OPTION\" -m pytest", false},
		{"python -W pytest -m pip install unittest", false},
		{"python -m pytest -q", true},
		{"python3 -I -W error -m unittest discover", true},
		{"npm install test", false},
		{"npm run build -- test", false},
		{"npm run \"$SCRIPT\" test", false},
		{"npm --prefix test install", false},
		{"pnpm add test", false},
		{"pnpm --filter test install", false},
		{"yarn add test", false},
		{"bun install test", false},
		{"npm test", true},
		{"npm --prefix web run test:unit", true},
		{"pnpm -r test", true},
		{"pnpm --filter web test:unit", true},
		{"yarn --cwd web test", true},
		{"bun run test:unit", true},
		{"pnpm exec vitest run", true},
		{"npm exec -- vitest run", true},
		{"pnpm playwright test", true},
		{"npx playwright test", true},
		{"npm ci && npm test", true},
		{"python -m pip install pytest && python -m pytest", true},
	} {
		t.Run(tc.command, func(t *testing.T) {
			if got := setupCommandRunsTests(tc.command); got != tc.tests {
				t.Fatalf("setupCommandRunsTests(%q) = %t, want %t", tc.command, got, tc.tests)
			}
		})
	}
}

func TestSetupSelectionDoesNotImportDependencyInstallationAsTests(t *testing.T) {
	for _, command := range []string{"python -m pip install pytest", "npm install test"} {
		t.Run(command, func(t *testing.T) {
			repo := t.TempDir()
			writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/ci.yml"), "jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: "+command+"\n")
			template, err := setupDetectedTemplate(repo, "fixture", setupOptions{Profile: "remote-ci"}, setupImportCandidates(repo, ""))
			if err == nil || template != "" {
				t.Fatalf("dependency install became an automatic test job: template=%q err=%v", template, err)
			}
		})
	}
}

func TestSetupSelectionFailsBeforeWritingUnknownProject(t *testing.T) {
	chdirCLITestRepo(t, "unknown-project")
	t.Setenv("READYCI_CONFIG_DIR", t.TempDir())
	t.Setenv("READYCI_API_TOKEN", "")
	_, err := buildSetupReport(setupOptions{APIURL: "http://127.0.0.1:1", ProjectID: "fixture", Detect: true, WriteConfig: true})
	if err == nil || !strings.Contains(err.Error(), "configuration required") {
		t.Fatalf("unsupported project: %v", err)
	}
	if _, err := os.Stat(".readyci.yml"); !os.IsNotExist(err) {
		t.Fatalf("unsupported selection wrote a configuration: %v", err)
	}
}

func TestSetupSelectionRejectsMissingAndConditionalMakeRules(t *testing.T) {
	for _, tc := range []struct {
		name, makefile, command string
		blocked                 bool
	}{
		{"missing target", "all:\n\tgo build ./...\n", "make test", true},
		{"nested target cannot satisfy root", "test:\n\tgo test ./...\n", "make test", true},
		{"conditional target", "ifdef ENABLE_TESTS\ntest:\n\tgo test ./...\nendif\n", "make test", true},
		{"variable body", "define GENERATED\ntest:\n\tgo test ./...\nendef\n", "make test", true},
		{"declared target", "test:\n\tgo test ./...\n", "make test", false},
		{"exact selected file", "test:\n\tgo test ./...\n", "make -f Makefile test", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			file := "Makefile"
			if tc.name == "nested target cannot satisfy root" {
				file = "sub/Makefile"
			}
			writeFileForRepoMapTest(t, filepath.Join(repo, file), tc.makefile)
			writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/ci.yml"), "jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: "+tc.command+"\n")
			candidates := setupImportCandidates(repo, "github-actions")
			if len(candidates) != 1 {
				t.Fatalf("candidates=%+v", candidates)
			}
			if (len(candidates[0].Unsupported) > 0) != tc.blocked {
				t.Fatalf("Make target validation: %+v", candidates[0])
			}
		})
	}
}

func TestSetupSelectionRejectsDroppedWorkflowSemantics(t *testing.T) {
	for _, tc := range []struct{ name, job, step, action string }{
		{"job failure handling", "    continue-on-error: true\n", "", ""},
		{"dynamic job deadline", "    timeout-minutes: ${{ vars.TIMEOUT }}\n", "", ""},
		{"invalid job deadline", "    timeout-minutes: 0\n", "", ""},
		{"dynamic step deadline", "", "        timeout-minutes: ${{ vars.TIMEOUT }}\n", ""},
		{"invalid step deadline", "", "        timeout-minutes: -1\n", ""},
		{"absolute directory", "", "        working-directory: /outside\n", ""},
		{"traversing directory", "", "        working-directory: ../outside\n", ""},
		{"toolchain architecture", "", "", "      - uses: actions/setup-go@v5\n        with:\n          architecture: arm64\n"},
		{"required cache", "", "", "      - uses: actions/cache@v4\n        with:\n          fail-on-cache-miss: true\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/ci.yml"), "jobs:\n  test:\n    runs-on: ubuntu-latest\n"+tc.job+"    steps:\n"+tc.action+"      - run: go test ./...\n"+tc.step)
			candidates := setupImportCandidates(repo, "github-actions")
			if len(candidates) != 1 || len(candidates[0].Unsupported) == 0 {
				t.Fatalf("untranslated requirement accepted: %+v", candidates)
			}
		})
	}
}
