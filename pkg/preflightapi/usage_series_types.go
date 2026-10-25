package preflightapi

import "time"

// AccountUsageSeriesResponse breaks the current usage period's compute
// slot-minutes down by UTC day, project, and check. Quantities use the same
// period-bounded proration as AccountUsageSummary.ComputeSlotMinutes, so
// TotalSlotMinutes equals that figure for the same period.
type AccountUsageSeriesResponse struct {
	PeriodStart      time.Time           `json:"period_start"`
	PeriodEnd        time.Time           `json:"period_end"`
	TotalSlotMinutes float64             `json:"total_slot_minutes"`
	Days             []UsageSeriesDay    `json:"days"`
	ByProject        []UsageBreakdownRow `json:"by_project"`
	ByCheck          []UsageBreakdownRow `json:"by_check"`
}

// UsageSeriesDay is one zero-filled UTC day from the period start through the
// earlier of today and the period end.
type UsageSeriesDay struct {
	Date        string  `json:"date"`
	SlotMinutes float64 `json:"slot_minutes"`
	Runs        int     `json:"runs"`
}

// UsageBreakdownRow attributes slot-minutes to a project, or to one check
// (run command alias) within a project. Check is empty on project rows and
// for runs submitted without a check alias.
type UsageBreakdownRow struct {
	ProjectID   string  `json:"project_id"`
	Check       string  `json:"check,omitempty"`
	SlotMinutes float64 `json:"slot_minutes"`
	Runs        int     `json:"runs"`
}
