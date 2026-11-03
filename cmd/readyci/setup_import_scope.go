package main

import (
	"fmt"
	"strconv"
	"strings"
)

// Import scope describes a local configuration choice, not verified coverage of
// the upstream workflow or of the complete repository.
type setupImportScope struct {
	Selected repoImportCandidate   `json:"selected"`
	Omitted  []repoImportCandidate `json:"omitted"`
	Coverage string                `json:"coverage"`
}

func filterSetupImportCandidates(candidates []repoImportCandidate, source string) []repoImportCandidate {
	if source == "" {
		return candidates
	}
	var selected []repoImportCandidate
	for _, candidate := range candidates {
		if candidate.Source == source {
			selected = append(selected, candidate)
		}
	}
	return selected
}

func selectedSetupImportScope(selected repoImportCandidate, candidates []repoImportCandidate) *setupImportScope {
	scope := &setupImportScope{
		Selected: selected,
		Omitted:  []repoImportCandidate{},
		Coverage: "Only the selected candidate is imported. Other upstream verification jobs and matrix variants are not covered by this selection.",
	}
	parentsWithVariants := map[string]bool{}
	for _, candidate := range candidates {
		if candidate.ParentJobID != "" {
			parentsWithVariants[candidate.ParentJobID] = true
		}
	}
	for _, candidate := range candidates {
		if candidate.ID == "" || candidate.ID == selected.ID || candidate.Source != "github-actions" {
			continue
		}
		// The aggregate parent is represented by its concrete siblings when a
		// variant is selected. Listing it as wholly omitted would be misleading.
		if parentsWithVariants[candidate.ID] {
			continue
		}
		scope.Omitted = append(scope.Omitted, candidate)
	}
	return scope
}

func setupImportedTemplateWithScope(root string, options setupOptions, projectID string, selected repoImportCandidate, candidates []repoImportCandidate) string {
	return appendSetupImportScope(setupImportedTemplateForRoot(root, options, projectID, selected), selectedSetupImportScope(selected, candidates))
}

func appendSetupImportScope(template string, scope *setupImportScope) string {
	if scope == nil {
		return template
	}
	var out strings.Builder
	out.WriteString(template)
	// The selected candidate is recorded in the check's imported_from field;
	// these comments only list what the selection leaves out.
	out.WriteString("\n# Ready CI import scope: selected candidate (imported_from) only.\n")
	for _, candidate := range scope.Omitted {
		fmt.Fprintf(&out, "# Not imported: %s", strconv.Quote(candidate.ID))
		if len(candidate.Unsupported) > 0 {
			fmt.Fprintf(&out, "; blocked: %s", strconv.Quote(strings.Join(candidate.Unsupported, "; ")))
		}
		out.WriteByte('\n')
	}
	return out.String()
}
