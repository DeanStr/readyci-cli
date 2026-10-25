package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestWorkflowStaticMatrixRetainsParentAndEveryVariant(t *testing.T) {
	repo := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/tests.yml"), `jobs:
  test:
    runs-on: ${{ matrix.os }}
    strategy:
      fail-fast: false
      matrix:
        os: [ubuntu-latest, windows-latest, macos-latest]
    steps:
      - run: go test ./...
`)
	candidates := setupImportCandidates(repo, "github-actions")
	if len(candidates) != 4 {
		t.Fatalf("matrix scope was not retained: got %d candidates, want blocked parent plus 3 explicit variants: %+v", len(candidates), candidates)
	}
	parent := "github-actions:.github/workflows/tests.yml:test"
	var ubuntu, unsupported int
	for _, candidate := range candidates {
		if candidate.ID == parent {
			if len(candidate.Unsupported) == 0 {
				t.Fatal("whole matrix became implicitly runnable")
			}
			continue
		}
		if candidate.ParentJobID != parent || !strings.HasPrefix(candidate.ID, parent+":matrix:") || len(strings.TrimPrefix(candidate.ID, parent+":matrix:")) != 64 {
			t.Fatalf("missing stable parent/variant identity: %+v", candidate)
		}
		if candidate.Matrix["os"] == "ubuntu-latest" {
			ubuntu++
			if len(candidate.Unsupported) != 0 {
				t.Fatalf("static Ubuntu variant was blocked: %+v", candidate)
			}
		} else if len(candidate.Unsupported) != 0 {
			unsupported++
		}
	}
	if ubuntu != 1 || unsupported != 2 {
		t.Fatalf("OS variants disappeared: ubuntu=%d unsupported=%d", ubuntu, unsupported)
	}
	if selection := automaticSetupSelection(candidates); selection.Imported != nil || len(selection.NotImported) != 1 || selection.NotImported[0].ID != parent {
		t.Fatalf("automatic setup silently selected a matrix variant: %+v", selection)
	}
	if repeated := setupImportCandidates(repo, "github-actions"); !reflect.DeepEqual(candidates, repeated) {
		t.Fatal("matrix scan was nondeterministic")
	}
}

func TestWorkflowMalformedYAMLLanguageFallbackReportsUncoveredScope(t *testing.T) {
	repo := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(repo, "go.mod"), "module fixture\n\ngo 1.25.13\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/tests.yml"), "jobs: [invalid YAML\n")
	candidates := setupImportCandidates(repo, "github-actions")
	template, err := setupDetectedTemplate(repo, "fixture", setupOptions{Profile: "remote-ci"}, candidates)
	if err != nil || !strings.Contains(template, `# Not imported: "github-actions:.github/workflows/tests.yml:uninspectable"; blocked: "workflow YAML cannot be parsed`) {
		t.Fatalf("uninspectable workflow silently fell back to a different test scope: %v\n%s", err, template)
	}
}

