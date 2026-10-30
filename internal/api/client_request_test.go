package api

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestJSONRequestTimeoutDoesNotExposeTransportURL(t *testing.T) {
	client := NewClient("https://private-api.invalid", WithToken("secret-bearer"))
	requests := 0
	client.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		<-req.Context().Done()
		return nil, req.Context().Err()
	})}
	var response any
	err := client.doJSONWithResponseLimitAndTimeout(t.Context(), http.MethodGet, "/v1/runs/run_test/tests?arbitrary=secret-query", nil, &response, maxJSONResponseBytes, 5*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("request error = %v, want original deadline", err)
	}
	for _, sensitive := range []string{"private-api.invalid", "secret-query", "secret-bearer"} {
		if strings.Contains(err.Error(), sensitive) {
			t.Errorf("request error exposed %q: %s", sensitive, err)
		}
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want one attempt", requests)
	}
	request := RequestDiagnosticsFromError(err)
	if request == nil || request.Path != "/v1/runs/{id}/tests" || request.Phase != "request" || request.TimeoutMS != 5 || request.ConnectionReused != nil {
		t.Fatalf("untraced request diagnostics = %+v", request)
	}
}

func TestJSONRequestPathMasksArbitraryResourceValues(t *testing.T) {
	for _, tc := range []struct{ path, want string }{
		{"", "/"},
		{"/v1/runs/run%2Fprivate/tests", "/v1/runs/{id}/tests"},
		{"/v1/projects/private-project/secrets/SECRET_NAME", "/v1/projects/{id}/secrets/{id}"},
		{"/private-prefix/v1/verification-sessions/group_private", "/{id}/v1/verification-sessions/{id}"},
	} {
		if got := safeJSONRequestPath(tc.path); got != tc.want {
			t.Errorf("safe path for %q = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestJSONRequestTimeoutDistinguishesHeadersFromBody(t *testing.T) {
	for _, bodyStarted := range []bool{false, true} {
		t.Run(map[bool]string{false: "headers", true: "body"}[bodyStarted], func(t *testing.T) {
			clientIDs := make(chan string, 1)
			const serverID = "req_0123456789abcdef0123456789abcdef"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				clientIDs <- req.Header.Get(preflightapi.ClientRequestIDHeader)
				if bodyStarted {
					w.Header().Set(preflightapi.ServerRequestIDHeader, serverID)
					_, _ = io.WriteString(w, "{")
					w.(http.Flusher).Flush()
				}
				<-req.Context().Done()
			}))
			defer server.Close()
			var out any
			err := NewClient(server.URL).doJSONWithResponseLimitAndTimeout(t.Context(), http.MethodGet, "/v1/runs/run_test/tests?ignored=private", nil, &out, maxJSONResponseBytes, 40*time.Millisecond)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("request error = %v, want deadline", err)
			}
			request := RequestDiagnosticsFromError(err)
			wantPhase := "response_headers"
			if bodyStarted {
				wantPhase = "response_body"
			}
			if request == nil || request.Phase != wantPhase || request.ElapsedMS < 0 || request.TimeoutMS != 40 || request.ConnectionReused == nil {
				t.Fatalf("request diagnostics = %+v, want phase %s and observed connection", request, wantPhase)
			}
			var clientID string
			select {
			case clientID = <-clientIDs:
			case <-time.After(time.Second):
				t.Fatal("server did not observe the request before its deadline")
			}
			if !regexp.MustCompile(`^cli_[0-9a-f]{32}$`).MatchString(request.RequestID) || request.RequestID != clientID {
				t.Fatalf("correlation ID = %q, sent %q", request.RequestID, clientID)
			}
			if bodyStarted && (request.StatusCode != 200 || request.ServerRequestID != serverID || !request.ResponseBodyIncomplete) {
				t.Fatalf("body diagnostics lost received HTTP metadata: %+v", request)
			}
			if !bodyStarted && (request.StatusCode != 0 || request.ServerRequestID != "" || request.ResponseBodyIncomplete) {
				t.Fatalf("headers timeout invented a response: %+v", request)
			}
		})
	}
}

