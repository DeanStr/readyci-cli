package diagnosiscommon

import (
	"regexp"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

// Missing configuration must begin an output line or follow a log, exception
// or timestamp separator. Prose such as "when API_KEY is unset" and test titles
// cannot state that the command itself stopped on missing configuration.
const (
	envDiagnosticPrefix = `^(?:.*?(?:[:\]>=]|\s-|[0-9]{2}:[0-9]{2}:[0-9]{2}(?:[.,][0-9]+)?Z?)\s*)?`
	envQuote            = "['\"`]?"
	envName             = `([A-Z][A-Z0-9_]*[A-Z0-9])`
	// A bare subject needs an underscore to look like an environment variable
	// rather than a field or identifier such as ID or URL. Its states exclude
	// "is not defined", which JavaScript reports for an undeclared identifier.
	envSnakeName       = `([A-Z][A-Z0-9]*(?:_[A-Z0-9]+)+)`
	envBareMissing     = `(?i:(?:(?:is|was|has)\s+)?not\s+(?:been\s+)?(?:set|configured|provided|specified|supplied)|(?:is|was)\s+(?:unset|required)|must\s+be\s+(?:set|configured|defined|provided|specified|supplied))`
	envVariableNoun    = `(?i:env(?:ironment)?\s+var(?:iable)?s?)`
	envVariableMissing = `(?i:(?:(?:is|was|has|are)\s+)?(?:not\s+(?:been\s+)?(?:set|configured|defined|provided|specified|supplied)|unset|undefined|missing|required|empty)|must\s+be\s+(?:set|configured|defined|provided|specified|supplied))`
	envDiagnosticEnd   = "(?:[\\s.,;:!)'\"`]|$)"
)

var (
	missingEnvironmentConfigurationLines = []*regexp.Regexp{
		// FATAL: DATABASE_URL must be configured before tests start
		regexp.MustCompile(envDiagnosticPrefix + envQuote + envSnakeName + envQuote + `\s+` + envBareMissing + envDiagnosticEnd),
		// Missing required environment variable: DATABASE_URL
		regexp.MustCompile(envDiagnosticPrefix + `(?i:missing\s+(?:required\s+)?)` + envVariableNoun + `(?:\s*[:=]?\s*` + envQuote + envName + `)?` + envDiagnosticEnd),
		// Environment variable DATABASE_URL is not set
		regexp.MustCompile(envDiagnosticPrefix + `(?i:(?:required\s+)?)` + envVariableNoun + `\s+` + envQuote + envName + envQuote + `(?:\s+[^.;:!?\r\n]{0,60}?)?\s+` + envVariableMissing + envDiagnosticEnd),
		// DATABASE_URL environment variable is not set
		regexp.MustCompile(envDiagnosticPrefix + envQuote + envName + envQuote + `\s+` + envVariableNoun + `\s+` + envVariableMissing + envDiagnosticEnd),
		// ImproperlyConfigured: Set the DATABASE_URL environment variable
		regexp.MustCompile(envDiagnosticPrefix + `(?i:(?:please\s+)?set\s+the)\s+` + envQuote + envName + envQuote + `\s+` + envVariableNoun + envDiagnosticEnd),
		// bash: line 1: DATABASE_URL: parameter null or not set
		regexp.MustCompile(envDiagnosticPrefix + envName + `:\s+parameter\s+(?:null\s+or\s+)?not\s+set\s*$`),
	}
	// Advisory lines describe a default, fallback or skip rather than a stop.
	environmentConfigurationAdvisory = regexp.MustCompile(`(?i)warn|skip|default|fall(?:s|ing)?\s*back|ignor|optional|[,;(]\s*using\b`)
	// A running test's own log output, such as a Go t.Skip message or a Jest or
	// Vitest console block, shows that tests started; it is not the command
	// stopping on its configuration.
	goTestLogLine         = regexp.MustCompile(`(?:^|\s)[A-Za-z0-9_./-]*_test\.go:[0-9]+:\s`)
	testConsoleBlockStart = regexp.MustCompile(`^(?:console\.(?:log|info|warn|debug|error|trace)|(?:stdout|stderr) \| .+)$`)
	pythonEnvironKeyError = regexp.MustCompile(`(?:^|\s)KeyError:\s+['"]` + envName + `['"]\s*$`)
	// os.environ raises from os._Environ.__getitem__; pytest prints the frame
	// as <frozen os>:N or .../os.py:N.
	pythonEnvironLookupFrame = regexp.MustCompile(`(?:<frozen os>|[\\/]os\.py)(?:", line [0-9]+, in __getitem__|:[0-9]+: (?:in __getitem__|KeyError))`)
)

// MissingEnvironmentConfigurationFromEvents recognizes a command's own report,
// in the final observed failed output scope, that required environment
// configuration is absent. Submitted commands and service output are not
// searched. It does not consult test-runner evidence: callers apply it only to
// a test classification whose output reports no test failure, because a failing
// test may legitimately mention an unset variable.
func MissingEnvironmentConfigurationFromEvents(events []preflightapi.RunEvent, exitCode *int) *EnvironmentPrerequisiteFailure {
	if exitCode == nil || *exitCode == 0 {
		return nil
	}
	scope := FinalCommandFailureEvents(events)
	var direct *EnvironmentPrerequisiteFailure
	type keyErrorCandidate struct {
		name     string
		evidence preflightapi.RunFailureEvidence
	}
	var keyErrors []keyErrorCandidate
	environLookupFrame := false
	environSubscripts := make(map[string]bool)
	previous := ""
	ForEachDiagnosticLine(scope, func(record DiagnosticLine) {
		if direct != nil || record.Message {
			return
		}
		line := strings.TrimSpace(StripDiagnosticANSI(record.Text))
		if line == "" {
			return
		}
		consoleBlock := testConsoleBlockStart.MatchString(previous)
		previous = line
		if consoleBlock || DefiniteTestNonFailureLine(line) {
			return
		}
		event := scope[record.EventIndex]
		stream, _ := EventStream(event)
		evidence := preflightapi.RunFailureEvidence{
			EventID: event.ID, Stream: stream, Seq: EventSequence(event, record.EventIndex), Line: record.Line, Text: line,
		}
		if strings.Contains(line, "__getitem__") || strings.Contains(line, "KeyError") {
			environLookupFrame = environLookupFrame || pythonEnvironLookupFrame.MatchString(line)
			if match := pythonEnvironKeyError.FindStringSubmatch(line); match != nil {
				keyErrors = append(keyErrors, keyErrorCandidate{name: match[1], evidence: evidence})
				return
			}
		}
		if index := strings.Index(line, "environ["); index >= 0 {
			if name := strings.Trim(strings.SplitN(line[index+len("environ["):], "]", 2)[0], `"'`); name != "" {
				environSubscripts[name] = true
			}
		}
		if name, ok := missingEnvironmentConfigurationLine(line); ok {
			direct = missingEnvironmentConfigurationFailure(name, evidence)
		}
	})
	if direct != nil {
		return direct
	}
	for _, candidate := range keyErrors {
		if environLookupFrame || environSubscripts[candidate.name] {
			return missingEnvironmentConfigurationFailure(candidate.name, candidate.evidence)
		}
	}
	return nil
}

func missingEnvironmentConfigurationLine(line string) (string, bool) {
	lower := strings.ToLower(line)
	if !ContainsAny(lower, "not ", "unset", "undefined", "missing", "required", "must be", "set the", "empty") ||
		environmentConfigurationAdvisory.MatchString(line) || goTestLogLine.MatchString(line) {
		return "", false
	}
	for _, pattern := range missingEnvironmentConfigurationLines {
		if match := pattern.FindStringSubmatch(line); match != nil {
			return match[1], true
		}
	}
	return "", false
}

func missingEnvironmentConfigurationFailure(name string, evidence preflightapi.RunFailureEvidence) *EnvironmentPrerequisiteFailure {
	summary := "The test command exited before reporting any test failure: required environment configuration is missing."
	if name != "" {
		summary = "The test command exited before reporting any test failure: required environment variable " + name + " is not configured."
	}
	return &EnvironmentPrerequisiteFailure{Summary: summary, Evidence: evidence}
}
