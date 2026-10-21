package main

import (
	"fmt"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

// Declaration inspection is static and confined to ordinary repository files.
// Only selected command runtimes participate; an unrelated language declaration
// does not constrain a different test job.
func setupDeclaredRuntimeRequirements(root string, commands []string, additionalRuntimes ...string) (map[string]string, error) {
	return setupDeclaredRuntimeRequirementsForSelection(root, commands, nil, additionalRuntimes...)
}

func setupDeclaredRuntimeRequirementsForSelection(root string, commands []string, selected map[string]string, additionalRuntimes ...string) (map[string]string, error) {
	active := map[string]bool{}
	for runtime := range selected {
		active[runtime] = true
	}
	for _, runtime := range additionalRuntimes {
		active[runtime] = true
	}
	for _, command := range commands {
		for _, runtime := range setupCommandRuntimes(root, command) {
			active[runtime] = true
		}
	}
	if active["npm"] || active["pnpm"] || active["yarn"] {
		active["node"] = true
	}
	if active["cargo"] {
		active["rust"] = true
	}
	var selectedRuntimes []string
	for runtime := range active {
		selectedRuntimes = append(selectedRuntimes, runtime)
	}
	declarations, warnings := setupRuntimeRequirementsForSelection(root, commands, selected, selectedRuntimes...)
	if len(warnings) > 0 {
		return nil, fmt.Errorf("runtime declarations require explicit configuration: %s", strings.Join(warnings, "; "))
	}
	result := map[string]string{}
	for runtime := range active {
		result[runtime] = "installed"
	}
	for _, declaration := range declarations {
		name, value := declaration.Runtime, strings.TrimSpace(declaration.Value)
		if name == "package_manager" {
			if !active["node"] {
				continue
			}
			manager, version, found := strings.Cut(value, "@")
			if !found || manager == "" || version == "" {
				return nil, fmt.Errorf("runtime declaration %s requires an exact supported package manager version", declaration.Source)
			}
			name, value = manager, version
			if strings.Contains(value, "+") {
				if _, _, verifiedPin := preflightapi.PackageManagerIntegrityRequirement(value); name != "pnpm" || !verifiedPin {
					return nil, fmt.Errorf("runtime declaration %s includes unsupported package-manager integrity metadata; use an exact pnpm version with a complete sha512 integrity pin", declaration.Source)
				}
			}
		} else if !active[name] {
			continue
		}
		switch name {
		case "node":
			if strings.Contains(declaration.Source, "engines.node") {
				var err error
				value, err = preflightapi.NormalizeNodeRuntimeRequirement(value)
				if err != nil {
					return nil, fmt.Errorf("runtime declaration %s: %w", declaration.Source, err)
				}
			}
		case "go":
			value = strings.TrimPrefix(value, "go")
			if strings.Contains(declaration.Source, "go.mod (") {
				value = ">=" + value
			}
		case "rust":
			if strings.Contains(declaration.Source, "rust-version") {
				value = ">=" + value
			}
		}
		merged, err := preflightapi.MergeRuntimeRequirements(result, map[string]string{name: value})
		if err != nil {
			return nil, fmt.Errorf("runtime declaration %s: %w", declaration.Source, err)
		}
		result = merged
	}
	return preflightapi.NormalizeRuntimeRequirements(result)
}

func validateSetupRuntimeTemplate(root, template string) error {
	cfg, err := config.ParseProjectConfig([]byte(template), "generated setup configuration")
	if err != nil {
		return err
	}
	for name, check := range cfg.Checks {
		commands := append([]string{check.Command}, check.Prepare...)
		for _, step := range check.Steps {
			commands = append(commands, setupRuntimeCommandWithEnvironment(step.Command, step.Env))
		}
		if blockers := setupDependencyReviewRequirements(root, commands, check.Prepare...); len(blockers) > 0 {
			return cliConfigError(strings.Join(blockers, "; "), nil)
		}
		var runtimes []string
		for runtime := range check.RuntimeRequirements {
			runtimes = append(runtimes, runtime)
		}
		declarations, err := setupDeclaredRuntimeRequirementsForSelection(root, commands, check.RuntimeRequirements, runtimes...)
		if err != nil {
			return cliConfigError(fmt.Sprintf("check %s: %s", name, err), nil)
		}
		if _, err := preflightapi.MergeRuntimeRequirements(check.RuntimeRequirements, declarations); err != nil {
			return cliConfigError(fmt.Sprintf("check %s: %s", name, err), nil)
		}
	}
	return nil
}
