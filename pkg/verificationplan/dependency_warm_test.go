package verificationplan

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestDependencyWarmPlansForTargetsRejectsMultiplePersistentCacheQuotas(t *testing.T) {
	warm := CheckPlan{
		Name: "dependencies",
		Run: RunSpec{
			CacheMode:          "cache-only",
			CacheLaneSizeBytes: 10,
		},
	}
	targets := []CheckPlan{
		{Name: "default", Run: RunSpec{CacheMode: "cache-only"}},
		{Name: "large", Run: RunSpec{CacheMode: "cache-only", CacheLaneSizeBytes: 40}},
		{Name: "large-duplicate", Run: RunSpec{CacheLaneSizeBytes: 40}},
		{Name: "ephemeral", Run: RunSpec{CacheMode: "ephemeral", CacheLaneSizeBytes: 80}},
	}

	_, err := DependencyWarmPlansForTargets(warm, targets)
	if err == nil || !strings.Contains(err.Error(), "must use one cache lane size") {
		t.Fatalf("multiple cache quota error = %v", err)
	}
	if warm.Run.CacheLaneSizeBytes != 10 || warm.LogicalChildID != "" {
		t.Fatalf("source warm plan was mutated: %+v", warm)
	}
}

func TestDependencyWarmPlansForTargetsPreservesCompactSingleQuotaIdentity(t *testing.T) {
	warm := CheckPlan{Name: "dependencies", Run: RunSpec{
		Region: "warm", Image: "warm-image", RunnerSize: "small", Docker: false,
		WorkerSelector: map[string]string{"warm": "true"}, CachePaths: []string{"warm-cache"},
		CacheMode: "cache-only", CacheLaneSizeBytes: 10, CacheKeyFiles: []string{"warm.lock"},
	}}
	plans, err := DependencyWarmPlansForTargets(
		warm,
		[]CheckPlan{{Name: "test", Run: RunSpec{
			Region: " EU-WEST ", Image: " target-image ", RunnerSize: " large ", Docker: true,
			WorkerSelector: map[string]string{"pool": "trusted"}, CachePaths: []string{"~/.cache/go", "./build/cache"},
			CacheMode: "cache-only", CacheLaneSizeBytes: 40, CacheKeyFiles: []string{"go.sum", "./go.sum"},
		}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || plans[0].LogicalChildID != "warm:dependencies" ||
		plans[0].LogicalChildName != "warm dependencies" || plans[0].Run.CacheLaneSizeBytes != 40 ||
		plans[0].Run.Region != "eu-west" || plans[0].Run.Image != "target-image" ||
		plans[0].Run.RunnerSize != "large" || plans[0].Run.Docker ||
		!reflect.DeepEqual(plans[0].Run.WorkerSelector, map[string]string{
			"pool": "trusted", "warm": "true", preflightapi.DockerCapabilitySystemLabel: "true",
		}) ||
		!slices.Equal(plans[0].Run.CacheKeyFiles, []string{"go.sum"}) ||
		!slices.Equal(plans[0].Run.CachePaths, []string{"~/.cache/go", "build/cache"}) {
		t.Fatalf("single-quota warm plans = %+v", plans)
	}
	if !slices.Equal(warm.Run.CacheKeyFiles, []string{"warm.lock"}) ||
		!slices.Equal(warm.Run.CachePaths, []string{"warm-cache"}) ||
		!reflect.DeepEqual(warm.Run.WorkerSelector, map[string]string{"warm": "true"}) {
		t.Fatalf("source warm plan was mutated: %+v", warm.Run)
	}
}

func TestDependencyWarmPlansForTargetsUnionsTargetCachePathsAndPlacementRequirements(t *testing.T) {
	warm := CheckPlan{Name: "dependencies", Run: RunSpec{CacheMode: "cache-only"}}
	targets := []CheckPlan{
		{Name: "unit", Run: RunSpec{
			CacheMode: "cache-only", CachePaths: []string{"~/.cache/go", "build/cache"},
			WorkerSelector: map[string]string{"pool": "trusted"},
		}},
		{Name: "integration", Run: RunSpec{
			CacheMode: "cache-only", CachePaths: []string{"./build/cache", "tmp/integration"}, Docker: true,
			WorkerSelector: map[string]string{"zone": "a"},
		}},
	}

	plans, err := DependencyWarmPlansForTargets(warm, targets)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || !slices.Equal(plans[0].Run.CachePaths, []string{"~/.cache/go", "build/cache", "tmp/integration"}) ||
		plans[0].Run.Image != preflightapi.DefaultImage || plans[0].Run.RunnerSize != preflightapi.DefaultRunnerSize ||
		plans[0].Run.Docker || !reflect.DeepEqual(plans[0].Run.WorkerSelector, map[string]string{
		"pool": "trusted", "zone": "a", preflightapi.DockerCapabilitySystemLabel: "true",
	}) {
		t.Fatalf("merged warm plan = %+v", plans)
	}
}

func TestDependencyWarmPlansForTargetsRejectsIncompatiblePlacement(t *testing.T) {
	tests := []struct {
		name    string
		first   RunSpec
		second  RunSpec
		message string
	}{
		{name: "region", first: RunSpec{Region: "us-east"}, second: RunSpec{Region: "eu-west"}, message: "must use one region"},
		{name: "image", first: RunSpec{Image: "image-a"}, second: RunSpec{Image: "image-b"}, message: "must use one worker image"},
		{name: "selector", first: RunSpec{WorkerSelector: map[string]string{"pool": "a"}}, second: RunSpec{WorkerSelector: map[string]string{"pool": "b"}}, message: "incompatible worker selector"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.first.CacheMode = "cache-only"
			test.second.CacheMode = "cache-only"
			_, err := DependencyWarmPlansForTargets(
				CheckPlan{Name: "dependencies", Run: RunSpec{CacheMode: "cache-only"}},
				[]CheckPlan{{Name: "first", Run: test.first}, {Name: "second", Run: test.second}},
			)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("placement error = %v, want %q", err, test.message)
			}
		})
	}
}

func TestDependencyWarmPlansForTargetsPartitionsRunnerSizes(t *testing.T) {
	warm := CheckPlan{Name: "dependencies", Run: RunSpec{CacheMode: "cache-only"}}
	targets := []CheckPlan{
		{Name: "standard", Run: RunSpec{
			CacheMode: "cache-only", RunnerSize: "standard", CachePaths: []string{"standard-cache"},
		}},
		{Name: "large", Run: RunSpec{
			CacheMode: "cache-only", RunnerSize: "large", CachePaths: []string{"large-cache"}, Docker: true,
		}},
	}

	plans, err := DependencyWarmPlansForTargets(warm, targets)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 2 || plans[0].Run.RunnerSize != "large" || plans[1].Run.RunnerSize != "standard" ||
		plans[0].LogicalChildID != "warm:dependencies:runner=large" ||
		plans[1].LogicalChildID != "warm:dependencies:runner=standard" ||
		!slices.Equal(plans[0].Run.CachePaths, []string{"standard-cache", "large-cache"}) ||
		!slices.Equal(plans[1].Run.CachePaths, []string{"standard-cache", "large-cache"}) ||
		plans[0].Run.WorkerSelector[preflightapi.DockerCapabilitySystemLabel] != "true" {
		t.Fatalf("runner-partitioned warm plans = %+v", plans)
	}
}

func TestDependencyWarmPlansForTargetsRejectsMultipleCacheKeyNamespaces(t *testing.T) {
	warm := CheckPlan{Name: "dependencies", Run: RunSpec{CacheMode: "cache-only"}}
	targets := []CheckPlan{
		{Name: "cli", Run: RunSpec{CacheMode: "cache-only", CacheKeyFiles: []string{"cli/go.sum"}}},
		{Name: "platform", Run: RunSpec{CacheMode: "cache-only", CacheKeyFiles: []string{"platform/go.sum"}}},
	}

	_, err := DependencyWarmPlansForTargets(warm, targets)
	if err == nil || !strings.Contains(err.Error(), "must use one cache key namespace") {
		t.Fatalf("multiple cache key namespace error = %v", err)
	}
}

func TestDependencyWarmPlansForTargetsRejectsEphemeralWarmCheck(t *testing.T) {
	_, err := DependencyWarmPlansForTargets(
		CheckPlan{Name: "dependencies", Run: RunSpec{CacheMode: "ephemeral"}},
		[]CheckPlan{{Name: "test", Run: RunSpec{CacheMode: "cache-only", CacheLaneSizeBytes: 40}}},
	)
	if err == nil || !strings.Contains(err.Error(), "must use a persistent cache lane") {
		t.Fatalf("ephemeral warm error = %v", err)
	}
}

func TestDependencyWarmPlansForTargetsRejectsParallelTargetMatrix(t *testing.T) {
	_, err := DependencyWarmPlansForTargets(
		CheckPlan{Name: "dependencies", Run: RunSpec{CacheMode: "cache-only"}},
		[]CheckPlan{{
			Name: "test",
			Run:  RunSpec{CacheMode: "cache-only"},
			Matrix: config.MatrixConfig{
				Axes: map[string][]string{"GO": {"1.24", "1.25"}}, MaxParallel: 2,
			},
		}},
	)
	if err == nil || !strings.Contains(err.Error(), "one warm phase provisions one worker-local cache lane") {
		t.Fatalf("parallel target error = %v, want single-lane rejection", err)
	}
}

func TestDependencyWarmPlansForTargetsRejectsParallelWarmMatrix(t *testing.T) {
	_, err := DependencyWarmPlansForTargets(
		CheckPlan{
			Name: "dependencies",
			Run:  RunSpec{CacheMode: "cache-only"},
			Matrix: config.MatrixConfig{
				Axes: map[string][]string{"GO": {"1.24", "1.25"}}, MaxParallel: 2,
			},
		},
		[]CheckPlan{{Name: "test", Run: RunSpec{CacheMode: "cache-only"}}},
	)
	if err == nil || !strings.Contains(err.Error(), "one warm phase provisions one worker-local cache lane") {
		t.Fatalf("parallel warm error = %v, want single-lane rejection", err)
	}
}
