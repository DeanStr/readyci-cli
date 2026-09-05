package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

type ProjectConfig struct {
	Version          int                      `yaml:"version"`
	ProjectID        string                   `yaml:"project_id"`
	Region           string                   `yaml:"region"`
	Image            string                   `yaml:"image"`
	RunnerSize       string                   `yaml:"runner_size"`
	TimeoutSeconds   int                      `yaml:"timeout_seconds"`
	Timeout          string                   `yaml:"timeout"`
	Retries          int                      `yaml:"retries"`
	ConcurrencyGroup string                   `yaml:"concurrency_group"`
	ConcurrencyLimit int                      `yaml:"concurrency_limit"`
	CacheMode        string                   `yaml:"cache_mode"`
	Sync             SyncConfig               `yaml:"sync"`
	Cache            CacheConfig              `yaml:"cache"`
	Artifacts        ArtifactConfig           `yaml:"artifacts"`
	Affected         AffectedConfig           `yaml:"affected"`
	Network          NetworkConfig            `yaml:"network"`
	Resources        ResourceConfig           `yaml:"resources"`
	Services         map[string]ServiceConfig `yaml:"services"`
	Checks           map[string]CheckConfig   `yaml:"checks"`
}

type SyncConfig struct {
	IncludeUntracked *bool    `yaml:"include_untracked"`
	RespectGitignore *bool    `yaml:"respect_gitignore"`
	Exclude          []string `yaml:"exclude"`
}

type CacheConfig struct {
	Mode     string   `yaml:"mode"`
	Size     string   `yaml:"size"`
	Paths    []string `yaml:"paths"`
	KeyFiles []string `yaml:"key_files"`
}

type ArtifactConfig struct {
	Paths []string `yaml:"paths"`
}

type AffectedConfig struct {
	Profiles        []string               `yaml:"profiles"`
	Rules           []AffectedRule         `yaml:"rules"`
	UnmatchedChecks CommandList            `yaml:"unmatched_checks"`
	Subsumes        map[string]CommandList `yaml:"subsumes"`
	Tests           []AffectedTest         `yaml:"tests"`
	AuditIgnore     []string               `yaml:"audit_ignore"`
}

type AffectedRule struct {
	Paths       []string                      `yaml:"paths"`
	PathsFrom   AffectedPathsFromList         `yaml:"paths_from"`
	PathSources map[string]AffectedPathSource `yaml:"-"`
	Exclude     []string                      `yaml:"exclude"`
	Checks      CommandList                   `yaml:"checks"`
}

type AffectedPathsFrom struct {
	File     string `yaml:"file"`
	Template string `yaml:"template"`
}

type AffectedPathSource struct {
	File     string
	Line     int
	Template string
}

type AffectedPathsFromList []AffectedPathsFrom

func (sources *AffectedPathsFromList) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.MappingNode:
		source, err := decodeAffectedPathsFrom(value)
		if err != nil {
			return err
		}
		*sources = []AffectedPathsFrom{source}
		return nil
	case yaml.SequenceNode:
		values := make([]AffectedPathsFrom, 0, len(value.Content))
		for _, item := range value.Content {
			source, err := decodeAffectedPathsFrom(item)
			if err != nil {
				return err
			}
			values = append(values, source)
		}
		*sources = values
		return nil
	default:
		return fmt.Errorf("expected paths_from object or list")
	}
}

func decodeAffectedPathsFrom(value *yaml.Node) (AffectedPathsFrom, error) {
	if value.Kind != yaml.MappingNode {
		return AffectedPathsFrom{}, fmt.Errorf("expected paths_from object")
	}
	var source AffectedPathsFrom
	for i := 0; i < len(value.Content); i += 2 {
		key := value.Content[i]
		field := value.Content[i+1]
		switch key.Value {
		case "file":
			if err := field.Decode(&source.File); err != nil {
				return AffectedPathsFrom{}, err
			}
		case "template":
			if err := field.Decode(&source.Template); err != nil {
				return AffectedPathsFrom{}, err
			}
		default:
			return AffectedPathsFrom{}, fmt.Errorf("field %s not found in type config.AffectedPathsFrom", key.Value)
		}
	}
	return source, nil
}

