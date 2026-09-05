package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

type agentProgressMode string

const (
	agentProgressAuto   agentProgressMode = "auto"
	agentProgressStatus agentProgressMode = "status"
	agentProgressQuiet  agentProgressMode = "quiet"
	agentProgressLogs   agentProgressMode = "logs"
)

type agentProgressEventKind string

const (
	agentProgressPlanned           agentProgressEventKind = "planned"
	agentProgressWorkspacePackaged agentProgressEventKind = "workspace_packaged"
	agentProgressRunAccepted       agentProgressEventKind = "run_accepted"
	agentProgressUploadStarted     agentProgressEventKind = "upload_started"
	agentProgressUploadCompleted   agentProgressEventKind = "upload_completed"
	agentProgressLifecycle         agentProgressEventKind = "lifecycle"
	agentProgressRetrying          agentProgressEventKind = "retrying"
	agentProgressSkipped           agentProgressEventKind = "skipped"
	agentProgressNodeStatus        agentProgressEventKind = "node_status"
)

type agentProgressEvent struct {
	Kind         agentProgressEventKind
	Mode         string
	Checks       []string
	RunName      string
	RunID        string
	EventType    string
	Status       string
	Step         string
	SizeBytes    int64
	Attempt      int
	AttemptCount int
	ExitCode     *int
}

type agentProgressSink interface {
	Emit(agentProgressEvent)
	Log(preflightapi.RunEvent, []byte)
}

func emitAgentProgress(sink agentProgressSink, event agentProgressEvent) {
	if sink != nil {
		sink.Emit(event)
	}
}

type agentProgressReporter struct {
	mu       sync.Mutex
	mode     agentProgressMode
	writer   io.Writer
	last     map[string]string
	lastStep map[string]string
	runNames map[string]string
}

func newAgentProgressReporter(value string, jsonOutput bool, writer io.Writer, interactive bool) (*agentProgressReporter, error) {
	mode, err := normalizeAgentProgressMode(value)
	if err != nil {
		return nil, err
	}
	if mode == agentProgressAuto {
		if !jsonOutput && interactive {
			mode = agentProgressStatus
		} else {
			mode = agentProgressQuiet
		}
	}
	if writer == nil {
		writer = io.Discard
	}
	return &agentProgressReporter{
		mode:     mode,
		writer:   writer,
		last:     make(map[string]string),
		lastStep: make(map[string]string),
		runNames: make(map[string]string),
	}, nil
}

func normalizeAgentProgressMode(value string) (agentProgressMode, error) {
	mode := agentProgressMode(strings.ToLower(strings.TrimSpace(value)))
	if mode == "" {
		mode = agentProgressAuto
	}
	switch mode {
	case agentProgressAuto, agentProgressStatus, agentProgressQuiet, agentProgressLogs:
		return mode, nil
	default:
		return "", fmt.Errorf("invalid progress mode %q (want auto, status, quiet, or logs)", value)
	}
}

