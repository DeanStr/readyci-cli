package diagnosiscommon

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

const scratchTestOperation = "mkdir /tmp/hugo-integration-test1: no space left on device"
const scratchTestActualError = `e"` + scratchTestOperation + `"`
const scratchTestWrapper = "        error:\n          got non-nil error\n        got:\n          " + scratchTestActualError + "\n"
const scratchTestDFHeader = "Filesystem 1-blocks Used Available Capacity Mounted on\n"
const scratchTestFullRow = "/dev/vdc 33501757440 33484980224 0 100% /tmp\n"
const scratchTestPressure = scratchTestDFHeader + scratchTestFullRow

func scratchTestLog(id int64, stream, output string) preflightapi.RunEvent {
	kind := preflightapi.EventStdout
	if stream == "stderr" {
		kind = preflightapi.EventStderr
	}
	return preflightapi.RunEvent{ID: id, Type: kind, RunID: "run-scratch", Phase: "command", Step: "check", Data: output}
}

func scratchTestFailureEvents(output []preflightapi.RunEvent) []preflightapi.RunEvent {
	code := 1
	return append(output,
		preflightapi.RunEvent{ID: 9001, Type: preflightapi.EventStderr, RunID: "run-scratch", Phase: "command", Step: "check", Message: preflightapi.CommandFailureScopeMessage, ExitCode: &code},
		preflightapi.RunEvent{ID: 9002, Type: preflightapi.EventCommandExited, RunID: "run-scratch", ExitCode: &code},
	)
}

func TestWrappedScratchStorageFailureRetainsCauseAndFilesystemEvidence(t *testing.T) {
	code := 1
	events := scratchTestFailureEvents([]preflightapi.RunEvent{
		scratchTestLog(11, "stdout", scratchTestWrapper),
		scratchTestLog(21, "stderr", scratchTestPressure),
	})
	failure := EnvironmentPrerequisiteFailureFromEvents(events, &code)
	if failure == nil {
		t.Fatal("actual failed scratch operation and matching zero-byte filesystem were not diagnosed")
	}
	if !strings.Contains(strings.ToLower(failure.Summary), "observed "+scratchTestOperation) || !strings.Contains(strings.ToLower(failure.Summary), "inspect") {
		t.Errorf("summary = %q, want observed operation and setup inspection", failure.Summary)
	}
	if failure.Evidence.EventID != 11 || failure.Evidence.Stream != "stdout" || failure.Evidence.Line != 4 || failure.Evidence.Text != scratchTestActualError || failure.Evidence.Path != "" {
		t.Errorf("cause evidence = %+v, want original actual-error location without a source target", failure.Evidence)
	}
	if len(failure.SupportingEvidence) != 1 {
		t.Fatalf("supporting evidence = %+v, want the corroborating filesystem row", failure.SupportingEvidence)
	}
	support := failure.SupportingEvidence[0]
	if support.EventID != 21 || support.Stream != "stderr" || support.Line != 2 || support.Text != strings.TrimSpace(scratchTestFullRow) || support.Path != "" {
		t.Errorf("filesystem evidence = %+v, want original df row location without a source target", support)
	}
	if code != 1 || *events[len(events)-1].ExitCode != 1 {
		t.Error("diagnosis changed the native failure exit")
	}
}

