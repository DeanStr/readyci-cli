package main

import (
	"context"
	"errors"

	"github.com/DeanStr/readyci-cli/internal/api"
)

func observeRemoteRun(ctx context.Context, client *api.Client, options remoteRunOptions, outputMode remoteRunOutputMode, result remoteRunResult) (remoteRunResult, error) {
	observer := remoteRunEventObserver{
		result:     &result,
		outputMode: outputMode,
		keepFailed: options.KeepFailed,
		progress:   options.Progress,
		runName:    displayRunName(options),
	}
	streamErr := client.StreamEvents(ctx, result.EventsURL, observer.handle)
	if streamErr != nil {
		var terminalErr terminalRunError
		if errors.As(streamErr, &terminalErr) {
			applyRemoteRunDetailDiagnosis(ctx, client, &result)
			return result, terminalErr
		}
		var exitErr exitCodeError
		if errors.As(streamErr, &exitErr) {
			applyRemoteRunDetailDiagnosis(ctx, client, &result)
			return result, exitErr
		}
		var eventStreamErr api.EventStreamError
		if errors.As(streamErr, &eventStreamErr) {
			if err := recoverRunEventStream(ctx, client, &observer, streamErr); err != nil {
				return result, err
			}
			return result, runStreamEndedError{runID: result.RunID, status: result.Status}
		}
		return result, streamErr
	}

	return result, runStreamEndedError{runID: result.RunID, status: result.Status}
}
