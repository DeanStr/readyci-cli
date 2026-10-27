package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/artifactbundle"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestListArtifactsForAgentHandoffRejectsUnboundPrereleaseArtifact(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeCLITestJSON(w, http.StatusOK, preflightapi.ListArtifactsResponse{
			RunID: "run_legacy",
			Artifacts: []preflightapi.ArtifactSummary{{
				ID: "artifact_legacy", RunID: "run_legacy", Name: "artifacts.tar.gz",
				Path: "artifacts.tar.gz", SizeBytes: 42,
			}},
		})
	}))
	defer server.Close()

	_, err := listArtifactsForAgentHandoff(context.Background(), agentArtifactHandoffOptions{
		APIURL: server.URL, APIToken: "pfl_fixture", RunID: "run_legacy",
	})
	if err == nil || !strings.Contains(err.Error(), "not bound to stored content") {
		t.Fatalf("checksum-less artifact error = %v, want explicit content-binding rejection", err)
	}
}

func TestAgentArtifactsPlanReturnsHandoffPacket(t *testing.T) {
	expiresAt := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "tests", "fixtures"), 0o755); err != nil {
		t.Fatalf("mkdir source fixtures: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "tests", "fixtures", "summary.json"), []byte("remote fixture"), 0o644); err != nil {
		t.Fatalf("write remote fixture: %v", err)
	}
	var archive bytes.Buffer
	buildArtifactArchiveForTest(t, source, []string{"tests/fixtures/**"}, &archive)
	archiveSHA256 := fmt.Sprintf("%x", sha256.Sum256(archive.Bytes()))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_fixture/artifacts" {
			writeCLITestJSON(w, http.StatusOK, preflightapi.ListArtifactsResponse{
				RunID: "run_fixture",
				Artifacts: []preflightapi.ArtifactSummary{{
					ID:            "artifact_fixture",
					RunID:         "run_fixture",
					Name:          "readyci-artifacts",
					Path:          "readyci-artifacts.tar.gz",
					SizeBytes:     64,
					ContentSHA256: archiveSHA256,
					ExpiresAt:     expiresAt,
				}},
			})
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_fixture/artifacts/archive" {
			_, _ = w.Write(archive.Bytes())
			return
		}
		http.NotFound(w, r)
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
	runGitForCLITest(t, "init")
	defer func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	}()

	output, err := captureStdoutForCLITest(t, func() error {
		return agentCommand([]string{
			"artifacts", "plan",
			"--api-url", server.URL,
			"--token", "pfl_fixture",
			"--json",
			"run_fixture",
			"--paths", "tests/fixtures/**",
		})
	})
	if err != nil {
		t.Fatalf("agent artifacts plan: %v\n%s", err, output)
	}
	var report agentArtifactHandoffReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode agent artifacts plan: %v\n%s", err, output)
	}
	if report.SchemaVersion != agentSchemaVersion || report.Status != "ready" || report.RunID != "run_fixture" || !report.DryRun || report.Confirmed {
		t.Fatalf("artifact plan report = %+v, want ready dry-run handoff packet", report)
	}
	if len(report.Artifacts) != 1 || report.Artifacts[0].ID != "artifact_fixture" {
		t.Fatalf("artifact plan artifacts = %+v, want fixture artifact", report.Artifacts)
	}
	if report.MatchedCount != 1 || len(report.Files) != 1 || report.Files[0].Path != "tests/fixtures/summary.json" {
		t.Fatalf("artifact plan files = count %d files %+v, want matched fixture", report.MatchedCount, report.Files)
	}
	if len(report.NextActions) != 2 || report.NextActions[0].Kind != "artifacts_apply_dry_run" || report.NextActions[1].Kind != "artifacts_apply_confirm" {
		t.Fatalf("artifact plan next actions = %+v, want dry-run and confirm actions", report.NextActions)
	}
	wantPreview := "readyci agent artifacts apply --api-url " + server.URL +
		" --json run_fixture --content-sha256 " + archiveSHA256 +
		" --paths 'tests/fixtures/**'"
	if report.ApplyCommand != wantPreview || report.NextActions[0].Command != wantPreview {
		t.Fatalf("apply command = %q next=%q, want %q", report.ApplyCommand, report.NextActions[0].Command, wantPreview)
	}
	if !strings.Contains(report.ConfirmCommand, "--confirm") {
		t.Fatalf("confirm command = %q, want --confirm", report.ConfirmCommand)
	}
}

