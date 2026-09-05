package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func TestClientSpecialRunEventAndArtifactMethods(t *testing.T) {
	var eventPageRequests int
	var streamed []preflightapi.RunEvent
	var archiveAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/v1/runs/run%201/upload-complete":
			writeAPIClientTestJSON(t, w, preflightapi.CompleteUploadResponse{RunID: "run 1", Status: preflightapi.StatusQueued})
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/v1/runs/run%201/events" && r.Header.Get("Accept") == "text/event-stream":
			exitCode := 0
			events := []preflightapi.RunEvent{
				{ID: 1, Type: preflightapi.EventStdout, Data: "hello\n"},
				{ID: 2, Type: preflightapi.EventRunFinished, Status: preflightapi.StatusSucceeded, ExitCode: &exitCode},
			}
			w.Header().Set("Content-Type", "text/event-stream")
			for _, event := range events {
				data, _ := json.Marshal(event)
				_, _ = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.ID, event.Type, data)
			}
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/v1/runs/run%201/events":
			eventPageRequests++
			if eventPageRequests == 1 {
				if got := r.URL.Query().Get("cursor"); got != "" {
					t.Fatalf("first cursor = %q, want empty", got)
				}
				writeAPIClientTestJSON(t, w, preflightapi.RunEventsResponse{
					RunID:      "run 1",
					Events:     []preflightapi.RunEvent{{ID: 1, Type: preflightapi.EventStdout, Data: "one"}},
					NextCursor: 1,
					HasMore:    true,
					Truncated:  true,
				})
				return
			}
			if got := r.URL.Query().Get("cursor"); got != "1" {
				t.Fatalf("second cursor = %q, want 1", got)
			}
			writeAPIClientTestJSON(t, w, preflightapi.RunEventsResponse{
				RunID:      "run 1",
				Events:     []preflightapi.RunEvent{{ID: 2, Type: preflightapi.EventStderr, Data: "two"}},
				NextCursor: 2,
			})
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/v1/runs/run%201/artifacts/archive":
			archiveAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/gzip")
			_, _ = w.Write([]byte("archive-bytes"))
		default:
			t.Fatalf("unexpected request: %s %s accept=%s", r.Method, r.URL.String(), r.Header.Get("Accept"))
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, WithToken("token_test"))
	complete, err := client.CompleteUpload(context.Background(), "run 1")
	if err != nil {
		t.Fatalf("complete upload: %v", err)
	}
	if complete.Status != preflightapi.StatusQueued {
		t.Fatalf("complete upload status = %q, want queued", complete.Status)
	}
	listed, err := client.ListRunEvents(context.Background(), "run 1")
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	if len(listed.Events) != 2 || !listed.Truncated || listed.NextCursor != 2 {
		t.Fatalf("listed events = %+v, want combined paginated response", listed)
	}
	err = client.StreamRunEvents(context.Background(), "run 1", func(event preflightapi.RunEvent) error {
		streamed = append(streamed, event)
		return nil
	})
	if err != nil {
		t.Fatalf("stream run events: %v", err)
	}
	if len(streamed) != 2 || streamed[1].Type != preflightapi.EventRunFinished {
		t.Fatalf("streamed events = %+v, want stdout and terminal event", streamed)
	}
	var archive strings.Builder
	if err := client.DownloadArtifactArchive(context.Background(), "run 1", &archive); err != nil {
		t.Fatalf("download artifact archive: %v", err)
	}
	if archive.String() != "archive-bytes" || archiveAuth != "Bearer token_test" {
		t.Fatalf("archive=%q auth=%q, want bytes with bearer auth", archive.String(), archiveAuth)
	}
}

