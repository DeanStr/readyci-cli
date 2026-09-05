package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

var builtInAffectedProfiles = map[string][]affectedProfileRule{
	"docs": {
		{Paths: []string{"docs/**", "README.md", "AGENTS.md"}, CheckCandidates: []string{"docs-lint", "docs"}, FallbackCheckGroups: [][]string{{"ci"}}},
	},
	"go": {
		{Paths: []string{"go.mod", "go.sum", "*/**/go.mod", "*/**/go.sum", "**/*.go"}, CheckCandidates: []string{"go"}, FallbackCheckGroups: [][]string{{"ci"}}},
	},
	"readyci": {
		{Paths: []string{".readyci.yml", ".readyci.yaml", ".readyciignore"}, CheckCandidates: []string{"ci"}, FallbackCheckGroups: [][]string{{"test", "affected"}}},
	},
	"shell": {
		{Paths: []string{"**/*.sh", "scripts/git-hooks/**"}, CheckCandidates: []string{"shell-syntax", "shell"}, FallbackCheckGroups: [][]string{{"ci"}}},
	},
}

var hostedAffectedProfiles = map[string]struct{}{
	"go-postgres":    {},
	"migrations":     {},
	"openapi":        {},
	"ops":            {},
	"repo-config":    {},
	"rust-workspace": {},
	"vite-react":     {},
}

func expandAffectedConfig(cfg config.ProjectConfig) (config.ProjectConfig, []string, error) {
	root, err := inferenceRoot(".")
	if err != nil {
		return cfg, nil, err
	}
	return expandAffectedConfigFromRoot(root, cfg)
}

func expandAffectedConfigForConfigPath(cfg config.ProjectConfig, configPath string) (config.ProjectConfig, []string, error) {
	if strings.TrimSpace(configPath) == "" {
		return expandAffectedConfig(cfg)
	}
	return expandAffectedConfigFromRoot(filepath.Dir(configPath), cfg)
}

func expandAffectedConfigFromRoot(root string, cfg config.ProjectConfig) (config.ProjectConfig, []string, error) {
	expanded := cfg
	var warnings []string
	var generated []config.AffectedRule
	seenProfiles := make(map[string]bool)
	for _, rawProfile := range cfg.Affected.Profiles {
		profile := strings.TrimSpace(rawProfile)
		if profile == "" {
			return cfg, nil, errors.New("affected.profiles contains an empty profile")
		}
		if profile != rawProfile {
			return cfg, nil, fmt.Errorf("affected.profiles contains a whitespace-padded profile %q", rawProfile)
		}
		if seenProfiles[profile] {
			return cfg, nil, fmt.Errorf("affected.profiles contains duplicate profile %q", profile)
		}
		seenProfiles[profile] = true
		rules, ok := affectedProfileRules(profile)
		if !ok {
			return cfg, nil, unknownAffectedProfileError(profile)
		}
		applied := 0
		for _, rule := range rules {
			checks := existingAffectedProfileChecks(cfg.Checks, rule)
			if len(checks) == 0 {
				continue
			}
			generated = append(generated, config.AffectedRule{
				Paths:   append([]string(nil), rule.Paths...),
				Exclude: nil,
				Checks:  config.CommandList(checks),
			})
			applied++
		}
		if applied == 0 {
			warnings = append(warnings, fmt.Sprintf("affected profile %q did not match any configured check names", profile))
		}
	}
	if len(generated) > 0 {
		expanded.Affected.Rules = append(generated, expanded.Affected.Rules...)
	}
	expanded.Affected.Profiles = nil
	withManifests, err := expandAffectedPathsFromRoot(root, expanded)
	if err != nil {
		return cfg, nil, err
	}
	return withManifests, warnings, nil
}

func expandAffectedPathsFromRoot(root string, cfg config.ProjectConfig) (config.ProjectConfig, error) {
	if len(cfg.Affected.Rules) == 0 {
		return cfg, nil
	}
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return cfg, err
	}
	rules := make([]config.AffectedRule, 0, len(cfg.Affected.Rules))
	for ruleIndex, rule := range cfg.Affected.Rules {
		if len(rule.PathsFrom) == 0 {
			rules = append(rules, rule)
			continue
		}
		expandedPaths := append([]string(nil), rule.Paths...)
		pathSources := copyAffectedPathSources(rule.PathSources)
		for sourceIndex, source := range rule.PathsFrom {
			paths, err := affectedPathsFromManifest(root, source)
			if err != nil {
				return cfg, fmt.Errorf("affected.rules[%d].paths_from[%d]: %w", ruleIndex, sourceIndex, err)
			}
			for _, path := range paths {
				expandedPaths = append(expandedPaths, path.Path)
				if _, ok := pathSources[path.Path]; !ok {
					pathSources[path.Path] = path.Source
				}
			}
		}
		rule.Paths = uniqueStrings(expandedPaths)
		rule.PathsFrom = nil
		if len(pathSources) > 0 {
			rule.PathSources = pathSources
		}
		rules = append(rules, rule)
	}
	cfg.Affected.Rules = rules
	return cfg, nil
}

