package preflightapi

// ProjectTestStatus reports retained test history. An empty ProjectID denotes
// the authenticated team across all its projects.
type ProjectTestStatus struct {
	ProjectID     string `json:"project_id"`
	CompletedTest bool   `json:"completed_test"`
}
