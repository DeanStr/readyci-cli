package main

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

type runLogEntry struct {
	EventID  int64  `json:"event_id,omitempty"`
	Seq      int    `json:"seq"`
	Time     string `json:"time,omitempty"`
	Phase    string `json:"phase,omitempty"`
	Step     string `json:"step,omitempty"`
	Service  string `json:"service,omitempty"`
	Stream   string `json:"stream,omitempty"`
	Text     string `json:"text,omitempty"`
	Event    string `json:"event,omitempty"`
	Status   string `json:"status,omitempty"`
	ExitCode *int   `json:"exit_code,omitempty"`
	Message  string `json:"message,omitempty"`
}

type runLogsJSONReport struct {
	RunID      string        `json:"run_id"`
	Events     []runLogEntry `json:"events"`
	ErrorsOnly bool          `json:"errors_only,omitempty"`
	Lifecycle  bool          `json:"include_lifecycle,omitempty"`
	Tail       int           `json:"tail,omitempty"`
	Truncated  bool          `json:"truncated"`
}

type runLogsFailureSummaryReport struct {
	RunID          string                            `json:"run_id"`
	Status         string                            `json:"status"`
	Classification string                            `json:"classification,omitempty"`
	Confidence     float64                           `json:"confidence,omitempty"`
	Retryable      bool                              `json:"retryable"`
	AgentAction    string                            `json:"agent_action,omitempty"`
	FailureSummary string                            `json:"failure_summary,omitempty"`
	PrimaryFile    string                            `json:"primary_file,omitempty"`
	Evidence       []preflightapi.RunFailureEvidence `json:"evidence,omitempty"`
	Truncated      bool                              `json:"truncated"`
}

type runLogFilters struct {
	Phase   string
	Step    string
	Service string
}

type optionalStringFlag struct {
	value string
	set   bool
}

func (f *optionalStringFlag) String() string {
	return f.value
}

func (f *optionalStringFlag) Set(value string) error {
	f.value = value
	f.set = true
	return nil
}

func collectRunEvents(ctx context.Context, client *api.Client, runID string) ([]preflightapi.RunEvent, error) {
	resp, err := client.ListRunEvents(ctx, runID)
	return resp.Events, err
}

func collectRunEventsTail(ctx context.Context, client *api.Client, runID string, tail int, errorsOnly bool, includeLifecycle bool) (preflightapi.RunEventsResponse, error) {
	return client.ListRunEventsTail(ctx, runID, tail, errorsOnly, true, includeLifecycle)
}

func collectRunEventsForFailureSummary(ctx context.Context, client *api.Client, runID string) (preflightapi.RunEventsResponse, error) {
	resp, err := client.ListRunEvents(ctx, runID)
	if err == nil {
		return resp, nil
	}
	tail, tailErr := collectRunEventsTail(ctx, client, runID, preflightapi.MaxRunEventsTailLimit, true, false)
	if tailErr != nil {
		return preflightapi.RunEventsResponse{}, err
	}
	tail.Truncated = true
	return tail, nil
}

func runLogEntries(events []preflightapi.RunEvent, errorsOnly bool, filters runLogFilters) []runLogEntry {
	entries := make([]runLogEntry, 0, len(events))
	for index, event := range events {
		if !runEventMatchesLogFilters(event, filters) {
			continue
		}
		entry, ok := runLogEntryFromEvent(eventLogSeq(event, index), event)
		if !ok {
			continue
		}
		if errorsOnly && !isErrorLogEntry(entry) {
			continue
		}
		entries = append(entries, entry)
	}
	return entries
}

func runEventMatchesLogFilters(event preflightapi.RunEvent, filters runLogFilters) bool {
	phase := strings.TrimSpace(filters.Phase)
	step := strings.TrimSpace(filters.Step)
	service := strings.TrimSpace(filters.Service)
	if phase != "" && event.Phase != phase {
		return false
	}
	if step != "" && event.Step != step {
		return false
	}
	if service != "" && event.Service != service {
		return false
	}
	return true
}

