package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func scanRepoMake(cwd string) repoMakeMap {
	var out repoMakeMap
	root, err := os.OpenRoot(cwd)
	if err != nil {
		return out
	}
	defer root.Close()
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
		if !entry.Type().IsRegular() && entry.Type()&os.ModeSymlink == 0 {
			return nil
		}
		switch entry.Name() {
		case "Makefile", "makefile", "GNUmakefile":
		default:
			return nil
		}
		rel := relPath(cwd, path)
		resolved, err := resolveRepoMakeSource(root, rel)
		if err != nil {
			return nil
		}
		data, err := root.ReadFile(resolved)
		if err != nil {
			return nil
		}
		// Make recipes run relative to the logical entrypoint, including a root
		// Makefile symlink. Its resolved file's directory is not the job's cwd.
		out.Files = append(out.Files, rel)
		out.Targets = append(out.Targets, repoMakeTargetsFromSource(data, rel)...)
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

// resolveRepoMakeSource follows only source-compatible internal links. Resolve
// components in filesystem order: cleaning alias/../file before expanding alias
// can turn a workspace escape into an apparently safe path. Root also confines
// the eventual read if the working tree changes during discovery.
func resolveRepoMakeSource(root *os.Root, rel string) (string, error) {
	if _, err := syncbundle.ValidateRelativePath(rel); err != nil {
		return "", err
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	var resolved []string
	hops := 0
	isDir := true
	for len(parts) > 0 {
		if !isDir {
			return "", fmt.Errorf("Make source traverses a regular file")
		}
		part := parts[0]
		parts = parts[1:]
		switch part {
		case "", ".":
			continue
		case "..":
			if len(resolved) == 0 {
				return "", fmt.Errorf("Make source escapes workspace")
			}
			resolved = resolved[:len(resolved)-1]
			continue
		}
		if syncbundle.IsGitControlPath(part) {
			return "", fmt.Errorf("Make source traverses Git control metadata")
		}
		candidate := strings.Join(append(append([]string(nil), resolved...), part), "/")
		info, err := root.Lstat(filepath.FromSlash(candidate))
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			hops++
			if hops > 40 {
				return "", fmt.Errorf("Make source exceeds 40 symlink hops")
			}
			target, err := root.Readlink(filepath.FromSlash(candidate))
			if err != nil {
				return "", err
			}
			if err := syncbundle.ValidateSymlinkTargetSyntax(target); err != nil {
				return "", err
			}
			parts = append(strings.Split(filepath.ToSlash(target), "/"), parts...)
			continue
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return "", fmt.Errorf("Make source is not a regular file")
		}
		resolved = append(resolved, part)
		isDir = info.IsDir()
	}
	if isDir {
		return "", fmt.Errorf("Make source resolves to a directory")
	}
	return filepath.FromSlash(strings.Join(resolved, "/")), nil
}

func repoMakeTargetsFromSource(data []byte, rel string) []repoMakeTarget {
	var targets []repoMakeTarget
	conditionalDepth, defineDepth := 0, 0
	for i, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(line, "\t") {
			fields := strings.Fields(trimmed)
			word := ""
			if len(fields) > 0 {
				word = fields[0]
			}
			if word == "override" && len(fields) > 1 && fields[1] == "define" {
				word = "define"
			}
			switch word {
			case "define":
				defineDepth++
			case "endef":
				if defineDepth > 0 {
					defineDepth--
				}
			case "ifdef", "ifndef", "ifeq", "ifneq":
				if defineDepth == 0 {
					conditionalDepth++
				}
			case "endif":
				if defineDepth == 0 && conditionalDepth > 0 {
					conditionalDepth--
				}
			}
		}
		if defineDepth > 0 {
			continue
		}
		for _, target := range makeRuleTargets(line) {
			targets = append(targets, repoMakeTarget{Name: target, Path: rel, Line: i + 1, Conditional: conditionalDepth > 0})
		}
	}
	return targets
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
	root, err := os.OpenRoot(cwd)
	if err != nil {
		return refs
	}
	defer root.Close()
	for _, file := range makeMap.Files {
		resolved, err := resolveRepoMakeSource(root, file)
		if err != nil {
			continue
		}
		data, err := root.ReadFile(resolved)
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
