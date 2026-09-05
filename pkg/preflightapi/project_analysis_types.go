package preflightapi

type ProjectAnalysisInventory struct {
	SchemaVersion string                    `json:"schema_version"`
	ProjectID     string                    `json:"project_id,omitempty"`
	ConfigPath    string                    `json:"config_path,omitempty"`
	Checks        []string                  `json:"checks,omitempty"`
	Signals       []ProjectAnalysisSignal   `json:"signals,omitempty"`
	Files         []ProjectAnalysisFileFact `json:"files,omitempty"`
	Packages      []ProjectAnalysisPackage  `json:"packages,omitempty"`
}

type ProjectAnalysisSignal struct {
	Kind       string   `json:"kind"`
	Path       string   `json:"path,omitempty"`
	Confidence float64  `json:"confidence,omitempty"`
	Evidence   []string `json:"evidence,omitempty"`
}

type ProjectAnalysisFileFact struct {
	Path string `json:"path"`
	Kind string `json:"kind,omitempty"`
	Hash string `json:"hash,omitempty"`
}

type ProjectAnalysisPackage struct {
	Path           string   `json:"path"`
	Ecosystem      string   `json:"ecosystem"`
	PackageManager string   `json:"package_manager,omitempty"`
	Lockfile       string   `json:"lockfile,omitempty"`
	Scripts        []string `json:"scripts,omitempty"`
	Features       []string `json:"features,omitempty"`
}

type AnalyzeProjectRequest struct {
	Inventory ProjectAnalysisInventory `json:"inventory"`
}

type AnalyzeProjectResponse struct {
	SchemaVersion string                      `json:"schema_version"`
	ProjectID     string                      `json:"project_id,omitempty"`
	Packs         []ProjectRecommendationPack `json:"packs,omitempty"`
	Warnings      []string                    `json:"warnings,omitempty"`
}

type ConfigSuggestionsRequest struct {
	Inventory ProjectAnalysisInventory `json:"inventory"`
}

type ConfigSuggestionsResponse struct {
	SchemaVersion   string                      `json:"schema_version"`
	ProjectID       string                      `json:"project_id,omitempty"`
	Packs           []ProjectRecommendationPack `json:"packs,omitempty"`
	SuggestedConfig string                      `json:"suggested_config,omitempty"`
	Warnings        []string                    `json:"warnings,omitempty"`
}

type ProjectRecommendationPack struct {
	ID         string                `json:"id"`
	Name       string                `json:"name,omitempty"`
	Version    string                `json:"version"`
	Confidence float64               `json:"confidence,omitempty"`
	Rationale  []string              `json:"rationale,omitempty"`
	Rules      []AffectedProfileRule `json:"rules,omitempty"`
	Fallbacks  []string              `json:"fallbacks,omitempty"`
	Metadata   map[string]string     `json:"metadata,omitempty"`
}

type AffectedProfileRule struct {
	Paths   []string `json:"paths"`
	Checks  []string `json:"checks,omitempty"`
	Exclude []string `json:"exclude,omitempty"`
}
