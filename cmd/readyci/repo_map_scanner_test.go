package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestRepoMapGitHubActionsWorkflowParserCorpus(t *testing.T) {
	repo := chdirCLITestRepo(t, "repo-map-workflow-corpus")
	workflowPath := filepath.Join(repo, ".github", "workflows", "ci.yml")
	writeFileForRepoMapTest(t, workflowPath, `
name: ci
on: [push]
env:
  READYCI_PROJECT_ID: readyci
jobs:
  reusable:
    uses: owner/repo/.github/workflows/reusable.yml@main
  check-ci:
    if: github.event_name == 'push'
    runs-on: [ubuntu-latest, ubuntu-24.04]
    steps:
      # - run: readyci check old-db
      - if: startsWith(github.ref, 'refs/heads/release/')
        run: readyci check ci
  check-db:
    if: github.actor == 'octocat' && success()
    needs: [check-ci]
    runs-on: ubuntu-latest
    steps:
      - run: |
          echo setup
          readyci check db
      - name: no run
        uses: actions/checkout@v4
  grouped-runner:
    runs-on:
      group: larger-runners
      labels:
        - ubuntu-24.04
        - gpu
    steps:
      - run: make check-gpu
`)
	workflow := scanWorkflowFile(repo, workflowPath)
	if workflow.Env["READYCI_PROJECT_ID"] != "readyci" {
		t.Fatalf("env = %+v, want READYCI_PROJECT_ID", workflow.Env)
	}
	jobs := map[string]repoWorkflowJob{}
	for _, job := range workflow.Jobs {
		jobs[job.Name] = job
	}
	if len(jobs) != 4 {
		t.Fatalf("jobs = %+v, want reusable, check-ci, check-db, grouped-runner", workflow.Jobs)
	}
	if got := strings.Join(jobs["check-ci"].RunsOn, ","); got != "ubuntu-latest,ubuntu-24.04" {
		t.Fatalf("check-ci runs-on = %q", got)
	}
	if got := strings.Join(jobs["check-db"].Needs, ","); got != "check-ci" {
		t.Fatalf("check-db needs = %q", got)
	}
	if got := strings.Join(jobs["check-ci"].Commands, "\n"); got != "readyci check ci" {
		t.Fatalf("check-ci commands = %q", got)
	}
	if len(jobs["check-ci"].Conditions) != 2 || !jobs["check-ci"].Conditions[0].Supported || jobs["check-ci"].Conditions[0].Translation != "when.event: push" || jobs["check-ci"].Conditions[1].Translation != "when.branch: release/*" {
		t.Fatalf("check-ci conditions = %+v, want supported event and branch translations", jobs["check-ci"].Conditions)
	}
	if len(jobs["check-db"].Conditions) != 1 || jobs["check-db"].Conditions[0].Supported || jobs["check-db"].Conditions[0].Translation != "manual translation required" {
		t.Fatalf("check-db conditions = %+v, want unsupported manual translation marker", jobs["check-db"].Conditions)
	}
	if got := strings.Join(jobs["check-db"].Commands, "\n---\n"); !strings.Contains(got, "readyci check db") {
		t.Fatalf("check-db commands = %q, want block run", got)
	}
	if got := strings.Join(jobs["grouped-runner"].RunsOn, ","); got != "group:larger-runners,label:ubuntu-24.04,label:gpu" {
		t.Fatalf("grouped-runner runs-on = %q, want structured group and labels", got)
	}
	if len(jobs["reusable"].Commands) != 0 {
		t.Fatalf("reusable commands = %+v, want none", jobs["reusable"].Commands)
	}
}

