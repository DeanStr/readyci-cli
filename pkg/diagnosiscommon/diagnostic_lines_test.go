package diagnosiscommon

import (
	"reflect"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestDiagnosticSearchTextPreservesChunkedSkippedLinesAndANSI(t *testing.T) {
	const skipped = "\x1b[33mtest_winsound skipped -- No module named 'winsound'\x1b[0m\n"
	const compiler = "\x1b[31msrc/main.c:42: error: expected expression\x1b[0m\n"
	for _, output := range []string{skipped, compiler} {
		for split := 1; split < len(output); split++ {
			events := []preflightapi.RunEvent{
				{Type: preflightapi.EventStdout, Phase: "command", Data: output[:split]},
				{Type: preflightapi.EventStderr, Phase: "command", Data: "ordinary progress\n"},
				{Type: preflightapi.EventStdout, Phase: "command", Data: output[split:]},
			}
			text := DiagnosticSearchText(events)
			if strings.Contains(text, "winsound") || strings.Contains(text, "\x1b") {
				t.Fatalf("split %d: filtered diagnostic = %q", split, text)
			}
			if output == compiler && !strings.Contains(text, "src/main.c:42: error: expected expression") {
				t.Fatalf("split %d: lost compiler diagnostic: %q", split, text)
			}
		}
	}
}

func TestDiagnosticLinesRetainFirstByteLocation(t *testing.T) {
	events := []preflightapi.RunEvent{
		{ID: 10, Type: preflightapi.EventStdout, Phase: "command", Data: "progress\n\x1b[3"},
		{ID: 11, Type: preflightapi.EventStderr, Phase: "command", Data: "side output\n"},
		{ID: 12, Type: preflightapi.EventStdout, Phase: "command", Data: "1msrc/main.c:42: error\x1b[0m\nlast line"},
	}
	var got []DiagnosticLine
	ForEachDiagnosticLine(events, func(line DiagnosticLine) { got = append(got, line) })
	want := []DiagnosticLine{
		{EventIndex: 0, Line: 1, Text: "progress"},
		{EventIndex: 1, Line: 1, Text: "side output"},
		{EventIndex: 0, Line: 2, Text: "\x1b[31msrc/main.c:42: error\x1b[0m"},
		{EventIndex: 2, Line: 2, Text: "last line"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lines = %+v, want %+v", got, want)
	}
}

func TestDiagnosticLinesNeverJoinDifferentSourcesOrInvocations(t *testing.T) {
	prefix := preflightapi.RunEvent{RunID: "one", Type: preflightapi.EventStdout, Phase: "command", Step: "test", Data: "test_winsound skipped -- "}
	suffix := prefix
	suffix.Data = "No module named 'winsound'\n"
	for _, name := range []string{"stream", "phase", "step", "service", "run", "invocation", "service invocation", "malformed bytes"} {
		t.Run(name, func(t *testing.T) {
			events := []preflightapi.RunEvent{prefix, suffix}
			switch name {
			case "stream":
				events[1].Type = preflightapi.EventStderr
			case "phase":
				events[1].Phase = "prepare"
			case "step":
				events[1].Step = "other"
			case "service":
				events[1].Service = "helper"
			case "run":
				events[1].RunID = "two"
			case "invocation":
				events = []preflightapi.RunEvent{prefix, {Type: preflightapi.EventCommandStarted}, suffix}
			case "service invocation":
				first, last := prefix, suffix
				first.Service, last.Service = "helper", "helper"
				events = []preflightapi.RunEvent{first, {RunID: first.RunID, Service: first.Service, Type: preflightapi.EventCommandStarted}, last}
			case "malformed bytes":
				broken := prefix
				broken.Data, broken.DataBase64 = "", "!malformed"
				events = []preflightapi.RunEvent{prefix, broken, suffix}
			}
			var lines []DiagnosticLine
			ForEachDiagnosticLine(events, func(line DiagnosticLine) { lines = append(lines, line) })
			for _, line := range lines {
				if strings.Contains(line.Text, "skipped -- No module") {
					t.Fatalf("different output sources combined: %+v", lines)
				}
			}
		})
	}
}

func TestDiagnosticLinesBoundUnfinishedRecordsAndRecover(t *testing.T) {
	events := []preflightapi.RunEvent{
		{Type: preflightapi.EventStderr, Data: strings.Repeat("x", 8192)},
		{Type: preflightapi.EventStderr, Data: "hidden diagnostic\nModuleNotFoundError: No module named 'required'\n"},
	}
	text := DiagnosticSearchText(events)
	if strings.Contains(text, "hidden") || !strings.Contains(text, "ModuleNotFoundError: No module named 'required'") {
		t.Fatalf("overlong line was accepted or next complete line lost: %q", text)
	}
}
