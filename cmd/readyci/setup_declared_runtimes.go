package main

import (
	"sort"
	"strconv"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

// setupDeclaredRuntimeRequirementsForSelection returns the selected
// requirements combined with the repository's runtime declarations, plus
// unresolved items. Declaration inspection is static and confined to ordinary
// repository files; only selected command runtimes participate, so an
// unrelated language declaration does not constrain a different test job.
// Declarations are advisory and never fail setup: a declaration is sealed only
// when it is a supported numeric requirement for a supported family that
// agrees with the selection. Anything else, including runtimes outside the
// supported families, moving or named selectors, unsupported syntax, unmatched
// integrity pins, conflicts and uninspectable scopes, becomes an unresolved
// item and leaves the runner image's installed toolchain in use.
func setupDeclaredRuntimeRequirementsForSelection(root string, commands []string, selected map[string]string, additionalRuntimes ...string) (map[string]string, []setupUnresolvedItem) {
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
	var activeRuntimes []string
	for runtime := range active {
		activeRuntimes = append(activeRuntimes, runtime)
	}
	sort.Strings(activeRuntimes)
	declarations, warnings := setupRuntimeRequirementsForSelection(root, commands, selected, activeRuntimes...)
	var items []setupUnresolvedItem
	for _, warning := range warnings {
		items = append(items, setupInspectionReview(warning))
	}
	result := preflightapi.CloneRuntimeRequirements(selected)
	if result == nil {
		result = map[string]string{}
	}
	declaredUnsupported := map[string]bool{}
	for _, declaration := range declarations {
		name, value := declaration.Runtime, strings.TrimSpace(declaration.Value)
		if name == "package_manager" {
			if !active["node"] {
				continue
			}
			manager, version, found := strings.Cut(value, "@")
			if !found || manager == "" || version == "" {
				items = append(items, setupDeclarationReview(setupUnresolvedRuntimeDeclarationNotApplied, "package manager", declaration.Source, value, "it does not name an exact package-manager version", "set runtime_requirements explicitly"))
				continue
			}
			name, value = manager, version
		} else if !active[name] {
			continue
		}
		// The note quotes the declaration as written, including a manager prefix.
		declared := strings.TrimSpace(declaration.Value)
		if !preflightapi.SupportedRuntimeRequirement(name) {
			declaredUnsupported[name] = true
			items = append(items, setupDeclarationReview(setupUnresolvedRuntimeFamilyUnsupported, name, declaration.Source, declared, name+" is not a supported runtime requirement family", "select a runner image that provides it"))
			continue
		}
		requirement, reason := setupTranslatableRuntimeRequirement(name, value, setupDeclarationNormalizer(declaration.Source))
		if reason == "" {
			merged, err := preflightapi.MergeRuntimeRequirements(result, map[string]string{name: requirement})
			if err == nil {
				result = merged
				continue
			}
			reason = "it conflicts with the " + name + " requirement " + strconv.Quote(result[name]) + " from the selection or another declaration"
		}
		items = append(items, setupDeclarationReview(setupUnresolvedRuntimeDeclarationNotApplied, name, declaration.Source, declared, reason, "set runtime_requirements."+name+" explicitly"))
	}
	for _, runtime := range activeRuntimes {
		switch {
		case !preflightapi.SupportedRuntimeRequirement(runtime):
			if !declaredUnsupported[runtime] {
				items = append(items, setupUnresolvedItem{
					Kind: setupUnresolvedRuntimeFamilyUnsupported, Subject: runtime, Source: "selected command",
					Detail: setupRuntimeNoteDetail(runtime+" (used by the selected command)", runtime, runtime+" is not a supported runtime requirement family", "select a runner image that provides it"),
				})
			}
		case result[runtime] == "":
			result[runtime] = "installed"
		}
	}
	if len(result) == 0 {
		result = nil
	}
	return result, mergeSetupUnresolved(items)
}

// setupDeclarationNormalizer translates each declaration format's own range
// semantics into Ready CI's numeric grammar.
func setupDeclarationNormalizer(source string) func(string, string) (string, error) {
	return func(runtime, value string) (string, error) {
		switch {
		case runtime == "node" && strings.Contains(source, "engines.node"):
			return preflightapi.NormalizeNodeRuntimeRequirement(value)
		case runtime == "go":
			value = strings.TrimPrefix(value, "go")
			if strings.Contains(source, "go.mod (") {
				value = ">=" + value
			}
		case runtime == "rust" && strings.Contains(source, "rust-version"):
			value = ">=" + value
		}
		return setupNumericRuntimeRequirement(runtime, value)
	}
}
