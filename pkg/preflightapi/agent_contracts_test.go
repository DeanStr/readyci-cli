package preflightapi

import "testing"

func TestAgentIdempotencyContracts(t *testing.T) {
	if got := AgentRetryIdempotencyKey(" task-1:GO=1 ", 2); got != "task-1:GO=1:__readyci_retry=2" {
		t.Fatalf("retry idempotency key = %q, want retry child key", got)
	}
	if got := AgentRetryIdempotencyKey("task-1", 1); got != "task-1" {
		t.Fatalf("first attempt idempotency key = %q, want base key", got)
	}
	cases := []struct {
		name string
		key  string
		want string
	}{
		{name: "base", key: "task-1", want: ""},
		{name: "matrix", key: "task-1:GO=1", want: "GO=1"},
		{name: "matrix retry", key: "task-1:GO=1:__readyci_retry=2", want: "GO=1"},
		{name: "retry named matrix", key: "task-1:retry=1", want: "retry=1"},
		{name: "malformed retry named matrix", key: "task-1:__readyci_retry=abc", want: "__readyci_retry=abc"},
		{name: "first retry marker is not generated", key: "task-1:__readyci_retry=1", want: "__readyci_retry=1"},
		{name: "other key", key: "task-2:GO=1", want: "task-2:GO=1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := AgentIdempotencyGroup("task-1", tc.key); got != tc.want {
				t.Fatalf("group = %q, want %q", got, tc.want)
			}
		})
	}
	if !AgentIdempotencyKeyMatches("task-1:GO=1", "task-1") {
		t.Fatal("matrix child key did not match base idempotency key")
	}
	if AgentIdempotencyKeyMatches("task-10:GO=1", "task-1") {
		t.Fatal("prefix-similar idempotency key matched base key")
	}
}

func TestSplitCheckAliasVariant(t *testing.T) {
	check, variant := SplitCheckAliasVariant("touched [GO=1,NODE=22]")
	if check != "touched" || variant != "GO=1,NODE=22" {
		t.Fatalf("split alias = %q/%q, want touched matrix variant", check, variant)
	}
	check, variant = SplitCheckAliasVariant("go")
	if check != "go" || variant != "" {
		t.Fatalf("plain alias = %q/%q, want go with no variant", check, variant)
	}
}

func TestIsTerminalRunStatus(t *testing.T) {
	for _, status := range []string{StatusSucceeded, StatusFailed, StatusTimedOut, StatusCancelled, StatusUploadExpired, StatusBlocked, StatusDenied} {
		if !IsTerminalRunStatus(status) {
			t.Fatalf("%s should be terminal", status)
		}
	}
	for _, status := range []string{StatusAwaitingUpload, StatusQueued, StatusScheduled, StatusStartingVM, StatusRunning, ""} {
		if IsTerminalRunStatus(status) {
			t.Fatalf("%s should not be terminal", status)
		}
	}
}

func TestRunEventTailPredicates(t *testing.T) {
	failedExit := 1
	successExit := 0
	logCandidates := []RunEvent{
		{Type: EventStdout},
		{Type: EventStderr},
		{Type: EventLogTruncated},
		{Type: EventRunFailed},
		{Type: EventRunTimedOut},
		{Type: EventRunCancelled},
		{Type: EventCommandExited, ExitCode: &failedExit},
	}
	for _, event := range logCandidates {
		if !IsRunEventLogTailCandidate(event) {
			t.Fatalf("%s should be a log-tail candidate", event.Type)
		}
	}
	if IsRunEventLogTailCandidate(RunEvent{Type: EventRunFinished}) {
		t.Fatal("run_finished should not crowd out displayable log tail entries")
	}
	if IsRunEventErrorTailCandidate(RunEvent{Type: EventCommandExited, ExitCode: &successExit}) {
		t.Fatal("successful command exit should not be an error-tail candidate")
	}
	if IsRunEventLogTailCandidate(RunEvent{Type: EventCommandExited, ExitCode: &successExit}) {
		t.Fatal("successful command exit should not crowd out displayable log tail entries")
	}
	if !IsRunEventLifecycleTailCandidate(RunEvent{Type: EventCommandExited, ExitCode: &successExit}) {
		t.Fatal("successful command exit should be available when lifecycle tailing is requested")
	}
	if !IsRunEventLifecycleTailCandidate(RunEvent{Type: EventRunFinished}) {
		t.Fatal("run_finished should be available when lifecycle tailing is requested")
	}
	if !IsRunEventErrorTailCandidate(RunEvent{Type: EventCommandExited, ExitCode: &failedExit}) {
		t.Fatal("failing command exit should be an error-tail candidate")
	}
	if !IsRunEventErrorTailCandidate(RunEvent{Type: EventStderr}) {
		t.Fatal("stderr should be an error-tail candidate")
	}
	if !IsRunEventErrorTailCandidate(RunEvent{Type: EventStdout, Data: "ModuleNotFoundError: No module named 'freezegun'\n"}) {
		t.Fatal("diagnostic stdout should be an error-tail candidate")
	}
	if !IsRunEventErrorTailCandidate(RunEvent{Type: EventStdout, Data: "........................................ [100%]\n=================================== FAILURES ===================================\n"}) {
		t.Fatal("pytest failure stdout should be an error-tail candidate")
	}
	if IsRunEventErrorTailCandidate(RunEvent{Type: EventStdout, Data: "ordinary progress output\n"}) {
		t.Fatal("ordinary stdout should not be an error-tail candidate")
	}
}