func TestWorkflowScannerOnlyReadsImmediateWorkflowFiles(t *testing.T) {
	repo := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/tests.yml"), "jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test ./...\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/scripts/spam-detection/eval-prompts.yml"), "prompt: [not workflow YAML\n")
	candidates := setupImportCandidates(repo, "github-actions")
	if len(candidates) != 1 {
		t.Fatalf("nested workflow support data was imported as execution scope: %+v", candidates)
	}
	if selected := automaticSetupSelection(candidates).Imported; selected == nil {
		t.Fatal("nested support data blocked the actual workflow")
	}
}

func TestWorkflowUnknownBuildScopeLanguageFallbackReportsUncoveredScope(t *testing.T) {
	for _, source := range []struct{ file, job string }{{"ci.yml", "build"}, {"quality.yml", "suite"}} {
		for _, steps := range []string{
			"    steps: []\n",
			"    steps:\n      - parallel:\n          - run: go test ./...\n",
			"    steps:\n      - uses: example/untranslated-tests@v1\n",
			"    uses: ./.github/workflows/reusable.yml\n",
			"    steps:\n      - run: ${{ inputs.command }}\n",
		} {
			repo := t.TempDir()
			writeFileForRepoMapTest(t, filepath.Join(repo, "go.mod"), "module fixture\n\ngo 1.25.13\n")
			writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows", source.file), "jobs:\n  "+source.job+":\n    runs-on: ubuntu-latest\n"+steps)
			candidates := setupImportCandidates(repo, "github-actions")
			if len(candidates) != 1 || !candidates[0].ScopeUnknown {
				t.Fatalf("unknown execution structure lost its scope diagnostic: %+v", candidates)
			}
			template, err := setupDetectedTemplate(repo, "fixture", setupOptions{Profile: "remote-ci"}, candidates)
			if err != nil || !strings.Contains(template, "# Ready CI setup scope: language default only") || !strings.Contains(template, "# Not imported: "+strconv.Quote(candidates[0].ID)) {
				t.Fatalf("uninspectable build job silently fell back to generic Go tests: %v\n%s", err, template)
			}
		}
	}
}

func workflowMatrixForTest(t *testing.T, strategy string) ([]setupWorkflowMatrixVariant, []string) {
	t.Helper()
	var workflow repoWorkflowYAML
	if err := yaml.Unmarshal([]byte("jobs:\n  test:\n    runs-on: ubuntu-latest\n    strategy:\n"+strategy+"    steps:\n      - run: go test ./...\n"), &workflow); err != nil {
		t.Fatal(err)
	}
	return setupWorkflowMatrixVariants(workflow, workflow.Jobs["test"])
}

func TestWorkflowMatrixExclusionsPreserveLiteralPartialMatches(t *testing.T) {
	for _, tc := range []struct {
		exclude string
		want    int
	}{
		{"          - os: windows-latest\n", 2},
		{"          - os: windows-latest\n            go: '1.26'\n", 3},
	} {
		variants, problems := workflowMatrixForTest(t, "      matrix:\n        os: [ubuntu-latest, windows-latest]\n        go: ['1.26', '1.27']\n        exclude:\n"+tc.exclude)
		if len(problems) != 0 || len(variants) != tc.want {
			t.Fatalf("partial exclusion changed matrix scope: got %d problems=%v", len(variants), problems)
		}
	}
}

func TestWorkflowMatrixIncludeOverlaysPreserveOriginalAxes(t *testing.T) {
	// This is the documented GitHub Actions overlay example. Later includes may
	// overwrite fields added by earlier includes, but never original axis values
	// or rows appended by earlier includes.
	variants, problems := workflowMatrixForTest(t, `      matrix:
        fruit: [apple, pear]
        animal: [cat, dog]
        include:
          - color: green
          - color: pink
            animal: cat
          - fruit: apple
            shape: circle
          - fruit: banana
          - fruit: banana
            animal: cat
`)
	want := []map[string]string{
		{"fruit": "apple", "animal": "cat", "color": "pink", "shape": "circle"},
		{"fruit": "apple", "animal": "dog", "color": "green", "shape": "circle"},
		{"fruit": "pear", "animal": "cat", "color": "pink"},
		{"fruit": "pear", "animal": "dog", "color": "green"},
		{"fruit": "banana"},
		{"fruit": "banana", "animal": "cat"},
	}
	if len(problems) != 0 || len(variants) != len(want) {
		t.Fatalf("documented overlay expansion failed: %d variants, problems=%v", len(variants), problems)
	}
	for i, variant := range variants {
		if !reflect.DeepEqual(variant.Values, want[i]) {
			t.Errorf("variant %d = %v, want %v", i, variant.Values, want[i])
		}
	}
}

func TestWorkflowMatrixExcludesBeforeIncludesAndCanRestoreRows(t *testing.T) {
	variants, problems := workflowMatrixForTest(t, `      matrix:
        os: [ubuntu-latest, windows-latest]
        node: [22, 24]
        exclude:
          - os: windows-latest
        include:
          - mode: stable
          - os: windows-latest
            node: 24
            mode: restored
          - mode: final
`)
	want := []map[string]string{
		{"os": "ubuntu-latest", "node": "22", "mode": "final"},
		{"os": "ubuntu-latest", "node": "24", "mode": "final"},
		{"os": "windows-latest", "node": "24", "mode": "restored"},
	}
	if len(problems) != 0 || len(variants) != len(want) {
		t.Fatalf("include did not restore excluded configuration: %d %v", len(variants), problems)
	}
	for i, variant := range variants {
		if !reflect.DeepEqual(variant.Values, want[i]) {
			t.Errorf("variant %d = %v, want %v", i, variant.Values, want[i])
		}
	}
	variants, problems = workflowMatrixForTest(t, "      matrix:\n        os: [ubuntu-latest]\n        exclude: [{os: ubuntu-latest}]\n        include: [{os: ubuntu-latest, mode: restored}]\n")
	if len(problems) != 0 || len(variants) != 1 || variants[0].Values["mode"] != "restored" {
		t.Fatalf("include could not restore a fully excluded matrix: %+v %v", variants, problems)
	}
}

func TestWorkflowMatrixIncludeMatchingPreservesScalarTypes(t *testing.T) {
	variants, problems := workflowMatrixForTest(t, `      matrix:
        value: [1, '1', true, 'true']
        include:
          - value: 1
            mode: integer
          - value: '1'
            mode: string
          - value: true
            mode: boolean
          - value: 'true'
            mode: string-boolean
`)
	if len(problems) != 0 || len(variants) != 4 {
		t.Fatalf("typed overlays lost: %d %v", len(variants), problems)
	}
	for i, mode := range []string{"integer", "string", "boolean", "string-boolean"} {
		if variants[i].Values["mode"] != mode {
			t.Errorf("variant %d has wrong typed overlay: %v", i, variants[i].Values)
		}
	}
}

func TestWorkflowMatrixRejectsCaseInsensitiveVariableAliases(t *testing.T) {
	for _, tc := range []struct{ name, matrix string }{
		{"axis and include", "        os: [ubuntu-latest, windows-latest]\n        include: [{OS: ubuntu-latest, flags: -race}]\n"},
		{"axis and exclude", "        os: [ubuntu-latest, windows-latest]\n        exclude: [{OS: windows-latest}]\n"},
		{"successive include fields", "        os: [ubuntu-latest]\n        include: [{FLAGS: -race}, {flags: -short}]\n"},
		{"duplicate axes", "        os: [ubuntu-latest]\n        OS: [windows-latest]\n"},
		{"same include row", "        os: [ubuntu-latest]\n        include: [{FLAGS: -race, flags: -short}]\n"},
		{"include-only fields", "        include: [{OS: ubuntu-latest}, {os: windows-latest}]\n"},
		{"include and exclude fields", "        os: [ubuntu-latest]\n        include: [{FLAGS: -race}]\n        exclude: [{flags: -short}]\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			variants, problems := workflowMatrixForTest(t, "      matrix:\n"+tc.matrix)
			if len(variants) != 0 || len(problems) == 0 || !strings.Contains(problems[0], "case-insensitive") {
				t.Fatalf("case-insensitive aliases were partially translated: %+v problems=%v", variants, problems)
			}
		})
	}
}

