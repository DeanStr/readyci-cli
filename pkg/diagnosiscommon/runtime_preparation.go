package diagnosiscommon

import "github.com/DeanStr/readyci-cli/pkg/preflightapi"

// RuntimePreparationFailed recognizes the worker's bounded diagnostic record
// from actual preparation stderr. Command text, service output, an exit code
// alone and successful SDK preparation cannot establish this failure.
func RuntimePreparationFailed(events []preflightapi.RunEvent, exitCode *int) bool {
	if exitCode == nil || *exitCode != 78 {
		return false
	}
	// The platform's fatal runtime marker is stronger than trailing cleanup
	// output. A later repository command or invocation still invalidates it.
	start, end := commandFailureScope(events, true)
	events = events[start:end]
	type lineState struct {
		text    string
		discard bool
	}
	pending := map[string]lineState{}
	remaining := 1 << 20
	for _, event := range events {
		stream, isLog := EventStream(event)
		if !isLog || stream != "stderr" || event.Phase != "prepare" || event.Service != "" {
			continue
		}
		if _, known := pending[event.Step]; !known && len(pending) >= 64 {
			continue
		}
		line := pending[event.Step]
		data, err := event.DataBytes()
		if err != nil {
			pending[event.Step] = lineState{discard: true}
			continue
		}
		for _, value := range data {
			if remaining == 0 {
				return false
			}
			remaining--
			if value == '\n' {
				if !line.discard && line.text == preflightapi.RuntimePreparationFailureMarker {
					return true
				}
				line = lineState{}
			} else if !line.discard {
				if len(line.text) >= len(preflightapi.RuntimePreparationFailureMarker) {
					line = lineState{discard: true}
				} else {
					line.text += string(value)
				}
			}
		}
		pending[event.Step] = line
	}
	for _, line := range pending {
		if !line.discard && line.text == preflightapi.RuntimePreparationFailureMarker {
			return true
		}
	}
	return false
}
