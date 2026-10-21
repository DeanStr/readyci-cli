package diagnosiscommon

import "github.com/DeanStr/readyci-cli/pkg/preflightapi"

// Command exits do not always carry phase/step metadata. Use the last observed
// repository output scope inside the final invocation, and require consistency
// with any metadata the exit does supply. Earlier scopes remain evidence for
// inspection but cannot replace the cause of a later failed step.
func finalCommandFailureScope(events []preflightapi.RunEvent) (int, int) {
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
		if _, log := EventStream(event); !log || event.Service != "" || (event.Data == "" && event.DataBase64 == "") {
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
