package verificationplan

import (
	"github.com/DeanStr/readyci-cli/pkg/config"
)

// HasCheckWhenChanges reports whether any check selects itself through
// when.changes rather than through an affected rule.
func HasCheckWhenChanges(cfg config.ProjectConfig) bool {
	for _, check := range cfg.Checks {
		if len(check.When.Changes.Include) > 0 {
			return true
		}
	}
	return false
}

// ExpandWhenChangesRules synthesizes an affected rule for every check that
// selects itself through `when.changes`. Selection is expressed entirely as
// affected rules, so a check configured this way is invisible to it until the
// rule exists. Callers that build per-check changed-file sets by walking rules
// must expand first, or such a check will be missing from the set as well.
//
// Expansion is idempotent: a check that already has an equivalent rule is left
// alone.
func ExpandWhenChangesRules(cfg config.ProjectConfig) config.ProjectConfig {
	if len(cfg.Checks) == 0 {
		return cfg
	}
	rules := append([]config.AffectedRule(nil), cfg.Affected.Rules...)
	for _, name := range SortedCheckNames(cfg.Checks) {
		check := cfg.Checks[name]
		if len(check.When.Changes.Include) == 0 {
			continue
		}
		if hasEquivalentAffectedRule(rules, name, check.When.Changes.Include, check.When.Changes.Exclude) {
			continue
		}
		rules = append(rules, config.AffectedRule{
			Paths:   append([]string(nil), check.When.Changes.Include...),
			Exclude: append([]string(nil), check.When.Changes.Exclude...),
			Checks:  config.CommandList{name},
		})
	}
	cfg.Affected.Rules = rules
	return cfg
}

func hasEquivalentAffectedRule(rules []config.AffectedRule, checkName string, include, exclude []string) bool {
	for _, rule := range rules {
		if len(rule.Checks) != 1 || rule.Checks[0] != checkName {
			continue
		}
		if stringSlicesEqual(rule.Paths, include) && stringSlicesEqual(rule.Exclude, exclude) {
			return true
		}
	}
	return false
}

func stringSlicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
