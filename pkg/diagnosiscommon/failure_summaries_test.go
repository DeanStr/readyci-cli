package diagnosiscommon

import (
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestPytestSummaryPrefersFailureAndExceptionToIncidentalWarning(t *testing.T) {
	evidence := []preflightapi.RunFailureEvidence{
		{Seq: 1, Line: 1, Path: "src/requests/auth.py", Text: "src/requests/auth.py:45: DeprecationWarning: deprecated username"},
		{Seq: 1, Line: 2, Text: "E requests.exceptions.ConnectionError: Connection refused"},
		{Seq: 1, Line: 3, Path: "tests/test_requests.py", Text: "FAILED tests/test_requests.py::TestTimeout::test_connect_timeout[timeout0]"},
	}
	summary, path := PytestFailureSummary(evidence)
	if !strings.HasPrefix(summary, "FAILED tests/test_requests.py") || !strings.Contains(summary, "ConnectionError") || strings.Contains(summary, "DeprecationWarning") || path != "tests/test_requests.py" {
		t.Fatalf("summary=%q path=%q", summary, path)
	}
	if len(strings.Split(summary, "\n")) != 2 || len(summary) > 240 {
		t.Fatalf("unbounded summary=%q", summary)
	}
}

func TestPytestSummaryDoesNotAssociateUnrelatedExceptions(t *testing.T) {
	failed := "FAILED tests/test_b.py::test_failure"
	for _, tc := range []struct{ name, record string }{
		{"terse failure", failed},
		{"self contained failure", failed + " - AssertionError: expected 2, received 1"},
	} {
		evidence := []preflightapi.RunFailureEvidence{
			{Seq: 1, EventID: 1, Stream: "stderr", Line: 1, Text: "E UnrelatedError: another test failed"},
			{Seq: 2, EventID: 2, Stream: "stdout", Line: 2, Text: tc.record},
		}
		if summary, _ := PytestFailureSummary(evidence); summary != tc.record {
			t.Fatalf("%s summary associated unrelated evidence: %q", tc.name, summary)
		}
	}
}

func TestPytestWarningRemainsAvailableWithoutStrongerFailure(t *testing.T) {
	warning := "src/module.py:12: DeprecationWarning: deprecated API"
	if summary, path := PytestFailureSummary([]preflightapi.RunFailureEvidence{{Text: warning, Path: "src/module.py"}}); summary != "" || path != "" || !PythonWarningDiagnostic(warning) {
		t.Fatalf("warning was promoted or discarded: %q %q", summary, path)
	}
	fatal := "FAILED tests/test_warnings.py::test_warning - DeprecationWarning: deprecated API"
	if summary, _ := PytestFailureSummary([]preflightapi.RunFailureEvidence{{Text: fatal}}); !strings.Contains(summary, "DeprecationWarning") {
		t.Fatalf("fatal warning disappeared: %q", summary)
	}
}

func TestPythonExceptionEvidenceIncludesPlainException(t *testing.T) {
	for _, text := range []string{"E Exception: fixture failed", "E Error: fixture failed", "E requests.exceptions.ConnectionError: Connection refused"} {
		if !PythonExceptionDiagnostic(text) {
			t.Fatalf("exception record ignored: %q", text)
		}
	}
}
