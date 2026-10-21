package diagnosiscommon

import (
	"regexp"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/textlimit"
)

var browserAssetDownloadError = regexp.MustCompile(`(?i)^Error: Failed to download [^\r\n]{1,256}\(playwright [^)\r\n]{1,80}\)(?:, caused by)?$`)

type BrowserDownloadFailure struct {
	Summary             string
	Evidence            []preflightapi.RunFailureEvidence
	ReachabilityFailure bool
}

// BrowserDownloadFailureFromEvents requires an actual browser-installer error,
// not a package listing, submitted command, lifecycle message, or service log.
// Only the final observed execution scope can establish a failure: a later
// phase, step or invocation means an earlier download error was non-terminal.
// It preserves chunk boundaries within each output source and bounds inspection.
func BrowserDownloadFailureFromEvents(events []preflightapi.RunEvent) *BrowserDownloadFailure {
	start, end := finalCommandFailureScope(events)
	type source struct{ stream, phase, step string }
	type context struct{ phase, step string }
	type pendingLine struct {
		text    string
		discard bool
		where   preflightapi.RunFailureEvidence
	}
	pending := map[source]pendingLine{}
	var order []source
	transport := map[context]preflightapi.RunFailureEvidence{}
	var failure *BrowserDownloadFailure
	var failureContext context
	remaining := 1 << 20
	inspect := func(text string, key source, where preflightapi.RunFailureEvidence) {
		text = strings.TrimSpace(stripANSISGR(text))
		where.Text = textlimit.PrefixUTF8WithinBytes(text, 500)
		ctx := context{key.phase, key.step}
		if failure == nil && browserAssetDownloadError.MatchString(text) {
			failure = &BrowserDownloadFailure{Evidence: []preflightapi.RunFailureEvidence{where}}
			failureContext = ctx
		}
		lower := strings.ToLower(text)
		if strings.HasPrefix(lower, "error:") && RegistryTransportFailureText(lower) &&
			(HostnameMentioned(lower, "cdn.playwright.dev") || HostnameMentioned(lower, "playwright.download.prss.microsoft.com") || HostnameMentioned(lower, "playwright.azureedge.net")) {
			transport[ctx] = where
		}
	}
	for index := start; index < end; index++ {
		event := events[index]
		stream, log := EventStream(event)
		if !log || event.Service != "" || (event.Phase != "" && event.Phase != "command" && event.Phase != "prepare") {
			continue
		}
		key := source{stream, event.Phase, event.Step}
		line, known := pending[key]
		if !known {
			if len(pending) == 16 {
				continue
			}
			order = append(order, key)
		}
		data, err := event.DataBytes()
		if err != nil {
			pending[key] = pendingLine{discard: true}
			continue
		}
		if remaining == 0 {
			break
		}
		if len(data) > remaining {
			data = data[:remaining]
		}
		remaining -= len(data)
		parts := strings.Split(string(data), "\n")
		for number, part := range parts {
			if line.text == "" && !line.discard {
				line.where = preflightapi.RunFailureEvidence{EventID: event.ID, Seq: EventSequence(event, index), Stream: stream, Line: number + 1}
			}
			if len(line.text)+len(part) > 8192 {
				line = pendingLine{discard: true}
			} else if !line.discard {
				line.text += part
			}
			if number == len(parts)-1 {
				break
			}
			if !line.discard {
				inspect(line.text, key, line.where)
			}
			line = pendingLine{}
		}
		pending[key] = line
	}
	if remaining > 0 {
		for _, key := range order {
			if line := pending[key]; !line.discard {
				inspect(line.text, key, line.where)
			}
		}
	}
	if failure == nil {
		return nil
	}
	failure.Summary = textlimit.PrefixUTF8WithinBytes(failure.Evidence[0].Text, 240)
	if detail, ok := transport[failureContext]; ok {
		failure.Evidence = append(failure.Evidence, detail)
		first := textlimit.PrefixUTF8WithinBytes(failure.Evidence[0].Text, 160)
		failure.Summary = first + "\n" + textlimit.PrefixUTF8WithinBytes(detail.Text, 239-len(first))
		failure.ReachabilityFailure = RegistryEgressReachabilityFailureText(strings.ToLower(detail.Text))
	}
	return failure
}