func TestAgentArtifactsPlanRejectsArchiveChangedAfterListing(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "generated.txt"), []byte("replacement"), 0o644); err != nil {
		t.Fatalf("write generated file: %v", err)
	}
	var archive bytes.Buffer
	buildArtifactArchiveForTest(t, source, []string{"generated.txt"}, &archive)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/runs/run_replaced/artifacts":
			writeCLITestJSON(w, http.StatusOK, preflightapi.ListArtifactsResponse{
				RunID: "run_replaced",
				Artifacts: []preflightapi.ArtifactSummary{{
					ID:            "artifact_replaced",
					RunID:         "run_replaced",
					Path:          "artifacts.tar.gz",
					ContentSHA256: strings.Repeat("a", 64),
				}},
			})
		case "/v1/runs/run_replaced/artifacts/archive":
			_, _ = w.Write(archive.Bytes())
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	repo := chdirCLITestRepo(t, "")
	runGitForCLITest(t, "init")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("test\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	_, err := captureStdoutForCLITest(t, func() error {
		return agentCommand([]string{
			"artifacts", "plan",
			"--api-url", server.URL,
			"--token", "pfl_fixture",
			"--json",
			"run_replaced",
			"--paths", "generated.txt",
		})
	})
	if err == nil || !strings.Contains(err.Error(), "downloaded artifact content checksum mismatch") {
		t.Fatalf("artifact replacement error = %v, want checksum mismatch", err)
	}
}

func TestAgentArtifactsPlanIdentifiesEvidencePacks(t *testing.T) {
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "test-results", "login"), 0o755); err != nil {
		t.Fatalf("mkdir test results: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(source, "playwright-report"), 0o755); err != nil {
		t.Fatalf("mkdir playwright report: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "test-results", "login", "trace.zip"), []byte("trace"), 0o644); err != nil {
		t.Fatalf("write trace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "playwright-report", "index.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatalf("write report: %v", err)
	}
	var archive bytes.Buffer
	buildArtifactArchiveForTest(t, source, []string{"test-results/**", "playwright-report/**"}, &archive)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_ui/artifacts":
			writeCLITestJSON(w, http.StatusOK, preflightapi.ListArtifactsResponse{
				RunID: "run_ui",
				Artifacts: []preflightapi.ArtifactSummary{{
					ID:        "artifact_ui",
					RunID:     "run_ui",
					Name:      "readyci-artifacts",
					Path:      "readyci-artifacts.tar.gz",
					SizeBytes: int64(archive.Len()),
					ContentSHA256: fmt.Sprintf(
						"%x", sha256.Sum256(archive.Bytes()),
					),
				}},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_ui/artifacts/archive":
			_, _ = w.Write(archive.Bytes())
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	repo := chdirCLITestRepo(t, "")
	runGitForCLITest(t, "init")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("evidence\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return agentCommand([]string{
			"artifacts", "plan",
			"--api-url", server.URL,
			"--token", "pfl_fixture",
			"--json",
			"run_ui",
			"--paths", "test-results/**",
			"--paths", "playwright-report/**",
		})
	})
	if err != nil {
		t.Fatalf("agent artifacts evidence plan: %v\n%s", err, output)
	}
	var report agentArtifactHandoffReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode evidence plan: %v\n%s", err, output)
	}
	if report.MatchedCount != 2 || len(report.EvidencePacks) != 2 {
		t.Fatalf("evidence plan = matched %d packs %+v, want two evidence packs", report.MatchedCount, report.EvidencePacks)
	}
	if !containsEvidencePackKind(report.EvidencePacks, "playwright_report") || !containsEvidencePackKind(report.EvidencePacks, "playwright_trace") {
		t.Fatalf("evidence packs = %+v, want playwright report and trace", report.EvidencePacks)
	}
	for _, pack := range report.EvidencePacks {
		if pack.PreviewCommand == "" || len(pack.Files) == 0 {
			t.Fatalf("evidence pack = %+v, want preview command and files", pack)
		}
	}
}