type affectedManifestPath struct {
	Path   string
	Source config.AffectedPathSource
}

func affectedPathsFromManifest(root string, source config.AffectedPathsFrom) ([]affectedManifestPath, error) {
	file := strings.TrimSpace(source.File)
	if file == "" {
		return nil, errors.New("file is required")
	}
	manifestPath, err := safeAffectedPathsFromManifestPath(root, file)
	if err != nil {
		return nil, err
	}
	template := strings.TrimSpace(source.Template)
	if template == "" {
		template = "${line}"
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", file, err)
	}
	var paths []affectedManifestPath
	seen := make(map[string]bool)
	for index, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		pathPattern := strings.ReplaceAll(template, "{{line}}", line)
		pathPattern = strings.ReplaceAll(pathPattern, "${line}", line)
		pathPattern = strings.ReplaceAll(pathPattern, "{line}", line)
		if filepath.IsAbs(pathPattern) {
			return nil, fmt.Errorf("expanded path %q from %s:%d must be repository-relative", pathPattern, file, index+1)
		}
		if strings.TrimSpace(pathPattern) == "" {
			continue
		}
		if err := validateAffectedPattern(pathPattern); err != nil {
			return nil, fmt.Errorf("expanded path %q from %s:%d: %w", pathPattern, file, index+1, err)
		}
		pathPattern = normalizeAffectedPath(pathPattern)
		if pathPattern == "" || seen[pathPattern] {
			continue
		}
		seen[pathPattern] = true
		paths = append(paths, affectedManifestPath{
			Path: pathPattern,
			Source: config.AffectedPathSource{
				File:     file,
				Line:     index + 1,
				Template: template,
			},
		})
	}
	return paths, nil
}

func safeAffectedPathsFromManifestPath(root string, file string) (string, error) {
	if filepath.IsAbs(file) || strings.Contains(filepath.Clean(file), ".."+string(filepath.Separator)) || filepath.Clean(file) == ".." {
		return "", fmt.Errorf("file %q must be repository-relative", file)
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	cleanFile := filepath.Clean(filepath.FromSlash(file))
	if syncbundle.IsGitControlPath(filepath.ToSlash(cleanFile)) {
		return "", fmt.Errorf("file %q must not target git control metadata", file)
	}
	fullPath := filepath.Join(rootAbs, cleanFile)
	rel, err := filepath.Rel(rootAbs, fullPath)
	if err != nil {
		return "", err
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("file %q must stay within the repository", file)
	}
	current := rootAbs
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", file, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("file %q must not contain symlinks", file)
		}
	}
	return fullPath, nil
}

func copyAffectedPathSources(values map[string]config.AffectedPathSource) map[string]config.AffectedPathSource {
	if len(values) == 0 {
		return make(map[string]config.AffectedPathSource)
	}
	out := make(map[string]config.AffectedPathSource, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func affectedProfileExpansionError(original config.ProjectConfig, expanded config.ProjectConfig, warnings []string) error {
	if len(original.Affected.Profiles) == 0 || len(original.Affected.Rules) > 0 || len(expanded.Affected.Rules) > 0 {
		return nil
	}
	if len(warnings) > 0 {
		return errors.New(warnings[0])
	}
	return cliConfigError("affected.profiles did not match any configured check names", nil)
}

func affectedProfileRules(profile string) ([]affectedProfileRule, bool) {
	rules, ok := builtInAffectedProfiles[profile]
	return rules, ok
}

func unknownAffectedProfileError(profile string) error {
	if _, hosted := hostedAffectedProfiles[profile]; hosted {
		return cliConfigError(fmt.Sprintf("unknown affected profile %q: this hosted profile moved to `readyci plan --recommend --suggest-config`; convert it to explicit affected.rules (available local profiles: %s)", profile, strings.Join(knownAffectedProfiles(), ", ")), nil)
	}
	return cliConfigError(fmt.Sprintf("unknown affected profile %q (available: %s)", profile, strings.Join(knownAffectedProfiles(), ", ")), nil)
}

func knownAffectedProfiles() []string {
	names := make([]string, 0, len(builtInAffectedProfiles))
	for name := range builtInAffectedProfiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func existingAffectedProfileChecks(checks map[string]config.CheckConfig, rule affectedProfileRule) []string {
	groups := make([][]string, 0, 1+len(rule.FallbackCheckGroups))
	groups = append(groups, rule.CheckCandidates)
	groups = append(groups, rule.FallbackCheckGroups...)
	for _, candidates := range groups {
		selected := existingAffectedProfileCheckGroup(checks, candidates)
		if len(selected) > 0 {
			return selected
		}
	}
	return nil
}

func existingAffectedProfileCheckGroup(checks map[string]config.CheckConfig, candidates []string) []string {
	selected := make([]string, 0, len(candidates))
	seen := make(map[string]bool)
	for _, candidate := range candidates {
		if _, ok := checks[candidate]; !ok {
			continue
		}
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		selected = append(selected, candidate)
	}
	return selected
}
