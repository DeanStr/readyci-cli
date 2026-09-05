package load

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/internal/syncbundlebuild"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

const defaultLoadRunCount = 3

func TestDogfoodRunEventStatusAndArtifactBaselines(t *testing.T) {
	if strings.ToLower(strings.TrimSpace(os.Getenv("READYCI_E2E_LOAD_ENABLED"))) != "true" {
		t.Skip("set READYCI_E2E_LOAD_ENABLED=true to run dogfood load baselines")
	}
	apiURL := strings.TrimSpace(os.Getenv("READYCI_API_URL"))
	token := strings.TrimSpace(os.Getenv("READYCI_API_TOKEN"))
	projectID := strings.TrimSpace(os.Getenv("READYCI_PROJECT_ID"))
	if apiURL == "" || token == "" || projectID == "" {
		t.Fatal("READYCI_API_URL, READYCI_API_TOKEN, and READYCI_PROJECT_ID are required")
	}
	runCount := envInt("READYCI_E2E_LOAD_RUNS", defaultLoadRunCount)
	if runCount <= 0 {
		t.Fatal("READYCI_E2E_LOAD_RUNS must be positive")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(runCount)*5*time.Minute)
	defer cancel()

	repo := prepareLoadRepo(t)
	client := api.NewClient(apiURL, api.WithToken(token))
	metrics := map[string][]time.Duration{}
	for i := 0; i < runCount; i++ {
		runID := createAndUploadLoadRun(ctx, t, client, repo, projectID, i, metrics)
		waitForLoadRun(ctx, t, client, runID, metrics)
		statusStart := time.Now()
		detail, err := client.GetRun(ctx, runID)
		recordMetric(metrics, "runs_status", statusStart)
		if err != nil {
			t.Fatalf("get run status for %s: %v", runID, err)
		}
		if detail.Summary.Status != preflightapi.StatusSucceeded {
			t.Fatalf("run %s status = %s, want succeeded", runID, detail.Summary.Status)
		}
		artifactStart := time.Now()
		artifacts, err := client.ListArtifacts(ctx, runID)
		recordMetric(metrics, "artifact_metadata", artifactStart)
		if err != nil {
			t.Fatalf("list artifacts for %s: %v", runID, err)
		}
		if len(artifacts.Artifacts) == 0 {
			t.Fatalf("run %s produced no artifact metadata", runID)
		}
	}
	for name, samples := range metrics {
		t.Logf("%s %s", name, percentileSummary(samples))
	}
}

func createAndUploadLoadRun(ctx context.Context, t *testing.T, client *api.Client, repo, projectID string, index int, metrics map[string][]time.Duration) string {
	t.Helper()
	bundleStart := time.Now()
	bundlePath, bundle, _, _, err := syncbundlebuild.BuildFullAndDeltaToTempWithOptions(ctx, repo, syncbundlebuild.BuildOptions{ChangeMode: syncbundle.ChangeModeAll})
	recordMetric(metrics, "bundle_build", bundleStart)
	if err != nil {
		t.Fatalf("build sync bundle: %v", err)
	}
	defer os.Remove(bundlePath)

	command := fmt.Sprintf("mkdir -p readyci-load && printf 'load-%d\\n' > readyci-load/result.txt", index)
	createStart := time.Now()
	createResp, err := client.CreateRun(ctx, preflightapi.CreateRunRequest{
		ProjectID:      projectID,
		Region:         preflightapi.DefaultRegion,
		IdempotencyKey: fmt.Sprintf("load-%d-%d", time.Now().UnixNano(), index),
		Image:          preflightapi.DefaultImage,
		Command:        command,
		CommandAlias:   "load-baseline",
		RunnerSize:     preflightapi.DefaultRunnerSize,
		TimeoutSeconds: 10 * 60,
		CacheMode:      "cache-only",
		ArtifactPaths:  []string{"readyci-load/**"},
		NetworkMode:    preflightapi.DefaultNetworkMode,
		Client: preflightapi.ClientInfo{
			Version:  "load-test",
			Platform: runtime.GOOS + "-" + runtime.GOARCH,
		},
		Git: preflightapi.GitInfo{
			Dirty:           true,
			Materialization: "minimal_git",
			Repository:      "readyci-load-baseline",
		},
	})
	recordMetric(metrics, "run_create", createStart)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if createResp.Status != preflightapi.StatusAwaitingUpload {
		return createResp.RunID
	}

	uploadStart := time.Now()
	proof := uploadLoadBundle(ctx, t, client, createResp, bundlePath, bundle)
	recordMetric(metrics, "bundle_upload", uploadStart)
	completeStart := time.Now()
	if createResp.UploadWorkerID != "" {
		_, err = client.CompleteUploadWithRequest(ctx, createResp.RunID, preflightapi.CompleteUploadRequest{WorkerUploadProof: &proof})
	} else {
		_, err = client.CompleteUpload(ctx, createResp.RunID)
	}
	recordMetric(metrics, "upload_complete", completeStart)
	if err != nil {
		t.Fatalf("complete upload for %s: %v", createResp.RunID, err)
	}
	return createResp.RunID
}

