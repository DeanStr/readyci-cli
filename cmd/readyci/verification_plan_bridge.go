package main

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/DeanStr/readyci-cli/internal/syncbundlebuild"
	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
	"github.com/DeanStr/readyci-cli/pkg/verificationplan"
)

// splitCSV stays here: it parses CLI flag values, not configuration.
func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// Planning logic that depends only on configuration and a changed-file set lives
// in pkg/verificationplan so the service can derive a plan of its own rather
// than accept one compiled here (ADR 0007). These aliases and wrappers keep the
// CLI's existing spelling; the difference is that the CLI now supplies the
// `when` context explicitly instead of the matcher reaching into the
// environment on its own.

type (
	matrixVariant        = verificationplan.Variant
	affectedRuleMatch    = verificationplan.RuleMatch
	affectedSkippedCheck = verificationplan.SkippedCheck
	whenContext          = verificationplan.WhenContext
)

func expandMatrix(matrix config.MatrixConfig) ([]matrixVariant, error) {
	return verificationplan.ExpandMatrix(matrix)
}

func matrixVariantName(env map[string]string) string {
	return verificationplan.VariantName(env)
}

func affectedPatternMatches(patternValue, pathValue string) (bool, error) {
	return verificationplan.PatternMatches(patternValue, pathValue)
}

func validateAffectedPattern(patternValue string) error {
	return verificationplan.ValidatePattern(patternValue)
}

func normalizeAffectedPath(value string) string {
	return verificationplan.NormalizePath(value)
}

func whenConfigMatchesContext(when config.WhenConfig, ctx whenContext) (bool, string) {
	return verificationplan.WhenMatches(when, ctx)
}

func whenGlobMatches(pattern, value string) bool {
	return verificationplan.GlobMatches(pattern, value)
}

func selectChecksForAffectedRules(cfg config.ProjectConfig, changedFilesByCheck map[string][]string) ([]string, map[string][]affectedRuleMatch, error) {
	selected, reasons, _, err := selectChecksForAffectedRulesWithSkipped(cfg, changedFilesByCheck)
	return selected, reasons, err
}

func selectChecksForAffectedRulesWithSkipped(cfg config.ProjectConfig, changedFilesByCheck map[string][]string) ([]string, map[string][]affectedRuleMatch, []affectedSkippedCheck, error) {
	return verificationplan.SelectChecks(cfg, changedFilesByCheck, currentWhenContextFromEnv())
}

func affectedRuleExcludes(rule config.AffectedRule, file string) (bool, string, error) {
	return verificationplan.RuleExcludes(rule, file)
}

func pruneSubsumedAffectedChecks(cfg config.ProjectConfig, selected []string, reasons map[string][]affectedRuleMatch) []string {
	kept, _ := verificationplan.PruneSubsumedChecks(cfg, selected, reasons)
	return kept
}

// checkResolveOptions keeps the CLI's flat shape, including the fields that
// never reach a run specification: how this process talks to the API, and what
// it does with the result afterwards.
func resolveOptionsForPlanning(options checkResolveOptions) verificationplan.ResolveOptions {
	return verificationplan.ResolveOptions{
		ProjectID:                options.ProjectID,
		Region:                   options.Region,
		Changes:                  options.Changes,
		NetworkMode:              options.NetworkMode,
		NetworkAllowlist:         options.NetworkAllowlist,
		Timeout:                  options.Timeout,
		Docker:                   options.Docker,
		DockerProvided:           options.DockerProvided,
		KeepFailed:               options.KeepFailed,
		ConcurrencyGroup:         options.ConcurrencyGroup,
		ConcurrencyGroupProvided: options.ConcurrencyGroupProvided,
		ConcurrencyLimit:         options.ConcurrencyLimit,
		ConcurrencyLimitProvided: options.ConcurrencyLimitProvided,
		RetryCount:               options.RetryCount,
		RetryProvided:            options.RetryProvided,
		CachePaths:               options.CachePaths,
	}
}