func TestJSONRequestTraceStagesPreserveOriginalNetworkError(t *testing.T) {
	for _, stage := range []string{"connection_acquisition", "dns", "connect", "tls_handshake", "request_write", "response_headers"} {
		t.Run(stage, func(t *testing.T) {
			original := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("private peer and secret-query")}
			client := NewClient("https://private-api.invalid")
			client.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				trace := httptrace.ContextClientTrace(req.Context())
				trace.GetConn("private-api.invalid:443")
				switch stage {
				case "dns":
					trace.DNSStart(httptrace.DNSStartInfo{Host: "private-api.invalid"})
				case "connect":
					trace.ConnectStart("tcp", "private-address")
				case "tls_handshake":
					trace.TLSHandshakeStart()
				case "request_write":
					trace.GotConn(httptrace.GotConnInfo{Reused: true})
				case "response_headers":
					trace.GotConn(httptrace.GotConnInfo{Reused: true})
					trace.WroteRequest(httptrace.WroteRequestInfo{})
				}
				return nil, original
			})}
			var out any
			err := client.doJSON(t.Context(), http.MethodGet, "/v1/runs/run_test/tests?secret=query", nil, &out)
			request := RequestDiagnosticsFromError(err)
			if request == nil || request.Phase != stage {
				t.Fatalf("request diagnostics = %+v, want stage %s", request, stage)
			}
			var opErr *net.OpError
			if !errors.As(err, &opErr) || opErr != original {
				t.Fatalf("error did not retain original network failure: %v", err)
			}
			for _, secret := range []string{"private-api", "private peer", "secret-query", "secret=query"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("request error exposed %q: %v", secret, err)
				}
			}
		})
	}
}

func TestJSONRequestTraceConcurrentCallbacksDoNotRegressProgress(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://example.invalid/v1/runs/run_test/tests", nil)
	if err != nil {
		t.Fatal(err)
	}
	req, observation, err := observeJSONRequest(req, requestTimeout)
	if err != nil {
		t.Fatal(err)
	}
	trace := httptrace.ContextClientTrace(req.Context())
	trace.GetConn("ignored")
	trace.GotConn(httptrace.GotConnInfo{Reused: true})
	trace.WroteRequest(httptrace.WroteRequestInfo{})
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			trace.DNSStart(httptrace.DNSStartInfo{})
			trace.ConnectStart("tcp", "ignored")
			trace.TLSHandshakeStart()
			if got := observation.snapshot().Phase; got != "response_headers" {
				t.Errorf("late dial observations regressed progress to %q", got)
			}
		})
	}
	wg.Wait()
}

type failingJSONBody struct{ io.Reader }

func (b failingJSONBody) Read(p []byte) (int, error) {
	if b.Reader != nil {
		n, err := b.Reader.Read(p)
		if err == nil {
			return n, nil
		}
	}
	return 0, context.DeadlineExceeded
}

func (failingJSONBody) Close() error { return nil }

func TestJSONRequestErrorResponsePreservesStatusWhenBodyReadFails(t *testing.T) {
	client := NewClient("https://example.invalid")
	client.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusForbidden, Status: "403 Forbidden", Header: make(http.Header), Body: failingJSONBody{Reader: strings.NewReader(`{"code":"auth_forbidden"}`)}, Request: req}, nil
	})}
	var out any
	err := client.doJSON(t.Context(), http.MethodGet, "/v1/runs/run_test/tests", nil, &out)
	response, ok := err.(ResponseError)
	if !ok || response.StatusCode != http.StatusForbidden || response.Code != "auth_forbidden" {
		t.Fatalf("request error = %#v, want original authoritative API denial", err)
	}
	request := RequestDiagnosticsFromError(err)
	if request == nil || request.StatusCode != 403 || !request.ResponseBodyIncomplete {
		t.Fatalf("body read diagnostics = %+v", request)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("incomplete API denial was replaced by transport timeout")
	}
}

func TestJSONRequestRejectsMalformedServerCorrelation(t *testing.T) {
	for _, id := range []string{"req_0123456789abcdef0123456789abcdef", "Bearer secret-value", strings.Repeat("a", 1024), "req_0123456789ABCDEF0123456789ABCDEF", "https://private.invalid/?token=secret"} {
		client := NewClient("https://example.invalid")
		client.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			header := make(http.Header)
			header.Set(preflightapi.ServerRequestIDHeader, id)
			return &http.Response{StatusCode: http.StatusOK, Header: header, Body: failingJSONBody{}, Request: req}, nil
		})}
		var out any
		err := client.doJSON(t.Context(), http.MethodGet, "/v1/runs/run_test/tests", nil, &out)
		request := RequestDiagnosticsFromError(err)
		if request == nil || (request.ServerRequestID != "") != serverRequestIDPattern.MatchString(id) {
			t.Fatalf("server ID %q was not validated: %+v", id, request)
		}
	}
}

func TestJSONRequestHonorsParentCancellationAndDeadline(t *testing.T) {
	for _, cancelNow := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Millisecond)
		if cancelNow {
			cancel()
		}
		client := NewClient("https://example.invalid")
		attempts := 0
		client.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			attempts++
			<-req.Context().Done()
			return nil, req.Context().Err()
		})}
		var out any
		err := client.doJSON(ctx, http.MethodGet, "/v1/runs/run_test/tests", nil, &out)
		cancel()
		want := context.DeadlineExceeded
		if cancelNow {
			want = context.Canceled
		}
		if !errors.Is(err, want) || attempts != 1 {
			t.Fatalf("error=%v attempts=%d, want original %v and one attempt", err, attempts, want)
		}
	}
}
