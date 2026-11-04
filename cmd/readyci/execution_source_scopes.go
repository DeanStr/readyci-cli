package main

import (
	"context"
	"strings"

	"github.com/DeanStr/readyci-cli/internal/syncbundlebuild"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

// Requested execution can use different views without acquiring verification
// authority. Every view is sealed from one upload and validated by the service.
func executionSessionBuildOptions(plans []checkRunPlan, fallback syncbundlebuild.BuildOptions) (syncbundlebuild.BuildOptions, error) {
	var selected []affectedCheckPlan
	for _, plan := range plans {
		selected = append(selected, affectedCheckPlan{Plan: plan})
	}
	options, err := registeredAffectedSessionBuildOptions(selected, fallback)
	if err != nil || !options.RequireRegisteredScopes {
		return options, err
	}
	ignore, err := syncbundlebuild.ProjectIgnorePatterns(context.Background(), ".")
	if err != nil {
		return options, err
	}
	var exclusions []string
	for _, pattern := range ignore {
		pattern = strings.TrimSpace(pattern)
		if pattern != "" && !strings.HasPrefix(pattern, "#") {
			exclusions = append(exclusions, pattern)
		}
	}
	const placeholder = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	scopes := make([]syncbundle.SourcePolicyBinding, len(plans))
	for i, plan := range plans {
		build := canonicalVerificationBuildOptions(plan.Options, options.ChangeMode)
		scopes[i] = syncbundle.SourcePolicyBinding{
			Digest: placeholder, ExcludePatterns: append(build.ExcludePatterns, exclusions...),
			DefaultExcludePatterns: build.DefaultExcludePatterns, DefaultUntrackedExcludePatterns: build.DefaultUntrackedExcludePatterns,
			ReviewedFixtures: build.ReviewedFixtures, IncludeUntracked: *build.IncludeUntracked,
		}
		scopes[i], err = syncbundle.NormalizeSourcePolicyBinding(scopes[i])
		if err != nil {
			return options, err
		}
	}
	digest, err := syncbundle.RequestedSourcePolicyDigest(scopes)
	if err != nil {
		return options, err
	}
	for i := range scopes {
		scopes[i].Digest = digest
	}
	binding, err := syncbundle.NormalizeSourcePolicyBinding(syncbundle.SourcePolicyBinding{Digest: digest, Scopes: scopes})
	if err != nil {
		return options, err
	}
	for i := range plans {
		plans[i].SourcePolicy = nil
		if len(binding.Scopes) > 0 {
			plans[i].SourcePolicy = &scopes[i]
		}
	}
	no := false
	return syncbundlebuild.BuildOptions{
		ChangeMode: options.ChangeMode, SourceScopes: binding.Scopes, SourcePolicyDigest: binding.Digest,
		ExcludePatterns: binding.ExcludePatterns, DefaultExcludePatterns: binding.DefaultExcludePatterns,
		DefaultUntrackedExcludePatterns: binding.DefaultUntrackedExcludePatterns,
		ReviewedFixtures:                binding.ReviewedFixtures, IncludeUntracked: &binding.IncludeUntracked,
		UseProjectIgnore: &no, SealSourceViews: true,
	}, nil
}
