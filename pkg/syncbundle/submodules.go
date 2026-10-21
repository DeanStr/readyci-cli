package syncbundle

import (
	"fmt"
	"path"
)

// Submodule records local source provenance, never a remote URL or credentials.
// Its files are materialized under Path in the ordinary bundle namespace. Git
// object databases, hooks, remotes, and native gitlinks are not transported.
type Submodule struct {
	Path   string `json:"path"`
	Commit string `json:"commit"`
}

const SubmoduleProvenancePath = ".git/readyci/submodules.json"

const (
	MaxSubmodules     = 1024
	MaxSubmoduleDepth = 32
)

func validateSubmodules(manifest Manifest) error {
	if len(manifest.Submodules) > MaxSubmodules {
		return fmt.Errorf("source contains more than %d submodules", MaxSubmodules)
	}
	leaves := make(map[string]bool, len(manifest.Baseline)+len(manifest.Workspace))
	for _, entries := range [][]Entry{manifest.Baseline, manifest.Workspace} {
		for _, entry := range entries {
			leaves[entry.Path] = true
		}
	}
	pins := make(map[string]bool, len(manifest.Submodules))
	previous := ""
	for _, module := range manifest.Submodules {
		rel, err := ValidateRelativePath(module.Path)
		if err != nil || rel != module.Path || IsGitControlPath(rel) {
			return fmt.Errorf("submodule path %q must be normalized and confined to source", module.Path)
		}
		if previous != "" && previous >= rel {
			return fmt.Errorf("submodule paths must be unique and sorted: %q", rel)
		}
		previous = rel
		if !validGitCommit(module.Commit) {
			return fmt.Errorf("submodule %q requires a full lowercase Git commit object ID", rel)
		}
		depth := 1
		for current := rel; current != "."; current = path.Dir(current) {
			if leaves[current] {
				return fmt.Errorf("submodule %q conflicts with source entry %q", rel, current)
			}
			if pins[current] {
				depth++
			}
		}
		if depth > MaxSubmoduleDepth {
			return fmt.Errorf("submodule %q exceeds recursion depth %d", rel, MaxSubmoduleDepth)
		}
		pins[rel] = true
	}
	return nil
}

func validGitCommit(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, c := range value {
		if c >= '0' && c <= '9' || c >= 'a' && c <= 'f' {
			continue
		}
		return false
	}
	return true
}
