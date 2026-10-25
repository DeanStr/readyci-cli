package diagnosiscommon

import (
	"regexp"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/textlimit"
)

var preparationHTTPFailure = regexp.MustCompile(`(?i)\b(?:HTTP(?:/[0-9.]+)?(?: error)?[ :=]+[45][0-9]{2}|(?:status(?: code)?|URL returned error)[ :=]+[45][0-9]{2}|[45][0-9]{2}\s+(?:service unavailable|bad gateway|gateway timeout|not found|forbidden|unauthorized|too many requests|internal server error))\b`)
var preparationFetchOperation = regexp.MustCompile(`(?i)(?:\b(?:failed to (?:fetch|download|pull)|could not (?:fetch|download)|downloading|fetching|pulling|curl|wget)\b|\bErr:[0-9]+\s+https?://)`)

// PreparationFailure records observed preparation facts. Callers retain their
// classification policy and authoritative terminal/worker failure precedence.
type PreparationFailure struct {
	Diagnostic          string
	Evidence            []preflightapi.RunFailureEvidence
	DownloadFailure     bool
	ReachabilityFailure bool
	Output              string
}

// PreparationFailureFromEvents requires a nonzero exit and the final actual
// output scope to be preparation. A submitted wrapper, service log or stale
// preparation warning cannot establish this fact. Download evidence must be an
// emitted fetch operation plus HTTP/transport failure, without hostname rules.
// Inspection is bounded and fragments never cross streams or damaged bytes.
func PreparationFailureFromEvents(events []preflightapi.RunEvent, exitCode *int) *PreparationFailure {
	if exitCode == nil || *exitCode == 0 {
		return nil
	}
	_, _, exit := commandInvocation(events)
	if exit != nil && (exit.ExitCode == nil || *exit.ExitCode != *exitCode) {
		return nil
	}
	events = FinalCommandFailureEvents(events)
	if len(events) == 0 || events[0].Phase != "prepare" {
		return nil
	}
	failure := &PreparationFailure{}
	var output strings.Builder
	type lineState struct {
		text, previous string
		discard        bool
		where          preflightapi.RunFailureEvidence
	}
	pending := map[string]lineState{}
	var order []string
	var lastDiagnostic preflightapi.RunFailureEvidence
	var download preflightapi.RunFailureEvidence
	inspect := func(text string, previous string, where preflightapi.RunFailureEvidence) {
		text = strings.TrimSpace(stripANSISGR(text))
		if text == "" || DefiniteTestNonFailureLine(text) {
			return
		}
		output.WriteString(text)
		output.WriteByte('\n')
		where.Text = textlimit.PrefixUTF8WithinBytes(text, 500)
		lower := strings.ToLower(text)
		if ContainsAny(lower, "error", "failed", "cannot", "not found", "no such file", "unavailable", "denied", "missing") {
			lastDiagnostic = where
		}
		window := previous + "\n" + text
		transport := RegistryTransportFailureText(lower)
		fetch := preparationFetchOperation.MatchString(window) || ContainsAny(strings.ToLower(window),
			"npm err!", "npm error", "err_pnpm", "warning: retrying after connection broken", "spurious network error", "reading http", "get \"http", "request to http")
		if (preparationHTTPFailure.MatchString(text) || transport) && fetch {
			download = where
			failure.ReachabilityFailure = RegistryEgressReachabilityFailureText(lower)
		}
	}
	finish := func() *PreparationFailure {
		failure.Output = output.String()
		if download.Text != "" {
			failure.DownloadFailure = true
			failure.Diagnostic = textlimit.PrefixUTF8WithinBytes(download.Text, 180)
			failure.Evidence = []preflightapi.RunFailureEvidence{download}
		} else if lastDiagnostic.Text != "" {
			failure.Diagnostic = textlimit.PrefixUTF8WithinBytes(lastDiagnostic.Text, 180)
			failure.Evidence = []preflightapi.RunFailureEvidence{lastDiagnostic}
		} else {
			// An earlier successful preparation message does not establish
			// where a later silent command failed in an incomplete log stream.
			return nil
		}
		return failure
	}
	remaining := 1 << 20
	for index, event := range events {
		stream, log := EventStream(event)
		if !log || event.Service != "" || PostRunArtifactWarning(event) {
			continue
		}
		line, known := pending[stream]
		if !known {
			order = append(order, stream)
		}
		// Avoid decoding unbounded base64 merely to discover the inspection cap.
		if len(event.DataBase64) > (remaining+2)/3*4 {
			return finish()
		}
		data, err := event.DataBytes()
		if err != nil {
			pending[stream] = lineState{discard: true}
			continue
		}
		if len(data) > remaining {
			return finish()
		}
		remaining -= len(data)
		parts := strings.Split(string(data), "\n")
		for number, part := range parts {
			if line.text == "" && !line.discard {
				line.where = preflightapi.RunFailureEvidence{EventID: event.ID, Seq: EventSequence(event, index), Stream: stream, Line: number + 1}
			}
			if len(line.text)+len(part) > 8192 {
				line = lineState{discard: true}
			} else if !line.discard {
				line.text += part
			}
			// The final split part is an unfinished line, including an empty
			// suffix after a newline. Do not join it to another output source.
			if number == len(parts)-1 {
				break
			}
			if !line.discard {
				inspect(line.text, line.previous, line.where)
				line = lineState{previous: textlimit.PrefixUTF8WithinBytes(line.text, 8192)}
			} else {
				line = lineState{}
			}
		}
		pending[stream] = line
	}
	for _, stream := range order {
		line := pending[stream]
		if !line.discard && line.text != "" {
			inspect(line.text, line.previous, line.where)
		}
	}
	return finish()
}
