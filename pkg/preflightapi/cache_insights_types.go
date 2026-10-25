package preflightapi

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Persistent cache lane states a worker reports when it attaches a lane. Warm
// means the lane image already existed on the worker from an earlier run; cold
// means the worker created the image for this run.
const (
	CacheLaneStateWarm = "warm"
	CacheLaneStateCold = "cold"
)

const CacheInsightWindowDays = 7

// WorkerPreparationReport is the agent_ready event payload. It embeds the
// worker-local phase timings and adds the attached cache lane's state.
type WorkerPreparationReport struct {
	PhaseTimings
	CacheLaneState string `json:"cache_lane_state,omitempty"`
}

// ParseWorkerPreparationReport decodes an agent_ready payload. An empty payload
// is a report without phase spans or cache state (runtimes that attach no
// cache send none). A present cache_lane_state must be warm or cold.
func ParseWorkerPreparationReport(data []byte) (WorkerPreparationReport, error) {
	var report WorkerPreparationReport
	if len(bytes.TrimSpace(data)) == 0 {
		return report, nil
	}
	if err := json.Unmarshal(data, &report); err != nil {
		return WorkerPreparationReport{}, fmt.Errorf("agent_ready data is not a worker preparation report: %w", err)
	}
	switch report.CacheLaneState {
	case "", CacheLaneStateWarm, CacheLaneStateCold:
		return report, nil
	default:
		return WorkerPreparationReport{}, fmt.Errorf("unsupported cache_lane_state %q", report.CacheLaneState)
	}
}

// CacheInsights summarizes how often a project's cached runs attached a warm
// lane in the last CacheInsightWindowDays days. Every project cache response
// carries it, with zero counts when there is nothing to report. Unreported runs
// finished with a lane but carry no reported state. EstimatedSavedMS is the sum
// of per-check estimates and is absent when no check has both warm and cold
// successes.
type CacheInsights struct {
	WindowDays       int                 `json:"window_days"`
	WarmRuns         int                 `json:"warm_runs"`
	ColdRuns         int                 `json:"cold_runs"`
	UnreportedRuns   int                 `json:"unreported_runs"`
	HitRate          *float64            `json:"hit_rate,omitempty"`
	EstimatedSavedMS *int64              `json:"estimated_saved_ms,omitempty"`
	Checks           []CacheCheckInsight `json:"checks"`
}

// CacheCheckInsight compares succeeded warm and cold runs of one check. An
// empty Check groups runs without a check name. Medians are of the claimed to
// finished interval; the saving estimate is
// max(0, cold median − warm median) × warm runs.
type CacheCheckInsight struct {
	Check            string `json:"check"`
	WarmRuns         int    `json:"warm_runs"`
	ColdRuns         int    `json:"cold_runs"`
	WarmMedianMS     *int64 `json:"warm_median_ms,omitempty"`
	ColdMedianMS     *int64 `json:"cold_median_ms,omitempty"`
	EstimatedSavedMS *int64 `json:"estimated_saved_ms,omitempty"`
}
