package preflightapi

import "time"

// RunTimings describes server-observed lifecycle intervals. Command time includes
// preparation inside the command; event delivery is part of these observations.
type RunTimings struct {
	Basis                   string            `json:"basis"`
	TotalMS                 *int64            `json:"total_ms"`
	QueueMS                 *int64            `json:"queue_ms"`
	WorkerPreparationMS     *int64            `json:"worker_preparation_ms"`
	CommandMS               *int64            `json:"command_ms"`
	FinalizationMS          *int64            `json:"finalization_ms"`
	DependencyPreparationMS *int64            `json:"dependency_preparation_ms"`
	PeakMemoryBytes         *int64            `json:"peak_memory_bytes"`
	CPUSeconds              *float64          `json:"cpu_seconds"`
	Unavailable             map[string]string `json:"unavailable"`
}

func ObserveRunTimings(summary RunSummary, events []RunEvent) RunTimings {
	result := RunTimings{Basis: "server_event_observations", Unavailable: map[string]string{
		"dependency_preparation_ms": "No independent dependency preparation measurement is recorded.",
		"peak_memory_bytes":         "Runtime resource measurement is unavailable.",
		"cpu_seconds":               "Runtime resource measurement is unavailable.",
	}}
	var started, exited time.Time
	for _, event := range events {
		if summary.ClaimedAt == nil || event.Timestamp.Before(*summary.ClaimedAt) || summary.CompletedAt != nil && event.Timestamp.After(*summary.CompletedAt) {
			continue
		}
		if event.Type == EventCommandStarted && started.IsZero() {
			started = event.Timestamp
		}
		if event.Type == EventCommandExited && !started.IsZero() && !event.Timestamp.Before(started) {
			exited = event.Timestamp
		}
	}
	result.TotalMS = timingInterval(runTimingTimePointer(summary.CreatedAt), summary.CompletedAt)
	result.QueueMS = timingInterval(summary.QueuedAt, summary.ClaimedAt)
	result.WorkerPreparationMS = timingInterval(summary.ClaimedAt, runTimingTimePointer(started))
	result.CommandMS = timingInterval(runTimingTimePointer(started), runTimingTimePointer(exited))
	result.FinalizationMS = timingInterval(runTimingTimePointer(exited), summary.CompletedAt)
	for name, value := range map[string]*int64{"total_ms": result.TotalMS, "queue_ms": result.QueueMS, "worker_preparation_ms": result.WorkerPreparationMS, "command_ms": result.CommandMS, "finalization_ms": result.FinalizationMS} {
		if value == nil {
			result.Unavailable[name] = "The required lifecycle boundaries have not been recorded."
		}
	}
	return result
}

func runTimingTimePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

func timingInterval(start, end *time.Time) *int64 {
	if start == nil || end == nil || start.IsZero() || end.IsZero() || end.Before(*start) {
		return nil
	}
	value := end.Sub(*start).Milliseconds()
	return &value
}
