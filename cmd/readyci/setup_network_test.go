package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

func TestSetupNetworkOverrideMatchesPrintPreviewSaveAndPlan(t *testing.T) {
	options, runs := newGuidedSetupReviewFixture(t)
	writeFileForRepoMapTest(t, filepath.Join(".github", "workflows", "tests.yml"), `jobs:
  unit:
    runs-on: ubuntu-latest
    steps:
      - run: go test ./...
  integration:
    runs-on: ubuntu-latest
    services:
      db:
        image: postgres:16
    steps:
      - run: go test -tags=integration ./...
`)
	options.ImportCandidate = "github-actions:.github/workflows/tests.yml:unit"
	options.NetworkAllowlist = []string{"example.com", "proxy.golang.org"}
	printed, err := captureStdoutForCLITest(t, func() error {
		return setupCommand([]string{"--print", "--project", options.ProjectID, "--candidate", options.ImportCandidate, "--network-allowlist", "EXAMPLE.com,proxy.golang.org,example.com"})
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := buildSetupReport(options)
	if err != nil {
		t.Fatal(err)
	}
	if report.ProposedConfig != printed || !strings.Contains(printed, "# Not imported: \"github-actions:.github/workflows/tests.yml:integration\"") {
		t.Fatal("preview/print differ or omitted upstream scope disappeared")
	}
	if report.Environment == nil || !reflect.DeepEqual(report.Environment.NetworkAllowlist, options.NetworkAllowlist) || report.Environment.NetworkReview.PolicySource != "explicit_setup_override" || report.Environment.NetworkReview.TestEndpointStatus != "not_verified" {
		t.Fatalf("network provenance was lost or overstated: %+v", report.Environment)
	}
	if !strings.Contains(strings.Join(report.Next, "\n"), "--network-allowlist example.com,proxy.golang.org") || !containsString(report.Environment.NetworkReview.InferredDependencyAllowlist, "dl.google.com") {
		t.Fatal("suggested command lost override or inferred dependency hosts were conflated with effective policy")
	}
	if _, err := os.Stat(".readyci.yml"); !os.IsNotExist(err) || runs.Load() != 0 {
		t.Fatal("preview wrote or executed the configuration")
	}
	options.WriteConfig = true
	if _, err := buildSetupReport(options); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(".readyci.yml")
	if err != nil || string(saved) != printed {
		t.Fatalf("saved configuration differs from reviewed policy: %v", err)
	}
	cfg, _, err := config.LoadProjectConfig(".")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := resolveCheckRunPlan("unit", cfg, checkResolveOptions{ProjectID: options.ProjectID, RetryCount: -1})
	if err != nil || plan.Options.NetworkMode != "allowlist" || !reflect.DeepEqual(plan.Options.NetworkAllowlist, options.NetworkAllowlist) {
		t.Fatalf("execution policy differs from reviewed override: %v %+v", err, plan.Options)
	}
}

func TestSetupNetworkOverrideRejectsInvalidOrConflictingInputBeforeWrites(t *testing.T) {
	for _, args := range [][]string{
		{"--network-allowlist", ""},
		{"--network-allowlist", ",,,"},
		{"--network-allowlist", "*.example.com"},
		{"--network-allowlist", "127.0.0.1"},
		{"--network-allowlist", "example.com", "--profile", "strict"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			chdirCLITestRepo(t, "network-invalid")
			if err := setupCommand(append([]string{"--write-config"}, args...)); err == nil {
				t.Fatalf("invalid override accepted: %v", args)
			}
			if _, err := os.Stat(".readyci.yml"); !os.IsNotExist(err) {
				t.Fatal("invalid override wrote configuration")
			}
		})
	}
}

func TestSetupNetworkOverrideRequiresExistingConfigRegeneration(t *testing.T) {
	options, runs := newGuidedSetupReviewFixture(t)
	original := "version: 1\nproject_id: consent\nchecks:\n  old:\n    command: echo old\n    network_mode: allowlist\n    network_allowlist: [old.example.com]\n"
	writeFileForRepoMapTest(t, ".readyci.yml", original)
	options.NetworkAllowlist = []string{"example.com"}
	if _, err := buildSetupReport(options); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("existing policy was silently overridden: %v", err)
	}
	options.Force, options.WriteConfig = true, true
	if _, err := buildSetupReport(options); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.LoadProjectConfig(".")
	if err != nil || !reflect.DeepEqual(cfg.Network.Allowlist, options.NetworkAllowlist) || len(cfg.Checks) != 1 || cfg.Checks["old"].Command != "" || runs.Load() != 0 {
		t.Fatalf("regeneration inherited old check policy or executed checks: %+v %v", cfg, err)
	}
}

func TestGuidedSetupNetworkOverrideSavesReviewedBytes(t *testing.T) {
	options, runs := newGuidedSetupReviewFixture(t)
	options.NetworkAllowlist = []string{"example.com"}
	var output bytes.Buffer
	if err := guidedSetup(options, strings.NewReader("y\nn\n"), &output, false); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(".readyci.yml")
	if err != nil || !strings.Contains(output.String(), string(saved)) || runs.Load() != 0 {
		t.Fatalf("guided review differs from saved policy or executed after refusal: %v", err)
	}
	cfg, _, err := config.LoadProjectConfig(".")
	if err != nil || !reflect.DeepEqual(cfg.Network.Allowlist, options.NetworkAllowlist) {
		t.Fatalf("guided policy lost: %+v %v", cfg.Network, err)
	}
}

func TestGuidedSetupNetworkOverrideRetainsUnresolvedCandidateSelection(t *testing.T) {
	options, runs := newGuidedSetupReviewFixture(t)
	options.NetworkAllowlist = []string{"example.com"}
	writeFileForRepoMapTest(t, filepath.Join(".github", "workflows", "tests.yml"), "jobs:\n  integration:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test -tags=integration ./...\n  unit:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test ./...\n")
	report, err := buildSetupReport(options)
	if err != nil || len(report.ImportCandidates) != 2 || report.ImportScope != nil || !strings.Contains(report.SelectionReason, "upstream jobs not imported or covered") ||
		!strings.Contains(report.ProposedConfig, `# Not imported: "github-actions:.github/workflows/tests.yml:unit"`) || !strings.Contains(report.ProposedConfig, `# Not imported: "github-actions:.github/workflows/tests.yml:integration"`) {
		t.Fatalf("network override discarded unresolved scope: %+v %v", report, err)
	}
	var output bytes.Buffer
	if err := guidedSetup(options, strings.NewReader("2\ny\nn\n"), &output, false); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.LoadProjectConfig(".")
	if err != nil || len(cfg.Checks) != 1 || len(cfg.Checks["unit"].Steps) != 1 || !reflect.DeepEqual(cfg.Network.Allowlist, options.NetworkAllowlist) || runs.Load() != 0 {
		t.Fatalf("guided selected scope or reviewed network changed: %+v %v", cfg, err)
	}
}
