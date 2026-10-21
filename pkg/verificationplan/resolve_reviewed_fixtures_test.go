package verificationplan

import (
	"reflect"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func TestResolveCheckCarriesProjectAndCheckReviewedFixtures(t *testing.T) {
	projectFixture := syncbundle.ReviewedFixture{Path: "tests/z/.env", SHA256: strings.Repeat("a", 64)}
	checkFixture := syncbundle.ReviewedFixture{Path: "tests/a/.env", SHA256: strings.Repeat("b", 64)}
	cfg := config.ProjectConfig{
		Sync:   config.SyncConfig{ReviewedFixtures: []syncbundle.ReviewedFixture{projectFixture}},
		Checks: map[string]config.CheckConfig{"test": {Command: "run-tests", Sync: config.SyncConfig{ReviewedFixtures: []syncbundle.ReviewedFixture{checkFixture}}}},
	}
	plan, err := ResolveCheck("test", cfg, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if want := []syncbundle.ReviewedFixture{checkFixture, projectFixture}; !reflect.DeepEqual(plan.Run.SyncReviewedFixtures, want) {
		t.Fatalf("resolved fixture allowances = %+v, want %+v", plan.Run.SyncReviewedFixtures, want)
	}
	plan.Run.SyncReviewedFixtures[0].SHA256 = strings.Repeat("c", 64)
	if cfg.Checks["test"].Sync.ReviewedFixtures[0] != checkFixture {
		t.Fatal("resolved fixture allowances alias the configuration")
	}
}

func TestResolveCheckRejectsConflictingReviewedFixtureHashes(t *testing.T) {
	cfg := config.ProjectConfig{
		Sync: config.SyncConfig{ReviewedFixtures: []syncbundle.ReviewedFixture{{Path: "tests/.env", SHA256: strings.Repeat("a", 64)}}},
		Checks: map[string]config.CheckConfig{"test": {Command: "run-tests", Sync: config.SyncConfig{
			ReviewedFixtures: []syncbundle.ReviewedFixture{{Path: "tests/.env", SHA256: strings.Repeat("b", 64)}},
		}}},
	}
	if _, err := ResolveCheck("test", cfg, ResolveOptions{}); err == nil || !strings.Contains(err.Error(), "sync.reviewed_fixtures") {
		t.Fatalf("conflicting project/check fixture declaration accepted: %v", err)
	}
}
