package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// These are original popular-50 source pins, not simplified workflows. Matrix
// expansion becoming inspectable must not imply that their other prerequisites
// or complete upstream test scopes can be imported.
func TestWorkflowMatrixPinnedOverlayCohort(t *testing.T) {
	const root = "testdata/setup-workflow-matrix"
	data, err := os.ReadFile(filepath.Join(root, "provenance.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Repository  string `json:"repository"`
		Commit      string `json:"commit"`
		SourcePath  string `json:"source_path"`
		SourceURL   string `json:"source_url"`
		SHA256      string `json:"sha256"`
		FixturePath string `json:"fixture_path"`
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures) != 4 {
		t.Fatalf("got %d pinned fixtures, want four reviewed repositories", len(fixtures))
	}
	wantCount := map[string]int{"caddyserver/caddy": 3, "fastapi/fastapi": 15, "gin-gonic/gin": 20, "google/guava": 9}
	const pythonWorkflowCommit = "3070b5105fe02c4afdba5e3aea6c2cf2e3796c7f"
	wantPin := map[string]string{
		"caddyserver/caddy": "719db53e946181dd10a9aae36721c3251f7f8dc1",
		"fastapi/fastapi":   pythonWorkflowCommit,
		"gin-gonic/gin":     "0f09c3a9b4626d9fe9979cf4d0521d6ae32ab646",
		"google/guava":      "669a232b4e20ae6e8bc6efc62caaf2f830d56327",
	}
	wantBlocker := map[string]string{
		"caddyserver/caddy": "step-security/harden-runner",
		"fastapi/fastapi":   "astral-sh/setup-uv",
		"gin-gonic/gin":     "job dependencies and outputs",
		"google/guava":      "actions/setup-java",
	}
	seenRepositories := map[string]bool{}
	for _, fixture := range fixtures {
		if fixture.Commit != wantPin[fixture.Repository] || seenRepositories[fixture.Repository] {
			t.Fatalf("original repository pin is missing, changed, or duplicated: %s@%s", fixture.Repository, fixture.Commit)
		}
		seenRepositories[fixture.Repository] = true
		t.Run(fixture.Repository, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(root, fixture.FixturePath))
			if err != nil {
				t.Fatal(err)
			}
			if len(fixture.Commit) != 40 || fixture.SourceURL != "https://raw.githubusercontent.com/"+fixture.Repository+"/"+fixture.Commit+"/"+fixture.SourcePath || fmt.Sprintf("%x", sha256.Sum256(data)) != fixture.SHA256 {
				t.Fatal("pinned upstream fixture identity changed")
			}
			var workflow repoWorkflowYAML
			if err := yaml.Unmarshal(data, &workflow); err != nil {
				t.Fatal(err)
			}
			job, exists := workflow.Jobs["test"]
			if !exists {
				t.Fatal("original test job disappeared")
			}
			variants, problems := setupWorkflowMatrixVariants(workflow, job)
			if len(problems) != 0 || len(variants) != wantCount[fixture.Repository] {
				t.Fatalf("original matrix: got %d variants, want %d; problems=%v", len(variants), wantCount[fixture.Repository], problems)
			}
			assertPinnedMatrixOverlayValues(t, fixture.Repository, variants)

			repo := t.TempDir()
			writeFileForRepoMapTest(t, filepath.Join(repo, filepath.FromSlash(fixture.SourcePath)), string(data))
			candidates := setupImportCandidates(repo, "github-actions")
			if _, err := automaticSetupImport(candidates); err == nil {
				t.Fatal("authentic workflow silently became an automatic selected scope")
			}
			parent := "github-actions:" + fixture.SourcePath + ":test"
			children, parents := 0, 0
			seen := map[string]bool{}
			for _, candidate := range candidates {
				if candidate.ID == parent {
					parents++
					if len(candidate.Unsupported) == 0 {
						t.Fatal("whole matrix lost its explicit-selection requirement")
					}
				}
				if candidate.ParentJobID != parent {
					continue
				}
				children++
				if seen[candidate.ID] || !strings.HasPrefix(candidate.ID, parent+":matrix:") || len(strings.TrimPrefix(candidate.ID, parent+":matrix:")) != 64 {
					t.Fatalf("matrix scope identity is missing or duplicated: %q", candidate.ID)
				}
				seen[candidate.ID] = true
				if !strings.Contains(strings.Join(candidate.Unsupported, "; "), wantBlocker[fixture.Repository]) {
					t.Fatalf("remaining upstream prerequisite was silently removed: %+v", candidate)
				}
			}
			if parents != 1 || children != wantCount[fixture.Repository] {
				t.Fatalf("upstream scope lost: parents=%d children=%d", parents, children)
			}
			if again := setupImportCandidates(repo, "github-actions"); !reflect.DeepEqual(candidates, again) {
				t.Fatal("pinned workflow variant identities are nondeterministic")
			}
		})
	}
}

