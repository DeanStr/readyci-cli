package preflightapi

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestRuntimeDiagnosticsRemainDiagnosticAndRespectAttemptBoundaries(t *testing.T) {
	input := RuntimeDiagnostics{SchemaVersion: RuntimeDiagnosticsSchema, InstanceID: "run-instance", Trigger: "unexpected_runtime_failure",
		Logs: map[string]RuntimeDiagnosticLog{"serial_stdout": {DataBase64: base64.StdEncoding.EncodeToString([]byte{0xff, 'x'}), RetainedBytes: 2}},
	}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	event := RunEvent{Type: EventRuntimeDiagnostics, Data: string(data)}
	result := RuntimeDiagnosticsFromEvents([]RunEvent{event})
	if result == nil || result.InstanceID != input.InstanceID {
		t.Fatalf("runtime diagnostics were not retained: %+v", result)
	}
	bytes, err := result.Logs["serial_stdout"].DataBytes()
	if err != nil || len(bytes) != 2 || bytes[0] != 0xff {
		t.Fatalf("non-UTF-8 diagnostic bytes changed: %x %v", bytes, err)
	}
	if !IsRunEventLifecycleTailCandidate(event) || IsRunEventLogTailCandidate(event) || IsRunEventErrorTailCandidate(event) {
		t.Fatal("runtime diagnostics must be visible as lifecycle data, separate from authored output")
	}
	for _, boundary := range []string{EventVMStarting, EventRunScheduled} {
		if result := RuntimeDiagnosticsFromEvents([]RunEvent{event, {Type: boundary}}); result != nil {
			t.Fatal("previous attempt diagnostics crossed the attempt boundary")
		}
	}
	if result := RuntimeDiagnosticsFromEvents([]RunEvent{{Type: EventRuntimeDiagnostics, Data: `{"schema_version":"unknown"}`}}); result != nil {
		t.Fatal("unknown diagnostics schema was accepted")
	}
}
