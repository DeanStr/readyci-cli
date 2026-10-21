package syncbundle

import (
	"fmt"
	"path"
	"strings"
)

// IsPublicEnvExamplePath identifies the existing public dotenv template
// allowance. Explicit policy exclusions still apply to these paths.
func IsPublicEnvExamplePath(rel string) bool {
	base := path.Base(CleanSlashPath(rel))
	return base == ".env.example" || strings.HasSuffix(base, ".env.example")
}

func validateSourceFilterEntries(manifest Manifest) error {
	untracked := make(map[string]bool, len(manifest.UntrackedFiles))
	for _, rel := range manifest.UntrackedFiles {
		untracked[rel] = true
	}
	defaults := DefaultSourceExcludePatterns()
	reviewed := make(map[string]bool, len(manifest.ReviewedFixtures))
	for _, fixture := range manifest.ReviewedFixtures {
		reviewed[fixture.Path] = true
	}
	for _, group := range []struct {
		name    string
		entries []Entry
	}{{"baseline", manifest.Baseline}, {"workspace", manifest.Workspace}} {
		for _, entry := range group.entries {
			if manifest.SourcePolicy != nil && len(manifest.SourcePolicy.Scopes) > 0 {
				if !SourcePolicyAllowsPath(*manifest.SourcePolicy, entry.Path, group.name == "workspace" && untracked[entry.Path]) {
					return fmt.Errorf("no registered source scope admits %s entry %q", group.name, entry.Path)
				}
				continue
			}
			if manifest.SourcePolicy != nil && PathMatchesExcludePatterns(manifest.SourcePolicy.ExcludePatterns, entry.Path) {
				return fmt.Errorf("source policy excludes %s entry %q", group.name, entry.Path)
			}
			for _, pattern := range defaults {
				if pattern == ".env*" && (IsPublicEnvExamplePath(entry.Path) || reviewed[entry.Path]) {
					continue
				}
				if PathMatchesExcludePatterns([]string{pattern}, entry.Path) {
					return fmt.Errorf("confidentiality exclusion blocks %s entry %q without a reviewed fixture", group.name, entry.Path)
				}
			}
		}
	}
	return nil
}
