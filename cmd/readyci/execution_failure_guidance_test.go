package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/guestcontract"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestExecutionFailureGuidanceRequiresAvailableRetainedWorkspace(t *testing.T) {
	for _, test := range []struct {
		name, log                    string
		available, expired, wantHint bool
	}{
		{name: "retained", log: guestcontract.FailedWorkspaceArtifactRetainedMessage, available: true, wantHint: true},
		{name: "upload failed", log: guestcontract.FailedWorkspaceArtifactRetainedMessage},
		{name: "expired", log: guestcontract.FailedWorkspaceArtifactRetainedMessage, available: true, expired: true},
		{name: "skipped", log: guestcontract.FailedWorkspaceArtifactRetainedMessage + "\n" + guestcontract.FailedWorkspaceArtifactSkippedMessage, available: true},
		{name: "other artifacts", available: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/events"):
					// Split the retention message across pages as happens in durable output.
					cut := len(test.log) / 2
					page := preflightapi.RunEventsResponse{RunID: "run", NextCursor: 1, HasMore: true, Events: []preflightapi.RunEvent{{ID: 1, Type: preflightapi.EventStderr, Data: test.log[:cut]}}}
					if r.URL.Query().Get("cursor") == "1" {
						page.NextCursor = 2
						page.HasMore = false
						page.Events = []preflightapi.RunEvent{{ID: 2, Type: preflightapi.EventStderr, Data: test.log[cut:]}}
					}
					writeCLITestJSON(w, 200, page)
				case strings.HasSuffix(r.URL.Path, "/artifacts"):
					result := preflightapi.ListArtifactsResponse{RunID: "run"}
					if test.available {
						expires := time.Now().Add(time.Hour)
						if test.expired {
							expires = time.Now().Add(-time.Hour)
						}
						result.Artifacts = []preflightapi.ArtifactSummary{{RunID: "run", SizeBytes: 100, ExpiresAt: expires}}
					}
					writeCLITestJSON(w, 200, result)
				default:
					writeCLITestJSON(w, 200, preflightapi.RunDetail{Summary: preflightapi.RunSummary{Classification: preflightapi.ClassificationTimeout, Retryable: true, FailureSummary: "failed https://user:secret@example.test/path"}})
				}
			}))
			defer server.Close()
			logs := &executionLogObserver{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
			session := preflightapi.VerificationSession{Recipe: preflightapi.VerificationRecipe{Purpose: preflightapi.ExecutionPurpose}, Nodes: []preflightapi.VerificationNode{{Attempts: []preflightapi.VerificationAttempt{{RunID: "run", Status: preflightapi.StatusFailed}}}}}
			if err := logs.observe(context.Background(), api.NewClient(server.URL), session); err != nil {
				t.Fatal(err)
			}
			output, err := captureStderrForCLITest(t, func() error { printExecutionFailure(server.URL, "", "run", logs.retainedWorkspace("run")); return nil })
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"classification: timeout (retryable: true)", "increase the check timeout", "readyci runs explain", "readyci runs logs", "--api-url " + server.URL} {
				if !strings.Contains(output, want) {
					t.Fatalf("missing %q: %s", want, output)
				}
			}
			if strings.Contains(output, "secret@") || strings.Contains(output, "user:") {
				t.Fatalf("unredacted summary: %s", output)
			}
			if strings.Contains(output, "readyci artifacts download") != test.wantHint {
				t.Fatalf("wrong artifact hint: %s", output)
			}
		})
	}
}
