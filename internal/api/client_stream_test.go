package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestUploadBundleCanTreatPreconditionFailureAsAlreadyUploaded(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPreconditionFailed)
	}))
	defer other.Close()

	apiServer := httptest.NewServer(http.NotFoundHandler())
	defer apiServer.Close()

	client := NewClient(apiServer.URL, WithToken("token_test"))
	err := client.UploadBundleWithOptions(context.Background(), other.URL+"/upload", strings.NewReader("bundle"), int64(len("bundle")), UploadBundleOptions{
		AllowCrossOrigin:              true,
		TreatAlreadyUploadedAsSuccess: true,
	})
	if err != nil {
		t.Fatalf("upload bundle: %v", err)
	}
}

func TestUploadBundleCanOmitAPIAuthenticationForSameOrigin(t *testing.T) {
	const token = "token_test"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("same-origin credential-free upload Authorization = %q, want empty", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(server.URL, WithToken(token))
	if err := client.UploadBundleWithOptions(
		context.Background(),
		server.URL+"/upload",
		strings.NewReader("bundle"),
		int64(len("bundle")),
		UploadBundleOptions{OmitAPIAuthentication: true},
	); err != nil {
		t.Fatalf("upload bundle: %v", err)
	}
}

func TestUploadBundleRetriesDirectConditionalConflict(t *testing.T) {
	var attempts atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := attempts.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		if string(body) != "bundle" {
			t.Errorf("body = %q, want bundle", body)
		}
		if attempt == 1 {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte("conditional request conflict"))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer other.Close()

	apiServer := httptest.NewServer(http.NotFoundHandler())
	defer apiServer.Close()

	client := NewClient(apiServer.URL, WithToken("token_test"))
	err := client.UploadBundleWithOptions(context.Background(), other.URL+"/upload", strings.NewReader("bundle"), int64(len("bundle")), UploadBundleOptions{
		AllowCrossOrigin:              true,
		TreatAlreadyUploadedAsSuccess: true,
	})
	if err != nil {
		t.Fatalf("upload bundle: %v", err)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
}

func TestStreamEventsRejectsCrossOriginBearerURL(t *testing.T) {
	var hit atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer other.Close()

	apiServer := httptest.NewServer(http.NotFoundHandler())
	defer apiServer.Close()

	client := NewClient(apiServer.URL, WithToken("token_test"))
	err := client.StreamEvents(context.Background(), other.URL+"/events", func(preflightapi.RunEvent) error {
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "cross-origin events URL") {
		t.Fatalf("stream error = %v, want cross-origin refusal", err)
	}
	if hit.Load() {
		t.Fatal("cross-origin events endpoint was contacted")
	}
}

func TestStreamEventsReconnectsWithLastEventID(t *testing.T) {
	var requests atomic.Int64
	var events []preflightapi.RunEvent
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		switch requests.Add(1) {
		case 1:
			if got := r.Header.Get("Last-Event-ID"); got != "" {
				t.Errorf("first Last-Event-ID = %q, want empty", got)
			}
			writeClientTestSSEEvent(t, w, 1, preflightapi.RunEvent{Type: preflightapi.EventStdout, Data: "hello\n"})
		case 2:
			if got := r.Header.Get("Last-Event-ID"); got != "1" {
				t.Errorf("second Last-Event-ID = %q, want 1", got)
			}
			writeClientTestSSEEvent(t, w, 2, preflightapi.RunEvent{Type: preflightapi.EventRunFinished, Status: preflightapi.StatusSucceeded})
		default:
			t.Errorf("unexpected stream request %d", requests.Load())
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, WithToken("token_test"))
	if err := client.StreamEvents(context.Background(), server.URL+"/events", func(event preflightapi.RunEvent) error {
		events = append(events, event)
		return nil
	}); err != nil {
		t.Fatalf("stream events: %v", err)
	}
	if requests.Load() != 2 {
		t.Fatalf("requests = %d, want 2", requests.Load())
	}
	if len(events) != 2 || events[0].ID != 1 || events[1].ID != 2 || events[1].Type != preflightapi.EventRunFinished {
		t.Fatalf("events = %+v, want stdout then terminal with SSE IDs", events)
	}
}

func TestStreamEventsRetriesTransientReconnectAfterResumabilityEstablished(t *testing.T) {
	var requests atomic.Int64
	var events []preflightapi.RunEvent
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch requests.Add(1) {
		case 1:
			if got := r.Header.Get("Last-Event-ID"); got != "" {
				t.Errorf("first Last-Event-ID = %q, want empty", got)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			writeClientTestSSEEvent(t, w, 1, preflightapi.RunEvent{Type: preflightapi.EventStdout, Data: "hello\n"})
		case 2:
			if got := r.Header.Get("Last-Event-ID"); got != "1" {
				t.Errorf("second Last-Event-ID = %q, want 1", got)
			}
			w.Header().Set("Retry-After", "0")
			http.Error(w, "edge unavailable", http.StatusBadGateway)
		case 3:
			if got := r.Header.Get("Last-Event-ID"); got != "1" {
				t.Errorf("third Last-Event-ID = %q, want 1", got)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			writeClientTestSSEEvent(t, w, 2, preflightapi.RunEvent{Type: preflightapi.EventRunFinished, Status: preflightapi.StatusSucceeded})
		default:
			t.Errorf("unexpected stream request %d", requests.Load())
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, WithToken("token_test"))
	if err := client.StreamEvents(context.Background(), server.URL+"/events", func(event preflightapi.RunEvent) error {
		events = append(events, event)
		return nil
	}); err != nil {
		t.Fatalf("stream events: %v", err)
	}
	if requests.Load() != 3 {
		t.Fatalf("requests = %d, want resumable stream retry after transient reconnect", requests.Load())
	}
	if len(events) != 2 || events[0].ID != 1 || events[1].ID != 2 || events[1].Type != preflightapi.EventRunFinished {
		t.Fatalf("events = %+v, want stdout then terminal without duplication", events)
	}
}

func TestStreamEventsDoesNotReconnectWithoutSSEID(t *testing.T) {
	var requests atomic.Int64
	var events []preflightapi.RunEvent
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		requests.Add(1)
		data, err := json.Marshal(preflightapi.RunEvent{ID: 7, Type: preflightapi.EventStdout, Data: "legacy\n"})
		if err != nil {
			t.Fatalf("marshal event: %v", err)
		}
		_, _ = w.Write(append(append([]byte("data: "), data...), '\n', '\n'))
	}))
	defer server.Close()

	client := NewClient(server.URL, WithToken("token_test"))
	if err := client.StreamEvents(context.Background(), server.URL+"/events", func(event preflightapi.RunEvent) error {
		events = append(events, event)
		return nil
	}); err != nil {
		t.Fatalf("stream events: %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want one legacy stream request", requests.Load())
	}
	if len(events) != 1 || events[0].ID != 7 {
		t.Fatalf("events = %+v, want single legacy event", events)
	}
}

func TestStreamEventsDoesNotApplyIDOnlyFrameToNextEvent(t *testing.T) {
	var requests atomic.Int64
	var events []preflightapi.RunEvent
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		requests.Add(1)
		_, _ = w.Write([]byte("id: 99\n\n"))
		data, err := json.Marshal(preflightapi.RunEvent{Type: preflightapi.EventStdout, Data: "legacy\n"})
		if err != nil {
			t.Fatalf("marshal event: %v", err)
		}
		_, _ = w.Write(append(append([]byte("data: "), data...), '\n', '\n'))
	}))
	defer server.Close()

	client := NewClient(server.URL, WithToken("token_test"))
	if err := client.StreamEvents(context.Background(), server.URL+"/events", func(event preflightapi.RunEvent) error {
		events = append(events, event)
		return nil
	}); err != nil {
		t.Fatalf("stream events: %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want one request without stale id reconnection", requests.Load())
	}
	if len(events) != 1 || events[0].ID != 0 {
		t.Fatalf("events = %+v, want data-only event without stale SSE id", events)
	}
}

