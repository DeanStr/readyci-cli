package preflightapi

import (
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
