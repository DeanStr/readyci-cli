package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

type privacyExportDownloaderStub struct {
	payload string
	err     error
}

func (s privacyExportDownloaderStub) DownloadAdminPrivacyExport(_ context.Context, _ string, writer io.Writer) error {
	if s.payload != "" {
		_, _ = io.WriteString(writer, s.payload)
	}
	return s.err
}

func TestDownloadAdminPrivacyExportValidatesBeforeWritingOutput(t *testing.T) {
	payload := validPrivacyExportPayload(t, "case_1")
	var output bytes.Buffer
	if err := downloadAdminPrivacyExport(t.Context(), privacyExportDownloaderStub{payload: payload}, "case_1", &output); err != nil {
		t.Fatalf("download privacy export: %v", err)
	}
	if output.String() != payload {
		t.Fatalf("output = %q, want exact downloaded payload", output.String())
	}
}

func TestDownloadAdminPrivacyExportDoesNotWritePartialOrInvalidOutput(t *testing.T) {
	tests := []struct {
		name string
		stub privacyExportDownloaderStub
	}{
		{name: "interrupted", stub: privacyExportDownloaderStub{payload: `{"schema_version":`, err: errors.New("connection reset")}},
		{name: "truncated", stub: privacyExportDownloaderStub{payload: `{"schema_version":"readyci.privacy_export.v2"`}},
		{name: "wrong case", stub: privacyExportDownloaderStub{payload: `{"schema_version":"readyci.privacy_export.v2","case_id":"case_2","digest":"sha256:test"}`}},
		{name: "missing digest", stub: privacyExportDownloaderStub{payload: `{"schema_version":"readyci.privacy_export.v2","case_id":"case_1"}`}},
		{name: "noncanonical digest", stub: privacyExportDownloaderStub{payload: `{"schema_version":"readyci.privacy_export.v2","case_id":"case_1","digest":"sha256:test"}`}},
		{name: "content digest mismatch", stub: privacyExportDownloaderStub{payload: strings.Replace(validPrivacyExportPayload(t, "case_1"), `"users":1`, `"users":2`, 1)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			err := downloadAdminPrivacyExport(t.Context(), test.stub, "case_1", &output)
			if err == nil {
				t.Fatal("download succeeded")
			}
			if output.Len() != 0 {
				t.Fatalf("invalid response wrote %d bytes", output.Len())
			}
		})
	}
}

func validPrivacyExportPayload(t *testing.T, caseID string) string {
	t.Helper()
	export := preflightapi.AdminPrivacyExport{
		SchemaVersion: preflightapi.PrivacyExportSchemaVersion,
		CaseID:        caseID,
		GeneratedAt:   time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC),
		Subject:       preflightapi.PrivacyExportSubject{UserID: "user_1", Email: "subject@example.test"},
		Manifest:      map[string]int64{"users": 1},
	}
	var err error
	export.Digest, err = preflightapi.PrivacyExportDigest(export)
	if err != nil {
		t.Fatalf("compute privacy export digest: %v", err)
	}
	payload, err := json.Marshal(export)
	if err != nil {
		t.Fatalf("encode privacy export: %v", err)
	}
	return string(payload)
}

func TestValidateDownloadedPrivacyExportRejectsUnknownField(t *testing.T) {
	err := validateDownloadedPrivacyExport(strings.NewReader(`{"schema_version":"readyci.privacy_export.v2","case_id":"case_1","unexpected":true,"digest":"sha256:test"}`), "case_1")
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("error = %v, want unknown field failure", err)
	}
}
