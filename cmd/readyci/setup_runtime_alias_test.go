package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/verificationplan"
)

func withReviewedAliasClock(t *testing.T, now time.Time) {
	t.Helper()
	prior := setupRuntimeResolutionNow
	priorResolver := setupRuntimeAliasResolve
	setupRuntimeResolutionNow = func() time.Time { return now }
	setupRuntimeAliasResolve = preflightapi.ResolveSetupRuntimeAlias
	t.Cleanup(func() { setupRuntimeResolutionNow = prior; setupRuntimeAliasResolve = priorResolver })
}

func TestPinnedVueAliasImportPreservesAuthenticCommandAndScope(t *testing.T) {
	withReviewedAliasClock(t, preflightapi.NodeRuntimeAliasCatalogMetadata().ReviewedAt.Add(time.Minute))
	const fixtureRoot = "testdata/setup-runtime-alias"
	data, err := os.ReadFile(filepath.Join(fixtureRoot, "provenance.json"))
	if err != nil {
		t.Fatal(err)
	}
	var sources []struct {
		Repository, Commit string
		SourcePath         string `json:"source_path"`
		SourceURL          string `json:"source_url"`
		SHA256             string `json:"sha256"`
		FixturePath        string `json:"fixture_path"`
	}
	if err := json.Unmarshal(data, &sources); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	if len(sources) != 3 {
		t.Fatal("authentic Vue source set changed")
	}
	for _, source := range sources {
		data, err := os.ReadFile(filepath.Join(fixtureRoot, source.FixturePath))
		if err != nil {
			t.Fatal(err)
		}
		if source.Repository != "vuejs/core" || source.Commit != "bf5f0dd877dd3c23ce85c2cbc1dd771567e84c8c" || source.SourceURL != "https://raw.githubusercontent.com/"+source.Repository+"/"+source.Commit+"/"+source.SourcePath || fmt.Sprintf("%x", sha256.Sum256(data)) != source.SHA256 {
			t.Fatal("authentic fixture identity changed")
		}
		writeFileForRepoMapTest(t, filepath.Join(repo, source.SourcePath), string(data))
	}
	candidates := setupImportCandidates(repo, "github-actions")
	const id = "github-actions:.github/workflows/test.yml:unit-test"
	selected, err := selectSetupImport(setupOptions{ImportCandidate: id}, candidates, nil, io.Discard, true)
	if err != nil {
		t.Fatal(err)
	}
	if selected.RuntimeResolutions["node"].Selector != "lts/*" || !preflightapi.RuntimeVersionSatisfies("24.18.0", selected.RuntimeRequirements["node"]) {
		t.Fatalf("alias declaration was lost: %+v", selected)
	}
	template := setupImportedTemplateWithScope(repo, setupOptions{Profile: "remote-ci"}, "fixture", *selected, candidates)
	if err := validateSetupRuntimeTemplate(repo, template); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.ParseProjectConfig([]byte(template), "authentic Vue import")
	if err != nil {
		t.Fatal(err)
	}
	check := cfg.Checks["unit-test"]
	if strings.Contains(template, "runtime_resolutions:") || !strings.Contains(template, "does not track future LTS changes") || !strings.Contains(strings.Join(selected.Requirements, ";"), "numeric series 24 at import time") {
		t.Fatal("generated configuration must disclose a numeric pin and retain provenance only in the report")
	}
	// A saved numeric check remains valid when new alias imports are blocked.
	setupRuntimeResolutionNow = func() time.Time { return preflightapi.NodeRuntimeAliasCatalogMetadata().ValidBefore.Add(time.Hour) }
	if err := validateSetupRuntimeTemplate(repo, template); err != nil {
		t.Fatalf("saved numeric pin expired with the import catalog: %v", err)
	}
	if len(check.Steps) != 2 || check.Steps[0].Command != "bash -e -c 'pnpm install'" || check.Steps[1].Command != "bash -e -c 'pnpm run test-unit'" || check.Steps[1].Env["CI"] != "true" || check.Steps[1].Env["PUPPETEER_SKIP_DOWNLOAD"] != "true" || strings.Contains(template, "test-unit --run") {
		t.Fatalf("upstream command or environment changed: %+v", check)
	}
	if !strings.Contains(template, "Not imported:") || len(selectedSetupImportScope(*selected, candidates).Omitted) == 0 {
		t.Fatal("sibling scope disappeared")
	}
	plan, err := verificationplan.ResolveCheck("unit-test", cfg, verificationplan.ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	nodes, _, err := verificationplan.NodesForPlans(nil, []verificationplan.CheckPlan{plan}, verificationplan.BundleIdentity{}, verificationplan.ClientIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || !preflightapi.RuntimeRequirementImplies(nodes[0].Run.RuntimeRequirements["node"], "24") {
		t.Fatal("sealed proposed node must use the explicit numeric requirement")
	}
}

func TestSetupAliasRequiresReviewedActionPolicyAndFreshCatalog(t *testing.T) {
	catalog := preflightapi.NodeRuntimeAliasCatalogMetadata()
	withReviewedAliasClock(t, catalog.ReviewedAt.Add(time.Minute))
	for _, test := range []struct{ name, action, extra string }{
		{"unreviewed action", "actions/setup-node@v7", ""},
		{"latest semantics", catalog.ReviewedActionRef, "\n          check-latest: true"},
		{"different architecture", catalog.ReviewedActionRef, "\n          architecture: arm64"},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := t.TempDir()
			writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/ci.yml"), "jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: "+test.action+"\n        with:\n          node-version: 'lts/*'"+test.extra+"\n      - run: node --test\n")
			candidates := setupImportCandidates(repo, "github-actions")
			if len(candidates) != 1 || len(candidates[0].Unsupported) == 0 {
				t.Fatalf("unsupported policy accepted: %+v", candidates)
			}
		})
	}
	setupRuntimeResolutionNow = func() time.Time { return catalog.ValidBefore }
	job := repoWorkflowJobYAML{Steps: []repoWorkflowStepYAML{{Uses: catalog.ReviewedActionRef, With: map[string]any{"node-version": "lts/*"}}}}
	_, resolutions, problems := setupWorkflowRuntimeRequirements(".", job)
	if len(resolutions) != 0 || len(problems) == 0 {
		t.Fatal("expired snapshot produced a fresh proposal")
	}
	setupRuntimeResolutionNow = func() time.Time { return catalog.ReviewedAt.Add(time.Minute) }
	job.Steps = append(job.Steps, repoWorkflowStepYAML{Uses: catalog.ReviewedActionRef, With: map[string]any{"node-version": "24.15.0"}})
	_, _, problems = setupWorkflowRuntimeRequirements(".", job)
	if !strings.Contains(strings.Join(problems, ";"), "sequential runtime changes") {
		t.Fatal("alias combined with a later runtime selection was silently flattened")
	}
}
