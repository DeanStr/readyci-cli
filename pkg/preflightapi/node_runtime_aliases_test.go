package preflightapi

import (
	"strings"
	"testing"
	"time"
)

func TestNodeAliasImportUsesReviewedLTSMajor(t *testing.T) {
	catalog := NodeRuntimeAliasCatalogMetadata()
	record, err := ResolveRuntimeAlias("node", "lts/*", catalog.ReviewedAt.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if record.Requirement != "24" || record.Policy != RuntimeResolutionPolicySetupNodeLTSImport || record.CatalogDigest != NodeRuntimeAliasCatalogDigest() {
		t.Fatalf("unexpected import: %+v", record)
	}
	// Selection must use observed LTS releases, never forecast a newer series.
	if major, _, err := nodeRuntimeAliasRelease(catalog); err != nil || major != record.Requirement {
		t.Fatalf("manifest LTS %s disagrees with resolved LTS %s: %v", major, record.Requirement, err)
	}
	for _, now := range []time.Time{catalog.ReviewedAt.Add(-time.Nanosecond), catalog.ValidBefore, catalog.ValidBefore.Add(24 * time.Hour)} {
		if _, err := ResolveRuntimeAlias("node", "lts/*", now); err == nil {
			t.Fatalf("import accepted outside review interval: %s", now)
		}
	}
	for _, selector := range []string{"lts/*", "lts/jod", "latest", "node"} {
		if _, err := NormalizeRuntimeRequirements(map[string]string{"node": selector}); err == nil {
			t.Fatalf("numeric execution contract accepted %q", selector)
		}
	}
	for _, tc := range [][2]string{{"node", "lts/jod"}, {"go", "lts/*"}} {
		if _, err := ResolveRuntimeAlias(tc[0], tc[1], catalog.ReviewedAt); err == nil {
			t.Fatalf("unreviewed alias accepted: %v", tc)
		}
	}
}

func TestNodeAliasImportChangesMajorOnlyWithObservedLTSRelease(t *testing.T) {
	catalog := NodeRuntimeAliasCatalogMetadata()
	// Even a schedule that says the transition happened cannot establish LTS.
	catalog.Series = []NodeRuntimeAliasSeries{{Major: 26, LTSStart: "2020-01-01"}}
	record, err := canonicalRuntimeAliasFromCatalog("node", "lts/*", catalog)
	if err != nil || record.Requirement != "24" {
		t.Fatalf("schedule overrode observed manifest: %+v %v", record, err)
	}
	catalog.Releases = append(catalog.Releases, NodeRuntimeAliasRelease{Version: "26.13.0", Stable: true, LTS: "Next"})
	record, err = canonicalRuntimeAliasFromCatalog("node", "lts/*", catalog)
	if err != nil || record.Requirement != "26" {
		t.Fatalf("observed LTS release was not selected: %+v %v", record, err)
	}
}

func TestNodeAliasImportRequiresReviewAcrossScheduledTransition(t *testing.T) {
	catalog := NodeRuntimeAliasCatalogMetadata()
	transition := nextUnobservedNodeLTSTransition(t, catalog)
	if catalog.ValidBefore.After(transition.Add(-24 * time.Hour)) {
		t.Fatalf("catalog validity %s reaches an unreviewed LTS transition", catalog.ValidBefore)
	}
	// A new timestamp cannot skip a transition that the manifest still does
	// not establish. Only observing a newer stable LTS release moves the guard.
	laterReview := catalog
	laterReview.ReviewedAt = transition.Add(72 * time.Hour)
	if got := nextUnobservedNodeLTSTransition(t, laterReview); !got.Equal(transition) {
		t.Fatalf("later review skipped an unobserved transition: %s -> %s", transition, got)
	}
	for _, tc := range []struct {
		name string
		now  time.Time
	}{
		{"before transition", transition.Add(-24 * time.Hour)},
		{"transition day", transition.Add(12 * time.Hour)},
		{"three days later", transition.Add(72 * time.Hour)},
		{"months later", transition.AddDate(0, 6, 0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record, err := ResolveRuntimeAlias("node", "lts/*", tc.now)
			if err == nil || !strings.Contains(err.Error(), "explicit numeric runtime") {
				t.Fatalf("unreviewed transition resolved: %+v %v", record, err)
			}
		})
	}
}

func nextUnobservedNodeLTSTransition(t *testing.T, catalog NodeRuntimeAliasCatalog) time.Time {
	t.Helper()
	observedMajor := 0
	for _, release := range catalog.Releases {
		if release.Stable && release.LTS != "" {
			version, err := parseRuntimeVersion(release.Version)
			if err != nil {
				t.Fatal(err)
			}
			observedMajor = max(observedMajor, version.parts[0])
		}
	}
	var transition time.Time
	for _, series := range catalog.Series {
		start, err := time.Parse("2006-01-02", series.LTSStart)
		if err != nil {
			t.Fatal(err)
		}
		if series.Major > observedMajor && (transition.IsZero() || start.Before(transition)) {
			transition = start
		}
	}
	if transition.IsZero() {
		t.Fatal("reviewed schedule must bound the next unobserved LTS transition")
	}
	return transition
}

func TestNodeAliasImportUsesOrdinaryNumericRuntimePolicy(t *testing.T) {
	record, err := ResolveRuntimeAlias("node", "lts/*", NodeRuntimeAliasCatalogMetadata().ReviewedAt)
	if err != nil {
		t.Fatal(err)
	}
	spec := RunSpec{RuntimeRequirements: map[string]string{"node": record.Requirement}, NetworkMode: "allowlist", NetworkAllowlist: []string{NodeRuntimeDownloadHost}}
	got, err := ResolveRuntimePreparation(spec, nil, []string{NodeRuntimeProviderCapability()})
	if err != nil || got.NodeArchive == nil || !RuntimeVersionSatisfies(got.NodeArchive.Version, record.Requirement) {
		t.Fatalf("numeric preparation: %+v %v", got, err)
	}
}

func TestVerificationPlanEmptyNodesNormalizationPreservesSealedDigest(t *testing.T) {
	for _, test := range []struct {
		name  string
		nodes []VerificationNodePlan
	}{
		{name: "nil nodes"},
		{name: "explicit empty nodes", nodes: []VerificationNodePlan{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := VerificationPlan{Nodes: test.nodes}
			if err := normalizeVerificationPlan(&plan, "fixture"); err != nil {
				t.Fatal(err)
			}
			firstDigest, err := VerificationPlanDigest(plan)
			if err != nil {
				t.Fatal(err)
			}
			for iteration := 0; iteration < 3; iteration++ {
				if err := normalizeVerificationPlan(&plan, "fixture"); err != nil {
					t.Fatal(err)
				}
				if plan.Nodes == nil {
					t.Fatal("canonical empty nodes changed from [] to null")
				}
				digest, err := VerificationPlanDigest(plan)
				if err != nil || digest != firstDigest {
					t.Fatalf("normalization changed a sealed empty-plan digest: %s %s %v", firstDigest, digest, err)
				}
			}
		})
	}
}
