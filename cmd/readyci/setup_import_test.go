package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

func TestGuidedSetupImportsSelectedMakeCommand(t *testing.T) {
	options, runs := newGuidedSetupReviewFixture(t)
	if err := os.WriteFile("Makefile", []byte("unit:\n\tgo test ./...\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	options.ImportSource = "make"
	options.ImportCandidate = "make:Makefile:unit"
	var output bytes.Buffer
	if err := guidedSetup(options, strings.NewReader("y\ny\n"), &output, false); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.LoadProjectConfig(".")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Checks) != 1 || cfg.Checks["unit"].Command != "make -f Makefile -- unit" || runs.Load() != 1 {
		t.Fatalf("checks=%+v runs=%d", cfg.Checks, runs.Load())
	}
	if !strings.Contains(output.String(), "Review:") || !strings.Contains(output.String(), "network:") {
		t.Fatal("requirements or network preview missing")
	}
}

func TestSetupImportPreviewDoesNotWriteOrExecute(t *testing.T) {
	options, runs := newGuidedSetupReviewFixture(t)
	if err := os.WriteFile("Makefile", []byte("unit:\n\tgo test ./...\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := buildSetupReport(options)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.ImportCandidates) != 1 || report.ImportCandidates[0].ID != "make:Makefile:unit" {
		t.Fatalf("candidates=%+v", report.ImportCandidates)
	}
	if _, err := os.Stat(".readyci.yml"); !os.IsNotExist(err) || runs.Load() != 0 {
		t.Fatal("preview caused a mutation")
	}
	options.ImportSource = "make"
	if err := guidedSetup(options, strings.NewReader(""), io.Discard, true); err == nil {
		t.Fatal("automation silently selected an imported command")
	}
}

func TestSetupBlocksUntranslatedWorkflowRequirements(t *testing.T) {
	options, runs := newGuidedSetupReviewFixture(t)
	path := filepath.Join(".github", "workflows", "ci.yml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	workflow := "jobs:\n  test:\n    runs-on: ubuntu-latest\n    services:\n      postgres:\n        image: postgres:16\n    steps:\n      - run: go test ./...\n"
	if err := os.WriteFile(path, []byte(workflow), 0o644); err != nil {
		t.Fatal(err)
	}
	options.ImportSource = "github-actions"
	options.ImportCandidate = "github-actions:.github/workflows/ci.yml:test"
	if err := guidedSetup(options, strings.NewReader(""), io.Discard, true); err == nil || !strings.Contains(err.Error(), "service topology") {
		t.Fatalf("error=%v", err)
	}
	if _, err := os.Stat(".readyci.yml"); !os.IsNotExist(err) || runs.Load() != 0 {
		t.Fatal("unsupported import wrote or executed")
	}
}

func TestSetupRejectsImportExecutionWithoutSavingSelection(t *testing.T) {
	for _, args := range [][]string{{"--import", "make", "--run"}, {"--candidate", "make:Makefile:unit", "--run"}, {"--import", "make", "--write-config"}} {
		if err := setupCommand(args); err == nil {
			t.Fatalf("accepted ambiguous import action: %v", args)
		}
	}
}

func TestSetupImportDoesNotReadSymlinkedSources(t *testing.T) {
	repo := chdirCLITestRepo(t, "import-symlinks")
	external := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(external, []byte("test:\n\tgo test ./...\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(repo, "Makefile")); err != nil {
		t.Skip(err)
	}
	if candidates := setupImportCandidates(repo, "make"); len(candidates) != 0 {
		t.Fatal("symlinked Makefile was imported")
	}
	workflowRoot := filepath.Join(repo, ".github", "workflows")
	if err := os.MkdirAll(workflowRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(external, []byte("jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test ./...\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(workflowRoot, "ci.yml")); err != nil {
		t.Fatal(err)
	}
	if candidates := setupImportCandidates(repo, "github-actions"); len(candidates) != 0 {
		t.Fatal("symlinked workflow was imported")
	}
}

func TestImportedStepsPreserveIndependentShellsAndFailures(t *testing.T) {
	t.Setenv("CI", "false")
	for _, tc := range []struct {
		commands []string
		want     string
		fails    bool
	}{
		{[]string{"export STEP_LOCAL=value\nprintf first # a trailing comment", `printf "${STEP_LOCAL:-unset}"`}, "firstunset", false},
		{[]string{"false\nprintf should-not-run", "printf later"}, "", true},
		{[]string{"false | true", "printf later"}, "", true},
		{[]string{`if [ "${CI:-}" = true ]; then printf executed; fi`}, "executed", false},
		{[]string{`test "$CI" = true; export CI=false`, `printf "$CI"`}, "true", false},
	} {
		var steps []repoWorkflowRunStep
		for _, command := range tc.commands {
			steps = append(steps, repoWorkflowRunStep{Command: command, Shell: "bash"})
		}
		cmd := exec.Command("bash", "-c", joinImportedSteps(steps))
		output, err := cmd.CombinedOutput()
		if (err != nil) != tc.fails || string(output) != tc.want {
			t.Fatalf("output=%q err=%v", output, err)
		}
	}
}

func TestImportedWorkflowMissingWorkingDirectoryStopsAllCommands(t *testing.T) {
	command := commandWithWorkingDirectory("printf first\nprintf second", filepath.Join(t.TempDir(), "missing"))
	output, err := exec.Command("bash", "-c", joinImportedSteps([]repoWorkflowRunStep{{Command: command}})).CombinedOutput()
	if err == nil || strings.Contains(string(output), "first") || strings.Contains(string(output), "second") {
		t.Fatalf("commands ran outside the requested directory: output=%q err=%v", output, err)
	}
}

func TestImportedMakeCommandUsesSelectedFile(t *testing.T) {
	repo := t.TempDir()
	for name, content := range map[string]string{"Makefile": "test:\n\t@printf selected\n", "GNUmakefile": "test:\n\t@printf other\n"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, candidate := range setupImportCandidates(repo, "make") {
		if candidate.ID != "make:Makefile:test" {
			continue
		}
		cmd := exec.Command("bash", "-c", candidate.Command)
		cmd.Dir = repo
		output, err := cmd.CombinedOutput()
		if err != nil || string(output) != "selected" {
			t.Fatalf("selected a different Makefile: output=%q err=%v", output, err)
		}
		return
	}
	t.Fatal("selected Makefile was not discovered")
}

func TestWorkflowImportBlocksDynamicUbuntuRunner(t *testing.T) {
	_, unsupported := workflowImportRequirements(repoWorkflowYAML{}, repoWorkflowJobYAML{RunsOn: "ubuntu-${{ vars.VERSION }}"})
	if len(unsupported) == 0 {
		t.Fatal("dynamic runner requirements were silently accepted")
	}
}

func TestWorkflowImportUsesEffectiveRunDefaults(t *testing.T) {
	for _, tc := range []struct {
		name, jobDefaults, stepDefaults string
		unsupported                     bool
	}{
		{name: "job overrides workflow", jobDefaults: "    defaults:\n      run:\n        shell: bash\n        working-directory: cli\n"},
		{name: "step overrides workflow", stepDefaults: "        shell: bash\n        working-directory: cli\n"},
		{name: "step overrides job", jobDefaults: "    defaults:\n      run:\n        shell: sh\n        working-directory: job-directory\n", stepDefaults: "        shell: bash\n        working-directory: cli\n"},
		{name: "effective custom shell", stepDefaults: "        working-directory: cli\n", unsupported: true},
		{name: "effective dynamic directory", jobDefaults: "    defaults:\n      run:\n        working-directory: ${{ vars.JOB_DIR }}\n", stepDefaults: "        shell: bash\n", unsupported: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			workflow := "defaults:\n  run:\n    shell: pwsh\n    working-directory: workflow-directory\njobs:\n  test:\n    runs-on: ubuntu-latest\n" + tc.jobDefaults + "    steps:\n      - run: cat probe.txt\n" + tc.stepDefaults
			writeFileForRepoMapTest(t, filepath.Join(repo, ".github", "workflows", "test.yml"), workflow)
			writeFileForRepoMapTest(t, filepath.Join(repo, "cli", "probe.txt"), "selected")
			candidates := setupImportCandidates(repo, "github-actions")
			if len(candidates) != 1 || (len(candidates[0].Unsupported) > 0) != tc.unsupported {
				t.Fatalf("candidates=%+v", candidates)
			}
			if !tc.unsupported {
				cmd := exec.Command("bash", "-c", candidates[0].Command)
				cmd.Dir = repo
				output, err := cmd.CombinedOutput()
				if err != nil || string(output) != "selected" {
					t.Fatalf("effective directory was not preserved: output=%q err=%v", output, err)
				}
			}
		})
	}
}

func TestWorkflowImportExcludesDirectoryWrappedOperationalCommands(t *testing.T) {
	for _, command := range []string{"npm publish", "cargo publish", "docker push image", "terraform apply", "gh release create v1", "export VERSION=1\nnpm publish", "npm test"} {
		t.Run(command, func(t *testing.T) {
			repo := t.TempDir()
			workflow := "jobs:\n  test:\n    runs-on: ubuntu-latest\n    defaults:\n      run:\n        working-directory: web\n    steps:\n      - run: |\n          " + strings.ReplaceAll(command, "\n", "\n          ") + "\n"
			writeFileForRepoMapTest(t, filepath.Join(repo, ".github", "workflows", "ci.yml"), workflow)
			candidates := setupImportCandidates(repo, "github-actions")
			want := 0
			if command == "npm test" {
				want = 1
			}
			if len(candidates) != want {
				t.Fatalf("candidates=%+v, want %d", candidates, want)
			}
		})
	}
}

func TestWorkflowImportBlocksPNPMActionDependencyInstallation(t *testing.T) {
	for _, tc := range []struct {
		input       string
		unsupported bool
	}{
		{"", false}, {"false", false}, {"null", false}, {"'false'", false}, {"'null'", false},
		{"true", true}, {"'true'", true}, {"'{ recursive: true }'", true},
		{"'[{ cwd: web }]'", true}, {"[{ cwd: web }]", true}, {"${{ vars.INSTALL }}", true},
	} {
		t.Run(tc.input, func(t *testing.T) {
			repo := t.TempDir()
			install := ""
			if tc.input != "" {
				install = "          run_install: " + tc.input + "\n"
			}
			workflow := "jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: pnpm/action-setup@v4\n        with:\n          version: 10\n" + install + "      - run: pnpm test\n"
			writeFileForRepoMapTest(t, filepath.Join(repo, ".github", "workflows", "ci.yml"), workflow)
			candidates := setupImportCandidates(repo, "github-actions")
			if len(candidates) != 1 {
				t.Fatalf("candidates=%+v", candidates)
			}
			_, err := validateSetupImport(candidates[0])
			if (err != nil) != tc.unsupported || tc.unsupported && !strings.Contains(err.Error(), "run_install") {
				t.Fatalf("dependency installation was not disclosed: candidates=%+v err=%v", candidates, err)
			}
		})
	}
}

func TestWorkflowImportSkipsCoverageUploads(t *testing.T) {
	repo := t.TempDir()
	workflow := "jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n" +
		"      - uses: actions/checkout@v4\n" +
		"      - run: go test ./...\n" +
		"      - uses: codecov/codecov-action@v5\n        env:\n          CODECOV_ENV: x\n        with:\n          token: ${{ secrets.CODECOV_TOKEN }}\n          files: ./coverage.out\n" +
		"      - uses: coverallsapp/github-action@v2\n        if: always()\n        with:\n          github-token: ${{ secrets.GITHUB_TOKEN }}\n"
	writeFileForRepoMapTest(t, filepath.Join(repo, ".github", "workflows", "ci.yml"), workflow)
	candidates := setupImportCandidates(repo, "github-actions")
	if len(candidates) != 1 {
		t.Fatalf("candidates=%+v", candidates)
	}
	candidate := candidates[0]
	if _, err := validateSetupImport(candidate); err != nil || len(candidate.AutomaticUnsupported) != 0 {
		t.Fatalf("coverage uploads blocked the import: candidate=%+v err=%v", candidate, err)
	}
	if len(candidate.RunSteps) != 1 || candidate.RunSteps[0].Command != "go test ./..." {
		t.Fatalf("coverage uploads changed the imported steps: %+v", candidate.RunSteps)
	}
	requirements := strings.Join(candidate.Requirements, "\n")
	for _, action := range []string{"codecov/codecov-action", "coverallsapp/github-action"} {
		if !strings.Contains(requirements, "("+action+") uploads coverage") {
			t.Fatalf("skipped %s upload was not disclosed: %q", action, requirements)
		}
	}
}

func TestWorkflowImportTranslatesCorepackEnable(t *testing.T) {
	for _, tc := range []struct {
		name, step, unsupported string
		steps                   int
	}{
		{name: "alone", step: "corepack enable", steps: 1},
		{name: "named managers", step: "corepack enable pnpm yarn", steps: 1},
		{name: "combined", step: "corepack enable && pnpm install", steps: 2, unsupported: "corepack enable combined with other commands"},
		{name: "other corepack command", step: "corepack prepare pnpm@10 --activate", steps: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			workflow := "jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: " + tc.step + "\n      - run: pnpm vitest\n"
			writeFileForRepoMapTest(t, filepath.Join(repo, ".github", "workflows", "ci.yml"), workflow)
			candidates := setupImportCandidates(repo, "github-actions")
			if len(candidates) != 1 {
				t.Fatalf("candidates=%+v", candidates)
			}
			candidate := candidates[0]
			if len(candidate.RunSteps) != tc.steps {
				t.Fatalf("run steps=%+v, want %d", candidate.RunSteps, tc.steps)
			}
			_, err := validateSetupImport(candidate)
			if tc.unsupported != "" {
				if err == nil || !strings.Contains(err.Error(), tc.unsupported) {
					t.Fatalf("combined corepack enable was not disclosed: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("candidate=%+v err=%v", candidate, err)
			}
			if tc.steps == 1 && !strings.Contains(strings.Join(candidate.Requirements, "\n"), "corepack enable is not executed") {
				t.Fatalf("skipped corepack enable was not disclosed: %+v", candidate.Requirements)
			}
		})
	}
}

func TestWorkflowImportTranslatesSetupJup(t *testing.T) {
	for _, tc := range []struct {
		name, with, lockfile, prepare, unsupported string
		noPackage                                  bool
	}{
		{name: "pnpm", lockfile: "pnpm-lock.yaml", prepare: "pnpm install --frozen-lockfile"},
		{name: "npm", lockfile: "package-lock.json", prepare: "npm ci"},
		{name: "yarn", lockfile: "yarn.lock", prepare: nodeYarnFrozenInstall},
		{name: "reviewed inputs", with: "          install: true\n          working-directory: .\n          package-managers: pnpm\n          cache: false\n", lockfile: "pnpm-lock.yaml", prepare: "pnpm install --frozen-lockfile"},
		{name: "no install", with: "          install: false\n", lockfile: "pnpm-lock.yaml"},
		{name: "install arguments", with: "          install: --prod\n", lockfile: "pnpm-lock.yaml", unsupported: "input install"},
		{name: "node selector", with: "          node-version: 22\n", lockfile: "pnpm-lock.yaml", prepare: "pnpm install --frozen-lockfile", unsupported: "input node-version"},
		{name: "other directory", with: "          working-directory: web\n", lockfile: "pnpm-lock.yaml", prepare: "pnpm install --frozen-lockfile", unsupported: "input working-directory"},
		{name: "no package", noPackage: true, unsupported: "requires a root package.json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			with := ""
			if tc.with != "" {
				with = "        with:\n" + tc.with
			}
			workflow := "jobs:\n  tests:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v4\n      - uses: unjs/setup-jup@v1\n" + with + "      - run: pnpm build\n      - run: pnpm vitest --coverage\n"
			writeFileForRepoMapTest(t, filepath.Join(repo, ".github", "workflows", "ci.yml"), workflow)
			if !tc.noPackage {
				writeFileForRepoMapTest(t, filepath.Join(repo, "package.json"), `{"scripts":{"build":"obuild"},"devDependencies":{"vitest":"4.0.0"}}`)
				writeFileForRepoMapTest(t, filepath.Join(repo, tc.lockfile), "")
			}
			candidates := setupImportCandidates(repo, "github-actions")
			if len(candidates) != 1 {
				t.Fatalf("candidates=%+v", candidates)
			}
			candidate := candidates[0]
			if got := strings.Join(candidate.Prepare, "\n"); got != tc.prepare {
				t.Fatalf("prepare=%q want %q", got, tc.prepare)
			}
			_, err := validateSetupImport(candidate)
			if tc.unsupported != "" {
				if err == nil || !strings.Contains(err.Error(), tc.unsupported) {
					t.Fatalf("untranslated setup-jup use was not disclosed: candidate=%+v err=%v", candidate, err)
				}
				return
			}
			if err != nil || len(candidate.AutomaticUnsupported) != 0 || !containsString(candidate.Runtimes, "node") {
				t.Fatalf("setup-jup was not translated: candidate=%+v err=%v", candidate, err)
			}
			template := setupImportedTemplateForRoot(repo, setupOptions{}, "p", candidate)
			if tc.prepare != "" && !strings.Contains(template, "    prepare:\n      - "+strconv.Quote(tc.prepare)+"\n") {
				t.Fatalf("template does not prepare dependencies:\n%s", template)
			}
			if tc.prepare == "" && strings.Contains(template, "prepare:") {
				t.Fatalf("install: false still prepares dependencies:\n%s", template)
			}
		})
	}
}