func TestWrappedScratchStorageFailureRespectsEveryChunkBoundary(t *testing.T) {
	code := 1
	for name, output := range map[string]string{"assertion": scratchTestWrapper, "filesystem": scratchTestPressure} {
		for split := 1; split < len(output); split++ {
			t.Run(fmt.Sprintf("%s/%d", name, split), func(t *testing.T) {
				stream := "stdout"
				if name == "filesystem" {
					stream = "stderr"
				}
				first, second := scratchTestLog(11, stream, output[:split]), scratchTestLog(12, stream, "")
				second.DataBase64 = base64.StdEncoding.EncodeToString([]byte(output[split:]))
				events := []preflightapi.RunEvent{first, second}
				if name == "assertion" {
					events = append(events, scratchTestLog(21, "stderr", scratchTestPressure))
				} else {
					events = append([]preflightapi.RunEvent{scratchTestLog(10, "stdout", scratchTestWrapper)}, events...)
				}
				failure := EnvironmentPrerequisiteFailureFromEvents(scratchTestFailureEvents(events), &code)
				if failure == nil || failure.Evidence.Text != scratchTestActualError || len(failure.SupportingEvidence) != 1 || failure.SupportingEvidence[0].Text != strings.TrimSpace(scratchTestFullRow) {
					t.Fatalf("transport split lost actual cause or filesystem corroboration: %+v", failure)
				}
				causeID, causeLine, capacityID, capacityLine := int64(10), 4, int64(21), 2
				lineStart := len(scratchTestDFHeader)
				if name == "assertion" {
					lineStart = strings.Index(output, "          e\"")
				}
				id, line := int64(11), strings.Count(output[:lineStart], "\n")+1
				if split <= lineStart {
					id, line = 12, strings.Count(output[split:lineStart], "\n")+1
				}
				if name == "assertion" {
					causeID, causeLine = id, line
				} else {
					capacityID, capacityLine = id, line
				}
				if failure.Evidence.EventID != causeID || failure.Evidence.Line != causeLine || failure.SupportingEvidence[0].EventID != capacityID || failure.SupportingEvidence[0].Line != capacityLine {
					t.Fatalf("split evidence lost original first-byte locations: cause=%+v capacity=%+v, want %d:%d and %d:%d", failure.Evidence, failure.SupportingEvidence[0], causeID, causeLine, capacityID, capacityLine)
				}
			})
		}
	}
}

func TestWrappedScratchStorageFailureRejectsFixturesAndNonactualErrors(t *testing.T) {
	code := 1
	for name, wrapper := range map[string]string{
		"bare quoted value":      scratchTestActualError + "\n",
		"expected value":         "        expected:\n          " + scratchTestActualError + "\n",
		"wanted value":           strings.Replace(scratchTestWrapper, "got:", "want:", 1),
		"quoted block":           strings.ReplaceAll(scratchTestWrapper, "        ", ">       "),
		"nil actual error":       strings.Replace(scratchTestWrapper, "got non-nil error", "got nil error", 1),
		"wrong value type":       strings.Replace(scratchTestWrapper, scratchTestActualError, `"`+scratchTestOperation+`"`, 1),
		"unrelated error text":   strings.Replace(scratchTestWrapper, scratchTestOperation, "fixture expects no space left on device", 1),
		"cache operation":        strings.ReplaceAll(scratchTestWrapper, "/tmp/", "/cache/"),
		"unclean path":           strings.ReplaceAll(scratchTestWrapper, "/tmp/hugo", "/tmp/../tmp/hugo"),
		"different path prefix":  strings.ReplaceAll(scratchTestWrapper, "/tmp/", "/tmpx/"),
		"outdented error":        strings.Replace(scratchTestWrapper, "          e", "        e", 1),
		"overindented error":     strings.Replace(scratchTestWrapper, "          e", "            e", 1),
		"outdented error marker": strings.Replace(scratchTestWrapper, "          got non-nil error", "        got non-nil error", 1),
		"different block indent": strings.Replace(scratchTestWrapper, "        got:", "      got:", 1),
		"intervening text":       strings.Replace(scratchTestWrapper, "        got:", "fixture output\n        got:", 1),
	} {
		t.Run(name, func(t *testing.T) {
			failure := EnvironmentPrerequisiteFailureFromEvents(scratchTestFailureEvents([]preflightapi.RunEvent{
				scratchTestLog(11, "stdout", wrapper), scratchTestLog(21, "stderr", scratchTestPressure),
			}), &code)
			if failure != nil {
				t.Fatalf("fixture or nonactual error established scratch exhaustion: %+v", failure)
			}
		})
	}
}

