package diagnosiscommon

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

const preparationFetchRecord = "Err:3 https://packages.example.test/snapshot noble-security InRelease\n503 Service Unavailable\n"

func TestPreparationFailureUsesFinalActualScope(t *testing.T) {
	zero, one, other := 0, 1, 2
	prepare := preflightapi.RunEvent{Type: "stderr", Phase: "prepare", Step: "install", Data: preparationFetchRecord}
	for _, tc := range []struct {
		name   string
		events []preflightapi.RunEvent
		want   bool
	}{
		{"failed preparation", []preflightapi.RunEvent{prepare, {Type: preflightapi.EventCommandExited, ExitCode: &one}}, true},
		{"terminal caller without command exit", []preflightapi.RunEvent{prepare}, true},
		{"successful preparation before unknown silent failure", []preflightapi.RunEvent{{Type: "stdout", Phase: "prepare", Data: "preparation completed successfully\n"}, {Type: preflightapi.EventCommandExited, ExitCode: &one}}, false},
		{"silent command phase newline", []preflightapi.RunEvent{prepare, {Type: "stdout", Phase: "command", Data: "\n"}, {Type: preflightapi.EventCommandExited, ExitCode: &one}}, false},
		{"post-run artifact warning", []preflightapi.RunEvent{prepare, {Type: "stderr", Data: "readyci: warning: artifact path \"report.xml\" matched no files after the run\n"}, {Type: preflightapi.EventCommandExited, ExitCode: &one}}, true},
		{"worker finalizer marker newline", []preflightapi.RunEvent{prepare, {Type: "stdout", Phase: "always", Data: "\n"}, {Type: preflightapi.EventCommandExited, ExitCode: &one}}, true},
		{"tagged artifact warning is command output", []preflightapi.RunEvent{prepare, {Type: "stderr", Phase: "command", Data: "readyci: warning: artifact path \"report.xml\" matched no files after the run\n"}, {Type: preflightapi.EventCommandExited, ExitCode: &one}}, false},
		{"arbitrary untagged warning", []preflightapi.RunEvent{prepare, {Type: "stderr", Data: "warning: cleanup did something\n"}, {Type: preflightapi.EventCommandExited, ExitCode: &one}}, false},
		{"later command", []preflightapi.RunEvent{prepare, {Type: "stdout", Phase: "command", Data: "FAILED tests/test_api.py::test_http503 - AssertionError\n"}, {Type: preflightapi.EventCommandExited, ExitCode: &one}}, false},
		{"later prepare step", []preflightapi.RunEvent{prepare, {Type: "stdout", Phase: "prepare", Step: "configure", Data: "configuration failed\n"}, {Type: preflightapi.EventCommandExited, ExitCode: &one}}, true},
		{"untagged later output", []preflightapi.RunEvent{prepare, {Type: "stdout", Data: "failed\n"}, {Type: preflightapi.EventCommandExited, ExitCode: &one}}, false},
		{"post output", []preflightapi.RunEvent{prepare, {Type: "stdout", Phase: "post", Data: "cleanup failed\n"}, {Type: preflightapi.EventCommandExited, ExitCode: &one}}, false},
		{"always output", []preflightapi.RunEvent{prepare, {Type: "stdout", Phase: "always", Data: "upload report failed\n"}, {Type: preflightapi.EventCommandExited, ExitCode: &one}}, false},
		{"successful last exit", []preflightapi.RunEvent{prepare, {Type: preflightapi.EventCommandExited, ExitCode: &zero}}, false},
		{"different exit phase", []preflightapi.RunEvent{prepare, {Type: preflightapi.EventCommandExited, Phase: "command", ExitCode: &one}}, false},
		{"different exit step", []preflightapi.RunEvent{prepare, {Type: preflightapi.EventCommandExited, Step: "tests", ExitCode: &one}}, false},
		{"different exit status", []preflightapi.RunEvent{prepare, {Type: preflightapi.EventCommandExited, ExitCode: &other}}, false},
		{"missing exit status", []preflightapi.RunEvent{prepare, {Type: preflightapi.EventCommandExited}}, false},
		{"later invocation", []preflightapi.RunEvent{prepare, {Type: preflightapi.EventCommandExited, ExitCode: &one}, {Type: preflightapi.EventCommandStarted}, {Type: "stdout", Phase: "command", Data: "tests failed\n"}, {Type: preflightapi.EventCommandExited, ExitCode: &one}}, false},
		{"only service output", []preflightapi.RunEvent{{Type: "stderr", Phase: "prepare", Service: "database", Data: preparationFetchRecord}}, false},
		{"lifecycle text", []preflightapi.RunEvent{{Type: preflightapi.EventRunFailed, Phase: "prepare", Message: preparationFetchRecord}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := PreparationFailureFromEvents(tc.events, &one)
			if (got != nil) != tc.want {
				t.Fatalf("failure=%+v, want presence %t", got, tc.want)
			}
			if tc.name == "later prepare step" && got.DownloadFailure {
				t.Fatal("recovered earlier download failure replaced the final preparation step")
			}
		})
	}
	if PreparationFailureFromEvents([]preflightapi.RunEvent{prepare}, nil) != nil || PreparationFailureFromEvents([]preflightapi.RunEvent{prepare}, &zero) != nil {
		t.Fatal("no failed exit")
	}
}

