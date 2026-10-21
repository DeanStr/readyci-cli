package diagnosiscommon

import (
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

const namedScalaTestFailure = "- persists a Unicode event *** FAILED ***"
const finalMavenTestFailure = "[ERROR] Failed to execute goal org.scalatest:scalatest-maven-plugin:2.2.0:test (test) on project example-core: There are test failures -> [Help 1]"

func mavenFailureEvents() []preflightapi.RunEvent {
	exit := 1
	return []preflightapi.RunEvent{
		{ID: 11, Type: preflightapi.EventStdout, Phase: "command", Step: "tests", Data: namedScalaTestFailure + "\n  java.io.IOException: No space left on device\n"},
		{ID: 12, Type: preflightapi.EventStderr, Phase: "command", Step: "tests", Data: finalMavenTestFailure + "\n"},
		{ID: 13, Type: preflightapi.EventCommandExited, ExitCode: &exit},
	}
}

func TestMavenTestFailureUsesFinalOutputScopeWithUntaggedExit(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func([]preflightapi.RunEvent) []preflightapi.RunEvent
		want   bool
	}{
		{"worker-shaped exit", func(e []preflightapi.RunEvent) []preflightapi.RunEvent { return e }, true},
		{"explicit step", func(e []preflightapi.RunEvent) []preflightapi.RunEvent { e[2].Step = "tests"; return e }, true},
		{"run identity inferred", func(e []preflightapi.RunEvent) []preflightapi.RunEvent {
			e[0].RunID, e[1].RunID = "maven-run", "maven-run"
			return e
		}, true},
		{"exit metadata disagrees", func(e []preflightapi.RunEvent) []preflightapi.RunEvent { e[2].RunID = "other-run"; return e }, false},
		{"later failed step", func(e []preflightapi.RunEvent) []preflightapi.RunEvent {
			return append(e[:2], preflightapi.RunEvent{Type: preflightapi.EventStderr, Phase: "command", Step: "lint", Data: "lint failed\n"}, e[2])
		}, false},
		{"reused step name", func(e []preflightapi.RunEvent) []preflightapi.RunEvent {
			return []preflightapi.RunEvent{e[0], {Type: preflightapi.EventStdout, Phase: "command", Step: "other", Data: "other step\n"}, e[1], e[2]}
		}, false},
		{"later preparation", func(e []preflightapi.RunEvent) []preflightapi.RunEvent {
			return append(e[:2], preflightapi.RunEvent{Type: preflightapi.EventStderr, Phase: "prepare", Step: "tests", Data: "preparation failed\n"}, e[2])
		}, false},
		{"later post phase", func(e []preflightapi.RunEvent) []preflightapi.RunEvent {
			return append(e[:2], preflightapi.RunEvent{Type: preflightapi.EventStderr, Phase: "post", Data: "post failed\n"}, e[2])
		}, false},
		{"new invocation", func(e []preflightapi.RunEvent) []preflightapi.RunEvent {
			return append(e, preflightapi.RunEvent{Type: preflightapi.EventCommandStarted}, preflightapi.RunEvent{Type: preflightapi.EventStderr, Phase: "command", Step: "tests", Data: "another command failed\n"})
		}, false},
		{"interleaved service", func(e []preflightapi.RunEvent) []preflightapi.RunEvent {
			return []preflightapi.RunEvent{e[0], {Type: preflightapi.EventStdout, Phase: "command", Service: "database", Data: "service log\n"}, e[1], e[2]}
		}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			exit := 1
			if got := MavenTestFailureFromEvents(test.mutate(mavenFailureEvents()), &exit); (got != nil) != test.want {
				t.Fatalf("failure=%+v, want match %v", got, test.want)
			}
		})
	}
}

func TestMavenTestFailureRequiresCompletedFailedRunner(t *testing.T) {
	exit := 1
	failure := MavenTestFailureFromEvents(mavenFailureEvents(), &exit)
	if failure == nil || failure.Summary != finalMavenTestFailure || len(failure.Evidence) != 2 {
		t.Fatalf("failure = %+v", failure)
	}
	for i, id := range []int64{12, 11} {
		if got := failure.Evidence[i]; got.EventID != id || got.Seq != int(id) {
			t.Fatalf("evidence[%d] = %+v", i, got)
		}
	}
}