func resolveCheckRunPlan(name string, cfg config.ProjectConfig, options checkResolveOptions) (checkRunPlan, error) {
	resolved, err := verificationplan.ResolveCheck(name, cfg, resolveOptionsForPlanning(options))
	if err != nil {
		// ResolveCheck only validates the user-supplied command overrides and the
		// loaded project configuration. Preserve that boundary so the root error
		// renderer never reports invalid input as an internal CLI failure.
		return checkRunPlan{}, cliConfigError(err.Error(), err)
	}
	if err := validateExplicitDependencyWarmSecretRequest(cfg, name, agentIntentEditLoop, options.RequestSecrets); err != nil {
		return checkRunPlan{}, cliConfigError(err.Error(), err)
	}
	resolved.Run.SecretsRequested = boolPtr(options.RequestSecrets)
	return checkRunPlan{
		Name:             resolved.Name,
		SkipWarm:         options.SkipWarm,
		RetryProvided:    options.RetryProvided,
		Retries:          resolved.Retries,
		Matrix:           resolved.Matrix,
		Variants:         resolved.Variants,
		Services:         resolved.Services,
		ServiceSpecs:     resolved.ServiceSpecs,
		When:             checkWhenReportFromConfig(resolved.When),
		WhenConfig:       resolved.When,
		RegionDefaulted:  resolved.RegionDefaulted,
		DiagnosticRegion: resolved.DiagnosticRegion,
		Options:          remoteRunOptionsFromRunSpec(resolved.Run, options),
	}, nil
}

func remoteRunOptionsFromRunSpec(run verificationplan.RunSpec, options checkResolveOptions) remoteRunOptions {
	return remoteRunOptions{
		CacheRuntimeRequirements: preflightapi.CloneRuntimeRequirements(run.CacheRuntimeRequirements),
		CacheDependencyCheck:     run.CacheDependencyCheck,
		RuntimeRequirements:      preflightapi.CloneRuntimeRequirements(run.RuntimeRequirements),
		EnvironmentRequirements:  preflightapi.CloneEnvironmentRequirements(run.EnvironmentRequirements),
		APIURL:                   options.APIURL,
		Open:                     options.Open,
		DashboardURL:             options.DashboardURL,
		ProjectID:                run.ProjectID,
		Region:                   run.Region,
		Command:                  run.Command,
		CommandAlias:             run.CommandAlias,
		Steps:                    run.Steps,
		ConcurrencyGroup:         run.ConcurrencyGroup,
		ConcurrencyLimit:         run.ConcurrencyLimit,
		PrepareCommands:          run.PrepareCommands,
		PostCommands:             run.PostCommands,
		AlwaysCommands:           run.AlwaysCommands,
		Image:                    run.Image,
		RunnerSize:               run.RunnerSize,
		TimeoutSeconds:           run.TimeoutSeconds,
		CacheMode:                run.CacheMode,
		CacheLaneSizeBytes:       run.CacheLaneSizeBytes,
		ArtifactPaths:            run.ArtifactPaths,
		NetworkMode:              run.NetworkMode,
		NetworkAllowlist:         run.NetworkAllowlist,
		Docker:                   run.Docker,
		Services:                 run.Services,
		WorkerSelector:           run.WorkerSelector,
		SecretsRequested:         run.SecretsRequested,
		Env:                      run.Env,
		ChangeMode:               run.ChangeMode,
		SyncExclude:              run.SyncExclude, SyncReviewedFixtures: append([]syncbundle.ReviewedFixture(nil), run.SyncReviewedFixtures...),
		IncludeUntracked: run.IncludeUntracked,
		CachePaths:       run.CachePaths,
		CacheKeyFiles:    run.CacheKeyFiles,
		KeepFailed:       run.KeepFailed,
	}
}

func effectiveCheckRetries(projectRetries int, check config.CheckConfig, cliRetries int) int {
	return verificationplan.EffectiveCheckRetries(projectRetries, check, cliRetries)
}

func effectiveCheckSyncConfig(projectSync, checkSync config.SyncConfig) config.SyncConfig {
	return verificationplan.EffectiveSyncConfig(projectSync, checkSync)
}

func effectiveProjectConcurrency(cfg config.ProjectConfig) (string, int, error) {
	return verificationplan.EffectiveProjectConcurrency(cfg)
}

func validateServiceConfigs(services map[string]config.ServiceConfig) error {
	return verificationplan.ValidateServiceConfigs(services)
}

func validateCheckServiceRefs(name string, refs []string, services map[string]config.ServiceConfig, docker bool) error {
	return verificationplan.ValidateCheckServiceRefs(name, refs, services, docker)
}

func resolvedServiceSpecs(services map[string]config.ServiceConfig, refs []string) []preflightapi.RunServiceSpec {
	return verificationplan.ResolvedServiceSpecs(services, refs)
}

func cloneRunSteps(steps []preflightapi.RunStepSpec) []preflightapi.RunStepSpec {
	return verificationplan.CloneRunSteps(steps)
}

