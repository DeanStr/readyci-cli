package diagnosiscommon

import (
	"regexp"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

var (
	goCompilerVersionMismatch  = regexp.MustCompile(`^compile: version "go[0-9][^"]*" does not match go tool version "go[0-9][^"]*"$`)
	nodeVersionUnavailable     = regexp.MustCompile(`^unsupported Node\.js version '[^']+'; supported versions: [0-9].*$`)
	rustVersionUnavailable     = regexp.MustCompile(`^error: rustc [0-9][0-9.]+ is not supported by the following packages:$`)
	mesonExecutableMissing     = regexp.MustCompile(`^meson-python: error: meson executable "[^"]+" not found$`)
	shellRuntimeMissing        = regexp.MustCompile(`^(?:/bin/)?(?:bash|dash|zsh|sh):\s*(?:line )?[0-9]+:\s*(?:node|npm|pnpm|yarn|python[0-9.]*|java|javac|mvn|gradle|php|ruby|bundle|elixir|mix|dotnet|swift|xcodebuild|cmake|meson|ninja|go|rustc|cargo):\s*(?:command not found|not found)$`)
	prepareRequirementGuard    = regexp.MustCompile(`(?i)^[a-z0-9_. -]+ requires (?:a|an) [^;\r\n]+(?:runner|runtime|toolchain|sdk)(?:;.*)?$`)
	directIOUnavailable        = regexp.MustCompile(`(?i)^cannot enable direct io: (?:invalid argument|operation not supported|not supported)$`)
	scratchStorageExhausted    = regexp.MustCompile(`(?i)^(?:[a-z0-9_./@-]+:\s*)?(?:write|mkdir|open|create|cannot create directory)\s+['"]?(?:/tmp/|\$work/|/workspace/)[^\r\n]{1,512}:\s*(?:no space left on device|enospc)(?:[; ].*)?$`)
	mountFilesystemUnavailable = regexp.MustCompile(`^mount: [^\r\n]{1,512}: unknown filesystem type '[A-Za-z0-9_-]+'\.?$`)
)

// EnvironmentPrerequisiteFailure records a concrete prerequisite diagnostic from
// the final observed failed output scope. It assigns no hosted classification.
type EnvironmentPrerequisiteFailure struct {
	Summary  string
	Evidence preflightapi.RunFailureEvidence
}

// EnvironmentPrerequisiteFailureFromEvents recognizes explicit prerequisite failures in
// actual command output. Submitted commands may quote diagnostic fixtures and
// cannot establish that a prerequisite failed. An exit code alone cannot either.
func EnvironmentPrerequisiteFailureFromEvents(events []preflightapi.RunEvent, exitCode *int) *EnvironmentPrerequisiteFailure {
	if exitCode == nil || *exitCode == 0 {
		return nil
	}
	events = FinalCommandFailureEvents(events)
	type source struct{ stream, phase, step string }
	type pendingLine struct {
		text    string
		discard bool
	}
	pending := make(map[source]pendingLine)
	var sources []source
	lastEvents := make(map[source]preflightapi.RunFailureEvidence)
	for index, event := range events {
		stream, isLog := EventStream(event)
		if !isLog || event.Service != "" {
			continue
		}
		key := source{stream, event.Phase, event.Step}
		line, seen := pending[key]
		if !seen {
			sources = append(sources, key)
		}
		data, err := event.DataBytes()
		if err != nil {
			// Missing bytes must not join two unrelated fragments into evidence.
			pending[key] = pendingLine{discard: true}
			continue
		}
		parts := strings.Split(string(data), "\n")
		for lineNumber, part := range parts {
			continued := line.text != "" || line.discard
			// Error markers are short. Bound an unfinished line rather than
			// buffering arbitrary binary or minified customer output.
			if len(line.text)+len(part) > 8192 {
				line = pendingLine{discard: true}
			} else if !line.discard {
				line.text += part
			}
			location := preflightapi.RunFailureEvidence{EventID: event.ID, Seq: EventSequence(event, index), Stream: stream}
			if !continued {
				location.Line = lineNumber + 1
			}
			lastEvents[key] = location
			if lineNumber == len(parts)-1 {
				break
			}
			if !line.discard {
				if failure := environmentPrerequisiteFailureFromLine(line.text, event.Phase, *exitCode, location); failure != nil {
					return failure
				}
			}
			line = pendingLine{}
		}
		pending[key] = line
	}
	// Shells may exit without a final newline. Preserve source order so the
	// selected diagnostic is deterministic even when several streams are partial.
	for _, key := range sources {
		line := pending[key]
		if !line.discard {
			if failure := environmentPrerequisiteFailureFromLine(line.text, key.phase, *exitCode, lastEvents[key]); failure != nil {
				return failure
			}
		}
	}
	return nil
}

func environmentPrerequisiteFailureFromLine(text, phase string, exitCode int, evidence preflightapi.RunFailureEvidence) *EnvironmentPrerequisiteFailure {
	line := strings.TrimSpace(StripDiagnosticANSI(text))
	if DefiniteTestNonFailureLine(line) {
		return nil
	}
	summary := ""
	switch {
	case goCompilerVersionMismatch.MatchString(line), nodeVersionUnavailable.MatchString(line), rustVersionUnavailable.MatchString(line):
		summary = line
	case phase == "prepare" && (mesonExecutableMissing.MatchString(line) || shellRuntimeMissing.MatchString(line)):
		summary = line
	case phase == "prepare" && exitCode == 78 && prepareRequirementGuard.MatchString(line):
		summary = line
	case directIOUnavailable.MatchString(line):
		// EINVAL establishes a failed operation, not the filesystem type or
		// whether changing TMPDIR will be sufficient to repair the environment.
		summary = line + "; inspect filesystem support and temporary-file placement."
	case mountFilesystemUnavailable.MatchString(line):
		summary = line + "; inspect filesystem support in the selected runner."
	case scratchStorageExhausted.MatchString(line):
		summary = line + "; inspect bytes and inodes on the filesystem containing the failed path."
	default:
		return nil
	}
	evidence.Text = line
	return &EnvironmentPrerequisiteFailure{Summary: summary, Evidence: evidence}
}
