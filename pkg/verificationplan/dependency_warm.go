package verificationplan

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

type dependencyWarmPlacement struct {
	region                   string
	image                    string
	runnerSize               string
	requiresDockerCapability bool
	workerSelector           map[string]string
}

// DependencyWarmPlansForTargets specializes one trusted dependency check for
// the single persistent cache quota shared by its target checks. Targets on
// different runner sizes or incompatible runtime versions need separate warm
// nodes. Each consumer binds to its own preparation, including its cache lane. A
// repository with multiple cache quotas or key namespaces must standardize them
// instead of multiplying warm nodes that only prepare unrelated cache lanes.
// Every runner-size node mounts the union of target cache paths because cache
// lane affinity is project/worker scoped rather than runner-size scoped.
func DependencyWarmPlansForTargets(warmPlan CheckPlan, targets []CheckPlan) ([]CheckPlan, error) {
	var quota int64
	hasQuota := false
	var cacheKeyFiles []string
	type targetGroup struct {
		placement dependencyWarmPlacement
		targets   []CheckPlan
	}
	groups := make(map[string]*targetGroup)
	var cachePaths []string
	for _, target := range targets {
		mode, err := NormalizeCacheMode(target.Run.CacheMode)
		if err != nil {
			return nil, fmt.Errorf("resolve dependency warm target %q cache mode: %w", target.Name, err)
		}
		if mode == "" {
			mode = "cache-only"
		}
		if mode != "cache-only" {
			continue
		}
		if err := ValidateDependencySourceScope(warmPlan, target); err != nil {
			return nil, err
		}
		if err := requireSingleDependencyWarmLane(target, "target"); err != nil {
			return nil, err
		}
		if target.Run.CacheLaneSizeBytes < 0 {
			return nil, fmt.Errorf("resolve dependency warm target %q: cache lane size must not be negative", target.Name)
		}
		if hasQuota && target.Run.CacheLaneSizeBytes != quota {
			return nil, fmt.Errorf("dependency warm targets must use one cache lane size; %q requests %d bytes, want %d", target.Name, target.Run.CacheLaneSizeBytes, quota)
		}
		targetKeyFiles, err := NormalizeCacheKeyFiles(target.Run.CacheKeyFiles)
		if err != nil {
			return nil, fmt.Errorf("resolve dependency warm target %q cache key files: %w", target.Name, err)
		}
		if hasQuota && !slices.Equal(targetKeyFiles, cacheKeyFiles) {
			return nil, fmt.Errorf("dependency warm targets must use one cache key namespace; %q uses %v, want %v", target.Name, targetKeyFiles, cacheKeyFiles)
		}
		targetPlacement, err := dependencyWarmPlacementForTarget(target)
		if err != nil {
			return nil, err
		}
		group := groups[targetPlacement.runnerSize]
		if group == nil {
			group = &targetGroup{placement: targetPlacement}
			groups[targetPlacement.runnerSize] = group
		} else {
			if err := group.placement.mergeTarget(target.Name, targetPlacement); err != nil {
				return nil, err
			}
		}
		group.targets = append(group.targets, target)
		targetCachePaths, err := NormalizeCachePaths(target.Run.CachePaths)
		if err != nil {
			return nil, fmt.Errorf("resolve dependency warm target %q cache paths: %w", target.Name, err)
		}
		for _, cachePath := range targetCachePaths {
			cachePaths = appendUniqueString(cachePaths, cachePath)
		}
		quota = target.Run.CacheLaneSizeBytes
		cacheKeyFiles = targetKeyFiles
		hasQuota = true
	}
	if !hasQuota {
		return nil, nil
	}

	warmMode, err := NormalizeCacheMode(warmPlan.Run.CacheMode)
	if err != nil {
		return nil, fmt.Errorf("resolve dependency warm check %q cache mode: %w", warmPlan.Name, err)
	}
	if warmMode == "ephemeral" {
		return nil, fmt.Errorf("dependency warm check %q must use a persistent cache lane", warmPlan.Name)
	}
	if err := requireSingleDependencyWarmLane(warmPlan, "check"); err != nil {
		return nil, err
	}
	warmSelector, err := preflightapi.NormalizeWorkerSelector(warmPlan.Run.WorkerSelector)
	if err != nil {
		return nil, fmt.Errorf("resolve dependency warm check %q worker selector: %w", warmPlan.Name, err)
	}
	name := strings.TrimSpace(warmPlan.Name)
	if name == "" {
		return nil, fmt.Errorf("dependency warm check name is required")
	}
	runnerSizes := make([]string, 0, len(groups))
	for runnerSize := range groups {
		runnerSizes = append(runnerSizes, runnerSize)
	}
	sort.Strings(runnerSizes)
	plans := make([]CheckPlan, 0, len(runnerSizes))
	for _, runnerSize := range runnerSizes {
		group := groups[runnerSize]
		placement := group.placement
		if err := placement.mergeWorkerSelector(warmPlan.Name, warmSelector); err != nil {
			return nil, err
		}
		runtimeGroups, err := dependencyWarmRuntimeGroups(warmPlan.Run.RuntimeRequirements, group.targets)
		if err != nil {
			return nil, err
		}
		for _, runtimeGroup := range runtimeGroups {
			plan := warmPlan
			plan.DependencyWarmTargets = append([]string{}, runtimeGroup.targets...)
			plan.Run.CacheMode = "cache-only"
			plan.Run.CacheLaneSizeBytes = quota
			plan.Run.CacheKeyFiles = append([]string(nil), cacheKeyFiles...)
			plan.Run.CachePaths = append([]string(nil), cachePaths...)
			plan.Run.Region = placement.region
			plan.Run.Image = placement.image
			plan.Run.RunnerSize = placement.runnerSize
			plan.Run.WorkerSelector = copyStringMap(placement.workerSelector)
			plan.Run.RuntimeRequirements = preflightapi.CloneRuntimeRequirements(runtimeGroup.requirements)
			plan.Run.EnvironmentRequirements, err = preflightapi.NormalizeEnvironmentRequirements(warmPlan.Run.EnvironmentRequirements)
			if err != nil {
				return nil, err
			}
			for _, target := range group.targets {
				// Runtime groups prepare independent filesystems, and each
				// consumer binds only to its own group's successful cache.
				if !slices.Contains(runtimeGroup.targets, target.Name) {
					continue
				}
				plan.Run.EnvironmentRequirements, err = preflightapi.MergeEnvironmentRequirements(plan.Run.EnvironmentRequirements, target.Run.EnvironmentRequirements)
				if err != nil {
					return nil, err
				}
			}

			// Docker is an execution mode, not a placement-only constraint. Keep the
			// trusted warm command native, but bind it to a worker that can later execute
			// the Docker target with the same image and worker-local cache lane.
			if placement.requiresDockerCapability && !plan.Run.Docker && len(plan.Run.Services) == 0 {
				if plan.Run.WorkerSelector == nil {
					plan.Run.WorkerSelector = map[string]string{}
				}
				plan.Run.WorkerSelector[preflightapi.DockerCapabilitySystemLabel] = "true"
			}
			plan.LogicalChildID = "warm:" + name
			plan.LogicalChildName = "warm " + name
			if len(runnerSizes) > 1 {
				plan.LogicalChildID += ":runner=" + runnerSize
				plan.LogicalChildName += " [runner=" + runnerSize + "]"
			}
			if len(runtimeGroups) > 1 {
				plan.LogicalChildID += ":runtime=" + runtimeGroup.digest()
				plan.LogicalChildName += " [" + runtimeGroup.description() + "]"
			}
			plans = append(plans, plan)
		}
	}
	return plans, nil
}

