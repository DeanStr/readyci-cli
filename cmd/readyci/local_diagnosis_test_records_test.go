package main

import (
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestLocalDiagnosisIgnoresCompletedPlaywrightRecords(t *testing.T) {
	exitCode := 1
	for _, marker := range []string{"-", "✓"} {
		t.Run(marker, func(t *testing.T) {
			analysis := localAnalyzeRunFailure(preflightapi.StatusFailed, "scripts/release-gate.sh", &exitCode, []preflightapi.RunEvent{
				{Type: preflightapi.EventStdout, Data: marker + "  546 [mobile-chrome] › tests/e2e/user-stories.spec.ts:860:3 › operator failure inspection controls (2.7s)\n"},
				{Type: preflightapi.EventStderr, Data: "direct-upload canary bundle format version differs from cli/pkg/syncbundle\nreadyci: cache filesystem usage after command failed\n"},
			}, "")
			if analysis.PrimaryFile != "" || !strings.Contains(analysis.Summary, "bundle format version differs") {
				t.Fatalf("analysis = %+v, want terminal mismatch without a completed/skipped test path", analysis)
			}
		})
	}
}

func TestLocalDiagnosisFiltersMultiplePlaywrightRowsBeforeLaterFileFailure(t *testing.T) {
	exitCode := 1
	analysis := localAnalyzeRunFailure(preflightapi.StatusFailed, "scripts/release-gate.sh", &exitCode, []preflightapi.RunEvent{
		{Type: preflightapi.EventStdout, Data: "✓  1 [chromium] › tests/earlier.spec.ts:1:1 › failure recovery (1.0s)\n-  2 [chromium] › tests/skipped.spec.ts:2:2 › error handling\n"},
		{Type: preflightapi.EventStderr, Data: "scripts/check.go:19:3: actual version differs from required version\n"},
	}, "")
	if analysis.PrimaryFile != "scripts/check.go" || !strings.Contains(analysis.Summary, "scripts/check.go:19:3") {
		t.Fatalf("analysis = %+v, want later actual failure", analysis)
	}
}