func TestWorkflowMatrixIncludeExpansionLimits(t *testing.T) {
	axis := make([]string, setupWorkflowMatrixLimit)
	for i := range axis {
		axis[i] = fmt.Sprint(i)
	}
	for _, tc := range []struct {
		name, strategy string
		want           int
	}{
		{"overlay at limit", "      matrix:\n        value: [" + strings.Join(axis, ",") + "]\n        include: [{mode: selected}]\n", 64},
		{"append beyond limit", "      matrix:\n        value: [" + strings.Join(axis, ",") + "]\n        include: [{value: 64}]\n", 0},
		{"exclude before bounded append", "      matrix:\n        value: [" + strings.Join(axis, ",") + "]\n        exclude: [{value: 0}]\n        include: [{value: 64}]\n", 64},
		{"too many include entries", "      matrix:\n        value: [0]\n        include:\n" + strings.Repeat("          - mode: same\n", 65), 0},
		{"duplicate appended rows", "      matrix:\n        value: [0]\n        include: [{value: 1}, {value: 1}]\n", 0},
		{"invalid final include", "      matrix:\n        value: [0]\n        include: [{mode: valid}, {mode: '${{ inputs.mode }}'}]\n", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			variants, problems := workflowMatrixForTest(t, tc.strategy)
			if len(variants) != tc.want || (tc.want == 0) != (len(problems) != 0) {
				t.Fatalf("bounded overlay expansion = %d, problems=%v, want %d", len(variants), problems, tc.want)
			}
		})
	}
}