func assertPinnedMatrixOverlayValues(t *testing.T, repository string, variants []setupWorkflowMatrixVariant) {
	t.Helper()
	find := func(selection map[string]string) setupWorkflowMatrixVariant {
		t.Helper()
		var matches []setupWorkflowMatrixVariant
		for _, variant := range variants {
			match := true
			for key, value := range selection {
				actual, exists := variant.Values[key]
				match = match && exists && actual == value
			}
			if match {
				matches = append(matches, variant)
			}
		}
		if len(matches) != 1 {
			t.Fatalf("selection %v matched %d variants, want one", selection, len(matches))
		}
		return matches[0]
	}
	actionInput := func(variant setupWorkflowMatrixVariant, action, key string) any {
		t.Helper()
		for _, step := range variant.Job.Steps {
			if name, _, _ := strings.Cut(step.Uses, "@"); name == action {
				return step.With[key]
			}
		}
		t.Fatalf("original action %q disappeared", action)
		return nil
	}
	switch repository {
	case "caddyserver/caddy":
		for _, expected := range []map[string]string{
			{"go": "1.26", "GO_SEMVER": "~1.26.0", "os": "linux", "OS_LABEL": "ubuntu-latest", "CADDY_BIN_PATH": "./cmd/caddy/caddy", "SUCCESS": "0"},
			{"go": "1.26", "GO_SEMVER": "~1.26.0", "os": "mac", "OS_LABEL": "macos-14", "CADDY_BIN_PATH": "./cmd/caddy/caddy", "SUCCESS": "0"},
			{"go": "1.26", "GO_SEMVER": "~1.26.0", "os": "windows", "OS_LABEL": "windows-latest", "CADDY_BIN_PATH": "./cmd/caddy/caddy.exe", "SUCCESS": "True"},
		} {
			variant := find(map[string]string{"os": expected["os"]})
			if !reflect.DeepEqual(variant.Values, expected) || variant.Job.RunsOn != expected["OS_LABEL"] || actionInput(variant, "actions/setup-go", "go-version") != "~1.26.0" || actionInput(variant, "actions/upload-artifact", "path") != expected["CADDY_BIN_PATH"] {
				t.Fatalf("per-Go and per-OS overlays were not combined faithfully: %+v", variant)
			}
		}
	case "fastapi/fastapi":
		var base, appended, missingStarlette int
		for _, variant := range variants {
			v := variant.Values
			if v["os"] != "ubuntu-latest" && (v["python-version"] == "3.14" || v["python-version"] == "3.14t") {
				base++
				if v["starlette-src"] != "starlette-pypi" && v["starlette-src"] != "starlette-git" {
					t.Fatalf("original Starlette axis disappeared: %v", v)
				}
			} else {
				appended++
				if _, exists := v["starlette-src"]; !exists {
					missingStarlette++
					if variant.Job.Env["STARLETTE_SRC"] != "" {
						t.Fatalf("missing include field must evaluate to an empty string: %v", variant.Job.Env)
					}
				}
			}
			if variant.Job.RunsOn != v["os"] || variant.Job.Env["UV_PYTHON"] != v["python-version"] || variant.Job.Env["UV_RESOLUTION"] != v["uv-resolution"] || actionInput(variant, "actions/setup-python", "python-version") != v["python-version"] {
				t.Fatalf("appended runtime/environment was not concretized: %+v", variant)
			}
		}
		if base != 8 || appended != 7 || missingStarlette != 6 {
			t.Fatalf("include rows became a cross product or inherited missing fields: base=%d appended=%d absent=%d", base, appended, missingStarlette)
		}
		variant := find(map[string]string{"without-httpx2": "true"})
		if variant.Values["os"] != "ubuntu-latest" || variant.Values["python-version"] != "3.13" || variant.Values["coverage"] != "" || variant.Values["codspeed"] != "" {
			t.Fatalf("independent appended rows contaminated each other: %v", variant.Values)
		}
	case "gin-gonic/gin":
		for _, selection := range []map[string]string{
			{"os": "ubuntu-latest", "go": "1.26", "test-tags": "-race"},
			{"os": "macos-latest", "go": "1.27", "test-tags": `--ldflags="-checklinkname=0" -tags sonic`},
		} {
			variant := find(selection)
			wantPath := "~/.cache/go-build"
			if selection["os"] == "macos-latest" {
				wantPath = "~/Library/Caches/go-build"
			}
			if variant.Values["go-build"] != wantPath || variant.Job.Env["TESTTAGS"] != selection["test-tags"] || actionInput(variant, "actions/setup-go", "go-version") != selection["go"] || actionInput(variant, "actions/cache", "path") != wantPath+"\n~/go/pkg/mod\n" {
				t.Fatalf("per-OS cache overlay changed test flags or runtime: %+v", variant)
			}
		}
	case "google/guava":
		var ubuntu, windows int
		for _, variant := range variants {
			if variant.Values["os"] == "ubuntu-latest" {
				ubuntu++
			} else if variant.Values["os"] == "windows-latest" {
				windows++
				if !reflect.DeepEqual(variant.Values, map[string]string{"os": "windows-latest", "java": "25", "root-pom": "pom.xml"}) {
					t.Fatalf("additional Windows include gained sibling axis values: %v", variant.Values)
				}
			}
			if variant.Job.Env["ROOT_POM"] != variant.Values["root-pom"] || actionInput(variant, "actions/setup-java", "java-version") != variant.Values["java"]+"\n27\n" || !strings.Contains(variant.Job.Steps[3].Run, "-Dsurefire.toolchain.version="+variant.Values["java"]+" -f $ROOT_POM") {
				t.Fatalf("matrix JDK/POM or separate JDK27 prerequisite changed: %+v", variant)
			}
		}
		if ubuntu != 8 || windows != 1 {
			t.Fatalf("additional include replaced or multiplied original rows: ubuntu=%d windows=%d", ubuntu, windows)
		}
	default:
		t.Fatalf("unreviewed original fixture %q", repository)
	}
}
