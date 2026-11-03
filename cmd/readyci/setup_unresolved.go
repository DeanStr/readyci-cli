package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Setup establishes three separate milestones: configuration is generated,
// the runner is compatible, and the intended command reaches application-level
// test feedback in the intended environment. Static inspection and the
// capability preflight establish only the first two. An unresolved item is
// something setup could not confirm statically; only a run that produces test
// feedback establishes it.
const (
	setupUnresolvedRuntimeDeclarationNotApplied = "runtime_declaration_not_applied"
	setupUnresolvedRuntimeFamilyUnsupported     = "runtime_family_unsupported"
	setupUnresolvedOpaqueEntrypoint             = "opaque_entrypoint"
	setupUnresolvedDependencyPreparationUnknown = "dependency_preparation_unknown"
)

// setupUnresolvedItem is the single source of each setup review note: the
// generated configuration comment, the human preview line and the JSON item
// are all derived from it, so they cannot drift.
type setupUnresolvedItem struct {
	Kind     string `json:"kind"`
	Check    string `json:"check"`
	Subject  string `json:"subject"`
	Source   string `json:"source"`
	Declared string `json:"declared,omitempty"`
	Detail   string `json:"detail"`
}

// Note is the human review note for the item.
func (item setupUnresolvedItem) Note() string {
	if item.Kind == setupUnresolvedDependencyPreparationUnknown {
		return "Preparation review: " + item.Detail
	}
	return "Runtime review: " + item.Detail
}

func (item setupUnresolvedItem) key() string {
	return strings.Join([]string{item.Kind, item.Check, item.Subject, item.Source, item.Declared, item.Detail}, "\x00")
}

func setupUnresolvedNotes(items []setupUnresolvedItem) []string {
	var notes []string
	for _, item := range items {
		notes = appendUniqueString(notes, item.Note())
	}
	return notes
}

// mergeSetupUnresolved keeps the first occurrence of each item in order.
func mergeSetupUnresolved(groups ...[]setupUnresolvedItem) []setupUnresolvedItem {
	var merged []setupUnresolvedItem
	seen := map[string]bool{}
	for _, group := range groups {
		for _, item := range group {
			if !seen[item.key()] {
				seen[item.key()] = true
				merged = append(merged, item)
			}
		}
	}
	return merged
}

