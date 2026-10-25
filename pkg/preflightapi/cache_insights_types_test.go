package preflightapi

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestWorkerPreparationReportMarshalsFlatPayload(t *testing.T) {
	setup := int64(12)
	report := WorkerPreparationReport{
		PhaseTimings:   PhaseTimings{Basis: "worker_monotonic", PhasesMS: map[string]*int64{"cache_setup_ms": &setup}},
		CacheLaneState: CacheLaneStateWarm,
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	if got, want := string(encoded), `{"basis":"worker_monotonic","phases_ms":{"cache_setup_ms":12},"cache_lane_state":"warm"}`; got != want {
		t.Fatalf("payload = %s, want %s", got, want)
	}
	report.CacheLaneState = ""
	encoded, err = json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report without lane: %v", err)
	}
	if strings.Contains(string(encoded), "cache_lane_state") {
		t.Fatalf("payload without lane = %s, want state omitted", encoded)
	}
}

func TestParseWorkerPreparationReport(t *testing.T) {
	for _, tc := range []struct {
		name  string
		data  string
		state string
	}{
		{name: "empty", data: "", state: ""},
		{name: "whitespace", data: "  \n", state: ""},
		{name: "null", data: "null", state: ""},
		{name: "timings only", data: `{"basis":"worker_monotonic","phases_ms":{"total_ms":5}}`, state: ""},
		{name: "warm", data: `{"basis":"worker_monotonic","phases_ms":{},"cache_lane_state":"warm"}`, state: CacheLaneStateWarm},
		{name: "cold", data: `{"cache_lane_state":"cold"}`, state: CacheLaneStateCold},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report, err := ParseWorkerPreparationReport([]byte(tc.data))
			if err != nil {
				t.Fatalf("parse %q: %v", tc.data, err)
			}
			if report.CacheLaneState != tc.state {
				t.Fatalf("state = %q, want %q", report.CacheLaneState, tc.state)
			}
		})
	}
	for _, data := range []string{
		`{"cache_lane_state":"hot"}`,
		`{"cache_lane_state":"WARM"}`,
		`{"cache_lane_state":" warm"}`,
		`{"cache_lane_state":1}`,
		`["warm"]`,
		"not JSON",
	} {
		if _, err := ParseWorkerPreparationReport([]byte(data)); err == nil {
			t.Fatalf("parse %q succeeded, want error", data)
		}
	}
}

func TestObserveRunTimingsParsesWorkerPreparationReport(t *testing.T) {
	claimed := time.Unix(100, 0)
	finished := claimed.Add(10 * time.Second)
	summary := RunSummary{CreatedAt: claimed, ClaimedAt: &claimed, CompletedAt: &finished}
	setup := int64(40)
	data, err := json.Marshal(WorkerPreparationReport{
		PhaseTimings:   PhaseTimings{Basis: "worker_monotonic", PhasesMS: map[string]*int64{"cache_setup_ms": &setup}},
		CacheLaneState: CacheLaneStateCold,
	})
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	timings := ObserveRunTimings(summary, []RunEvent{{Type: EventAgentReady, Timestamp: claimed, Data: string(data)}})
	if timings.WorkerPreparation == nil || timings.WorkerPreparation.PhasesMS["cache_setup_ms"] == nil || *timings.WorkerPreparation.PhasesMS["cache_setup_ms"] != 40 {
		t.Fatalf("worker preparation = %+v, want cache setup span from report", timings.WorkerPreparation)
	}
	if _, unavailable := timings.Unavailable["worker_preparation"]; unavailable {
		t.Fatalf("worker preparation reported unavailable: %+v", timings.Unavailable)
	}
}
