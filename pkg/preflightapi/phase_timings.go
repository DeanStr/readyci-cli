package preflightapi

// PhaseTimings contains process-local monotonic wall-clock observations, not CPU
// measurements or verification evidence. Nested phases overlap their parents;
// null means the phase was not observed, including phases that were not needed.
type PhaseTimings struct {
	Basis    string            `json:"basis"`
	PhasesMS map[string]*int64 `json:"phases_ms"`
}
