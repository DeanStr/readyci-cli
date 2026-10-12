package main

import (
	"context"

	"github.com/DeanStr/readyci-cli/internal/syncbundlebuild"
	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func prepareExecutionRerun(previous preflightapi.VerificationSession, apiURL string) (preparedVerificationSession, error) {
	var loaded loadedReadyCIConfig
	var err error
	if previous.Recipe.Command != "" {
		// Literal commands use the same optional configuration as readyci run.
		// Inferring a check template here would change source and runtime defaults.
		loaded.Config, loaded.Path, err = config.LoadProjectConfig(".")
	} else {
		loaded, err = loadProjectConfigOrInfer(".")
	}
	if err != nil {
		return preparedVerificationSession{}, err
	}
	cfg := loaded.Config
	resolve := checkResolveOptions{SkipWarm: previous.Recipe.SkipWarm, RequestSecrets: previous.Recipe.RequestSecrets, APIURL: apiURL, ProjectID: previous.ProjectID, Changes: previous.Recipe.ChangeMode, RetryCount: previous.Recipe.RetryCount, RetryProvided: previous.Recipe.RetryProvided}
	if len(previous.Recipe.WarmChecks) > 0 {
		warmResolve, err := warmDependencyResolveOptions(resolve, cfg)
		if err != nil {
			return preparedVerificationSession{}, err
		}
		prepared := preparedVerificationSession{ProjectID: previous.ProjectID, CoverageMode: "check"}
		var all []affectedCheckPlan
		for _, name := range previous.Recipe.WarmChecks {
			warm, err := resolveCheckRunPlan(name, cfg, warmResolve)
			if err != nil {
				return prepared, err
			}
			warm.Options.SecretsRequested = boolPtr(false)
			prepared.WarmPlans = append(prepared.WarmPlans, warm)
			all = append(all, affectedCheckPlan{Plan: warm})
			if !previous.Recipe.SkipConfirm {
				check, err := resolveCheckRunPlan(name, cfg, resolve)
				if err != nil {
					return prepared, err
				}
				check.Options.SecretsRequested = boolPtr(previous.Recipe.RequestSecrets)
				prepared.Plans = append(prepared.Plans, check)
				all = append(all, affectedCheckPlan{Plan: check})
			}
		}
		first := all[0].Plan.Options
		prepared.BuildOptions, err = compatibleAffectedSessionBuildOptions(all, syncbundlebuild.BuildOptions{ChangeMode: first.ChangeMode, ExcludePatterns: first.SyncExclude, IncludeUntracked: first.IncludeUntracked})
		return prepared, err
	}

	name := previous.Recipe.Check
	if previous.Recipe.Command != "" {
		// An ad hoc command keeps its literal command while current configuration
		// supplies the environment and source filters for the new workspace.
		name = "requested-command"
		cfg.Checks = map[string]config.CheckConfig{name: {Command: previous.Recipe.Command}}
	}
	var selected []affectedCheckPlan
	if name == "" {
		if !shouldUseAffectedRules(cfg, "") {
			return preparedVerificationSession{}, cliConfigError("cannot rerun affected selection without affected rules; restore the rules or run readyci check NAME explicitly", nil)
		}
		selected, _, _, err = buildAgentAffectedRulePlans(context.Background(), ".", cfg, loaded.Path, resolve)
	} else {
		var plan checkRunPlan
		plan, err = resolveCheckRunPlan(name, cfg, resolve)
		if previous.Recipe.Command != "" {
			plan.Retries = literalCommandRetries(previous.Recipe.RetryCount, previous.Recipe.RetryProvided)
		}
		selected = []affectedCheckPlan{{Plan: plan}}
	}
	if err != nil {
		return preparedVerificationSession{}, err
	}
	if len(selected) == 0 {
		return preparedVerificationSession{}, cliConfigError("no affected checks selected; use readyci check to inspect the current selection", nil)
	}
	warm, err := dependencyWarmPlansForAffected(".", selected, cfg, resolve)
	if err != nil {
		return preparedVerificationSession{}, err
	}
	all := append(append([]affectedCheckPlan{}, warm...), selected...)
	first := selected[0].Plan.Options
	build, err := compatibleAffectedSessionBuildOptions(all, syncbundlebuild.BuildOptions{ChangeMode: first.ChangeMode, ExcludePatterns: first.SyncExclude, IncludeUntracked: first.IncludeUntracked})
	if err != nil {
		return preparedVerificationSession{}, err
	}
	prepared := preparedVerificationSession{ProjectID: previous.ProjectID, BuildOptions: build, CoverageMode: "check"}
	for _, plan := range selected {
		plan.Plan.Options.SecretsRequested = boolPtr(previous.Recipe.RequestSecrets)
		prepared.Plans = append(prepared.Plans, plan.Plan)
	}
	for _, plan := range warm {
		plan.Plan.Options.SecretsRequested = boolPtr(false)
		prepared.WarmPlans = append(prepared.WarmPlans, plan.Plan)
	}
	return prepared, nil
}