func TestRepoMapCommandScannerCorpus(t *testing.T) {
	tests := []struct {
		name       string
		command    string
		wantChecks []string
		wantMake   []string
	}{
		{
			name:       "readyci check with value flags",
			command:    "readyci check --region fsn1 --watch-interval 5s ci",
			wantChecks: []string{"ci"},
		},
		{
			name:       "multiple readyci checks on one line",
			command:    "readyci check ci && $(READYCI) check db",
			wantChecks: []string{"ci", "db"},
		},
		{
			name:       "semicolon without spaces",
			command:    "readyci check ci;readyci check db",
			wantChecks: []string{"ci", "db"},
		},
		{
			name:       "comments stop readyci scanning",
			command:    "readyci check ci # readyci check old-db",
			wantChecks: []string{"ci"},
		},
		{
			name:       "quoted readyci check",
			command:    `readyci check "ci"`,
			wantChecks: []string{"ci"},
		},
		{
			name:       "make recipe quiet readyci command",
			command:    "@$(READYCI) check ci",
			wantChecks: []string{"ci"},
		},
		{
			name:       "readyci after shell separator",
			command:    "cd cli && readyci check ci",
			wantChecks: []string{"ci"},
		},
		{
			name:       "readyci inside conditional branch",
			command:    "if test -n \"$RUN_DB\"; then readyci check db; else make check-ci; fi",
			wantChecks: []string{"db"},
			wantMake:   []string{"check-ci"},
		},
		{
			name:       "readyci and make inside case branches",
			command:    "case \"$GOAL\" in db) readyci check db ;; ci) make check-ci ;; esac",
			wantChecks: []string{"db"},
			wantMake:   []string{"check-ci"},
		},
		{
			name:       "function definition readyci is scanned",
			command:    "run_db() { readyci check old-db; }\nreadyci check ci",
			wantChecks: []string{"old-db", "ci"},
		},
		{
			name:       "readyci after env assignment",
			command:    "READYCI_PROJECT_ID=readyci readyci check old-db",
			wantChecks: []string{"old-db"},
		},
		{
			name:       "readyci after dynamic env assignment value",
			command:    "READYCI_API_TOKEN=$READYCI_API_TOKEN readyci check ci",
			wantChecks: []string{"ci"},
		},
		{
			name:       "readyci after env wrapper dynamic assignment value",
			command:    "env READYCI_API_TOKEN=$READYCI_API_TOKEN readyci check ci",
			wantChecks: []string{"ci"},
		},
		{
			name:       "make recipe quiet readyci after env assignment",
			command:    "@READYCI_PROJECT_ID=readyci $(READYCI) check ci",
			wantChecks: []string{"ci"},
		},
		{
			name:       "readyci line continuation",
			command:    "readyci check --region fsn1 \\\nold-db",
			wantChecks: []string{"old-db"},
		},
		{
			name:    "assignment-only command has no readyci reference",
			command: "READYCI_PROJECT_ID=readyci",
		},
		{
			name:    "echoed readyci text is ignored",
			command: `echo "readyci check old-db"`,
		},
		{
			name:    "dynamic readyci check is ignored",
			command: "readyci check ${{ matrix.check }}",
		},
		{
			name:    "shell variable readyci check is ignored",
			command: "readyci check $CHECK",
		},
		{
			name:     "make with multiple goals",
			command:  "make check-ci check-db",
			wantMake: []string{"check-ci", "check-db"},
		},
		{
			name:     "make chained with attached semicolon",
			command:  "make check-ci;make check-db",
			wantMake: []string{"check-ci", "check-db"},
		},
		{
			name:     "make redirection with separate operand",
			command:  "make check-ci > ci.log",
			wantMake: []string{"check-ci"},
		},
		{
			name:     "make combined stdout stderr redirection",
			command:  "make check-ci &> ci.log",
			wantMake: []string{"check-ci"},
		},
		{
			name:     "make stderr redirection",
			command:  "make check-ci 2>&1",
			wantMake: []string{"check-ci"},
		},
		{
			name:     "make after shell comment in multiline block",
			command:  "# setup\nmake check-ci",
			wantMake: []string{"check-ci"},
		},
		{
			name:     "make background separator",
			command:  "make serve & make test",
			wantMake: []string{"serve", "test"},
		},
		{
			name:     "make after newline",
			command:  "echo setup\nmake check-ci",
			wantMake: []string{"check-ci"},
		},
		{
			name:     "multiple make commands separated by newline",
			command:  "make check-ci\nmake lint",
			wantMake: []string{"check-ci", "lint"},
		},
		{
			name:     "make directory context",
			command:  "make -C cli check",
			wantMake: []string{"cli:check"},
		},
		{
			name:     "make nested directory context",
			command:  "make -C cli -C internal check",
			wantMake: []string{"cli/internal:check"},
		},
		{
			name:     "make options and assignment",
			command:  "make FOO=bar --jobs 4 check-ci",
			wantMake: []string{"check-ci"},
		},
		{
			name:     "make after env wrapper dynamic assignment value",
			command:  "env NODE_OPTIONS=$NODE_OPTIONS make check-ci",
			wantMake: []string{"check-ci"},
		},
		{
			name:     "make recipe quiet make command",
			command:  "@make check-ci",
			wantMake: []string{"check-ci"},
		},
		{
			name:     "make after shell separator",
			command:  "cd cli && make check-ci",
			wantMake: []string{"cli:check-ci"},
		},
		{
			name:     "subshell cd does not leak to later make",
			command:  "(cd cli && make check)\nmake root-check",
			wantMake: []string{"cli:check", "root-check"},
		},
		{
			name:     "brace group cd persists for later make",
			command:  "{ cd cli; make check; }\nmake lint",
			wantMake: []string{"cli:check", "cli:lint"},
		},
		{
			name:     "loop body make is recorded without leaking cd",
			command:  "for pkg in cli web; do cd \"$pkg\"; make check; done\nmake root-check",
			wantMake: []string{"root-check"},
		},
		{
			name:     "make after env assignment",
			command:  "FOO=bar make check-ci",
			wantMake: []string{"check-ci"},
		},
		{
			name:     "make after dynamic env assignment value",
			command:  "FOO=$BAR make check-ci",
			wantMake: []string{"check-ci"},
		},
		{
			name:     "make after multiple env assignments",
			command:  "FOO=bar BAR=baz make check-ci",
			wantMake: []string{"check-ci"},
		},
		{
			name:     "make recipe quiet env assignment",
			command:  "@FOO=bar make check-ci",
			wantMake: []string{"check-ci"},
		},
		{
			name:       "env wrapper readyci and make",
			command:    "env READYCI_PROJECT_ID=readyci readyci check ci && env FOO=bar make check-ci",
			wantChecks: []string{"ci"},
			wantMake:   []string{"check-ci"},
		},
		{
			name:     "make option terminator",
			command:  "make -- check-ci",
			wantMake: []string{"check-ci"},
		},
		{
			name:    "assignment-only command has no make reference",
			command: "FOO=bar",
		},
		{
			name:    "echoed make text is ignored",
			command: `echo "make check-db"`,
		},
		{
			name:     "dynamic make option value",
			command:  "make --jobs ${{ matrix.jobs }} check-ci",
			wantMake: []string{"check-ci"},
		},
		{
			name:     "dynamic make target is skipped",
			command:  "make ${{ matrix.target }} check-ci",
			wantMake: []string{"check-ci"},
		},
		{
			name:    "dynamic make directory skips validation",
			command: "make -C ${{ matrix.dir }} check",
		},
		{
			name:    "embedded shell variable make target is skipped",
			command: "make check-$TARGET",
		},
		{
			name:     "comments stop make scanning",
			command:  "make check-ci # make old-db",
			wantMake: []string{"check-ci"},
		},
		{
			name:       "mixed readyci and make",
			command:    "readyci check ci && make -C cli check",
			wantChecks: []string{"ci"},
			wantMake:   []string{"cli:check"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := readyCIChecksFromCommand(tt.command); strings.Join(got, ",") != strings.Join(tt.wantChecks, ",") {
				t.Fatalf("readyCIChecksFromCommand(%q) = %v, want %v", tt.command, got, tt.wantChecks)
			}
			if got := makeInvocationDisplays(makeInvocationsFromCommand(tt.command)); strings.Join(got, ",") != strings.Join(tt.wantMake, ",") {
				t.Fatalf("makeInvocationsFromCommand(%q) = %v, want %v", tt.command, got, tt.wantMake)
			}
		})
	}
}

