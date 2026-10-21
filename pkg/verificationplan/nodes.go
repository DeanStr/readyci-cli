package verificationplan

import (
	"fmt"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

// BundleIdentity is what the sealed node graph needs to know about the uploaded
// workspace. It is a narrow struct rather than the builder's result type so that
// a caller reading a validated bundle server-side can supply it too.
type BundleIdentity struct {
	BaselineKey string
}

// ClientIdentity is the provenance stamped onto every node's run request: which
// client built the plan, and which repository it came from. The CLI reports
// itself and the local git remote; a service-side planner reports itself and the
// repository recorded for the project.
type ClientIdentity struct {
	Client     preflightapi.ClientInfo
	Repository string
}

// nodeIdentity is how one matrix variant of a check names itself in the graph.
type nodeIdentity struct {
	LogicalChildID   string
	LogicalChildName string
	CommandAlias     string
}

// applyVariantToRunSpec specializes a run specification for one matrix variant
// and derives that variant's identity in the graph.
//
// An identity the planner already assigned wins over one derived from the
// command alias: a node the planner inserted has a name that says what it is
// for, and overwriting it would both lose that meaning and collide with the
// user's own check of the same name.
func applyVariantToRunSpec(plan CheckPlan, variant Variant) (RunSpec, nodeIdentity) {
	run := plan.Run
	identity := nodeIdentity{
		LogicalChildID:   defaultString(plan.LogicalChildID, defaultString(run.CommandAlias, "check")),
		LogicalChildName: defaultString(plan.LogicalChildName, run.CommandAlias),
		CommandAlias:     run.CommandAlias,
	}
	if variant.Name != "" {
		identity.LogicalChildID += ":" + variant.Name
		if identity.CommandAlias != "" {
			identity.CommandAlias += " [" + variant.Name + "]"
		}
	}
	if len(variant.Env) > 0 {
		run.Env = mergeStringMaps(run.Env, variant.Env)
	}
	return run, identity
}

// DefaultRunTimeoutSeconds is the run timeout applied when neither the check nor
// the project sets one.
const DefaultRunTimeoutSeconds = 20 * 60

func defaultInt(value, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
}

func planRequiresSuccessfulStatus(plan CheckPlan) bool {
	return strings.TrimSpace(plan.When.Status) == "success"
}

func runRequestRegion(region string) string {
	region = strings.TrimSpace(region)
	if region == "" {
		return ""
	}
	return preflightapi.NormalizeRegion(region)
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

// NodesForPlans compiles resolved checks into the sealed verification graph:
// warm phase first, then verify, with each plan depending on the one before it
// and each check requiring its matching preparation to succeed.
//
// A check declaring `when.status: success` depends on every earlier node having
// succeeded, not merely the one immediately before it. Sequential execution
// tracks failure cumulatively — once anything fails, later success-only checks
// are skipped even if an unconditional check succeeded in between — and a graph
// that only carried the immediate predecessor would run them.
func NodesForPlans(warmPlans, plans []CheckPlan, bundle BundleIdentity, client ClientIdentity) ([]preflightapi.VerificationNodePlan, []preflightapi.VerificationMatrixGroup, error) {
	var nodes []preflightapi.VerificationNodePlan
	var matrixGroups []preflightapi.VerificationMatrixGroup
	seen := map[string]int{}
	used := map[string]bool{}
	groupSeen := map[string]int{}
	groupUsed := map[string]bool{}
	dependencies := []string(nil)
	warmDependencies := []string(nil)
	warmTargets := map[string][]string{}
	warmRuntimeRequirements := map[string]map[string]string{}
	completed := []string(nil)
	type phasedPlan struct {
		plan  CheckPlan
		phase string
	}
	phased := make([]phasedPlan, 0, len(warmPlans)+len(plans))
	for _, plan := range warmPlans {
		phased = append(phased, phasedPlan{plan: plan, phase: preflightapi.VerificationNodePhaseWarm})
	}
	for _, plan := range plans {
		phased = append(phased, phasedPlan{plan: plan, phase: preflightapi.VerificationNodePhaseVerify})
	}
	for _, item := range phased {
		plan := item.plan
		variants, err := ExpandMatrix(plan.Matrix)
		if err != nil {
			return nil, nil, err
		}
		matrixGroup := ""
		if len(variants) > 1 || plan.Matrix.MaxParallel > 0 || plan.Matrix.FailFast {
			matrixGroup = uniqueNodeID("matrix:"+plan.Name, groupSeen, groupUsed)
			maxParallel := plan.Matrix.MaxParallel
			if maxParallel < 1 {
				maxParallel = 1
			}
			matrixGroups = append(matrixGroups, preflightapi.VerificationMatrixGroup{
				ID: matrixGroup, MaxParallel: maxParallel, FailFast: plan.Matrix.FailFast,
			})
		}
		// Verify-matrix siblings are independent. Warm variants instead form a
		// success chain so each extends the same worker-local cache.
		priorNodes := append([]string(nil), completed...)
		selectedWarmDependencies := warmDependencies
		if item.phase == preflightapi.VerificationNodePhaseWarm && plan.DependencyWarmTargets != nil {
			// Each runtime group prepares its own filesystem. Matrix variants
			// within this plan still extend that filesystem in sequence below.
			selectedWarmDependencies = nil
		} else {
			// An ordinary warm check extends its dependency preparation just
			// as an offline consumer does. Its confirmation will bind to this
			// extended cache below rather than skipping back to the installer.
			if selected, ok := warmTargets[plan.Name]; ok {
				selectedWarmDependencies = selected
			}
		}
		currentIDs := make([]string, 0, len(variants))
		for _, variant := range variants {
			options, identity := applyVariantToRunSpec(plan, variant)
			if requirements, prepared := warmRuntimeRequirements[plan.Name]; prepared && (item.phase == preflightapi.VerificationNodePhaseVerify || plan.DependencyWarmTargets == nil) {
				// The same compatible constraints must select the runtime for
				// preparation and consumption on the bound worker. Otherwise a
				// broader consumer range can select another installed major and
				// load native dependencies prepared by an incompatible runtime.
				selected := map[string]string{}
				for runtime, required := range options.RuntimeRequirements {
					if !preflightapi.RuntimeRequirementImplies(requirements[runtime], required) {
						return nil, nil, fmt.Errorf("dependency preparation for check %q does not satisfy runtime %s %s", plan.Name, runtime, required)
					}
					selected[runtime] = requirements[runtime]
				}
				// Keep the group's exact normalized representation as well: it
				// identifies the shared cache namespace even when a consumer used
				// a differently spelled but equivalent range in configuration.
				options.CacheRuntimeRequirements = preflightapi.CloneRuntimeRequirements(requirements)
				// Preparing a dependency may need an SDK that its consumer never
				// executes. Keep those SDKs in cache identity without forcing an
				// offline consumer to provision them again.
				options.RuntimeRequirements = preflightapi.CloneRuntimeRequirements(selected)
			}
			command, err := CommandForRunSpec(options)
			if err != nil {
				return nil, nil, err
			}
			createCommand, createSteps := CommandAndSteps(options, command)
			id := uniqueNodeID(defaultString(identity.LogicalChildID, plan.Name), seen, used)
			artifactPaths := append([]string(nil), options.ArtifactPaths...)
			if options.KeepFailed {
				artifactPaths = appendUniqueString(artifactPaths, preflightapi.FailedWorkspaceArtifactPath)
			}
			cacheMode := defaultString(options.CacheMode, "cache-only")
			condition := preflightapi.VerificationNodeConditionAlways
			dependsOn := append([]string(nil), dependencies...)
			successDependsOn := append([]string(nil), selectedWarmDependencies...)
			if item.phase == preflightapi.VerificationNodePhaseWarm {
				dependsOn = append(dependsOn, currentIDs...)
				successDependsOn = append(successDependsOn, currentIDs...)
			}
			if planRequiresSuccessfulStatus(plan) {
				condition = preflightapi.VerificationNodeConditionOnSuccess
				// Every earlier node, not just the immediate predecessor. The
				// scheduler requires all of an on_success node's dependencies to
				// have succeeded, so listing them all reproduces the cumulative
				// failure tracking of sequential execution.
				for _, prior := range priorNodes {
					dependsOn = appendUniqueString(dependsOn, prior)
				}
			}
			nodes = append(nodes, preflightapi.VerificationNodePlan{
				ID: id, Name: defaultString(identity.LogicalChildName, plan.Name), Kind: "check",
				Phase: item.phase, Condition: condition, MatrixGroup: matrixGroup,
				Required: true, DependsOn: dependsOn,
				SuccessDependsOn: successDependsOn,
				Retry:            preflightapi.VerificationRetryPolicy{MaxAttempts: plan.Retries + 1, RetryableClassifications: []string{preflightapi.ClassificationTransportFailure, preflightapi.ClassificationWorkerUnavailable, preflightapi.ClassificationWorkerInternalError, preflightapi.ClassificationPlatformInternalError}},
				Run: preflightapi.CreateRunRequest{
					RuntimeRequirements: preflightapi.CloneRuntimeRequirements(options.RuntimeRequirements), ProjectID: options.ProjectID, Region: runRequestRegion(options.Region), Image: defaultString(strings.TrimSpace(options.Image), preflightapi.DefaultImage), Command: createCommand, CommandAlias: identity.CommandAlias, Steps: createSteps,
					ConcurrencyGroup: options.ConcurrencyGroup, ConcurrencyLimit: options.ConcurrencyLimit, RunnerSize: defaultString(options.RunnerSize, preflightapi.DefaultRunnerSize), TimeoutSeconds: defaultInt(options.TimeoutSeconds, DefaultRunTimeoutSeconds), CacheMode: cacheMode, CacheLaneSizeBytes: CacheLaneSizeForMode(cacheMode, options.CacheLaneSizeBytes), ArtifactPaths: artifactPaths,
					NetworkMode: defaultString(strings.TrimSpace(options.NetworkMode), preflightapi.DefaultNetworkMode), NetworkAllowlist: append([]string(nil), options.NetworkAllowlist...), Docker: options.Docker, Services: CloneRunServices(options.Services), WorkerSelector: copyStringMap(options.WorkerSelector), BundleBaselineKey: bundle.BaselineKey, SecretsRequested: options.SecretsRequested,
					Client: client.Client, Git: preflightapi.GitInfo{Materialization: "minimal_git", Repository: client.Repository},
				},
			})
			currentIDs = append(currentIDs, id)
		}
		dependencies = currentIDs
		completed = append(completed, currentIDs...)
		if item.phase == preflightapi.VerificationNodePhaseWarm {
			if plan.DependencyWarmTargets == nil {
				warmDependencies = append(warmDependencies, currentIDs...)
				warmTargets[plan.Name] = append([]string(nil), currentIDs...)
			} else {
				for _, target := range plan.DependencyWarmTargets {
					warmTargets[target] = append(warmTargets[target], currentIDs...)
					requirements, err := mergeDependencyWarmRequirements(warmRuntimeRequirements[target], plan.Run.RuntimeRequirements)
					if err != nil {
						return nil, nil, fmt.Errorf("bind check %q to dependency preparation runtimes: %w", target, err)
					}
					warmRuntimeRequirements[target] = requirements
				}
			}
		}
	}
	return nodes, matrixGroups, nil
}

func uniqueNodeID(base string, seen map[string]int, used map[string]bool) string {
	seen[base]++
	candidate := base
	if seen[base] > 1 {
		candidate = fmt.Sprintf("%s:%d", base, seen[base])
	}
	for used[candidate] {
		seen[base]++
		candidate = fmt.Sprintf("%s:%d", base, seen[base])
	}
	used[candidate] = true
	return candidate
}
