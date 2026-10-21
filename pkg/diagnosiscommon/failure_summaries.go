package diagnosiscommon

import (
	"regexp"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/textlimit"
)

var (
	pytestFailedRecord = regexp.MustCompile(`(?i)^FAILED\s+([A-Za-z0-9_./-]+\.py)(?:::|\s|$)`)
	pythonException    = regexp.MustCompile(`(?:^|:\s+|^E\s+)(?:[A-Za-z_][A-Za-z0-9_]*\.)*(?:[A-Za-z_][A-Za-z0-9_]*(?:Error|Exception)|Error|Exception):`)
	pythonWarning      = regexp.MustCompile(`^[^\r\n]+\.py:[0-9]+:\s*[A-Za-z_][A-Za-z0-9_]*Warning:`)
	pytestInlineError  = regexp.MustCompile(` - [A-Za-z_][A-Za-z0-9_.]*(?:Error|Exception|Warning):\s*\S`)
)

// PythonWarningDiagnostic identifies a warning record, without deciding whether
// it was fatal. A warning remains useful when no stronger failure was observed.
func PythonWarningDiagnostic(text string) bool {
	return pythonWarning.MatchString(stripANSISGR(strings.TrimSpace(text)))
}

func PythonExceptionDiagnostic(text string) bool {
	return pythonException.MatchString(stripANSISGR(strings.TrimSpace(text)))
}

// PytestFailureSummary prefers a named failed test and an observed exception to
// incidental warning locations. The two retained lines are diagnostic output;
// they do not establish why that test failed or authorize a source modification.
func PytestFailureSummary(evidence []preflightapi.RunFailureEvidence) (summary, path string) {
	failed, exception := "", ""
	var failedRecord preflightapi.RunFailureEvidence
	exceptionScore := 0
	for _, item := range evidence {
		text := strings.TrimSpace(item.Text)
		if match := pytestFailedRecord.FindStringSubmatch(stripANSISGR(text)); failed == "" && len(match) > 1 && ValidPrimaryFilePath(match[1]) {
			failed, path = text, match[1]
			failedRecord = item
		}
	}
	if failed == "" {
		return "", ""
	}
	if pytestInlineError.MatchString(stripANSISGR(failed)) {
		return textlimit.PrefixUTF8WithinBytes(failed, 240), path
	}
	for _, item := range evidence {
		text := strings.TrimSpace(item.Text)
		// Do not attach an exception from another test, output stream or
		// event. Only the immediately preceding reporter lines can supply
		// context missing from a terse FAILED record.
		if item.Seq != failedRecord.Seq || item.Stream != failedRecord.Stream || item.EventID != failedRecord.EventID ||
			item.Line <= 0 || failedRecord.Line-item.Line < 1 || failedRecord.Line-item.Line > 3 {
			continue
		}
		if !pythonException.MatchString(stripANSISGR(text)) || pytestFailedRecord.MatchString(stripANSISGR(text)) {
			continue
		}
		score := 1
		if item.Path != "" {
			score = 2
		}
		if score > exceptionScore {
			exception, exceptionScore = text, score
		}
	}
	if exception == "" {
		return textlimit.PrefixUTF8WithinBytes(failed, 240), path
	}
	first := textlimit.PrefixUTF8WithinBytes(failed, 160)
	return first + "\n" + textlimit.PrefixUTF8WithinBytes(exception, 239-len(first)), path
}
