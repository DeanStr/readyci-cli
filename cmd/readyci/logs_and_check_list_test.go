package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestRunLogEntriesFilterTailAndClassifyEvents(t *testing.T) {
	exitCode := 2
	when := time.Date(2026, 6, 28, 10, 30, 0, 123, time.UTC)
	events := []preflightapi.RunEvent{
		{ID: 10, Type: preflightapi.EventStdout, Timestamp: when, Phase: "test", Step: "unit", Data: "ok\n"},
		{ID: 11, Type: preflightapi.EventStderr, Timestamp: when, Phase: "test", Step: "unit", Data: "fail\n"},
		{ID: 12, Type: preflightapi.EventCommandExited, Timestamp: when, Phase: "test", Step: "unit", ExitCode: &exitCode},
		{ID: 13, Type: preflightapi.EventRunFinished, Timestamp: when, Phase: "deploy", Status: preflightapi.StatusSucceeded},
		{ID: 14, Type: preflightapi.EventLogTruncated, Timestamp: when},
		{ID: 15, Type: preflightapi.EventStdout, Timestamp: when, Data: "ModuleNotFoundError: No module named 'freezegun'\n"},
		{ID: 16, Type: "ignored", Timestamp: when},
	}

	entries := runLogEntries(events, false, runLogFilters{Phase: "test", Step: "unit"})
	if len(entries) != 3 {
		t.Fatalf("entries = %+v, want stdout, stderr, and exit entries", entries)
	}
	if entries[0].Seq != 10 || entries[0].Stream != "stdout" || entries[0].Text != "ok\n" || entries[0].Time != when.Format(time.RFC3339Nano) {
		t.Fatalf("stdout entry = %+v", entries[0])
	}
	if !isErrorLogEntry(entries[1]) || !isErrorLogEntry(entries[2]) {
		t.Fatalf("stderr/exit entries were not classified as errors: %+v", entries)
	}
	if got := tailLogEntries(entries, 2); len(got) != 2 || got[0].Seq != 11 || got[1].Seq != 12 {
		t.Fatalf("tail entries = %+v, want last two", got)
	}
	if got := tailLogEntries(entries, 0); len(got) != len(entries) {
		t.Fatalf("zero tail length = %d, want all entries", len(got))
	}

	errorsOnly := runLogEntries(events, true, runLogFilters{})
	if len(errorsOnly) != 4 || errorsOnly[0].Stream != "stderr" || errorsOnly[1].Event != preflightapi.EventCommandExited || errorsOnly[2].Event != preflightapi.EventLogTruncated || errorsOnly[3].Stream != "stdout" {
		t.Fatalf("errors only entries = %+v", errorsOnly)
	}
	if !eventsWereTruncated(events) || eventsWereTruncated(events[:3]) {
		t.Fatalf("eventsWereTruncated did not detect only the truncation event")
	}
}

func TestRunLogEntriesTrimFiltersBeforeMatching(t *testing.T) {
	events := []preflightapi.RunEvent{
		{ID: 10, Type: preflightapi.EventStdout, Phase: "test", Step: "unit", Service: "postgres", Data: "service log\n"},
		{ID: 11, Type: preflightapi.EventStdout, Phase: "test", Step: "unit", Data: "plain log\n"},
	}

	entries := runLogEntries(events, false, runLogFilters{Phase: " test ", Step: " unit ", Service: " postgres "})
	if len(entries) != 1 || entries[0].Text != "service log\n" {
		t.Fatalf("trimmed service filters entries = %+v, want postgres service log", entries)
	}

	entries = runLogEntries(events, false, runLogFilters{Phase: " test ", Step: " unit ", Service: " \t "})
	if len(entries) != 2 {
		t.Fatalf("whitespace-only service filter entries = %+v, want both unit logs", entries)
	}
}

func TestRunLogEntryFromEventHandlesDataAndMessages(t *testing.T) {
	entry, ok := runLogEntryFromEvent(1, preflightapi.RunEvent{Type: preflightapi.EventStdout, DataBase64: "bad-base64"})
	if !ok || entry.Event != preflightapi.EventStdout || entry.Stream != "" || !strings.Contains(entry.Message, "illegal base64") {
		t.Fatalf("invalid base64 entry = %+v/%t", entry, ok)
	}

	entry, ok = runLogEntryFromEvent(2, preflightapi.RunEvent{Type: preflightapi.EventLogTruncated})
	if !ok || entry.Message == "" || humanLogEventMessage(entry) == "" {
		t.Fatalf("truncation entry = %+v/%t", entry, ok)
	}

	entry, ok = runLogEntryFromEvent(3, preflightapi.RunEvent{Type: preflightapi.EventRunTimedOut, Status: preflightapi.StatusTimedOut})
	if !ok || humanLogEventMessage(entry) != "readyci: run "+preflightapi.StatusTimedOut {
		t.Fatalf("timeout message = %+v/%q", entry, humanLogEventMessage(entry))
	}

	if entry, ok := runLogEntryFromEvent(4, preflightapi.RunEvent{Type: "debug"}); ok || entry.Event != "" {
		t.Fatalf("ignored entry = %+v/%t, want empty/false", entry, ok)
	}
	if seq := eventLogSeq(preflightapi.RunEvent{}, 4); seq != 5 {
		t.Fatalf("fallback seq = %d, want index+1", seq)
	}
}

