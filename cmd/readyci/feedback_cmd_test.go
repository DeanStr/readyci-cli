package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestFeedbackCommandSubmitsFeedback(t *testing.T) {
	var got preflightapi.CreateFeedbackRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/feedback" {
			t.Fatalf("path = %s, want /v1/feedback", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-token" {
			t.Fatalf("authorization = %q", auth)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		writeCLITestJSON(w, http.StatusCreated, preflightapi.CreateFeedbackResponse{Feedback: preflightapi.FeedbackRecord{
			FeedbackID:        "fb_test",
			ProjectID:         got.ProjectID,
			Category:          got.Category,
			Message:           got.Message,
			TicketStatus:      preflightapi.FeedbackTicketStatusCreated,
			ExternalTicketID:  "12345",
			ExternalTicketURL: "https://chatwoot.example.test/app/accounts/7/conversations/1",
		}})
	}))
	defer server.Close()

	output, err := captureStdoutForCLITest(t, func() error {
		return feedbackCommand([]string{
			"--api-url", server.URL,
			"--token", "test-token",
			"--project", "proj_feedback",
			"--category", "bug",
			"--contact-email", "user@example.test",
			"--contact-name", "Test User",
			"--message", "the run page is confusing",
		})
	})
	if err != nil {
		t.Fatalf("feedback command: %v", err)
	}
	if got.ProjectID != "proj_feedback" || got.Category != preflightapi.FeedbackCategoryBug || got.Message != "the run page is confusing" {
		t.Fatalf("request = %+v", got)
	}
	if got.Client.Version == "" || !strings.Contains(got.Client.Platform, "/") {
		t.Fatalf("client info = %+v, want version and platform", got.Client)
	}
	if got.Contact.Email != "user@example.test" || got.Contact.Name != "Test User" {
		t.Fatalf("contact = %+v", got.Contact)
	}
	requireCLITestOutputContains(t, output, "feedback_id: fb_test", "support_status: created", "support_url: https://chatwoot.example.test/app/accounts/7/conversations/1")
}

func TestFeedbackCommandRejectsUnsafeProjectID(t *testing.T) {
	err := feedbackCommand([]string{"--api-url", "https://api.readyci.test", "--token", "test-token", "--project", "owner\nrepo", "--message", "helpful note"})
	if err == nil || !strings.Contains(err.Error(), "project_id must not contain control characters") {
		t.Fatalf("feedback unsafe project err = %v, want project_id rejection", err)
	}
}

func TestFeedbackCommandUsesLatestRunContext(t *testing.T) {
	var got preflightapi.CreateFeedbackRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs":
			if project := r.URL.Query().Get("project"); project != "proj_feedback" {
				t.Fatalf("project query = %q, want proj_feedback", project)
			}
			writeCLITestJSON(w, http.StatusOK, preflightapi.ListRunsResponse{Runs: []preflightapi.RunSummary{{
				RunID:          "run_latest",
				ProjectID:      "proj_feedback",
				Status:         preflightapi.StatusFailed,
				Command:        "go test ./...",
				Classification: preflightapi.ClassificationTestFailed,
				FailureSummary: "tests failed",
				PrimaryFile:    "pkg/app_test.go",
			}}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/feedback":
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			writeCLITestJSON(w, http.StatusCreated, preflightapi.CreateFeedbackResponse{Feedback: preflightapi.FeedbackRecord{
				FeedbackID:   "fb_latest",
				ProjectID:    got.ProjectID,
				RunID:        got.RunID,
				Category:     got.Category,
				Message:      got.Message,
				Run:          got.Run,
				TicketStatus: preflightapi.FeedbackTicketStatusNotConfigured,
			}})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	if _, err := captureStdoutForCLITest(t, func() error {
		return feedbackCommand([]string{
			"--api-url", server.URL,
			"--token", "test-token",
			"--project", "proj_feedback",
			"--run", "latest",
			"this failure looks like infrastructure",
		})
	}); err != nil {
		t.Fatalf("feedback command: %v", err)
	}
	if got.RunID != "run_latest" || got.Run.Status != preflightapi.StatusFailed || got.Run.FailureSummary != "tests failed" {
		t.Fatalf("run context = %+v", got.Run)
	}
}

func TestFeedbackCommandDoesNotReadExplicitRun(t *testing.T) {
	t.Setenv("READYCI_PROJECT_ID", "ambient_project")

	var got preflightapi.CreateFeedbackRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/feedback" {
			t.Fatalf("unexpected request %s %s; concrete --run should not prefetch run detail", r.Method, r.URL.String())
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		writeCLITestJSON(w, http.StatusCreated, preflightapi.CreateFeedbackResponse{Feedback: preflightapi.FeedbackRecord{
			FeedbackID:   "fb_explicit_run",
			RunID:        got.RunID,
			Category:     got.Category,
			Message:      got.Message,
			TicketStatus: preflightapi.FeedbackTicketStatusNotConfigured,
		}})
	}))
	defer server.Close()

	if _, err := captureStdoutForCLITest(t, func() error {
		return feedbackCommand([]string{
			"--api-url", server.URL,
			"--token", "test-token",
			"--run", "run_explicit",
			"--message", "please look at this run",
		})
	}); err != nil {
		t.Fatalf("feedback command: %v", err)
	}
	if got.RunID != "run_explicit" {
		t.Fatalf("run id = %q, want run_explicit", got.RunID)
	}
	if got.ProjectID != "" {
		t.Fatalf("project id = %q, want empty so server can infer it from run_id", got.ProjectID)
	}
	if got.Run.RunID != "" || got.Run.Status != "" {
		t.Fatalf("run context = %+v, want empty without runs:read prefetch", got.Run)
	}
}

func TestFeedbackCommandAllowsExplicitProjectForExplicitRun(t *testing.T) {
	t.Setenv("READYCI_PROJECT_ID", "ambient_project")

	var got preflightapi.CreateFeedbackRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/feedback" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		writeCLITestJSON(w, http.StatusCreated, preflightapi.CreateFeedbackResponse{Feedback: preflightapi.FeedbackRecord{
			FeedbackID:   "fb_explicit_project",
			ProjectID:    got.ProjectID,
			RunID:        got.RunID,
			Category:     got.Category,
			Message:      got.Message,
			TicketStatus: preflightapi.FeedbackTicketStatusNotConfigured,
		}})
	}))
	defer server.Close()

	if _, err := captureStdoutForCLITest(t, func() error {
		return feedbackCommand([]string{
			"--api-url", server.URL,
			"--token", "test-token",
			"--project", "explicit_project",
			"--run", "run_explicit",
			"--message", "please look at this run",
		})
	}); err != nil {
		t.Fatalf("feedback command: %v", err)
	}
	if got.ProjectID != "explicit_project" || got.RunID != "run_explicit" {
		t.Fatalf("request = %+v, want explicit project and run", got)
	}
}
