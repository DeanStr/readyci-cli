package verificationplan

import (
	"fmt"
	"sort"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

// RuleMatch records why an affected rule selected a check: which rule, which
// pattern, and which changed path matched, plus the config source when the rule
// came from an expanded profile template.
type RuleMatch struct {
	Rule           int    `json:"rule,omitempty"`
	Pattern        string `json:"pattern"`
	Path           string `json:"path"`
	SourceFile     string `json:"source_file,omitempty"`
	SourceLine     int    `json:"source_line,omitempty"`
	SourceTemplate string `json:"source_template,omitempty"`
}

// SkippedCheck records a check that an affected rule matched but that was not
// selected, either because its `when` conditions did not hold or because another
// selected check subsumes it.
type SkippedCheck struct {
	Name         string      `json:"name"`
	Status       string      `json:"status"`
	Reason       string      `json:"reason"`
	SubsumedBy   []string    `json:"subsumed_by,omitempty"`
	MatchedPaths []string    `json:"matched_paths,omitempty"`
	Reasons      []RuleMatch `json:"reasons,omitempty"`
}

// SelectChecks applies affected rules to a per-check changed-file set and
// returns the selected checks, why each was selected, and what was skipped.
//
// changedFilesByCheck is per-check because checks may exclude different paths
// from their workspace. A caller that has only one changed-file list (the
// service, reading a validated bundle manifest) supplies the same list for every
// check, which is equivalent while one session means one workspace — see
// compatibleAffectedSessionBuildOptions and ADR 0007.
func SelectChecks(cfg config.ProjectConfig, changedFilesByCheck map[string][]string, whenContext WhenContext) ([]string, map[string][]RuleMatch, []SkippedCheck, error) {
	return selectChecks(cfg, changedFilesByCheck, whenContext, nil)
}

// selectChecks additionally excludes named checks from selection, reporting each
// with the supplied reason.
//
// Exclusion happens here rather than by filtering the result, because a check
// that never enters the selected set also never acts as a subsuming parent.
// Filtering afterwards would let an excluded check prune a child that could
// safely have run, leaving neither in the plan.
func selectChecks(cfg config.ProjectConfig, changedFilesByCheck map[string][]string, whenContext WhenContext, excludedChecks map[string]string) ([]string, map[string][]RuleMatch, []SkippedCheck, error) {
	selected := make([]string, 0)
	seen := make(map[string]bool)
	reasons := make(map[string][]RuleMatch)
	whenSkipped := make(map[string]SkippedCheck)
	ruleMatchedPaths := make(map[string]bool)
	for ruleIndex, rule := range cfg.Affected.Rules {
		for _, pattern := range rule.Paths {
			trimmedPattern := strings.TrimSpace(pattern)
			if trimmedPattern == "" {
				continue
			}
			for _, checkName := range rule.Checks {
				trimmedCheck := strings.TrimSpace(checkName)
				if trimmedCheck == "" {
					continue
				}
				for _, changedFile := range changedFilesByCheck[trimmedCheck] {
					match, err := PatternMatches(trimmedPattern, changedFile)
					if err != nil {
						return nil, nil, nil, fmt.Errorf("affected.rules[%d].paths: %w", ruleIndex, err)
					}
					if !match {
						continue
					}
					excluded, _, err := RuleExcludes(rule, changedFile)
					if err != nil {
						return nil, nil, nil, fmt.Errorf("affected.rules[%d].exclude: %w", ruleIndex, err)
					}
					if excluded {
						continue
					}
					reason := RuleMatch{Rule: ruleIndex + 1, Pattern: trimmedPattern, Path: NormalizePath(changedFile)}
					ruleMatchedPaths[reason.Path] = true
					if source, ok := rule.PathSources[trimmedPattern]; ok {
						reason.SourceFile = source.File
						reason.SourceLine = source.Line
						reason.SourceTemplate = source.Template
					}
					if !seen[trimmedCheck] {
						if excludedReason, isExcluded := excludedChecks[trimmedCheck]; isExcluded {
							addWhenSkippedCheck(whenSkipped, trimmedCheck, excludedReason, reason)
							continue
						}
						if check, ok := cfg.Checks[trimmedCheck]; ok {
							if ok, skipReason := WhenMatches(check.When, whenContext); !ok {
								addWhenSkippedCheck(whenSkipped, trimmedCheck, skipReason, reason)
								continue
							}
						}
						seen[trimmedCheck] = true
						selected = append(selected, trimmedCheck)
					}
					reasons[trimmedCheck] = append(reasons[trimmedCheck], reason)
				}
			}
		}
	}
	for _, checkName := range cfg.Affected.UnmatchedChecks {
		trimmedCheck := strings.TrimSpace(checkName)
		if trimmedCheck == "" {
			continue
		}
		for _, changedFile := range changedFilesByCheck[trimmedCheck] {
			path := NormalizePath(changedFile)
			if path == "" || ruleMatchedPaths[path] {
				continue
			}
			reason := RuleMatch{Pattern: "affected.unmatched_checks", Path: path}
			if !seen[trimmedCheck] {
				if excludedReason, isExcluded := excludedChecks[trimmedCheck]; isExcluded {
					addWhenSkippedCheck(whenSkipped, trimmedCheck, excludedReason, reason)
					continue
				}
				if check, ok := cfg.Checks[trimmedCheck]; ok {
					if ok, skipReason := WhenMatches(check.When, whenContext); !ok {
						addWhenSkippedCheck(whenSkipped, trimmedCheck, skipReason, reason)
						continue
					}
				}
				seen[trimmedCheck] = true
				selected = append(selected, trimmedCheck)
			}
			reasons[trimmedCheck] = append(reasons[trimmedCheck], reason)
		}
	}
	selected, skipped := PruneSubsumedChecks(cfg, selected, reasons)
	if len(whenSkipped) > 0 {
		for _, name := range sortedSkippedCheckNames(whenSkipped) {
			skipped = append(skipped, whenSkipped[name])
		}
	}
	return selected, reasons, skipped, nil
}

func addWhenSkippedCheck(skipped map[string]SkippedCheck, name, reason string, match RuleMatch) {
	current := skipped[name]
	if current.Name == "" {
		current = SkippedCheck{
			Name:   name,
			Status: "skipped",
			Reason: reason,
		}
	}
	current.Reasons = append(current.Reasons, match)
	current.MatchedPaths = matchedPaths(current.Reasons)
	skipped[name] = current
}

func sortedSkippedCheckNames(skipped map[string]SkippedCheck) []string {
	names := make([]string, 0, len(skipped))
	for name := range skipped {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// RuleExcludes reports whether a rule's exclude patterns remove a matched file.
func RuleExcludes(rule config.AffectedRule, file string) (bool, string, error) {
	for _, pattern := range rule.Exclude {
		match, err := PatternMatches(pattern, file)
		if err != nil {
			return false, "", err
		}
		if match {
			return true, pattern, nil
		}
	}
	return false, "", nil
}

// PruneSubsumedChecks drops checks whose matched paths are fully covered by a
// selected check that subsumes them.
func PruneSubsumedChecks(cfg config.ProjectConfig, selected []string, reasons map[string][]RuleMatch) ([]string, []SkippedCheck) {
	if len(selected) == 0 || len(cfg.Affected.Subsumes) == 0 {
		return selected, nil
	}
	selectedSet := make(map[string]bool, len(selected))
	for _, check := range selected {
		selectedSet[check] = true
	}
	coverageByCheck := make(map[string]map[string]bool, len(reasons))
	for check, matches := range reasons {
		for _, match := range matches {
			path := NormalizePath(match.Path)
			if path == "" {
				continue
			}
			if coverageByCheck[check] == nil {
				coverageByCheck[check] = make(map[string]bool)
			}
			coverageByCheck[check][path] = true
		}
	}
	coveredBySubsumingParent := make(map[string]map[string]bool)
	parentsByChild := make(map[string]map[string]bool)
	for _, parent := range selected {
		parentCoverage := coverageByCheck[parent]
		if len(parentCoverage) == 0 {
			continue
		}
		for _, child := range subsumedClosure(cfg.Affected.Subsumes, parent) {
			if !selectedSet[child] {
				continue
			}
			if coveredBySubsumingParent[child] == nil {
				coveredBySubsumingParent[child] = make(map[string]bool)
			}
			if parentsByChild[child] == nil {
				parentsByChild[child] = make(map[string]bool)
			}
			parentsByChild[child][parent] = true
			for path := range parentCoverage {
				coveredBySubsumingParent[child][path] = true
			}
		}
	}
	pruned := make(map[string]bool)
	for _, check := range selected {
		checkCoverage := coverageByCheck[check]
		parentCoverage := coveredBySubsumingParent[check]
		if len(checkCoverage) == 0 || len(parentCoverage) == 0 {
			continue
		}
		allCovered := true
		for path := range checkCoverage {
			if !parentCoverage[path] {
				allCovered = false
				break
			}
		}
		if allCovered {
			pruned[check] = true
		}
	}
	if len(pruned) == 0 {
		return selected, nil
	}
	kept := selected[:0]
	skipped := make([]SkippedCheck, 0, len(pruned))
	for _, check := range selected {
		if pruned[check] {
			skipped = append(skipped, SkippedCheck{
				Name:         check,
				Status:       "skipped",
				Reason:       "subsumed",
				SubsumedBy:   sortedBoolMapKeys(parentsByChild[check]),
				MatchedPaths: matchedPaths(reasons[check]),
				Reasons:      append([]RuleMatch(nil), reasons[check]...),
			})
			delete(reasons, check)
			continue
		}
		kept = append(kept, check)
	}
	return kept, skipped
}

func matchedPaths(reasons []RuleMatch) []string {
	seen := make(map[string]bool, len(reasons))
	paths := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		path := NormalizePath(reason.Path)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func sortedBoolMapKeys(values map[string]bool) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func subsumedClosure(subsumes map[string]config.CommandList, parent string) []string {
	seen := make(map[string]bool)
	var out []string
	var visit func(string)
	visit = func(check string) {
		for _, child := range subsumes[check] {
			child = strings.TrimSpace(child)
			if child == "" || seen[child] {
				continue
			}
			seen[child] = true
			out = append(out, child)
			visit(child)
		}
	}
	visit(strings.TrimSpace(parent))
	return out
}
