package main

import (
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func scanMakeReferencesInText(text, source, path string, archive bool) []repoMakeReference {
	var refs []repoMakeReference
	text = normalizeShellLineContinuations(text)
	for i, line := range strings.Split(text, "\n") {
		for _, snippet := range commandSnippetsForScan(line, source == "docs") {
			snippet = strings.TrimSpace(snippet)
			if snippet == "" || strings.HasPrefix(snippet, "#") {
				continue
			}
			for _, invocation := range makeInvocationsFromCommand(snippet) {
				target := strings.TrimSpace(invocation.Target)
				if target == "" || strings.Contains(target, "=") {
					continue
				}
				if !strings.HasPrefix(target, "remote-") && !strings.HasPrefix(target, "check-") {
					continue
				}
				refs = append(refs, repoMakeReference{Target: target, Dir: invocation.Dir, Source: source, Path: path, Line: i + 1, Context: strings.TrimSpace(line), Archive: archive})
			}
		}
	}
	return cleanMakeReferences(refs)
}

func cleanMakeReferences(refs []repoMakeReference) []repoMakeReference {
	seen := map[string]bool{}
	var out []repoMakeReference
	for _, ref := range refs {
		key := ref.Source + "\x00" + ref.Path + "\x00" + fmt.Sprint(ref.Line) + "\x00" + ref.Dir + "\x00" + ref.Target + "\x00" + ref.Context
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path == out[j].Path {
			if out[i].Line == out[j].Line {
				return out[i].Target < out[j].Target
			}
			return out[i].Line < out[j].Line
		}
		return out[i].Path < out[j].Path
	})
	return out
}

type makeInvocation struct {
	Dir    string
	Target string
}

func (invocation makeInvocation) Display() string {
	if invocation.Dir == "" {
		return invocation.Target
	}
	return invocation.Dir + ":" + invocation.Target
}

func makeTargetsFromCommand(command string) []string {
	invocations := makeInvocationsFromCommand(command)
	targets := make([]string, 0, len(invocations))
	for _, invocation := range invocations {
		targets = append(targets, invocation.Target)
	}
	return targets
}

func makeInvocationsFromCommand(command string) []makeInvocation {
	var invocations []makeInvocation
	for _, command := range shellCommandsFromCommand(command) {
		executable := executableShellCommandFromCommand(command)
		args := executable.Args
		if len(args) == 0 || args[0].Dynamic {
			continue
		}
		commandName := normalizeCommandInvocationToken(args[0].Text)
		if commandName != "make" && commandName != "$(MAKE)" {
			continue
		}
		pendingValue := false
		pendingDirectory := false
		dynamicDirectory := executable.DirDynamic
		dir := executable.Dir
		for index, argWord := range args[1:] {
			arg := argWord.Text
			if arg == "" {
				continue
			}
			if argWord.Dynamic {
				if pendingValue {
					if pendingDirectory {
						dynamicDirectory = true
					}
					pendingValue = false
					pendingDirectory = false
				}
				if makeDynamicDirectoryOption(arg) {
					dynamicDirectory = true
				}
				continue
			}
			if pendingValue {
				if pendingDirectory {
					var ok bool
					dir, ok = resolveMakeDir(dir, arg)
					if !ok {
						dynamicDirectory = true
					}
				}
				pendingValue = false
				pendingDirectory = false
				continue
			}
			if strings.HasPrefix(arg, "-") {
				if arg == "-j" || arg == "--jobs" {
					pendingValue = index+2 < len(args) && !args[index+2].Dynamic && makeStaticJobCount(args[index+2].Text)
					continue
				}
				if value, ok := makeInlineDirectoryValue(arg); ok {
					var resolved bool
					dir, resolved = resolveMakeDir(dir, value)
					if !resolved {
						dynamicDirectory = true
					}
					continue
				}
				kind := makeOptionValueKind(arg)
				if kind != "" {
					pendingValue = true
					pendingDirectory = kind == "directory"
				}
				continue
			}
			if strings.Contains(arg, "=") {
				continue
			}
			if !dynamicDirectory {
				invocations = append(invocations, makeInvocation{Dir: normalizeMakeInvocationDir(dir), Target: arg})
			}
		}
	}
	return uniqueMakeInvocations(invocations)
}

func makeStaticJobCount(value string) bool {
	if value == "" {
		return false
	}
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	count, err := strconv.Atoi(value)
	return err == nil && count > 0
}

func makeDynamicDirectoryOption(token string) bool {
	return strings.HasPrefix(token, "--directory=") || strings.HasPrefix(token, "-C")
}

func makeOptionNeedsValue(token string) bool {
	return makeOptionValueKind(token) != ""
}

func makeOptionValueKind(token string) string {
	if strings.Contains(token, "=") {
		return ""
	}
	switch token {
	case "-C", "--directory":
		return "directory"
	case "-f", "-I", "-W", "-j", "-l", "-o", "--file", "--makefile", "--include-dir", "--what-if", "--assume-new", "--assume-old", "--old-file", "--eval", "--jobs", "--load-average", "--max-load":
		return "value"
	default:
		return ""
	}
}

func makeInlineDirectoryValue(token string) (string, bool) {
	if strings.HasPrefix(token, "--directory=") {
		return strings.TrimPrefix(token, "--directory="), true
	}
	if strings.HasPrefix(token, "-C") && len(token) > len("-C") {
		return strings.TrimPrefix(token, "-C"), true
	}
	return "", false
}

func resolveMakeDir(base string, value string) (string, bool) {
	raw := strings.TrimSpace(strings.Trim(value, `"'`))
	if raw == "" || raw == "~" || strings.HasPrefix(raw, "~/") || filepath.IsAbs(raw) || path.IsAbs(filepath.ToSlash(raw)) {
		return "", false
	}
	value = path.Clean(filepath.ToSlash(raw))
	if value == "." {
		return normalizeAffectedPath(base), true
	}
	if base != "" && base != "." {
		value = path.Join(normalizeAffectedPath(base), value)
	}
	value = path.Clean(value)
	if value == ".." || strings.HasPrefix(value, "../") {
		return "", false
	}
	return normalizeAffectedPath(value), true
}

func normalizeMakeInvocationDir(dir string) string {
	dir = normalizeAffectedPath(dir)
	if dir == "." {
		return ""
	}
	return dir
}

func makeDirForPath(path string) string {
	dir := normalizeAffectedPath(filepath.Dir(filepath.ToSlash(path)))
	if dir == "." {
		return ""
	}
	return dir
}

func makeTargetKey(dir string, target string) string {
	return normalizeAffectedPath(dir) + "\x00" + target
}

func uniqueMakeInvocations(values []makeInvocation) []makeInvocation {
	seen := map[string]bool{}
	out := make([]makeInvocation, 0, len(values))
	for _, value := range values {
		key := makeTargetKey(value.Dir, value.Target)
		if value.Target == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	return out
}
