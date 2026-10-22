package diagnosiscommon

import (
	"strconv"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

// FinalCommandFailureEvents returns the final observed repository output scope.
// Unknown phases, inconsistent exits and later invocations cannot establish an
// earlier scope as the failed one. Service output never establishes this scope.
func FinalCommandFailureEvents(events []preflightapi.RunEvent) []preflightapi.RunEvent {
	start, end := finalCommandFailureScope(events)
	return events[start:end]
}

// Command exits do not always carry phase/step metadata. Use the last observed
// repository output scope inside the final invocation, and require consistency
// with any metadata the exit does supply. Earlier scopes remain evidence for
// inspection but cannot replace the cause of a later failed step.
func finalCommandFailureScope(events []preflightapi.RunEvent) (int, int) {
	return commandFailureScope(events, false)
}

func commandFailureScope(events []preflightapi.RunEvent, runtimePreparation bool) (int, int) {
	start, end, boundary := 0, len(events), 0
	var exit *preflightapi.RunEvent
	for index := range events {
		event := &events[index]
		if event.Service != "" {
			continue
		}
		switch event.Type {
		case preflightapi.EventCommandStarted:
			start, end, boundary, exit = index+1, len(events), index+1, nil
		case preflightapi.EventCommandExited:
			start, end, boundary, exit = boundary, index, index+1, event
		}
	}
	if exit != nil && (exit.ExitCode == nil || *exit.ExitCode == 0) {
		return 0, 0
	}
	type scope struct{ run, phase, step string }
	var latest scope
	seen := false
	for index := start; index < end; index++ {
		event := events[index]
		if _, log := EventStream(event); !log || event.Service != "" || postRunArtifactWarning(event) ||
			(runtimePreparation && event.Phase == "always") || finalizerBoundaryWhitespace(event) || (event.Data == "" && event.DataBase64 == "") {
			continue
		}
		current := scope{event.RunID, event.Phase, event.Step}
		if !seen || current != latest {
			start, latest, seen = index, current, true
		}
	}
	if !seen || (latest.phase != "" && latest.phase != "command" && latest.phase != "prepare") {
		return 0, 0
	}
	if exit != nil && ((exit.RunID != "" && exit.RunID != latest.run) ||
		(exit.Phase != "" && exit.Phase != latest.phase) || (exit.Step != "" && exit.Step != latest.step)) {
		return 0, 0
	}
	return start, end
}

// The worker enters finalization even after preparation fails. A marker-only
// newline is not another executed command; its emission depends on chunking.
// A command-phase newline, a named always step or substantive cleanup output
// remains an execution boundary for ordinary preparation heuristics.
func finalizerBoundaryWhitespace(event preflightapi.RunEvent) bool {
	if event.Phase != "always" || event.Step != "" || len(event.Data)+len(event.DataBase64) > 64 {
		return false
	}
	data, err := event.DataBytes()
	return err == nil && len(data) > 0 && strings.TrimSpace(string(data)) == ""
}

// The worker emits this exact, unscoped warning after command output and before
// the command-exit event. It describes artifact collection, not another command
// scope. Tagged customer output, arbitrary warnings and damaged bytes remain
// ordinary output; they cannot be skipped to resurrect an earlier failure.
func postRunArtifactWarning(event preflightapi.RunEvent) bool {
	if event.Type != preflightapi.EventStderr || event.Phase != "" || event.Step != "" || event.Service != "" || len(event.Data)+len(event.DataBase64) > 8192 {
		return false
	}
	data, err := event.DataBytes()
	if err != nil {
		return false
	}
	text := strings.TrimSuffix(string(data), "\n")
	const prefix = "readyci: warning: artifact path "
	const suffix = " matched no files after the run"
	if !strings.HasPrefix(text, prefix) || !strings.HasSuffix(text, suffix) {
		return false
	}
	quoted := strings.TrimSuffix(strings.TrimPrefix(text, prefix), suffix)
	_, err = strconv.Unquote(quoted)
	return err == nil && strings.HasPrefix(quoted, `"`)
}
