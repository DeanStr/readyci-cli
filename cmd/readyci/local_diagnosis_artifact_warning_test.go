package main

import (
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/guestcontract"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestLocalArtifactCollectionWarningsPreserveFailedCommand(t *testing.T) {
	warnings := []string{
		"readyci: artifact collection retained 2 of 3 selected files; 1 omitted; details in .readyci/artifact-retention.json",
		guestcontract.FailedWorkspaceArtifactSkippedMessage,
	}
	for _, tc := range []struct {
		name, phase, step, command, output, kind, path, summary string
		code                                                    int
	}{
		{"environment", "prepare", "filesystem", "prepare-filesystems && cargo nextest run", "mount: /btrfs: unknown filesystem type 'btrfs'.\n", preflightapi.ClassificationEnvironmentSetupFailed, "", "unknown filesystem type 'btrfs'", 32},
		{"compiler", "command", "compile", "go build ./...", "src/main.go:42: undefined: missing\n", preflightapi.ClassificationBuildFailed, "src/main.go", "undefined: missing", 1},
	} {
		for _, warning := range warnings {
			t.Run(tc.name+"/"+warning, func(t *testing.T) {
				events := []preflightapi.RunEvent{
					{ID: 1, RunID: "run_probe", Type: preflightapi.EventStderr, Phase: tc.phase, Step: tc.step, Data: tc.output},
					{ID: 2, RunID: "run_probe", Type: preflightapi.EventStderr, Data: warning + "\n"},
					{ID: 3, RunID: "run_probe", Type: preflightapi.EventCommandExited, ExitCode: &tc.code},
				}
				got := localAnalyzeRunFailure(preflightapi.StatusFailed, tc.command, &tc.code, events, "")
				if got.Classification != tc.kind || got.PrimaryFile != tc.path || !strings.Contains(got.Summary, tc.summary) {
					t.Fatalf("artifact warning replaced original failure: %+v, want %s/%q/%q", got, tc.kind, tc.path, tc.summary)
				}
				foundWarning := false
				for _, item := range got.Evidence {
					foundWarning = foundWarning || item.EventID == 2 && item.Text == warning
				}
				if !foundWarning {
					t.Fatalf("original artifact warning missing from supporting evidence: %+v", got.Evidence)
				}
			})
		}
	}
}