func dependencyWarmPlacementForTarget(target CheckPlan) (dependencyWarmPlacement, error) {
	selector, err := preflightapi.NormalizeWorkerSelector(target.Run.WorkerSelector)
	if err != nil {
		return dependencyWarmPlacement{}, fmt.Errorf("resolve dependency warm target %q worker selector: %w", target.Name, err)
	}
	region := strings.TrimSpace(target.Run.Region)
	if region != "" {
		region = preflightapi.NormalizeRegion(region)
	}
	return dependencyWarmPlacement{
		region:                   region,
		image:                    defaultString(strings.TrimSpace(target.Run.Image), preflightapi.DefaultImage),
		runnerSize:               defaultString(strings.TrimSpace(target.Run.RunnerSize), preflightapi.DefaultRunnerSize),
		requiresDockerCapability: target.Run.Docker || len(target.Run.Services) > 0,
		workerSelector:           selector,
	}, nil
}

func (placement *dependencyWarmPlacement) mergeTarget(name string, target dependencyWarmPlacement) error {
	if target.region != placement.region {
		return fmt.Errorf("dependency warm targets must use one region; %q uses %q, want %q", name, target.region, placement.region)
	}
	if target.image != placement.image {
		return fmt.Errorf("dependency warm targets must use one worker image; %q uses %q, want %q", name, target.image, placement.image)
	}
	if target.runnerSize != placement.runnerSize {
		return fmt.Errorf("dependency warm targets must use one runner size; %q uses %q, want %q", name, target.runnerSize, placement.runnerSize)
	}

	placement.requiresDockerCapability = placement.requiresDockerCapability || target.requiresDockerCapability
	return placement.mergeWorkerSelector(name, target.workerSelector)
}

func requireSingleDependencyWarmLane(plan CheckPlan, kind string) error {
	variants, err := ExpandMatrix(plan.Matrix)
	if err != nil {
		return fmt.Errorf("resolve dependency warm %s %q matrix: %w", kind, plan.Name, err)
	}
	if len(variants) > 1 && plan.Matrix.MaxParallel > 1 {
		return fmt.Errorf("dependency warm %s %q cannot use matrix.max_parallel=%d: one warm phase provisions one worker-local cache lane", kind, plan.Name, plan.Matrix.MaxParallel)
	}
	return nil
}

func (placement *dependencyWarmPlacement) mergeWorkerSelector(name string, selector map[string]string) error {
	if placement.workerSelector == nil && len(selector) > 0 {
		placement.workerSelector = make(map[string]string, len(selector))
	}
	for key, value := range selector {
		if existing, ok := placement.workerSelector[key]; ok && existing != value {
			return fmt.Errorf("dependency warm target %q has incompatible worker selector %s=%q; common placement requires %s=%q", name, key, value, key, existing)
		}
		placement.workerSelector[key] = value
	}
	return nil
}
