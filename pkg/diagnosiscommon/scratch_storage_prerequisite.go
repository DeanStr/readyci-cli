package diagnosiscommon

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/textlimit"
)

var wrappedScratchOperation = regexp.MustCompile(`^(mkdir|write|open|create) (/[^\s:"'\\]{1,512}): (no space left on device|ENOSPC)$`)

type scratchFailureScope struct {
	run, phase, step  string
	invocation, epoch int
}

type scratchFailureSource struct {
	scope  scratchFailureScope
	stream string
}

type scratchLinePosition struct {
	source  scratchFailureSource
	ordinal int
}

type scratchOutputState struct {
	lastOrdinal, assertion, indentation int
	mountTable, byteTable               bool
}

type scratchOperationFailure struct {
	scope           scratchFailureScope
	path, operation string
	evidence        preflightapi.RunFailureEvidence
	index           int
}

type scratchMountObservation struct {
	scope     scratchFailureScope
	mount     string
	available uint64
	known     bool
	bytes     bool
	evidence  preflightapi.RunFailureEvidence
	index     int
}

// wrappedScratchStorageFailure only refines an assertion's actual error when
// the final failed scope also reports matching scratch filesystem pressure.
// Repository output is advisory evidence, not a trusted root-cause attestation.
func wrappedScratchStorageFailure(events []preflightapi.RunEvent) *EnvironmentPrerequisiteFailure {
	selected := scratchSelectedEventIndices(events)
	positions, valid := scratchLinePositions(events)
	if !valid {
		return nil
	}
	states := make(map[scratchFailureSource]*scratchOutputState)
	var operations []scratchOperationFailure
	var mounts []scratchMountObservation
	uncertain := make(map[scratchFailureScope]bool)
	bounded := true
	ForEachDiagnosticLine(events, func(record DiagnosticLine) {
		event := events[record.EventIndex]
		_, log := EventStream(event)
		if !bounded || !selected[record.EventIndex] || record.Message || !log || event.Service != "" ||
			(event.Phase != "command" && event.Phase != "prepare" && event.Phase != "post") {
			return
		}
		position := positions[record.EventIndex]
		position.ordinal += record.Line
		state := states[position.source]
		if state == nil {
			if len(states) == 64 {
				bounded = false
				return
			}
			state = &scratchOutputState{}
			states[position.source] = state
		}
		if position.ordinal != state.lastOrdinal+1 {
			state.assertion, state.mountTable, state.byteTable = 0, false, false
		}
		state.lastOrdinal = position.ordinal
		line := strings.TrimRight(StripDiagnosticANSI(record.Text), "\r")
		text := strings.TrimSpace(line)
		location := preflightapi.RunFailureEvidence{
			EventID: event.ID, Seq: EventSequence(event, record.EventIndex),
			Stream: position.source.stream, Line: record.Line, Text: textlimit.PrefixUTF8WithinBytes(text, 500),
		}
		if operation, failedPath := state.actualScratchError(line); failedPath != "" {
			if len(operations) == 256 {
				bounded = false
				return
			}
			operations = append(operations, scratchOperationFailure{position.source.scope, failedPath, operation, location, record.EventIndex})
		}
		fields := strings.Fields(text)
		if scratchDFHeader(fields) {
			state.mountTable, state.byteTable = true, scratchByteDFHeader(fields)
			return
		}
		if state.mountTable {
			mount, available, valid := "", uint64(0), false
			if state.byteTable {
				mount, available, valid = scratchByteDFRow(fields)
			}
			if !valid && len(fields) == 6 && canonicalScratchDiagnosticPath(fields[5]) {
				// A malformed capacity value must not erase a deeper mount
				// and let a full parent stand in for its unknown capacity.
				mount = fields[5]
			}
			if mount != "" {
				if len(mounts) == 256 {
					bounded = false
					return
				}
				mounts = append(mounts, scratchMountObservation{position.source.scope, mount, available, valid, state.byteTable, location, record.EventIndex})
			}
			if len(fields) != 6 {
				if scratchDFRowLike(fields) {
					uncertain[position.source.scope] = true
				}
				// A nonrow ends the table. Invalid numeric rows still retain
				// topology and cannot hide following rows in that same table.
				state.mountTable, state.byteTable = false, false
			}
		}
	})
	if !bounded {
		return nil
	}
	for _, operation := range operations {
		if uncertain[operation.scope] {
			continue
		}
		var selected *scratchMountObservation
		for index := range mounts {
			mount := &mounts[index]
			if mount.scope != operation.scope || !scratchPathWithin(operation.path, mount.mount) {
				continue
			}
			// All table formats preserve topology, including pre-error healthy
			// rows. Only a numeric byte table can establish capacity below.
			if selected == nil || len(mount.mount) > len(selected.mount) {
				selected = mount
			}
		}
		if selected != nil {
			mountPath := selected.mount
			selected = nil
			for index := range mounts {
				mount := &mounts[index]
				if mount.scope == operation.scope && mount.mount == mountPath && mount.bytes &&
					(selected == nil || mount.index > selected.index || mount.index == selected.index && mount.evidence.Line >= selected.evidence.Line) {
					selected = mount
				}
			}
		}
		if selected == nil || !selected.known || selected.available != 0 || !supportedScratchPath(selected.mount) || selected.index < operation.index ||
			selected.index == operation.index && selected.evidence.Line < operation.evidence.Line {
			// A full root filesystem alone cannot locate scratch pressure:
			// an unreported deeper /tmp mount may have separate capacity.
			continue
		}
		return &EnvironmentPrerequisiteFailure{
			Summary:  fmt.Sprintf("Observed %s %s: no space left on device; %s reported zero available bytes. Inspect filesystem capacity and temporary-file placement.", operation.operation, operation.path, selected.mount),
			Evidence: operation.evidence, SupportingEvidence: []preflightapi.RunFailureEvidence{selected.evidence},
		}
	}
	return nil
}

