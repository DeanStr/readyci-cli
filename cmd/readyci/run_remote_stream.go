package main

import (
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func runStatusForTerminalEvent(eventType string) string {
	switch eventType {
	case preflightapi.EventRunFinished:
		return preflightapi.StatusSucceeded
	case preflightapi.EventRunTimedOut:
		return preflightapi.StatusTimedOut
	case preflightapi.EventRunCancelled:
		return preflightapi.StatusCancelled
	case preflightapi.EventRunFailed:
		return preflightapi.StatusFailed
	default:
		return ""
	}
}