func cloneRunServices(services []preflightapi.RunServiceSpec) []preflightapi.RunServiceSpec {
	return verificationplan.CloneRunServices(services)
}

func sortedServiceNames(services map[string]config.ServiceConfig) []string {
	return verificationplan.SortedServiceNames(services)
}

func validServiceEnvKey(key string) bool { return verificationplan.ValidServiceEnvKey(key) }

func secretBearingServiceEnv(key, value string) bool {
	return verificationplan.SecretBearingServiceEnv(key, value)
}

func projectCacheMode(cfg config.ProjectConfig) string {
	return verificationplan.ProjectCacheMode(cfg)
}

func normalizeCacheMode(mode string) (string, error) {
	return verificationplan.NormalizeCacheMode(mode)
}

func cacheLaneSizeForMode(mode string, size int64) int64 {
	return verificationplan.CacheLaneSizeForMode(mode, size)
}

func validateRunnerSize(size string) error { return verificationplan.ValidateRunnerSize(size) }

func parseByteSize(value string) (int64, error) { return verificationplan.ParseByteSize(value) }

func parseTimeoutSeconds(seconds int, text string) (int, error) {
	return verificationplan.ParseTimeoutSeconds(seconds, text)
}

func validateControlFreeValue(field, value string) error {
	return verificationplan.ValidateControlFreeValue(field, value)
}

func validateNetworkMode(mode string) error { return verificationplan.ValidateNetworkMode(mode) }

func effectiveRunNetworkMode(networkMode string, docker, provided bool) (string, error) {
	return verificationplan.EffectiveRunNetworkMode(networkMode, docker, provided)
}

func validateNetworkAllowlistUse(networkMode string, allowlist []string) error {
	return verificationplan.ValidateNetworkAllowlistUse(networkMode, allowlist)
}

func mergeResourceDefaults(base, override config.ResourceDefaults) config.ResourceDefaults {
	return verificationplan.MergeResourceDefaults(base, override)
}

func sortedCheckNames(checks map[string]config.CheckConfig) []string {
	return verificationplan.SortedCheckNames(checks)
}

func effectiveCheckConcurrency(name string, cfg config.ProjectConfig, check config.CheckConfig, options checkResolveOptions) (string, int, error) {
	return verificationplan.EffectiveCheckConcurrency(name, cfg, check, resolveOptionsForPlanning(options))
}

func resolvedServiceType(service config.ServiceConfig) string {
	return verificationplan.ResolvedServiceType(service)
}

func recommendAgentCheckWithIntent(cfg config.ProjectConfig, explicit, goal, intent string) (agentCheckRecommendation, error) {
	recommended, err := verificationplan.RecommendCheck(cfg, explicit, goal, intent)
	if err != nil {
		// Recommendation failures describe invalid or incomplete local project
		// configuration, including an explicitly requested check that does not
		// exist. Type them at this boundary instead of guessing from error text.
		return agentCheckRecommendation{}, cliConfigError(err.Error(), err)
	}
	alternatives := make([]agentCheckAlternative, 0, len(recommended.Alternatives))
	for _, alternative := range recommended.Alternatives {
		alternatives = append(alternatives, agentCheckAlternative(alternative))
	}
	out := agentCheckRecommendation{Check: recommended.Check, Reason: recommended.Reason}
	if len(alternatives) > 0 {
		out.Alternatives = alternatives
	}
	return out, nil
}

func agentGoalRequestsBroadVerification(goal string) bool {
	return verificationplan.GoalRequestsBroadVerification(goal)
}

func normalizeCachePaths(values []string) ([]string, error) {
	return verificationplan.NormalizeCachePaths(values)
}

func normalizeCacheKeyFiles(values []string) ([]string, error) {
	return verificationplan.NormalizeCacheKeyFiles(values)
}

func remoteCommandForOptions(options remoteRunOptions) (string, error) {
	return verificationplan.CommandForRunSpec(runSpecFromRemoteRunOptions(options))
}

func createRunCommandAndSteps(options remoteRunOptions, command string) (string, []preflightapi.RunStepSpec) {
	return verificationplan.CommandAndSteps(runSpecFromRemoteRunOptions(options), command)
}

