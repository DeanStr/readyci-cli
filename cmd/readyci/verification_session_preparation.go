package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/internal/syncbundlebuild"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

type verificationSessionRemoteContext struct {
	Client         *api.Client
	IdempotencyKey string
	TaskID         string
	PredecessorID  string
}

func prepareVerificationSessionRemoteContext(
	ctx context.Context,
	options verificationSessionOptions,
) (verificationSessionRemoteContext, error) {
	token, err := effectiveAPIToken(options.APIURL, options.APIToken)
	if err != nil {
		return verificationSessionRemoteContext{}, err
	}
	if strings.TrimSpace(token) == "" && !(options.Purpose == preflightapi.ExecutionPurpose && localAPIEndpoint(options.APIURL)) {
		return verificationSessionRemoteContext{}, cliAuthMissingError("No Ready CI credential is available.", nil)
	}
	baseKey, err := normalizeIdempotencyKey(options.IdempotencyKey)
	if err != nil {
		return verificationSessionRemoteContext{}, cliConfigError(err.Error(), err)
	}
	if baseKey == "" {
		baseKey, err = newIdempotencyKey()
		if err != nil {
			return verificationSessionRemoteContext{}, err
		}
	}
	remote := verificationSessionRemoteContext{
		Client:         api.NewClient(options.APIURL, api.WithToken(token)),
		IdempotencyKey: baseKey,
		TaskID:         strings.TrimSpace(options.TaskID),
		PredecessorID:  strings.TrimSpace(options.PredecessorID),
	}
	predecessorTaskID := ""
	if remote.PredecessorID != "" {
		predecessor, predecessorErr := remote.Client.VerificationSession(ctx, remote.PredecessorID)
		if predecessorErr != nil {
			return verificationSessionRemoteContext{}, fmt.Errorf("read predecessor verification session: %w", predecessorErr)
		}
		predecessorTaskID = defaultString(strings.TrimSpace(predecessor.TaskID), strings.TrimSpace(predecessor.IdempotencyKey))
	}
	remote.TaskID, err = verificationTaskID(baseKey, remote.TaskID, predecessorTaskID)
	if err != nil {
		return verificationSessionRemoteContext{}, cliConfigError(err.Error(), err)
	}
	return remote, nil
}

func verificationSessionRequest(
	options verificationSessionOptions,
	intent string,
	remote verificationSessionRemoteContext,
	projectID string,
	bundle syncbundlebuild.BuildResult,
	buildOptions syncbundlebuild.BuildOptions,
	matched []string,
	coverageMode string,
	warmPlans, plans []checkRunPlan,
) (preflightapi.CreateVerificationSessionRequest, error) {
	matched, uncovered := verificationCoverage(coverageMode, bundle.Manifest.ChangedFiles, matched)
	nodes, matrixGroups, err := verificationNodesForPlans(warmPlans, plans, bundle)
	if err != nil {
		return preflightapi.CreateVerificationSessionRequest{}, err
	}
	if options.Purpose == preflightapi.ExecutionPurpose {
		// Ordinary execution preserves its retry policy while the service still
		// requires a retryable result and enforces the sealed attempt limit.
		for index := range nodes {
			nodes[index].Retry.RetryableClassifications = append(nodes[index].Retry.RetryableClassifications,
				preflightapi.ClassificationDependencyDownloadFailed, preflightapi.ClassificationTimeout)
		}
	}
	recipeRetryCount := options.RetryCount
	if !options.RetryProvided {
		recipeRetryCount = 0
	}
	return preflightapi.CreateVerificationSessionRequest{
		SchemaVersion:  preflightapi.VerificationSchemaVersion,
		ProjectID:      projectID,
		IdempotencyKey: remote.IdempotencyKey,
		TaskID:         remote.TaskID,
		PredecessorID:  remote.PredecessorID,
		Intent:         intent,
		Goal:           strings.TrimSpace(options.Goal),
		Recipe: preflightapi.VerificationRecipe{
			SchemaVersion:  preflightapi.VerificationRecipeSchemaVersion,
			Rerunnable:     true,
			Check:          strings.TrimSpace(options.CheckName),
			ChangeMode:     bundle.Manifest.ChangeMode,
			RequestSecrets: options.RequestSecrets,
			RetryCount:     recipeRetryCount,
			RetryProvided:  options.RetryProvided,
			Source:         verificationSourceRecipe(buildOptions),
		},
		Snapshot: preflightapi.VerificationSnapshot{
			Digest:       bundle.Manifest.SnapshotDigest,
			GitHead:      bundle.Manifest.Git.Head,
			GitBranch:    bundle.Manifest.Git.Branch,
			Dirty:        bundle.Manifest.Git.Dirty,
			ChangeMode:   bundle.Manifest.ChangeMode,
			ChangedFiles: append([]string(nil), bundle.Manifest.ChangedFiles...),
		},
		Coverage: preflightapi.VerificationCoverage{MatchedFiles: matched, UncoveredFiles: uncovered},
		Plan: preflightapi.VerificationPlan{
			Authority:    preflightapi.VerificationPlanAuthorityClientProposed,
			MatrixGroups: matrixGroups,
			Nodes:        nodes,
		},
	}, nil
}

type preparedVerificationSession struct {
	ProjectID    string
	Plans        []checkRunPlan
	WarmPlans    []checkRunPlan
	MatchedFiles []string
	BuildOptions syncbundlebuild.BuildOptions
	CoverageMode string
}