func TestWrappedScratchStorageFailureRequiresMatchingCurrentBytePressure(t *testing.T) {
	code := 1
	for name, pressure := range map[string]string{
		"missing corroboration":      "",
		"only cache full":            scratchTestDFHeader + "/dev/vdb 1000 1000 0 100% /cache\n",
		"root alone full":            scratchTestDFHeader + "/dev/root 1000 1000 0 100% /\n",
		"prefix is not mount parent": scratchTestDFHeader + "/dev/vdc 1000 1000 0 100% /tmpx\n",
		"healthy tmp":                scratchTestDFHeader + "/dev/vdc 1000 999 1 100% /tmp\n",
		"root full tmp healthy":      scratchTestDFHeader + "/dev/root 1000 1000 0 100% /\n/dev/vdc 1000 900 100 90% /tmp\n",
		"later tmp recovery":         scratchTestPressure + scratchTestDFHeader + "/dev/vdc 1000 500 500 50% /tmp\n",
		"inode table":                "Filesystem Inodes IUsed IFree IUse% Mounted on\n/dev/vdc 1000 1000 0 100% /tmp\n",
		"inode table after bytes":    scratchTestDFHeader + "Filesystem Inodes IUsed IFree IUse% Mounted on\n/dev/vdc 1000 1000 0 100% /tmp\n",
		"human units":                "Filesystem Size Used Avail Use% Mounted on\n/dev/vdc 32G 32G 0 100% /tmp\n",
		"missing header":             scratchTestFullRow,
		"row after unrelated text":   scratchTestDFHeader + "expected output:\n" + scratchTestFullRow,
		"invalid integer":            scratchTestDFHeader + "/dev/vdc 1000 broken 0 100% /tmp\n",
		"overflow integer":           scratchTestDFHeader + "/dev/vdc 18446744073709551616 1000 0 100% /tmp\n",
		"negative available":         scratchTestDFHeader + "/dev/vdc 1000 1000 -1 100% /tmp\n",
		"zero total":                 scratchTestDFHeader + "/dev/vdc 0 0 0 100% /tmp\n",
		"used exceeds total":         scratchTestDFHeader + "/dev/vdc 1000 1001 0 100% /tmp\n",
		"invalid percentage":         scratchTestDFHeader + "/dev/vdc 1000 1000 0 101% /tmp\n",
		"unmounted path":             scratchTestDFHeader + "/dev/vdc 1000 1000 0 100% tmp\n",
	} {
		t.Run(name, func(t *testing.T) {
			failure := EnvironmentPrerequisiteFailureFromEvents(scratchTestFailureEvents([]preflightapi.RunEvent{
				scratchTestLog(11, "stdout", scratchTestWrapper), scratchTestLog(21, "stderr", pressure),
			}), &code)
			if failure != nil {
				t.Fatalf("unrelated, stale or invalid pressure established scratch exhaustion: %+v", failure)
			}
		})
	}
	deep := strings.ReplaceAll(scratchTestWrapper, "/tmp/hugo-integration-test1", "/tmp/deep/failed")
	for name, rows := range map[string]string{
		"healthy deeper mount after full tmp":  scratchTestFullRow + "/dev/deep 1000 500 500 50% /tmp/deep\n",
		"healthy deeper mount before full tmp": "/dev/deep 1000 500 500 50% /tmp/deep\n" + scratchTestFullRow,
		"unknown deeper mount before full tmp": "/dev/deep 1000 broken 0 100% /tmp/deep\n" + scratchTestDFHeader + scratchTestFullRow,
		"unknown deeper mount after full tmp":  scratchTestFullRow + "/dev/deep 1000 broken 0 100% /tmp/deep\n",
		"malformed other row before deeper":    "/dev/other 1000 broken 0 100% /other\n/dev/deep 1000 500 500 50% /tmp/deep\n" + scratchTestDFHeader + scratchTestFullRow,
		"wrong-shape row before deeper":        "/dev/other 1000 broken 50% /other\n/dev/deep 1000 500 500 50% /tmp/deep\n" + scratchTestDFHeader + scratchTestFullRow,
		"inode-only deeper mount":              scratchTestFullRow + "Filesystem Inodes IUsed IFree IUse% Mounted on\n/dev/deep 1000 500 500 50% /tmp/deep\n",
		"human-unit deeper mount":              scratchTestFullRow + "Filesystem Size Used Avail Use% Mounted on\n/dev/deep 1G 500M 500M 50% /tmp/deep\n",
	} {
		t.Run(name, func(t *testing.T) {
			if failure := EnvironmentPrerequisiteFailureFromEvents(scratchTestFailureEvents([]preflightapi.RunEvent{
				scratchTestLog(11, "stdout", deep), scratchTestLog(21, "stderr", scratchTestDFHeader+rows),
			}), &code); failure != nil {
				t.Fatalf("full parent filesystem overrode healthy deeper mount: %+v", failure)
			}
		})
	}
	t.Run("pre-error healthy deeper mount", func(t *testing.T) {
		if failure := EnvironmentPrerequisiteFailureFromEvents(scratchTestFailureEvents([]preflightapi.RunEvent{
			scratchTestLog(10, "stderr", scratchTestDFHeader+"/dev/deep 1000 500 500 50% /tmp/deep\n"),
			scratchTestLog(11, "stdout", deep),
			scratchTestLog(21, "stderr", scratchTestPressure),
		}), &code); failure != nil {
			t.Fatalf("full parent filesystem overrode the observed deeper mount topology: %+v", failure)
		}
	})
}