func uploadLoadBundle(ctx context.Context, t *testing.T, client *api.Client, createResp preflightapi.CreateRunResponse, bundlePath string, bundle syncbundlebuild.BuildResult) preflightapi.WorkerBundleUploadProof {
	t.Helper()
	file, err := os.Open(bundlePath)
	if err != nil {
		t.Fatalf("open bundle: %v", err)
	}
	defer file.Close()
	var uploadResp preflightapi.WorkerBundleUploadResponse
	options := api.UploadBundleOptions{
		Method:                        createResp.UploadMethod,
		Headers:                       createResp.UploadHeaders,
		AllowCrossOrigin:              len(createResp.UploadHeaders) > 0,
		TreatAlreadyUploadedAsSuccess: createResp.UploadWorkerID == "",
	}
	if createResp.UploadWorkerID != "" {
		options.DecodeJSONResponse = &uploadResp
	}
	if err := client.UploadBundleWithOptions(ctx, createResp.UploadURL, io.NewSectionReader(file, 0, bundle.Size), bundle.Size, options); err != nil {
		t.Fatalf("upload bundle for %s: %v", createResp.RunID, err)
	}
	if createResp.UploadWorkerID != "" && strings.TrimSpace(uploadResp.Proof.Signature) == "" {
		t.Fatalf("worker upload for %s did not return proof", createResp.RunID)
	}
	return uploadResp.Proof
}

func waitForLoadRun(ctx context.Context, t *testing.T, client *api.Client, runID string, metrics map[string][]time.Duration) {
	t.Helper()
	streamStart := time.Now()
	err := client.StreamRunEvents(ctx, runID, func(event preflightapi.RunEvent) error {
		if event.Type == preflightapi.EventRunFailed || event.Type == preflightapi.EventRunTimedOut || event.Type == preflightapi.EventRunCancelled {
			return fmt.Errorf("run %s ended with %s: %s", runID, event.Type, event.Message)
		}
		return nil
	})
	recordMetric(metrics, "event_stream", streamStart)
	if err != nil {
		t.Fatalf("stream events for %s: %v", runID, err)
	}
}

func prepareLoadRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	run("init")
	run("config", "user.email", "readyci@example.invalid")
	run("config", "user.name", "Ready CI")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("readyci load baseline\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	run("add", ".")
	run("commit", "-m", "load baseline")
	return repo
}

func recordMetric(metrics map[string][]time.Duration, name string, start time.Time) {
	metrics[name] = append(metrics[name], time.Since(start))
}

func percentileSummary(samples []time.Duration) string {
	if len(samples) == 0 {
		return "count=0"
	}
	sorted := append([]time.Duration(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return fmt.Sprintf("count=%d p50=%s p95=%s p99=%s", len(sorted), percentile(sorted, 0.50), percentile(sorted, 0.95), percentile(sorted, 0.99))
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	index := int(float64(len(sorted)-1) * p)
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

func envInt(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}