func TestRunLogEntrySanitizesTerminalControlSequences(t *testing.T) {
	entry, ok := runLogEntryFromEvent(1, preflightapi.RunEvent{
		Type:    preflightapi.EventStdout,
		Data:    "ok\x1b[31m red\x1b[0m\x1bPsecret\x1b\\ visible\u009b32m green\u009b0m\u0090hidden\u009c\x00\x08\nnext\tline",
		Message: "\x1b]0;title\x07hidden",
	})
	if !ok {
		t.Fatal("stdout event was not converted to a log entry")
	}
	if entry.Text != "ok red visible green\nnext\tline" {
		t.Fatalf("sanitized text = %q, want ANSI/control characters removed", entry.Text)
	}
	if entry.Message != "hidden" {
		t.Fatalf("sanitized message = %q, want OSC/control characters removed", entry.Message)
	}
}

func TestWriteHumanRunLogsFailureSummarySanitizesTerminalControlSequences(t *testing.T) {
	output, err := captureStdoutForCLITest(t, func() error {
		return writeHumanRunLogsFailureSummary(runLogsFailureSummaryReport{
			RunID:          "run_escape\x1b[31m",
			Status:         preflightapi.StatusFailed,
			Classification: preflightapi.ClassificationTestFailed,
			AgentAction:    preflightapi.AgentActionFixCode,
			FailureSummary: "\x1b]0;pwned\x07FAILED tests/test_escape.py\rrewritten",
			PrimaryFile:    "tests/test_escape.py\x1b[0m",
			Evidence: []preflightapi.RunFailureEvidence{{
				Stream: "stderr\x1b[31m",
				Line:   7,
				Path:   "tests/test_escape.py\x1b[0m",
				Text:   "AssertionError: \x1b[31mexpected\x1b[0m got 2 \x1b]8;;https://bad.example\x07link\x1b]8;;\x07\x00",
			}},
		})
	})
	if err != nil {
		t.Fatalf("write human summary: %v", err)
	}
	outputWithoutLineBreaks := strings.ReplaceAll(output, "\n", "")
	if strings.ContainsAny(outputWithoutLineBreaks, "\x00\x07\x1b\r") {
		t.Fatalf("human summary output contains terminal control characters: %q", output)
	}
	requireCLITestOutputContains(t, output,
		"failure_summary: FAILED tests/test_escape.py rewritten",
		"primary_file: tests/test_escape.py",
		"stderr:7 tests/test_escape.py: AssertionError: expected got 2 link",
	)
}

func TestWriteLogTruncationNoticeSanitizesMessage(t *testing.T) {
	output, err := captureStderrForCLITest(t, func() error {
		return writeLogTruncationNotice(preflightapi.RunEvent{
			Type:    preflightapi.EventLogTruncated,
			Message: "\x1b]0;title\x07logs incomplete\x00",
		})
	})
	if err != nil {
		t.Fatalf("writeLogTruncationNotice: %v", err)
	}
	if output != "logs incomplete\n" {
		t.Fatalf("truncation notice = %q, want sanitized message", output)
	}
}

func TestWriteJSONLLogsPrintsOneEntryPerLine(t *testing.T) {
	output, err := captureStdoutForCLITest(t, func() error {
		return writeJSONLLogs([]runLogEntry{
			{Seq: 1, Stream: "stdout", Text: "one\n"},
			{Seq: 2, Event: preflightapi.EventRunFailed, Message: "failed"},
		})
	})
	if err != nil {
		t.Fatalf("write jsonl logs: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 2 {
		t.Fatalf("jsonl output = %q, want two lines", output)
	}
	var first runLogEntry
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("decode first jsonl line: %v", err)
	}
	if first.Seq != 1 || first.Text != "one\n" {
		t.Fatalf("first jsonl entry = %+v", first)
	}
}

