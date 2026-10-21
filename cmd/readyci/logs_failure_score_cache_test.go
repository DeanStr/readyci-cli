package main

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestRunLogsEvidenceCachedRanksPreserveSelection(t *testing.T) {
	items := []preflightapi.RunFailureEvidence{
		{EventID: 100, Text: "   "},
		{EventID: 101, Text: "readyci: cache filesystem usage"},
		{EventID: 102, Text: "random retained context"},
		{EventID: 103, Path: "src/a.go", Text: "warning"},
		{EventID: 104, Text: "--- FAIL: TestCase"},
		{EventID: 105, Text: "  ModuleNotFoundError: No module named 'missing'  "},
		{EventID: 106, Text: "FAILED tests/test_case.py::test_case"},
		{EventID: 107, Text: "expected 2 got 1"},
		{EventID: 108, Text: "ERROR COLLECTING TESTS"},
		{EventID: 109, Text: "error collecting tests"},
		{EventID: 110, Text: "error TS2322: incompatible type"},
		{EventID: 111, Path: "/vendor/lib.go", Text: "diagnostic from library"},
		{EventID: 112, Path: "tests/test_x.py", Text: "tests/test_x.py:9: PytestUnknownMarkWarning: Unknown pytest.mark.slow"},
		{EventID: 113, Text: "  AssertionError: mismatch  "},
	}
	for i := range items {
		items[i].Seq = 500 + i
		items[i].Line = i + 1
		items[i].Stream = "stderr"
		items[i].Event = preflightapi.EventStderr
	}
	original := append([]preflightapi.RunFailureEvidence(nil), items...)
	// Fixed expected priority, stable assertion ties, case-insensitive duplicate
	// selection and complete metadata. Noise must not displace useful evidence.
	order := []int{5, 4, 6, 7, 13, 3, 10, 11, 8, 2, 12}
	var all []preflightapi.RunFailureEvidence
	for _, index := range order {
		item := items[index]
		switch index {
		case 5:
			item.Text = "ModuleNotFoundError: No module named 'missing'"
		case 13:
			item.Text = "AssertionError: mismatch"
		}
		all = append(all, item)
	}
	for _, limit := range []int{-1, 0, 1, 2, 8, 12} {
		want := all
		if limit > 0 && limit < len(want) {
			want = want[:limit]
		}
		if got := selectRunLogsFailureEvidence(items, limit); !reflect.DeepEqual(got, want) {
			t.Fatalf("limit%d got%+v want%+v", limit, got, want)
		}
	}
	if !reflect.DeepEqual(items, original) {
		t.Fatal("selection mutated evidence")
	}
	noise := []preflightapi.RunFailureEvidence{{EventID: 41, Seq: 6, Line: 3, Stream: "stdout", Text: "  readyci: cache filesystem usage  "}, {EventID: 42, Seq: 7, Line: 4, Stream: "stderr", Text: "Filesystem Size Used Avail Use% Mounted on"}}
	wantNoise := []preflightapi.RunFailureEvidence{{EventID: 41, Seq: 6, Line: 3, Stream: "stdout", Text: "readyci: cache filesystem usage"}, noise[1]}
	for _, limit := range []int{-1, 0, 1, 8} {
		want := wantNoise
		if limit == 1 {
			want = want[:1]
		}
		if got := selectRunLogsFailureEvidence(noise, limit); !reflect.DeepEqual(got, want) {
			t.Fatalf("fallback limit%d got%+v want%+v", limit, got, want)
		}
	}
	if got := selectRunLogsFailureEvidence(nil, 12); got == nil || len(got) != 0 {
		t.Fatalf("empty selection shape changed:%#v", got)
	}
}