type AffectedTest struct {
	Path   string      `yaml:"path"`
	Paths  []string    `yaml:"paths"`
	Checks CommandList `yaml:"checks"`
}

type NetworkConfig struct {
	Mode          string   `yaml:"mode"`
	Allowlist     []string `yaml:"allowlist"`
	WarmMode      string   `yaml:"warm_mode"`
	WarmAllowlist []string `yaml:"warm_allowlist"`
}

type ResourceConfig struct {
	Default ResourceDefaults            `yaml:"default"`
	Checks  map[string]ResourceDefaults `yaml:"checks"`
}

type ResourceDefaults struct {
	Size    string `yaml:"size"`
	Timeout string `yaml:"timeout"`
	Docker  *bool  `yaml:"docker"`
}

type ServiceConfig struct {
	Type    string              `yaml:"type"`
	Version string              `yaml:"version"`
	Image   string              `yaml:"image"`
	Ports   []string            `yaml:"ports"`
	Env     map[string]string   `yaml:"env"`
	Health  ServiceHealthConfig `yaml:"health"`
	Logs    []string            `yaml:"logs"`
}

type ServiceHealthConfig struct {
	Command  string `yaml:"command"`
	Timeout  string `yaml:"timeout"`
	Interval string `yaml:"interval"`
}

type CheckConfig struct {
	Command             string             `yaml:"command"`
	Steps               []StepConfig       `yaml:"steps"`
	Prepare             CommandList        `yaml:"prepare"`
	Post                CommandList        `yaml:"post"`
	Always              CommandList        `yaml:"always"`
	Changes             string             `yaml:"changes"`
	Region              string             `yaml:"region"`
	Image               string             `yaml:"image"`
	RunnerSize          string             `yaml:"runner_size"`
	Size                string             `yaml:"size"`
	TimeoutSeconds      int                `yaml:"timeout_seconds"`
	Timeout             string             `yaml:"timeout"`
	Retries             int                `yaml:"retries"`
	RetriesSet          bool               `yaml:"-"`
	ConcurrencyGroup    string             `yaml:"concurrency_group"`
	ConcurrencyGroupSet bool               `yaml:"-"`
	ConcurrencyLimit    int                `yaml:"concurrency_limit"`
	ConcurrencyLimitSet bool               `yaml:"-"`
	Matrix              MatrixConfig       `yaml:"matrix"`
	When                WhenConfig         `yaml:"when"`
	Sync                SyncConfig         `yaml:"sync"`
	CacheMode           string             `yaml:"cache_mode"`
	Cache               CacheConfig        `yaml:"cache"`
	NetworkMode         string             `yaml:"network_mode"`
	NetworkAllowlist    []string           `yaml:"network_allowlist"`
	Docker              *bool              `yaml:"docker"`
	Browser             string             `yaml:"browser"`
	Services            []string           `yaml:"services"`
	WorkerSelector      map[string]string  `yaml:"worker_selector"`
	KeepFailed          bool               `yaml:"keep_failed"`
	ArtifactPaths       []string           `yaml:"artifact_paths"`
	Outputs             CheckOutputsConfig `yaml:"outputs"`
}

type CheckOutputsConfig struct {
	Reviewable []string `yaml:"reviewable"`
}

type StepConfig struct {
	Name    string            `yaml:"name"`
	Command string            `yaml:"command"`
	Always  bool              `yaml:"always"`
	Env     map[string]string `yaml:"env"`
	Timeout string            `yaml:"timeout"`
}

type WhenConfig struct {
	Event   []string          `yaml:"event"`
	Branch  []string          `yaml:"branch"`
	Tag     string            `yaml:"tag"`
	Env     map[string]string `yaml:"env"`
	Status  string            `yaml:"status"`
	Changes WhenChangesConfig `yaml:"changes"`
}

type WhenChangesConfig struct {
	Include []string `yaml:"include"`
	Exclude []string `yaml:"exclude"`
}

type CommandList []string

type MatrixConfig struct {
	Axes        map[string][]string `yaml:"axes"`
	Include     []map[string]string `yaml:"include"`
	Exclude     []map[string]string `yaml:"exclude"`
	FailFast    bool                `yaml:"fail_fast"`
	MaxParallel int                 `yaml:"max_parallel"`
}