func TestWrappedScratchStorageFailureRejectsBrokenOutputAndScopeMixing(t *testing.T) {
	code := 1
	const prefix = "        error:\n          got non-nil error\n"
	const suffix = "        got:\n          " + scratchTestActualError + "\n"
	for name, logs := range map[string][]preflightapi.RunEvent{
		"different streams":                    {scratchTestLog(11, "stdout", prefix), scratchTestLog(12, "stderr", suffix), scratchTestLog(21, "stderr", scratchTestPressure)},
		"damaged assertion bytes":              {scratchTestLog(11, "stdout", prefix), {ID: 12, Type: preflightapi.EventStdout, RunID: "run-scratch", Phase: "command", Step: "check", DataBase64: "!invalid"}, scratchTestLog(13, "stdout", suffix), scratchTestLog(21, "stderr", scratchTestPressure)},
		"oversized assertion line":             {scratchTestLog(11, "stdout", prefix+strings.Repeat("x", 8193)+"\n"+suffix), scratchTestLog(21, "stderr", scratchTestPressure)},
		"oversized df line":                    {scratchTestLog(11, "stdout", scratchTestWrapper), scratchTestLog(21, "stderr", scratchTestDFHeader+strings.Repeat("x", 8193)+"\n"+scratchTestFullRow)},
		"damaged df bytes":                     {scratchTestLog(11, "stdout", scratchTestWrapper), scratchTestLog(21, "stderr", scratchTestDFHeader), {ID: 22, Type: preflightapi.EventStderr, RunID: "run-scratch", Phase: "command", Step: "check", DataBase64: "!invalid"}, scratchTestLog(23, "stderr", scratchTestFullRow)},
		"pressure before operation":            {scratchTestLog(21, "stderr", scratchTestPressure), scratchTestLog(11, "stdout", scratchTestWrapper)},
		"same-event pressure before operation": {scratchTestLog(11, "stdout", scratchTestPressure+scratchTestWrapper)},
		"oversized hidden deeper row":          {scratchTestLog(11, "stdout", scratchTestWrapper), scratchTestLog(21, "stderr", scratchTestDFHeader+strings.Repeat("x", 8193)+" /tmp/deep\n"+scratchTestPressure)},
		"trailing oversized topology row":      {scratchTestLog(11, "stdout", scratchTestWrapper), scratchTestLog(21, "stderr", scratchTestPressure+strings.Repeat("x", 8193)+" /tmp/deep\n")},
		"trailing oversized partial row":       {scratchTestLog(11, "stdout", scratchTestWrapper), scratchTestLog(21, "stderr", scratchTestPressure+strings.Repeat("x", 8193))},
		"trailing damaged bytes":               {scratchTestLog(11, "stdout", scratchTestWrapper), scratchTestLog(21, "stderr", scratchTestPressure), {ID: 22, Type: preflightapi.EventStderr, RunID: "run-scratch", Phase: "command", Step: "check", DataBase64: "!invalid"}},
	} {
		t.Run(name, func(t *testing.T) {
			if failure := EnvironmentPrerequisiteFailureFromEvents(scratchTestFailureEvents(logs), &code); failure != nil {
				t.Fatalf("broken or temporally unrelated output established exhaustion: %+v", failure)
			}
		})
	}
	for _, field := range []string{"phase", "step", "run", "service", "invocation"} {
		t.Run(field, func(t *testing.T) {
			first, second := scratchTestLog(11, "stdout", prefix), scratchTestLog(12, "stdout", suffix)
			var between []preflightapi.RunEvent
			switch field {
			case "phase":
				first.Phase = "prepare"
			case "step":
				first.Step = "other"
			case "run":
				first.RunID = "other-run"
			case "service":
				first.Service = "helper"
			case "invocation":
				zero := 0
				between = []preflightapi.RunEvent{{Type: preflightapi.EventCommandExited, RunID: "run-scratch", ExitCode: &zero}, {Type: preflightapi.EventCommandStarted, RunID: "run-scratch"}}
			}
			logs := append([]preflightapi.RunEvent{first}, between...)
			logs = append(logs, second, scratchTestLog(21, "stderr", scratchTestPressure))
			if failure := EnvironmentPrerequisiteFailureFromEvents(scratchTestFailureEvents(logs), &code); failure != nil {
				t.Fatalf("output from different %s values was combined: %+v", field, failure)
			}
		})
	}
	for _, field := range []string{"phase", "step", "run", "service"} {
		t.Run("corroboration/"+field, func(t *testing.T) {
			pressure := scratchTestLog(21, "stderr", scratchTestPressure)
			switch field {
			case "phase":
				pressure.Phase = "always"
			case "step":
				pressure.Step = "other"
			case "run":
				pressure.RunID = "other-run"
			case "service":
				pressure.Service = "helper"
			}
			if failure := EnvironmentPrerequisiteFailureFromEvents(scratchTestFailureEvents([]preflightapi.RunEvent{
				scratchTestLog(11, "stdout", scratchTestWrapper), pressure,
			}), &code); failure != nil {
				t.Fatalf("filesystem pressure from a different %s corroborated the operation: %+v", field, failure)
			}
		})
	}
	for name, initial := range map[string]string{"split assertion": prefix, "split corroboration": scratchTestWrapper} {
		t.Run("A-to-B-to-A/"+name, func(t *testing.T) {
			other := scratchTestLog(15, "stdout", "unrelated scope completed\n")
			other.Step = "other"
			logs := []preflightapi.RunEvent{scratchTestLog(11, "stdout", initial), other}
			if name == "split assertion" {
				logs = append(logs, scratchTestLog(12, "stdout", suffix))
			}
			logs = append(logs, scratchTestLog(21, "stderr", scratchTestPressure))
			if failure := EnvironmentPrerequisiteFailureFromEvents(scratchTestFailureEvents(logs), &code); failure != nil {
				t.Fatalf("explicit marker hid an intervening scope: %+v", failure)
			}
		})
	}
}

