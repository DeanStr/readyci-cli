package diagnosiscommon

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/artifactbundle"
	"github.com/DeanStr/readyci-cli/pkg/guestcontract"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

var artifactRetentionWarning = regexp.MustCompile(`^readyci: artifact collection retained (0|[1-9][0-9]*) of (0|[1-9][0-9]*) selected files; (0|[1-9][0-9]*) omitted; details in ` + regexp.QuoteMeta(artifactbundle.RetentionManifestPath) + `$`)

// FinalCommandFailureEvents returns the final observed repository output scope.
// Unknown phases, inconsistent exits and later invocations cannot establish an
// earlier scope as the failed one. Service events are excluded from the returned
// diagnostic scope as well as from selecting it.
func FinalCommandFailureEvents(events []preflightapi.RunEvent) []preflightapi.RunEvent {
	if selected, found := primaryCommandFailureEvents(events); found {
		return selected
	}
	start, end := finalCommandFailureScope(events)
	return WithoutServiceEvents(events[start:end])
}

func primaryCommandFailureEvent(event preflightapi.RunEvent) bool {
	return event.Type == preflightapi.EventStderr && event.Service == "" && event.Status == "" &&
		event.Message == preflightapi.CommandFailureScopeMessage && event.Data == "" && event.DataBase64 == "" &&
		event.ExitCode != nil && *event.ExitCode > 0 && *event.ExitCode <= 255 &&
		(event.Phase == "command" || event.Phase == "prepare" || event.Phase == "post" || event.Phase == "always")
}

func primaryCommandFailureEvents(events []preflightapi.RunEvent) ([]preflightapi.RunEvent, bool) {
	start, end, exit := commandInvocation(events)
	if exit != nil && (exit.ExitCode == nil || *exit.ExitCode == 0) {
		return nil, false
	}
	var primary *preflightapi.RunEvent
	for index := start; index < end; index++ {
		event := &events[index]
		if !primaryCommandFailureEvent(*event) {
			continue
		}
		if exit != nil && (*exit.ExitCode != *event.ExitCode || exit.RunID != "" && exit.RunID != event.RunID ||
			exit.Phase != "" && exit.Phase != event.Phase || exit.Step != "" && exit.Step != event.Step) {
			return nil, true // inconsistent records cannot authorize a scope
		}
		if primary != nil && (primary.RunID != event.RunID || primary.Phase != event.Phase || primary.Step != event.Step || *primary.ExitCode != *event.ExitCode) {
			return nil, true
		}
		primary = event
	}
	if primary == nil {
		return nil, false
	}
	var selected []preflightapi.RunEvent
	for _, event := range events[start:end] {
		if event.Service == "" && event.RunID == primary.RunID && event.Phase == primary.Phase && event.Step == primary.Step {
			selected = append(selected, event)
		}
	}
	return selected, true
}

// WithoutServiceEvents prevents a sidecar's diagnostics from identifying the
// repository command's cause or source file. Callers may retain the original
// events separately as supporting evidence.
func WithoutServiceEvents(events []preflightapi.RunEvent) []preflightapi.RunEvent {
	for index, event := range events {
		if event.Service != "" {
			selected := append([]preflightapi.RunEvent(nil), events[:index]...)
			for _, event := range events[index+1:] {
				if event.Service == "" {
					selected = append(selected, event)
				}
			}
			return selected
		}
	}
	return events
}

// Command exits do not always carry phase/step metadata. Use the last observed
// repository output scope inside the final invocation, and require consistency
// with any metadata the exit does supply. Earlier scopes remain evidence for
// inspection but cannot replace the cause of a later failed step.
func finalCommandFailureScope(events []preflightapi.RunEvent) (int, int) {
	return commandFailureScope(events, false)
}

func commandInvocation(events []preflightapi.RunEvent) (int, int, *preflightapi.RunEvent) {
	start, end, boundary := 0, len(events), 0
	var exit *preflightapi.RunEvent
	for index := range events {
		event := &events[index]
		if event.Service != "" {
			continue
		}
		switch event.Type {
		case preflightapi.EventRunScheduled, preflightapi.EventVMStarting, preflightapi.EventCommandStarted:
			start, end, boundary, exit = index+1, len(events), index+1, nil
		case preflightapi.EventCommandExited:
			start, end, boundary, exit = boundary, index, index+1, event
		}
	}
	return start, end, exit
}

func commandFailureScope(events []preflightapi.RunEvent, runtimePreparation bool) (int, int) {
	start, end, exit := commandInvocation(events)
	if exit != nil && (exit.ExitCode == nil || *exit.ExitCode == 0) {
		return 0, 0
	}
	type scope struct{ run, phase, step string }
	var latest scope
	seen := false
	for index := start; index < end; index++ {
		event := events[index]
		artifactWarning := PostRunArtifactWarning(event) && (!seen || event.RunID == "" || latest.run == "" || event.RunID == latest.run)
		if _, log := EventStream(event); !log || event.Service != "" || artifactWarning ||
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

// PostRunArtifactWarning recognizes the worker's exact, unscoped collection
// warnings emitted before command_exit. They remain supporting evidence, but
// do not establish another authored command scope. Tagged output, inconsistent
// counts, extra metadata and damaged/ambiguous bytes remain ordinary output.
func PostRunArtifactWarning(event preflightapi.RunEvent) bool {
	if event.Type != preflightapi.EventStderr || event.Phase != "" || event.Step != "" || event.Service != "" ||
		event.Message != "" || event.Status != "" || event.ExitCode != nil || event.Data != "" && event.DataBase64 != "" ||
		len(event.Data)+len(event.DataBase64) > 8192 {
		return false
	}
	data, err := event.DataBytes()
	if err != nil {
		return false
	}
	text := strings.TrimSuffix(string(data), "\n")
	if text == guestcontract.FailedWorkspaceArtifactSkippedMessage {
		return true
	}
	if match := artifactRetentionWarning.FindStringSubmatch(text); match != nil {
		var counts [3]uint64
		for index := range counts {
			count, err := strconv.ParseUint(match[index+1], 10, 32)
			if err != nil {
				return false
			}
			counts[index] = count
		}
		retained, matched, omitted := counts[0], counts[1], counts[2]
		return retained <= artifactbundle.MaxEntryCount && matched <= artifactbundle.MaxRetentionCandidates &&
			matched > retained && omitted == matched-retained
	}
	const prefix = "readyci: warning: artifact path "
	const suffix = " matched no files after the run"
	if !strings.HasPrefix(text, prefix) || !strings.HasSuffix(text, suffix) {
		return false
	}
	quoted := strings.TrimSuffix(strings.TrimPrefix(text, prefix), suffix)
	_, err = strconv.Unquote(quoted)
	return err == nil && strings.HasPrefix(quoted, `"`)
}
