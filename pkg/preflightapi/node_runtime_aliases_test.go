package preflightapi

import (
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
	catalog.Releases = append(catalog.Releases, NodeRuntimeAliasRelease{Version: "28.1.0", Stable: true}, NodeRuntimeAliasRelease{Version: "26.13.0", Stable: true, LTS: "Next"})
	major, latest, err := nodeRuntimeAliasRelease(catalog)
	if err != nil || major != "26" || latest != "26.13.0" {
		t.Fatalf("LTS selection: %s %s %v", major, latest, err)
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
