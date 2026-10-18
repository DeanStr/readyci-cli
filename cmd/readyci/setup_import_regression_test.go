package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

func TestWorkflowImportPreservesPipelineShellDefaults(t *testing.T) {
	for _, tc := range []struct {
		name, workflowDefaults, jobDefaults, stepShell string
		fails                                          bool
	}{
		{name: "unspecified"},
		{name: "explicit bash", stepShell: "        shell: bash\n", fails: true},
		{name: "workflow bash", workflowDefaults: "defaults:\n  run:\n    shell: bash\n", fails: true},
		{name: "job bash overrides workflow", workflowDefaults: "defaults:\n  run:\n    shell: sh\n", jobDefaults: "    defaults:\n      run:\n        shell: bash\n", fails: true},
		{name: "step bash overrides job", jobDefaults: "    defaults:\n      run:\n        shell: sh\n", stepShell: "        shell: bash\n", fails: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			workflow := tc.workflowDefaults + "jobs:\n  test:\n    runs-on: ubuntu-latest\n" + tc.jobDefaults + "    steps:\n      - run: |\n          printf 'nothing matched\\n' | grep optional | wc -l\n          printf reached\n" + tc.stepShell + "      - run: printf next\n" + tc.stepShell
			writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/ci.yml"), workflow)
			candidates := setupImportCandidates(repo, "github-actions")
			if len(candidates) != 1 || len(candidates[0].Unsupported) != 0 {
				t.Fatalf("candidates=%+v", candidates)
			}
			out, err := exec.Command("bash", "-c", candidates[0].Command).CombinedOutput()
			if (err != nil) != tc.fails || strings.Contains(string(out), "reachednext") == tc.fails {
				t.Fatalf("output=%q error=%v", out, err)
			}
		})
	}
}

func TestWorkflowImportRejectsHistoryCheckoutRequirements(t *testing.T) {
	for _, depth := range []string{"0", "2", "'0'", "${{ vars.DEPTH }}", "-1", "false", "null", "1", "'1'"} {
		t.Run(depth, func(t *testing.T) {
			repo := t.TempDir()
			writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/ci.yml"), "jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v4\n        with:\n          fetch-depth: "+depth+"\n      - run: git describe --tags\n")
			candidates := setupImportCandidates(repo, "github-actions")
			if len(candidates) != 1 {
				t.Fatalf("candidates=%+v", candidates)
			}
			_, err := validateSetupImport(candidates[0])
			blocked := depth != "1" && depth != "'1'"
			if (err != nil) != blocked {
				t.Fatalf("depth=%s error=%v", depth, err)
			}
			if blocked && !strings.Contains(err.Error(), "history") {
				t.Fatalf("missing history explanation: %v", err)
			}
			if !strings.Contains(strings.Join(candidates[0].Requirements, " "), "synthetic baseline") {
				t.Fatal("checkout preview omitted the Git metadata limitation")
			}
		})
	}
}

func TestWorkflowImportPreservesDifferentShellsWithinJob(t *testing.T) {
	repo := t.TempDir()
	workflow := "jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: |\n          false | true\n          printf first\n      - run: |\n          false | true\n          printf second\n        shell: bash\n      - run: printf third\n"
	writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/ci.yml"), workflow)
	candidates := setupImportCandidates(repo, "github-actions")
	if len(candidates) != 1 || len(candidates[0].Unsupported) > 0 {
		t.Fatalf("candidates=%+v", candidates)
	}
	output, err := exec.Command("bash", "-c", candidates[0].Command).CombinedOutput()
	if err == nil || string(output) != "first" {
		t.Fatalf("shell settings leaked between steps: output=%q error=%v", output, err)
	}
}

func TestSetupImportRuntimeInferenceStaysInsideRepository(t *testing.T) {
	repo, outside := t.TempDir(), t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(outside, "go.mod"), "module external\n")
	writeFileForRepoMapTest(t, filepath.Join(outside, "Cargo.toml"), "[package]\nname = \"external\"\nversion = \"0.1.0\"\n")
	if err := os.Symlink(outside, filepath.Join(repo, "linked")); err != nil {
		t.Skip(err)
	}
	writeFileForRepoMapTest(t, filepath.Join(repo, "local/placeholder"), "")
	if err := os.Symlink(filepath.Join(outside, "go.mod"), filepath.Join(repo, "local/go.mod")); err != nil {
		t.Fatal(err)
	}
	traversal, err := filepath.Rel(repo, outside)
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{outside, traversal, "linked", "local"} {
		candidate := repoImportCandidate{WorkingDirectories: []string{dir}}
		if runtimes := setupImportRuntimes(repo, candidate); len(runtimes) != 0 {
			t.Fatalf("inferred runtimes outside uploaded source via %q: %v", dir, runtimes)
		}
		if targets := setupImportRustTargetPaths(repo, candidate); len(targets) != 0 {
			t.Fatalf("inferred Rust cache paths outside uploaded source via %q: %v", dir, targets)
		}
	}
}

