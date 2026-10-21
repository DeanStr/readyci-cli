package main

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestWorkflowStaticExpressionsPreserveWholeSelectedJob(t *testing.T) {
	const source = `env:
  WORKFLOW_VALUE: outer
jobs:
  test:
    runs-on: ${{ matrix.os || 'ubuntu-latest' }}
    timeout-minutes: 7
    strategy:
      matrix:
        include:
          - {node: '24', tests: true, mode: unit}
          - {node: '24', tests: false, mode: lint}
    env:
      MODE: ${{ matrix.mode }}
      OUTER: literal
    steps:
      - uses: actions/checkout@pinned
        with: {persist-credentials: false}
      - uses: actions/setup-node@pinned
        with: {node-version: '${{ matrix.node }}'}
      - run: npm ci
      - if: runner.os == 'Linux' && matrix.tests
        run: npm test
        env:
          LABEL: ${{ format('{0}-{1}', matrix.mode, matrix.node) }}
      - if: runner.os != 'Linux'
        run: exit 99
      - if: "!matrix.tests"
        run: npm run lint
`
	var workflow repoWorkflowYAML
	if err := yaml.Unmarshal([]byte(source), &workflow); err != nil {
		t.Fatal(err)
	}
	variants, problems := setupWorkflowMatrixVariants(workflow, workflow.Jobs["test"])
	if len(problems) != 0 || len(variants) != 2 {
		t.Fatalf("matrix: %v %+v", problems, variants)
	}
	for i, variant := range variants {
		report := scanWorkflowJob(t.TempDir(), variant.Workflow, "test", variant.Job)
		if len(report.Unsupported) != 0 || report.RunsOn[0] != "ubuntu-latest" || report.Timeout != "7m" || report.RuntimeRequirements["node"] != "24" {
			t.Fatalf("selected job was not imported completely: %+v", report)
		}
		if len(report.RunSteps) != 2 || report.RunSteps[0].Command != "npm ci" || len(report.Conditions) != 3 {
			t.Fatalf("steps/conditions lost: %+v", report)
		}
		if i == 0 && (report.RunSteps[1].Command != "npm test" || report.RunSteps[1].Env["LABEL"] != "unit-24") {
			t.Fatalf("true typed condition/scope precedence lost: %+v", report)
		}
		if i == 1 && report.RunSteps[1].Command != "npm run lint" {
			t.Fatalf("false boolean became truthy: %+v", report)
		}
		for _, condition := range report.Conditions {
			if !condition.Supported || condition.Expression == "" || condition.Translation == "" {
				t.Fatalf("conditional omission was not retained: %+v", condition)
			}
		}
		if len(report.Requirements) < 3 {
			t.Fatalf("conditional choices are absent from generated configuration disclosure: %+v", report)
		}
	}
}

func TestWorkflowStaticExpressionsKeepUnknownOrSkippedJobsBlocked(t *testing.T) {
	for _, expression := range []string{"github.event_name == 'push'", "needs.build.outputs.run", "steps.cache.outputs.cache-hit", "success()", "runner.arch == 'X64'"} {
		t.Run(expression, func(t *testing.T) {
			workflow := repoWorkflowYAML{}
			job := repoWorkflowJobYAML{RunsOn: "ubuntu-latest", Steps: []repoWorkflowStepYAML{{If: expression, Run: "npm test"}}}
			report := scanWorkflowJob(t.TempDir(), workflow, "test", job)
			if len(report.Unsupported) == 0 || len(report.RunSteps) != 1 || report.RunSteps[0].Command != "npm test" {
				t.Fatalf("unknown condition was guessed or scope dropped: %+v", report)
			}
		})
	}
	report := scanWorkflowJob(t.TempDir(), repoWorkflowYAML{}, "test", repoWorkflowJobYAML{RunsOn: "ubuntu-custom", Steps: []repoWorkflowStepYAML{{If: "runner.os != 'Linux'", Run: "npm test"}}})
	if len(report.Unsupported) == 0 || len(report.RunSteps) != 1 {
		t.Fatalf("arbitrary label was assumed to prove a Linux runner: %+v", report)
	}
	for _, job := range []repoWorkflowJobYAML{
		{RunsOn: "ubuntu-latest", If: "false", Steps: []repoWorkflowStepYAML{{Run: "npm test"}}},
		{RunsOn: "ubuntu-latest", Steps: []repoWorkflowStepYAML{{If: "false", Run: "npm test"}}},
	} {
		report := scanWorkflowJob(t.TempDir(), repoWorkflowYAML{}, "test", job)
		if len(report.Unsupported) == 0 {
			t.Fatalf("skipped/empty job was reported as supported: %+v", report)
		}
	}
}

