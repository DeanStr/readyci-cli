package preflightapi

import (
	"encoding/base64"
	"encoding/json"
	"time"
)

const RuntimeDiagnosticsSchema = "readyci.runtime_diagnostics.v1"

// RuntimeDiagnostics describes one failed runtime instance. It is diagnostic
// output, not a guest command result or runner execution evidence.
type RuntimeDiagnostics struct {
	SchemaVersion string                          `json:"schema_version"`
	InstanceID    string                          `json:"instance_id"`
	Trigger       string                          `json:"trigger"`
	CapturedAt    time.Time                       `json:"captured_at"`
	Launcher      RuntimeDiagnosticProcess        `json:"launcher"`
	Logs          map[string]RuntimeDiagnosticLog `json:"logs"`
	VMResources   RunResources                    `json:"vm_resources"`
	Limitations   []string                        `json:"limitations"`
}

type RuntimeDiagnosticProcess struct {
	Scope                  string `json:"scope"`
	Exited                 bool   `json:"exited"`
	ObservedBeforeTeardown bool   `json:"observed_before_teardown"`
	ExitCode               *int   `json:"exit_code"`
	Signal                 string `json:"signal,omitempty"`
	Unavailable            string `json:"unavailable,omitempty"`
}

// DataBase64 preserves a bounded, redacted byte tail, including non-UTF-8
// serial output. ObservedBytes counts the source before redaction.
type RuntimeDiagnosticLog struct {
	DataBase64    string `json:"data_base64,omitempty"`
	ObservedBytes int64  `json:"observed_bytes"`
	RetainedBytes int    `json:"retained_bytes"`
	Truncated     bool   `json:"truncated"`
	Unavailable   string `json:"unavailable,omitempty"`
}

func (log RuntimeDiagnosticLog) DataBytes() ([]byte, error) {
	return base64.StdEncoding.DecodeString(log.DataBase64)
}

func RuntimeDiagnosticsFromEvents(events []RunEvent) *RuntimeDiagnostics {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == EventVMStarting || events[i].Type == EventRunScheduled {
			return nil
		}
		if events[i].Type != EventRuntimeDiagnostics {
			continue
		}
		data, err := events[i].DataBytes()
		var result RuntimeDiagnostics
		if err == nil && json.Unmarshal(data, &result) == nil && result.SchemaVersion == RuntimeDiagnosticsSchema {
			return &result
		}
	}
	return nil
}