func TestMavenTestFailureRejectsUnrelatedOrIncompleteOutput(t *testing.T) {
	cases := map[string]func([]preflightapi.RunEvent) []preflightapi.RunEvent{
		"named only":    func(e []preflightapi.RunEvent) []preflightapi.RunEvent { return append(e[:1], e[2]) },
		"goal only":     func(e []preflightapi.RunEvent) []preflightapi.RunEvent { return e[1:] },
		"reverse order": func(e []preflightapi.RunEvent) []preflightapi.RunEvent { e[0], e[1] = e[1], e[0]; return e },
		"reverse in one chunk": func(e []preflightapi.RunEvent) []preflightapi.RunEvent {
			e[0].Data = finalMavenTestFailure + "\n" + namedScalaTestFailure + "\n"
			return append(e[:1], e[2])
		},
		"after exit": func(e []preflightapi.RunEvent) []preflightapi.RunEvent {
			return []preflightapi.RunEvent{e[2], e[0], e[1]}
		},
		"prepare":        func(e []preflightapi.RunEvent) []preflightapi.RunEvent { e[0].Phase = "prepare"; return e },
		"empty phase":    func(e []preflightapi.RunEvent) []preflightapi.RunEvent { e[0].Phase = ""; return e },
		"service":        func(e []preflightapi.RunEvent) []preflightapi.RunEvent { e[0].Service = "database"; return e },
		"different step": func(e []preflightapi.RunEvent) []preflightapi.RunEvent { e[1].Step = "other"; return e },
		"different run":  func(e []preflightapi.RunEvent) []preflightapi.RunEvent { e[1].RunID = "other"; return e },
		"exit step":      func(e []preflightapi.RunEvent) []preflightapi.RunEvent { e[2].Step = "other"; return e },
		"exit service":   func(e []preflightapi.RunEvent) []preflightapi.RunEvent { e[2].Service = "database"; return e },
		"exit prepare":   func(e []preflightapi.RunEvent) []preflightapi.RunEvent { e[2].Phase = "prepare"; return e },
		"exit missing":   func(e []preflightapi.RunEvent) []preflightapi.RunEvent { return e[:2] },
		"exit unknown":   func(e []preflightapi.RunEvent) []preflightapi.RunEvent { e[2].ExitCode = nil; return e },
		"exit zero":      func(e []preflightapi.RunEvent) []preflightapi.RunEvent { zero := 0; e[2].ExitCode = &zero; return e },
		"exit differs":   func(e []preflightapi.RunEvent) []preflightapi.RunEvent { code := 2; e[2].ExitCode = &code; return e },
		"exit text only": func(e []preflightapi.RunEvent) []preflightapi.RunEvent {
			e[2].Type = preflightapi.EventStdout
			e[2].Data = "command_exited: 1"
			return e
		},
		"later zero exit": func(e []preflightapi.RunEvent) []preflightapi.RunEvent {
			zero := 0
			return append(e, preflightapi.RunEvent{Type: preflightapi.EventCommandExited, Step: "tests", ExitCode: &zero})
		},
		"later unknown exit": func(e []preflightapi.RunEvent) []preflightapi.RunEvent {
			return append(e, preflightapi.RunEvent{Type: preflightapi.EventCommandExited, Step: "tests"})
		},
		"new command": func(e []preflightapi.RunEvent) []preflightapi.RunEvent {
			return []preflightapi.RunEvent{e[0], {Type: preflightapi.EventCommandStarted}, e[1], e[2]}
		},
		"old exited command": func(e []preflightapi.RunEvent) []preflightapi.RunEvent {
			zero := 0
			return []preflightapi.RunEvent{e[0], {Type: preflightapi.EventCommandExited, ExitCode: &zero}, e[1], e[2]}
		},
		"later Maven success": func(e []preflightapi.RunEvent) []preflightapi.RunEvent {
			return append(e[:2], preflightapi.RunEvent{Type: preflightapi.EventStdout, Phase: "command", Step: "tests", Data: "[INFO] BUILD SUCCESS\n"}, e[2])
		},
		"quoted name": func(e []preflightapi.RunEvent) []preflightapi.RunEvent {
			e[0].Data = "\"" + namedScalaTestFailure + "\"\n"
			return e
		},
		"indented quotation": func(e []preflightapi.RunEvent) []preflightapi.RunEvent {
			e[0].Data = "  " + namedScalaTestFailure + "\n"
			return e
		},
		"prefixed goal": func(e []preflightapi.RunEvent) []preflightapi.RunEvent {
			e[1].Data = "expected: " + finalMavenTestFailure + "\n"
			return e
		},
		"lifecycle message": func(e []preflightapi.RunEvent) []preflightapi.RunEvent {
			e[0].Message, e[0].Data = e[0].Data, ""
			return e
		},
		"compile failure": func(e []preflightapi.RunEvent) []preflightapi.RunEvent {
			e[1].Data = strings.Replace(finalMavenTestFailure, ":test (test)", ":compile (compile)", 1) + "\n"
			return e
		},
		"plugin resolution": func(e []preflightapi.RunEvent) []preflightapi.RunEvent {
			e[1].Data = strings.Replace(finalMavenTestFailure, "There are test failures", "Could not resolve dependencies", 1) + "\n"
			return e
		},
		"empty name": func(e []preflightapi.RunEvent) []preflightapi.RunEvent { e[0].Data = "-  *** FAILED ***\n"; return e },
		"oversized record": func(e []preflightapi.RunEvent) []preflightapi.RunEvent {
			e[0].Data = strings.Repeat("x", 8193) + namedScalaTestFailure + "\n"
			return e
		},
		"inspection budget": func(e []preflightapi.RunEvent) []preflightapi.RunEvent {
			e[0].Data = strings.Repeat("x", (32<<20)+1) + "\n" + namedScalaTestFailure + "\n"
			return e
		},
	}
	cases["unterminated name"] = func(e []preflightapi.RunEvent) []preflightapi.RunEvent {
		e[0].Data = namedScalaTestFailure
		return e
	}
	cases["pending name across success"] = func(e []preflightapi.RunEvent) []preflightapi.RunEvent {
		e[0].Data = namedScalaTestFailure
		return []preflightapi.RunEvent{e[0], {Type: preflightapi.EventStderr, Phase: "command", Step: "tests", Data: "[INFO] BUILD SUCCESS\n"}, e[1], e[2]}
	}
	cases["name completes after goal"] = func(e []preflightapi.RunEvent) []preflightapi.RunEvent {
		a, b := e[0], e[0]
		a.Data, b.Data = namedScalaTestFailure[:12], namedScalaTestFailure[12:]+"\n"
		return []preflightapi.RunEvent{a, e[1], b, e[2]}
	}
	cases["unterminated later success"] = func(e []preflightapi.RunEvent) []preflightapi.RunEvent {
		return []preflightapi.RunEvent{e[0], e[1], {Type: preflightapi.EventStdout, Phase: "command", Step: "tests", Data: "[INFO] BUILD SUCCESS"}, e[2]}
	}

	cases["whitespace-only name"] = func(e []preflightapi.RunEvent) []preflightapi.RunEvent {
		e[0].Data = "- " + strings.Repeat(" ", 80) + " *** FAILED ***\n"
		return e
	}

	cases["unicode-whitespace-only name"] = func(e []preflightapi.RunEvent) []preflightapi.RunEvent {
		e[0].Data = "- " + strings.Repeat("\u00a0\t", 40) + " *** FAILED ***\n"
		return e
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			exit := 1
			if got := MavenTestFailureFromEvents(mutate(mavenFailureEvents()), &exit); got != nil {
				t.Fatalf("unexpected match: %+v", got)
			}
		})
	}
	if got := MavenTestFailureFromEvents(mavenFailureEvents(), nil); got != nil {
		t.Fatalf("nil run exit matched: %+v", got)
	}
}