func TestAgentArtifactsPlanReportsNoMatches(t *testing.T) {
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "tests", "fixtures"), 0o755); err != nil {
		t.Fatalf("mkdir source fixtures: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "tests", "fixtures", "summary.json"), []byte("remote fixture"), 0o644); err != nil {
		t.Fatalf("write remote fixture: %v", err)
	}
	var archive bytes.Buffer
	buildArtifactArchiveForTest(t, source, []string{"tests/fixtures/**"}, &archive)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_fixture/artifacts":
			writeCLITestJSON(w, http.StatusOK, preflightapi.ListArtifactsResponse{
				RunID: "run_fixture",
				Artifacts: []preflightapi.ArtifactSummary{{
					ID:        "artifact_fixture",
					RunID:     "run_fixture",
					Name:      "readyci-artifacts",
					Path:      "readyci-artifacts.tar.gz",
					SizeBytes: int64(archive.Len()),
					ContentSHA256: fmt.Sprintf(
						"%x", sha256.Sum256(archive.Bytes()),
					),
				}},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_fixture/artifacts/archive":
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
	runGitForCLITest(t, "init")
	defer func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	}()

	output, err := captureStdoutForCLITest(t, func() error {
		return agentCommand([]string{
			"artifacts", "plan",
			"--api-url", server.URL,
			"--token", "pfl_fixture",
			"--json",
			"run_fixture",
			"--paths", "test-results/**",
		})
	})
	if err != nil {
		t.Fatalf("agent artifacts plan no matches: %v\n%s", err, output)
	}
	var report agentArtifactHandoffReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode no-match plan: %v\n%s", err, output)
	}
	if report.Status != "no_matches" || report.MatchedCount != 0 || len(report.Files) != 0 || len(report.NextActions) != 0 || report.ConfirmCommand != "" {
		t.Fatalf("no-match artifact plan = %+v, want no_matches without confirm action", report)
	}
	if len(report.EvidencePacks) != 0 {
		t.Fatalf("no-match evidence packs = %+v, want none when archive contains no matching evidence files", report.EvidencePacks)
	}
}

func containsEvidencePackKind(packs []agentEvidencePack, kind string) bool {
	for _, pack := range packs {
		if pack.Kind == kind {
			return true
		}
	}
	return false
}

func TestEvidencePacksKeepStandaloneLogPathsNarrow(t *testing.T) {
	packs := evidencePacksFromArtifactFiles("", "run_logs", []artifactbundle.File{
		{Path: "catalogs/index.json", SizeBytes: 16},
		{Path: "blogs/output.json", SizeBytes: 24},
		{Path: "reports/output.log.json", SizeBytes: 32},
		{Path: "app/logs/browser.log", SizeBytes: 64},
	})
	if len(packs) != 2 {
		t.Fatalf("evidence packs = %+v, want only standalone log file and logs directory packs", packs)
	}
	if containsEvidencePackPath(packs, "catalogs/**") || containsEvidencePackPath(packs, "blogs/**") {
		t.Fatalf("evidence packs = %+v, did not expect path segments containing logs to count as log evidence", packs)
	}
	if !containsEvidencePackPath(packs, "reports/output.log.json") {
		t.Fatalf("evidence packs = %+v, want standalone log file path kept exact", packs)
	}
	if containsEvidencePackPath(packs, "reports/**") {
		t.Fatalf("evidence packs = %+v, did not expect broad reports log glob", packs)
	}
	if !containsEvidencePackPath(packs, "app/logs/**") {
		t.Fatalf("evidence packs = %+v, want dedicated logs directory grouped", packs)
	}
}

func containsEvidencePackPath(packs []agentEvidencePack, path string) bool {
	for _, pack := range packs {
		if pack.Path == path {
			return true
		}
	}
	return false
}

func TestAgentArtifactsPlanJSONReportsArchiveInspectionError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_fixture/artifacts":
			writeCLITestJSON(w, http.StatusOK, preflightapi.ListArtifactsResponse{
				RunID: "run_fixture",
				Artifacts: []preflightapi.ArtifactSummary{{
					ID:        "artifact_fixture",
					RunID:     "run_fixture",
					Name:      "readyci-artifacts",
					Path:      "readyci-artifacts.tar.gz",
					SizeBytes: 16,
					ContentSHA256: fmt.Sprintf(
						"%x", sha256.Sum256([]byte("not a gzip archive")),
					),
				}},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_fixture/artifacts/archive":
			_, _ = w.Write([]byte("not a gzip archive"))
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
	runGitForCLITest(t, "init")
	defer func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	}()

	output, err := captureStdoutForCLITest(t, func() error {
		return agentCommand([]string{
			"artifacts", "plan",
			"--api-url", server.URL,
			"--token", "pfl_fixture",
			"--json",
			"run_fixture",
			"--paths", "tests/fixtures/**",
		})
	})
	if err == nil {
		t.Fatalf("agent artifacts plan succeeded, want archive inspection error\n%s", output)
	}
	var report agentArtifactHandoffReport
	if decodeErr := json.Unmarshal([]byte(output), &report); decodeErr != nil {
		t.Fatalf("decode error plan report: %v\n%s", decodeErr, output)
	}
	if report.Status != "error" || report.Error == "" || len(report.NextActions) != 0 {
		t.Fatalf("error plan report = %+v, want status error with no next actions", report)
	}
	if report.ApplyCommand != "" || report.ConfirmCommand != "" || len(report.Warnings) != 0 {
		t.Fatalf("error plan commands/warnings = apply %q confirm %q warnings %+v, want empty", report.ApplyCommand, report.ConfirmCommand, report.Warnings)
	}
	if report.SchemaVersion != agentSchemaVersion || report.RunID != "run_fixture" || !report.DryRun || report.Confirmed {
		t.Fatalf("error plan report metadata = %+v, want dry-run handoff metadata", report)
	}
}

