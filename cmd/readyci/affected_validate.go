package main

import (
	"fmt"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

func validateAffectedRulesConfig(cfg config.ProjectConfig) error {
	if len(cfg.Affected.UnmatchedChecks) > 0 && len(cfg.Affected.Rules) == 0 {
		return fmt.Errorf("affected.unmatched_checks requires affected.rules")
	}
	seenUnmatched := make(map[string]bool, len(cfg.Affected.UnmatchedChecks))
	for _, checkName := range cfg.Affected.UnmatchedChecks {
		trimmed := strings.TrimSpace(checkName)
		if trimmed == "" {
			return fmt.Errorf("affected.unmatched_checks contains an empty check")
		}
		if trimmed != checkName {
			return fmt.Errorf("affected.unmatched_checks contains a whitespace-padded check %q", checkName)
		}
		if seenUnmatched[trimmed] {
			return fmt.Errorf("affected.unmatched_checks contains duplicate check %q", trimmed)
		}
		seenUnmatched[trimmed] = true
		if _, ok := cfg.Checks[trimmed]; !ok {
			return fmt.Errorf("affected.unmatched_checks references unknown check %q", trimmed)
		}
	}
	for index, rule := range cfg.Affected.Rules {
		if len(rule.Paths) == 0 {
			return fmt.Errorf("affected.rules[%d] must include at least one path pattern", index)
		}
		for _, pattern := range rule.Paths {
			if strings.TrimSpace(pattern) != pattern {
				return fmt.Errorf("affected.rules[%d].paths contains a whitespace-padded path pattern %q", index, pattern)
			}
			if err := validateAffectedPattern(pattern); err != nil {
				return fmt.Errorf("affected.rules[%d].paths: %w", index, err)
			}
		}
		for _, pattern := range rule.Exclude {
			if strings.TrimSpace(pattern) != pattern {
				return fmt.Errorf("affected.rules[%d].exclude contains a whitespace-padded path pattern %q", index, pattern)
			}
			if err := validateAffectedPattern(pattern); err != nil {
				return fmt.Errorf("affected.rules[%d].exclude: %w", index, err)
			}
		}
		if len(rule.Checks) == 0 {
			return fmt.Errorf("affected.rules[%d] must include at least one check", index)
		}
		for _, checkName := range rule.Checks {
			trimmed := strings.TrimSpace(checkName)
			if trimmed == "" {
				return fmt.Errorf("affected.rules[%d].checks contains an empty check", index)
			}
			if trimmed != checkName {
				return fmt.Errorf("affected.rules[%d].checks contains a whitespace-padded check %q", index, checkName)
			}
			if _, ok := cfg.Checks[trimmed]; !ok {
				return fmt.Errorf("affected.rules[%d].checks references unknown check %q", index, trimmed)
			}
		}
	}
	if err := validateAffectedSubsumes(cfg); err != nil {
		return err
	}
	for index, test := range cfg.Affected.Tests {
		paths := affectedTestPaths(test)
		if len(paths) == 0 {
			return fmt.Errorf("affected.tests[%d] must include path or paths", index)
		}
		for _, testPath := range paths {
			if strings.TrimSpace(testPath) != testPath {
				return fmt.Errorf("affected.tests[%d].paths contains a whitespace-padded path %q", index, testPath)
			}
			if err := validateAffectedPattern(testPath); err != nil {
				return fmt.Errorf("affected.tests[%d].paths: %w", index, err)
			}
		}
		if len(test.Checks) == 0 {
			return fmt.Errorf("affected.tests[%d] must include at least one check", index)
		}
		for _, checkName := range test.Checks {
			trimmed := strings.TrimSpace(checkName)
			if trimmed == "" {
				return fmt.Errorf("affected.tests[%d].checks contains an empty check", index)
			}
			if trimmed != checkName {
				return fmt.Errorf("affected.tests[%d].checks contains a whitespace-padded check %q", index, checkName)
			}
			if _, ok := cfg.Checks[trimmed]; !ok {
				return fmt.Errorf("affected.tests[%d].checks references unknown check %q", index, trimmed)
			}
		}
	}
	for index, pattern := range cfg.Affected.AuditIgnore {
		if strings.TrimSpace(pattern) != pattern {
			return fmt.Errorf("affected.audit_ignore[%d] contains a whitespace-padded path pattern %q", index, pattern)
		}
		if err := validateAffectedPattern(pattern); err != nil {
			return fmt.Errorf("affected.audit_ignore[%d]: %w", index, err)
		}
	}
	return nil
}

func validateAffectedSubsumes(cfg config.ProjectConfig) error {
	for parent, children := range cfg.Affected.Subsumes {
		trimmedParent := strings.TrimSpace(parent)
		if trimmedParent == "" {
			return cliConfigError("affected.subsumes contains an empty check", nil)
		}
		if trimmedParent != parent {
			return fmt.Errorf("affected.subsumes contains a whitespace-padded check %q", parent)
		}
		if _, ok := cfg.Checks[parent]; !ok {
			return fmt.Errorf("affected.subsumes references unknown check %q", parent)
		}
		if len(children) == 0 {
			return fmt.Errorf("affected.subsumes.%s must include at least one check", parent)
		}
		seen := make(map[string]bool)
		for _, child := range children {
			trimmedChild := strings.TrimSpace(child)
			if trimmedChild == "" {
				return fmt.Errorf("affected.subsumes.%s contains an empty check", parent)
			}
			if trimmedChild != child {
				return fmt.Errorf("affected.subsumes.%s contains a whitespace-padded check %q", parent, child)
			}
			if child == parent {
				return fmt.Errorf("affected.subsumes.%s must not include itself", parent)
			}
			if seen[child] {
				return fmt.Errorf("affected.subsumes.%s contains duplicate check %q", parent, child)
			}
			seen[child] = true
			if _, ok := cfg.Checks[child]; !ok {
				return fmt.Errorf("affected.subsumes.%s references unknown check %q", parent, child)
			}
		}
	}
	visiting := make(map[string]bool)
	visited := make(map[string]bool)
	var visit func(string) error
	visit = func(check string) error {
		if visiting[check] {
			return fmt.Errorf("affected.subsumes contains a cycle at %q", check)
		}
		if visited[check] {
			return nil
		}
		visiting[check] = true
		for _, child := range cfg.Affected.Subsumes[check] {
			if err := visit(child); err != nil {
				return err
			}
		}
		delete(visiting, check)
		visited[check] = true
		return nil
	}
	for parent := range cfg.Affected.Subsumes {
		if err := visit(parent); err != nil {
			return err
		}
	}
	return nil
}
