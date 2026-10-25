package diagnosiscommon

import (
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

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
