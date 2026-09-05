package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

const (
	runEventStreamRecoveryPageLimit          = 128
	runEventStreamRecoveryInterval           = 2 * time.Second
	runEventStreamRecoveryMaxRequestAttempts = 5
	runEventStreamRecoveryMaxRetryDelay      = 60 * time.Second
)

type remoteRunEventObserver struct {
	result             *remoteRunResult
	outputMode         remoteRunOutputMode
	keepFailed         bool
	stderrMarkerBuffer string
	cursor             int64
	seen               int64
	progress           agentProgressSink
	runName            string
}

func (o *remoteRunEventObserver) handle(event preflightapi.RunEvent) error {
	o.advanceCursor(event)
	if o.progress != nil {
		event.RunID = defaultString(event.RunID, o.result.RunID)
		o.progress.Emit(agentProgressEvent{
			Kind:      agentProgressLifecycle,
			RunName:   o.runName,
			RunID:     event.RunID,
			EventType: event.Type,
			Status:    event.Status,
			Step:      event.Step,
			ExitCode:  event.ExitCode,
		})
	}
	if o.outputMode == remoteRunOutputCollect {
		o.result.Events = append(o.result.Events, event)
	}
	switch event.Type {
	case preflightapi.EventStdout:
		data, err := event.DataBytes()
		if err != nil {
			return err
		}
		o.observeAnalysisStreamEvent(event, data)
		if o.progress != nil {
			o.progress.Log(event, data)
		}
		if o.outputMode == remoteRunOutputHuman {
			if _, err := os.Stdout.Write(data); err != nil {
				return err
			}
		}
	case preflightapi.EventStderr:
		data, err := event.DataBytes()
		if err != nil {
			return err
		}
		o.observeAnalysisStreamEvent(event, data)
		if o.progress != nil {
			o.progress.Log(event, data)
		}
		if o.outputMode == remoteRunOutputHuman {
			if _, err := os.Stderr.Write(data); err != nil {
				return err
			}
		}
		if o.keepFailed {
			o.stderrMarkerBuffer, o.result.FailedWorkspaceArtifactRetained = observeStreamMessage(o.stderrMarkerBuffer, data, preflightapi.FailedWorkspaceArtifactRetainedMessage, o.result.FailedWorkspaceArtifactRetained)
			o.stderrMarkerBuffer, o.result.FailedWorkspaceArtifactSkipped = observeStreamMessage(o.stderrMarkerBuffer, data, preflightapi.FailedWorkspaceArtifactSkippedMessage, o.result.FailedWorkspaceArtifactSkipped)
		}
	case preflightapi.EventLogTruncated:
		o.result.LogTruncated = true
		o.observeAnalysisEvent(event)
		if o.outputMode == remoteRunOutputHuman {
			if err := writeLogTruncationNotice(event); err != nil {
				return err
			}
		}
	case preflightapi.EventCommandExited:
		o.result.CommandExited = true
		if event.ExitCode != nil {
			o.result.CommandExitCode = *event.ExitCode
		}
		o.observeAnalysisEvent(event)
	case preflightapi.EventArtifactUploaded:
		o.result.ArtifactUploaded = true
	case preflightapi.EventRunFinished, preflightapi.EventRunFailed, preflightapi.EventRunTimedOut, preflightapi.EventRunCancelled:
		o.result.TerminalEventType = event.Type
		o.result.Status = defaultString(event.Status, runStatusForTerminalEvent(event.Type))
		o.result.FailureReason = strings.TrimSpace(event.Message)
		if event.ExitCode != nil {
			validExitCode, err := validateExitCode(*event.ExitCode)
			if err != nil {
				return err
			}
			o.result.ExitCode = validExitCode
		} else {
			o.result.ExitCode = defaultExitCodeForTerminalRunStatus(o.result.Status, o.result)
		}
		o.observeAnalysisEvent(event)
		return terminalRunError{eventType: event.Type, exitCode: o.result.ExitCode}
	}
	return nil
}

func (o *remoteRunEventObserver) observeAnalysisStreamEvent(event preflightapi.RunEvent, data []byte) {
	text := string(data)
	if event.Type != preflightapi.EventStderr && !runFailureAnalysisTextLooksUseful(text) {
		return
	}
	next := event
	next.DataBase64 = ""
	next.Data = trimRunFailureAnalysisData(text)
	o.observeAnalysisEvent(next)
}

