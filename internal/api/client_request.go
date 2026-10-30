package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

// RequestError retains the original failure for errors.Is/As while rendering
// only safe observations. Transport errors can include private addresses, URLs
// and credentials, so their arbitrary Error strings are never copied here.
type RequestError struct {
	Request *preflightapi.APIRequestDiagnostics
	Err     error
}

func (e RequestError) Error() string {
	reason := "request failed"
	switch {
	case errors.Is(e.Err, context.DeadlineExceeded):
		reason = "context deadline exceeded"
	case errors.Is(e.Err, context.Canceled):
		reason = "context canceled"
	case errors.Is(e.Err, io.ErrUnexpectedEOF):
		reason = "response ended unexpectedly"
	case errors.Is(e.Err, io.EOF):
		reason = "connection closed"
	}
	if e.Request == nil {
		return "API " + reason
	}
	return fmt.Sprintf("%s %s failed during %s: %s (request %s)", e.Request.Method, e.Request.Path, e.Request.Phase, reason, e.Request.RequestID)
}

func (e RequestError) Unwrap() error { return e.Err }

// RequestDiagnosticsFromError returns safe request-local observations when the
// JSON API path recorded them. It does not imply that the server received the
// request or identify the cause of a transport failure.
func RequestDiagnosticsFromError(err error) *preflightapi.APIRequestDiagnostics {
	var requestErr RequestError
	if errors.As(err, &requestErr) {
		return requestErr.Request
	}
	var responseErr ResponseError
	if errors.As(err, &responseErr) {
		return responseErr.Request
	}
	return nil
}

var serverRequestIDPattern = regexp.MustCompile(`^req_[0-9a-f]{32}$`)

type requestObservation struct {
	mu                sync.Mutex
	started           time.Time
	diagnostics       preflightapi.APIRequestDiagnostics
	gettingConnection bool
	dnsStarted        bool
	connectStarted    bool
	tlsStarted        bool
	gotConnection     bool
	wroteRequest      bool
	responseHeaders   bool
}

func observeJSONRequest(req *http.Request, timeout time.Duration) (*http.Request, *requestObservation, error) {
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return nil, nil, errors.New("could not generate API request correlation ID")
	}
	requestID := "cli_" + hex.EncodeToString(entropy[:])
	observation := &requestObservation{
		started: time.Now(),
		diagnostics: preflightapi.APIRequestDiagnostics{
			Method: req.Method, Path: safeJSONRequestPath(req.URL.EscapedPath()), RequestID: requestID,
			Phase: "request", TimeoutMS: timeout.Milliseconds(),
		},
	}
	req.Header.Set(preflightapi.ClientRequestIDHeader, requestID)
	trace := &httptrace.ClientTrace{
		GetConn: func(string) {
			observation.mu.Lock()
			defer observation.mu.Unlock()
			// net/http may replay a request internally. Describe its latest
			// connection acquisition, without adding an application retry.
			observation.gettingConnection = true
			observation.dnsStarted = false
			observation.connectStarted = false
			observation.tlsStarted = false
			observation.gotConnection = false
			observation.wroteRequest = false
			observation.diagnostics.ConnectionReused = nil
		},
		DNSStart: func(httptrace.DNSStartInfo) {
			observation.mu.Lock()
			defer observation.mu.Unlock()
			observation.dnsStarted = true
		},
		ConnectStart: func(string, string) {
			observation.mu.Lock()
			defer observation.mu.Unlock()
			observation.connectStarted = true
		},
		TLSHandshakeStart: func() {
			observation.mu.Lock()
			defer observation.mu.Unlock()
			observation.tlsStarted = true
		},
		// Completion of a racing failed dial must not regress GotConn or
		// WroteRequest observations. Success is established by GotConn.
		GotConn: func(info httptrace.GotConnInfo) {
			observation.mu.Lock()
			defer observation.mu.Unlock()
			observation.gotConnection = true
			reused := info.Reused
			observation.diagnostics.ConnectionReused = &reused
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			if info.Err == nil {
				observation.mu.Lock()
				defer observation.mu.Unlock()
				observation.wroteRequest = true
			}
		},
	}
	return req.WithContext(httptrace.WithClientTrace(req.Context(), trace)), observation, nil
}

// Only fixed API route words survive into diagnostics. Resource identifiers,
// project/secret names, escaped input and unknown paths are opaque. Keeping this
// allowlist diagnostic-only avoids coupling request admission to observability.
func safeJSONRequestPath(path string) string {
	if path == "" {
		return "/"
	}
	const routeWords = " v1 admin account runs run-groups verification-sessions projects workers capability tokens teams queue status explain storage audit cleanup-expired-artifacts cleanup-cache-lanes cancel privacy cases review plan export complete erase providers restore-replay feedback runtime-aliases resolve auth cli device start poll events tests artifacts archive upload-complete latest retry stale cache settings analyze config-suggestions verification-policy source-binding preview proposals secrets disable release-billing-hold billing-evidence cancel-runs drain undrain stage-release promote-release demote-release sources complete-upload upload healthz readyz rotationz "
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		if segment != "" && !strings.Contains(routeWords, " "+segment+" ") {
			segments[i] = "{id}"
		}
	}
	return strings.Join(segments, "/")
}

func (o *requestObservation) receivedHeaders(resp *http.Response) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.responseHeaders = true
	o.diagnostics.StatusCode = resp.StatusCode
	if id := strings.TrimSpace(resp.Header.Get(preflightapi.ServerRequestIDHeader)); serverRequestIDPattern.MatchString(id) {
		o.diagnostics.ServerRequestID = id
	}
}

func (o *requestObservation) snapshot() *preflightapi.APIRequestDiagnostics {
	o.mu.Lock()
	defer o.mu.Unlock()
	diagnostics := o.diagnostics
	diagnostics.ElapsedMS = time.Since(o.started).Milliseconds()
	switch {
	case o.responseHeaders:
		diagnostics.Phase = "response_body"
	case o.wroteRequest:
		diagnostics.Phase = "response_headers"
	case o.gotConnection:
		diagnostics.Phase = "request_write"
	case o.tlsStarted:
		diagnostics.Phase = "tls_handshake"
	case o.connectStarted:
		diagnostics.Phase = "connect"
	case o.dnsStarted:
		diagnostics.Phase = "dns"
	case o.gettingConnection:
		diagnostics.Phase = "connection_acquisition"
	}
	return &diagnostics
}

func (o *requestObservation) incompleteBody() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.diagnostics.ResponseBodyIncomplete = true
}

func (o *requestObservation) failure(err error) error {
	return RequestError{Request: o.snapshot(), Err: err}
}
