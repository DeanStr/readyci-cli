package main

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/internal/syncbundlebuild"
	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func TestReviewedFixtureHashFailureIsConfigurationError(t *testing.T) {
	repo := chdirCLITestRepo(t, "reviewed-fixture-diagnostics")
	writeFileForRepoMapTest(t, filepath.Join(repo, "fixtures/.env"), "READYCI_TEST_FIXTURE=public-placeholder\n")
	runGitForCLITest(t, "init")
	runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "public fixture")
	cfg := config.ProjectConfig{Sync: config.SyncConfig{ReviewedFixtures: []syncbundle.ReviewedFixture{{Path: "fixtures/.env", SHA256: strings.Repeat("0", 64)}}}}
	_, err := buildSyncDryRunReport(context.Background(), repo, cfg, "", syncDryRunOptions{})
	if err == nil || !strings.Contains(err.Error(), "reviewed sha256") {
		t.Fatalf("changed fixture did not fail explicit source review: %v", err)
	}
	normalized := normalizeCLIError(err)
	if normalized.Classification != preflightapi.ClassificationConfigInvalid || normalized.ExitCode != cliExitUsageOrConfig || normalized.Retryable {
		t.Fatalf("fixture review failure must be a non-retryable configuration error: %+v", normalized)
	}
}

func TestRegisteredFixturePolicyReplacesCandidateAllowances(t *testing.T) {
	trusted := syncbundle.ReviewedFixture{Path: "tests/.env", SHA256: strings.Repeat("a", 64)}
	candidate := syncbundle.ReviewedFixture{Path: ".env.private", SHA256: strings.Repeat("b", 64)}
	active := syncbundle.SourcePolicyBinding{
		Digest:                          "sha256:" + strings.Repeat("c", 64),
		DefaultExcludePatterns:          syncbundle.DefaultSourceExcludePatterns(),
		DefaultUntrackedExcludePatterns: syncbundle.DefaultUntrackedExcludePatterns(),
		ReviewedFixtures:                []syncbundle.ReviewedFixture{trusted},
		IncludeUntracked:                true,
	}
	actual, err := registeredPolicyBuildOptions(syncbundlebuild.BuildOptions{
		ChangeMode: "working", ReviewedFixtures: []syncbundle.ReviewedFixture{candidate},
	}, &active)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual.ReviewedFixtures, []syncbundle.ReviewedFixture{trusted}) ||
		!reflect.DeepEqual(actual.DefaultUntrackedExcludePatterns, active.DefaultUntrackedExcludePatterns) {
		t.Fatalf("candidate changed trusted source allowances: %+v", actual)
	}
	actual.ReviewedFixtures[0] = candidate
	if active.ReviewedFixtures[0] != trusted {
		t.Fatal("resolved fixture allowances alias the registered policy")
	}
}

func TestVerificationSourceRecipePreservesReviewedFixturesForCurrentness(t *testing.T) {
	include, projectIgnore := true, false
	want := syncbundlebuild.BuildOptions{
		ChangeMode: "working", IncludeUntracked: &include, UseProjectIgnore: &projectIgnore,
		ExcludePatterns:                 []string{"private/**"},
		DefaultExcludePatterns:          syncbundle.DefaultSourceExcludePatterns(),
		DefaultUntrackedExcludePatterns: syncbundle.DefaultUntrackedExcludePatterns(),
		ReviewedFixtures:                []syncbundle.ReviewedFixture{{Path: "tests/.env", SHA256: strings.Repeat("a", 64)}},
		SourcePolicyDigest:              "sha256:" + strings.Repeat("b", 64),
	}
	recipe := preflightapi.VerificationRecipe{
		SchemaVersion: preflightapi.VerificationRecipeSchemaVersion,
		ChangeMode:    "working", Source: verificationSourceRecipe(want),
	}
	var err error
	recipe.Digest, err = preflightapi.VerificationRecipeDigest(recipe)
	if err != nil {
		t.Fatal(err)
	}
	got, err := verificationSourceBuildOptions(recipe)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("replayed source contract = %+v, want %+v", got, want)
	}
}

func TestOneSessionRejectsDifferentReviewedFixtureHashes(t *testing.T) {
	base := affectedCheckPlan{Plan: checkRunPlan{Name: "first", Options: remoteRunOptions{
		SyncReviewedFixtures: []syncbundle.ReviewedFixture{{Path: "tests/.env", SHA256: strings.Repeat("a", 64)}},
	}}}
	other := affectedCheckPlan{Plan: checkRunPlan{Name: "second", Options: remoteRunOptions{
		SyncReviewedFixtures: []syncbundle.ReviewedFixture{{Path: "tests/.env", SHA256: strings.Repeat("b", 64)}},
	}}}
	if _, err := compatibleAffectedSessionBuildOptions([]affectedCheckPlan{base, other}, syncbundlebuild.BuildOptions{}); err == nil || !strings.Contains(err.Error(), "reviewed_fixtures") {
		t.Fatalf("different fixture hashes accepted for one workspace: %v", err)
	}
	first := config.SyncConfig{ReviewedFixtures: base.Plan.Options.SyncReviewedFixtures}
	second := config.SyncConfig{ReviewedFixtures: other.Plan.Options.SyncReviewedFixtures}
	if affectedSyncConfigKey(first) == affectedSyncConfigKey(second) {
		t.Fatal("affected path filter cache aliases different fixture hashes")
	}
}

func TestVerificationSourceKeysBindGeneratedDefaultsAndFixtureOrder(t *testing.T) {
	first := syncbundlebuild.BuildOptions{DefaultUntrackedExcludePatterns: []string{"dist"}, ReviewedFixtures: []syncbundle.ReviewedFixture{
		{Path: "tests/z.env", SHA256: strings.Repeat("a", 64)},
		{Path: "tests/a.env", SHA256: strings.Repeat("b", 64)},
	}}
	other := first
	other.ReviewedFixtures = []syncbundle.ReviewedFixture{first.ReviewedFixtures[1], first.ReviewedFixtures[0]}
	if verificationBuildOptionsKey(first) != verificationBuildOptionsKey(other) {
		t.Fatal("fixture declaration order changes an otherwise identical source filter")
	}
	other.DefaultUntrackedExcludePatterns = []string{"target"}
	if verificationBuildOptionsKey(first) == verificationBuildOptionsKey(other) {
		t.Fatal("different generated-output defaults share a source filter key")
	}
}