func (o *remoteRunEventObserver) observeAnalysisEvent(event preflightapi.RunEvent) {
	if o == nil || o.result == nil {
		return
	}
	if len(event.Data) > runFailureAnalysisDataLimit {
		event.Data = trimRunFailureAnalysisData(event.Data)
	}
	if event.DataBase64 != "" {
		if data, err := event.DataBytes(); err == nil {
			event.DataBase64 = ""
			event.Data = trimRunFailureAnalysisData(string(data))
		}
	}
	o.result.AnalysisEvents = append(o.result.AnalysisEvents, event)
	if len(o.result.AnalysisEvents) > runFailureAnalysisEventLimit {
		copy(o.result.AnalysisEvents, o.result.AnalysisEvents[len(o.result.AnalysisEvents)-runFailureAnalysisEventLimit:])
		o.result.AnalysisEvents = o.result.AnalysisEvents[:runFailureAnalysisEventLimit]
	}
}

func (o *remoteRunEventObserver) advanceCursor(event preflightapi.RunEvent) {
	o.seen++
	if event.ID > o.cursor {
		o.cursor = event.ID
		return
	}
	if event.ID == 0 && o.seen > o.cursor {
		o.cursor = o.seen
	}
}

func recoverRunEventStream(ctx context.Context, client *api.Client, observer *remoteRunEventObserver, streamErr error) error {
	if err := ctx.Err(); err != nil {
		return streamErr
	}
	recovery := &remoteRunStreamRecovery{
		Reason:      streamErr.Error(),
		StartCursor: observer.cursor,
		LastCursor:  observer.cursor,
		SeenEvents:  observer.seen,
	}
	observer.result.StreamRecovery = recovery
	if observer.outputMode == remoteRunOutputHuman {
		fmt.Fprintf(os.Stderr, "\nreadyci: event stream interrupted (%v); recovering with durable event polling from cursor %d (seen=%d)\n", streamErr, observer.cursor, observer.seen)
	}
	ticker := time.NewTicker(runEventStreamRecoveryInterval)
	defer ticker.Stop()
	for {
		for {
			beforeCursor := observer.cursor
			page, err := listRunEventsPageWithRecoveryRetry(ctx, client, observer.result.RunID, observer.cursor, runEventStreamRecoveryPageLimit)
			if err != nil {
				if err := ctx.Err(); err != nil {
					return streamErr
				}
				return fmt.Errorf("recover run event stream for %s: list events after stream interruption from cursor %d: %w", observer.result.RunID, observer.cursor, err)
			}
			recovery.Polls++
			for _, event := range page.Events {
				recovery.ReplayedEvents++
				if err := observer.handle(event); err != nil {
					recovery.LastCursor = observer.cursor
					recovery.SeenEvents = observer.seen
					var terminalErr terminalRunError
					var exitErr exitCodeError
					if errors.As(err, &terminalErr) || errors.As(err, &exitErr) {
						applyRemoteRunDetailDiagnosis(ctx, client, observer.result)
					}
					return err
				}
			}
			if page.NextCursor > observer.cursor {
				observer.cursor = page.NextCursor
			}
			recovery.LastCursor = observer.cursor
			recovery.SeenEvents = observer.seen
			if observer.outputMode == remoteRunOutputHuman && len(page.Events) > 0 {
				fmt.Fprintf(os.Stderr, "readyci: recovered %d durable events (cursor %d -> %d)\n", len(page.Events), beforeCursor, observer.cursor)
			}
			if !page.HasMore {
				break
			}
			if observer.cursor <= beforeCursor {
				return fmt.Errorf("recover run event stream for %s: event pagination did not advance after cursor %d", observer.result.RunID, beforeCursor)
			}
		}
		detail, err := getRunWithRecoveryRetry(ctx, client, observer.result.RunID)
		if err == nil {
			observer.result.applyRunSummaryDiagnosis(detail.Summary)
			terminal, err := observer.finishFromRunDetail(detail)
			if err != nil {
				return err
			}
			if terminal {
				return terminalRunError{eventType: observer.result.TerminalEventType, exitCode: observer.result.ExitCode}
			}
			observer.result.Status = detail.Summary.Status
		} else if ctx.Err() != nil {
			return streamErr
		} else {
			return fmt.Errorf("recover run event stream for %s: get run status after stream interruption at cursor %d: %w", observer.result.RunID, observer.cursor, err)
		}
		select {
		case <-ctx.Done():
			return streamErr
		case <-ticker.C:
		}
	}
}