func TestWorkflowStaticExpressionsDoNotResolveUnavailableContextsOrRecurse(t *testing.T) {
	workflow := repoWorkflowYAML{Env: map[string]any{"UNAVAILABLE": "${{ github.token }}"}}
	job := repoWorkflowJobYAML{RunsOn: "ubuntu-latest", Env: map[string]any{"A": "one", "B": "${{ env.A }}"}, Steps: []repoWorkflowStepYAML{{Run: "printf '%s' '${{ matrix.value }}'", Env: map[string]any{"A": "step", "B": "${{ env.A }}"}}}}
	w, concrete := setupWorkflowStaticJob(workflow, job, map[string]any{"value": "${{ github.token }}"})
	if concrete.Steps[0].Run != job.Steps[0].Run && concrete.Steps[0].Run != "printf '%s' '${{ github.token }}'" {
		t.Fatalf("recursive expression evaluation: %s", concrete.Steps[0].Run)
	}
	if concrete.Env["B"] != "${{ env.A }}" || concrete.Steps[0].Env["B"] != "one" {
		t.Fatalf("environment scope evaluated using itself: %+v %+v", concrete.Env, concrete.Steps[0].Env)
	}
	report := scanWorkflowJob(t.TempDir(), w, "test", concrete)
	if !strings.Contains(strings.Join(report.Unsupported, ";"), "provider") {
		t.Fatalf("unresolved provider context escaped admission: %+v", report)
	}
}

func TestWorkflowRuntimeFamiliesKeepUnsupportedPythonSelectorsExplicit(t *testing.T) {
	for _, selector := range []string{"3.14t", "3.15-dev", "pypy-3.11", "03.14"} {
		_, _, problems := setupWorkflowRuntimeRequirements(t.TempDir(), repoWorkflowJobYAML{Steps: []repoWorkflowStepYAML{{Uses: "actions/setup-python@pinned", With: map[string]any{"python-version": selector}}}})
		message := strings.Join(problems, "; ")
		if !strings.Contains(message, "python") || strings.Contains(message, "npm ") {
			t.Fatalf("wrong family or unsupported selector admitted: %s: %s", selector, message)
		}
	}
}

func TestWorkflowStaticStepEnvironmentPrecedenceAndIsolation(t *testing.T) {
	workflow := repoWorkflowYAML{Env: map[string]any{"OVERRIDE": "workflow"}}
	job := repoWorkflowJobYAML{RunsOn: "ubuntu-latest", Env: map[string]any{"OVERRIDE": "job", "RUN": "false"}, Steps: []repoWorkflowStepYAML{
		{If: "env.RUN == 'true'", Run: "echo '${{ env.OVERRIDE }}'", Env: map[string]any{"OVERRIDE": "step", "RUN": "true"}},
		{Run: "echo \"$OVERRIDE\""},
	}}
	report := scanWorkflowJob(t.TempDir(), workflow, "test", job)
	if len(report.Unsupported) != 0 || len(report.RunSteps) != 2 || report.RunSteps[0].Command != "echo 'step'" || report.RunSteps[1].Env["OVERRIDE"] != "job" {
		t.Fatalf("step env was unavailable or leaked into a sibling: %+v", report)
	}
	// The same step overlay is visible to action inputs; the input must not
	// resolve from a job-level default while the run shell sees another value.
	_, concrete := setupWorkflowStaticJob(workflow, repoWorkflowJobYAML{RunsOn: "ubuntu-latest", Steps: []repoWorkflowStepYAML{{Uses: "actions/setup-node@pinned", Env: map[string]any{"NODE": "24"}, With: map[string]any{"node-version": "${{ env.NODE }}"}}}}, nil)
	if concrete.Steps[0].With["node-version"] != "24" {
		t.Fatalf("step env not applied to action inputs: %+v", concrete.Steps[0])
	}
}

