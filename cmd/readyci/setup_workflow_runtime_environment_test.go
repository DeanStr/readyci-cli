package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkflowRuntimeEnvironmentCannotOmitTests(t *testing.T) {
	repo := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/tests.yml"), `jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-python@v6
        with:
          python-version: '3.12'
      - run: pytest tests/unit
      - if: env.pythonLocation != ''
        run: pytest tests/integration
`)
	candidates := setupImportCandidates(repo, "github-actions")
	if len(candidates) != 1 || len(candidates[0].RunSteps) != 2 || !strings.Contains(candidates[0].Command, "pytest tests/integration") {
		t.Fatalf("runtime-dependent condition removed upstream test scope: %+v", candidates)
	}
	if selected := automaticSetupSelection(candidates).Imported; selected != nil {
		t.Fatalf("runtime-dependent job became an automatic import: %+v", selected)
	}
}

func TestWorkflowRuntimeEnvironmentInvalidatesEnclosingValues(t *testing.T) {
	for _, first := range []repoWorkflowStepYAML{
		{Uses: "actions/setup-python@v6", With: map[string]any{"python-version": "3.12"}},
		{Run: "./scripts/configure-environment.sh"},
	} {
		for _, dependent := range []repoWorkflowStepYAML{
			{If: "env.RUN_TESTS == 'true'", Run: "pytest tests/integration"},
			{Run: "pytest '${{ env.TEST_SCOPE }}'"},
			{Run: "pytest tests/integration", Env: map[string]any{"SCOPE": "${{ env.TEST_SCOPE }}"}},
		} {
			job := repoWorkflowJobYAML{RunsOn: "ubuntu-latest", Env: map[string]any{"RUN_TESTS": "false", "TEST_SCOPE": "tests/unit"}, Steps: []repoWorkflowStepYAML{first, dependent}}
			report := scanWorkflowJob(t.TempDir(), repoWorkflowYAML{}, "test", job)
			if len(report.Unsupported) == 0 {
				t.Fatalf("assumed an enclosing env value survived earlier execution: %+v", report)
			}
		}
	}
}

func TestWorkflowCurrentStepEnvironmentRemainsStatic(t *testing.T) {
	job := repoWorkflowJobYAML{RunsOn: "ubuntu-latest", Steps: []repoWorkflowStepYAML{
		{Run: "./scripts/configure-environment.sh"},
		{If: "env.RUN_TESTS == 'true'", Run: "pytest '${{ env.TEST_SCOPE }}'", Env: map[string]any{"RUN_TESTS": "true", "TEST_SCOPE": "tests/integration"}},
	}}
	report := scanWorkflowJob(t.TempDir(), repoWorkflowYAML{}, "test", job)
	if len(report.Unsupported) != 0 || len(report.RunSteps) != 2 || report.RunSteps[1].Command != "pytest 'tests/integration'" {
		t.Fatalf("explicit current-step values lost their static meaning: %+v", report)
	}
	job.Steps[0] = repoWorkflowStepYAML{If: "false", Run: "./scripts/configure-environment.sh"}
	job.Env = map[string]any{"TEST_SCOPE": "tests/unit"}
	job.Steps[1] = repoWorkflowStepYAML{Run: "pytest '${{ env.TEST_SCOPE }}'"}
	report = scanWorkflowJob(t.TempDir(), repoWorkflowYAML{}, "test", job)
	if len(report.Unsupported) != 0 || len(report.RunSteps) != 1 || report.RunSteps[0].Command != "pytest 'tests/unit'" {
		t.Fatalf("a skipped command invalidated an otherwise static value: %+v", report)
	}
}
