package config

import (
	"fmt"
	"sort"
	"strings"
)

const DefaultDependencyCheck = "dependencies"

// IsDependencyCheck identifies installer definitions, which never implicitly
// inherit the project's default installer themselves.
func IsDependencyCheck(cfg ProjectConfig, name string) bool {
	if name == "" {
		return false
	}
	if name == DefaultDependencyCheck {
		_, exists := cfg.Checks[name]
		return exists
	}
	for _, check := range cfg.Checks {
		if check.DependencyCheck == name {
			return true
		}
	}
	return false
}

func DependencyCheckFor(cfg ProjectConfig, name string) (string, error) {
	check, exists := cfg.Checks[name]
	if !exists {
		return "", fmt.Errorf("unknown check %q", name)
	}
	if check.DependencyCheck != "" {
		dependency := check.DependencyCheck
		if strings.TrimSpace(dependency) != dependency {
			return "", fmt.Errorf("checks.%s.dependency_check must be a check name without surrounding whitespace", name)
		}
		warm, exists := cfg.Checks[dependency]
		if !exists {
			return "", fmt.Errorf("checks.%s.dependency_check references undefined check %q", name, dependency)
		}
		if dependency == name || warm.DependencyCheck != "" {
			return "", fmt.Errorf("checks.%s.dependency_check cannot reference itself or a check with another dependency_check", name)
		}
		return dependency, nil
	}
	if IsDependencyCheck(cfg, name) {
		return "", nil
	}
	if _, exists := cfg.Checks[DefaultDependencyCheck]; exists {
		return DefaultDependencyCheck, nil
	}
	return "", nil
}

func ValidateDependencyChecks(cfg ProjectConfig) error {
	names := make([]string, 0, len(cfg.Checks))
	for name := range cfg.Checks {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if IsDependencyCheck(cfg, name) && cfg.Checks[name].DependencyCheck != "" {
			return fmt.Errorf("checks.%s.dependency_check cannot nest dependency installers", name)
		}
		if _, err := DependencyCheckFor(cfg, name); err != nil {
			return err
		}
	}
	return nil
}