func TestRepoMapParsesReadyCICheckFlagsWithValues(t *testing.T) {
	tests := map[string]string{
		"--region fsn1 ci":          "ci",
		"--watch-interval 5s ci":    "ci",
		"--api-url http://x ci":     "ci",
		"--region=fsn1 --watch ci":  "ci",
		"--network-allowlist x ci;": "ci",
	}
	for args, want := range tests {
		if got := checkFromReadyCIArgs(args); got != want {
			t.Fatalf("checkFromReadyCIArgs(%q) = %q, want %q", args, got, want)
		}
	}
}

func TestRepoMapParsesMultipleReadyCICheckCommandsOnOneLine(t *testing.T) {
	got := readyCIChecksFromCommand("readyci check --region fsn1 ci && $(READYCI) check old-db")
	if strings.Join(got, ",") != "ci,old-db" {
		t.Fatalf("readyCIChecksFromCommand = %v, want [ci old-db]", got)
	}
}

func TestRepoMapSkipsDynamicReadyCICheckExpressions(t *testing.T) {
	got := readyCIChecksFromCommand("readyci check ${{ matrix.check }} && readyci check ci")
	if strings.Join(got, ",") != "ci" {
		t.Fatalf("readyCIChecksFromCommand = %v, want [ci]", got)
	}
}