func TestAgentArtifactsApplyPreviewsByDefaultAndConfirmsWrites(t *testing.T) {
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "tests", "fixtures"), 0o755); err != nil {
		t.Fatalf("mkdir source fixtures: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "tests", "fixtures", "summary.json"), []byte("remote fixture"), 0o644); err != nil {
		t.Fatalf("write remote fixture: %v", err)
	}
	var archive bytes.Buffer
	buildArtifactArchiveForTest(t, source, []string{"tests/fixtures/**"}, &archive)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_fixture/artifacts/archive" {
			_, _ = w.Write(archive.Bytes())
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	cliTestWorkingDirectoryMu.Lock()
	defer cliTestWorkingDirectoryMu.Unlock()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "tests", "fixtures"), 0o755); err != nil {
		t.Fatalf("mkdir repo fixtures: %v", err)
	}
	fixturePath := filepath.Join(repo, "tests", "fixtures", "summary.json")
	if err := os.WriteFile(fixturePath, []byte("local fixture"), 0o644); err != nil {
		t.Fatalf("write local fixture: %v", err)
	}
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}
	runGitForCLITest(t, "init")
	defer func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	}()

	previewOutput, err := captureStdoutForCLITest(t, func() error {
		return agentCommand([]string{
			"artifacts", "apply",
			"--api-url", server.URL,
			"--token", "pfl_fixture",
			"--json",
			"run_fixture",
			"--paths", "tests/fixtures/**",
		})
	})
	if err != nil {
		t.Fatalf("agent artifacts apply preview: %v\n%s", err, previewOutput)
	}
	var preview agentArtifactHandoffReport
	if err := json.Unmarshal([]byte(previewOutput), &preview); err != nil {
		t.Fatalf("decode preview: %v\n%s", err, previewOutput)
	}
	if preview.Status != "previewed" || !preview.DryRun || preview.Confirmed || preview.MatchedCount != 1 || preview.AppliedCount != 0 || len(preview.Files) != 1 {
		t.Fatalf("preview report = %+v, want dry-run one-match report", preview)
	}
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read preview fixture: %v", err)
	}
	if string(data) != "local fixture" {
		t.Fatalf("preview fixture content = %q, want local fixture", data)
	}

	confirmOutput, err := captureStdoutForCLITest(t, func() error {
		return agentCommand([]string{
			"artifacts", "apply",
			"--api-url", server.URL,
			"--token", "pfl_fixture",
			"--json",
			"run_fixture",
			"--confirm",
			"--paths", "tests/fixtures/**",
		})
	})
	if err != nil {
		t.Fatalf("agent artifacts apply confirm: %v\n%s", err, confirmOutput)
	}
	var confirmed agentArtifactHandoffReport
	if err := json.Unmarshal([]byte(confirmOutput), &confirmed); err != nil {
		t.Fatalf("decode confirmed apply: %v\n%s", err, confirmOutput)
	}
	if confirmed.Status != "applied" || confirmed.DryRun || !confirmed.Confirmed || confirmed.MatchedCount != 1 || confirmed.AppliedCount != 1 {
		t.Fatalf("confirmed report = %+v, want applied one-match report", confirmed)
	}
	data, err = os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read confirmed fixture: %v", err)
	}
	if string(data) != "remote fixture" {
		t.Fatalf("confirmed fixture content = %q, want remote fixture", data)
	}
}

func TestEvidencePacksRecognizeExplicitCompressedJUnit(t *testing.T) {
	for _, path := range []string{"reports/TEST-api.xml.gz", "reports/api.junit.tar.gz", "reports/api.junit.tgz"} {
		kind, _, ok := evidencePackKindForPath(path)
		if !ok || kind != "junit" {
			t.Errorf("compressed report %q kind=%q accepted=%v", path, kind, ok)
		}
	}
}
