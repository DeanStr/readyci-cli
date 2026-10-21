package diagnosiscommon

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

const browserFailureRecord = "Error: Failed to download Chrome for Testing 151.0.7922.34 (playwright chromium v1234), caused by"

func TestBrowserDownloadRequiresFinalExecutionScope(t *testing.T) {
	zero, one := 0, 1
	for _, tc := range []struct {
		name   string
		events []preflightapi.RunEvent
		want   bool
	}{
		{"later command phase", []preflightapi.RunEvent{
			{Type: "stderr", Phase: "prepare", Data: browserFailureRecord + "\n"},
			{Type: "stderr", Phase: "command", Data: "src/app.js:3:1 error Unexpected console statement no-console\n"},
		}, false},
		{"later command step", []preflightapi.RunEvent{
			{Type: "stderr", Phase: "command", Step: "browser", Data: browserFailureRecord + "\n"},
			{Type: "stderr", Phase: "command", Step: "lint", Data: "src/app.js:3:1 error Unexpected console statement no-console\n"},
		}, false},
		{"post phase", []preflightapi.RunEvent{
			{Type: "stderr", Phase: "command", Data: browserFailureRecord + "\n"},
			{Type: "stderr", Phase: "post", Data: "publish report failed\n"},
		}, false},
		{"successful command", []preflightapi.RunEvent{
			{Type: "stderr", Data: browserFailureRecord + "\n"},
			{Type: preflightapi.EventCommandExited, ExitCode: &zero},
		}, false},
		{"later invocation", []preflightapi.RunEvent{
			{Type: "stderr", Data: browserFailureRecord + "\n"},
			{Type: preflightapi.EventCommandExited, ExitCode: &zero},
			{Type: preflightapi.EventCommandStarted},
			{Type: "stderr", Data: "lint failed\n"},
			{Type: preflightapi.EventCommandExited, ExitCode: &one},
		}, false},
		{"different exit step", []preflightapi.RunEvent{
			{Type: "stderr", Phase: "command", Step: "browser", Data: browserFailureRecord + "\n"},
			{Type: preflightapi.EventCommandExited, Phase: "command", Step: "lint", ExitCode: &one},
		}, false},
		{"terminal browser failure", []preflightapi.RunEvent{
			{Type: "stdout", Phase: "prepare", Data: "dependencies installed\n"},
			{Type: "stderr", Phase: "command", Step: "browser", Data: browserFailureRecord + "\n"},
			{Type: preflightapi.EventCommandExited, ExitCode: &one},
		}, true},
		{"terminal prepare failure", []preflightapi.RunEvent{
			{Type: "stderr", Phase: "prepare", Data: browserFailureRecord + "\n"},
			{Type: preflightapi.EventCommandExited, ExitCode: &one},
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := BrowserDownloadFailureFromEvents(tc.events); (got != nil) != tc.want {
				t.Fatalf("failure=%+v, want presence=%t", got, tc.want)
			}
		})
	}
}

func TestBrowserDownloadRequiresActualBoundedInstallerOutput(t *testing.T) {
	for _, tc := range []struct {
		name   string
		events []preflightapi.RunEvent
		want   bool
	}{
		{"command output", []preflightapi.RunEvent{{Type: "stdout", Phase: "command", Data: browserFailureRecord + "\n"}}, true},
		{"prepare output", []preflightapi.RunEvent{{Type: "stderr", Phase: "prepare", Data: browserFailureRecord}}, true},
		{"message only", []preflightapi.RunEvent{{Type: "stdout", Message: browserFailureRecord}}, false},
		{"lifecycle", []preflightapi.RunEvent{{Type: "run_failed", Data: browserFailureRecord, Message: browserFailureRecord}}, false},
		{"service", []preflightapi.RunEvent{{Type: "stderr", Service: "db", Data: browserFailureRecord}}, false},
		{"package listing", []preflightapi.RunEvent{{Type: "stdout", Data: "+ playwright\n+ eslint\n"}}, false},
		{"download start", []preflightapi.RunEvent{{Type: "stdout", Data: "Downloading Chromium from https://cdn.playwright.dev\n"}}, false},
		{"DNS only", []preflightapi.RunEvent{{Type: "stderr", Data: "Error: getaddrinfo ENOTFOUND cdn.playwright.dev\n"}}, false},
		{"quoted fixture", []preflightapi.RunEvent{{Type: "stdout", Data: "expected '" + browserFailureRecord + "', received ''\n"}}, false},
		{"malformed bytes", []preflightapi.RunEvent{{Type: "stdout", Data: browserFailureRecord, DataBase64: "bad base64"}}, false},
		{"oversized line", []preflightapi.RunEvent{{Type: "stdout", Data: strings.Repeat("x", 8192) + browserFailureRecord}}, false},
		{"oversized line then complete error", []preflightapi.RunEvent{{Type: "stdout", Data: strings.Repeat("x", 8192) + "\n" + browserFailureRecord + "\n"}}, true},
		{"inspection byte limit", []preflightapi.RunEvent{{Type: "stdout", Data: strings.Repeat("x", 1<<20)}, {Type: "stdout", Data: browserFailureRecord}}, false},
		{"partial at byte limit", []preflightapi.RunEvent{{Type: "stdout", Data: strings.Repeat("x", (1<<20)-40) + "\n" + browserFailureRecord}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := BrowserDownloadFailureFromEvents(tc.events); (got != nil) != tc.want {
				t.Fatalf("failure=%+v, want presence=%t", got, tc.want)
			}
		})
	}
}

