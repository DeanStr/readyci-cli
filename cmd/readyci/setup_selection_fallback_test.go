package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

// Ordinary repository shapes must keep a first-run check. Upstream jobs that
// setup cannot import are reported, never claimed as covered.
func TestSetupSelectionKeepsFirstRunCheckForCommonWorkflowShapes(t *testing.T) {
	const goTest = "    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v4\n      - uses: actions/setup-go@v5\n        with:\n          go-version: '1.24'\n      - run: go test ./...\n"
	for _, tc := range []struct {
		name, packageJSON, workflow string
		files                       map[string]string
		check, command, selected    string
		notImported                 []string
	}{
		{
			name:        "test job beside lint action",
			workflow:    "jobs:\n  lint:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v4\n      - uses: golangci/golangci-lint-action@v6\n  test:\n" + goTest,
			check:       "test",
			command:     "bash -e -c 'go test ./...'",
			selected:    "github-actions:.github/workflows/ci.yml:test",
			notImported: []string{"github-actions:.github/workflows/ci.yml:lint"},
		},
		{
			name:        "test job beside scheduled bot workflow",
			workflow:    "jobs:\n  test:\n" + goTest,
			files:       map[string]string{".github/workflows/stale.yml": "on:\n  schedule:\n    - cron: '0 0 * * *'\njobs:\n  stale:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/stale@v9\n"},
			check:       "test",
			command:     "bash -e -c 'go test ./...'",
			selected:    "github-actions:.github/workflows/ci.yml:test",
			notImported: []string{"github-actions:.github/workflows/stale.yml:stale"},
		},
		{
			name:        "matrix test job",
			workflow:    "jobs:\n  test:\n    runs-on: ubuntu-latest\n    strategy:\n      matrix:\n        go: ['1.24', '1.25']\n    steps:\n      - uses: actions/setup-go@v5\n        with:\n          go-version: ${{ matrix.go }}\n      - run: go test ./...\n",
			check:       "go",
			command:     "go test ./...",
			notImported: []string{"github-actions:.github/workflows/ci.yml:test"},
		},
		{
			name:        "coverage upload action",
			workflow:    "jobs:\n  test:\n" + goTest + "      - uses: codecov/codecov-action@v4\n",
			check:       "go",
			command:     "go test ./...",
			notImported: []string{"github-actions:.github/workflows/ci.yml:test"},
		},
		{
			name:        "separate unit and e2e jobs",
			packageJSON: `{"scripts":{"test":"node --test","e2e":"playwright test"}}`,
			workflow:    "jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: npm ci\n      - run: npm test\n  e2e:\n    runs-on: ubuntu-latest\n    steps:\n      - run: npm ci\n      - run: npx playwright test\n",
			check:       "node",
			command:     "npm ci && npm run test",
			notImported: []string{"github-actions:.github/workflows/ci.yml:e2e", "github-actions:.github/workflows/ci.yml:test"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			if tc.packageJSON != "" {
				writeFileForRepoMapTest(t, filepath.Join(repo, "package.json"), tc.packageJSON)
				writeFileForRepoMapTest(t, filepath.Join(repo, "package-lock.json"), `{"lockfileVersion":3,"packages":{}}`)
			} else {
				writeFileForRepoMapTest(t, filepath.Join(repo, "go.mod"), "module example.test/fixture\n\ngo 1.24\n")
			}
			writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/ci.yml"), tc.workflow)
			for name, data := range tc.files {
				writeFileForRepoMapTest(t, filepath.Join(repo, filepath.FromSlash(name)), data)
			}
			template, err := setupDetectedTemplate(repo, "fixture", setupOptions{Profile: "remote-ci"}, setupImportCandidates(repo, ""))
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := config.ParseProjectConfig([]byte(template), "generated")
			if err != nil {
				t.Fatal(err)
			}
			check, ok := cfg.Checks[tc.check]
			if !ok || len(cfg.Checks) != 1 {
				t.Fatalf("checks=%v, want only %s:\n%s", sortedCheckNames(cfg.Checks), tc.check, template)
			}
			command := check.Command
			if len(check.Steps) == 1 {
				command = check.Steps[0].Command
			}
			if command != tc.command {
				t.Fatalf("command=%q, want %q:\n%s", command, tc.command, template)
			}
			if tc.selected != "" && !strings.Contains(template, `# Selected: "`+tc.selected+`"`) {
				t.Fatalf("imported scope lost its provenance:\n%s", template)
			}
			if tc.selected == "" && (strings.Contains(template, "# Selected:") || !strings.Contains(template, "# Ready CI setup scope: language default only")) {
				t.Fatalf("language default was not labelled as such:\n%s", template)
			}
			for _, id := range tc.notImported {
				if !strings.Contains(template, `# Not imported: "`+id+`"`) {
					t.Fatalf("uncovered upstream job %s was not reported:\n%s", id, template)
				}
			}
			inferred, _, err := inferredProjectTemplateWithError(repo, "fixture")
			if err != nil || inferred != template {
				t.Fatalf("zero-config inference diverged from setup: %v\n%s", err, inferred)
			}
		})
	}
}

func TestSetupSelectionWithoutLanguageDefaultStillRequiresConfiguration(t *testing.T) {
	repo := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/ci.yml"), "jobs:\n  test:\n    runs-on: ubuntu-latest\n    strategy:\n      matrix:\n        go: ['1.24', '1.25']\n    steps:\n      - run: go test ./...\n")
	template, err := setupDetectedTemplate(repo, "fixture", setupOptions{Profile: "remote-ci"}, setupImportCandidates(repo, ""))
	if err == nil || template != "" || !strings.Contains(err.Error(), "configuration required: upstream test jobs need explicit translation") || !strings.Contains(err.Error(), "2 variants") {
		t.Fatalf("matrix without language default was guessed: %q %v", template, err)
	}
}