func TestWrappedScratchStorageFailureRequiresNonzeroKnownExitAndBounds(t *testing.T) {
	zero, code := 0, 1
	logs := []preflightapi.RunEvent{scratchTestLog(11, "stdout", scratchTestWrapper), scratchTestLog(21, "stderr", scratchTestPressure)}
	for name, exit := range map[string]*int{"unknown exit": nil, "successful exit": &zero} {
		t.Run(name, func(t *testing.T) {
			if failure := EnvironmentPrerequisiteFailureFromEvents(scratchTestFailureEvents(logs), exit); failure != nil {
				t.Fatalf("exit did not establish failure: %+v", failure)
			}
		})
	}
	for name, extra := range map[string]string{
		"operations":         strings.Repeat(scratchTestWrapper, 257),
		"mount observations": strings.Repeat(scratchTestFullRow, 257),
	} {
		t.Run(name, func(t *testing.T) {
			wrapper, pressure := scratchTestWrapper, scratchTestPressure
			if name == "operations" {
				wrapper = extra
			} else {
				pressure = scratchTestDFHeader + extra
			}
			if failure := EnvironmentPrerequisiteFailureFromEvents(scratchTestFailureEvents([]preflightapi.RunEvent{
				scratchTestLog(11, "stdout", wrapper), scratchTestLog(21, "stderr", pressure),
			}), &code); failure != nil {
				t.Fatalf("bounded parser selected partial evidence after too many %s: %+v", name, failure)
			}
		})
	}
	t.Run("too many sources", func(t *testing.T) {
		var logs []preflightapi.RunEvent
		for index := 0; index < 65; index++ {
			event := scratchTestLog(int64(index+100), "stdout", "unrelated output\n")
			event.RunID = fmt.Sprintf("other-run-%d", index)
			logs = append(logs, event)
		}
		logs = append(logs, scratchTestLog(11, "stdout", scratchTestWrapper), scratchTestLog(21, "stderr", scratchTestPressure))
		if failure := EnvironmentPrerequisiteFailureFromEvents(scratchTestFailureEvents(logs), &code); failure != nil {
			t.Fatalf("parser ignored exceeded source bound and selected partial evidence: %+v", failure)
		}
	})
}

func TestScratchLinePositionsBoundsUnterminatedScopeEpochs(t *testing.T) {
	var events []preflightapi.RunEvent
	for index := 0; index < 65; index++ {
		event := scratchTestLog(int64(index+1), "stdout", "unfinished")
		event.Step = fmt.Sprintf("step-%d", index)
		events = append(events, event)
	}
	if _, valid := scratchLinePositions(events); valid {
		t.Fatal("unterminated scope epochs bypassed the bounded line-state prepass")
	}
}
