package diagnosiscommon

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestGoCrossInstallPrerequisiteRespectsChunkBoundaries(t *testing.T) {
	const diagnostic = "go: cannot install cross-compiled binaries when GOBIN is set"
	const output = "\x1b[31m" + diagnostic + "\x1b[0m\n"
	code := 1
	for split := 1; split < len(output); split++ {
		failure := EnvironmentPrerequisiteFailureFromEvents([]preflightapi.RunEvent{
			{ID: 1, Type: preflightapi.EventStderr, Phase: "command", Step: "build", Data: output[:split]},
			{ID: 2, Type: preflightapi.EventStdout, Phase: "command", Step: "build", Data: "Build for dragonfly\n"},
			{ID: 3, Type: preflightapi.EventStderr, Phase: "command", Step: "build", DataBase64: base64.StdEncoding.EncodeToString([]byte(output[split:]))},
		}, &code)
		if failure == nil || failure.Summary != diagnostic || failure.Evidence.Text != diagnostic || failure.Evidence.EventID != 3 || failure.Evidence.Path != "" {
			t.Fatalf("split %d failure = %+v", split, failure)
		}
	}
}

func TestGoCrossInstallPrerequisiteRequiresConcreteFailedOutput(t *testing.T) {
	const diagnostic = "go: cannot install cross-compiled binaries when GOBIN is set"
	code, success := 1, 0
	for _, tc := range []struct {
		name   string
		events []preflightapi.RunEvent
		exit   *int
	}{
		{"unknown exit", []preflightapi.RunEvent{{Type: preflightapi.EventStderr, Data: diagnostic}}, nil},
		{"successful exit", []preflightapi.RunEvent{{Type: preflightapi.EventStderr, Data: diagnostic}}, &success},
		{"quoted diagnostic", []preflightapi.RunEvent{{Type: preflightapi.EventStderr, Data: "expected: '" + diagnostic + "'\n"}}, &code},
		{"partial diagnostic", []preflightapi.RunEvent{{Type: preflightapi.EventStderr, Data: "go: cannot install cross-compiled binaries"}}, &code},
		{"different go error", []preflightapi.RunEvent{{Type: preflightapi.EventStderr, Data: "go: cannot install cross-compiled binaries: package build failed\n"}}, &code},
		{"service output", []preflightapi.RunEvent{{Type: preflightapi.EventStderr, Service: "helper", Data: diagnostic}}, &code},
		{"different streams", []preflightapi.RunEvent{{Type: preflightapi.EventStderr, Data: diagnostic[:20]}, {Type: preflightapi.EventStdout, Data: diagnostic[20:]}}, &code},
		{"different steps", []preflightapi.RunEvent{{Type: preflightapi.EventStderr, Phase: "command", Step: "first", Data: diagnostic[:20]}, {Type: preflightapi.EventStderr, Phase: "command", Step: "second", Data: diagnostic[20:]}}, &code},
		{"damaged bytes", []preflightapi.RunEvent{{Type: preflightapi.EventStderr, Data: diagnostic[:20]}, {Type: preflightapi.EventStderr, DataBase64: "!invalid"}, {Type: preflightapi.EventStderr, Data: diagnostic[20:]}}, &code},
		{"oversized line", []preflightapi.RunEvent{{Type: preflightapi.EventStderr, Data: strings.Repeat(" ", 8192) + diagnostic}}, &code},
		{"earlier successful scope", []preflightapi.RunEvent{
			{Type: preflightapi.EventStderr, Phase: "prepare", Data: diagnostic + "\n"},
			{Type: preflightapi.EventStderr, Phase: "command", Data: "src/main.go:42: undefined: missing\n"},
			{Type: preflightapi.EventCommandExited, ExitCode: &code},
		}, &code},
		{"cleanup after primary failure", []preflightapi.RunEvent{
			{Type: preflightapi.EventStderr, Phase: "command", Step: "build", Data: "src/main.go:42: undefined: missing\n"},
			{Type: preflightapi.EventStderr, Phase: "command", Step: "build", Message: preflightapi.CommandFailureScopeMessage, ExitCode: &code},
			{Type: preflightapi.EventStderr, Phase: "always", Data: diagnostic + "\n"},
			{Type: preflightapi.EventCommandExited, ExitCode: &code},
		}, &code},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if failure := EnvironmentPrerequisiteFailureFromEvents(tc.events, tc.exit); failure != nil {
				t.Fatalf("unproven Go prerequisite displaced the actual failure: %+v", failure)
			}
		})
	}
}

func TestEnvironmentPrerequisiteEvidenceRetainsBoundedCorroboration(t *testing.T) {
	cause := preflightapi.RunFailureEvidence{EventID: 11, Seq: 11, Text: "actual error"}
	pressure := preflightapi.RunFailureEvidence{EventID: 12, Seq: 12, Text: "matching filesystem capacity"}
	failure := EnvironmentPrerequisiteFailure{Evidence: cause, SupportingEvidence: []preflightapi.RunFailureEvidence{pressure}}
	ordinary := []preflightapi.RunFailureEvidence{cause, {EventID: 13, Seq: 13, Text: "ordinary diagnostic"}, pressure, {EventID: 14, Seq: 14, Text: "beyond limit"}}
	selected := failure.MergeEvidence(ordinary, 3)
	if len(selected) != 3 || selected[0] != cause || selected[1] != pressure || selected[2].EventID != 13 {
		t.Fatalf("cause/corroboration were displaced or duplicated: %+v", selected)
	}
	if ordinary[0] != cause || ordinary[len(ordinary)-1].EventID != 14 {
		t.Fatal("evidence selection changed its inputs")
	}
}

func TestMountPrerequisiteRespectsEveryChunkBoundary(t *testing.T) {
	const output = "\x1b[31mmount: /btrfs: unknown filesystem type 'btrfs'.\x1b[0m\n"
	code := 32
	for split := 1; split < len(output); split++ {
		failure := EnvironmentPrerequisiteFailureFromEvents([]preflightapi.RunEvent{
			{ID: 1, Type: preflightapi.EventStderr, Phase: "command", Data: output[:split]},
			{ID: 2, Type: preflightapi.EventStderr, Phase: "command", Data: output[split:]},
		}, &code)
		if failure == nil || !strings.Contains(failure.Summary, "unknown filesystem type 'btrfs'") || strings.Contains(failure.Summary, "\x1b") {
			t.Fatalf("split %d failure = %+v", split, failure)
		}
	}
}

func TestFinalCommandFailureEventsExcludeServiceEvidence(t *testing.T) {
	code := 1
	events := []preflightapi.RunEvent{
		{ID: 1, Type: preflightapi.EventStderr, Phase: "command", Data: "src/main.go:42: undefined: missing\n"},
		{ID: 2, Type: preflightapi.EventStderr, Phase: "command", Service: "helper", Data: "ModuleNotFoundError: No module named 'sidecar_plugin'\n"},
		{ID: 3, Type: preflightapi.EventCommandExited, ExitCode: &code},
	}
	scope := FinalCommandFailureEvents(events)
	if len(scope) != 1 || scope[0].ID != 1 || events[1].Service != "helper" {
		t.Fatalf("scope = %+v; source events must remain unchanged", scope)
	}
}
