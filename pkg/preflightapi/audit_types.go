package preflightapi

import (
	"encoding/json"
	"time"
)

// AuditEventSummary is a persisted audit event as returned by GET /v1/admin/audit.
type AuditEventSummary struct {
	ID           int64           `json:"id"`
	TeamID       string          `json:"team_id"`
	ActorUserID  string          `json:"actor_user_id,omitempty"`
	ActorTokenID string          `json:"actor_token_id,omitempty"`
	Action       string          `json:"action"`
	TargetType   string          `json:"target_type,omitempty"`
	TargetID     string          `json:"target_id,omitempty"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
}

// ListAuditEventsResponse is the audit-log listing. NextBefore is the keyset
// cursor to pass as ?before= for the next page; HasMore indicates more rows exist.
type ListAuditEventsResponse struct {
	Events     []AuditEventSummary `json:"events"`
	NextBefore int64               `json:"next_before,omitempty"`
	HasMore    bool                `json:"has_more"`
}
