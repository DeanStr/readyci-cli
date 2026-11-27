package main

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/internal/syncbundlebuild"
	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/verificationplan"
)

// A secret check the service would refuse is a configuration error found
// while resolving it, so dry runs report it and real runs exit as config
// errors rather than internal ones.
func TestSecretCheckSourceLimitsAreConfigErrors(t *testing.T) {
	prepare := make([]string, verificationplan.MaxCommandSourcePhaseCommands+1)
	for index := range prepare {
		prepare[index] = "\"echo " + strconv.Itoa(index) + "\""
	}
	cfg, err := config.ParseProjectConfig([]byte("version: 1\ncache:\n  mode: cache-only\n  paths: [~/.cache/go-build]\nchecks:\n  integration:\n    command: go test ./...\n    prepare: ["+strings.Join(prepare, ", ")+"]\n"), "limits")
	if err != nil {
		t.Fatal(err)
	}
	isConfigError := func(err error) bool {
		var cliErr cliError
		return errors.As(err, &cliErr) && cliErr.Code == "config_invalid" && errors.Is(err, verificationplan.ErrInvalidCommandSource)
	}
	if _, err := resolveCheckRunPlan("integration", cfg, checkResolveOptions{RequestSecrets: true}); !isConfigError(err) {
		t.Fatalf("resolving the secret check = %v, want a config_invalid source error", err)
	}
	plan, err := resolveCheckRunPlan("integration", cfg, checkResolveOptions{})
	if err != nil {
		t.Fatalf("the same check without secrets: %v", err)
	}
	// Plans that reach node construction without that check map the same way.
	plan.Options.SecretsRequested = boolPtr(true)
	if _, _, err := verificationNodesForPlans(nil, []checkRunPlan{plan}, syncbundlebuild.BuildResult{}); !isConfigError(err) {
		t.Fatalf("building nodes = %v, want a config_invalid source error", err)
	}
}

func TestAgentPlanRejectsSecretChecksTheServiceWouldRefuse(t *testing.T) {
	t.Setenv("READYCI_API_URL", "")
	t.Setenv("READYCI_PROJECT_ID", "")
	cliTestWorkingDirectoryMu.Lock()
	defer cliTestWorkingDirectoryMu.Unlock()
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatal(err)
		}
	}()
	prepare := make([]string, verificationplan.MaxCommandSourcePhaseCommands+1)
	for index := range prepare {
		prepare[index] = "\"echo " + strconv.Itoa(index) + "\""
	}
	if err := os.WriteFile(".readyci.yml", []byte("project_id: proj_story\ncache:\n  mode: cache-only\n  paths: [~/.cache/go-build]\nchecks:\n  touched:\n    command: go test ./...\n    changes: working\n    prepare: ["+strings.Join(prepare, ", ")+"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = captureStdoutForCLITest(t, func() error {
		return agentPlanCommand([]string{"--json", "--check", "touched", "--request-secrets"})
	})
	var cliErr cliError
	if !errors.As(err, &cliErr) || cliErr.Code != "config_invalid" || !strings.Contains(err.Error(), "prepare must contain at most") {
		t.Fatalf("agent plan for an over-limit secret check = %v, want config_invalid", err)
	}
	if _, err := captureStdoutForCLITest(t, func() error {
		return agentPlanCommand([]string{"--json", "--check", "touched"})
	}); err != nil {
		t.Fatalf("the same plan without secrets: %v", err)
	}
	// The affected-plan path applies the same check per selected plan.
	plan, err := resolveCheckRunPlan("touched", mustLoadProjectConfigForTest(t), checkResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	plan.Options.SecretsRequested = boolPtr(true)
	if err := validateAgentSecretSources(plan); !errors.As(err, &cliErr) || cliErr.Code != "config_invalid" {
		t.Fatalf("validateAgentSecretSources = %v, want config_invalid", err)
	}
}

func mustLoadProjectConfigForTest(t *testing.T) config.ProjectConfig {
	t.Helper()
	cfg, _, err := config.LoadProjectConfig(".")
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}