func TestRunsLogsSummaryPrioritizesDiagnosticEvidence(t *testing.T) {
	t.Setenv("READYCI_API_TOKEN", "")

	exitCode := 1
	events := []preflightapi.RunEvent{
		{
			ID:   10,
			Type: preflightapi.EventStdout,
			Data: "........................................ [100%]\n=================================== FAILURES ===================================\nFAILED tests/test_widget.py::test_one - AssertionError: expected 1 got 2\n",
		},
		{
			ID:   11,
			Type: preflightapi.EventStderr,
			Data: "readyci: retained failed workspace at /cache/failed-runs/run_py/workspace.tar.gz\n",
		},
		{ID: 12, Type: preflightapi.EventCommandExited, ExitCode: &exitCode},
		{ID: 13, Type: preflightapi.EventRunFailed, Status: preflightapi.StatusFailed},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer pfl_logs" {
			t.Fatalf("authorization = %q, want bearer token", got)
		}
		switch r.URL.Path {
		case "/v1/runs/run_py":
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunDetail{
				Summary: preflightapi.RunSummary{
					RunID:          "run_py",
					ProjectID:      "readyci",
					Status:         preflightapi.StatusFailed,
					ExitCode:       &exitCode,
					Classification: preflightapi.ClassificationTestFailed,
					AgentAction:    preflightapi.AgentActionFixCode,
					FailureSummary: "FAILED tests/test_widget.py::test_one",
					PrimaryFile:    "/usr/lib/python3.12/importlib/__init__.py",
				},
				Spec: preflightapi.RunSpec{
					RunID:   "run_py",
					Command: "pytest -q",
				},
			})
		case "/v1/runs/run_py/events":
			if r.URL.Query().Get("format") != "json" {
				t.Fatalf("events query = %s, want format=json", r.URL.RawQuery)
			}
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunEventsResponse{
				RunID:  "run_py",
				Events: events,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	output, err := captureStdoutForCLITest(t, func() error {
		return runsLogsCommand([]string{"--api-url", server.URL, "--token", "pfl_logs", "--summary", "run_py"})
	})
	if err != nil {
		t.Fatalf("runs logs --summary: %v", err)
	}
	requireCLITestOutputContains(t, output,
		"run_id: run_py",
		"classification: test_failed",
		"agent_action: fix_code",
		"failure_summary: FAILED tests/test_widget.py::test_one",
		"primary_file: tests/test_widget.py",
		"FAILED tests/test_widget.py::test_one - AssertionError: expected 1 got 2",
	)
	if strings.Contains(output, "retained failed workspace") {
		t.Fatalf("summary output included low-signal lifecycle stderr:\n%s", output)
	}

	jsonOutput, err := captureStdoutForCLITest(t, func() error {
		return runsLogsCommand([]string{"--api-url", server.URL, "--token", "pfl_logs", "--summary", "--json", "run_py"})
	})
	if err != nil {
		t.Fatalf("runs logs --summary --json: %v", err)
	}
	var report runLogsFailureSummaryReport
	if err := json.Unmarshal([]byte(jsonOutput), &report); err != nil {
		t.Fatalf("decode summary json: %v\n%s", err, jsonOutput)
	}
	if report.RunID != "run_py" || report.Classification != preflightapi.ClassificationTestFailed || report.PrimaryFile != "tests/test_widget.py" {
		t.Fatalf("summary report = %+v", report)
	}
	if len(report.Evidence) == 0 || !strings.Contains(report.Evidence[0].Text, "FAILED tests/test_widget.py::test_one") {
		t.Fatalf("summary evidence = %+v, want failing test first", report.Evidence)
	}
}

func TestRunsLogsSummaryFallsBackToDiagnosticTail(t *testing.T) {
	t.Setenv("READYCI_API_TOKEN", "")

	exitCode := 1
	sawFallbackTail := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/runs/run_big":
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunDetail{
				Summary: preflightapi.RunSummary{
					RunID:          "run_big",
					Status:         preflightapi.StatusFailed,
					ExitCode:       &exitCode,
					Classification: preflightapi.ClassificationDependencySetupFailed,
					FailureSummary: "Missing Python dependency: hypothesis.",
				},
				Spec: preflightapi.RunSpec{
					RunID:   "run_big",
					Command: "pytest -q",
				},
			})
		case "/v1/runs/run_big/events":
			if r.URL.Query().Get("tail") == strconv.Itoa(preflightapi.MaxRunEventsTailLimit) && r.URL.Query().Get("errors_only") == "true" && r.URL.Query().Get("logs_only") == "true" {
				sawFallbackTail = true
				writeCLITestJSON(w, http.StatusOK, preflightapi.RunEventsResponse{
					RunID: "run_big",
					Events: []preflightapi.RunEvent{{
						ID:   20,
						Type: preflightapi.EventStdout,
						Data: "E   ModuleNotFoundError: No module named 'hypothesis'\n",
					}},
				})
				return
			}
			http.Error(w, "response too large", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	output, err := captureStdoutForCLITest(t, func() error {
		return runsLogsCommand([]string{"--api-url", server.URL, "--token", "pfl_logs", "--summary", "--json", "run_big"})
	})
	if err != nil {
		t.Fatalf("runs logs --summary fallback: %v", err)
	}
	if !sawFallbackTail {
		t.Fatal("summary did not fall back to diagnostic tail after full event collection failed")
	}
	var report runLogsFailureSummaryReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode summary json: %v\n%s", err, output)
	}
	if !report.Truncated {
		t.Fatalf("summary report truncated = false, want true after tail fallback: %+v", report)
	}
	if len(report.Evidence) == 0 || !strings.Contains(report.Evidence[0].Text, "hypothesis") {
		t.Fatalf("fallback summary evidence = %+v, want missing module evidence", report.Evidence)
	}
}

