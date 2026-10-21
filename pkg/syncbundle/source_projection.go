package syncbundle

import "fmt"

// ScopedSource is a deterministic projection of a validated, coherent upload.
// Entries share immutable file bytes with the input rather than copying them.
type ScopedSource struct {
	Manifest  Manifest
	Baseline  []ArchiveEntry
	Workspace []ArchiveEntry
}

func ProjectSource(manifest Manifest, files map[string]BundleFile, policy SourcePolicyBinding) (ScopedSource, error) {
	if manifest.BaselineRef || manifest.SourcePolicy == nil || len(manifest.SourcePolicy.Scopes) < 2 || len(policy.Scopes) != 0 {
		return ScopedSource{}, fmt.Errorf("source projection requires a complete registered multi-scope upload and one member scope")
	}
	key := SourcePolicyScopeKey(policy)
	found := false
	for _, scope := range manifest.SourcePolicy.Scopes {
		found = found || SourcePolicyScopeKey(scope) == key
	}
	if !found {
		return ScopedSource{}, fmt.Errorf("source projection filter is absent from the registered upload contract")
	}
	untracked := make(map[string]bool, len(manifest.UntrackedFiles))
	for _, rel := range manifest.UntrackedFiles {
		untracked[rel] = true
	}
	projected := ScopedSource{Manifest: Manifest{
		Version: FormatVersion, CreatedAt: manifest.CreatedAt, Git: manifest.Git,
		ChangeMode: manifest.ChangeMode, SourcePolicy: &policy,
		ReviewedFixtures: append([]ReviewedFixture(nil), policy.ReviewedFixtures...),
	}}
	for _, group := range []struct {
		name     string
		entries  []Entry
		target   *[]ArchiveEntry
		metadata *[]Entry
	}{
		{"baseline", manifest.Baseline, &projected.Baseline, &projected.Manifest.Baseline},
		{"workspace", manifest.Workspace, &projected.Workspace, &projected.Manifest.Workspace},
	} {
		for _, entry := range group.entries {
			if !SourcePolicyAllowsPath(policy, entry.Path, group.name == "workspace" && untracked[entry.Path]) {
				continue
			}
			file, exists := files[group.name+"/"+entry.Path]
			if !exists {
				return ScopedSource{}, fmt.Errorf("source projection is missing %s entry %q", group.name, entry.Path)
			}
			*group.metadata = append(*group.metadata, entry)
			*group.target = append(*group.target, ArchiveEntry{Entry: entry, Data: file.Data})
		}
	}
	for _, rel := range manifest.Deleted {
		if SourcePolicyAllowsPath(policy, rel, false) {
			projected.Manifest.Deleted = append(projected.Manifest.Deleted, rel)
		}
	}
	for _, module := range manifest.Submodules {
		if SourcePolicyAllowsPath(policy, module.Path, false) {
			projected.Manifest.Submodules = append(projected.Manifest.Submodules, module)
		}
	}
	var err error
	projected.Manifest.BaselineKey, err = BaselineContentKey(projected.Baseline, projected.Manifest.ContentBinding())
	if err != nil {
		return ScopedSource{}, err
	}
	projected.Manifest.SnapshotDigest, err = WorkspaceContentDigest(projected.Baseline, projected.Workspace, projected.Manifest.Deleted, projected.Manifest.ContentBinding())
	if err != nil {
		return ScopedSource{}, err
	}
	projected.Manifest.ChangedFiles, err = WorkspaceChangedFiles(projected.Baseline, projected.Workspace, projected.Manifest.Deleted)
	if err != nil {
		return ScopedSource{}, err
	}
	if err := ValidateManifest(projected.Manifest); err != nil {
		return ScopedSource{}, err
	}
	return projected, nil
}
