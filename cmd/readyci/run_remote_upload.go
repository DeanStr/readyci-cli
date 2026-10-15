package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/internal/syncbundlebuild"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

type remoteBundleUpload struct {
	path   string
	bundle syncbundlebuild.BuildResult
	delta  bool
}

func uploadAndCompleteRunBundle(ctx context.Context, client *api.Client, createResp preflightapi.CreateRunResponse, upload, full remoteBundleUpload, uploadOptions api.UploadBundleOptions) (preflightapi.CompleteUploadResponse, error) {
	requireWorkerProof := strings.TrimSpace(createResp.UploadWorkerID) != ""
	proof, err := uploadRunBundle(ctx, client, createResp.UploadURL, upload, uploadOptions, requireWorkerProof)
	if err != nil {
		if upload.delta && isStaleBundleBaselineError(err) {
			proof, err := uploadRunBundle(ctx, client, createResp.UploadURL, full, uploadOptions, requireWorkerProof)
			if err != nil {
				return preflightapi.CompleteUploadResponse{}, err
			}
			return completeRunUpload(ctx, client, createResp.RunID, requireWorkerProof, proof)
		}
		return preflightapi.CompleteUploadResponse{}, err
	}

	completeResp, err := completeRunUpload(ctx, client, createResp.RunID, requireWorkerProof, proof)
	if err != nil {
		if upload.delta && isStaleBundleBaselineError(err) {
			proof, err := uploadRunBundle(ctx, client, createResp.UploadURL, full, uploadOptions, requireWorkerProof)
			if err != nil {
				return preflightapi.CompleteUploadResponse{}, err
			}
			return completeRunUpload(ctx, client, createResp.RunID, requireWorkerProof, proof)
		}
		return preflightapi.CompleteUploadResponse{}, err
	}
	return completeResp, nil
}

func completeRunUpload(ctx context.Context, client *api.Client, runID string, requireWorkerProof bool, proof preflightapi.WorkerBundleUploadProof) (preflightapi.CompleteUploadResponse, error) {
	if !requireWorkerProof {
		return client.CompleteUpload(ctx, runID)
	}
	return client.CompleteUploadWithRequest(ctx, runID, preflightapi.CompleteUploadRequest{WorkerUploadProof: &proof})
}

func uploadRunBundle(ctx context.Context, client *api.Client, uploadURL string, upload remoteBundleUpload, uploadOptions api.UploadBundleOptions, requireWorkerProof bool) (preflightapi.WorkerBundleUploadProof, error) {
	bundleFile, err := os.Open(upload.path)
	if err != nil {
		return preflightapi.WorkerBundleUploadProof{}, err
	}
	defer bundleFile.Close()
	var uploadResp preflightapi.WorkerBundleUploadResponse
	if requireWorkerProof {
		uploadOptions.DecodeJSONResponse = &uploadResp
	}
	// Admission retries share one transfer deadline. Each attempt has its own
	// read offset, even if the transport is still closing a rejected request.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	for {
		err := client.UploadBundleWithOptions(ctx, uploadURL, io.NewSectionReader(bundleFile, 0, upload.bundle.Size), upload.bundle.Size, uploadOptions)
		if err == nil {
			break
		}
		var responseErr api.ResponseError
		method := strings.TrimSpace(uploadOptions.Method)
		if !requireWorkerProof || (method != "" && method != http.MethodPut) ||
			!errors.As(err, &responseErr) || !responseErr.HasRetryAfter ||
			(responseErr.StatusCode != http.StatusConflict && responseErr.StatusCode != http.StatusServiceUnavailable) {
			return preflightapi.WorkerBundleUploadProof{}, err
		}
		// The worker rejected admission before spooling. Wait without a busy
		// loop; baseline conflicts without Retry-After retain their fallback.
		delay := max(100*time.Millisecond, min(responseErr.RetryAfter, 5*time.Second))
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return preflightapi.WorkerBundleUploadProof{}, ctx.Err()
		case <-timer.C:
		}
	}
	if requireWorkerProof && strings.TrimSpace(uploadResp.Proof.Signature) == "" {
		return preflightapi.WorkerBundleUploadProof{}, fmt.Errorf("worker upload response did not include proof")
	}
	return uploadResp.Proof, nil
}

func isStaleBundleBaselineError(err error) bool {
	var responseErr api.ResponseError
	if errors.As(err, &responseErr) {
		switch responseErr.StatusCode {
		case http.StatusBadRequest, http.StatusConflict:
			body := strings.ToLower(responseErr.Body)
			return strings.Contains(body, "referenced") && strings.Contains(body, "baseline") && strings.Contains(body, "not available")
		default:
			return false
		}
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "referenced") && strings.Contains(message, "baseline") && strings.Contains(message, "not available")
}