func TestRepoMapParsesMakeOptionsWithValues(t *testing.T) {
	tests := map[string]string{
		"make -C cli check":                "check",
		"make --directory cli check":       "check",
		"make --directory=cli check":       "check",
		"$(MAKE) -f build.mk check-ci":     "check-ci",
		"make --jobs 4 check-ci":           "check-ci",
		"make -j 4 check-ci":               "check-ci",
		"make FOO=bar --directory cli db;": "db",
	}
	for command, want := range tests {
		got := makeTargetsFromCommand(command)
		if len(got) != 1 || got[0] != want {
			t.Fatalf("makeTargetsFromCommand(%q) = %v, want [%s]", command, got, want)
		}
	}
}

func TestRepoMapParsesMultipleMakeGoals(t *testing.T) {
	tests := map[string]string{
		"make check-ci check-db":                  "check-ci,check-db",
		"make -C cli check-ci check-db":           "check-ci,check-db",
		"make check-ci; make check-db":            "check-ci,check-db",
		"make ${{ matrix.target }} check-ci":      "check-ci",
		"make --jobs ${{ matrix.jobs }} check-ci": "check-ci",
	}
	for command, want := range tests {
		got := makeTargetsFromCommand(command)
		if strings.Join(got, ",") != want {
			t.Fatalf("makeTargetsFromCommand(%q) = %v, want %s", command, got, want)
		}
	}
}

func TestRepoMapSkipsDynamicMakeDirectoryExpressions(t *testing.T) {
	got := makeTargetsFromCommand("make -C ${{ matrix.dir }} check")
	if len(got) != 0 {
		t.Fatalf("makeTargetsFromCommand = %v, want dynamic directory invocation ignored", got)
	}
}

func TestRepoMapParsesMakeDirectoryInvocations(t *testing.T) {
	tests := map[string]makeInvocation{
		"make -C cli check":             {Dir: "cli", Target: "check"},
		"make --directory cli check":    {Dir: "cli", Target: "check"},
		"make --directory=cli check":    {Dir: "cli", Target: "check"},
		"make -C cli -C internal check": {Dir: "cli/internal", Target: "check"},
		"cd cli && make check":          {Dir: "cli", Target: "check"},
		"cd cli || make check":          {Dir: "", Target: "check"},
		"cd cli | make check":           {Dir: "", Target: "check"},
		"env -C cli make check":         {Dir: "cli", Target: "check"},
		"env --chdir cli make check":    {Dir: "cli", Target: "check"},
		"env --chdir=cli make check":    {Dir: "cli", Target: "check"},
	}
	for command, want := range tests {
		got := makeInvocationsFromCommand(command)
		if len(got) != 1 || got[0] != want {
			t.Fatalf("makeInvocationsFromCommand(%q) = %+v, want %+v", command, got, want)
		}
	}
	for _, command := range []string{
		"make -C /tmp check",
		"make -C ../outside check",
		"make -C ~/project check",
		"cd /tmp && make check",
		"cd ~/project && make check",
		"cd '' && make check",
		"env -C ${{ matrix.dir }} make check",
		"env --chdir=${{ matrix.dir }} make check",
		"env -C '' make check",
		"env --chdir= make check",
	} {
		if got := makeInvocationsFromCommand(command); len(got) != 0 {
			t.Fatalf("makeInvocationsFromCommand(%q) = %+v, want no static invocation", command, got)
		}
	}
}

func makeInvocationDisplays(invocations []makeInvocation) []string {
	out := make([]string, 0, len(invocations))
	for _, invocation := range invocations {
		out = append(out, invocation.Display())
	}
	return out
}

