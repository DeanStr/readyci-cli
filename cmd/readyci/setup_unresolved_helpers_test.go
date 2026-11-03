package main

import "strings"

// setupDeclaredRuntimeRequirements reviews repository declarations for
// commands without a configured selection and returns the human notes derived
// from the unresolved items.
func setupDeclaredRuntimeRequirements(root string, commands []string, additionalRuntimes ...string) (map[string]string, []string) {
	requirements, items := setupDeclaredRuntimeRequirementsForSelection(root, commands, nil, additionalRuntimes...)
	return requirements, setupUnresolvedNotes(items)
}

func setupDeclaredRuntimeNotesForSelection(root string, commands []string, selected map[string]string) (map[string]string, []string) {
	requirements, items := setupDeclaredRuntimeRequirementsForSelection(root, commands, selected)
	return requirements, setupUnresolvedNotes(items)
}

// setupWarningsRepeatUnresolvedNotes reports whether environment.warnings
// duplicates review text that environment.unresolved already carries.
func setupWarningsRepeatUnresolvedNotes(environment *setupEnvironmentReport) bool {
	for _, warning := range environment.Warnings {
		if strings.HasPrefix(warning, "Runtime review: ") || strings.HasPrefix(warning, "Preparation review: ") {
			return true
		}
	}
	return false
}

func setupUnresolvedKinds(items []setupUnresolvedItem) []string {
	var kinds []string
	for _, item := range items {
		kinds = appendUniqueString(kinds, item.Kind)
	}
	return kinds
}
