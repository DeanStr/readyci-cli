package syncbundlebuild

import (
	"io"
	"sort"
	"strconv"

	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func writePlannedBundle(writer io.Writer, planned plannedBundle, omitBaseline bool) (syncbundle.Manifest, error) {
	manifest := planned.manifest
	manifest.BaselineRef = omitBaseline && manifest.BaselineKey != ""
	baselineFiles := planned.baseline
	if manifest.BaselineRef {
		baselineFiles = nil
	}
	if err := syncbundle.WriteBundle(writer, manifest, plannedArchiveEntries(baselineFiles), plannedArchiveEntries(planned.workspace)); err != nil {
		return syncbundle.Manifest{}, err
	}
	return manifest, nil
}

func baselineContentKey(entries []plannedEntry, binding syncbundle.ContentBinding) (string, error) {
	return syncbundle.BaselineContentKey(plannedArchiveEntries(entries), binding)
}

func plannedArchiveEntries(entries []plannedEntry) []syncbundle.ArchiveEntry {
	out := make([]syncbundle.ArchiveEntry, 0, len(entries))
	for _, item := range entries {
		out = append(out, syncbundle.ArchiveEntry{
			Entry:  item.entry,
			Source: item.source,
			Data:   item.data,
		})
	}
	return out
}

func gitModeToFileMode(mode string) int64 {
	parsed, err := strconv.ParseInt(mode, 8, 64)
	if err != nil {
		return 0o644
	}
	if parsed&0o111 != 0 {
		return 0o755
	}
	return 0o644
}

func sortPlanned(entries []plannedEntry) {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].entry.Path < entries[j].entry.Path
	})
}

func plannedSourceViews(planned plannedBundle, manifest syncbundle.Manifest, seal bool) (map[string]syncbundle.Manifest, error) {
	if !seal || manifest.SourcePolicy == nil || len(manifest.SourcePolicy.Scopes) == 0 {
		return nil, nil
	}
	files := make(map[string]syncbundle.BundleFile, len(planned.baseline)+len(planned.workspace))
	for _, group := range []struct {
		name    string
		entries []plannedEntry
	}{{"baseline", planned.baseline}, {"workspace", planned.workspace}} {
		for _, entry := range group.entries {
			files[group.name+"/"+entry.entry.Path] = syncbundle.BundleFile{Data: entry.data}
		}
	}
	views := make(map[string]syncbundle.Manifest, len(manifest.SourcePolicy.Scopes))
	for _, policy := range manifest.SourcePolicy.Scopes {
		view, err := syncbundle.ProjectSource(manifest, files, policy)
		if err != nil {
			return nil, err
		}
		views[syncbundle.SourcePolicyScopeKey(policy)] = view.Manifest
	}
	return views, nil
}
