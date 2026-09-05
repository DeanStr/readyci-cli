package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

func readyCICheckWrapperMacro(name string, wrappers map[string]bool) bool {
	if wrappers[name] {
		return true
	}
	return readyCICheckWrapperNamePattern.MatchString(name)
}

var readyCICheckWrapperNamePattern = regexp.MustCompile(`^remote-readyci(?:-[A-Za-z0-9_]+)*-check$`)

func readyCICheckWrapperMacros(text string) map[string]bool {
	wrappers := map[string]bool{}
	var name string
	var body []string
	flush := func() {
		if name != "" && makeMacroBodyRunsReadyCICheckArg(body) {
			wrappers[name] = true
		}
		name = ""
		body = nil
	}
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if name == "" {
			if strings.HasPrefix(trimmed, "define ") {
				fields := strings.Fields(strings.TrimPrefix(trimmed, "define "))
				if len(fields) > 0 {
					name = fields[0]
				}
			}
			continue
		}
		if trimmed == "endef" {
			flush()
			continue
		}
		body = append(body, line)
	}
	flush()
	return wrappers
}

func makeMacroBodyRunsReadyCICheckArg(lines []string) bool {
	text := strings.Join(lines, "\n")
	lower := strings.ToLower(text)
	if !strings.Contains(lower, " check ") || !strings.Contains(text, "$(1)") {
		return false
	}
	return strings.Contains(lower, "readyci") || strings.Contains(text, "$(READYCI)")
}

func checkFromReadyCIArgs(args string) string {
	checks := checksFromReadyCIArgs(args)
	if len(checks) == 0 {
		return ""
	}
	return checks[0]
}

func readyCIChecksFromCommand(command string) []string {
	var checks []string
	for _, command := range shellCommandsFromCommand(command) {
		args := executableShellArgs(command.Args)
		if len(args) < 2 || args[0].Dynamic || args[1].Dynamic {
			continue
		}
		if !isReadyCICommandToken(args[0].Text) || args[1].Text != "check" {
			continue
		}
		checks = append(checks, checksFromReadyCIWords(args[2:])...)
	}
	return uniqueStrings(checks)
}

func checksFromReadyCIArgs(args string) []string {
	commands := shellCommandsFromCommand("readyci check " + args)
	if len(commands) == 0 || len(commands[0].Args) < 2 {
		return nil
	}
	return checksFromReadyCIWords(commands[0].Args[2:])
}

func checksFromReadyCIWords(words []shellWord) []string {
	var pendingValue bool
	var checks []string
	for _, word := range words {
		token := word.Text
		if token == "" || token == "\\" {
			continue
		}
		if pendingValue {
			pendingValue = false
			continue
		}
		if word.Dynamic {
			continue
		}
		if strings.HasPrefix(token, "--") {
			if !strings.Contains(token, "=") && readyCIOptionNeedsValue(token) {
				pendingValue = true
			}
			continue
		}
		if isPlaceholderToken(token) {
			continue
		}
		checks = append(checks, token)
		break
	}
	return checks
}

func isReadyCICommandToken(token string) bool {
	token = normalizeCommandInvocationToken(token)
	return token == "readyci" || token == "$(READYCI)"
}

func readyCIOptionNeedsValue(token string) bool {
	if strings.Contains(token, "=") {
		token = strings.SplitN(token, "=", 2)[0]
	}
	switch token {
	case "--api-url", "--token", "--project", "--region", "--changes", "--timeout", "--runner-size", "--network-mode", "--network-allowlist", "--cache-path", "--concurrency-group", "--concurrency-limit", "--retry", "--dashboard-url", "--watch-interval":
		return true
	default:
		return false
	}
}

func isPlaceholderToken(token string) bool {
	token = strings.Trim(token, "<>[](){}.,;:`")
	if token == "" {
		return true
	}
	return token == strings.ToUpper(token) && strings.ContainsAny(token, "ABCDEFGHIJKLMNOPQRSTUVWXYZ")
}

func cleanCheckReferences(refs []repoCheckReference) []repoCheckReference {
	seen := map[string]bool{}
	var out []repoCheckReference
	for _, ref := range refs {
		ref.Check = strings.TrimSpace(ref.Check)
		if ref.Check == "" || isDynamicCheckReference(ref.Check) {
			continue
		}
		key := ref.Source + "\x00" + ref.Path + "\x00" + fmt.Sprint(ref.Line) + "\x00" + ref.Check + "\x00" + ref.Context
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path == out[j].Path {
			if out[i].Line == out[j].Line {
				return out[i].Check < out[j].Check
			}
			return out[i].Line < out[j].Line
		}
		return out[i].Path < out[j].Path
	})
	return out
}

func isDynamicCheckReference(check string) bool {
	return strings.ContainsAny(check, "$(){}") || strings.HasPrefix(check, "-")
}
