package diagnosiscommon

import (
	"bytes"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

// DiagnosticLine retains the first event and line containing an emitted output
// line. A lifecycle message is already a complete record and has Message set.
type DiagnosticLine struct {
	EventIndex int
	Line       int
	Text       string
	Message    bool
}

// ForEachDiagnosticLine reconstructs log bytes before diagnostic filtering.
// Chunks may split skipped-test records, ANSI escapes and source paths. Never
// join different streams, services, steps, phases, runs or command invocations.
// Unfinished lines are bounded; missing bytes discard through the next newline.
func ForEachDiagnosticLine(events []preflightapi.RunEvent, visit func(DiagnosticLine)) {
	const maxLineBytes = 8192
	const maxSources = 64
	type source struct{ run, service string }
	type lineState struct {
		data       []byte
		eventIndex int
		line       int
		started    bool
		discard    bool
	}
	type outputState struct {
		phase, step string
		streams     [2]lineState
	}
	states := make(map[source]*outputState)
	var order []*outputState
	flushLine := func(line *lineState) {
		if line.started && !line.discard {
			visit(DiagnosticLine{EventIndex: line.eventIndex, Line: line.line, Text: string(line.data)})
		}
		*line = lineState{}
	}
	flushState := func(state *outputState) {
		// An unterminated stdout/stderr suffix still has its original event
		// location. Emit those suffixes in the order their records began.
		first, second := &state.streams[0], &state.streams[1]
		if second.started && (!first.started || second.eventIndex < first.eventIndex) {
			first, second = second, first
		}
		flushLine(first)
		flushLine(second)
	}
	flushAll := func() {
		for _, state := range order {
			flushState(state)
		}
		states = make(map[source]*outputState)
		order = nil
	}
	for index, event := range events {
		if primaryCommandFailureEvent(event) {
			continue // scope control metadata is not authored diagnostic text
		}
		switch event.Type {
		case preflightapi.EventRunScheduled, preflightapi.EventVMStarting,
			preflightapi.EventCommandStarted, preflightapi.EventCommandExited:
			// The supplied slice belongs to a run. A command boundary also
			// separates partial sidecar output from another invocation.
			if event.Service == "" {
				flushAll()
			} else if state := states[source{event.RunID, event.Service}]; state != nil {
				flushState(state)
			}
		}
		if event.Message != "" {
			visit(DiagnosticLine{EventIndex: index, Text: event.Message, Message: true})
		}
		stream, log := EventStream(event)
		if !log {
			continue
		}
		key := source{event.RunID, event.Service}
		state := states[key]
		if state == nil {
			if len(states) == maxSources {
				flushAll()
			}
			state = &outputState{phase: event.Phase, step: event.Step}
			states[key] = state
			order = append(order, state)
		} else if state.phase != event.Phase || state.step != event.Step {
			flushState(state)
			state.phase, state.step = event.Phase, event.Step
		}
		streamIndex := 0
		if stream == "stderr" {
			streamIndex = 1
		}
		line := &state.streams[streamIndex]
		data, err := event.DataBytes()
		if err != nil {
			*line = lineState{discard: true}
			continue
		}
		lineNumber := 1
		for len(data) != 0 {
			if !line.started {
				line.started, line.eventIndex, line.line = true, index, lineNumber
			}
			end := bytes.IndexByte(data, '\n')
			part := data
			if end >= 0 {
				part = data[:end]
			}
			if len(line.data)+len(part) > maxLineBytes {
				line.data, line.discard = nil, true
			} else if !line.discard {
				line.data = append(line.data, part...)
			}
			if end < 0 {
				break
			}
			flushLine(line)
			data = data[end+1:]
			lineNumber++
		}
	}
	flushAll()
}

// DiagnosticSearchText filters complete emitted records rather than treating
// transport chunk boundaries as authored newlines.
func DiagnosticSearchText(events []preflightapi.RunEvent) string {
	var text strings.Builder
	ForEachDiagnosticLine(events, func(line DiagnosticLine) {
		cleaned := FilterTestNonFailureLines(StripDiagnosticANSI(line.Text))
		if cleaned != "" {
			text.WriteString(cleaned)
			text.WriteByte('\n')
		}
	})
	return text.String()
}