func (state *scratchOutputState) actualScratchError(line string) (operation, failedPath string) {
	text := strings.TrimSpace(line)
	indentation := len(line) - len(strings.TrimLeft(line, " "))
	switch {
	case text == "error:":
		state.assertion, state.indentation = 1, indentation
	case state.assertion == 1 && text == "got non-nil error" && indentation == state.indentation+2:
		state.assertion = 2
	case state.assertion == 2 && text == "got:" && indentation == state.indentation:
		state.assertion = 3
	case state.assertion == 3 && strings.HasPrefix(text, `e"`) && indentation == state.indentation+2:
		state.assertion = 0
		value, err := strconv.Unquote(strings.TrimPrefix(text, "e"))
		if err != nil {
			return "", ""
		}
		match := wrappedScratchOperation.FindStringSubmatch(value)
		if len(match) == 4 && supportedScratchPath(match[2]) {
			return match[1], match[2]
		}
	default:
		state.assertion = 0
	}
	return "", ""
}

func supportedScratchPath(value string) bool {
	return canonicalScratchDiagnosticPath(value) &&
		(scratchPathWithin(value, "/tmp") || scratchPathWithin(value, "/var/tmp") || scratchPathWithin(value, "/workspace"))
}

func canonicalScratchDiagnosticPath(value string) bool {
	if !path.IsAbs(value) || path.Clean(value) != value || !utf8.ValidString(value) || strings.Contains(value, "\\") {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func scratchPathWithin(value, directory string) bool {
	return value == directory || directory == "/" || strings.HasPrefix(value, directory+"/")
}

func scratchByteDFHeader(fields []string) bool {
	return len(fields) == 7 && fields[0] == "Filesystem" && (fields[1] == "1-blocks" || fields[1] == "1B-blocks") &&
		fields[2] == "Used" && fields[3] == "Available" && (fields[4] == "Capacity" || fields[4] == "Use%") && fields[5] == "Mounted" && fields[6] == "on"
}

func scratchDFHeader(fields []string) bool {
	return len(fields) == 7 && fields[0] == "Filesystem" && fields[5] == "Mounted" && fields[6] == "on"
}

func scratchDFRowLike(fields []string) bool {
	if len(fields) == 0 {
		return false
	}
	if fields[0] == "Filesystem" || canonicalScratchDiagnosticPath(fields[0]) || canonicalScratchDiagnosticPath(fields[len(fields)-1]) {
		return true
	}
	if len(fields) > 1 {
		_, err := strconv.ParseUint(fields[1], 10, 64)
		return err == nil
	}
	return false
}

func scratchByteDFRow(fields []string) (mount string, available uint64, valid bool) {
	if len(fields) != 6 || !canonicalScratchDiagnosticPath(fields[5]) {
		return "", 0, false
	}
	values := [3]uint64{}
	for index := range values {
		value, err := strconv.ParseUint(fields[index+1], 10, 64)
		if err != nil {
			return "", 0, false
		}
		values[index] = value
	}
	percent, err := strconv.ParseUint(strings.TrimSuffix(fields[4], "%"), 10, 8)
	if err != nil || !strings.HasSuffix(fields[4], "%") || percent > 100 || values[0] == 0 || values[1] > values[0] || values[2] > values[0] {
		return "", 0, false
	}
	return fields[5], values[2], true
}

// Give parser state a physical line ordinal and scope epoch, so discarded
// oversized lines, missing bytes and repeated scopes cannot join a wrapper or
// df header to an unrelated following value. Any missing or oversized repository
// line suppresses this refinement, including earlier scopes and trailing lines:
// lost bytes may conceal deeper mount topology. Ordinary diagnosis still runs.
func scratchLinePositions(events []preflightapi.RunEvent) ([]scratchLinePosition, bool) {
	positions := make([]scratchLinePosition, len(events))
	type outputScope struct{ phase, step string }
	previous := make(map[string]outputScope)
	epochs := make(map[string]int)
	ordinals := make(map[scratchFailureSource]int)
	lineBytes := make(map[scratchFailureSource]int)
	invocation := 0
	for index, event := range events {
		if event.Service != "" {
			continue
		}
		switch event.Type {
		case preflightapi.EventRunScheduled, preflightapi.EventVMStarting, preflightapi.EventCommandStarted, preflightapi.EventCommandExited:
			invocation++
			previous = make(map[string]outputScope)
			epochs = make(map[string]int)
		}
		stream, log := EventStream(event)
		if !log {
			continue
		}
		scope := outputScope{event.Phase, event.Step}
		if earlier, seen := previous[event.RunID]; seen && earlier != scope {
			epochs[event.RunID]++
		}
		previous[event.RunID] = scope
		if len(previous) > 64 {
			return nil, false
		}
		data, err := event.DataBytes()
		if err != nil {
			return nil, false
		}
		key := scratchFailureSource{scratchFailureScope{event.RunID, event.Phase, event.Step, invocation, epochs[event.RunID]}, stream}
		positions[index] = scratchLinePosition{key, ordinals[key]}
		for _, value := range data {
			if value == '\n' {
				ordinals[key]++
				lineBytes[key] = 0
			} else {
				lineBytes[key]++
				if lineBytes[key] > 8192 {
					// The shared iterator drops oversized lines, including trailing
					// rows with no later callback. Missing topology is not evidence.
					return nil, false
				}
			}
		}
		if len(ordinals) > 64 || len(lineBytes) > 64 {
			return nil, false
		}
	}
	return positions, true
}

// Retain original event positions so selecting the final scope cannot conceal
// an intervening scope or invocation from the multiline parser. The selected
// scope is an ordered subsequence; resolve duplicate older events backwards.
func scratchSelectedEventIndices(events []preflightapi.RunEvent) []bool {
	selected := make([]bool, len(events))
	scope := FinalCommandFailureEvents(events)
	next := len(scope) - 1
	for index := len(events) - 1; index >= 0 && next >= 0; index-- {
		if events[index] == scope[next] {
			selected[index] = true
			next--
		}
	}
	return selected
}
