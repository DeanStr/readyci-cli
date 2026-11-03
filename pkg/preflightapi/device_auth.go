package preflightapi

import "time"

type CLIDeviceStartRequest struct {
	CodeChallenge string `json:"code_challenge"`
}

// CLIDeviceAuthorization starts a device login. VerificationURI is the plain
// approval page: it never carries the user code, which the user must type
// (there is deliberately no verification_uri_complete).
type CLIDeviceAuthorization struct {
	DeviceCode      string    `json:"device_code"`
	UserCode        string    `json:"user_code"`
	VerificationURI string    `json:"verification_uri"`
	ExpiresAt       time.Time `json:"expires_at"`
	IntervalSeconds int       `json:"interval_seconds"`
}
type CLIDevicePollRequest struct {
	DeviceCode   string `json:"device_code"`
	CodeVerifier string `json:"code_verifier"`
}
type CLIDevicePollResponse struct {
	State string                  `json:"state"`
	Token *CreateAPITokenResponse `json:"token,omitempty"`
}

// CLIDeviceRequester describes the terminal that started a device login, as
// observed by the API. IP is empty when the API could not determine it.
type CLIDeviceRequester struct {
	IP          string    `json:"ip"`
	UserAgent   string    `json:"user_agent"`
	RequestedAt time.Time `json:"requested_at"`
	AgeSeconds  int64     `json:"age_seconds"`
}

// CLIDeviceReview is the browser review of a pending device login. Scopes is
// the exact set an approval grants; the decision must echo it unchanged.
type CLIDeviceReview struct {
	UserID    string             `json:"user_id"`
	UserCode  string             `json:"user_code"`
	State     string             `json:"state"`
	Account   string             `json:"account"`
	TeamID    string             `json:"team_id"`
	TeamName  string             `json:"team_name"`
	Scopes    []string           `json:"scopes"`
	ExpiresAt time.Time          `json:"expires_at"`
	Requester CLIDeviceRequester `json:"requester"`
}
type CLIDeviceDecision struct {
	Scopes   []string `json:"scopes"`
	UserID   string   `json:"user_id"`
	TeamID   string   `json:"team_id"`
	UserCode string   `json:"user_code"`
	Approve  bool     `json:"approve"`
}