func prepareVerificationSession(ctx context.Context, options verificationSessionOptions, intent string) (preparedVerificationSession, error) {
	loaded, err := loadProjectConfigOrInfer(".")
	if err != nil {
		return preparedVerificationSession{}, cliConfigError(err.Error(), err)
	}
	cfg := loaded.Config
	if err := validateExplicitDependencyWarmSecretRequest(cfg, options.CheckName, intent, options.RequestSecrets); err != nil {
		return preparedVerificationSession{}, cliConfigError(err.Error(), err)
	}
	projectID := strings.TrimSpace(options.ProjectID)
	if projectID == "" {
		projectID = cfg.ProjectID
	}
	projectID, err = executionProjectID(options.APIURL, projectID)
	if err != nil {
		return preparedVerificationSession{}, err
	}
	resolve := agentResolveOptionsForIntent(intent, checkResolveOptions{
		APIURL: options.APIURL, ProjectID: projectID, Changes: options.Changes,
		SkipWarm:   options.SkipWarm,
		RetryCount: options.RetryCount, RetryProvided: options.RetryProvided,
	})
	prepared := preparedVerificationSession{ProjectID: projectID, CoverageMode: "check"}
	if shouldUseAffectedRules(cfg, options.CheckName) && !agentIntentRequestsBroadVerification(intent, options.Goal) {
		selected, report, _, err := buildAgentAffectedRulePlans(ctx, ".", cfg, loaded.Path, resolve)
		if err != nil {
			return preparedVerificationSession{}, err
		}
		selected, report = agentAugmentAffectedPlansForIntent(cfg, selected, report, intent, resolve)
		selected = applyAgentAffectedPlanOptionsForIntent(intent, options.RequestSecrets, selected)
		warmSelected, err := agentDependencyWarmPlansForAffected(".", selected, cfg, resolve, intent)
		if err != nil {
			return preparedVerificationSession{}, err
		}
		for _, item := range warmSelected {
			prepared.WarmPlans = append(prepared.WarmPlans, item.Plan)
		}
		includeUntracked := report.Sync.IncludeUntracked
		sessionPlans := append(append(make([]affectedCheckPlan, 0, len(warmSelected)+len(selected)), warmSelected...), selected...)
		prepared.BuildOptions, err = compatibleAffectedSessionBuildOptions(sessionPlans, syncbundlebuild.BuildOptions{
			ChangeMode: report.ChangeMode, ExcludePatterns: append([]string(nil), report.Sync.ExcludePatterns...), ReviewedFixtures: append([]syncbundle.ReviewedFixture(nil), report.Sync.ReviewedFixtures...), IncludeUntracked: &includeUntracked,
		})
		if err != nil {
			return preparedVerificationSession{}, err
		}
		for _, item := range selected {
			prepared.Plans = append(prepared.Plans, item.Plan)
			for _, reason := range item.Reasons {
				prepared.MatchedFiles = append(prepared.MatchedFiles, reason.Path)
			}
		}
		prepared.CoverageMode = "affected_rules"
		return prepared, nil
	}
	recommendation, err := recommendAgentCheckWithIntent(cfg, options.CheckName, options.Goal, intent)
	if err != nil {
		return preparedVerificationSession{}, err
	}
	plan, err := resolveCheckRunPlan(recommendation.Check, cfg, resolve)
	if err != nil {
		return preparedVerificationSession{}, err
	}
	applyAgentRemoteRunOptionsForIntent(intent, options.RequestSecrets, &plan.Options)
	prepared.BuildOptions = syncbundlebuild.BuildOptions{
		ChangeMode: plan.Options.ChangeMode, ExcludePatterns: append([]string(nil), plan.Options.SyncExclude...), ReviewedFixtures: append([]syncbundle.ReviewedFixture(nil), plan.Options.SyncReviewedFixtures...), IncludeUntracked: plan.Options.IncludeUntracked,
	}
	if explicitDependencyWarmRequested(cfg, options.CheckName, intent) {
		targets, targetErr := dependencyWarmTargetsForExplicitCheck(ctx, ".", cfg, loaded.Path, resolve, plan)
		if targetErr != nil {
			return preparedVerificationSession{}, targetErr
		}
		prepared.WarmPlans, err = dependencyWarmPlansForExplicitCheck(plan, targets, cfg, resolve)
		if err != nil {
			return preparedVerificationSession{}, err
		}
		prepared.BuildOptions, err = explicitDependencyWarmSessionBuildOptions(prepared.WarmPlans, targets, prepared.BuildOptions)
		if err != nil {
			return preparedVerificationSession{}, err
		}
		return prepared, nil
	}
	prepared.Plans = []checkRunPlan{plan}
	warmSelected, err := agentDependencyWarmPlansForAffected(".", []affectedCheckPlan{{Plan: plan}}, cfg, resolve, intent)
	if err != nil {
		return preparedVerificationSession{}, err
	}
	for _, selected := range warmSelected {
		prepared.WarmPlans = append(prepared.WarmPlans, selected.Plan)
	}
	prepared.BuildOptions, err = compatibleAffectedSessionBuildOptions(
		append(warmSelected, affectedCheckPlan{Plan: plan}), prepared.BuildOptions,
	)
	if err != nil {
		return preparedVerificationSession{}, err
	}
	return prepared, nil
}