func lineContaining(text, needle string) int {
	text = strings.TrimLeft(text, "\n")
	for i, line := range strings.Split(text, "\n") {
		if strings.Contains(line, needle) {
			return i + 1
		}
	}
	return 0
}

func writeRepoMapFixture(t *testing.T, repo string) {
	t.Helper()
	writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), `
READYCI ?= readyci

check-ci:
	@echo ci

check-ci-db:
	DOCKER=1 cargo test --features tc

check-ci-db-parallel:
	@scripts/check-ci-db-parallel.sh

define remote-readyci-check
	@$(READYCI) check $(READYCI_CHECK_FLAGS) $(1)
endef

remote-check-ci:
	$(call remote-readyci-check,ci,check-ci)

remote-check-ci-db:
	$(call remote-readyci-check,db-serial,check-ci-db)

remote-check-ci-db-parallel:
	$(call remote-readyci-check,db,check-ci-db-parallel)

remote-check-all-db:
	$(call remote-readyci-check,all-db,check-all-db)
`)
	writeFileForRepoMapTest(t, filepath.Join(repo, "AGENTS.md"), "Run `readyci check db` before DB-sensitive merges.\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, "docs", "ci-cd.md"), "Use `make remote-check-ci-db-parallel` for DB confidence.\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, "docs", "api", "openapi.yaml"), "openapi: 3.1.0\ninfo:\n  title: API\n  version: 1.0.0\npaths: {}\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, ".github", "workflows", "ci.yml"), `
name: ci
on: [push]
jobs:
  check-ci:
    runs-on: ubuntu-latest
    steps:
      - run: make check-ci
  check-db:
    needs: check-ci
    runs-on: ubuntu-latest
    steps:
      - run: make check-ci-db
`)
	writeFileForRepoMapTest(t, filepath.Join(repo, "apps", "web", "package.json"), `{
  "private": true,
  "packageManager": "pnpm@10.16.0",
  "scripts": {
    "check": "pnpm contracts:check && vite build",
    "contracts:generate": "openapi-typescript ../../docs/api/openapi.yaml -o .generated/openapi.ts",
    "contracts:check": "node scripts/check-generated-contracts.mjs",
    "ci:e2e": "playwright test"
  },
  "dependencies": {"@vitejs/plugin-react": "latest", "react": "latest", "vite": "latest"},
  "devDependencies": {"@playwright/test": "latest"}
}`)
	writeFileForRepoMapTest(t, filepath.Join(repo, "apps", "web", "vite.config.ts"), "export default {}\n")
}

func writeFileForRepoMapTest(t *testing.T, path string, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimLeft(contents, "\n")), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func projectAnalysisInventoryHasFile(inventory preflightapi.ProjectAnalysisInventory, path string, kind string) bool {
	for _, file := range inventory.Files {
		if file.Path == path && file.Kind == kind {
			return true
		}
	}
	return false
}

func projectAnalysisInventoryHasSignal(inventory preflightapi.ProjectAnalysisInventory, kind string, path string) bool {
	for _, signal := range inventory.Signals {
		if signal.Kind == kind && signal.Path == path {
			return true
		}
	}
	return false
}

func projectAnalysisInventoryHasPackage(inventory preflightapi.ProjectAnalysisInventory, path string, ecosystem string) bool {
	for _, pkg := range inventory.Packages {
		if pkg.Path == path && pkg.Ecosystem == ecosystem {
			return true
		}
	}
	return false
}

func repoCheckReferencesContain(refs []repoCheckReference, check string) bool {
	for _, ref := range refs {
		if ref.Check == check {
			return true
		}
	}
	return false
}

func importReportHasCheck(report repoImportReport, check string) bool {
	for _, candidate := range report.Candidates {
		if candidate.Check == check {
			return true
		}
	}
	return false
}

func importReportHasCommand(report repoImportReport, command string) bool {
	for _, candidate := range report.Candidates {
		if candidate.Command == command {
			return true
		}
	}
	return false
}

func importReportHasName(report repoImportReport, name string) bool {
	for _, candidate := range report.Candidates {
		if candidate.Name == name {
			return true
		}
	}
	return false
}

func importReportHasNameContaining(report repoImportReport, value string) bool {
	for _, candidate := range report.Candidates {
		if strings.Contains(candidate.Name, value) {
			return true
		}
	}
	return false
}