func TestSetupImportRustCachesPossibleWorkspaceTargets(t *testing.T) {
	repo := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(repo, "workspace/Cargo.toml"), "[workspace]\nmembers = [\"crate\"]\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, "workspace/crate/Cargo.toml"), "[package]\nname = \"crate\"\nversion = \"0.1.0\"\n")
	paths := setupImportRustTargetPaths(repo, repoImportCandidate{WorkingDirectories: []string{"workspace/crate"}})
	if !containsString(paths, "workspace/target") || !containsString(paths, "workspace/crate/target") || containsString(paths, "target") {
		t.Fatalf("Rust target cache paths = %v", paths)
	}
}

func TestSetupImportedRustQuotesCachePaths(t *testing.T) {
	repo := chdirCLITestRepo(t, "rust-yaml-path")
	writeFileForRepoMapTest(t, filepath.Join(repo, "[crate]/Cargo.toml"), "[package]\nname = \"crate\"\nversion = \"0.1.0\"\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, "[crate]/Makefile"), "test:\n\tcargo test\n")
	candidates := setupImportCandidates(repo, "make")
	if len(candidates) != 1 {
		t.Fatalf("candidates=%+v", candidates)
	}
	template := setupImportedTemplate(setupOptions{Profile: "remote-ci"}, "fixture", candidates[0])
	cfg, err := config.ParseProjectConfig([]byte(template), "imported")
	if err != nil {
		t.Fatalf("invalid imported config: %v\n%s", err, template)
	}
	if !containsString(cfg.Cache.Paths, "[crate]/target") {
		t.Fatalf("Rust target cache paths = %v", cfg.Cache.Paths)
	}
}

func TestSetupImportedSubdirectoryRuntimeConfiguration(t *testing.T) {
	for _, tc := range []struct{ name, source, manifest, workflow, host, cache, target string }{
		{"go workflow", "github-actions", "cli/go.mod", "jobs:\n  test:\n    runs-on: ubuntu-latest\n    defaults:\n      run:\n        working-directory: cli\n    steps:\n      - run: go test ./...\n", "proxy.golang.org", "~/.cache/go-build", ""},
		{"node workflow", "github-actions", "cli/package.json", "jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: npm test\n        working-directory: cli\n", "registry.npmjs.org", "~/.npm", ""},
		{"rust make", "make", "cli/Cargo.toml", "", "index.crates.io", "~/.cargo/registry", "cli/target"},
		{"go make", "make", "cli/go.mod", "", "proxy.golang.org", "~/.cache/go-build", ""},
		{"setup action", "github-actions", "", "jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/setup-go@v5\n        with:\n          go-version-file: sdk/go.mod\n      - run: go test ./...\n", "proxy.golang.org", "~/.cache/go-build", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := chdirCLITestRepo(t, "import-runtime")
			if tc.manifest != "" {
				writeFileForRepoMapTest(t, filepath.Join(repo, tc.manifest), "fixture")
			}
			if tc.source == "make" {
				writeFileForRepoMapTest(t, filepath.Join(repo, "cli/Makefile"), "test:\n\t@echo test\n")
			} else {
				writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/ci.yml"), tc.workflow)
			}
			candidates := setupImportCandidates(repo, tc.source)
			if len(candidates) != 1 {
				t.Fatalf("candidates=%+v", candidates)
			}
			options := setupOptions{Profile: "remote-ci", Image: "ubuntu-24.04-ci"}
			template := setupImportedTemplate(options, "fixture", candidates[0])
			if !strings.Contains(template, tc.host) || !strings.Contains(template, tc.cache) {
				t.Fatalf("missing runtime setup:\n%s", template)
			}
			if tc.target != "" {
				cfg, err := config.ParseProjectConfig([]byte(template), "imported")
				if err != nil {
					t.Fatal(err)
				}
				if !containsString(cfg.Cache.Paths, tc.target) || containsString(cfg.Cache.Paths, "target") {
					t.Fatalf("Rust target cache paths = %v, want %q", cfg.Cache.Paths, tc.target)
				}
			}
			options.Profile = "strict"
			template = setupImportedTemplate(options, "fixture", candidates[0])
			if !strings.Contains(template, "mode: none") || strings.Contains(template, tc.host) {
				t.Fatalf("strict import enabled network:\n%s", template)
			}
		})
	}
}

func TestWorkflowReferencesKeepOriginalSourceLines(t *testing.T) {
	for _, run := range []string{"      - run: readyci check unit\n", "      - run: |\n          echo setup\n          readyci check unit\n", "      - run: |\n\n          readyci check unit\n"} {
		repo := t.TempDir()
		workflow := "jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n" + run + "        working-directory: cli\n"
		path := filepath.Join(repo, ".github/workflows/ci.yml")
		writeFileForRepoMapTest(t, path, workflow)
		refs := checkReferencesFromGitHubActions(repoGitHubActionsMap{Workflows: []repoWorkflowMap{scanWorkflowFile(repo, path)}})
		want := 0
		for i, line := range strings.Split(workflow, "\n") {
			if strings.Contains(line, "readyci check unit") {
				want = i + 1
			}
		}
		if len(refs) != 1 || refs[0].Line != want {
			t.Fatalf("refs=%+v, want line %d", refs, want)
		}
	}
}