func TestBrowserDownloadPreservesChunkSourcesAndEvidenceLocation(t *testing.T) {
	first, second := browserFailureRecord[:40], browserFailureRecord[40:]
	start := preflightapi.RunEvent{ID: 15, Type: "stdout", Phase: "command", Step: "browser", Data: first}
	end := preflightapi.RunEvent{ID: 16, Type: "stdout", Phase: "command", Step: "browser", DataBase64: base64.StdEncoding.EncodeToString([]byte(second + "\n"))}
	result := BrowserDownloadFailureFromEvents([]preflightapi.RunEvent{start, end})
	if result == nil || result.Evidence[0].EventID != 15 || result.Evidence[0].Line != 1 || result.Evidence[0].Stream != "stdout" {
		t.Fatalf("split output evidence=%+v", result)
	}
	for _, change := range []func(*preflightapi.RunEvent){
		func(event *preflightapi.RunEvent) { event.Type = "stderr" },
		func(event *preflightapi.RunEvent) { event.Phase = "prepare" },
		func(event *preflightapi.RunEvent) { event.Step = "other" },
		func(event *preflightapi.RunEvent) { event.Service = "database" },
	} {
		other := end
		change(&other)
		if BrowserDownloadFailureFromEvents([]preflightapi.RunEvent{start, other}) != nil {
			t.Fatal("invented a failure across output sources")
		}
	}
	invalid := preflightapi.RunEvent{Type: "stdout", Phase: "command", Step: "browser", DataBase64: "bad base64"}
	if BrowserDownloadFailureFromEvents([]preflightapi.RunEvent{start, invalid, end}) != nil {
		t.Fatal("joined a diagnostic across missing bytes")
	}
}

func TestBrowserDownloadFinalScopeHasIndependentInspectionBudget(t *testing.T) {
	var events []preflightapi.RunEvent
	for number := 0; number < 16; number++ {
		events = append(events, preflightapi.RunEvent{Type: "stdout", Step: strings.Repeat("x", number), Data: "a\n"})
	}
	events = append(events, preflightapi.RunEvent{Type: "stdout", Step: "new-source", Data: browserFailureRecord + "\n"})
	if BrowserDownloadFailureFromEvents(events) == nil {
		t.Fatal("earlier execution scopes exhausted the final step's inspection budget")
	}
}

func TestBrowserDownloadTransportDetailDoesNotProvePolicyDenial(t *testing.T) {
	events := []preflightapi.RunEvent{
		{ID: 1, Type: "stderr", Phase: "command", Step: "browser", Data: "Error: getaddrinfo ENOTFOUND cdn.playwright.dev\n"},
		{ID: 2, Type: "stdout", Phase: "command", Step: "browser", Data: browserFailureRecord + "\n"},
	}
	failure := BrowserDownloadFailureFromEvents(events)
	if failure == nil || !failure.ReachabilityFailure || !strings.Contains(failure.Summary, "cdn.playwright.dev") || len(failure.Evidence) != 2 || len(failure.Summary) > 240 {
		t.Fatalf("failure=%+v", failure)
	}
	events[0].Step = "other"
	failure = BrowserDownloadFailureFromEvents(events)
	if failure == nil || failure.ReachabilityFailure || strings.Contains(failure.Summary, "ENOTFOUND") {
		t.Fatalf("unrelated transport error was associated: %+v", failure)
	}
}
