package diagnosiscommon

import (
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestPrimaryFailureScopeUsesObservedOutcomeAcrossCleanupAndStreams(t *testing.T) {
	code := 1
	for _, phase := range []string{"prepare", "command", "post", "always"} {
		t.Run(phase, func(t *testing.T) {
			marker := preflightapi.RunEvent{RunID: "one", Type: preflightapi.EventStderr, Phase: phase, Step: "failed", ExitCode: &code, Message: preflightapi.CommandFailureScopeMessage}
			events := []preflightapi.RunEvent{
				{RunID: "one", Type: preflightapi.EventStderr, Phase: phase, Step: "failed", Data: "src/failure.go:7: undefined: missing\n"},
				marker,
				{RunID: "one", Type: preflightapi.EventStdout, Phase: "command", Step: "cleanup", Data: "cleanup completed\n"},
				// Delivery across streams can place failed stdout after cleanup.
				{RunID: "one", Type: preflightapi.EventStdout, Phase: phase, Step: "failed", Data: "failed stdout\n"},
				{RunID: "one", Type: preflightapi.EventCommandExited, ExitCode: &code},
			}
			selected := FinalCommandFailureEvents(events)
			text := DiagnosticSearchText(selected)
			if len(selected) != 3 || !strings.Contains(text, "src/failure.go") || !strings.Contains(text, "failed stdout") || strings.Contains(text, "cleanup") || strings.Contains(text, marker.Message) {
				t.Fatalf("primary scope or authored output lost: %+v text=%q", selected, text)
			}
		})
	}
}

func TestPrimaryFailureScopeRejectsConflictingAndPreviousExecutionMarkers(t *testing.T) {
	zero, one, two := 0, 1, 2
	marker := preflightapi.RunEvent{RunID: "one", Type: preflightapi.EventStderr, Phase: "command", Step: "failed", ExitCode: &one, Message: preflightapi.CommandFailureScopeMessage}
	for _, boundary := range []string{preflightapi.EventRunScheduled, preflightapi.EventVMStarting, preflightapi.EventCommandStarted} {
		events := []preflightapi.RunEvent{marker, {RunID: "one", Type: boundary}, {RunID: "one", Type: preflightapi.EventStdout, Phase: "command", Step: "current", Data: "current failure\n"}, {RunID: "one", Type: preflightapi.EventCommandExited, ExitCode: &one}}
		if selected, found := primaryCommandFailureEvents(events); found || len(selected) != 0 {
			t.Fatalf("%s retained previous execution marker: %+v", boundary, selected)
		}
	}
	for _, exitCode := range []*int{nil, &zero, &two} {
		selected, _ := primaryCommandFailureEvents([]preflightapi.RunEvent{marker, {RunID: "one", Type: preflightapi.EventCommandExited, ExitCode: exitCode}})
		if len(selected) != 0 {
			t.Fatalf("inconsistent global exit selected primary scope: %+v", selected)
		}
	}
	other := marker
	other.Step = "other"
	if selected, found := primaryCommandFailureEvents([]preflightapi.RunEvent{marker, other}); !found || len(selected) != 0 {
		t.Fatalf("conflicting primary scopes were guessed: %+v found=%t", selected, found)
	}
}
