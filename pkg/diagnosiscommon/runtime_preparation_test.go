package diagnosiscommon

import (
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestRuntimePreparationFailurePreservesPrimaryFailureBeforeCleanup(t *testing.T) {
	code := 78
	marker := preflightapi.RunEvent{Type: preflightapi.EventStderr, Phase: "prepare", Data: preflightapi.RuntimePreparationFailureMarker + "\n"}
	for _, tc := range []struct {
		name  string
		extra []preflightapi.RunEvent
		want  bool
	}{
		{"successful always cleanup", []preflightapi.RunEvent{{Type: "stdout", Phase: "always", Data: "cleanup complete\n"}}, true},
		{"later repository command", []preflightapi.RunEvent{{Type: "stdout", Phase: "command", Data: "FAILED tests/test_api.py::test_runtime - AssertionError\n"}}, false},
		{"later silent command boundary", []preflightapi.RunEvent{{Type: "stdout", Phase: "command", Data: "\n"}}, false},
		{"later command then cleanup", []preflightapi.RunEvent{{Type: "stdout", Phase: "command", Data: "FAILED tests/test_api.py::test_runtime - AssertionError\n"}, {Type: "stdout", Phase: "always", Data: "cleanup complete\n"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := append([]preflightapi.RunEvent{marker}, tc.extra...)
			events = append(events, preflightapi.RunEvent{Type: preflightapi.EventCommandExited, ExitCode: &code})
			if got := RuntimePreparationFailed(events, &code); got != tc.want {
				t.Fatalf("runtime preparation failure = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestRuntimePreparationFailureRequiresItsDiagnosticRecord(t *testing.T) {
	code := 78
	otherCode := 1
	marker := preflightapi.RuntimePreparationFailureMarker
	for _, tc := range []struct {
		name  string
		event preflightapi.RunEvent
		want  bool
	}{
		{"preparation failure", preflightapi.RunEvent{Type: preflightapi.EventStderr, Phase: "prepare", Data: marker + "\n"}, true},
		{"no final newline", preflightapi.RunEvent{Type: preflightapi.EventStderr, Phase: "prepare", Data: marker}, true},
		{"stdout", preflightapi.RunEvent{Type: preflightapi.EventStdout, Phase: "prepare", Data: marker}, false},
		{"test output", preflightapi.RunEvent{Type: preflightapi.EventStderr, Phase: "command", Data: marker}, false},
		{"service output", preflightapi.RunEvent{Type: preflightapi.EventStderr, Phase: "prepare", Service: "postgres", Data: marker}, false},
		{"quoted diagnostic", preflightapi.RunEvent{Type: preflightapi.EventStderr, Phase: "prepare", Data: "expected " + marker}, false},
		{"modified diagnostic", preflightapi.RunEvent{Type: preflightapi.EventStderr, Phase: "prepare", Data: marker + " unexpected"}, false},
		{"successful preparation", preflightapi.RunEvent{Type: preflightapi.EventStderr, Phase: "prepare", Data: "readyci runtime preparation: Go 1.25.13 archive_sha256=abc\n"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := RuntimePreparationFailed([]preflightapi.RunEvent{tc.event}, &code); got != tc.want {
				t.Fatalf("diagnostic=%v want=%v", got, tc.want)
			}
			for _, other := range []*int{nil, new(int), &otherCode} {
				if RuntimePreparationFailed([]preflightapi.RunEvent{tc.event}, other) {
					t.Fatal("diagnostic replaced the execution exit code")
				}
			}
		})
	}
	for split := 1; split < len(marker); split++ {
		events := []preflightapi.RunEvent{
			{Type: preflightapi.EventStderr, Phase: "prepare", Data: marker[:split]},
			{Type: preflightapi.EventStderr, Phase: "prepare", Data: marker[split:] + "\n"},
		}
		if !RuntimePreparationFailed(events, &code) {
			t.Fatalf("lost diagnostic split at byte %d", split)
		}
		events[1].Step = "another-step"
		if RuntimePreparationFailed(events, &code) {
			t.Fatal("joined diagnostic across steps")
		}
	}
}
