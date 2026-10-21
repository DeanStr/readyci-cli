package preflightapi

import "time"

type CLIDeviceStartRequest struct {
	CodeChallenge string `json:"code_challenge"`
}
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
type CLIDeviceReview struct {
	UserID    string    `json:"user_id"`
	UserCode  string    `json:"user_code"`
	State     string    `json:"state"`
	Account   string    `json:"account"`
	TeamID    string    `json:"team_id"`
	Scopes    []string  `json:"scopes"`
	ExpiresAt time.Time `json:"expires_at"`
}
type CLIDeviceDecision struct {
	Scopes   []string `json:"scopes"`
	UserID   string   `json:"user_id"`
	TeamID   string   `json:"team_id"`
	UserCode string   `json:"user_code"`
	Approve  bool     `json:"approve"`
}