func TestMavenTestFailureFragmentedLinesAndSourceBoundaries(t *testing.T) {
	exit := 1
	for _, text := range []string{namedScalaTestFailure, "\x1b[31m- persists a π event *** FAILED ***\x1b[0m"} {
		for split := 1; split < len(text); split++ {
			e := mavenFailureEvents()
			first := e[0]
			first.Data = text[:split]
			second := e[0]
			second.ID, second.Data = 21, text[split:]+"\r\n"
			e = append([]preflightapi.RunEvent{first, second}, e[1:]...)
			got := MavenTestFailureFromEvents(e, &exit)
			if got == nil || got.Evidence[1].EventID != 11 || got.Evidence[1].Line != 1 {
				t.Fatalf("split %d: %+v", split, got)
			}
		}
	}
	for _, boundary := range []string{"stream", "step", "phase", "service"} {
		t.Run(boundary, func(t *testing.T) {
			e := mavenFailureEvents()
			a, b := e[0], e[0]
			a.Data, b.Data = namedScalaTestFailure[:12], namedScalaTestFailure[12:]+"\n"
			switch boundary {
			case "stream":
				b.Type = preflightapi.EventStderr
			case "step":
				b.Step = "other"
			case "phase":
				b.Phase = "prepare"
			case "service":
				b.Service = "database"
			}
			e = append([]preflightapi.RunEvent{a, b}, e[1:]...)
			if got := MavenTestFailureFromEvents(e, &exit); got != nil {
				t.Fatalf("joined %s: %+v", boundary, got)
			}
		})
	}
	for _, prefix := range []preflightapi.RunEvent{
		{Type: preflightapi.EventStdout, Phase: "command", Step: "tests", Data: strings.Repeat("x", 8193)},
		{Type: preflightapi.EventStdout, Phase: "command", Step: "tests", DataBase64: "%%%"},
	} {
		e := mavenFailureEvents()
		e[0].Data = "\n" + e[0].Data
		e = append([]preflightapi.RunEvent{prefix}, e...)
		if got := MavenTestFailureFromEvents(e, &exit); got == nil {
			t.Fatalf("did not recover at newline: %+v", prefix)
		}
	}
	// A later failure can be found after large ordinary output; the actual
	// regression's named failure occurs beyond the first MiB of its log.
	e := mavenFailureEvents()
	e = append([]preflightapi.RunEvent{{Type: preflightapi.EventStdout, Phase: "command", Step: "tests", Data: strings.Repeat("ordinary output\n", 80000)}}, e...)
	e[1].Data = strings.TrimSuffix(e[1].Data, "\n") + "\n"
	e[2].Data = strings.TrimSuffix(e[2].Data, "\n")
	if got := MavenTestFailureFromEvents(e, &exit); got == nil {
		t.Fatal("missed late or unterminated final failure")
	}
}
