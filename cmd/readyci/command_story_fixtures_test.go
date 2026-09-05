package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestFixturesUpdateAppliesLatestRunArtifacts(t *testing.T) {
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "tests", "fixtures"), 0o755); err != nil {
		t.Fatalf("mkdir fixtures: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "tests", "fixtures", "summary.json"), []byte("remote fixture"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var archive bytes.Buffer
	buildArtifactArchiveForTest(t, source, []string{"tests/fixtures/**"}, &archive)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs":
			if r.URL.Query().Get("status") != preflightapi.StatusSucceeded {
				t.Fatalf("fixtures latest status query = %q, want succeeded", r.URL.Query().Get("status"))
			}
			writeCLITestJSON(w, http.StatusOK, preflightapi.ListRunsResponse{Runs: []preflightapi.RunSummary{{RunID: "run_latest_fixture", ProjectID: "airline", Status: preflightapi.StatusSucceeded}}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_latest_fixture/artifacts":
			writeCLITestJSON(w, http.StatusOK, preflightapi.ListArtifactsResponse{
				RunID: "run_latest_fixture",
				Artifacts: []preflightapi.ArtifactSummary{{
					ID:        "art_fixture",
					RunID:     "run_latest_fixture",
					Name:      "readyci-artifacts",
					Path:      "readyci-artifacts.tar.gz",
					SizeBytes: int64(archive.Len()),
				}},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_latest_fixture/artifacts/archive":
			_, _ = w.Write(archive.Bytes())
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cliTestWorkingDirectoryMu.Lock()
	defer cliTestWorkingDirectoryMu.Unlock()
	repo := t.TempDir()
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	}()
	runGitForCLITest(t, "init")
	if err := os.WriteFile(".readyci.yml", []byte("project_id: airline\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return fixturesUpdateCommand([]string{"--api-url", server.URL, "--token", "pfl_fixture", "--paths", "tests/fixtures/**"})
	})
	if err != nil {
		t.Fatalf("fixtures update: %v\n%s", err, output)
	}
	requireCLITestOutputContains(t, output, "applied 1 artifact file(s) from run run_latest_fixture", "tests/fixtures/summary.json")
	data, err := os.ReadFile(filepath.Join(repo, "tests", "fixtures", "summary.json"))
	if err != nil {
		t.Fatalf("read applied fixture: %v", err)
	}
	if string(data) != "remote fixture" {
		t.Fatalf("fixture content = %q, want remote fixture", data)
	}
}

func TestFixturesUpdateLatestFailsWhenSuccessfulRunHasNoArtifacts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs":
			if r.URL.Query().Get("status") != preflightapi.StatusSucceeded {
				t.Fatalf("fixtures latest status query = %q, want succeeded", r.URL.Query().Get("status"))
			}
			writeCLITestJSON(w, http.StatusOK, preflightapi.ListRunsResponse{Runs: []preflightapi.RunSummary{{RunID: "run_without_artifacts", ProjectID: "airline", Status: preflightapi.StatusSucceeded}}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_without_artifacts/artifacts":
			writeCLITestJSON(w, http.StatusOK, preflightapi.ListArtifactsResponse{RunID: "run_without_artifacts"})
		case strings.Contains(r.URL.Path, "/artifacts/archive"):
			t.Fatalf("fixtures latest downloaded artifacts for artifact-less run")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	repo := chdirCLITestRepo(t, "")
	if err := os.WriteFile(filepath.Join(repo, ".readyci.yml"), []byte("project_id: airline\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	err := fixturesUpdateCommand([]string{"--api-url", server.URL, "--token", "pfl_fixture", "--paths", "tests/fixtures/**", "--dry-run"})
	if err == nil || !strings.Contains(err.Error(), "latest successful run run_without_artifacts has no artifacts") {
		t.Fatalf("fixtures update error = %v, want no-artifacts error", err)
	}
}

func TestFixturesUpdateLatestRequiresProject(t *testing.T) {
	t.Setenv("READYCI_API_TOKEN", "pfl_fixture")
	t.Setenv("READYCI_PROJECT_ID", "")
	chdirCLITestRepo(t, "")

	err := fixturesUpdateCommand([]string{"--paths", "tests/fixtures/**", "--dry-run"})
	if err == nil || !strings.Contains(err.Error(), "requires --project or project_id") {
		t.Fatalf("fixtures update error = %v, want project requirement", err)
	}
}

func TestFixturesUpdateLatestRejectsUnsafeProjectID(t *testing.T) {
	err := fixturesUpdateCommand([]string{"--api-url", "https://api.readyci.test", "--token", "pfl_fixture", "--project", "airline\nshadow", "--paths", "tests/fixtures/**", "--dry-run"})
	if err == nil || !strings.Contains(err.Error(), "project_id must not contain control characters") {
		t.Fatalf("fixtures update unsafe project err = %v, want project_id rejection", err)
	}
}
