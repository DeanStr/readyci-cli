package main

import (
	"runtime"
	"strings"

	"github.com/DeanStr/readyci-cli/internal/syncbundlebuild"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func buildRemoteRunRequest(options remoteRunOptions, bundle syncbundlebuild.BuildResult, command, projectID, idempotencyKey string) preflightapi.CreateRunRequest {
	artifactPaths := append([]string(nil), options.ArtifactPaths...)
	if options.KeepFailed {
		artifactPaths = appendUniqueString(artifactPaths, preflightapi.FailedWorkspaceArtifactPath)
	}
	createCommand, createSteps := createRunCommandAndSteps(options, command)
	repository := gitRepository()
	cacheMode := defaultString(options.CacheMode, "cache-only")
	cacheLaneSizeBytes := cacheLaneSizeForMode(cacheMode, options.CacheLaneSizeBytes)
	networkMode := defaultString(strings.TrimSpace(options.NetworkMode), preflightapi.DefaultNetworkMode)
	request := preflightapi.CreateRunRequest{
		ProjectID:             projectID,
		Region:                createRunRequestRegion(options.Region),
		IdempotencyKey:        idempotencyKey,
		RunGroupKey:           options.RunGroupKey,
		RunGroupKind:          options.RunGroupKind,
		RunGroupName:          options.RunGroupName,
		RunGroupPredecessorID: options.RunGroupPredecessorID,
		LogicalChildID:        options.LogicalChildID,
		LogicalChildName:      options.LogicalChildName,
		MatrixVariant:         options.MatrixVariant,
		Attempt:               options.Attempt,
		Image:                 defaultString(strings.TrimSpace(options.Image), preflightapi.DefaultImage),
		Command:               createCommand,
		CommandAlias:          options.CommandAlias,
		Steps:                 createSteps,
		ConcurrencyGroup:      options.ConcurrencyGroup,
		ConcurrencyLimit:      options.ConcurrencyLimit,
		RunnerSize:            defaultString(options.RunnerSize, "standard"),
		TimeoutSeconds:        defaultInt(options.TimeoutSeconds, defaultRunTimeoutSeconds),
		CacheMode:             cacheMode,
		CacheLaneSizeBytes:    cacheLaneSizeBytes,
		ArtifactPaths:         artifactPaths,
		NetworkMode:           networkMode,
		NetworkAllowlist:      append([]string(nil), options.NetworkAllowlist...),
		Docker:                options.Docker,
		Services:              cloneRunServices(options.Services),
		WorkerSelector:        copyStringMap(options.WorkerSelector),
		SnapshotDigest:        bundle.Manifest.SnapshotDigest,
		BundleBaselineKey:     bundle.Manifest.BaselineKey,
		SecretsRequested:      options.SecretsRequested,
		Client: preflightapi.ClientInfo{
			Version:  version,
			Platform: runtime.GOOS + "-" + runtime.GOARCH,
		},
		Git: preflightapi.GitInfo{
			Branch:          bundle.Manifest.Git.Branch,
			Head:            bundle.Manifest.Git.Head,
			Dirty:           bundle.Manifest.Git.Dirty,
			Materialization: "minimal_git",
			Repository:      repository,
		},
	}
	return request
}