func TestWorkflowMatrixIncludeOnlyResolvesHyphenatedKeysAndRuntime(t *testing.T) {
	var workflow repoWorkflowYAML
	if err := yaml.Unmarshal([]byte(`jobs:
  test:
    runs-on: ${{ matrix.os }}
    strategy:
      matrix:
        include:
          - node-version: 22
            os: ubuntu-latest
          - node-version: 24
            os: windows-latest
    steps:
      - uses: actions/setup-node@v4
        with:
          node-version: ${{ matrix.node-version }}
      - run: npm test
        env:
          NODE_VERSION: ${{ matrix.node-version }}
`), &workflow); err != nil {
		t.Fatal(err)
	}
	variants, problems := setupWorkflowMatrixVariants(workflow, workflow.Jobs["test"])
	if len(problems) != 0 || len(variants) != 2 {
		t.Fatalf("include-only matrix=%+v problems=%v", variants, problems)
	}
	for _, variant := range variants {
		job := scanWorkflowJob(t.TempDir(), variant.Workflow, "test", variant.Job)
		if job.RuntimeRequirements["node"] != variant.Values["node-version"] || job.RunSteps[0].Env["NODE_VERSION"] != variant.Values["node-version"] {
			t.Fatalf("variant runtime/environment was not concretized: %+v", job)
		}
	}
	variants[0].Job.Steps[0].With["node-version"] = "changed"
	variants[0].Job.Steps[1].Env["NODE_VERSION"] = "changed"
	if variants[1].Job.Steps[0].With["node-version"] != "24" || workflow.Jobs["test"].Steps[0].With["node-version"] != "${{ matrix.node-version }}" {
		t.Fatal("matrix variants share mutable action inputs")
	}
	if variants[1].Job.Steps[1].Env["NODE_VERSION"] != "24" || workflow.Jobs["test"].Steps[1].Env["NODE_VERSION"] != "${{ matrix.node-version }}" {
		t.Fatal("matrix variants share mutable environment")
	}
}

func TestWorkflowMatrixRejectsUnsupportedScopeWithoutPartialExpansion(t *testing.T) {
	for _, tc := range []struct{ name, strategy string }{
		{"include exclude ordering", "      matrix:\n        include: [{os: ubuntu-latest}]\n        exclude: [{os: ubuntu-latest}]\n"},
		{"dynamic matrix", "      matrix: ${{ fromJSON(needs.build.outputs.matrix) }}\n"},
		{"empty axis", "      matrix:\n        os: []\n"},
		{"object axis", "      matrix:\n        os: [{name: ubuntu-latest}]\n"},
		{"null scalar", "      matrix:\n        os: [null]\n"},
		{"ambiguous float", "      matrix:\n        go: [1.20]\n"},
		{"recursive expression", "      matrix:\n        os: ['${{ matrix.other }}']\n"},
		{"duplicate axis", "      matrix:\n        os: [ubuntu-latest, ubuntu-latest]\n"},
		{"duplicate include", "      matrix:\n        include: [{os: ubuntu-latest}, {os: ubuntu-latest}]\n"},
		{"all excluded", "      matrix:\n        os: [ubuntu-latest]\n        exclude: [{os: ubuntu-latest}]\n"},
		{"unknown exclude axis", "      matrix:\n        os: [ubuntu-latest]\n        exclude: [{version: '24'}]\n"},
		{"unknown strategy", "      unknown: true\n      matrix:\n        os: [ubuntu-latest]\n"},
		{"dynamic scheduling", "      fail-fast: '${{ matrix.experimental }}'\n      matrix:\n        os: [ubuntu-latest]\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			variants, problems := workflowMatrixForTest(t, tc.strategy)
			if len(variants) != 0 || len(problems) == 0 {
				t.Fatalf("unsupported matrix partially imported: %+v problems=%v", variants, problems)
			}
		})
	}
	variants, problems := workflowMatrixForTest(t, "      matrix:\n        a: [0,1,2,3,4,5,6,7]\n        b: [0,1,2,3,4,5,6,7]\n        c: [0,1]\n        exclude: [{a: 0}]\n")
	if len(variants) != 0 || len(problems) == 0 || !strings.Contains(problems[0], "64") {
		t.Fatalf("unbounded intermediate expansion accepted: %d %v", len(variants), problems)
	}
}