// runSpecFromRemoteRunOptions is the inverse of remoteRunOptionsFromRunSpec: it
// drops the transport and graph-identity fields that a run specification does
// not carry.
func runSpecFromRemoteRunOptions(options remoteRunOptions) verificationplan.RunSpec {
	return verificationplan.RunSpec{
		CacheRuntimeRequirements: preflightapi.CloneRuntimeRequirements(options.CacheRuntimeRequirements),
		CacheDependencyCheck:     options.CacheDependencyCheck,
		RuntimeRequirements:      preflightapi.CloneRuntimeRequirements(options.RuntimeRequirements),
		EnvironmentRequirements:  preflightapi.CloneEnvironmentRequirements(options.EnvironmentRequirements),
		ProjectID:                options.ProjectID,
		Region:                   options.Region,
		Command:                  options.Command,
		CommandAlias:             options.CommandAlias,
		Steps:                    options.Steps,
		Env:                      options.Env,
		ConcurrencyGroup:         options.ConcurrencyGroup,
		ConcurrencyLimit:         options.ConcurrencyLimit,
		PrepareCommands:          options.PrepareCommands,
		PostCommands:             options.PostCommands,
		AlwaysCommands:           options.AlwaysCommands,
		Image:                    options.Image,
		RunnerSize:               options.RunnerSize,
		TimeoutSeconds:           options.TimeoutSeconds,
		CacheMode:                options.CacheMode,
		CacheLaneSizeBytes:       options.CacheLaneSizeBytes,
		ArtifactPaths:            options.ArtifactPaths,
		NetworkMode:              options.NetworkMode,
		NetworkAllowlist:         options.NetworkAllowlist,
		Docker:                   options.Docker,
		Services:                 options.Services,
		WorkerSelector:           options.WorkerSelector,
		SecretsRequested:         options.SecretsRequested,
		ChangeMode:               options.ChangeMode,
		SyncExclude:              options.SyncExclude, SyncReviewedFixtures: append([]syncbundle.ReviewedFixture(nil), options.SyncReviewedFixtures...),
		IncludeUntracked: options.IncludeUntracked,
		CachePaths:       options.CachePaths,
		CacheKeyFiles:    options.CacheKeyFiles,
		KeepFailed:       options.KeepFailed,
	}
}

func shellQuoteArg(arg string) string { return verificationplan.ShellQuoteArg(arg) }

func verificationNodesForPlans(warmPlans, plans []checkRunPlan, bundle syncbundlebuild.BuildResult) ([]preflightapi.VerificationNodePlan, []preflightapi.VerificationMatrixGroup, error) {
	return verificationplan.NodesForPlans(
		checkPlansForPlanning(warmPlans), checkPlansForPlanning(plans),
		verificationplan.BundleIdentity{BaselineKey: bundle.Manifest.BaselineKey},
		verificationplan.ClientIdentity{
			Client:     preflightapi.ClientInfo{Version: version, Platform: runtime.GOOS + "-" + runtime.GOARCH},
			Repository: gitRepository(),
		},
	)
}

func checkPlansForPlanning(plans []checkRunPlan) []verificationplan.CheckPlan {
	out := make([]verificationplan.CheckPlan, 0, len(plans))
	for _, plan := range plans {
		out = append(out, verificationplan.CheckPlan{
			DependencyWarmTargets: append([]string(nil), plan.DependencyWarmTargets...),
			Name:                  plan.Name,
			Run:                   runSpecFromRemoteRunOptions(plan.Options),
			LogicalChildID:        plan.Options.LogicalChildID,
			LogicalChildName:      plan.Options.LogicalChildName,
			Retries:               plan.Retries,
			Matrix:                plan.Matrix,
			When:                  plan.WhenConfig,
		})
	}
	return out
}

func projectConfigWithCheckWhenRules(cfg config.ProjectConfig) config.ProjectConfig {
	return verificationplan.ExpandWhenChangesRules(cfg)
}

func hasCheckWhenChanges(cfg config.ProjectConfig) bool {
	return verificationplan.HasCheckWhenChanges(cfg)
}

func affectedRuleMatchDescription(match affectedRuleMatch) string {
	if match.Rule <= 0 {
		if match.Pattern == "affected.unmatched_checks" {
			return fmt.Sprintf("%s selected by affected.unmatched_checks", match.Path)
		}
		return fmt.Sprintf("%s matched %s", match.Path, match.Pattern)
	}
	description := fmt.Sprintf("%s matched %s (rule %d", match.Path, match.Pattern, match.Rule)
	if match.SourceFile != "" {
		description += fmt.Sprintf(", from %s:%d", match.SourceFile, match.SourceLine)
	}
	return description + ")"
}
