package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestAdminPrivacyBulkOperationsUseTransferTimeout(t *testing.T) {
	var paths []string
	var deadlines []time.Duration
	client := NewClient("https://api.readyci.test")
	client.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		deadline, ok := req.Context().Deadline()
		if !ok {
			t.Fatal("privacy bulk request has no deadline")
		}
		paths = append(paths, req.URL.EscapedPath())
		deadlines = append(deadlines, time.Until(deadline))
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Request:    req,
		}, nil
	})}

	if _, err := client.AdminPlanPrivacyCase(t.Context(), "case_1", "review"); err != nil {
		t.Fatalf("plan privacy case: %v", err)
	}
	if _, err := client.AdminExecutePrivacyErasure(t.Context(), "case_1", preflightapi.AdminExecutePrivacyErasureRequest{}); err != nil {
		t.Fatalf("execute privacy erasure: %v", err)
	}
	if got := strings.Join(paths, ","); got != "/v1/admin/privacy/cases/case_1/plan,/v1/admin/privacy/cases/case_1/erase" {
		t.Fatalf("privacy bulk request paths = %q", got)
	}
	for _, remaining := range deadlines {
		if remaining <= requestTimeout || remaining > uploadTimeout {
			t.Fatalf("privacy bulk request deadline = %s, want more than %s and at most %s", remaining, requestTimeout, uploadTimeout)
		}
	}
}

func TestDownloadAdminPrivacyExportStreamsBeyondGenericJSONLimit(t *testing.T) {
	payload := strings.Repeat("x", maxJSONResponseBytes+1)
	var gotPath, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = strings.NewReader(payload).WriteTo(w)
	}))
	defer server.Close()

	var output bytes.Buffer
	err := NewClient(server.URL, WithToken("privacy-token")).DownloadAdminPrivacyExport(t.Context(), "case/large", &output)
	if err != nil {
		t.Fatalf("download privacy export: %v", err)
	}
	if output.Len() != len(payload) {
		t.Fatalf("downloaded bytes = %d, want %d", output.Len(), len(payload))
	}
	if gotPath != "/v1/admin/privacy/cases/case%2Flarge/export" {
		t.Fatalf("request path = %q", gotPath)
	}
	if gotAuth != "Bearer privacy-token" {
		t.Fatalf("authorization = %q", gotAuth)
	}
}

func TestDownloadAdminPrivacyExportReturnsStructuredHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"code":"privacy_case_blocked","error":"blocked"}`))
	}))
	defer server.Close()

	var output bytes.Buffer
	err := NewClient(server.URL).DownloadAdminPrivacyExport(context.Background(), "case_1", &output)
	responseErr, ok := err.(ResponseError)
	if !ok || responseErr.StatusCode != http.StatusConflict || responseErr.Code != "privacy_case_blocked" {
		t.Fatalf("error = %#v, want structured conflict", err)
	}
	if output.Len() != 0 {
		t.Fatalf("error response wrote %d export bytes", output.Len())
	}
}
