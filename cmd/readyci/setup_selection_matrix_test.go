package main

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

func setupSelectionTestRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	repo := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(repo, "go.mod"), "module example.test/fixture\n\ngo 1.24\n")
	for name, data := range files {
		writeFileForRepoMapTest(t, filepath.Join(repo, filepath.FromSlash(name)), data)
	}
	return repo
}

// A nightly suite does not gate changes, so it cannot make the gating test
// job ambiguous. It stays reported, and is imported only when nothing gates.
func TestSetupSelectionIgnoresScheduledOnlyTestJobs(t *testing.T) {
	const gating = "on: [push, pull_request]\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test ./...\n"
	const nightly = "on:\n  schedule:\n    - cron: '0 6 * * *'\n  workflow_dispatch:\njobs:\n  race-tests:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test -race ./...\n"
	for _, tc := range []struct {
		name, selected string
		files          map[string]string
		notImported    []string
	}{
		{
			name:        "beside a gating test job",
			files:       map[string]string{".github/workflows/ci.yml": gating, ".github/workflows/nightly.yml": nightly},
			selected:    "github-actions:.github/workflows/ci.yml:test",
			notImported: []string{"github-actions:.github/workflows/nightly.yml:race-tests"},
		},
		{
			name:     "alone",
			files:    map[string]string{".github/workflows/nightly.yml": nightly},
			selected: "github-actions:.github/workflows/nightly.yml:race-tests",
		},
		{
			// Any other event gates; otherwise both jobs would compete.
			name:        "beside a pull_request_target job",
			files:       map[string]string{".github/workflows/ci.yml": strings.Replace(gating, "[push, pull_request]", "pull_request_target", 1), ".github/workflows/nightly.yml": nightly},
			selected:    "github-actions:.github/workflows/ci.yml:test",
			notImported: []string{"github-actions:.github/workflows/nightly.yml:race-tests"},
		},
		{
			name:  "beside a manual-only test job",
			files: map[string]string{".github/workflows/nightly.yml": nightly, ".github/workflows/manual.yml": "on: workflow_dispatch\njobs:\n  unit-tests:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test ./...\n"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := setupSelectionTestRepo(t, tc.files)
			template, err := setupDetectedTemplate(repo, "fixture", setupOptions{Profile: "remote-ci"}, setupImportCandidates(repo, ""))
			if tc.selected == "" {
				// With nothing gating, non-gating test jobs still compete.
				if err == nil && len(setupImportedCandidates(t, template)) != 0 {
					t.Fatalf("one of two non-gating test jobs was imported:\n%s", template)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := setupImportedCandidates(t, template); !reflect.DeepEqual(got, []string{tc.selected}) {
				t.Fatalf("imported %v, want %s:\n%s", got, tc.selected, template)
			}
			for _, id := range tc.notImported {
				if !strings.Contains(template, `# Not imported: "`+id+`"`) {
					t.Fatalf("scheduled job %s was not reported:\n%s", id, template)
				}
			}
		})
	}
}

func TestWorkflowImportTranslatesSetupUV(t *testing.T) {
	for _, tc := range []struct {
		name, with, unsupported string
	}{
		{name: "cache inputs", with: "\n        with:\n          enable-cache: true\n          cache-dependency-glob: uv.lock"},
		{name: "no inputs"},
		{name: "uv version", with: "\n        with:\n          version: '0.9.4'", unsupported: "astral-sh/setup-uv input version requires manual translation"},
		{name: "python selector", with: "\n        with:\n          python-version: '3.13'", unsupported: "astral-sh/setup-uv input python-version requires manual translation"},
		{name: "activated environment", with: "\n        with:\n          activate-environment: true", unsupported: "astral-sh/setup-uv input activate-environment requires manual translation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := setupSelectionTestRepo(t, map[string]string{".github/workflows/ci.yml": "jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: astral-sh/setup-uv@v7" + tc.with + "\n      - run: go test ./...\n"})
			var job *repoImportCandidate
			for _, candidate := range setupImportCandidates(repo, "") {
				if candidate.ID == "github-actions:.github/workflows/ci.yml:test" {
					job = &candidate
				}
			}
			if job == nil {
				t.Fatal("workflow job was not inventoried")
			}
			unsupported := strings.Join(append(job.Unsupported, job.AutomaticUnsupported...), "; ")
			if tc.unsupported != "" {
				if !strings.Contains(unsupported, tc.unsupported) {
					t.Fatalf("unsupported = %q, want %q", unsupported, tc.unsupported)
				}
				return
			}
			if unsupported != "" || !strings.Contains(strings.Join(job.Requirements, "; "), "astral-sh/setup-uv is satisfied by the runner image's uv") {
				t.Fatalf("setup-uv was not translated: unsupported=%q requirements=%v", unsupported, job.Requirements)
			}
		})
	}
}

// A matrix job imports one variant only when a runner admits it. Variants with
// every runtime declaration applied are offered first, in upstream order, so the
// moving "stable" selector is offered last. go.mod's floor is sealed with each pin.
func TestSetupSelectionImportsTheFirstRunnerAdmittedMatrixVariant(t *testing.T) {
	const workflow = "jobs:\n  test:\n    runs-on: ubuntu-latest\n    strategy:\n      matrix:\n        go: ['stable', '1.24', '1.25']\n    steps:\n      - uses: actions/checkout@v4\n      - uses: actions/setup-go@v5\n        with:\n          go-version: ${{ matrix.go }}\n      - run: go test ./...\n"
	for _, tc := range []struct {
		name     string
		admitted string
		known    bool
		asked    []string
		selected bool
	}{
		{name: "second applied variant admitted", admitted: "1.25,>=1.24", known: true, asked: []string{"1.24,>=1.24", "1.25,>=1.24"}, selected: true},
		{name: "no variant admitted", admitted: "none", known: true, asked: []string{"1.24,>=1.24", "1.25,>=1.24", ">=1.24"}},
		{name: "no admission answer", known: false, asked: []string{"1.24,>=1.24"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := setupSelectionTestRepo(t, map[string]string{".github/workflows/ci.yml": workflow})
			var asked []string
			options := setupOptions{Profile: "remote-ci", variantAdmission: func(template, check string) (bool, bool) {
				cfg, err := config.ParseProjectConfig([]byte(template), "variant")
				if err != nil {
					t.Fatal(err)
				}
				version := cfg.Checks[check].RuntimeRequirements["go"]
				asked = append(asked, version)
				return version == tc.admitted, tc.known
			}}
			template, err := setupDetectedTemplate(repo, "fixture", options, setupImportCandidates(repo, ""))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(asked, tc.asked) {
				t.Fatalf("admission order = %q, want %q", asked, tc.asked)
			}
			imported := setupImportedCandidates(t, template)
			if !tc.selected {
				if len(imported) != 0 || !strings.Contains(template, "# Ready CI setup scope: language default only") {
					t.Fatalf("an unadmitted variant was imported:\n%s", template)
				}
				return
			}
			if len(imported) != 1 || !strings.HasPrefix(imported[0], "github-actions:.github/workflows/ci.yml:test:matrix:") || !strings.Contains(template, `go: "1.25,>=1.24"`) {
				t.Fatalf("admitted variant was not imported: %v\n%s", imported, template)
			}
			if !strings.Contains(template, `# Selected matrix variant: "go=1.25"; sibling variants below are not run.`) || !strings.Contains(template, `; matrix: "go=1.24"`) || !strings.Contains(template, `; matrix: "go=stable"`) {
				t.Fatalf("single-variant import was not disclosed:\n%s", template)
			}
			cfg, err := config.ParseProjectConfig([]byte(template), "selected")
			if err != nil {
				t.Fatal(err)
			}
			check := cfg.Checks["test"]
			command := check.Command
			if len(check.Steps) == 1 {
				command = check.Steps[0].Command
			}
			if command != "bash -e -c 'go test ./...'" {
				t.Fatalf("selected variant command = %q:\n%s", command, template)
			}
		})
	}
}

// uv run syncs the project's locked environment and then runs its command, so
// the wrapped tox run is the test entrypoint and the sync is its preparation.
// A sibling type-check environment does not compete with it.
func TestSetupSelectionImportsAUVRunToxMatrixVariant(t *testing.T) {
	const workflow = "on: [push, pull_request]\njobs:\n  typing:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v4\n      - uses: astral-sh/setup-uv@v7\n      - run: uv run --locked --no-default-groups --group dev tox run -e typing\n  tests:\n    runs-on: ubuntu-latest\n    strategy:\n      matrix:\n        include:\n          - {python: '3.13'}\n          - {python: '3.12'}\n    steps:\n      - uses: actions/checkout@v4\n      - uses: astral-sh/setup-uv@v7\n        with:\n          enable-cache: true\n      - uses: actions/setup-python@v6\n        with:\n          python-version: ${{ matrix.python }}\n      - run: uv run --locked --no-default-groups --group dev tox run\n        env:\n          TOX_ENV: ${{ format('py{0}', matrix.python) }}\n"
	repo := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(repo, "pyproject.toml"), "[project]\nname = \"fixture\"\nversion = \"1\"\nrequires-python = \">=3.10\"\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, "uv.lock"), "version = 1\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/tests.yml"), workflow)
	var asked []string
	options := setupOptions{Profile: "remote-ci", variantAdmission: func(template, check string) (bool, bool) {
		cfg, err := config.ParseProjectConfig([]byte(template), "variant")
		if err != nil {
			t.Fatal(err)
		}
		version := cfg.Checks[check].RuntimeRequirements["python"]
		asked = append(asked, version)
		return strings.HasPrefix(version, "3.12"), true
	}}
	template, err := setupDetectedTemplate(repo, "fixture", options, setupImportCandidates(repo, ""))
	if err != nil {
		t.Fatal(err)
	}
	if imported := setupImportedCandidates(t, template); len(imported) != 1 || !strings.HasPrefix(imported[0], "github-actions:.github/workflows/tests.yml:tests:matrix:") {
		t.Fatalf("uv run tox variant was not imported (asked %q): %v\n%s", asked, imported, template)
	}
	if !strings.Contains(template, `TOX_ENV: "py3.12"`) || !strings.Contains(template, `"~/.cache/uv"`) || strings.Contains(template, "Preparation review: Python tests") {
		t.Fatalf("selected variant lost its environment or uv cache, or reported missing dependency preparation:\n%s", template)
	}
}

func TestExecutableShellCommandUnwrapsUVRun(t *testing.T) {
	for _, tc := range []struct {
		command, executable, dir string
		sync                     bool
	}{
		{command: "uv run --locked --no-default-groups --group dev tox run", executable: "tox run", sync: true},
		{command: "uv run --frozen -p 3.12 --with=pytest-xdist pytest -n 4", executable: "pytest -n 4", sync: true},
		{command: "uv run -- pytest", executable: "pytest", sync: true},
		{command: "uv run -m pytest tests", executable: "python -m pytest tests", sync: true},
		{command: "uv run --directory pkg pytest", executable: "pytest", dir: "pkg", sync: true},
		{command: "env CI=1 uv run -q pytest", executable: "pytest", sync: true},
		{command: "uv run --no-sync pytest", executable: "pytest"},
		{command: "uv run --no-project pytest", executable: "pytest"},
		{command: "uv run --script tests/run.py", executable: "python tests/run.py"},
		// Unrecognized options and other subcommands stay opaque.
		{command: "uv run --project pkg pytest", executable: "uv run --project pkg pytest"},
		{command: "uv run --unknown pytest", executable: "uv run --unknown pytest"},
		{command: "uv sync --locked", executable: "uv sync --locked"},
		{command: "uv run", executable: "uv run"},
	} {
		t.Run(tc.command, func(t *testing.T) {
			parsed := shellCommandsFromCommand(tc.command)
			if len(parsed) != 1 {
				t.Fatalf("parsed %d commands", len(parsed))
			}
			executable := executableShellCommandFromCommand(parsed[0])
			var words []string
			for _, word := range executable.Args {
				words = append(words, word.Text)
			}
			if got := strings.Join(words, " "); got != tc.executable || executable.Dir != tc.dir || executable.UVProjectSync != tc.sync {
				t.Fatalf("executable=%q dir=%q sync=%v, want %q %q %v", got, executable.Dir, executable.UVProjectSync, tc.executable, tc.dir, tc.sync)
			}
		})
	}
}

func TestSetupRecognizesToxAndNoxTestSelections(t *testing.T) {
	for command, tests := range map[string]bool{
		"tox":                              true,
		"tox run":                          true,
		"tox -p auto":                      true,
		"tox run -e py3.12":                true,
		"tox -e py312,lint":                true,
		"tox -epy312":                      true,
		"tox run -e stress-py3.14":         true,
		"tox run -e $ENV":                  true,
		"tox run -m ci":                    true,
		"tox run -e typing":                false,
		"tox -e lint,docs":                 false,
		"tox --envlist=style":              false,
		"tox run -e pre-commit":            false,
		"tox run -e docs -- -W tests":      false,
		"nox":                              true,
		"nox -s tests-3.12":                true,
		"nox -s lint tests":                true,
		"nox -k unit":                      true,
		"nox -s lint mypy":                 false,
		"nox --session=docs":               false,
		"uv run --locked tox run -e mypy":  false,
		"uv run --locked tox run -e py313": true,
	} {
		if got := setupCommandRunsTests(command); got != tests {
			t.Errorf("setupCommandRunsTests(%q) = %v, want %v", command, got, tests)
		}
	}
}
