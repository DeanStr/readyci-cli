package preflightapi

const (
	// ClientRequestIDHeader correlates one logical CLI request with API logs.
	// It is diagnostic input only and never supplies trusted server identity.
	ClientRequestIDHeader = "X-ReadyCI-Client-Request-ID"
	ServerRequestIDHeader = "X-ReadyCI-Request-ID"
)

// APIRequestDiagnostics records client observations of an API request. A phase
// identifies where progress was last observed, not the cause of a failure.
// It contains no origin, query, headers, response body or network addresses.
type APIRequestDiagnostics struct {
	Method                 string `json:"method"`
	Path                   string `json:"path"`
	RequestID              string `json:"request_id"`
	ServerRequestID        string `json:"server_request_id,omitempty"`
	Phase                  string `json:"phase"`
	ElapsedMS              int64  `json:"elapsed_ms"`
	TimeoutMS              int64  `json:"timeout_ms"`
	StatusCode             int    `json:"status_code,omitempty"`
	ConnectionReused       *bool  `json:"connection_reused,omitempty"`
	ResponseBodyIncomplete bool   `json:"response_body_incomplete,omitempty"`
}
