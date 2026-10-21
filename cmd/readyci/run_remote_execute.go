package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/internal/syncbundlebuild"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func printUntrackedUploadWarning(bundle syncbundlebuild.BuildResult) {
	if len(bundle.UntrackedFiles) == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "readyci: warning: %s\n", formatUntrackedUploadWarning(bundle.UntrackedFiles))
}

func executeRemoteRunInternal(options remoteRunOptions, outputMode remoteRunOutputMode) (result remoteRunResult, returnedErr error) {
	if strings.TrimSpace(options.Command) == "" && len(options.Steps) == 0 {
		return remoteRunResult{}, errors.New("empty command")
	}
	changeMode, err := syncbundle.NormalizeChangeMode(options.ChangeMode)
	if err != nil {
		return remoteRunResult{}, cliConfigError(err.Error(), err)
	}
	command, err := remoteCommandForOptions(options)
	if err != nil {
		return remoteRunResult{}, err
	}
	projectID, err := executionProjectID(options.APIURL, options.ProjectID)
	if err != nil {
		return remoteRunResult{}, err
	}
	idempotencyKey, err := normalizeIdempotencyKey(options.IdempotencyKey)
	if err != nil {
		return remoteRunResult{}, err
	}
	if idempotencyKey == "" {
		idempotencyKey, err = newIdempotencyKey()
		if err != nil {
			return remoteRunResult{}, err
		}
	}

	ctx, interruption, stop := interruptContext(context.Background())
	defer stop()
	client := api.NewClient(options.APIURL, api.WithToken(options.APIToken))
	var createRequest *preflightapi.CreateRunRequest
	defer func() {
		received := interruption.Signal()
		if received == nil {
			return
		}
		var interrupted cliInterruptedError
		result, interrupted = reconcileAndCancelInterruptedRun(options.APIURL, client, createRequest, result, received, returnedErr)
		returnedErr = interrupted
	}()

	bundleOptions := syncbundlebuild.BuildOptions{
		ChangeMode:      changeMode,
		ExcludePatterns: append([]string(nil), options.SyncExclude...), ReviewedFixtures: append([]syncbundle.ReviewedFixture(nil), options.SyncReviewedFixtures...),
		IncludeUntracked: options.IncludeUntracked,
	}
	bundlePath, bundle, deltaBundlePath, deltaBundle, err := syncbundlebuild.BuildFullAndDeltaToTempWithOptions(ctx, ".", bundleOptions)
	if err != nil {
		return remoteRunResult{}, err
	}
	defer os.Remove(bundlePath)
	if deltaBundlePath != "" {
		defer os.Remove(deltaBundlePath)
	}
	if options.Progress != nil {
		options.Progress.Emit(agentProgressEvent{Kind: agentProgressWorkspacePackaged, SizeBytes: bundle.Size})
	}
	if outputMode == remoteRunOutputHuman {
		printUntrackedUploadWarning(bundle)
	}

	request := buildRemoteRunRequest(options, bundle, command, projectID, idempotencyKey)
	createRequest = &request
	createResp, err := client.CreateRun(ctx, request)
	if err != nil {
		return remoteRunResult{}, err
	}
	result = remoteRunResult{
		RunID:          createResp.RunID,
		RunGroupID:     createResp.RunGroupID,
		EventsURL:      createResp.EventsURL,
		IdempotencyKey: idempotencyKey,
		Status:         createResp.Status,
		NetworkMode:    request.NetworkMode,
		ExitCode:       1,
	}
	if options.Progress != nil {
		options.Progress.Emit(agentProgressEvent{
			Kind:    agentProgressRunAccepted,
			RunName: displayRunName(options),
			RunID:   createResp.RunID,
			Status:  createResp.Status,
		})
	}
	if options.Open {
		openRunURL(options.DashboardURL, createResp.RunID)
	}

	if createResp.Status == preflightapi.StatusAwaitingUpload {
		upload, uploadOptions := selectRemoteBundleUpload(createResp, remoteBundleUpload{path: bundlePath, bundle: bundle}, remoteBundleUpload{path: deltaBundlePath, bundle: deltaBundle})
		if options.Progress != nil {
			options.Progress.Emit(agentProgressEvent{
				Kind:      agentProgressUploadStarted,
				RunName:   displayRunName(options),
				RunID:     createResp.RunID,
				SizeBytes: upload.bundle.Size,
			})
		}
		completeResp, err := uploadAndCompleteRunBundle(ctx, client, createResp, upload, remoteBundleUpload{path: bundlePath, bundle: bundle}, uploadOptions)
		if err != nil {
			return result, err
		}
		result.Status = completeResp.Status
		if options.Progress != nil {
			options.Progress.Emit(agentProgressEvent{
				Kind:    agentProgressUploadCompleted,
				RunName: displayRunName(options),
				RunID:   createResp.RunID,
			})
		}
	}
	if options.Detach {
		result.Detached = true
		result.ExitCode = 0
		return result, nil
	}

	return observeRemoteRun(ctx, client, options, outputMode, result)
}

func reconcileAndCancelInterruptedRun(
	apiURL string,
	client *api.Client,
	request *preflightapi.CreateRunRequest,
	result remoteRunResult,
	received os.Signal,
	priorErr error,
) (remoteRunResult, cliInterruptedError) {
	interrupted := cliInterruptedError{Signal: received, ResourceID: result.RunID, APIURL: apiURL}
	if result.RunID == "" && client != nil && request != nil {
		reconcileCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		reconciled, err := client.CreateRun(reconcileCtx, *request)
		cancel()
		if err != nil {
			interrupted.CancellationError = errors.Join(priorErr, fmt.Errorf("reconcile interrupted run creation: %w", err))
			return result, interrupted
		}
		result.RunID = reconciled.RunID
		result.RunGroupID = reconciled.RunGroupID
		result.EventsURL = reconciled.EventsURL
		result.IdempotencyKey = request.IdempotencyKey
		result.Status = reconciled.Status
		result.NetworkMode = request.NetworkMode
		interrupted.ResourceID = reconciled.RunID
	}
	if result.RunID == "" {
		return result, interrupted
	}
	cancelCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	response, err := client.CancelRun(cancelCtx, result.RunID, preflightapi.CancelRunRequest{Reason: "client_interrupt"})
	cancel()
	acknowledged := err == nil
	interrupted.CancellationAcknowledged = &acknowledged
	interrupted.CancellationError = err
	if err == nil {
		result.Status = response.Status
	}
	return result, interrupted
}

func applyRemoteRunDetailDiagnosis(ctx context.Context, client *api.Client, result *remoteRunResult) {
	if result == nil || strings.TrimSpace(result.RunID) == "" {
		return
	}
	detail, err := client.GetRun(ctx, result.RunID)
	if err != nil {
		return
	}
	result.applyRunSummaryDiagnosis(detail.Summary)
}

func createRunRequestRegion(region string) string {
	region = strings.TrimSpace(region)
	if region == "" {
		return ""
	}
	return preflightapi.NormalizeRegion(region)
}
