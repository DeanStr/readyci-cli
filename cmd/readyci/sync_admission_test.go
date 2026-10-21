package main

import (
	"fmt"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func TestSourceAdmissionErrorsHaveConfigurationRecovery(t *testing.T) {
	err := normalizeCLIError(fmt.Errorf("prepare source: %w", &syncbundle.AdmissionError{
		Resource: "manifest_bytes", Observed: 5000000, Limit: syncbundle.MaxManifestBytes,
	}))
	if err.Code != "config_invalid" || err.Classification != preflightapi.ClassificationConfigInvalid || err.ExitCode != cliExitUsageOrConfig || err.Retryable {
		t.Fatalf("source budget failure must be an actionable non-retryable configuration failure: %+v", err)
	}
	if len(err.NextActions) != 1 || err.NextActions[0].Command != "readyci sync --dry-run --json" {
		t.Fatalf("source budget failure lost its preview recovery command: %+v", err)
	}
}
