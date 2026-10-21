package diagnosiscommon

import (
	"regexp"
	"sort"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/textlimit"
)

var scalaTestNamedFailure = regexp.MustCompile(`^- ([^\r\n]{1,1000}) \*\*\* FAILED \*\*\*(?: \([^\r\n]{1,128}\))?$`)
var mavenScalaTestGoalFailure = regexp.MustCompile(`^\[ERROR\] Failed to execute goal org\.scalatest:scalatest-maven-plugin:[A-Za-z0-9_.-]{1,80}:test \([^\r\n)]{1,80}\) on project [A-Za-z0-9_.-]{1,200}: There are test failures(?: -> \[Help [0-9]+\])?$`)

func isNamedScalaTestFailure(text string) bool {
	match := scalaTestNamedFailure.FindStringSubmatch(text)
	return len(match) > 1 && strings.TrimSpace(match[1]) != ""
}

// MavenTestFailure identifies an emitted failed test task, not a dependency
// diagnostic printed by an otherwise passing negative test.
type MavenTestFailure struct {
	Summary  string
	Evidence []preflightapi.RunFailureEvidence
}

// MavenTestFailureFromEvents requires an ordered named ScalaTest failure and
// Maven's final failed test goal in the same command step, followed by the last
// actual matching nonzero command exit. Submitted commands, service output and
// preparation logs cannot establish this context. Inspection and retained line
// state are bounded; incomplete or oversized input cannot establish a match.
func MavenTestFailureFromEvents(events []preflightapi.RunEvent, exitCode *int) *MavenTestFailure {
	if exitCode == nil || *exitCode == 0 {
		return nil
	}
	lastExit := -1
	for i, event := range events {
		if event.Type == preflightapi.EventCommandExited {
			lastExit = i
		}
	}
	if lastExit < 0 {
		return nil
	}
	exit := events[lastExit]

	if exit.ExitCode == nil || *exit.ExitCode != *exitCode || exit.Service != "" || (exit.Phase != "" && exit.Phase != "command") {
		return nil
	}
	start, end := finalCommandFailureScope(events)
	if end != lastExit || start >= end || events[start].Phase != "command" {
		return nil
	}
	finalScope := events[start]
	type lineState struct {
		text     string
		discard  bool
		where    preflightapi.RunFailureEvidence
		position uint64
	}
	type source struct{ stream, step, run string }
	type match struct {
		failure, goal     preflightapi.RunFailureEvidence
		failureAt, goalAt uint64
	}
	pending := map[source]lineState{}
	var order []source
	var found match
	inspect := func(line lineState) {
		if line.discard {
			return
		}
		// Keep the column-zero anchor: indentation and quoted stacktrace text
		// are not a runner's named result or Maven's final goal failure.
		text := strings.TrimRight(stripANSISGR(line.text), "\r")
		where := line.where
		where.Text = textlimit.PrefixUTF8WithinBytes(text, 500)
		if text == "[INFO] BUILD SUCCESS" {
			found = match{}
			return
		}
		if isNamedScalaTestFailure(text) && found.failure.Text == "" {
			found.failure, found.failureAt = where, line.position
		}
		if mavenScalaTestGoalFailure.MatchString(text) && (found.goal.Text == "" || line.position > found.goalAt) {
			found.goal, found.goalAt = where, line.position
		}

	}
	remaining := 32 << 20
	for i := start; i < end; i++ {
		event := events[i]
		stream, isLog := EventStream(event)
		if !isLog || event.Phase != "command" || event.Service != "" || event.Step != finalScope.Step || event.RunID != finalScope.RunID {
			continue
		}
		key := source{stream, event.Step, event.RunID}
		line, known := pending[key]
		if !known {
			if len(pending) == 2 {
				return nil
			}
			order = append(order, key)
		}
		data, err := event.DataBytes()
		if err != nil {
			pending[key] = lineState{discard: true}
			continue
		}
		if len(data) > remaining {
			return nil
		}
		remaining -= len(data)
		parts := strings.Split(string(data), "\n")
		for number, part := range parts {
			if line.text == "" && !line.discard {
				line.where = preflightapi.RunFailureEvidence{EventID: event.ID, Seq: EventSequence(event, i), Stream: stream, Line: number + 1}
			}
			line.position = uint64(i)<<32 | uint64(number+1)
			if len(line.text)+len(part) > 8192 {
				line = lineState{discard: true}
			} else if !line.discard {
				line.text += part
			}
			if number == len(parts)-1 {
				break
			}
			inspect(line)
			line = lineState{}
		}
		pending[key] = line
	}
	// An exit can terminate the final Maven message. A named test result
	// requires a completed line, so a buffered fragment cannot be introduced
	// after an already observed final goal or BUILD SUCCESS from another stream.
	sort.SliceStable(order, func(i, j int) bool { return pending[order[i]].position < pending[order[j]].position })
	for _, key := range order {
		line := pending[key]
		if !isNamedScalaTestFailure(strings.TrimRight(stripANSISGR(line.text), "\r")) {
			inspect(line)
		}
	}
	if found.failure.Text == "" || found.goal.Text == "" || found.goalAt <= found.failureAt {
		return nil
	}
	evidence := []preflightapi.RunFailureEvidence{found.goal, found.failure}
	return &MavenTestFailure{Summary: textlimit.PrefixUTF8WithinBytes(found.goal.Text, 240), Evidence: evidence}
}
