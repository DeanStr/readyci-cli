package syncbundle

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
)

func WriteBaselineBundle(reader io.Reader, writer io.Writer) (Manifest, error) {
	manifest, files, err := readBundleFiles(reader)
	if err != nil {
		return Manifest{}, err
	}
	if manifest.BaselineRef {
		return Manifest{}, fmt.Errorf("baseline bundle cannot reference another baseline")
	}
	if manifest.BaselineKey == "" {
		return Manifest{}, fmt.Errorf("baseline bundle requires baseline_key")
	}
	baselineEntries, err := archiveEntriesFromBundleFiles("baseline", manifest.Baseline, files)
	if err != nil {
		return Manifest{}, err
	}
	baselineBinding := manifest.ContentBinding()
	baselineBinding.ReviewedFixtures = ReviewedFixturesInEntries(manifest.ReviewedFixtures, baselineEntries)
	baselineDigest, err := WorkspaceContentDigest(baselineEntries, nil, nil, baselineBinding)
	if err != nil {
		return Manifest{}, err
	}

	baselineManifest := Manifest{
		Version:          FormatVersion,
		Submodules:       manifest.Submodules,
		SnapshotDigest:   baselineDigest,
		BaselineKey:      manifest.BaselineKey,
		Baseline:         manifest.Baseline,
		ReviewedFixtures: ReviewedFixturesInEntries(manifest.ReviewedFixtures, baselineEntries),
	}
	if err := validateRehydratedBundleBounds(baselineManifest, baselineEntries, nil); err != nil {
		return Manifest{}, err
	}
	if err := WriteBundle(writer, baselineManifest, baselineEntries, nil); err != nil {
		return Manifest{}, err
	}
	return baselineManifest, nil
}

func RehydrateDeltaBundle(baselineReader, deltaReader io.Reader, writer io.Writer) (Manifest, error) {
	baselineManifest, baselineFiles, err := readBundleFiles(baselineReader)
	if err != nil {
		return Manifest{}, err
	}
	if baselineManifest.BaselineRef {
		return Manifest{}, fmt.Errorf("baseline bundle cannot itself reference another baseline")
	}
	deltaManifest, deltaFiles, err := readBundleFiles(deltaReader)
	if err != nil {
		return Manifest{}, err
	}
	if !deltaManifest.BaselineRef {
		return Manifest{}, fmt.Errorf("bundle does not reference an external baseline")
	}
	if baselineManifest.BaselineKey == "" || deltaManifest.BaselineKey == "" || baselineManifest.BaselineKey != deltaManifest.BaselineKey {
		return Manifest{}, fmt.Errorf("bundle baseline key does not match referenced baseline")
	}
	if !reflect.DeepEqual(baselineManifest.Baseline, deltaManifest.Baseline) {
		return Manifest{}, fmt.Errorf("bundle baseline metadata does not match referenced baseline")
	}
	if !reflect.DeepEqual(baselineManifest.Submodules, deltaManifest.Submodules) {
		return Manifest{}, fmt.Errorf("bundle submodule pins do not match referenced baseline")
	}

	baselineEntries, err := archiveEntriesFromBundleFiles("baseline", baselineManifest.Baseline, baselineFiles)
	if err != nil {
		return Manifest{}, err
	}
	workspaceEntries, err := archiveEntriesFromBundleFiles("workspace", deltaManifest.Workspace, deltaFiles)
	if err != nil {
		return Manifest{}, err
	}

	merged := deltaManifest
	merged.Version = FormatVersion
	merged.BaselineRef = false
	merged.Baseline = baselineManifest.Baseline
	digest, err := WorkspaceContentDigest(baselineEntries, workspaceEntries, merged.Deleted, merged.ContentBinding())
	if err != nil {
		return Manifest{}, err
	}
	if merged.SnapshotDigest != "" && digest != merged.SnapshotDigest {
		return Manifest{}, fmt.Errorf("snapshot_digest does not match rehydrated workspace")
	}
	merged.SnapshotDigest = digest
	merged.ChangedFiles, err = WorkspaceChangedFiles(baselineEntries, workspaceEntries, merged.Deleted)
	if err != nil {
		return Manifest{}, err
	}
	if err := validateRehydratedBundleBounds(merged, baselineEntries, workspaceEntries); err != nil {
		return Manifest{}, err
	}
	if err := WriteBundle(writer, merged, baselineEntries, workspaceEntries); err != nil {
		return Manifest{}, err
	}
	return merged, nil
}

func archiveEntriesFromBundleFiles(group string, entries []Entry, files map[string]bundledFile) ([]ArchiveEntry, error) {
	archiveEntries := make([]ArchiveEntry, 0, len(entries))
	for _, entry := range entries {
		key := group + "/" + entry.Path
		file, ok := files[key]
		if !ok {
			return nil, fmt.Errorf("bundle is missing %s", key)
		}
		item := ArchiveEntry{Entry: entry}
		if entry.Type == entryTypeFile {
			item.Data = file.Data
		}
		archiveEntries = append(archiveEntries, item)
	}
	return archiveEntries, nil
}

func validateRehydratedBundleBounds(manifest Manifest, baseline, workspace []ArchiveEntry) error {
	if _, err := ManifestAdmission(manifest); err != nil {
		return err
	}

	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	remaining := int64(MaxExpandedBundleBytes)
	if err := reserveTarEntryBytes(&remaining, int64(len(manifestBytes)), "manifest.json"); err != nil {
		return err
	}
	for _, item := range baseline {
		if err := reserveArchiveTarEntryBytes(&remaining, "baseline/"+item.Entry.Path, item); err != nil {
			return err
		}
	}
	for _, item := range workspace {
		if err := reserveArchiveTarEntryBytes(&remaining, "workspace/"+item.Entry.Path, item); err != nil {
			return err
		}
	}
	if remaining < 1024 {
		return fmt.Errorf("rehydrated bundle decompressed contents exceed %d bytes at end-of-archive", MaxExpandedBundleBytes)
	}
	return nil
}

func reserveArchiveTarEntryBytes(remaining *int64, name string, item ArchiveEntry) error {
	size := int64(0)
	if item.Entry.Type == entryTypeFile {
		size = item.Entry.Size
	}
	return reserveTarEntryBytes(remaining, size, name)
}

func reserveTarEntryBytes(remaining *int64, size int64, name string) error {
	if size < 0 {
		return fmt.Errorf("rehydrated bundle path %q has invalid size", name)
	}
	if size > MaxExpandedBundleBytes {
		return fmt.Errorf("rehydrated bundle path %q exceeds %d bytes", name, MaxExpandedBundleBytes)
	}
	const tarMetadataBudget = int64(1024)
	paddedSize := ((size + 511) / 512) * 512
	required := tarMetadataBudget + paddedSize
	if required > *remaining {
		return fmt.Errorf("rehydrated bundle decompressed contents exceed %d bytes at %s", MaxExpandedBundleBytes, name)
	}
	*remaining -= required
	return nil
}
