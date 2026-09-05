package main

import (
	"path/filepath"
	"regexp"
	"strings"
)

func scanCheckReferencesInText(text, source, path string, archive bool) []repoCheckReference {
	var refs []repoCheckReference
	wrapperMacros := readyCICheckWrapperMacros(text)
	macroCallRe := regexp.MustCompile(`\$\(\s*call\s+([A-Za-z0-9_.-]+)\s*,\s*([A-Za-z0-9_.-]+)`)
	text = normalizeShellLineContinuations(text)
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		for _, snippet := range commandSnippetsForScan(line, source == "docs") {
			snippet = strings.TrimSpace(snippet)
			if snippet == "" || strings.HasPrefix(snippet, "#") {
				continue
			}
			for _, match := range macroCallRe.FindAllStringSubmatch(snippet, -1) {
				if readyCICheckWrapperMacro(match[1], wrapperMacros) {
					refs = append(refs, repoCheckReference{Check: match[2], Source: source, Path: path, Line: i + 1, Context: strings.TrimSpace(line), Archive: archive})
				}
			}
			for _, check := range readyCIChecksFromCommand(snippet) {
				refs = append(refs, repoCheckReference{Check: check, Source: source, Path: path, Line: i + 1, Context: strings.TrimSpace(line), Archive: archive})
			}
		}
	}
	return cleanCheckReferences(refs)
}

func isArchivePath(path string) bool {
	path = strings.ToLower(filepath.ToSlash(path))
	return strings.Contains(path, "/archive/") || strings.HasPrefix(path, "archive/") || strings.Contains(path, "/archived/")
}

func commandSnippetsForScan(line string, docs bool) []string {
	if !docs {
		return []string{line}
	}
	var snippets []string
	codeRe := regexp.MustCompile("`([^`]+)`")
	for _, match := range codeRe.FindAllStringSubmatch(line, -1) {
		if len(match) == 2 {
			snippets = append(snippets, match[1])
		}
	}
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "readyci ") || strings.HasPrefix(trimmed, "$(READYCI) ") || strings.HasPrefix(trimmed, "make ") {
		snippets = append(snippets, trimmed)
	}
	return snippets
}

func normalizeShellLineContinuations(text string) string {
	var out strings.Builder
	for i := 0; i < len(text); i++ {
		if text[i] == '\\' {
			if i+1 < len(text) && text[i+1] == '\n' {
				out.WriteByte(' ')
				i++
				continue
			}
			if i+2 < len(text) && text[i+1] == '\r' && text[i+2] == '\n' {
				out.WriteByte(' ')
				i += 2
				continue
			}
		}
		out.WriteByte(text[i])
	}
	return out.String()
}

func isVerificationDocPath(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))
	for _, marker := range []string{"ci", "readyci", "verification", "agent"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func parseInlineList(value string) []string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "[]")
	parts := strings.Split(value, ",")
	var out []string
	for _, part := range parts {
		part = strings.Trim(strings.TrimSpace(part), `"'`)
		if part != "" {
			out = append(out, part)
		}
	}
	if len(out) == 0 && value != "" {
		out = append(out, strings.Trim(value, `"'`))
	}
	return uniqueStrings(out)
}
