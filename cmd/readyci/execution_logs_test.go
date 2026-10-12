package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestExecutionLogsFollowLiveAttemptsAndDrainTerminalPages(t *testing.T) {
	var stdout, stderr bytes.Buffer
	logs := &executionLogObserver{stdout: &stdout, stderr: &stderr}
	polls := 0
	reads := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/verification-sessions/") {
			polls++
			status, state := preflightapi.StatusRunning, preflightapi.VerificationStateSealed
			attempts := []preflightapi.VerificationAttempt{{RunID: "first", Attempt: 1, Status: status}}
			if polls > 1 {
				if stdout.String() != "live\n" {
					t.Errorf("no live output before terminal poll: %q", stdout.String())
				}
				state = preflightapi.VerificationStateTerminal
				attempts[0].Status = preflightapi.StatusFailed
				attempts = append(attempts, preflightapi.VerificationAttempt{RunID: "retry", Attempt: 2, Status: preflightapi.StatusSucceeded})
			}
			writeCLITestJSON(w, 200, preflightapi.VerificationSession{Recipe: preflightapi.VerificationRecipe{Purpose: preflightapi.ExecutionPurpose}, State: state, Nodes: []preflightapi.VerificationNode{{Name: "tests[go=1.25]", Attempts: attempts}}})
			return
		}
		runID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/runs/"), "/events")
		reads[runID]++
		cursor, _ := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
		event := preflightapi.RunEvent{ID: cursor + 1, Type: preflightapi.EventStdout}
		more := false
		if runID == "retry" {
			event.Data = "retried\n"
		} else {
			switch cursor {
			case 0:
				event.Data = "live\n"
			case 1:
				event.Type = preflightapi.EventStderr
				event.DataBase64 = base64.StdEncoding.EncodeToString([]byte("failure\n"))
				more = true
			case 2:
				event.Data = "final\n"
			default:
				t.Errorf("duplicate read at %d", cursor)
			}
		}
		writeCLITestJSON(w, 200, preflightapi.RunEventsResponse{RunID: runID, Events: []preflightapi.RunEvent{event}, NextCursor: event.ID, HasMore: more})
	}))
	defer server.Close()
	session, err := watchVerificationSession(context.Background(), api.NewClient(server.URL), "session", nil, logs)
	if err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "live\nfinal\nretried\n" || !strings.Contains(stderr.String(), "failure\n") || !strings.Contains(stderr.String(), "attempt 2, retry") {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if err := logs.observe(context.Background(), api.NewClient(server.URL), session); err != nil {
		t.Fatal(err)
	}
	if reads["first"] != 3 || reads["retry"] != 1 {
		t.Fatalf("reads=%v", reads)
	}
}

func TestExecutionLogsFailOnInvalidPagesAndWrites(t *testing.T) {
	for _, test := range []struct {
		name      string
		page      preflightapi.RunEventsResponse
		badWriter bool
	}{
		{name: "stalled cursor", page: preflightapi.RunEventsResponse{RunID: "run", HasMore: true}},
		{name: "wrong run", page: preflightapi.RunEventsResponse{RunID: "other"}},
		{name: "bad payload", page: preflightapi.RunEventsResponse{RunID: "run", NextCursor: 1, Events: []preflightapi.RunEvent{{ID: 1, Type: preflightapi.EventStdout, DataBase64: "invalid"}}}},
		{name: "bad event cursor", page: preflightapi.RunEventsResponse{RunID: "run", NextCursor: 1, Events: []preflightapi.RunEvent{{ID: 2, Type: preflightapi.EventStdout}}}},
		{name: "write failure", badWriter: true, page: preflightapi.RunEventsResponse{RunID: "run", NextCursor: 1, Events: []preflightapi.RunEvent{{ID: 1, Type: preflightapi.EventStdout, Data: "output"}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeCLITestJSON(w, 200, test.page) }))
			defer server.Close()
			var out bytes.Buffer
			observer := &executionLogObserver{stdout: &out, stderr: &out}
			if test.badWriter {
				observer.stdout = executionFailWriter{}
			}
			session := preflightapi.VerificationSession{Recipe: preflightapi.VerificationRecipe{Purpose: preflightapi.ExecutionPurpose}, Nodes: []preflightapi.VerificationNode{{Attempts: []preflightapi.VerificationAttempt{{RunID: "run", Status: preflightapi.StatusSucceeded}}}}}
			if err := observer.observe(context.Background(), api.NewClient(server.URL), session); err == nil {
				t.Fatal("accepted invalid output")
			}
		})
	}
}

type executionFailWriter struct{}

func (executionFailWriter) Write([]byte) (int, error) { return 0, errors.New("closed pipe") }

func TestExecutionLogsSkipJSONAndVerification(t *testing.T) {
	if executionLogsForOutput(true, "") != nil {
		t.Fatal("JSON enabled live output")
	}
	observer := &executionLogObserver{}
	if err := observer.observe(context.Background(), nil, preflightapi.VerificationSession{}); err != nil {
		t.Fatal(err)
	}
}
