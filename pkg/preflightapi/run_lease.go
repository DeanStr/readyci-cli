package preflightapi

import "time"

type RunHeartbeatRequest struct {
	ClaimToken string `json:"claim_token"`
}

// A worker stops execution after 30 seconds without acknowledged claim
// liveness. The server's longer window leaves time for containment before
// reassignment; neither window extends the hard execution/finalization lease.
const (
	RunHeartbeatInterval     = 5 * time.Second
	RunControlSilenceTimeout = 30 * time.Second
	RunHeartbeatTimeout      = 90 * time.Second
)