func TestCompleteUploadRetriesRetryableResponse(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/v1/runs/run_retry/upload-complete" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		if attempts.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			http.Error(w, "edge unavailable", http.StatusBadGateway)
			return
		}
		writeAPIClientTestJSON(t, w, preflightapi.CompleteUploadResponse{RunID: "run_retry", Status: preflightapi.StatusQueued})
	}))
	defer server.Close()

	client := NewClient(server.URL, WithToken("token_test"))
	complete, err := client.CompleteUpload(context.Background(), "run_retry")
	if err != nil {
		t.Fatalf("complete upload retry: %v", err)
	}
	if complete.Status != preflightapi.StatusQueued {
		t.Fatalf("complete upload status = %q, want queued", complete.Status)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("complete upload attempts = %d, want 2", got)
	}
}

func TestStreamRunEventsRetriesTransientResponse(t *testing.T) {
	var attempts atomic.Int32
	exitCode := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/v1/runs/run_retry/events" || r.Header.Get("Accept") != "text/event-stream" {
			t.Fatalf("unexpected request: %s %s accept=%s", r.Method, r.URL.String(), r.Header.Get("Accept"))
		}
		if attempts.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			http.Error(w, "edge unavailable", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		event := preflightapi.RunEvent{ID: 1, Type: preflightapi.EventRunFinished, RunID: "run_retry", Status: preflightapi.StatusSucceeded, ExitCode: &exitCode}
		data, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("marshal event: %v", err)
		}
		_, _ = fmt.Fprintf(w, "id: 1\ndata: %s\n\n", data)
	}))
	defer server.Close()

	client := NewClient(server.URL, WithToken("token_test"))
	var streamed []preflightapi.RunEvent
	err := client.StreamRunEvents(context.Background(), "run_retry", func(event preflightapi.RunEvent) error {
		streamed = append(streamed, event)
		return nil
	})
	if err != nil {
		t.Fatalf("stream run events retry: %v", err)
	}
	if len(streamed) != 1 || streamed[0].Type != preflightapi.EventRunFinished || streamed[0].ExitCode == nil || *streamed[0].ExitCode != 0 {
		t.Fatalf("streamed events = %+v, want terminal success after retry", streamed)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("stream attempts = %d, want 2", got)
	}
}

func TestStreamRunEventsReturnsInterruptionAfterTransientResponsesExhausted(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/v1/runs/run_retry/events" || r.Header.Get("Accept") != "text/event-stream" {
			t.Fatalf("unexpected request: %s %s accept=%s", r.Method, r.URL.String(), r.Header.Get("Accept"))
		}
		attempts.Add(1)
		w.Header().Set("Retry-After", "0")
		http.Error(w, "edge unavailable", http.StatusBadGateway)
	}))
	defer server.Close()

	client := NewClient(server.URL, WithToken("token_test"))
	err := client.StreamRunEvents(context.Background(), "run_retry", func(event preflightapi.RunEvent) error {
		t.Fatalf("unexpected event: %+v", event)
		return nil
	})
	var streamErr EventStreamError
	if !errors.As(err, &streamErr) {
		t.Fatalf("stream run events error = %T %v, want EventStreamError", err, err)
	}
	if got := attempts.Load(); got != streamEventsTransientErrorMaxAttempts {
		t.Fatalf("stream attempts = %d, want %d", got, streamEventsTransientErrorMaxAttempts)
	}
}

func TestStreamRunEventsDoesNotRetryHandlerErrors(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/v1/runs/run_handler/events" || r.Header.Get("Accept") != "text/event-stream" {
			t.Fatalf("unexpected request: %s %s accept=%s", r.Method, r.URL.String(), r.Header.Get("Accept"))
		}
		attempts.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		event := preflightapi.RunEvent{ID: 1, Type: preflightapi.EventStdout, RunID: "run_handler", Data: "hello\n"}
		data, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("marshal event: %v", err)
		}
		_, _ = fmt.Fprintf(w, "id: 1\ndata: %s\n\n", data)
	}))
	defer server.Close()

	wantErr := ResponseError{Operation: "handler", Status: "502 Bad Gateway", StatusCode: http.StatusBadGateway, Body: "callback failed"}
	client := NewClient(server.URL, WithToken("token_test"))
	err := client.StreamRunEvents(context.Background(), "run_handler", func(event preflightapi.RunEvent) error {
		return wantErr
	})
	var responseErr ResponseError
	if !errors.As(err, &responseErr) || responseErr.Operation != wantErr.Operation || responseErr.StatusCode != wantErr.StatusCode {
		t.Fatalf("stream run events error = %T %v, want original handler ResponseError", err, err)
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("stream attempts = %d, want no retry after handler error", got)
	}
}

func TestResponseErrorPreservesStructuredCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"code":"worker_unavailable","error":"no worker"}`)
	}))
	defer server.Close()

	var response any
	err := NewClient(server.URL).doJSON(context.Background(), http.MethodGet, "/v1/runs", nil, &response)
	var responseErr ResponseError
	if !errors.As(err, &responseErr) || responseErr.Code != "worker_unavailable" {
		t.Fatalf("response error = %#v, want structured worker_unavailable code", err)
	}
}

func TestStreamEventsDoesNotRetryReadErrorAfterNonResumableEvent(t *testing.T) {
	var attempts atomic.Int32
	data, err := json.Marshal(preflightapi.RunEvent{ID: 7, Type: preflightapi.EventStdout, Data: "legacy\n"})
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	client := NewClient("https://api.readyci.test", WithToken("token_test"))
	client.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || req.URL.String() != "https://api.readyci.test/events" || req.Header.Get("Accept") != "text/event-stream" {
			t.Fatalf("unexpected request: %s %s accept=%s", req.Method, req.URL.String(), req.Header.Get("Accept"))
		}
		if got := req.Header.Get("Last-Event-ID"); got != "" {
			t.Fatalf("Last-Event-ID = %q, want no reconnect after non-resumable event", got)
		}
		attempts.Add(1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       &retryableReadErrorBody{data: append(append([]byte("data: "), data...), '\n', '\n')},
			Request:    req,
		}, nil
	})}

	var events []preflightapi.RunEvent
	err = client.StreamEvents(context.Background(), "https://api.readyci.test/events", func(event preflightapi.RunEvent) error {
		events = append(events, event)
		return nil
	})
	var streamErr EventStreamError
	if !errors.As(err, &streamErr) {
		t.Fatalf("stream error = %T %v, want EventStreamError", err, err)
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("stream attempts = %d, want no retry after non-resumable event", got)
	}
	if len(events) != 1 || events[0].ID != 7 {
		t.Fatalf("events = %+v, want delivered legacy event before recovery", events)
	}
}

func TestStreamEventsRetryDelayUsesShortStreamBackoff(t *testing.T) {
	err := ResponseError{Operation: "stream events", Status: "502 Bad Gateway", StatusCode: http.StatusBadGateway, Body: "edge unavailable"}
	for attempt, want := range []time.Duration{
		250 * time.Millisecond,
		500 * time.Millisecond,
		750 * time.Millisecond,
		time.Second,
		1250 * time.Millisecond,
	} {
		if got := streamEventsRetryDelay(attempt, err); got != want {
			t.Fatalf("stream retry delay attempt %d = %s, want %s", attempt, got, want)
		}
	}
	if got := streamEventsRetryDelay(8, err); got != streamEventsRetryMaxDelay {
		t.Fatalf("stream retry delay after cap = %s, want %s", got, streamEventsRetryMaxDelay)
	}

	withRetryAfter := ResponseError{
		Operation:     "stream events",
		Status:        "503 Service Unavailable",
		StatusCode:    http.StatusServiceUnavailable,
		Body:          "edge unavailable",
		RetryAfter:    time.Minute,
		HasRetryAfter: true,
	}
	if got := streamEventsRetryDelay(0, withRetryAfter); got != streamEventsRetryMaxDelay {
		t.Fatalf("stream retry delay with long Retry-After = %s, want cap %s", got, streamEventsRetryMaxDelay)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type retryableReadErrorBody struct {
	data   []byte
	offset int
}

func (b *retryableReadErrorBody) Read(p []byte) (int, error) {
	if b.offset < len(b.data) {
		n := copy(p, b.data[b.offset:])
		b.offset += n
		return n, nil
	}
	return 0, retryableStreamReadError{}
}

func (b *retryableReadErrorBody) Close() error {
	return nil
}

type retryableStreamReadError struct{}

var _ net.Error = retryableStreamReadError{}

func (retryableStreamReadError) Error() string {
	return "retryable stream read error"
}

func (retryableStreamReadError) Timeout() bool {
	return false
}

func (retryableStreamReadError) Temporary() bool {
	return true
}

func TestCompleteUploadDoesNotRetryRunLimitResponse(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/v1/runs/run_limited/upload-complete" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		attempts.Add(1)
		http.Error(w, "project queued run limit exceeded (1)", http.StatusTooManyRequests)
	}))
	defer server.Close()

	client := NewClient(server.URL, WithToken("token_test"))
	_, err := client.CompleteUpload(context.Background(), "run_limited")
	if err == nil {
		t.Fatal("complete upload succeeded, want queued limit error")
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("complete upload attempts = %d, want 1", got)
	}
}

func TestClientErrorFormatting(t *testing.T) {
	respErr := ResponseError{Operation: "GET run", Status: "404 Not Found", Body: " missing\n"}
	if got := respErr.Error(); got != "GET run failed: 404 Not Found: missing" {
		t.Fatalf("ResponseError = %q", got)
	}
	if got := (EventStreamError{}).Error(); got != "stream events interrupted" {
		t.Fatalf("empty EventStreamError = %q", got)
	}
	wrapped := fmt.Errorf("read tcp reset")
	streamErr := EventStreamError{Err: wrapped}
	if got := streamErr.Error(); got != "stream events interrupted: read tcp reset" {
		t.Fatalf("EventStreamError = %q", got)
	}
	if streamErr.Unwrap() != wrapped {
		t.Fatal("EventStreamError unwrap did not return wrapped error")
	}
}

func TestClientErrorFormattingRedactsCredentials(t *testing.T) {
	got := (ResponseError{
		Operation: "GET run",
		Status:    "500 Internal Server Error",
		Body:      "Bearer secret-token https://user:pass@example.test/path?token=query-secret&X-Amz-Credential=access-id&X-Amz-Signature=signed-grant&X-Amz-Security-Token=session-grant",
	}).Error()
	for _, secret := range []string{"secret-token", "user:pass", "query-secret", "access-id", "signed-grant", "session-grant"} {
		if strings.Contains(got, secret) {
			t.Fatalf("ResponseError leaked %q: %s", secret, got)
		}
	}
}

func writeAPIClientTestJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode json response: %v", err)
	}
}

func TestUploadBundleResolvesRelativeURLAndAuthorizesSameOrigin(t *testing.T) {
	var sawAuth atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bundle-upload/run_test" {
			t.Errorf("path = %q", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got == "Bearer token_test" {
			sawAuth.Store(true)
		}
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("{}"))
	}))
	defer server.Close()

	client := NewClient(server.URL, WithToken("token_test"))
	if err := client.UploadBundle(context.Background(), "/bundle-upload/run_test", strings.NewReader("bundle"), int64(len("bundle"))); err != nil {
		t.Fatalf("upload bundle: %v", err)
	}
	if !sawAuth.Load() {
		t.Fatal("same-origin relative upload did not include Authorization")
	}
}

func TestUploadBundlePreservesPathPrefixedAPIBase(t *testing.T) {
	var sawAuth atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/bundle-upload/run_test" {
			t.Errorf("path = %q", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got == "Bearer token_test" {
			sawAuth.Store(true)
		}
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("{}"))
	}))
	defer server.Close()

	client := NewClient(server.URL+"/api", WithToken("token_test"))
	if err := client.UploadBundle(context.Background(), "/bundle-upload/run_test", strings.NewReader("bundle"), int64(len("bundle"))); err != nil {
		t.Fatalf("upload bundle: %v", err)
	}
	if !sawAuth.Load() {
		t.Fatal("same-origin prefixed upload did not include Authorization")
	}
}

func TestUploadBundleRejectsCrossOriginBearerURL(t *testing.T) {
	var hit atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit.Store(true)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer other.Close()

	apiServer := httptest.NewServer(http.NotFoundHandler())
	defer apiServer.Close()

	client := NewClient(apiServer.URL, WithToken("token_test"))
	err := client.UploadBundle(context.Background(), other.URL+"/upload", strings.NewReader("bundle"), int64(len("bundle")))
	if err == nil || !strings.Contains(err.Error(), "cross-origin upload URL") {
		t.Fatalf("upload error = %v, want cross-origin refusal", err)
	}
	if hit.Load() {
		t.Fatal("cross-origin upload endpoint was contacted")
	}
}

func TestUploadBundleAllowsExplicitDirectCrossOriginURL(t *testing.T) {
	var sawUpload atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawUpload.Store(true)
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q, want empty", got)
		}
		if got := r.Header.Get("If-None-Match"); got != "*" {
			t.Errorf("If-None-Match = %q, want *", got)
		}
		if got := r.Header.Get("Content-Type"); got != syncbundle.BundleMediaType {
			t.Errorf("Content-Type = %q, want %q", got, syncbundle.BundleMediaType)
		}
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer other.Close()

	apiServer := httptest.NewServer(http.NotFoundHandler())
	defer apiServer.Close()

	client := NewClient(apiServer.URL, WithToken("token_test"))
	err := client.UploadBundleWithOptions(context.Background(), other.URL+"/upload", strings.NewReader("bundle"), int64(len("bundle")), UploadBundleOptions{
		Headers: map[string]string{
			"If-None-Match": "*",
			"Content-Type":  syncbundle.BundleMediaType,
		},
		AllowCrossOrigin: true,
	})
	if err != nil {
		t.Fatalf("upload bundle: %v", err)
	}
	if !sawUpload.Load() {
		t.Fatal("cross-origin upload endpoint was not contacted")
	}
}

func TestUploadBundleAllowsWorkerCrossOriginURLWithoutBearer(t *testing.T) {
	var sawUpload atomic.Bool
	workerUpload := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawUpload.Store(true)
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		if r.URL.Path != preflightapi.WorkerBundleUploadPathPrefix+"run_test" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q, want empty", got)
		}
		if got := r.Header.Get("Content-Type"); got != syncbundle.BundleMediaType {
			t.Errorf("Content-Type = %q, want %q", got, syncbundle.BundleMediaType)
		}
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer workerUpload.Close()

	apiServer := httptest.NewServer(http.NotFoundHandler())
	defer apiServer.Close()

	client := NewClient(apiServer.URL, WithToken("token_test"))
	target := workerUpload.URL + preflightapi.WorkerBundleUploadPathPrefix + "run_test?expires=1&signature=sig"
	err := client.UploadBundleWithOptions(context.Background(), target, strings.NewReader("bundle"), int64(len("bundle")), UploadBundleOptions{
		Headers: map[string]string{
			"Content-Type": syncbundle.BundleMediaType,
		},
		AllowCrossOrigin: true,
	})
	if err != nil {
		t.Fatalf("upload bundle: %v", err)
	}
	if !sawUpload.Load() {
		t.Fatal("worker upload endpoint was not contacted")
	}
}

func TestUploadBundleDecodeJSONResponseRejectsTrailingData(t *testing.T) {
	workerUpload := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"proof":{"signature":"sig"}} trailing`))
	}))
	defer workerUpload.Close()

	apiServer := httptest.NewServer(http.NotFoundHandler())
	defer apiServer.Close()

	var uploadResp preflightapi.WorkerBundleUploadResponse
	client := NewClient(apiServer.URL, WithToken("token_test"))
	err := client.UploadBundleWithOptions(context.Background(), workerUpload.URL+"/upload", strings.NewReader("bundle"), int64(len("bundle")), UploadBundleOptions{
		AllowCrossOrigin:   true,
		DecodeJSONResponse: &uploadResp,
	})
	if err == nil || !strings.Contains(err.Error(), "single JSON value") {
		t.Fatalf("upload decode err = %v, want single JSON value rejection", err)
	}
}
