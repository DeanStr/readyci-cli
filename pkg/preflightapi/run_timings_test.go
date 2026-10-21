package preflightapi

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestObservedTimingsKeepUnavailableSeparateFromZero(t *testing.T) {
	created := time.Unix(100, 0)
	queued, claimed, finished := created.Add(time.Second), created.Add(2*time.Second), created.Add(10*time.Second)
	summary := RunSummary{CreatedAt: created, QueuedAt: &queued, ClaimedAt: &claimed, CompletedAt: &finished}
	timings := ObserveRunTimings(summary, []RunEvent{{Type: EventCommandStarted, Timestamp: claimed}, {Type: EventCommandExited, Timestamp: created.Add(8 * time.Second)}})
	if timings.QueueMS == nil || *timings.QueueMS != 1000 || timings.WorkerPreparationMS == nil || *timings.WorkerPreparationMS != 0 || timings.CommandMS == nil || *timings.CommandMS != 6000 || timings.FinalizationMS == nil || *timings.FinalizationMS != 2000 {
		t.Fatalf("timings=%+v", timings)
	}
	if timings.DependencyPreparationMS != nil || timings.PeakMemoryBytes != nil || timings.CPUSeconds != nil {
		t.Fatal("unmeasured metrics fabricated")
	}
	encoded, _ := json.Marshal(timings)
	if !strings.Contains(string(encoded), `"peak_memory_bytes":null`) {
		t.Fatalf("JSON=%s", encoded)
	}
	timings = ObserveRunTimings(summary, []RunEvent{{Type: EventCommandStarted, Timestamp: created}, {Type: EventCommandExited, Timestamp: finished.Add(time.Second)}})
	if timings.CommandMS != nil || timings.WorkerPreparationMS != nil {
		t.Fatal("events outside current claim used for timings")
	}
}

func TestWorkerPreparationSpansAreDiagnosticAndClaimScoped(t *testing.T) {
	claimed := time.Unix(100, 0)
	finished := claimed.Add(10 * time.Second)
	summary := RunSummary{CreatedAt: claimed, ClaimedAt: &claimed, CompletedAt: &finished}
	data := `{"basis":"worker_monotonic","phases_ms":{"source_download_ms":0,"cache_setup_ms":null}}`
	for _, event := range []RunEvent{
		{Type: EventAgentReady, Timestamp: claimed, Data: data},
		{Type: EventAgentReady, Timestamp: claimed, DataBase64: base64.StdEncoding.EncodeToString([]byte(data))},
	} {
		timings := ObserveRunTimings(summary, []RunEvent{event})
		if timings.WorkerPreparation == nil || *timings.WorkerPreparation.PhasesMS["source_download_ms"] != 0 || timings.WorkerPreparation.PhasesMS["cache_setup_ms"] != nil {
			t.Fatalf("timings=%+v", timings)
		}
		if timings.WorkerPreparationMS != nil || timings.CommandMS != nil {
			t.Fatal("diagnostics fabricated lifecycle boundaries")
		}
	}
	for _, event := range []RunEvent{
		{Type: EventAgentReady, Timestamp: claimed.Add(-time.Second), Data: data},
		{Type: EventAgentReady, Timestamp: finished.Add(time.Second), Data: data},
		{Type: EventAgentReady, Timestamp: claimed, Data: `{"basis":"worker_monotonic","phases_ms":{"bad_ms":-1}}`},
		{Type: EventAgentReady, Timestamp: claimed, Data: `{"basis":"server_event_observations","phases_ms":{"total_ms":0}}`},
		{Type: EventAgentReady, Timestamp: claimed, Data: "not JSON"},
		{Type: EventAgentReady, Timestamp: claimed, DataBase64: "not base64"},
	} {
		if got := ObserveRunTimings(summary, []RunEvent{event}); got.WorkerPreparation != nil || got.Unavailable["worker_preparation"] == "" {
			t.Fatalf("invalid/out-of-claim event used: %+v", got)
		}
	}
}
