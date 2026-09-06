package main

import (
	"strings"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func selectRemoteBundleUpload(createResp preflightapi.CreateRunResponse, full, delta remoteBundleUpload) (remoteBundleUpload, api.UploadBundleOptions) {
	upload := full
	if createResp.BundleBaselineReusable && delta.path != "" {
		if delta.bundle.Manifest.BaselineKey == full.bundle.Manifest.BaselineKey && delta.bundle.Size < full.bundle.Size {
			upload = delta
			upload.delta = true
		}
	}
	uploadOptions := api.UploadBundleOptions{
		Method:  createResp.UploadMethod,
		Headers: createResp.UploadHeaders,
	}
	if len(createResp.UploadHeaders) > 0 {
		uploadOptions.AllowCrossOrigin = true
		uploadOptions.TreatAlreadyUploadedAsSuccess = strings.TrimSpace(createResp.UploadWorkerID) == ""
	}
	return upload, uploadOptions
}
