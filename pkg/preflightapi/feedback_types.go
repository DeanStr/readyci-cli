package preflightapi

import "time"

const (
	FeedbackCategoryBug     = "bug"
	FeedbackCategoryFeature = "feature"
	FeedbackCategoryBilling = "billing"
	FeedbackCategoryOps     = "ops"
	FeedbackCategoryOther   = "other"

	FeedbackTicketStatusNotConfigured = "not_configured"
	FeedbackTicketStatusCreated       = "created"
	FeedbackTicketStatusFailed        = "failed"
)

type FeedbackRunContext struct {
	RunID          string `json:"run_id,omitempty"`
	Status         string `json:"status,omitempty"`
	Command        string `json:"command,omitempty"`
	Classification string `json:"classification,omitempty"`
	FailureSummary string `json:"failure_summary,omitempty"`
	PrimaryFile    string `json:"primary_file,omitempty"`
}

type FeedbackContact struct {
	UserID string `json:"user_id,omitempty"`
	Name   string `json:"name,omitempty"`
	Email  string `json:"email,omitempty"`
	Source string `json:"source,omitempty"`
}

type CreateFeedbackRequest struct {
	ProjectID         string             `json:"project_id,omitempty"`
	RunID             string             `json:"run_id,omitempty"`
	Category          string             `json:"category"`
	Message           string             `json:"message"`
	IncludeLogs       bool               `json:"include_logs,omitempty"`
	LogsIncluded      bool               `json:"logs_included,omitempty"`
	Contact           FeedbackContact    `json:"contact,omitempty"`
	Client            ClientInfo         `json:"client"`
	Run               FeedbackRunContext `json:"run,omitempty"`
	DiagnosticSummary string             `json:"diagnostic_summary,omitempty"`
}

type FeedbackRecord struct {
	FeedbackID        string             `json:"feedback_id"`
	TeamID            string             `json:"team_id,omitempty"`
	ProjectID         string             `json:"project_id,omitempty"`
	RunID             string             `json:"run_id,omitempty"`
	Category          string             `json:"category"`
	Message           string             `json:"message"`
	IncludeLogs       bool               `json:"include_logs"`
	LogsIncluded      bool               `json:"logs_included"`
	Contact           FeedbackContact    `json:"contact,omitempty"`
	Client            ClientInfo         `json:"client"`
	Run               FeedbackRunContext `json:"run,omitempty"`
	DiagnosticSummary string             `json:"diagnostic_summary,omitempty"`
	TicketStatus      string             `json:"ticket_status,omitempty"`
	ExternalTicketID  string             `json:"external_ticket_id,omitempty"`
	ExternalTicketURL string             `json:"external_ticket_url,omitempty"`
	TicketError       string             `json:"ticket_error,omitempty"`
	CreatedAt         time.Time          `json:"created_at"`
}

type CreateFeedbackResponse struct {
	Feedback FeedbackRecord `json:"feedback"`
}