func TestStreamEventsReportsMalformedSSEAndHTTPErrorBody(t *testing.T) {
	t.Run("malformed event", func(t *testing.T) {
		var handled atomic.Bool
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("id: 1\ndata: {not-json}\n\n"))
		}))
		defer server.Close()

		client := NewClient(server.URL, WithToken("token_test"))
		err := client.StreamEvents(context.Background(), server.URL+"/events", func(preflightapi.RunEvent) error {
			handled.Store(true)
			return nil
		})
		if err == nil || !strings.Contains(err.Error(), "invalid character") {
			t.Fatalf("stream malformed error = %v, want JSON decode error", err)
		}
		if handled.Load() {
			t.Fatal("handler was called for malformed event")
		}
	})

	t.Run("http error body", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "events unavailable", http.StatusServiceUnavailable)
		}))
		defer server.Close()

		client := NewClient(server.URL, WithToken("token_test"))
		err := client.StreamEvents(context.Background(), server.URL+"/events", func(preflightapi.RunEvent) error {
			t.Fatal("handler called for failed HTTP stream")
			return nil
		})
		if err == nil || !strings.Contains(err.Error(), "503 Service Unavailable") || !strings.Contains(err.Error(), "events unavailable") {
			t.Fatalf("stream HTTP error = %v, want status and body", err)
		}
	})
}

