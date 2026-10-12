package main

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestExecutionRecoversReadsWithoutReplayingOutput(t *testing.T) {
	var out bytes.Buffer
	polls, reads := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected mutation %s", r.Method)
		}
		w.Header().Set("Retry-After", "0")
		if strings.Contains(r.URL.Path, "verification-sessions") {
			polls++
			if polls == 1 {
				http.Error(w, "temporary", 503)
				return
			}
			writeCLITestJSON(w, 200, preflightapi.VerificationSession{State: preflightapi.VerificationStateTerminal, Recipe: preflightapi.VerificationRecipe{Purpose: preflightapi.ExecutionPurpose}, Nodes: []preflightapi.VerificationNode{{Name: "test", Attempts: []preflightapi.VerificationAttempt{{RunID: "run", Status: preflightapi.StatusFailed}}}}})
			return
		}
		reads++
		if reads == 2 {
			http.Error(w, "temporary", 503)
			return
		}
		cursor := r.URL.Query().Get("cursor")
		page := preflightapi.RunEventsResponse{RunID: "run"}
		switch reads {
		case 1:
			if cursor != "" && cursor != "0" {
				t.Errorf("initial cursor=%s", cursor)
			}
			page.NextCursor = 1
			page.HasMore = true
			page.Events = []preflightapi.RunEvent{{ID: 1, Type: preflightapi.EventStdout, Data: "once\nhttps://user:pass"}}
		case 3:
			if cursor != "1" {
				t.Errorf("resumed cursor=%s", cursor)
			}
			page.NextCursor = 2
			page.Events = []preflightapi.RunEvent{{ID: 2, Type: preflightapi.EventStdout, Data: "word@example.test/path\n"}}
		default:
			t.Errorf("unexpected read %d", reads)
		}
		writeCLITestJSON(w, 200, page)
	}))
	defer server.Close()
	logs := &executionLogObserver{stdout: &out, stderr: &bytes.Buffer{}, redactor: newCLISecretRedactor(nil)}
	_, err := watchVerificationSession(context.Background(), api.NewClient(server.URL), "session", nil, logs)
	if err != nil {
		t.Fatal(err)
	}
	if polls != 2 || reads != 3 || strings.Count(out.String(), "once\n") != 1 || strings.Contains(out.String(), "password") || !strings.Contains(out.String(), "[REDACTED]") {
		t.Fatalf("polls=%d reads=%d output=%q", polls, reads, out.String())
	}
}

func TestExecutionReadFailuresAreBoundedAndCancel(t *testing.T) {
	for _, code := range []int{401, 403, 400, 503} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			reads := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reads++
				w.Header().Set("Retry-After", "0")
				http.Error(w, "failed", code)
			}))
			defer server.Close()
			_, err := watchVerificationSession(context.Background(), api.NewClient(server.URL), "session", nil, nil)
			want := 1
			if code == 503 {
				want = 5
			}
			if err == nil || reads != want {
				t.Fatalf("reads=%d want=%d err=%v", reads, want, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { cancel(); http.Error(w, "failed", 503) }))
	defer server.Close()
	began := time.Now()
	_, err := watchVerificationSession(ctx, api.NewClient(server.URL), "session", nil, nil)
	if !errors.Is(err, context.Canceled) || time.Since(began) > time.Second {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestExecutionReadRecoversInterruptedBodyButRejectsMalformedJSON(t *testing.T) {
	for _, truncated := range []bool{true, false} {
		reads := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reads++
			if reads == 1 {
				if truncated {
					w.Header().Set("Content-Length", "1000")
				}
				_, _ = w.Write([]byte(`{"state":`))
				return
			}
			writeCLITestJSON(w, 200, preflightapi.VerificationSession{State: preflightapi.VerificationStateTerminal})
		}))
		_, err := watchVerificationSession(context.Background(), api.NewClient(server.URL), "session", nil, nil)
		server.Close()
		if truncated {
			if err != nil || reads != 2 {
				t.Fatalf("interrupted body: reads=%d err=%v", reads, err)
			}
		} else if err == nil || reads != 1 {
			t.Fatalf("malformed JSON: reads=%d err=%v", reads, err)
		}
	}
}

func TestExecutionReadCancellationInterruptsBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	began := time.Now()
	err := retryObservationRead(ctx, func() error {
		calls++
		time.AfterFunc(10*time.Millisecond, cancel)
		return api.ResponseError{StatusCode: http.StatusServiceUnavailable}
	})
	if !errors.Is(err, context.Canceled) || calls != 1 || time.Since(began) > time.Second {
		t.Fatalf("backoff cancellation: calls=%d err=%v elapsed=%s", calls, err, time.Since(began))
	}
}

func TestExecutionReadFailsFastOnUnknownAPIHost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	missingHost := &url.Error{Op: "Get", URL: "https://missing.invalid/v1/verification-sessions/session", Err: &net.DNSError{Err: "no such host", Name: "missing.invalid", IsNotFound: true}}
	calls := 0
	err := retryObservationRead(ctx, func() error { calls++; return missingHost })
	if err != missingHost || calls != 1 {
		t.Fatalf("permanent DNS failure was retried: calls=%d err=%v", calls, err)
	}
}
