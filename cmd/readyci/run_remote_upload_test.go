package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/internal/syncbundlebuild"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestUploadRunBundleRetriesWorkerAdmission(t *testing.T) {
	for _, status := range []int{http.StatusConflict, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			const payload = "source bundle"
			path := filepath.Join(t.TempDir(), "bundle.tar.gz")
			if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
				t.Fatal(err)
			}
			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil || string(body) != payload {
					t.Errorf("upload body=%q err=%v", body, err)
				}
				if attempts.Add(1) == 1 {
					w.Header().Set("Retry-After", "0")
					http.Error(w, "worker upload admission busy", status)
					return
				}
				writeCLITestJSON(w, http.StatusAccepted, preflightapi.WorkerBundleUploadResponse{Proof: preflightapi.WorkerBundleUploadProof{Signature: "test-proof"}})
			}))
			defer server.Close()
			proof, err := uploadRunBundle(context.Background(), api.NewClient(server.URL), server.URL, remoteBundleUpload{path: path, bundle: syncbundlebuild.BuildResult{Size: int64(len(payload))}}, api.UploadBundleOptions{}, true)
			if err != nil || proof.Signature != "test-proof" || attempts.Load() != 2 {
				t.Fatalf("upload proof=%+v attempts=%d err=%v", proof, attempts.Load(), err)
			}
		})
	}
}

func TestUploadRunBundleDoesNotRetryOtherRejections(t *testing.T) {
	for _, test := range []struct {
		name       string
		worker     bool
		method     string
		retryAfter string
		status     int
	}{
		{"object store", false, "", "1", http.StatusServiceUnavailable},
		{"baseline conflict", true, "", "", http.StatusConflict},
		{"malformed retry", true, "", "invalid", http.StatusConflict},
		{"unsupported method", true, http.MethodPost, "1", http.StatusServiceUnavailable},
		{"invalid archive", true, "", "1", http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bundle.tar.gz")
			if err := os.WriteFile(path, []byte("bundle"), 0o600); err != nil {
				t.Fatal(err)
			}
			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Retry-After", test.retryAfter)
				http.Error(w, "rejected", test.status)
			}))
			defer server.Close()
			_, err := uploadRunBundle(context.Background(), api.NewClient(server.URL), server.URL, remoteBundleUpload{path: path, bundle: syncbundlebuild.BuildResult{Size: 6}}, api.UploadBundleOptions{Method: test.method}, test.worker)
			var responseErr api.ResponseError
			if !errors.As(err, &responseErr) || responseErr.StatusCode != test.status || attempts.Load() != 1 {
				t.Fatalf("attempts=%d err=%v", attempts.Load(), err)
			}
			if test.retryAfter == "1" && (!responseErr.HasRetryAfter || responseErr.RetryAfter != time.Second) {
				t.Fatalf("retry metadata lost: %+v", responseErr)
			}
		})
	}
}

func TestUploadRunBundleAdmissionRetryHonorsCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bundle.tar.gz")
	if err := os.WriteFile(path, []byte("bundle"), 0o600); err != nil {
		t.Fatal(err)
	}
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Retry-After", "60")
		http.Error(w, "capacity exhausted", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := uploadRunBundle(ctx, api.NewClient(server.URL), server.URL, remoteBundleUpload{path: path, bundle: syncbundlebuild.BuildResult{Size: 6}}, api.UploadBundleOptions{}, true)
	if !errors.Is(err, context.DeadlineExceeded) || attempts.Load() != 1 {
		t.Fatalf("attempts=%d err=%v", attempts.Load(), err)
	}
}