func TestResolveReturnedURLRejectsUserinfo(t *testing.T) {
	client := NewClient("https://api.example.test", WithToken("token_test"))
	if _, _, err := client.resolveReturnedURL("https://user:pass@api.example.test/v1/runs/run/events"); err == nil {
		t.Fatal("returned URL with userinfo was accepted")
	}

	client = NewClient("https://user:pass@api.example.test")
	resolved, sameOrigin, err := client.resolveReturnedURL("/v1/runs/run/events")
	if err != nil {
		t.Fatalf("resolve relative URL with sanitized base userinfo: %v", err)
	}
	if strings.Contains(resolved, "user:pass") {
		t.Fatalf("resolved URL = %q, leaked base API userinfo", resolved)
	}
	if !sameOrigin {
		t.Fatal("relative URL with sanitized base was not same-origin")
	}
}

func TestResolveReturnedURLRejectsDotSegments(t *testing.T) {
	client := NewClient("https://ci.example.test/api", WithToken("token_test"))
	for _, returnedURL := range []string{
		"../v1/runs/run/events",
		"/../v1/runs/run/events",
		"/v1/%2e%2e/admin",
		"https://ci.example.test/api/../admin",
	} {
		t.Run(returnedURL, func(t *testing.T) {
			if _, _, err := client.resolveReturnedURL(returnedURL); err == nil {
				t.Fatalf("resolveReturnedURL(%q) succeeded, want dot-segment rejection", returnedURL)
			}
		})
	}
}

func writeClientTestSSEEvent(t *testing.T, w http.ResponseWriter, id int64, event preflightapi.RunEvent) {
	t.Helper()
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	_, _ = w.Write([]byte("id: "))
	_, _ = w.Write([]byte(fmt.Sprint(id)))
	_, _ = w.Write([]byte("\n"))
	_, _ = w.Write(append(append([]byte("data: "), data...), '\n', '\n'))
}

func TestResolveReturnedURLPreservesAPIBasePathPrefix(t *testing.T) {
	client := NewClient("https://ci.example.test/api", WithToken("token_test"))
	resolved, sameOrigin, err := client.resolveReturnedURL("/v1/runs/run_test/events?cursor=1")
	if err != nil {
		t.Fatalf("resolve returned URL: %v", err)
	}
	if want := "https://ci.example.test/api/v1/runs/run_test/events?cursor=1"; resolved != want {
		t.Fatalf("resolved URL = %q, want %q", resolved, want)
	}
	if !sameOrigin {
		t.Fatal("path-prefixed relative URL was not same-origin")
	}
}

