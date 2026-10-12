package syncbundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

type ArchiveEntry struct {
	Entry  Entry
	Source string
	Data   []byte
}

func BaselineContentKey(entries []ArchiveEntry) (string, error) {
	if len(entries) == 0 {
		return "", nil
	}
	return contentDigest(entries, "baseline")
}

// WorkspaceContentDigest returns a deterministic digest for the workspace that
// results from applying workspace entries and deletions to baseline entries.
// Paths, entry types, modes, symlink targets, and file bytes are all bound into
// the digest. Archive layout and compression are intentionally excluded.
func WorkspaceContentDigest(baseline, workspace []ArchiveEntry, deleted []string) (string, error) {
	materialized := materializedWorkspaceEntries(baseline, workspace, deleted)
	paths := make([]string, 0, len(materialized))
	for path := range materialized {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	ordered := make([]ArchiveEntry, 0, len(paths))
	for _, path := range paths {
		ordered = append(ordered, materialized[path])
	}
	digest, err := contentDigest(ordered, "workspace")
	if err != nil {
		return "", err
	}
	return "sha256:" + digest, nil
}

// WorkspaceChangedFiles derives the changed-file set from the baseline and
// fully materialized workspace. It does not trust the manifest's changed_files
// declaration.
func WorkspaceChangedFiles(baseline, workspace []ArchiveEntry, deleted []string) ([]string, error) {
	before := make(map[string]ArchiveEntry, len(baseline))
	for _, item := range baseline {
		before[item.Entry.Path] = item
	}
	after := materializedWorkspaceEntries(baseline, workspace, deleted)
	paths := make(map[string]struct{}, len(before)+len(after))
	for path := range before {
		paths[path] = struct{}{}
	}
	for path := range after {
		paths[path] = struct{}{}
	}
	changed := make([]string, 0, len(paths))
	for path := range paths {
		left, leftOK := before[path]
		right, rightOK := after[path]
		if leftOK && rightOK {
			equal, err := archiveEntriesEqual(left, right)
			if err != nil {
				return nil, err
			}
			if equal {
				continue
			}
		}
		changed = append(changed, path)
	}
	sort.Strings(changed)
	return changed, nil
}

// ChangedFilesDigest returns the canonical digest used to bind a verification
// session's coverage declaration to a validated bundle manifest.
func ChangedFilesDigest(paths []string) (string, error) {
	normalized := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		rel, err := ValidateRelativePath(path)
		if err != nil {
			return "", err
		}
		if IsGitControlPath(rel) {
			return "", fmt.Errorf("changed path %q targets git control metadata", path)
		}
		if _, ok := seen[rel]; ok {
			continue
		}
		seen[rel] = struct{}{}
		normalized = append(normalized, rel)
	}
	sort.Strings(normalized)
	hash := sha256.New()
	for _, path := range normalized {
		_, _ = hash.Write([]byte(path))
		_, _ = hash.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func materializedWorkspaceEntries(baseline, workspace []ArchiveEntry, deleted []string) map[string]ArchiveEntry {
	materialized := make(map[string]ArchiveEntry, len(baseline)+len(workspace))
	for _, item := range baseline {
		materialized[item.Entry.Path] = item
	}
	for _, path := range deleted {
		delete(materialized, path)
		prefix := path + "/"
		for candidate := range materialized {
			if strings.HasPrefix(candidate, prefix) {
				delete(materialized, candidate)
			}
		}
	}
	for _, item := range workspace {
		materialized[item.Entry.Path] = item
	}
	return materialized
}

func archiveEntriesEqual(left, right ArchiveEntry) (bool, error) {
	if left.Entry.Path != right.Entry.Path ||
		left.Entry.Type != right.Entry.Type ||
		left.Entry.Mode != right.Entry.Mode ||
		left.Entry.Size != right.Entry.Size ||
		left.Entry.Linkname != right.Entry.Linkname {
		return false, nil
	}
	if left.Entry.Type == entryTypeFile && left.Source == "" && right.Source == "" {
		// Validated bundle entries already hold their bytes in memory. Exact
		// equality does not need two more digest passes over unchanged files;
		// the complete workspace digest is still computed independently.
		for _, item := range []ArchiveEntry{left, right} {
			if int64(len(item.Data)) != item.Entry.Size {
				return false, fmt.Errorf("%s data size %d does not match manifest size %d", item.Entry.Path, len(item.Data), item.Entry.Size)
			}
		}
		return bytes.Equal(left.Data, right.Data), nil
	}
	leftDigest, err := contentDigest([]ArchiveEntry{left}, "workspace")
	if err != nil {
		return false, err
	}
	rightDigest, err := contentDigest([]ArchiveEntry{right}, "workspace")
	if err != nil {
		return false, err
	}
	return leftDigest == rightDigest, nil
}

func contentDigest(entries []ArchiveEntry, entrySet string) (string, error) {
	hash := sha256.New()
	for _, item := range entries {
		entry := item.Entry
		_, _ = fmt.Fprintf(hash, "%s\x00%s\x00%o\x00%d\x00", entry.Path, entry.Type, entry.Mode, entry.Size)
		switch entry.Type {
		case entryTypeSymlink:
			_, _ = hash.Write([]byte(entry.Linkname))
		case entryTypeFile:
			if item.Source != "" {
				file, err := openArchiveSource(item)
				if err != nil {
					return "", err
				}
				if _, err := io.CopyN(hash, file, entry.Size); err != nil {
					_ = file.Close()
					return "", err
				}
				if err := file.Close(); err != nil {
					return "", err
				}
			} else {
				if int64(len(item.Data)) != entry.Size {
					return "", fmt.Errorf("%s data size %d does not match manifest size %d", entry.Path, len(item.Data), entry.Size)
				}
				_, _ = hash.Write(item.Data)
			}
		default:
			return "", fmt.Errorf("unsupported %s entry type %q", entrySet, entry.Type)
		}
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func WriteBundle(writer io.Writer, manifest Manifest, baseline, workspace []ArchiveEntry) error {
	if manifest.SnapshotDigest == "" && !manifest.BaselineRef {
		digest, err := WorkspaceContentDigest(baseline, workspace, manifest.Deleted)
		if err != nil {
			return err
		}
		manifest.SnapshotDigest = digest
	}
	if manifest.ChangedFiles == nil && !manifest.BaselineRef {
		changed, err := WorkspaceChangedFiles(baseline, workspace, manifest.Deleted)
		if err != nil {
			return err
		}
		manifest.ChangedFiles = changed
	}
	if err := validateBundleWriteInputs(manifest, baseline, workspace); err != nil {
		return err
	}

	gzipWriter := gzip.NewWriter(writer)
	gzipWriter.Name = "bundle.tar"
	gzipWriter.ModTime = time.Unix(0, 0).UTC()
	tarWriter := tar.NewWriter(gzipWriter)

	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := writeTarFile(tarWriter, "manifest.json", 0o644, manifestBytes); err != nil {
		return err
	}
	for _, item := range baseline {
		if err := writeTarEntry(tarWriter, "baseline", item); err != nil {
			return err
		}
	}
	for _, item := range workspace {
		if err := writeTarEntry(tarWriter, "workspace", item); err != nil {
			return err
		}
	}
	if err := tarWriter.Close(); err != nil {
		return err
	}
	return gzipWriter.Close()
}

func validateBundleWriteInputs(manifest Manifest, baseline, workspace []ArchiveEntry) error {
	if manifest.Version != FormatVersion {
		return fmt.Errorf("cannot write bundle format version %d; current version is %d", manifest.Version, FormatVersion)
	}
	if err := validateManifest(manifest); err != nil {
		return err
	}
	if err := ValidateSnapshotDigest(manifest.SnapshotDigest); err != nil {
		return err
	}
	if manifest.BaselineRef && len(baseline) > 0 {
		return fmt.Errorf("baseline_ref bundle must not include baseline archive entries")
	}
	if err := validateArchiveEntriesForManifest("baseline", manifest.Baseline, baseline, manifest.BaselineRef); err != nil {
		return err
	}
	if err := validateArchiveEntriesForManifest("workspace", manifest.Workspace, workspace, false); err != nil {
		return err
	}
	if manifest.BaselineKey != "" && !manifest.BaselineRef {
		orderedBaseline, err := archiveEntriesInManifestOrder("baseline", manifest.Baseline, baseline)
		if err != nil {
			return err
		}
		key, err := BaselineContentKey(orderedBaseline)
		if err != nil {
			return err
		}
		if key != manifest.BaselineKey {
			return fmt.Errorf("baseline_key does not match bundle baseline contents")
		}
	}
	if !manifest.BaselineRef {
		digest, err := WorkspaceContentDigest(baseline, workspace, manifest.Deleted)
		if err != nil {
			return err
		}
		if digest != manifest.SnapshotDigest {
			return fmt.Errorf("snapshot_digest does not match bundle workspace contents")
		}
		changed, err := WorkspaceChangedFiles(baseline, workspace, manifest.Deleted)
		if err != nil {
			return err
		}
		if !equalStrings(changed, manifest.ChangedFiles) {
			return fmt.Errorf("changed_files does not match bundle workspace changes")
		}
	}
	return nil
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func archiveEntriesInManifestOrder(group string, manifestEntries []Entry, archiveEntries []ArchiveEntry) ([]ArchiveEntry, error) {
	byPath := make(map[string]ArchiveEntry, len(archiveEntries))
	for _, item := range archiveEntries {
		if _, ok := byPath[item.Entry.Path]; ok {
			return nil, fmt.Errorf("%s archive contains duplicate entry %q", group, item.Entry.Path)
		}
		byPath[item.Entry.Path] = item
	}
	ordered := make([]ArchiveEntry, 0, len(manifestEntries))
	for _, entry := range manifestEntries {
		item, ok := byPath[entry.Path]
		if !ok {
			return nil, fmt.Errorf("%s archive is missing entry %q", group, entry.Path)
		}
		ordered = append(ordered, item)
	}
	return ordered, nil
}

func validateArchiveEntriesForManifest(group string, manifestEntries []Entry, archiveEntries []ArchiveEntry, metadataOnly bool) error {
	expected := make(map[string]Entry, len(manifestEntries))
	for _, entry := range manifestEntries {
		expected[entry.Path] = entry
	}
	seen := make(map[string]struct{}, len(archiveEntries))
	for _, item := range archiveEntries {
		entry := item.Entry
		rel, err := validateRelativePath(entry.Path)
		if err != nil {
			return err
		}
		if entry.Path != rel {
			return fmt.Errorf("%s archive entry path %q is not normalized", group, entry.Path)
		}
		if isGitControlPath(rel) {
			return fmt.Errorf("%s archive entry path %q targets git control metadata", group, entry.Path)
		}
		manifestEntry, ok := expected[entry.Path]
		if !ok || metadataOnly {
			return fmt.Errorf("%s archive contains undeclared entry %q", group, entry.Path)
		}
		if entry != manifestEntry {
			return fmt.Errorf("%s archive entry %q does not match manifest metadata", group, entry.Path)
		}
		if _, ok := seen[entry.Path]; ok {
			return fmt.Errorf("%s archive contains duplicate entry %q", group, entry.Path)
		}
		seen[entry.Path] = struct{}{}
		if err := validateArchiveEntryPayload(item); err != nil {
			return err
		}
	}
	if !metadataOnly && len(seen) != len(expected) {
		for _, entry := range manifestEntries {
			if _, ok := seen[entry.Path]; !ok {
				return fmt.Errorf("%s archive is missing entry %q", group, entry.Path)
			}
		}
	}
	return nil
}

func validateArchiveEntryPayload(item ArchiveEntry) error {
	switch item.Entry.Type {
	case entryTypeSymlink:
		return nil
	case entryTypeFile:
		if item.Source != "" {
			file, err := openArchiveSource(item)
			if err != nil {
				return err
			}
			return file.Close()
		}
		if int64(len(item.Data)) != item.Entry.Size {
			return fmt.Errorf("%s data size %d does not match manifest size %d", item.Entry.Path, len(item.Data), item.Entry.Size)
		}
		return nil
	default:
		return fmt.Errorf("%s has unsupported type %q", item.Entry.Path, item.Entry.Type)
	}
}

func writeTarEntry(writer *tar.Writer, prefix string, item ArchiveEntry) error {
	name := prefix + "/" + item.Entry.Path
	if item.Entry.Type == entryTypeSymlink {
		return writer.WriteHeader(&tar.Header{
			Name:     name,
			Typeflag: tar.TypeSymlink,
			Mode:     item.Entry.Mode,
			Linkname: item.Entry.Linkname,
			ModTime:  time.Unix(0, 0).UTC(),
		})
	}
	var reader io.Reader
	if item.Source != "" {
		file, err := openArchiveSource(item)
		if err != nil {
			return err
		}
		defer file.Close()
		reader = file
	} else {
		if int64(len(item.Data)) != item.Entry.Size {
			return fmt.Errorf("%s data size %d does not match manifest size %d", item.Entry.Path, len(item.Data), item.Entry.Size)
		}
		reader = bytes.NewReader(item.Data)
	}
	if err := writer.WriteHeader(&tar.Header{
		Name:     name,
		Typeflag: tar.TypeReg,
		Mode:     item.Entry.Mode,
		Size:     item.Entry.Size,
		ModTime:  time.Unix(0, 0).UTC(),
	}); err != nil {
		return err
	}
	_, err := io.CopyN(writer, reader, item.Entry.Size)
	return err
}

func openArchiveSource(item ArchiveEntry) (*os.File, error) {
	info, err := os.Lstat(item.Source)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s source %q is not a regular file", item.Entry.Path, item.Source)
	}
	if info.Size() != item.Entry.Size {
		return nil, fmt.Errorf("%s source size %d does not match manifest size %d", item.Entry.Path, info.Size(), item.Entry.Size)
	}
	file, err := os.Open(item.Source)
	if err != nil {
		return nil, err
	}
	openedInfo, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !openedInfo.Mode().IsRegular() {
		_ = file.Close()
		return nil, fmt.Errorf("%s source %q is not a regular file", item.Entry.Path, item.Source)
	}
	if !os.SameFile(info, openedInfo) {
		_ = file.Close()
		return nil, fmt.Errorf("%s source %q changed while archiving", item.Entry.Path, item.Source)
	}
	if openedInfo.Size() != item.Entry.Size {
		_ = file.Close()
		return nil, fmt.Errorf("%s source size %d does not match manifest size %d", item.Entry.Path, openedInfo.Size(), item.Entry.Size)
	}
	return file, nil
}

func writeTarFile(writer *tar.Writer, name string, mode int64, data []byte) error {
	if err := writer.WriteHeader(&tar.Header{
		Name:     name,
		Typeflag: tar.TypeReg,
		Mode:     mode,
		Size:     int64(len(data)),
		ModTime:  time.Unix(0, 0).UTC(),
	}); err != nil {
		return err
	}
	_, err := writer.Write(data)
	return err
}
