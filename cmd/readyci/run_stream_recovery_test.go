package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestRunRecoversAfterEventStreamTransportError(t *testing.T) {
	cliTestWorkingDirectoryMu.Lock()
	defer cliTestWorkingDirectoryMu.Unlock()
	setupDeltaFallbackRepo(t)

	var serverURL string
	streamRequests := 0
	eventsPageRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs":
			writeCLITestJSON(w, http.StatusCreated, preflightapi.CreateRunResponse{
				RunID:     "run_stream",
				UploadURL: serverURL + "/upload",
				EventsURL: serverURL + "/events",
				Status:    preflightapi.StatusAwaitingUpload,
			})
		case r.Method == http.MethodPut && r.URL.Path == "/upload":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte("{}\n"))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs/run_stream/upload-complete":
			writeCLITestJSON(w, http.StatusOK, preflightapi.CompleteUploadResponse{RunID: "run_stream", Status: preflightapi.StatusQueued})
		case r.Method == http.MethodGet && r.URL.Path == "/events":
			streamRequests++
			event := preflightapi.RunEvent{ID: 1, Type: preflightapi.EventStdout, RunID: "run_stream", Data: "before reset\n"}
			data, _ := json.Marshal(event)
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Content-Length", "4096")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(append(append([]byte("data: "), data...), '\n', '\n'))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_stream/events":
			eventsPageRequests++
			if r.URL.Query().Get("format") != "json" {
				t.Errorf("events format = %q, want json", r.URL.Query().Get("format"))
			}
			if r.URL.Query().Get("cursor") != "1" {
				t.Errorf("events cursor = %q, want 1", r.URL.Query().Get("cursor"))
			}
			exitCode := 0
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunEventsResponse{
				RunID: "run_stream",
				Events: []preflightapi.RunEvent{
					{ID: 2, Type: preflightapi.EventCommandExited, RunID: "run_stream", ExitCode: &exitCode},
					{ID: 3, Type: preflightapi.EventRunFinished, RunID: "run_stream", Status: preflightapi.StatusSucceeded, ExitCode: &exitCode},
				},
				NextCursor: 3,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_stream":
			exitCode := 0
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunDetail{
				Summary: preflightapi.RunSummary{
					RunID:    "run_stream",
					Status:   preflightapi.StatusSucceeded,
					ExitCode: &exitCode,
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	result, err := executeRemoteRunInternal(remoteRunOptions{
		APIURL:      server.URL,
		ProjectID:   "proj_dev",
		Command:     "cat README.md",
		CacheMode:   "cache-only",
		NetworkMode: "none",
	}, remoteRunOutputCollect)
	if err != nil && !isSuccessfulExit(err) {
		t.Fatalf("run error = %v, want successful terminal exit", err)
	}
	if streamRequests != 1 || eventsPageRequests != 1 {
		t.Fatalf("requests = stream %d events %d, want 1/1", streamRequests, eventsPageRequests)
	}
	if result.Status != preflightapi.StatusSucceeded || result.ExitCode != 0 || len(result.Events) != 3 {
		t.Fatalf("result = %+v, want recovered successful run with 3 events", result)
	}
	if result.StreamRecovery == nil || result.StreamRecovery.StartCursor != 1 || result.StreamRecovery.LastCursor != 3 || result.StreamRecovery.ReplayedEvents != 2 || result.StreamRecovery.Polls != 1 {
		t.Fatalf("stream recovery = %+v, want cursor 1->3 with 2 replayed events", result.StreamRecovery)
	}
}

func TestRunRecoversAfterTransientEventStreamResponsesExhausted(t *testing.T) {
	cliTestWorkingDirectoryMu.Lock()
	defer cliTestWorkingDirectoryMu.Unlock()
	setupDeltaFallbackRepo(t)

	var serverURL string
	streamRequests := 0
	eventsPageRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs":
			writeCLITestJSON(w, http.StatusCreated, preflightapi.CreateRunResponse{
				RunID:     "run_stream",
				UploadURL: serverURL + "/upload",
				EventsURL: serverURL + "/events",
				Status:    preflightapi.StatusAwaitingUpload,
			})
		case r.Method == http.MethodPut && r.URL.Path == "/upload":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte("{}\n"))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs/run_stream/upload-complete":
			writeCLITestJSON(w, http.StatusOK, preflightapi.CompleteUploadResponse{RunID: "run_stream", Status: preflightapi.StatusQueued})
		case r.Method == http.MethodGet && r.URL.Path == "/events":
			streamRequests++
			w.Header().Set("Retry-After", "0")
			http.Error(w, "edge unavailable", http.StatusBadGateway)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_stream/events":
			eventsPageRequests++
			if r.URL.Query().Get("format") != "json" {
				t.Errorf("events format = %q, want json", r.URL.Query().Get("format"))
			}
			if r.URL.Query().Get("cursor") != "" {
				t.Errorf("events cursor = %q, want empty initial cursor", r.URL.Query().Get("cursor"))
			}
			exitCode := 0
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunEventsResponse{
				RunID: "run_stream",
				Events: []preflightapi.RunEvent{
					{ID: 1, Type: preflightapi.EventCommandExited, RunID: "run_stream", ExitCode: &exitCode},
					{ID: 2, Type: preflightapi.EventRunFinished, RunID: "run_stream", Status: preflightapi.StatusSucceeded, ExitCode: &exitCode},
				},
				NextCursor: 2,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_stream":
			exitCode := 0
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunDetail{
				Summary: preflightapi.RunSummary{
					RunID:    "run_stream",
					Status:   preflightapi.StatusSucceeded,
					ExitCode: &exitCode,
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	result, err := executeRemoteRunInternal(remoteRunOptions{
		APIURL:      server.URL,
		ProjectID:   "proj_dev",
		Command:     "cat README.md",
		CacheMode:   "cache-only",
		NetworkMode: "none",
	}, remoteRunOutputCollect)
	if err != nil && !isSuccessfulExit(err) {
		t.Fatalf("run error = %v, want successful terminal exit", err)
	}
	if streamRequests != 5 || eventsPageRequests != 1 {
		t.Fatalf("requests = stream %d events %d, want 5/1", streamRequests, eventsPageRequests)
	}
	if result.Status != preflightapi.StatusSucceeded || result.ExitCode != 0 || len(result.Events) != 2 {
		t.Fatalf("result = %+v, want recovered successful run with 2 events", result)
	}
	if result.StreamRecovery == nil || result.StreamRecovery.StartCursor != 0 || result.StreamRecovery.LastCursor != 2 || result.StreamRecovery.ReplayedEvents != 2 || result.StreamRecovery.Polls != 1 {
		t.Fatalf("stream recovery = %+v, want cursor 0->2 with 2 replayed events", result.StreamRecovery)
	}
}

func TestRecoverRunEventStreamAppliesHostedDiagnosisForReplayedTerminalEvent(t *testing.T) {
	exitCode := 1
	var detailRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_stream/events":
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunEventsResponse{
				RunID: "run_stream",
				Events: []preflightapi.RunEvent{
					{ID: 2, Type: preflightapi.EventRunFailed, RunID: "run_stream", Message: "npm ci failed", Status: preflightapi.StatusFailed, ExitCode: &exitCode},
				},
				NextCursor: 2,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_stream":
			detailRequests++
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunDetail{
				Summary: preflightapi.RunSummary{
					RunID:          "run_stream",
					Status:         preflightapi.StatusFailed,
					ExitCode:       &exitCode,
					Classification: preflightapi.ClassificationDependencyDownloadFailed,
					Retryable:      true,
					AgentAction:    preflightapi.AgentActionRetry,
					FailureSummary: "Dependency download failed.",
					PrimaryFile:    "package-lock.json",
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := &remoteRunResult{RunID: "run_stream"}
	observer := remoteRunEventObserver{
		result:     result,
		outputMode: remoteRunOutputCollect,
		cursor:     1,
	}

	err := recoverRunEventStream(ctx, api.NewClient(server.URL), &observer, errors.New("stream reset"))
	var terminal terminalRunError
	if !errors.As(err, &terminal) || terminal.eventType != preflightapi.EventRunFailed || terminal.exitCode != exitCode {
		t.Fatalf("recoverRunEventStream error = %v, want terminal failed exit %d", err, exitCode)
	}
	if detailRequests != 1 {
		t.Fatalf("run detail requests = %d, want 1", detailRequests)
	}
	if result.Classification != preflightapi.ClassificationDependencyDownloadFailed || !result.Retryable || result.AgentAction != preflightapi.AgentActionRetry || result.FailureSummary != "Dependency download failed." || result.PrimaryFile != "package-lock.json" {
		t.Fatalf("result diagnosis = classification %q retryable %v action %q summary %q primary %q, want hosted diagnosis", result.Classification, result.Retryable, result.AgentAction, result.FailureSummary, result.PrimaryFile)
	}
	if result.StreamRecovery == nil || result.StreamRecovery.LastCursor != 2 || result.StreamRecovery.ReplayedEvents != 1 || result.StreamRecovery.Polls != 1 {
		t.Fatalf("stream recovery = %+v, want replayed terminal cursor 2", result.StreamRecovery)
	}
}

func TestRecoverRunEventStreamRetriesTransientPollingErrors(t *testing.T) {
	exitCode := 0
	var eventPageRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_stream/events" {
			eventPageRequests++
			if eventPageRequests == 1 {
				w.Header().Set("Retry-After", "0")
				http.Error(w, "edge unavailable", http.StatusBadGateway)
				return
			}
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunEventsResponse{
				RunID: "run_stream",
				Events: []preflightapi.RunEvent{
					{ID: 2, Type: preflightapi.EventRunFinished, RunID: "run_stream", Status: preflightapi.StatusSucceeded, ExitCode: &exitCode},
				},
				NextCursor: 2,
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := &remoteRunResult{RunID: "run_stream"}
	observer := remoteRunEventObserver{
		result:     result,
		outputMode: remoteRunOutputCollect,
		cursor:     1,
	}

	err := recoverRunEventStream(ctx, api.NewClient(server.URL), &observer, errors.New("stream reset"))
	var terminal terminalRunError
	if !errors.As(err, &terminal) || terminal.eventType != preflightapi.EventRunFinished || terminal.exitCode != 0 {
		t.Fatalf("recoverRunEventStream error = %v, want terminal success", err)
	}
	if eventPageRequests != 2 {
		t.Fatalf("event page requests = %d, want retry then success", eventPageRequests)
	}
	if result.StreamRecovery == nil || result.StreamRecovery.LastCursor != 2 || result.StreamRecovery.ReplayedEvents != 1 || result.StreamRecovery.Polls != 1 {
		t.Fatalf("stream recovery = %+v, want replayed terminal after retry", result.StreamRecovery)
	}
}

func TestRecoverRunEventStreamReturnsPollingErrors(t *testing.T) {
	for name, tt := range map[string]struct {
		handler http.HandlerFunc
		want    string
	}{
		"events page": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_stream/events" {
					http.Error(w, "events unavailable", http.StatusNotFound)
					return
				}
				http.NotFound(w, r)
			},
			want: "list events after stream interruption",
		},
		"run detail": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_stream/events":
					writeCLITestJSON(w, http.StatusOK, preflightapi.RunEventsResponse{RunID: "run_stream"})
				case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_stream":
					http.Error(w, "run unavailable", http.StatusNotFound)
				default:
					http.NotFound(w, r)
				}
			},
			want: "get run status after stream interruption",
		},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(tt.handler)
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			client := api.NewClient(server.URL)
			observer := remoteRunEventObserver{
				result:     &remoteRunResult{RunID: "run_stream"},
				outputMode: remoteRunOutputCollect,
				cursor:     1,
			}

			err := recoverRunEventStream(ctx, client, &observer, errors.New("stream reset"))
			if err == nil {
				t.Fatal("recoverRunEventStream returned nil, want polling error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("recoverRunEventStream error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestObserveStreamMessageHandlesSplitMarkers(t *testing.T) {
	first := preflightapi.FailedWorkspaceArtifactRetainedMessage[:20]
	second := preflightapi.FailedWorkspaceArtifactRetainedMessage[20:]
	buffer, seen := observeStreamMessage("", []byte(first), preflightapi.FailedWorkspaceArtifactRetainedMessage, false)
	if seen {
		t.Fatal("partial marker matched")
	}
	_, seen = observeStreamMessage(buffer, []byte(second), preflightapi.FailedWorkspaceArtifactRetainedMessage, false)
	if !seen {
		t.Fatal("split marker was not detected")
	}
}