func TestWorkflowMatrixTypedIdentitiesAndUnresolvedExpressions(t *testing.T) {
	variants, problems := workflowMatrixForTest(t, "      matrix:\n        value: [1, '1', true, 'true']\n")
	if len(problems) != 0 || len(variants) != 4 {
		t.Fatalf("typed variants lost: %d %v", len(variants), problems)
	}
	seen := map[string]bool{}
	for _, variant := range variants {
		if seen[variant.Digest] {
			t.Fatal("different scalar types share a variant ID")
		}
		seen[variant.Digest] = true
	}
	for _, expression := range []string{"${{ matrix['os'] }}", "${{ github.token }}"} {
		repo := t.TempDir()
		command, _ := json.Marshal("printf '%s' '" + expression + "'\ngo test ./...")
		writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/tests.yml"), "jobs:\n  test:\n    runs-on: ubuntu-latest\n    strategy:\n      matrix:\n        os: [ubuntu-latest]\n    steps:\n      - run: "+string(command)+"\n")
		candidates := setupImportCandidates(repo, "github-actions")
		if len(candidates) != 2 || len(candidates[1].Unsupported) == 0 {
			t.Fatalf("unresolved expression became runnable: %q %+v", expression, candidates)
		}
	}
	for _, expression := range []string{"${{ matrix.missing }}", "${{ matrix.os || 'ubuntu-latest' }}"} {
		repo := t.TempDir()
		command, _ := json.Marshal("printf '%s' '" + expression + "'\ngo test ./...")
		writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/tests.yml"), "jobs:\n  test:\n    runs-on: ubuntu-latest\n    strategy:\n      matrix:\n        os: [ubuntu-latest]\n    steps:\n      - run: "+string(command)+"\n")
		candidates := setupImportCandidates(repo, "github-actions")
		if len(candidates) != 2 || len(candidates[0].Unsupported) == 0 || len(candidates[1].Unsupported) != 0 || strings.Contains(candidates[1].Command, "${{") {
			t.Fatalf("static expression was not resolved for only the explicit variant: %q", expression)
		}
	}
}

func TestWorkflowScannerRejectsUnknownExecutionFields(t *testing.T) {
	for _, snippet := range []string{
		"    snapshot: true\n    steps:\n      - run: go test ./...\n",
		"    steps:\n      - run: go test ./...\n        background: true\n",
		"    steps:\n      - parallel: []\n      - run: go test ./...\n",
		"    defaults:\n      run:\n        unknown: true\n    steps:\n      - run: go test ./...\n",
		"    steps:\n      - run: [install, prerequisites]\n      - run: go test ./...\n",
		"    steps:\n      - name: prerequisite without command\n      - run: go test ./...\n",
		"    steps:\n      - uses: actions/checkout@v4\n        run: prerequisite\n      - run: go test ./...\n",
		"    steps:\n      - run: go test ./...\n        if: {condition: false}\n",
		"    steps:\n      - run: go test ./...\n        working-directory: [subdir]\n",
		"    steps:\n      - run: go test ./...\n        with: {ignored: prerequisite}\n",
		"    steps:\n      - uses: actions/checkout@v4\n        shell: bash\n      - run: go test ./...\n",
		"    steps: []\n",
	} {
		repo := t.TempDir()
		writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/tests.yml"), "jobs:\n  test:\n    runs-on: ubuntu-latest\n"+snippet)
		candidates := setupImportCandidates(repo, "github-actions")
		if len(candidates) != 1 || len(candidates[0].Unsupported) == 0 {
			t.Fatalf("unknown workflow execution semantics disappeared: %+v", candidates)
		}
	}
}

