package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func TestRunFallsBackToFullBundleWhenWorkerDeltaBaselineIsStale(t *testing.T) {
	cliTestWorkingDirectoryMu.Lock()
	defer cliTestWorkingDirectoryMu.Unlock()
	setupDeltaFallbackRepo(t)

	var serverURL string
	var uploadBaselineRefs []bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs":
			writeCLITestJSON(w, http.StatusCreated, preflightapi.CreateRunResponse{
				RunID:                  "run_delta",
				UploadURL:              serverURL + "/upload",
				UploadHeaders:          map[string]string{"Content-Type": syncbundle.BundleMediaType},
				UploadWorkerID:         "worker-local",
				EventsURL:              serverURL + "/events",
				Status:                 preflightapi.StatusAwaitingUpload,
				BundleBaselineReusable: true,
			})
		case r.Method == http.MethodPut && r.URL.Path == "/upload":
			manifest, err := syncbundle.ReadManifest(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			uploadBaselineRefs = append(uploadBaselineRefs, manifest.BaselineRef)
			if len(uploadBaselineRefs) == 1 {
				if !manifest.BaselineRef {
					t.Errorf("first upload used full bundle, want delta")
					http.Error(w, "expected delta", http.StatusBadRequest)
					return
				}
				http.Error(w, "referenced baseline is not available on this worker", http.StatusConflict)
				return
			}
			if manifest.BaselineRef {
				t.Errorf("fallback upload still used delta")
				http.Error(w, "expected full bundle", http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(preflightapi.WorkerBundleUploadResponse{
				Status: "uploaded",
				Bytes:  123,
				Proof: preflightapi.WorkerBundleUploadProof{
					RunID:       "run_delta",
					WorkerID:    "worker-local",
					ExpiresUnix: time.Now().Add(time.Minute).Unix(),
					SizeBytes:   123,
					Signature:   "test-signature",
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs/run_delta/upload-complete":
			writeCLITestJSON(w, http.StatusOK, preflightapi.CompleteUploadResponse{RunID: "run_delta", Status: preflightapi.StatusQueued})
		case r.Method == http.MethodGet && r.URL.Path == "/events":
			writeCLITestFinishedEvent(w)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	_, err := executeRemoteRunInternal(remoteRunOptions{
		APIURL:      server.URL,
		ProjectID:   "proj_dev",
		Command:     "cat README.md",
		CacheMode:   "cache-only",
		NetworkMode: "none",
	}, remoteRunOutputCollect)
	if err != nil && !isSuccessfulExit(err) {
		t.Fatalf("run error = %v, want successful terminal exit", err)
	}
	assertUploadBaselineRefs(t, uploadBaselineRefs, []bool{true, false})
}

func TestRunWarnsWhenUploadingUntrackedFiles(t *testing.T) {
	cliTestWorkingDirectoryMu.Lock()
	defer cliTestWorkingDirectoryMu.Unlock()
	setupDeltaFallbackRepo(t)

	includeUntracked := true
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs":
			writeCLITestJSON(w, http.StatusCreated, preflightapi.CreateRunResponse{
				RunID:     "run_untracked_warning",
				UploadURL: serverURL + "/upload",
				EventsURL: serverURL + "/events",
				Status:    preflightapi.StatusAwaitingUpload,
			})
		case r.Method == http.MethodPut && r.URL.Path == "/upload":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte("{}\n"))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs/run_untracked_warning/upload-complete":
			writeCLITestJSON(w, http.StatusOK, preflightapi.CompleteUploadResponse{RunID: "run_untracked_warning", Status: preflightapi.StatusQueued})
		case r.Method == http.MethodGet && r.URL.Path == "/events":
			writeCLITestFinishedEvent(w)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	stderr, err := captureStderrForCLITest(t, func() error {
		_, err := executeRemoteRunInternal(remoteRunOptions{
			APIURL:           server.URL,
			ProjectID:        "proj_dev",
			Command:          "cat README.md",
			CacheMode:        "cache-only",
			NetworkMode:      "none",
			IncludeUntracked: &includeUntracked,
		}, remoteRunOutputHuman)
		if err != nil && isSuccessfulExit(err) {
			return nil
		}
		return err
	})
	if err != nil {
		t.Fatalf("run error = %v, want successful terminal exit", err)
	}
	if !strings.Contains(stderr, "readyci: warning: untracked files included in upload") || !strings.Contains(stderr, "local-change.txt") {
		t.Fatalf("stderr missing untracked warning/path:\n%s", stderr)
	}
}

func TestRunFallsBackToFullBundleWhenUploadCompleteRejectsStaleDelta(t *testing.T) {
	cliTestWorkingDirectoryMu.Lock()
	defer cliTestWorkingDirectoryMu.Unlock()
	setupDeltaFallbackRepo(t)

	var serverURL string
	var uploadBaselineRefs []bool
	completeCalls := 0
	hasStoredUpload := false
	storedUploadBaselineRef := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs":
			writeCLITestJSON(w, http.StatusCreated, preflightapi.CreateRunResponse{
				RunID:                  "run_delta",
				UploadURL:              serverURL + "/upload",
				EventsURL:              serverURL + "/events",
				Status:                 preflightapi.StatusAwaitingUpload,
				BundleBaselineReusable: true,
			})
		case r.Method == http.MethodPut && r.URL.Path == "/upload":
			if hasStoredUpload {
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"status":"already_uploaded"}` + "\n"))
				return
			}
			manifest, err := syncbundle.ReadManifest(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			hasStoredUpload = true
			storedUploadBaselineRef = manifest.BaselineRef
			uploadBaselineRefs = append(uploadBaselineRefs, manifest.BaselineRef)
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte("{}\n"))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs/run_delta/upload-complete":
			completeCalls++
			if completeCalls == 1 {
				if !storedUploadBaselineRef {
					t.Errorf("first complete saw full bundle, want stale delta")
				}
				hasStoredUpload = false
				http.Error(w, "invalid bundle: referenced bundle baseline is not available", http.StatusBadRequest)
				return
			}
			if storedUploadBaselineRef {
				t.Errorf("fallback complete still saw stale delta")
				http.Error(w, "invalid bundle: referenced bundle baseline is not available", http.StatusBadRequest)
				return
			}
			writeCLITestJSON(w, http.StatusOK, preflightapi.CompleteUploadResponse{RunID: "run_delta", Status: preflightapi.StatusQueued})
		case r.Method == http.MethodGet && r.URL.Path == "/events":
			writeCLITestFinishedEvent(w)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	_, err := executeRemoteRunInternal(remoteRunOptions{
		APIURL:      server.URL,
		ProjectID:   "proj_dev",
		Command:     "cat README.md",
		CacheMode:   "cache-only",
		NetworkMode: "none",
	}, remoteRunOutputCollect)
	if err != nil && !isSuccessfulExit(err) {
		t.Fatalf("run error = %v, want successful terminal exit", err)
	}
	assertUploadBaselineRefs(t, uploadBaselineRefs, []bool{true, false})
	if completeCalls != 2 {
		t.Fatalf("complete calls = %d, want 2", completeCalls)
	}
}

func setupDeltaFallbackRepo(t *testing.T) {
	t.Helper()
	repo := t.TempDir()
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore wd: %v", err)
		}
	})
	runGitForCLITest(t, "init")
	runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte(strings.Repeat("baseline\n", 4096)), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "baseline")
	if err := os.WriteFile(filepath.Join(repo, "local-change.txt"), []byte("small delta\n"), 0o644); err != nil {
		t.Fatalf("write local change: %v", err)
	}
}

func assertUploadBaselineRefs(t *testing.T, got, want []bool) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("upload baseline refs = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("upload baseline refs = %v, want %v", got, want)
		}
	}
}