func TestWorkflowStaticFieldContextsAndNullEnvironment(t *testing.T) {
	for _, job := range []repoWorkflowJobYAML{
		{RunsOn: "${{ env.RUNNER }}", Steps: []repoWorkflowStepYAML{{Run: "go test ./..."}}},
		{RunsOn: "ubuntu-latest", Env: map[string]any{"COPY": "${{ env.VALUE }}"}, Steps: []repoWorkflowStepYAML{{Run: "go test ./..."}}},
		{RunsOn: "ubuntu-latest", TimeoutMinutes: "${{ env.TIMEOUT }}", Steps: []repoWorkflowStepYAML{{Run: "go test ./..."}}},
		{RunsOn: "ubuntu-latest", Defaults: repoWorkflowDefaultsYAML{Run: repoWorkflowRunDefaultsYAML{WorkingDirectory: "${{ runner.os }}"}}, Steps: []repoWorkflowStepYAML{{Run: "go test ./..."}}},
	} {
		report := scanWorkflowJob(t.TempDir(), repoWorkflowYAML{Env: map[string]any{"RUNNER": "ubuntu-latest", "VALUE": "known", "TIMEOUT": "5"}}, "test", job)
		if len(report.Unsupported) == 0 {
			t.Fatalf("invalid field context became supported: %+v", report)
		}
	}
	report := scanWorkflowJob(t.TempDir(), repoWorkflowYAML{Env: map[string]any{"EMPTY": nil}}, "test", repoWorkflowJobYAML{RunsOn: "ubuntu-latest", Steps: []repoWorkflowStepYAML{{Run: "go test ./..."}}})
	if len(report.Unsupported) != 0 || report.RunSteps[0].Env["EMPTY"] != "" {
		t.Fatalf("explicit null env value did not become empty: %+v", report)
	}
}

func TestWorkflowStaticFalseConditionDoesNotHideInvalidStep(t *testing.T) {
	job := repoWorkflowJobYAML{RunsOn: "ubuntu-latest", Steps: []repoWorkflowStepYAML{
		{Run: "go test ./..."},
		{If: "false", Run: "ignored", Problems: []string{"step field invented requires explicit translation"}},
	}}
	report := scanWorkflowJob(t.TempDir(), repoWorkflowYAML{}, "test", job)
	if !strings.Contains(strings.Join(report.Unsupported, ";"), "invented") {
		t.Fatalf("false condition hid a malformed step: %+v", report)
	}
	job.Defaults.Run.WorkingDirectory = "${{ format('{1}', 'x') }}"
	job.Steps = []repoWorkflowStepYAML{{Run: "go test ./...", WorkingDirectory: "."}}
	report = scanWorkflowJob(t.TempDir(), repoWorkflowYAML{}, "test", job)
	if len(report.Unsupported) == 0 {
		t.Fatal("overridden job defaults hid an upstream initialization error")
	}
}

func TestWorkflowImportRejectsARMOrCustomUbuntuLabels(t *testing.T) {
	for _, runner := range []any{"ubuntu-24.04-arm", "ubuntu-custom", "ubuntu-24.04-large", []any{"self-hosted", "ubuntu-24.04"}} {
		report := scanWorkflowJob(t.TempDir(), repoWorkflowYAML{}, "test", repoWorkflowJobYAML{RunsOn: runner, Steps: []repoWorkflowStepYAML{{Run: "go test ./..."}}})
		if len(report.Unsupported) == 0 {
			t.Fatalf("unsupported runner silently became an x64 Ubuntu check: %+v", runner)
		}
	}
}