func TestWorkflowMatrixPinnedPopular50Sources(t *testing.T) {
	const root = "testdata/setup-workflow-import"
	provenance, err := os.ReadFile(filepath.Join(root, "provenance.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Repository  string `json:"repository"`
		Commit      string `json:"commit"`
		SourcePath  string `json:"source_path"`
		SHA256      string `json:"sha256"`
		FixturePath string `json:"fixture_path"`
	}
	if err := json.Unmarshal(provenance, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Repository, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(root, fixture.FixturePath))
			if err != nil {
				t.Fatal(err)
			}
			if len(fixture.Commit) != 40 || fmt.Sprintf("%x", sha256.Sum256(data)) != fixture.SHA256 {
				t.Fatal("pinned upstream fixture identity changed")
			}
			repo := t.TempDir()
			writeFileForRepoMapTest(t, filepath.Join(repo, filepath.FromSlash(fixture.SourcePath)), string(data))
			writeFileForRepoMapTest(t, filepath.Join(repo, "go.mod"), "module fixture\n\ngo 1.27.2\n")
			candidates := setupImportCandidates(repo, "github-actions")
			if selection := automaticSetupSelection(candidates); selection.Imported != nil || selection.Reason == "" {
				t.Fatalf("authentic multiple scopes became an automatic single variant: %+v", selection)
			}
			parentName, want := "build", 3
			if fixture.Repository == "sveltejs/svelte" {
				parentName, want = "Tests", 5
			}
			parent := "github-actions:" + fixture.SourcePath + ":" + parentName
			var variants, ubuntu int
			nodeVersions := map[string]int{}
			for _, candidate := range candidates {
				if candidate.ParentJobID != parent {
					continue
				}
				variants++
				if fixture.Repository == "cli/cli" {
					if candidate.RuntimeRequirements["go"] != "1.27.2" || !strings.Contains(candidate.Command, "go test -race -tags=integration ./...") {
						t.Fatalf("authentic Go runtime/test scope changed: %+v", candidate)
					}
					if candidate.Matrix["os"] == "ubuntu-latest" {
						ubuntu++
						if len(candidate.Unsupported) != 0 {
							t.Fatalf("reviewed CLI Ubuntu variant blocked: %+v", candidate)
						}
					} else if len(candidate.Unsupported) == 0 {
						t.Fatal("unsupported OS became runnable")
					}
				} else {
					version := candidate.Matrix["node-version"]
					nodeVersions[version]++
					if candidate.RuntimeRequirements["node"] != version || candidate.Timeout != "15m" {
						t.Fatalf("authentic Node variant runtime/deadline changed: %+v", candidate)
					}
					if len(candidate.RunSteps) != 3 || candidate.RunSteps[2].Env["PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD"] != "1" || candidate.RunSteps[2].Env["CI"] != "true" {
						t.Fatalf("authentic Svelte prerequisites/environment changed: %+v", candidate)
					}
				}
			}
			if variants != want {
				t.Fatalf("got %d variants, want all %d", variants, want)
			}
			if fixture.Repository == "cli/cli" && ubuntu != 1 {
				t.Fatal("Ubuntu variant missing")
			}
			if fixture.Repository == "sveltejs/svelte" && !reflect.DeepEqual(nodeVersions, map[string]int{"20": 3, "22": 1, "24": 1}) {
				t.Fatalf("include-only version rows changed: %v", nodeVersions)
			}
		})
	}
}
