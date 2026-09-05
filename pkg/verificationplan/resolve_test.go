package verificationplan

import (
	"reflect"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

func TestResolveCheckAppliesLayerPrecedence(t *testing.T) {
	falseValue := false
	trueValue := true
	tests := []struct {
		name        string
		check       config.CheckConfig
		options     ResolveOptions
		wantProject string
		wantRegion  string
		wantChanges string
		wantNetwork string
		wantDocker  bool
		wantRunner  string
		wantTimeout int
		wantCache   string
		wantPaths   []string
	}{
		{
			name:        "project defaults",
			check:       config.CheckConfig{Command: "go test ./..."},
			wantProject: "project-default",
			wantRegion:  "project-region",
			wantChanges: "working",
			wantNetwork: "none",
			wantDocker:  true,
			wantRunner:  "small",
			wantTimeout: 600,
			wantCache:   "cache-only",
			wantPaths:   []string{"project-cache"},
		},
		{
			name: "check overrides",
			check: config.CheckConfig{
				Command:     "go test ./...",
				Region:      "check-region",
				Changes:     "staged",
				NetworkMode: "default",
				Docker:      &falseValue,
				RunnerSize:  "large",
				Timeout:     "2m",
				Cache:       config.CacheConfig{Mode: "ephemeral", Paths: []string{"check-cache"}},
			},
			wantProject: "project-default",
			wantRegion:  "check-region",
			wantChanges: "staged",
			wantNetwork: "default",
			wantDocker:  false,
			wantRunner:  "large",
			wantTimeout: 120,
			wantCache:   "ephemeral",
			wantPaths:   []string{"project-cache", "check-cache"},
		},
		{
			name: "caller overrides",
			check: config.CheckConfig{
				Command:     "go test ./...",
				Region:      "check-region",
				Changes:     "staged",
				NetworkMode: "none",
				Docker:      &falseValue,
				Timeout:     "2m",
			},
			options: ResolveOptions{
				ProjectID:      "caller-project",
				Region:         "caller-region",
				Changes:        "all",
				NetworkMode:    "default",
				Timeout:        "30m",
				Docker:         true,
				DockerProvided: true,
				CachePaths:     []string{"caller-cache"},
			},
			wantProject: "caller-project",
			wantRegion:  "caller-region",
			wantChanges: "all",
			wantNetwork: "default",
			wantDocker:  true,
			wantRunner:  "small",
			wantTimeout: 1800,
			wantCache:   "cache-only",
			wantPaths:   []string{"project-cache", "caller-cache"},
		},
		{
			name: "caller disables inherited docker",
			check: config.CheckConfig{
				Command: "go test ./...",
				Docker:  &trueValue,
			},
			options:     ResolveOptions{Docker: false, DockerProvided: true},
			wantProject: "project-default",
			wantRegion:  "project-region",
			wantChanges: "working",
			wantNetwork: "none",
			wantDocker:  false,
			wantRunner:  "small",
			wantTimeout: 600,
			wantCache:   "cache-only",
			wantPaths:   []string{"project-cache"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.DefaultProjectConfig()
			cfg.ProjectID = "project-default"
			cfg.Region = "project-region"
			cfg.Network.Mode = "none"
			cfg.Timeout = "10m"
			cfg.Cache = config.CacheConfig{Mode: "cache-only", Size: "1GiB", Paths: []string{"project-cache"}}
			cfg.Resources.Default = config.ResourceDefaults{Size: "small", Docker: &trueValue}
			cfg.Checks = map[string]config.CheckConfig{"ci": test.check}

			plan, err := ResolveCheck("ci", cfg, test.options)
			if err != nil {
				t.Fatal(err)
			}
			if plan.Run.ProjectID != test.wantProject || plan.Run.Region != test.wantRegion || plan.Run.ChangeMode != test.wantChanges {
				t.Fatalf("identity = %q/%q/%q, want %q/%q/%q", plan.Run.ProjectID, plan.Run.Region, plan.Run.ChangeMode, test.wantProject, test.wantRegion, test.wantChanges)
			}
			if plan.Run.NetworkMode != test.wantNetwork || plan.Run.Docker != test.wantDocker {
				t.Fatalf("network/docker = %q/%t, want %q/%t", plan.Run.NetworkMode, plan.Run.Docker, test.wantNetwork, test.wantDocker)
			}
			if plan.Run.RunnerSize != test.wantRunner || plan.Run.TimeoutSeconds != test.wantTimeout {
				t.Fatalf("runner/timeout = %q/%d, want %q/%d", plan.Run.RunnerSize, plan.Run.TimeoutSeconds, test.wantRunner, test.wantTimeout)
			}
			if plan.Run.CacheMode != test.wantCache || !reflect.DeepEqual(plan.Run.CachePaths, test.wantPaths) {
				t.Fatalf("cache = %q/%v, want %q/%v", plan.Run.CacheMode, plan.Run.CachePaths, test.wantCache, test.wantPaths)
			}
		})
	}
}

func TestResolveCheckDefaultsDockerNetworkToNoneOnlyWhenModeWasImplicit(t *testing.T) {
	trueValue := true
	base := config.DefaultProjectConfig()
	base.Resources.Default.Docker = &trueValue
	base.Checks = map[string]config.CheckConfig{"ci": {Command: "go test ./..."}}

	implicit, err := ResolveCheck("ci", base, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if implicit.Run.NetworkMode != "none" {
		t.Fatalf("implicit docker network = %q, want none", implicit.Run.NetworkMode)
	}

	explicit, err := ResolveCheck("ci", base, ResolveOptions{NetworkMode: "default"})
	if err != nil {
		t.Fatal(err)
	}
	if explicit.Run.NetworkMode != "default" {
		t.Fatalf("explicit docker network = %q, want default", explicit.Run.NetworkMode)
	}
}