func TestPreparationFailureDownloadFactsRespectBytesAndSources(t *testing.T) {
	one := 1
	for split := 1; split < len(preparationFetchRecord); split++ {
		events := []preflightapi.RunEvent{
			{Type: "stderr", Phase: "prepare", Data: preparationFetchRecord[:split]},
			{Type: "stderr", Phase: "prepare", DataBase64: base64.StdEncoding.EncodeToString([]byte(preparationFetchRecord[split:]))},
		}
		got := PreparationFailureFromEvents(events, &one)
		if got == nil || !got.DownloadFailure || !strings.Contains(got.Diagnostic, "503") || len(got.Evidence) != 1 {
			t.Fatalf("split=%d failure=%+v", split, got)
		}
	}
	for _, tc := range []struct {
		name   string
		events []preflightapi.RunEvent
	}{
		{"different streams", []preflightapi.RunEvent{{Type: "stdout", Phase: "prepare", Data: "Err:3 https://packages.example.test noble InRelease\n"}, {Type: "stderr", Phase: "prepare", Data: "503 Service Unavailable\n"}}},
		{"different steps", []preflightapi.RunEvent{{Type: "stderr", Phase: "prepare", Step: "one", Data: "Err:3 https://packages.example.test noble InRelease\n"}, {Type: "stderr", Phase: "prepare", Step: "two", Data: "503 Service Unavailable\n"}}},
		{"malformed middle", []preflightapi.RunEvent{{Type: "stderr", Phase: "prepare", Data: "Err:3 https://packages.example.test noble InRelease\n503 Ser"}, {Type: "stderr", Phase: "prepare", DataBase64: "!invalid"}, {Type: "stderr", Phase: "prepare", Data: "vice Unavailable\n"}}},
		{"oversized line", []preflightapi.RunEvent{{Type: "stderr", Phase: "prepare", Data: strings.Repeat(" ", 8192) + preparationFetchRecord}}},
		{"inspection bound", []preflightapi.RunEvent{{Type: "stderr", Phase: "prepare", Data: strings.Repeat("progress\n", (1<<20)/9+1) + preparationFetchRecord}}},
		{"service failure", []preflightapi.RunEvent{{Type: "stdout", Phase: "prepare", Data: "configuration failed\n"}, {Type: "stderr", Phase: "prepare", Service: "database", Data: preparationFetchRecord}}},
		{"bare HTTP status", []preflightapi.RunEvent{{Type: "stderr", Phase: "prepare", Data: "expected HTTP 503 but received 200\n"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := PreparationFailureFromEvents(tc.events, &one)
			if got != nil && got.DownloadFailure {
				t.Fatalf("failure=%+v, want preparation without inferred download cause", got)
			}
		})
	}
}

func TestPreparationFailureRetainsObservedFactsAtInspectionBound(t *testing.T) {
	one := 1
	for _, large := range []preflightapi.RunEvent{
		{Type: "stderr", Phase: "prepare", Data: strings.Repeat("progress\n", 1<<17)},
		{Type: "stderr", Phase: "prepare", DataBase64: strings.Repeat("A", 2<<20)},
	} {
		got := PreparationFailureFromEvents([]preflightapi.RunEvent{{Type: "stderr", Phase: "prepare", Data: preparationFetchRecord}, large}, &one)
		if got == nil || !got.DownloadFailure || len(got.Evidence) != 1 || !strings.Contains(got.Diagnostic, "503") {
			t.Fatalf("already observed failure lost at bounded stop: %+v", got)
		}
	}
}

func TestPreparationDownloadDoesNotDependOnHostnameOrHTTP503(t *testing.T) {
	one := 1
	for _, output := range []string{
		"curl: (22) The requested URL returned error: 404\n",
		"wget https://artifacts.example.test/tool: HTTP/1.1 429 Too Many Requests\n",
		"Downloading dependency archive\ncurl: (6) Could not resolve host: artifacts.example.test\n",
		"npm ERR! code ECONNRESET\nnpm ERR! network connection reset\n",
	} {
		failure := PreparationFailureFromEvents([]preflightapi.RunEvent{{Type: "stderr", Phase: "prepare", Data: output}}, &one)
		if failure == nil || !failure.DownloadFailure {
			t.Fatalf("output=%q failure=%+v", output, failure)
		}
	}
}