func TestSameOriginNormalizesDefaultPorts(t *testing.T) {
	left, err := url.Parse("https://api.example.test")
	if err != nil {
		t.Fatalf("parse left: %v", err)
	}
	right, err := url.Parse("https://api.example.test:443")
	if err != nil {
		t.Fatalf("parse right: %v", err)
	}
	if !sameOrigin(left, right) {
		t.Fatal("same origin rejected equivalent default https port")
	}

	rightLeadingZeroes, err := url.Parse("https://api.example.test:0443")
	if err != nil {
		t.Fatalf("parse right leading zeroes: %v", err)
	}
	if !sameOrigin(left, rightLeadingZeroes) {
		t.Fatal("same origin rejected equivalent default https port with leading zeroes")
	}

	httpLeft, err := url.Parse("http://api.example.test")
	if err != nil {
		t.Fatalf("parse http left: %v", err)
	}
	httpRightLeadingZeroes, err := url.Parse("http://api.example.test:0080")
	if err != nil {
		t.Fatalf("parse http right leading zeroes: %v", err)
	}
	if !sameOrigin(httpLeft, httpRightLeadingZeroes) {
		t.Fatal("same origin rejected equivalent default http port with leading zeroes")
	}

	other, err := url.Parse("https://api.example.test:444")
	if err != nil {
		t.Fatalf("parse other: %v", err)
	}
	if sameOrigin(left, other) {
		t.Fatal("same origin accepted different port")
	}
}

func TestAdminListEventsFollowsPages(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/admin/runs/run_test/events" {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("limit"); got != "32" {
			t.Errorf("limit = %q, want 32", got)
		}
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("cursor") {
		case "":
			_, _ = io.WriteString(w, `{"run_id":"run_test","events":[{"type":"stdout","run_id":"run_test","data":"one"},{"type":"stdout","run_id":"run_test","data":"two"}],"next_cursor":2,"has_more":true}`)
		case "2":
			_, _ = io.WriteString(w, `{"run_id":"run_test","events":[{"type":"stdout","run_id":"run_test","data":"three"}],"next_cursor":3}`)
		default:
			t.Errorf("unexpected cursor %q", r.URL.Query().Get("cursor"))
			http.Error(w, "unexpected cursor", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	resp, err := NewClient(server.URL, WithToken("admin-token")).AdminListEvents(context.Background(), "run_test")
	if err != nil {
		t.Fatalf("admin list events: %v", err)
	}
	if len(resp.Events) != 3 {
		t.Fatalf("events length = %d, want 3", len(resp.Events))
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("requests = %d, want 2", got)
	}
}

func TestListRunEventsTailAcceptsDocumentedLargeResponse(t *testing.T) {
	const eventCount = 200
	payload := base64.StdEncoding.EncodeToString(make([]byte, 64<<10))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/runs/run_big/events" {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("format"); got != "json" {
			t.Errorf("format = %q, want json", got)
		}
		if got := r.URL.Query().Get("tail"); got != "200" {
			t.Errorf("tail = %q, want 200", got)
		}
		if got := r.URL.Query().Get("logs_only"); got != "true" {
			t.Errorf("logs_only = %q, want true", got)
		}
		events := make([]preflightapi.RunEvent, eventCount)
		for i := range events {
			events[i] = preflightapi.RunEvent{
				Type:       preflightapi.EventStdout,
				RunID:      "run_big",
				DataBase64: payload,
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(preflightapi.RunEventsResponse{
			RunID:  "run_big",
			Events: events,
		}); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	defer server.Close()

	resp, err := NewClient(server.URL, WithToken("token_test")).ListRunEventsTail(context.Background(), "run_big", eventCount, false, true, false)
	if err != nil {
		t.Fatalf("list run events tail: %v", err)
	}
	if len(resp.Events) != eventCount {
		t.Fatalf("events length = %d, want %d", len(resp.Events), eventCount)
	}
}