func listRunEventsPageWithRecoveryRetry(ctx context.Context, client *api.Client, runID string, cursor int64, limit int) (preflightapi.RunEventsResponse, error) {
	var page preflightapi.RunEventsResponse
	err := retryRunEventStreamRecoveryRequest(ctx, func() error {
		var err error
		page, err = client.ListRunEventsPage(ctx, runID, cursor, limit)
		return err
	})
	return page, err
}

func getRunWithRecoveryRetry(ctx context.Context, client *api.Client, runID string) (preflightapi.RunDetail, error) {
	var detail preflightapi.RunDetail
	err := retryRunEventStreamRecoveryRequest(ctx, func() error {
		var err error
		detail, err = client.GetRun(ctx, runID)
		return err
	})
	return detail, err
}

func retryRunEventStreamRecoveryRequest(ctx context.Context, fn func() error) error {
	var err error
	for attempt := 0; attempt < runEventStreamRecoveryMaxRequestAttempts; attempt++ {
		err = fn()
		if err == nil {
			return nil
		}
		if attempt == runEventStreamRecoveryMaxRequestAttempts-1 || !api.RetryableRequestError(ctx, err) {
			return err
		}
		if sleepErr := sleepRunEventStreamRecoveryRetry(ctx, api.RequestRetryDelay(attempt, err, runEventStreamRecoveryMaxRetryDelay)); sleepErr != nil {
			return sleepErr
		}
	}
	return err
}

func sleepRunEventStreamRecoveryRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (o *remoteRunEventObserver) finishFromRunDetail(detail preflightapi.RunDetail) (bool, error) {
	o.result.applyRunSummaryDiagnosis(detail.Summary)
	status := strings.TrimSpace(detail.Summary.Status)
	if !preflightapi.IsTerminalRunStatus(status) {
		return false, nil
	}
	o.result.Status = status
	o.result.TerminalEventType = terminalEventTypeForRunStatus(status)
	if detail.Summary.ExitCode != nil {
		validExitCode, err := validateExitCode(*detail.Summary.ExitCode)
		if err != nil {
			return true, err
		}
		o.result.ExitCode = validExitCode
	} else {
		o.result.ExitCode = defaultExitCodeForTerminalRunStatus(status, o.result)
	}
	return true, nil
}

func (r *remoteRunResult) applyRunSummaryDiagnosis(summary preflightapi.RunSummary) {
	if r == nil {
		return
	}
	r.Classification = strings.TrimSpace(summary.Classification)
	r.Retryable = summary.Retryable
	r.AgentAction = strings.TrimSpace(summary.AgentAction)
	r.FailureSummary = strings.TrimSpace(summary.FailureSummary)
	r.PrimaryFile = strings.TrimSpace(summary.PrimaryFile)
}

func terminalEventTypeForRunStatus(status string) string {
	switch status {
	case preflightapi.StatusSucceeded:
		return preflightapi.EventRunFinished
	case preflightapi.StatusTimedOut:
		return preflightapi.EventRunTimedOut
	case preflightapi.StatusCancelled:
		return preflightapi.EventRunCancelled
	default:
		return preflightapi.EventRunFailed
	}
}

func runStatusForTerminalEvent(eventType string) string {
	switch eventType {
	case preflightapi.EventRunFinished:
		return preflightapi.StatusSucceeded
	case preflightapi.EventRunTimedOut:
		return preflightapi.StatusTimedOut
	case preflightapi.EventRunCancelled:
		return preflightapi.StatusCancelled
	case preflightapi.EventRunFailed:
		return preflightapi.StatusFailed
	default:
		return ""
	}
}

func defaultExitCodeForTerminalRunStatus(status string, result *remoteRunResult) int {
	if result != nil && result.CommandExited {
		if code, err := validateExitCode(result.CommandExitCode); err == nil {
			if code != 0 {
				return code
			}
		}
	}
	switch status {
	case preflightapi.StatusSucceeded:
		return 0
	case preflightapi.StatusTimedOut:
		return 124
	case preflightapi.StatusCancelled:
		return 130
	default:
		return 1
	}
}