func runLogEntryFromEvent(seq int, event preflightapi.RunEvent) (runLogEntry, bool) {
	entry := runLogEntry{
		EventID: event.ID,
		Seq:     seq,
		Time:    formatEventTime(event.Timestamp),
		Phase:   event.Phase,
		Step:    event.Step,
		Service: event.Service,
		Event:   event.Type,
		Status:  event.Status,
		Message: strings.TrimSpace(sanitizeLogDisplayText(event.Message)),
	}
	if event.ExitCode != nil {
		entry.ExitCode = event.ExitCode
	}
	switch event.Type {
	case preflightapi.EventStdout, preflightapi.EventStderr:
		data, err := event.DataBytes()
		if err != nil {
			entry.Message = err.Error()
			return entry, true
		}
		if event.Type == preflightapi.EventStdout {
			entry.Stream = "stdout"
		} else {
			entry.Stream = "stderr"
		}
		entry.Event = ""
		entry.Text = sanitizeLogDisplayText(string(data))
		return entry, true
	case preflightapi.EventLogTruncated:
		if entry.Message == "" {
			entry.Message = "preflight: run log limit reached; further log output was truncated"
		}
		return entry, true
	case preflightapi.EventCommandExited,
		preflightapi.EventRunFinished,
		preflightapi.EventRunFailed,
		preflightapi.EventRunCancelled,
		preflightapi.EventRunTimedOut:
		return entry, true
	default:
		return runLogEntry{}, false
	}
}

func sanitizeLogDisplayText(value string) string {
	if value == "" {
		return ""
	}
	var builder strings.Builder
	builder.Grow(len(value))
	for i := 0; i < len(value); {
		if value[i] == 0x1b {
			i = skipANSIEscape(value, i)
			continue
		}
		r, size := utf8.DecodeRuneInString(value[i:])
		if r == utf8.RuneError && size == 1 {
			switch value[i] {
			case 0x9b:
				i = skipCSISequence(value, i+1)
			case 0x90, 0x98, 0x9d, 0x9e, 0x9f:
				i = skipControlString(value, i+1)
			default:
				i += size
			}
			continue
		}
		switch r {
		case '\u009b':
			i = skipCSISequence(value, i+size)
			continue
		case '\u0090', '\u0098', '\u009d', '\u009e', '\u009f':
			i = skipControlString(value, i+size)
			continue
		}
		if r == '\n' || r == '\r' || r == '\t' || !unicode.IsControl(r) {
			builder.WriteRune(r)
		}
		i += size
	}
	return builder.String()
}

func skipANSIEscape(value string, start int) int {
	i := start + 1
	if i >= len(value) {
		return i
	}
	switch value[i] {
	case '[':
		return skipCSISequence(value, i+1)
	case ']', 'P', 'X', '^', '_':
		return skipControlString(value, i+1)
	default:
		return i + 1
	}
}

func skipCSISequence(value string, i int) int {
	for i < len(value) {
		b := value[i]
		i++
		if b >= 0x40 && b <= 0x7e {
			break
		}
	}
	return i
}

func skipControlString(value string, i int) int {
	for i < len(value) {
		if value[i] == 0x07 || value[i] == 0x9c {
			return i + 1
		}
		if value[i] == 0x1b && i+1 < len(value) && value[i+1] == '\\' {
			return i + 2
		}
		if value[i] == 0xc2 && i+1 < len(value) && value[i+1] == 0x9c {
			return i + 2
		}
		i++
	}
	return i
}

func eventLogSeq(event preflightapi.RunEvent, index int) int {
	if event.ID > 0 {
		return int(event.ID)
	}
	return index + 1
}

func isErrorLogEntry(entry runLogEntry) bool {
	if entry.Stream == "stderr" {
		return true
	}
	if entry.Stream == "stdout" && preflightapi.LogTextLooksDiagnostic(entry.Text) {
		return true
	}
	switch entry.Event {
	case preflightapi.EventLogTruncated, preflightapi.EventRunFailed, preflightapi.EventRunTimedOut, preflightapi.EventRunCancelled:
		return true
	case preflightapi.EventCommandExited:
		return entry.ExitCode != nil && *entry.ExitCode != 0
	default:
		return false
	}
}

func tailLogEntries(entries []runLogEntry, tail int) []runLogEntry {
	if tail <= 0 || len(entries) <= tail {
		return entries
	}
	return entries[len(entries)-tail:]
}

func eventsWereTruncated(events []preflightapi.RunEvent) bool {
	for _, event := range events {
		if event.Type == preflightapi.EventLogTruncated {
			return true
		}
	}
	return false
}
