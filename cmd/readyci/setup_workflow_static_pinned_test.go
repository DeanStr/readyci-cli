package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestWorkflowStaticPinnedFlaskExpressionsKeepOtherPrerequisites(t *testing.T) {
	const root = "testdata/setup-workflow-static"
	provenance, err := os.ReadFile(filepath.Join(root, "provenance.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Repository  string `json:"repository"`
		Commit      string `json:"commit"`
		SourcePath  string `json:"source_path"`
		SourceURL   string `json:"source_url"`
		SHA256      string `json:"sha256"`
		FixturePath string `json:"fixture_path"`
	}
	if err := json.Unmarshal(provenance, &fixture); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, fixture.FixturePath))
	if err != nil {
		t.Fatal(err)
	}
	if fixture.Repository != "pallets/flask" || fixture.Commit != "d086db856be187255b8ec61ef409357393020f32" || fixture.SourceURL != "https://raw.githubusercontent.com/"+fixture.Repository+"/"+fixture.Commit+"/"+fixture.SourcePath || fmt.Sprintf("%x", sha256.Sum256(data)) != fixture.SHA256 {
		t.Fatal("original cohort source provenance changed")
	}
	var workflow repoWorkflowYAML
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	variants, problems := setupWorkflowMatrixVariants(workflow, workflow.Jobs["tests"])
	if len(problems) != 0 || len(variants) != 11 {
		t.Fatalf("original full job scope changed: variants=%d problems=%v", len(variants), problems)
	}
	linux := 0
	for _, variant := range variants {
		if variant.Job.RunsOn == "ubuntu-latest" {
			linux++
		}
		report := scanWorkflowJob(t.TempDir(), variant.Workflow, "tests", variant.Job)
		if !strings.Contains(strings.Join(report.Unsupported, ";"), "astral-sh/setup-uv") || !strings.Contains(strings.Join(report.Unsupported, ";"), "allow-prereleases") {
			t.Fatalf("static expressions erased independent action prerequisites: %v", report.Unsupported)
		}
		if len(report.RunSteps) != 1 || report.RunSteps[0].Command != "uv run --locked --no-default-groups --group dev tox run" || strings.Contains(report.RunSteps[0].Env["TOX_ENV"], "${{") {
			t.Fatalf("selected upstream command or static tox environment was lost: %+v", report.RunSteps)
		}
		want := variant.Values["tox"]
		if want == "" {
			want = "py" + variant.Values["python"]
		}
		if report.RunSteps[0].Env["TOX_ENV"] != want {
			t.Fatalf("optional include/fallback was not evaluated faithfully: %v", report.RunSteps[0].Env)
		}
	}
	if linux != 9 {
		t.Fatalf("literal Linux defaults lost: got %d, want 9", linux)
	}
}
