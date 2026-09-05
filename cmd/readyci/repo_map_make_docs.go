package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func scanRepoMake(cwd string) repoMakeMap {
	var out repoMakeMap
	_ = filepath.WalkDir(cwd, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if path != cwd && shouldSkipDetectDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		switch entry.Name() {
		case "Makefile", "makefile", "GNUmakefile":
		default:
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		rel := relPath(cwd, path)
		out.Files = append(out.Files, rel)
		for i, line := range strings.Split(string(data), "\n") {
			for _, target := range makeRuleTargets(line) {
				out.Targets = append(out.Targets, repoMakeTarget{Name: target, Path: rel, Line: i + 1})
			}
		}
		return nil
	})
	sort.Slice(out.Targets, func(i, j int) bool {
		if out.Targets[i].Path == out.Targets[j].Path {
			return out.Targets[i].Line < out.Targets[j].Line
		}
		return out.Targets[i].Path < out.Targets[j].Path
	})
	return out
}

func makeRuleTargets(line string) []string {
	if strings.HasPrefix(line, "\t") {
		return nil
	}
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return nil
	}
	colon := strings.IndexByte(trimmed, ':')
	if colon < 0 {
		return nil
	}
	if strings.HasPrefix(strings.TrimSpace(trimmed[colon+1:]), "=") {
		return nil
	}
	var targets []string
	for _, field := range strings.Fields(strings.TrimSpace(trimmed[:colon])) {
		if strings.HasPrefix(field, ".") || !isMakeRuleTargetToken(field) {
			continue
		}
		targets = append(targets, field)
	}
	return targets
}

func isMakeRuleTargetToken(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r == '_' || r == '.' || r == '%' || r == '/' || r == '@' || r == '+' || r == '-' {
			continue
		}
		if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

func checkReferencesFromMake(cwd string, makeMap repoMakeMap) []repoCheckReference {
	var refs []repoCheckReference
	for _, file := range makeMap.Files {
		data, err := os.ReadFile(filepath.Join(cwd, filepath.FromSlash(file)))
		if err != nil {
			continue
		}
		fileRefs := scanCheckReferencesInText(string(data), "make", file, false)
		for i := range fileRefs {
			if target := enclosingMakeTarget(makeMap, file, fileRefs[i].Line); target != "" {
				fileRefs[i].Context = "target " + target + ": " + fileRefs[i].Context
			}
		}
		refs = append(refs, fileRefs...)
	}
	return refs
}

func enclosingMakeTarget(makeMap repoMakeMap, file string, line int) string {
	targetName := ""
	targetLine := 0
	for _, target := range makeMap.Targets {
		if target.Path != file || target.Line >= line || target.Line < targetLine {
			continue
		}
		targetName = target.Name
		targetLine = target.Line
	}
	return targetName
}

func scanRepoDocs(cwd string) ([]repoCheckReference, []repoMakeReference) {
	var checkRefs []repoCheckReference
	var makeRefs []repoMakeReference
	var candidates []string
	_ = filepath.WalkDir(cwd, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if path != cwd && shouldSkipDetectDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel := relPath(cwd, path)
		if entry.Name() == "AGENTS.md" {
			candidates = append(candidates, rel)
			return nil
		}
		if isRepoDocScanCandidate(rel, entry.Name()) {
			candidates = append(candidates, rel)
		}
		return nil
	})
	for _, rel := range uniqueStrings(candidates) {
		data, err := os.ReadFile(filepath.Join(cwd, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		archive := isArchivePath(rel)
		text := repoDocTextForReferenceScan(string(data))
		checkRefs = append(checkRefs, scanCheckReferencesInText(text, "docs", rel, archive)...)
		makeRefs = append(makeRefs, scanMakeReferencesInText(text, "docs", rel, archive)...)
	}
	return checkRefs, makeRefs
}

func repoDocTextForReferenceScan(text string) string {
	lines := strings.Split(text, "\n")
	ignored := false
	for i, line := range lines {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "readyci-doctor: ignore-start") {
			ignored = true
			lines[i] = ""
			continue
		}
		if strings.Contains(lower, "readyci-doctor: ignore-end") {
			ignored = false
			lines[i] = ""
			continue
		}
		if ignored || strings.Contains(lower, "readyci-doctor: ignore-line") {
			lines[i] = ""
		}
	}
	return strings.Join(lines, "\n")
}

func isRepoDocScanCandidate(rel string, name string) bool {
	if !isMarkdownDocName(name) {
		return false
	}
	rel = filepath.ToSlash(rel)
	if strings.HasPrefix(rel, "docs/") {
		return isVerificationDocPath(rel)
	}
	if strings.Contains(rel, "/") {
		return false
	}
	switch strings.TrimSuffix(strings.TrimSuffix(strings.ToLower(name), ".mdx"), ".md") {
	case "readme", "contributing", "development", "developing", "testing", "hacking":
		return true
	default:
		return false
	}
}

func isMarkdownDocName(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".mdx")
}
