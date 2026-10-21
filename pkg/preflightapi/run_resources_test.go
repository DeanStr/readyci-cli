package preflightapi

import (
	"testing"
	"time"
)

func TestResourceObservationsAreClaimBoundAndNullable(t *testing.T) {
	now := time.Now()
	event := RunEvent{Type: EventResourceUsage, Timestamp: now, Data: `{"basis":"host_vm_cgroup_v2","peak_memory_bytes":4096,"cpu_usage_us":1500000,"oom_kills":0}`}
	summary := RunSummary{ClaimedAt: &now}
	timings := ObserveRunTimings(summary, []RunEvent{event})
	if timings.Resources == nil || timings.PeakMemoryBytes == nil || *timings.PeakMemoryBytes != 4096 || timings.CPUSeconds == nil || *timings.CPUSeconds != 1.5 {
		t.Fatalf("timings=%+v", timings)
	}
	later := now.Add(time.Second)
	summary.ClaimedAt = &later
	if got := ObserveRunTimings(summary, []RunEvent{event}); got.Resources != nil || got.PeakMemoryBytes != nil {
		t.Fatal("old attempt supplied measurements")
	}
	if ResourcesFromEvents([]RunEvent{event, {Type: EventVMStarting}}) != nil {
		t.Fatal("old attempt OOM attribution leaked")
	}
}