func TestRunsLogsSummaryOmitsFailureFieldsForSuccess(t *testing.T) {
	t.Setenv("READYCI_API_TOKEN", "")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/runs/run_ok":
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunDetail{
				Summary: preflightapi.RunSummary{
					RunID:          "run_ok",
					Status:         preflightapi.StatusSucceeded,
					Classification: preflightapi.ClassificationSuccess,
					AgentAction:    preflightapi.AgentActionNone,
				},
				Spec: preflightapi.RunSpec{
					RunID:   "run_ok",
					Command: "go test ./...",
				},
			})
		case "/v1/runs/run_ok/events":
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunEventsResponse{
				RunID: "run_ok",
				Events: []preflightapi.RunEvent{{
					ID:   30,
					Type: preflightapi.EventStdout,
					Data: "ok\n",
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	output, err := captureStdoutForCLITest(t, func() error {
		return runsLogsCommand([]string{"--api-url", server.URL, "--token", "pfl_logs", "--summary", "--json", "run_ok"})
	})
	if err != nil {
		t.Fatalf("runs logs --summary success: %v", err)
	}
	if strings.Contains(output, "failure_summary") || strings.Contains(output, "evidence") || strings.Contains(output, "primary_file") {
		t.Fatalf("success summary included failure-only fields:\n%s", output)
	}
	var report runLogsFailureSummaryReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode success summary json: %v\n%s", err, output)
	}
	if report.RunID != "run_ok" || report.Classification != preflightapi.ClassificationSuccess || report.FailureSummary != "" || len(report.Evidence) != 0 || report.PrimaryFile != "" {
		t.Fatalf("success summary report = %+v", report)
	}
}

func TestPrintCheckListReportIncludesOperationalFields(t *testing.T) {
	cfg := config.ProjectConfig{
		Region: "syd",
		Checks: map[string]config.CheckConfig{
			"empty": {},
			"web": {
				Command:             "pnpm test\npnpm lint",
				Steps:               []config.StepConfig{{Name: "unit", Command: "pnpm test"}, {Name: "lint", Command: "pnpm lint"}},
				Changes:             "all",
				Image:               "ubuntu-24.04-base",
				Region:              "iad",
				Docker:              boolPtr(true),
				ConcurrencyGroup:    "frontend",
				ConcurrencyGroupSet: true,
				ConcurrencyLimit:    2,
				ConcurrencyLimitSet: true,
				CacheMode:           "restore",
				Timeout:             "20m",
				Services:            []string{"postgres", "redis"},
				Matrix:              config.MatrixConfig{Axes: map[string][]string{"node": {"22", "24"}}},
			},
		},
	}
	report := buildCheckListReport(cfg, ".readyci.yml")
	output, err := captureStdoutForCLITest(t, func() error {
		printCheckListReport(report)
		return nil
	})
	if err != nil {
		t.Fatalf("print check list: %v", err)
	}
	for _, want := range []string{
		"config: .readyci.yml",
		"checks: 2",
		"web changes=all docker=true image=ubuntu-24.04-base region=iad concurrency_group=frontend concurrency_limit=2 cache_mode=restore timeout=20m services=postgres,redis matrix=2 steps=2 command=",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("check list output = %q, missing %q", output, want)
		}
	}

	output, err = captureStdoutForCLITest(t, func() error {
		printCheckListReport(checkListReport{ConfigPath: ".readyci.yml"})
		return nil
	})
	if err != nil {
		t.Fatalf("print empty check list: %v", err)
	}
	if !strings.Contains(output, "checks: 0") {
		t.Fatalf("empty check list output = %q", output)
	}
}