func setupUnresolvedForCheck(items []setupUnresolvedItem, check string) []setupUnresolvedItem {
	out := make([]setupUnresolvedItem, 0, len(items))
	for _, item := range items {
		item.Check = check
		out = append(out, item)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func setupRuntimeNoteDetail(described, runtime, reason, remedy string) string {
	return described + " is not emitted as a " + runtime + " runtime requirement (" + reason + "); the runner image's installed " + runtime + " is used. Review it, or " + remedy + "."
}

// setupDeclarationReview describes a repository or workflow declaration that
// setup did not seal as a runtime requirement.
func setupDeclarationReview(kind, runtime, source, declared, reason, remedy string) setupUnresolvedItem {
	described := source
	if declared != "" {
		described += " " + setupNoteValue(declared)
	}
	return setupUnresolvedItem{Kind: kind, Subject: runtime, Source: source, Declared: declared, Detail: setupRuntimeNoteDetail(described, runtime, reason, remedy)}
}

// setupInspectionReview turns a declaration-scope inspection warning into an
// item. Warnings that name the uninspectable file end with ": PATH".
func setupInspectionReview(warning string) setupUnresolvedItem {
	source := "selected command"
	if index := strings.LastIndex(warning, ": "); index >= 0 {
		source = warning[index+2:]
	}
	return setupUnresolvedItem{
		Kind: setupUnresolvedRuntimeDeclarationNotApplied, Subject: "runtime declarations", Source: source,
		Detail: warning + "; affected declarations are not emitted as runtime requirements.",
	}
}

const setupOpaqueEntrypointRemedy = "; the runtimes and dependency preparation it needs are unconfirmed until the first run produces test feedback. Add runtime_requirements for the runtimes it needs, and prepare: commands or a dependency_check for explicit dependency preparation."

// setupOpaqueEntrypoints reports selected test commands whose contents setup
// deliberately does not inspect: Make targets and repository scripts, including
// those an invoked package script runs. Their runtimes and dependency
// preparation stay unknown until a run.
func setupOpaqueEntrypoints(root string, commands []string) []setupUnresolvedItem {
	var items []setupUnresolvedItem
	entries, _ := setupRuntimeCommands(root, commands)
	for _, entry := range entries {
		if !entry.InferRuntimes || entry.Command == setupPythonVenvPreparation {
			continue // Conditional lifecycle hooks are not entrypoints.
		}
		for _, call := range setupExpandedShellCommands(entry.Command) {
			if _, wrapper := setupLiteralBashPayload(call); wrapper {
				continue // Its literal payload is inspected separately.
			}
			invocation := executableShellCommandFromCommand(call)
			args := invocation.Args
			if len(args) == 0 || args[0].Dynamic || invocation.DirDynamic {
				continue
			}
			directory := defaultString(invocation.Dir, ".")
			executable := normalizeCommandInvocationToken(args[0].Text)
			switch name := filepath.Base(executable); {
			case name == "make" || name == "gmake":
				items = append(items, setupOpaqueMakeEntrypoints(root, directory, args[1:])...)
			case name == "bash" || name == "sh":
				if script, ok := setupShellScriptArgument(directory, args[1:]); ok {
					items = append(items, setupOpaqueScript(script))
				}
			case strings.Contains(executable, "/") && len(setupExecutableRuntimes(executable)) == 0:
				if script, ok := resolveMakeDir(directory, executable); ok {
					items = append(items, setupOpaqueScript(script))
				}
			}
		}
	}
	return mergeSetupUnresolved(items)
}

func setupOpaqueScript(script string) setupUnresolvedItem {
	return setupUnresolvedItem{
		Kind: setupUnresolvedOpaqueEntrypoint, Subject: script, Source: script,
		Detail: "repository script " + script + " was not inspected by setup" + setupOpaqueEntrypointRemedy,
	}
}

// setupShellScriptArgument returns the script a literal `bash FILE` or
// `sh FILE` invocation runs. Inline -c payloads are not scripts.
func setupShellScriptArgument(directory string, args []shellWord) (string, bool) {
	for i := 0; i < len(args); i++ {
		if args[i].Dynamic {
			return "", false
		}
		value := args[i].Text
		short := len(value) > 1 && (value[0] == '-' || value[0] == '+') && !strings.HasPrefix(value, "--")
		switch {
		case short && strings.Contains(value, "c"):
			return "", false
		case short && strings.HasSuffix(value, "o"):
			i++ // -o OPTION
		case short || strings.HasPrefix(value, "--"):
		default:
			return resolveMakeDir(directory, value)
		}
	}
	return "", false
}

func setupOpaqueMakeEntrypoints(root, directory string, args []shellWord) []setupUnresolvedItem {
	file, targets, dynamic := "", []string(nil), false
	for i := 0; i < len(args); i++ {
		if args[i].Dynamic {
			if makeDynamicDirectoryOption(args[i].Text) {
				return nil
			}
			dynamic = true
			continue
		}
		value := args[i].Text
		if value == "--" {
			continue
		}
		if !strings.HasPrefix(value, "-") {
			if !strings.Contains(value, "=") {
				targets = appendUniqueString(targets, value)
			}
			continue
		}
		if inline, ok := makeInlineDirectoryValue(value); ok {
			if directory, ok = resolveMakeDir(directory, inline); !ok {
				return nil
			}
			continue
		}
		if name, inline, ok := strings.Cut(value, "="); ok && (name == "--file" || name == "--makefile") {
			file = inline
			continue
		}
		if strings.HasPrefix(value, "-f") && len(value) > len("-f") && !strings.HasPrefix(value, "--") {
			file = strings.TrimPrefix(value, "-f")
			continue
		}
		if value == "-j" || value == "--jobs" {
			if i+1 < len(args) && !args[i+1].Dynamic && makeStaticJobCount(args[i+1].Text) {
				i++
			}
			continue
		}
		switch makeOptionValueKind(value) {
		case "directory":
			if i+1 >= len(args) || args[i+1].Dynamic {
				return nil
			}
			i++
			var ok bool
			if directory, ok = resolveMakeDir(directory, args[i].Text); !ok {
				return nil
			}
		case "value":
			if i+1 >= len(args) {
				continue
			}
			i++
			if value == "-f" || value == "--file" || value == "--makefile" {
				if args[i].Dynamic {
					file = "(dynamic Makefile)"
				} else {
					file = args[i].Text
				}
			}
		}
	}
	path := setupMakeEntrypointFile(root, directory, file)
	if len(targets) == 0 {
		subject := "default goal"
		if dynamic {
			subject = "dynamic target"
		}
		targets = []string{subject}
	}
	items := make([]setupUnresolvedItem, 0, len(targets))
	for _, target := range targets {
		described := path + " target " + strconv.Quote(target)
		if target == "default goal" || target == "dynamic target" {
			described = path + " " + target
		}
		items = append(items, setupUnresolvedItem{
			Kind: setupUnresolvedOpaqueEntrypoint, Subject: target, Source: path,
			Detail: described + " was not inspected by setup" + setupOpaqueEntrypointRemedy,
		})
	}
	return items
}

// setupMakeEntrypointFile names the Makefile Make would read, following GNU
// Make's default file precedence when the command does not name one.
func setupMakeEntrypointFile(root, directory, file string) string {
	if file == "" {
		file = "Makefile"
		for _, name := range []string{"GNUmakefile", "makefile", "Makefile"} {
			if _, err := os.Lstat(filepath.Join(root, directory, name)); err == nil {
				file = name
				break
			}
		}
	}
	if filepath.IsAbs(file) || strings.HasPrefix(file, "(") {
		return file
	}
	return filepath.ToSlash(filepath.Join(directory, file))
}

// setupMilestoneSummary states which setup milestones are established before
// the first run. It never claims test feedback: only a run establishes that.
func setupMilestoneSummary(report setupReport) string {
	environment := report.Environment
	if environment == nil || environment.Status == "blocked" || report.Check == "" || report.ConfigStatus != "ok" && report.ProposedConfig == "" {
		return ""
	}
	summary := "configuration generated"
	if report.ConfigStatus == "ok" && !report.configGenerated {
		summary = "configuration valid"
	}
	separator := ", but "
	if environment.WorkerCapability != nil {
		summary += " and runner compatible"
	} else {
		summary += "; runner compatibility not checked until you sign in"
		separator = "; "
	}
	switch count := len(environment.Unresolved); count {
	case 0:
		return summary + "; the first run confirms that the tests produce feedback."
	case 1:
		return summary + separator + "the unresolved item listed is unconfirmed until the first run produces test feedback."
	default:
		return fmt.Sprintf("%s%sthe %d unresolved items listed are unconfirmed until the first run produces test feedback.", summary, separator, count)
	}
}
