package diagnosiscommon

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/guestcontract"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

const retentionWarningRecord = "readyci: artifact collection retained 2 of 3 selected files; 1 omitted; details in .readyci/artifact-retention.json"

func TestPostRunArtifactWarningRejectsMalformedMetadata(t *testing.T) {
	code := 1
	for _, event := range []preflightapi.RunEvent{
		{Type: preflightapi.EventStderr, Phase: "command", Data: retentionWarningRecord + "\n"},
		{Type: preflightapi.EventStderr, Step: "cleanup", Data: retentionWarningRecord + "\n"},
		{Type: preflightapi.EventStderr, Service: "helper", Data: retentionWarningRecord + "\n"},
		{Type: preflightapi.EventStdout, Data: retentionWarningRecord + "\n"},
		{Type: preflightapi.EventStderr, Status: preflightapi.StatusFailed, Data: retentionWarningRecord + "\n"},
		{Type: preflightapi.EventStderr, ExitCode: &code, Data: retentionWarningRecord + "\n"},
		{Type: preflightapi.EventStderr, Message: "another diagnostic", Data: retentionWarningRecord + "\n"},
		{Type: preflightapi.EventStderr, Data: "unknown output", DataBase64: base64.StdEncoding.EncodeToString([]byte(retentionWarningRecord + "\n"))},
		{Type: preflightapi.EventStderr, DataBase64: "!invalid"},
	} {
		if PostRunArtifactWarning(event) {
			t.Fatalf("customer or inconsistent metadata treated as worker collection warning: %+v", event)
		}
	}
}

func TestPostRunArtifactWarningRejectsInconsistentCountsAndPaths(t *testing.T) {
	code := 1
	for _, text := range []string{
		strings.Replace(retentionWarningRecord, "1 omitted", "2 omitted", 1),
		strings.Replace(retentionWarningRecord, "2 of 3", "4 of 3", 1),
		strings.Replace(retentionWarningRecord, "2 of 3", "02 of 3", 1),
		strings.Replace(retentionWarningRecord, "2 of 3", "-1 of 3", 1),
		strings.Replace(retentionWarningRecord, "2 of 3", "2 of 4294967296", 1),
		strings.Replace(retentionWarningRecord, "2 of 3 selected files; 1 omitted", "2 of 100001 selected files; 99999 omitted", 1),
		strings.Replace(retentionWarningRecord, "2 of 3 selected files; 1 omitted", "10001 of 10002 selected files; 1 omitted", 1),
		strings.Replace(retentionWarningRecord, "2 of 3 selected files; 1 omitted", "3 of 3 selected files; 0 omitted", 1),
		strings.Replace(retentionWarningRecord, ".readyci/artifact-retention.json", ".readyci/../artifact-retention.json", 1),
		retentionWarningRecord + "\nother command failed\n",
		retentionWarningRecord + "\n\n",
		retentionWarningRecord + "\r\n",
		retentionWarningRecord + " ",
		guestcontract.FailedWorkspaceArtifactSkippedMessage + " (customer text)",
	} {
		t.Run(text, func(t *testing.T) {
			warning := preflightapi.RunEvent{Type: preflightapi.EventStderr, Data: text}
			if PostRunArtifactWarning(warning) {
				t.Fatal("malformed worker-like output was recognized")
			}
			events := []preflightapi.RunEvent{
				{ID: 1, Type: preflightapi.EventStderr, Phase: "prepare", Step: "install", Data: "fatal: preparation failed\n"},
				warning,
				{Type: preflightapi.EventCommandExited, ExitCode: &code},
			}
			if scope := FinalCommandFailureEvents(events); len(scope) != 0 && scope[0].ID == 1 {
				t.Fatalf("malformed warning resurrected earlier scope: %+v", scope)
			}
		})
	}
}

func TestPostRunArtifactWarningCanonicalCountsAndScopeMetadata(t *testing.T) {
	for _, text := range []string{
		retentionWarningRecord,
		strings.Replace(retentionWarningRecord, "2 of 3 selected files; 1 omitted", "0 of 1 selected files; 1 omitted", 1),
		strings.Replace(retentionWarningRecord, "2 of 3 selected files; 1 omitted", "10000 of 100000 selected files; 90000 omitted", 1),
		guestcontract.FailedWorkspaceArtifactSkippedMessage,
		"readyci: warning: artifact path \"report.xml\" matched no files after the run",
	} {
		for _, encoded := range []bool{false, true} {
			event := preflightapi.RunEvent{Type: preflightapi.EventStderr, Data: text + "\n"}
			if encoded {
				event.Data, event.DataBase64 = "", base64.StdEncoding.EncodeToString([]byte(event.Data))
			}
			if !PostRunArtifactWarning(event) {
				t.Fatalf("canonical worker warning not recognized: %+v", event)
			}
		}
	}
	code := 1
	for _, event := range []preflightapi.RunEvent{
		{ID: 2, Type: preflightapi.EventStderr, Phase: "command", Data: retentionWarningRecord + "\n"},
		{ID: 2, Type: preflightapi.EventStderr, Step: "cleanup", Data: retentionWarningRecord + "\n"},
		{ID: 2, RunID: "other_run", Type: preflightapi.EventStderr, Data: retentionWarningRecord + "\n"},
	} {
		events := []preflightapi.RunEvent{
			{ID: 1, RunID: "run_probe", Type: preflightapi.EventStderr, Phase: "prepare", Step: "install", Data: "fatal: preparation failed\n"},
			event,
			{RunID: "run_probe", Type: preflightapi.EventCommandExited, ExitCode: &code},
		}
		if scope := FinalCommandFailureEvents(events); len(scope) != 0 && scope[0].ID == 1 {
			t.Fatalf("tagged or mismatched output resurrected earlier scope: %+v", scope)
		}
	}
}

func TestPostRunArtifactWarningsPreserveFailureScope(t *testing.T) {
	code := 32
	warnings := []string{
		"readyci: artifact collection retained 2 of 3 selected files; 1 omitted; details in .readyci/artifact-retention.json",
		guestcontract.FailedWorkspaceArtifactSkippedMessage,
	}
	for _, warning := range warnings {
		t.Run(warning, func(t *testing.T) {
			events := []preflightapi.RunEvent{
				{ID: 1, RunID: "run_probe", Type: preflightapi.EventStderr, Phase: "prepare", Step: "filesystem", Data: "mount: /btrfs: unknown filesystem type 'btrfs'.\n"},
				{ID: 2, RunID: "run_probe", Type: preflightapi.EventStderr, Data: warning + "\n"},
				{ID: 3, RunID: "run_probe", Type: preflightapi.EventCommandExited, ExitCode: &code},
			}
			scope := FinalCommandFailureEvents(events)
			if len(scope) == 0 || scope[0].ID != 1 {
				t.Fatalf("artifact warning replaced failed scope: %+v", scope)
			}
		})
	}
}