func stderrIsInteractive() bool {
	info, err := os.Stderr.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func (r *agentProgressReporter) Emit(event agentProgressEvent) {
	if r == nil || r.mode == agentProgressQuiet {
		return
	}
	line, key, state := formatAgentProgressEvent(event)
	if line == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if event.Kind == agentProgressRunAccepted && strings.TrimSpace(event.RunID) != "" {
		r.runNames[event.RunID] = defaultString(sanitizeAgentProgressField(event.RunName), sanitizeAgentProgressField(event.RunID))
	}
	if key != "" {
		if previous, ok := r.last[key]; ok && previous == state {
			return
		}
	}
	if key != "" {
		r.last[key] = state
	}
	_, _ = fmt.Fprintln(r.writer, line)
}

func (r *agentProgressReporter) Log(event preflightapi.RunEvent, data []byte) {
	if r == nil || r.mode == agentProgressQuiet {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if step := sanitizeAgentProgressField(event.Step); step != "" && r.lastStep[event.RunID] != step {
		r.lastStep[event.RunID] = step
		name := defaultString(r.runNames[event.RunID], defaultString(strings.TrimSpace(event.RunID), "run"))
		_, _ = fmt.Fprintf(r.writer, "readyci: %s · step: %s\n", name, step)
	}
	if r.mode == agentProgressLogs && len(data) > 0 {
		_, _ = r.writer.Write(data)
		if data[len(data)-1] != '\n' {
			_, _ = io.WriteString(r.writer, "\n")
		}
	}
}

func formatAgentProgressEvent(event agentProgressEvent) (line, key, state string) {
	runName := sanitizeAgentProgressField(event.RunName)
	if runName == "" {
		runName = sanitizeAgentProgressField(event.RunID)
	}
	if runName == "" {
		runName = "verification"
	}
	switch event.Kind {
	case agentProgressPlanned:
		checks := append([]string(nil), event.Checks...)
		for index := range checks {
			checks[index] = sanitizeAgentProgressField(checks[index])
		}
		sort.Strings(checks)
		selection := strings.Join(checks, ", ")
		if selection == "" {
			selection = "none"
		}
		mode := defaultString(sanitizeAgentProgressField(event.Mode), "check")
		return fmt.Sprintf("readyci: verification planned · %s: %s", strings.ReplaceAll(mode, "_", " "), selection), "plan", mode + "\x00" + selection
	case agentProgressWorkspacePackaged:
		return fmt.Sprintf("readyci: workspace packaged · %s", formatProgressBytes(event.SizeBytes)), "workspace", fmt.Sprint(event.SizeBytes)
	case agentProgressRunAccepted:
		runID := sanitizeAgentProgressField(event.RunID)
		return fmt.Sprintf("readyci: %s · accepted · %s", runName, runID), "accepted:" + runID, event.Status
	case agentProgressUploadStarted:
		return fmt.Sprintf("readyci: %s · uploading workspace · %s", runName, formatProgressBytes(event.SizeBytes)), "upload:" + event.RunID, "started"
	case agentProgressUploadCompleted:
		return fmt.Sprintf("readyci: %s · workspace uploaded", runName), "upload:" + event.RunID, "completed"
	case agentProgressRetrying:
		return fmt.Sprintf("readyci: %s · retrying · attempt %d/%d", runName, event.Attempt, event.AttemptCount), "retry:" + runName, fmt.Sprintf("%d/%d", event.Attempt, event.AttemptCount)
	case agentProgressSkipped:
		return fmt.Sprintf("readyci: %s · skipped · previous required check failed", runName), "skip:" + runName, "skipped"
	case agentProgressNodeStatus:
		status := defaultString(sanitizeAgentProgressField(event.Status), "unknown")
		return fmt.Sprintf("readyci: %s · %s", runName, strings.ReplaceAll(status, "_", " ")), "node:" + defaultString(event.RunID, runName), status
	case agentProgressLifecycle:
		label := lifecycleProgressLabel(event)
		if label == "" {
			return "", "", ""
		}
		return fmt.Sprintf("readyci: %s · %s", runName, label), "lifecycle:" + event.RunID, label
	default:
		return "", "", ""
	}
}

func sanitizeAgentProgressField(value string) string {
	value = strings.Join(strings.Fields(sanitizeLogDisplayText(value)), " ")
	const maxRunes = 160
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes-1]) + "…"
}

func lifecycleProgressLabel(event agentProgressEvent) string {
	switch event.EventType {
	case preflightapi.EventRunQueued:
		return "queued"
	case preflightapi.EventRunScheduled:
		return "scheduled"
	case preflightapi.EventUploadReceived:
		return "workspace received"
	case preflightapi.EventUploadFailed:
		return "workspace upload failed"
	case preflightapi.EventVMStarting:
		return "VM starting"
	case preflightapi.EventAgentReady:
		return "VM ready"
	case preflightapi.EventCommandStarted:
		return "command running"
	case preflightapi.EventCommandExited:
		if event.ExitCode != nil {
			return fmt.Sprintf("command exited · code %d", *event.ExitCode)
		}
		return "command exited"
	case preflightapi.EventArtifactUploaded:
		return "artifact uploaded"
	case preflightapi.EventLogTruncated:
		return "log truncated"
	case preflightapi.EventRunFinished:
		return "succeeded"
	case preflightapi.EventRunFailed:
		return "failed"
	case preflightapi.EventRunTimedOut:
		return "timed out"
	case preflightapi.EventRunCancelled:
		return "cancelled"
	default:
		return ""
	}
}

func formatProgressBytes(value int64) string {
	if value < 0 {
		value = 0
	}
	const unit = int64(1024)
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	divisor := float64(unit)
	unitName := "KiB"
	if value >= unit*unit {
		divisor = float64(unit * unit)
		unitName = "MiB"
	}
	if value >= unit*unit*unit {
		divisor = float64(unit * unit * unit)
		unitName = "GiB"
	}
	return fmt.Sprintf("%.1f %s", float64(value)/divisor, unitName)
}