func TestRunLogsEvidenceRetainedRanksAreBoundedAndReused(t *testing.T) {
	const limit = 12
	calls := map[runLogsFailureEvidenceRankKey]int{}
	ranker := runLogsFailureEvidenceRanker{score: func(item preflightapi.RunFailureEvidence) int {
		calls[runLogsFailureEvidenceKey(item)]++
		return runLogsFailureEvidenceScore(item)
	}}
	var retained, admitted []preflightapi.RunFailureEvidence
	for i := 0; i < 200; i++ {
		item := preflightapi.RunFailureEvidence{EventID: int64(i + 1), Seq: i + 1, Stream: "stderr", Path: fmt.Sprintf("src/item%d.go", i), Text: fmt.Sprintf("src/item%d.go:%d: expected 2 got 1", i, i+1)}
		admitted = append(admitted, item)
		candidate := append(append([]preflightapi.RunFailureEvidence(nil), retained...), item)
		retained = selectRunLogsFailureEvidenceWithRanker(candidate, limit, &ranker)
		ranker.retain(retained)
		// Equal-priority later candidates must not displace the first twelve.
		if want := admitted[:min(len(admitted), limit)]; !reflect.DeepEqual(retained, want) {
			t.Fatalf("equal-score metadata/order changed:got%+v want%+v", retained, want)
		}
		if len(ranker.ranks) > limit {
			t.Fatalf("unbounded rank history%d", len(ranker.ranks))
		}
	}
	if len(calls) != 200 {
		t.Fatalf("ranked%d distinct candidates", len(calls))
	}
	for key, n := range calls {
		if n != 1 {
			t.Fatalf("immutable retained candidate%+v scored%d times", key, n)
		}
	}
}

func TestRunLogsEvidenceRankKeyPreservesMetadataAndWhitespace(t *testing.T) {
	calls := 0
	ranker := runLogsFailureEvidenceRanker{score: func(item preflightapi.RunFailureEvidence) int { calls++; return runLogsFailureEvidenceScore(item) }}
	original := preflightapi.RunFailureEvidence{EventID: 4, Seq: 4, Line: 2, Stream: "stderr", Path: "src/a.go", Text: "  error collecting tests  "}
	first := ranker.rank(original)
	changed := original
	changed.EventID = 99
	changed.Seq = 100
	changed.Line = 8
	changed.Stream = "stdout"
	changed.Text = strings.TrimSpace(original.Text)
	second := ranker.rank(changed)
	if first != second || calls != 1 {
		t.Fatalf("text/path invariant lost: calls%d", calls)
	}
	changed.Path = "/vendor/a.go"
	ranker.rank(changed)
	if calls != 2 {
		t.Fatal("different path reused rank")
	}
}

func TestLocalEvidenceCachedRanksPreserveStreamingThresholds(t *testing.T) {
	texts := []string{"src/a.go:1: error first", "src/b.go:2: error second", "ModuleNotFoundError: No module named 'pkg'", "--- FAIL: TestCase", "FAILED tests/test_case.py::test_case"}
	paths := []string{"src/a.go", "src/b.go", "", "", "tests/test_case.py"}
	var events []preflightapi.RunEvent
	var expected []preflightapi.RunFailureEvidence
	for i, text := range texts {
		events = append(events, preflightapi.RunEvent{ID: int64(100 + i), Type: preflightapi.EventStderr, Data: text + "\n", Phase: "command"})
		expected = append(expected, preflightapi.RunFailureEvidence{EventID: int64(100 + i), Seq: 100 + i, Line: 1, Event: preflightapi.EventStderr, Stream: "stderr", Path: paths[i], Text: text})
	}
	for _, limit := range []int{-1, 0, 1, 2, 8, 12} {
		want := expected
		switch limit {
		case 1:
			want = []preflightapi.RunFailureEvidence{expected[2]}
		case 2:
			want = []preflightapi.RunFailureEvidence{expected[2], expected[3]}
		}
		if got := localEvidenceFromEvents(events, limit); !reflect.DeepEqual(got, want) {
			t.Fatalf("streaming limit%d got%+v want%+v", limit, got, want)
		}
	}
}
