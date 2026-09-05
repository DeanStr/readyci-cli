package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func writeJSONLLogs(entries []runLogEntry) error {
	encoder := json.NewEncoder(os.Stdout)
	for _, entry := range entries {
		if err := encoder.Encode(entry); err != nil {
			return err
		}
	}
	return nil
}

func writeHumanLogEntries(entries []runLogEntry) error {
	for _, entry := range entries {
		var err error
		switch entry.Stream {
		case "stdout":
			_, err = os.Stdout.WriteString(entry.Text)
		case "stderr":
			_, err = os.Stderr.WriteString(entry.Text)
		default:
			message := humanLogEventMessage(entry)
			if message == "" {
				continue
			}
			_, err = os.Stderr.WriteString(message + "\n")
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func humanLogEventMessage(entry runLogEntry) string {
	switch entry.Event {
	case preflightapi.EventLogTruncated:
		if entry.Message != "" {
			return entry.Message
		}
		return "preflight: run log limit reached; further log output was truncated"
	case preflightapi.EventRunFailed, preflightapi.EventRunTimedOut, preflightapi.EventRunCancelled:
		if entry.Message != "" {
			return entry.Message
		}
		if entry.Status != "" {
			return "readyci: run " + entry.Status
		}
		return "readyci: " + entry.Event
	case preflightapi.EventCommandExited:
		if entry.ExitCode != nil && *entry.ExitCode != 0 {
			return fmt.Sprintf("readyci: command exited with code %d", *entry.ExitCode)
		}
	}
	return ""
}

func formatEventTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339Nano)
}
