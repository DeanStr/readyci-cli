package main

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/verificationplan"
)

func TestWorkflowImportAcceptsLiteralEnvironmentAndDeadlines(t *testing.T) {
	// These literal settings occur in the pinned Svelte, Vue, and Django
	// workflows: browser-download flags, CI=true, and job deadlines.
	workflow := repoWorkflowYAML{Env: map[string]any{"PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD": "1"}}
	job := repoWorkflowJobYAML{
		RunsOn: "ubuntu-latest", TimeoutMinutes: 15,
		Env: map[string]any{"PUPPETEER_SKIP_DOWNLOAD": "true"},
		Steps: []repoWorkflowStepYAML{
			{Uses: "actions/checkout@v4", With: map[string]any{"persist-credentials": false}},
			{Run: "pnpm test", Env: map[string]any{"CI": true, "SVELTE_NO_ASYNC": true}, TimeoutMinutes: 10},
		},
	}
	_, unsupported := workflowImportRequirements(workflow, job)
	if len(unsupported) != 0 {
		t.Fatalf("literal requirements were blocked: %s", strings.Join(unsupported, "; "))
	}
}

func TestImportedWorkflowEnvironmentPrecedenceAndIsolation(t *testing.T) {
	repo := t.TempDir()
	workflow := `env:
  VALUE: workflow
  EMPTY: ' '
  LITERAL: '$(touch injected);$HOME'
  NUMBER: 12
jobs:
  test:
    runs-on: ubuntu-latest
    timeout-minutes: 7
    env:
      VALUE: job
      EMPTY: ''
    steps:
      - run: |
          printf '%s|%s|%s|%s|%s|' "$VALUE" "$EMPTY" "$LITERAL" "$NUMBER" "$CI"
          export LOCAL=leaked
          export CI=false
        env:
          VALUE: ' step '
          CI: false
        timeout-minutes: 1
      - run: printf '%s|%s|%s' "$VALUE" "${LOCAL-unset}" "$CI"
`
	writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/ci.yml"), workflow)
	candidates := setupImportCandidates(repo, "github-actions")
	if len(candidates) != 1 || len(candidates[0].Unsupported) != 0 {
		t.Fatalf("candidates=%+v", candidates)
	}
	template := setupImportedTemplateForRoot(repo, setupOptions{Profile: "strict", Image: "ubuntu-24.04-ci"}, "fixture", candidates[0])
	cfg, err := config.ParseProjectConfig([]byte(template), "imported")
	if err != nil {
		t.Fatalf("invalid config: %v\n%s", err, template)
	}
	var selected string
	for name, check := range cfg.Checks {
		selected = name
		if check.Command != "" || len(check.Steps) != 2 || check.Timeout != "7m" || check.Steps[0].Timeout != "1m" || check.Steps[1].Timeout != "" {
			t.Fatalf("lost structured deadlines or commands: %+v", check)
		}
		if check.Steps[0].Env["VALUE"] != " step " || check.Steps[0].Env["EMPTY"] != "" || check.Steps[0].Env["CI"] != "false" || check.Steps[1].Env["CI"] != "true" {
			t.Fatalf("environment changed: %+v", check.Steps)
		}
	}
	plan, err := verificationplan.ResolveCheck(selected, cfg, verificationplan.ResolveOptions{})
	if err != nil || plan.Run.TimeoutSeconds != 420 || len(plan.Run.Steps) != 2 || plan.Run.Steps[0].TimeoutSeconds != 60 {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	compiled, err := verificationplan.CommandForRunSpec(plan.Run)
	if err != nil {
		t.Fatal(err)
	}
	want := " step ||$(touch injected);$HOME|12|false|job|unset|true"
	markers := regexp.MustCompile("\x1eREADYCI_(?:PHASE|STEP):[^\x1e]*\x1e\n")
	for _, command := range []string{candidates[0].Command, compiled} {
		cmd := exec.Command("bash", "-c", command)
		cmd.Dir = repo
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		output, err := cmd.Output()
		if err != nil || string(markers.ReplaceAll(output, nil)) != want {
			t.Fatalf("output=%q want=%q err=%v stderr=%s", output, want, err, stderr.String())
		}
	}
	if _, err := os.Stat(filepath.Join(repo, "injected")); !os.IsNotExist(err) {
		t.Fatal("literal environment value executed shell syntax")
	}
}

func TestWorkflowImportRejectsUnsupportedEnvironmentValues(t *testing.T) {
	for _, tc := range []struct {
		name, key string
		value     any
	}{
		{"provider expression", "VALUE", "${{ github.ref }}"},
		{"secret expression", "VALUE", "${{ secrets.TOKEN }}"},
		{"mapping", "VALUE", map[string]any{"x": "y"}},
		{"sequence", "VALUE", []any{"x"}},
		{"newline", "VALUE", "a\nb"},
		{"NUL", "VALUE", "a\x00b"},
		{"invalid name", "lowercase", "x"},
		{"provider namespace", "GITHUB_WORKSPACE", "/tmp"},
		{"runner namespace", "RUNNER_TEMP", "/tmp"},
		{"infinity", "VALUE", math.Inf(1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workflow := repoWorkflowYAML{Env: map[string]any{tc.key: tc.value}}
			_, unsupported := workflowImportRequirements(workflow, repoWorkflowJobYAML{RunsOn: "ubuntu-latest", Steps: []repoWorkflowStepYAML{{Run: "go test ./..."}}})
			if len(unsupported) == 0 {
				t.Fatalf("unsupported environment accepted: %#v", tc)
			}
		})
	}
	values := map[string]any{}
	for i := 0; i < preflightapi.MaxRunStepEnvVars; i++ {
		values[fmt.Sprintf("KEY_%d", i)] = "value"
	}
	_, problems := effectiveWorkflowEnvironment(repoWorkflowYAML{Env: values}, repoWorkflowJobYAML{}, repoWorkflowStepYAML{})
	if len(problems) == 0 {
		t.Fatal("effective environment limit omitted the implicit CI variable")
	}
}

func TestWorkflowImportStaticTimeoutValidation(t *testing.T) {
	for _, tc := range []struct {
		value any
		want  string
	}{
		{nil, ""}, {1, "1m"}, {int64(60), "60m"}, {uint64(2), "2m"}, {"15", "15m"},
	} {
		got, problems := staticWorkflowTimeout(tc.value, "job")
		if got != tc.want || len(problems) > 0 {
			t.Fatalf("input=%#v got=%s problems=%v", tc.value, got, problems)
		}
	}
	for _, value := range []any{0, -1, "0", "-2", true, 1.5, "1.5", " 2 ", "", "${{ matrix.timeout }}", math.MaxInt64, uint64(math.MaxUint64), []any{1}, map[string]any{"minutes": 1}} {
		_, problems := staticWorkflowTimeout(value, "step")
		if len(problems) != 1 || !strings.Contains(problems[0], "step timeout-minutes") {
			t.Fatalf("accepted unsupported timeout %#v: %v", value, problems)
		}
	}
}

func TestWorkflowLiteralEnvironmentProjectionPreservesValues(t *testing.T) {
	values := workflowLiteralEnvironmentProjection(map[string]any{"EMPTY": "", "SPACE": " ", "BOOL": true, "DYNAMIC": "${{ env.DYNAMIC }}"})
	if len(values) != 3 || values["EMPTY"] != "" || values["SPACE"] != " " || values["BOOL"] != "true" {
		t.Fatalf("literal environment projection changed values: %+v", values)
	}
	if empty := workflowLiteralEnvironmentProjection(nil); empty != nil {
		t.Fatalf("empty projection=%+v", empty)
	}
}

func TestWorkflowCheckoutCredentialImportIsExplicit(t *testing.T) {
	for _, value := range []any{false, "false", true, "true", "${{ vars.PERSIST }}", nil, map[string]any{"enabled": false}} {
		job := repoWorkflowJobYAML{RunsOn: "ubuntu-latest", Steps: []repoWorkflowStepYAML{{Uses: "actions/checkout@v4", With: map[string]any{"persist-credentials": value}}, {Run: "go test ./..."}}}
		_, problems := workflowImportRequirements(repoWorkflowYAML{}, job)
		wantBlocked := !checkoutCredentialsDisabled(value)
		if (len(problems) > 0) != wantBlocked {
			t.Fatalf("value=%#v problems=%v", value, problems)
		}
	}
}

func TestWorkflowImportDoesNotDropActionLocalEnvironmentOrDeadline(t *testing.T) {
	for _, step := range []repoWorkflowStepYAML{
		{Uses: "actions/setup-node@v4", With: map[string]any{"node-version": "24"}, Env: map[string]any{"FLAG": "literal"}},
		{Uses: "actions/checkout@v4", TimeoutMinutes: 5},
	} {
		job := repoWorkflowJobYAML{RunsOn: "ubuntu-latest", Steps: []repoWorkflowStepYAML{step, {Run: "npm test"}}}
		_, problems := workflowImportRequirements(repoWorkflowYAML{}, job)
		if !strings.Contains(strings.Join(problems, "; "), "action step") {
			t.Fatalf("action-local settings were silently discarded: step=%+v problems=%v", step, problems)
		}
	}
}

func TestStructuredWorkflowImportPreservesShellAndDirectory(t *testing.T) {
	for _, explicitBash := range []bool{false, true} {
		t.Run(fmt.Sprint(explicitBash), func(t *testing.T) {
			repo := t.TempDir()
			writeFileForRepoMapTest(t, filepath.Join(repo, "component/probe"), "selected-directory")
			shell := ""
			if explicitBash {
				shell = "        shell: bash\n"
			}
			workflow := "jobs:\n  test:\n    runs-on: ubuntu-latest\n    defaults:\n      run:\n        working-directory: component\n    steps:\n      - run: |\n          false | true\n          cat probe\n" + shell + "      - run: printf later\n"
			writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/ci.yml"), workflow)
			candidates := setupImportCandidates(repo, "github-actions")
			if len(candidates) != 1 || len(candidates[0].Unsupported) != 0 {
				t.Fatalf("candidates=%+v", candidates)
			}
			template := setupImportedTemplateForRoot(repo, setupOptions{Profile: "strict", Image: "ubuntu-24.04-ci"}, "fixture", candidates[0])
			cfg, err := config.ParseProjectConfig([]byte(template), "imported")
			if err != nil {
				t.Fatal(err)
			}
			for name := range cfg.Checks {
				plan, err := verificationplan.ResolveCheck(name, cfg, verificationplan.ResolveOptions{})
				if err != nil {
					t.Fatal(err)
				}
				command, err := verificationplan.CommandForRunSpec(plan.Run)
				if err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command("bash", "-c", command)
				cmd.Dir = repo
				output, err := cmd.CombinedOutput()
				if (err != nil) != explicitBash || strings.Contains(string(output), "selected-directory") == explicitBash || strings.Contains(string(output), "later") == explicitBash {
					t.Fatalf("explicit bash=%v output=%q err=%v", explicitBash, output, err)
				}
			}
		})
	}
}

func TestDetectedProjectSignalsCannotEscapeYAMLComments(t *testing.T) {
	signal := "repo\nchecks:\n  injected:\n    command: false\rproject_id: injected\x00"
	template := renderDetectedProjectTemplate("fixture", "ubuntu-24.04-ci", "strict", []detectedCheck{{Name: "test", Command: "true"}}, nil, []string{signal}, nil)
	cfg, err := config.ParseProjectConfig([]byte(template), "imported")
	if err != nil || cfg.ProjectID != "fixture" || len(cfg.Checks) != 1 || cfg.Checks["test"].Command != "true" {
		t.Fatalf("signal escaped a comment: cfg=%+v err=%v\n%s", cfg, err, template)
	}
}
