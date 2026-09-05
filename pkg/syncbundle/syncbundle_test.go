package syncbundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyBundleWithoutSnapshotDigestIsRejected(t *testing.T) {
	manifest := Manifest{
		Version: 1,
		Workspace: []Entry{{
			Path: "legacy.txt",
			Type: entryTypeFile,
			Mode: 0o644,
			Size: int64(len("legacy\n")),
		}},
	}
	var bundle bytes.Buffer
	gzipWriter := gzip.NewWriter(&bundle)
	tarWriter := tar.NewWriter(gzipWriter)
	writeManifestEntry(t, tarWriter, manifest)
	writeTarFileEntry(t, tarWriter, "workspace/legacy.txt", "legacy\n")
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}

	if _, err := ReadManifest(bytes.NewReader(bundle.Bytes())); err == nil || !strings.Contains(err.Error(), "unsupported bundle format version 1") {
		t.Fatalf("ReadManifest legacy bundle error = %v", err)
	}
	if err := Validate(bytes.NewReader(bundle.Bytes())); err == nil || !strings.Contains(err.Error(), "unsupported bundle format version 1") {
		t.Fatalf("Validate legacy bundle error = %v", err)
	}
}

func TestWriteBundleRejectsLegacyFormat(t *testing.T) {
	var bundle bytes.Buffer
	err := WriteBundle(&bundle, Manifest{Version: 1}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "cannot write bundle format version") {
		t.Fatalf("WriteBundle legacy format error = %v", err)
	}
}

func TestReadBundleFilesRejectsSymlinkMismatchAndUndeclaredFiles(t *testing.T) {
	manifest := Manifest{
		Version: FormatVersion,
		Workspace: []Entry{{
			Path:     "link.txt",
			Type:     entryTypeSymlink,
			Linkname: "expected.txt",
		}},
	}
	var bundle bytes.Buffer
	writeTestBundleWithSymlinks(t, &bundle, manifest, nil, map[string]string{
		"workspace/link.txt": "actual.txt",
	})
	if _, _, err := readBundleFiles(bytes.NewReader(bundle.Bytes())); err == nil || !strings.Contains(err.Error(), "symlink target mismatch") {
		t.Fatalf("read symlink mismatch error = %v, want target mismatch", err)
	}

	bundle.Reset()
	writeTestBundle(t, &bundle, Manifest{Version: FormatVersion}, map[string]string{
		"workspace/extra.txt": "extra",
	})
	if _, _, err := readBundleFiles(bytes.NewReader(bundle.Bytes())); err == nil || !strings.Contains(err.Error(), "undeclared file") {
		t.Fatalf("read undeclared file error = %v, want undeclared file rejection", err)
	}
}

func TestDeltaBundleRejectsOversizedRehydratedBundle(t *testing.T) {
	fileSize := int64(MaxExpandedBundleBytes / 2)
	manifest := Manifest{
		Version: FormatVersion,
		Baseline: []Entry{{
			Path: "base.bin",
			Type: entryTypeFile,
			Mode: 0o644,
			Size: fileSize,
		}},
		Workspace: []Entry{{
			Path: "workspace.bin",
			Type: entryTypeFile,
			Mode: 0o644,
			Size: fileSize,
		}},
	}
	err := validateRehydratedBundleBounds(manifest, []ArchiveEntry{{Entry: manifest.Baseline[0]}}, []ArchiveEntry{{Entry: manifest.Workspace[0]}})
	if err == nil || !strings.Contains(err.Error(), "rehydrated bundle decompressed contents exceed") {
		t.Fatalf("validateRehydratedBundleBounds error = %v, want expanded-size rejection", err)
	}
}

func TestBaselineContentKeyUsesSourceWhenWritingBundle(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "base.txt")
	if err := os.WriteFile(sourcePath, []byte("base from source\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	entry := Entry{
		Path: "base.txt",
		Type: entryTypeFile,
		Mode: 0o644,
		Size: int64(len("base from source\n")),
	}
	sourceKey, err := BaselineContentKey([]ArchiveEntry{{Entry: entry, Source: sourcePath}})
	if err != nil {
		t.Fatalf("source baseline key: %v", err)
	}
	dataKey, err := BaselineContentKey([]ArchiveEntry{{Entry: entry, Data: []byte("base from source\n")}})
	if err != nil {
		t.Fatalf("data baseline key: %v", err)
	}
	if sourceKey != dataKey {
		t.Fatalf("source baseline key = %q, data key = %q", sourceKey, dataKey)
	}

	manifest := Manifest{
		Version:     FormatVersion,
		BaselineKey: sourceKey,
		Baseline:    []Entry{entry},
	}
	var bundle bytes.Buffer
	if err := WriteBundle(&bundle, manifest, []ArchiveEntry{{Entry: entry, Source: sourcePath}}, nil); err != nil {
		t.Fatalf("write source-backed bundle: %v", err)
	}
	if err := Validate(bytes.NewReader(bundle.Bytes())); err != nil {
		t.Fatalf("validate source-backed bundle: %v", err)
	}
}

func TestWriteBundleRejectsMismatchedSourceSize(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "base.txt")
	if err := os.WriteFile(sourcePath, []byte("changed\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	entry := Entry{
		Path: "base.txt",
		Type: entryTypeFile,
		Mode: 0o644,
		Size: int64(len("base\n")),
	}
	var bundle bytes.Buffer
	err := WriteBundle(&bundle, Manifest{Version: FormatVersion, Baseline: []Entry{entry}}, []ArchiveEntry{{
		Entry:  entry,
		Source: sourcePath,
	}}, nil)
	if err == nil || !strings.Contains(err.Error(), "does not match manifest size") {
		t.Fatalf("WriteBundle mismatched source error = %v, want source size mismatch", err)
	}
}

func TestWriteBundleRejectsSymlinkSourceForFileEntry(t *testing.T) {
	root := t.TempDir()
	targetPath := filepath.Join(root, "target.txt")
	if err := os.WriteFile(targetPath, []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	sourcePath := filepath.Join(root, "link.txt")
	if err := os.Symlink(targetPath, sourcePath); err != nil {
		t.Fatalf("symlink source: %v", err)
	}
	entry := Entry{
		Path: "base.txt",
		Type: entryTypeFile,
		Mode: 0o644,
		Size: int64(len("base\n")),
	}
	var bundle bytes.Buffer
	err := WriteBundle(&bundle, Manifest{Version: FormatVersion, Baseline: []Entry{entry}}, []ArchiveEntry{{
		Entry:  entry,
		Source: sourcePath,
	}}, nil)
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("WriteBundle symlink source error = %v, want regular-file rejection", err)
	}
}

func TestWriteBundleRejectsInvalidInputsBeforeWriting(t *testing.T) {
	validEntry := Entry{
		Path: "ok.txt",
		Type: entryTypeFile,
		Mode: 0o644,
		Size: int64(len("ok")),
	}
	tests := []struct {
		name      string
		manifest  Manifest
		baseline  []ArchiveEntry
		workspace []ArchiveEntry
		want      string
	}{
		{
			name:     "invalid manifest path",
			manifest: Manifest{Version: FormatVersion, Workspace: []Entry{{Path: "../escape.txt", Type: entryTypeFile, Mode: 0o644, Size: 1}}},
			want:     "escapes workspace",
		},
		{
			name:      "missing workspace entry",
			manifest:  Manifest{Version: FormatVersion, Workspace: []Entry{validEntry}},
			workspace: nil,
			want:      "missing entry",
		},
		{
			name:      "undeclared workspace entry",
			manifest:  Manifest{Version: FormatVersion},
			workspace: []ArchiveEntry{{Entry: validEntry, Data: []byte("ok")}},
			want:      "undeclared entry",
		},
		{
			name:      "archive metadata mismatch",
			manifest:  Manifest{Version: FormatVersion, Workspace: []Entry{validEntry}},
			workspace: []ArchiveEntry{{Entry: Entry{Path: validEntry.Path, Type: entryTypeFile, Mode: 0o755, Size: validEntry.Size}, Data: []byte("ok")}},
			want:      "does not match manifest metadata",
		},
		{
			name:     "baseline key mismatch",
			manifest: Manifest{Version: FormatVersion, BaselineKey: strings.Repeat("0", 64), Baseline: []Entry{validEntry}},
			baseline: []ArchiveEntry{{Entry: validEntry, Data: []byte("ok")}},
			want:     "baseline_key does not match",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var bundle bytes.Buffer
			err := WriteBundle(&bundle, tt.manifest, tt.baseline, tt.workspace)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("WriteBundle error = %v, want %q", err, tt.want)
			}
			if bundle.Len() != 0 {
				t.Fatalf("WriteBundle wrote %d bytes before rejecting invalid input", bundle.Len())
			}
		})
	}
}

func TestWriteBundleHashesBaselineKeyInManifestOrder(t *testing.T) {
	first := Entry{
		Path: "a.txt",
		Type: entryTypeFile,
		Mode: 0o644,
		Size: int64(len("first")),
	}
	second := Entry{
		Path: "b.txt",
		Type: entryTypeFile,
		Mode: 0o644,
		Size: int64(len("second")),
	}
	manifestOrder := []ArchiveEntry{
		{Entry: first, Data: []byte("first")},
		{Entry: second, Data: []byte("second")},
	}
	baselineKey, err := BaselineContentKey(manifestOrder)
	if err != nil {
		t.Fatalf("baseline key: %v", err)
	}
	manifest := Manifest{
		Version:     FormatVersion,
		BaselineKey: baselineKey,
		Baseline:    []Entry{first, second},
	}
	var bundle bytes.Buffer
	if err := WriteBundle(&bundle, manifest, []ArchiveEntry{manifestOrder[1], manifestOrder[0]}, nil); err != nil {
		t.Fatalf("WriteBundle with reversed baseline archive order: %v", err)
	}
	if err := Validate(bytes.NewReader(bundle.Bytes())); err != nil {
		t.Fatalf("Validate bundle with reversed baseline archive order: %v", err)
	}
}

func TestValidateRejectsTraversalManifest(t *testing.T) {
	manifest := Manifest{
		Version: FormatVersion,
		Baseline: []Entry{{
			Path: "../escape",
			Type: entryTypeFile,
			Mode: 0o644,
			Size: 1,
		}},
	}
	if err := validateManifest(manifest); err == nil {
		t.Fatal("validateManifest accepted escaping path")
	}
}

func TestValidateRejectsDeletedPathOutsideBaseline(t *testing.T) {
	manifest := Manifest{
		Version: FormatVersion,
		Deleted: []string{
			"not-in-baseline.txt",
		},
	}
	if err := validateManifest(manifest); err == nil {
		t.Fatal("validateManifest accepted deleted path outside baseline")
	}
}

func TestValidateRejectsWorkspaceNestedUnderUndeletedBaselineFile(t *testing.T) {
	manifest := Manifest{
		Version: FormatVersion,
		Baseline: []Entry{{
			Path: "file-to-dir",
			Type: entryTypeFile,
			Mode: 0o644,
			Size: 1,
		}},
		Workspace: []Entry{{
			Path: "file-to-dir/nested.txt",
			Type: entryTypeFile,
			Mode: 0o644,
			Size: 1,
		}},
	}
	if err := validateManifest(manifest); err == nil {
		t.Fatal("validateManifest accepted workspace nested under undeleted baseline file")
	}
	manifest.Deleted = []string{"file-to-dir"}
	if err := validateManifest(manifest); err != nil {
		t.Fatalf("validateManifest rejected file-to-dir replacement with deleted ancestor: %v", err)
	}
}

func TestValidateRejectsWorkspaceAncestorOfUndeletedBaselineFile(t *testing.T) {
	manifest := Manifest{
		Version: FormatVersion,
		Baseline: []Entry{{
			Path: "dir/file.txt",
			Type: entryTypeFile,
			Mode: 0o644,
			Size: 1,
		}},
		Workspace: []Entry{{
			Path: "dir",
			Type: entryTypeFile,
			Mode: 0o644,
			Size: 1,
		}},
	}
	if err := validateManifest(manifest); err == nil {
		t.Fatal("validateManifest accepted workspace file replacing undeleted baseline directory contents")
	}
	manifest.Deleted = []string{"dir/file.txt"}
	if err := validateManifest(manifest); err != nil {
		t.Fatalf("validateManifest rejected dir-to-file replacement with deleted child: %v", err)
	}
}

func TestValidateRejectsDeletedWorkspacePathConflict(t *testing.T) {
	manifest := Manifest{
		Version: FormatVersion,
		Baseline: []Entry{{
			Path: "modified.txt",
			Type: entryTypeFile,
			Mode: 0o644,
			Size: 1,
		}},
		Workspace: []Entry{{
			Path: "modified.txt",
			Type: entryTypeFile,
			Mode: 0o644,
			Size: 1,
		}},
		Deleted: []string{"modified.txt"},
	}
	if err := validateManifest(manifest); err == nil {
		t.Fatal("validateManifest accepted path listed as both deleted and workspace")
	}
}

func TestValidateRejectsGitControlManifestPaths(t *testing.T) {
	for _, path := range []string{".git/hooks/pre-commit", "nested/.git/config", ".GIT/config"} {
		manifest := Manifest{
			Version: FormatVersion,
			Baseline: []Entry{{
				Path: path,
				Type: entryTypeFile,
				Mode: 0o755,
				Size: 1,
			}},
		}
		if err := validateManifest(manifest); err == nil {
			t.Fatalf("validateManifest accepted git control path %q", path)
		}
	}
}

func TestValidateRejectsInvalidChangedFiles(t *testing.T) {
	for _, path := range []string{"../escape", ".git/config"} {
		manifest := Manifest{
			Version:      FormatVersion,
			ChangeMode:   ChangeModeWorking,
			ChangedFiles: []string{path},
		}
		if err := validateManifest(manifest); err == nil {
			t.Fatalf("validateManifest accepted changed path %q", path)
		}
	}
}

func TestValidateRejectsGitControlBundlePath(t *testing.T) {
	manifest := Manifest{Version: FormatVersion}
	var bundle bytes.Buffer
	writeTestBundle(t, &bundle, manifest, map[string]string{
		"workspace/.git/hooks/pre-commit": "hook",
	})
	if err := Validate(bytes.NewReader(bundle.Bytes())); err == nil {
		t.Fatal("Validate accepted git control bundle path")
	}
}

func TestValidateRejectsPrefixConflicts(t *testing.T) {
	manifest := Manifest{
		Version: FormatVersion,
		Workspace: []Entry{
			{Path: "dir", Type: entryTypeFile, Mode: 0o644},
			{Path: "dir/nested.txt", Type: entryTypeFile, Mode: 0o644},
		},
	}
	if err := validateManifest(manifest); err == nil {
		t.Fatal("validateManifest accepted prefix-conflicting entries")
	}
}

func TestValidateRejectsInvalidMode(t *testing.T) {
	manifest := Manifest{
		Version: FormatVersion,
		Workspace: []Entry{{
			Path: "mode.txt",
			Type: entryTypeFile,
			Mode: 0o4755,
			Size: 1,
		}},
	}
	if err := validateManifest(manifest); err == nil {
		t.Fatal("validateManifest accepted invalid mode")
	}
}

func TestValidateRejectsFileEntryWithLinkname(t *testing.T) {
	manifest := Manifest{
		Version: FormatVersion,
		Workspace: []Entry{{
			Path:     "file.txt",
			Type:     entryTypeFile,
			Mode:     0o644,
			Size:     1,
			Linkname: "target.txt",
		}},
	}
	if err := validateManifest(manifest); err == nil {
		t.Fatal("validateManifest accepted file entry with linkname")
	}
}

func TestValidateRejectsSymlinkEntryWithSize(t *testing.T) {
	manifest := Manifest{
		Version: FormatVersion,
		Workspace: []Entry{{
			Path:     "link.txt",
			Type:     entryTypeSymlink,
			Mode:     0o777,
			Size:     1,
			Linkname: "target.txt",
		}},
	}
	if err := validateManifest(manifest); err == nil {
		t.Fatal("validateManifest accepted symlink entry with size")
	}
}

func TestValidateRejectsEmptySymlinkTarget(t *testing.T) {
	manifest := Manifest{
		Version: FormatVersion,
		Workspace: []Entry{{
			Path: "link.txt",
			Type: entryTypeSymlink,
			Mode: 0o777,
		}},
	}
	if err := validateManifest(manifest); err == nil {
		t.Fatal("validateManifest accepted empty symlink target")
	}
}

func TestValidateRejectsUnsafeSymlinkTargets(t *testing.T) {
	for _, linkname := range []string{"/etc/passwd", "../outside.txt", "dir/../../outside.txt", "target.txt\n", "\ttarget.txt"} {
		t.Run(linkname, func(t *testing.T) {
			manifest := Manifest{
				Version: FormatVersion,
				Workspace: []Entry{{
					Path:     "link.txt",
					Type:     entryTypeSymlink,
					Mode:     0o777,
					Linkname: linkname,
				}},
			}
			if err := validateManifest(manifest); err == nil {
				t.Fatalf("validateManifest accepted symlink target %q", linkname)
			}
		})
	}
}

func TestValidateRejectsWindowsAbsoluteAndBackslashPaths(t *testing.T) {
	for _, value := range []string{`dir\file.txt`, `..\escape.txt`, `C:\escape.txt`, `C:/escape.txt`, `\\server\share\escape.txt`, "dir\nfile.txt"} {
		t.Run(value, func(t *testing.T) {
			if _, err := ValidateRelativePath(value); err == nil {
				t.Fatalf("ValidateRelativePath(%q) succeeded, want rejection", value)
			}
		})
	}
	for _, value := range []string{`dir\target.txt`, `..\target.txt`, `C:\target.txt`, `\\server\share\target.txt`, "dir\ntarget.txt"} {
		t.Run("symlink_"+value, func(t *testing.T) {
			if err := ValidateSymlinkTarget(value); err == nil {
				t.Fatalf("ValidateSymlinkTarget(%q) succeeded, want rejection", value)
			}
		})
	}
}

func TestValidateRejectsUndeclaredBundleFile(t *testing.T) {
	manifest := Manifest{Version: FormatVersion}
	var bundle bytes.Buffer
	writeTestBundle(t, &bundle, manifest, map[string]string{
		"workspace/extra.txt": "extra",
	})
	if err := Validate(bytes.NewReader(bundle.Bytes())); err == nil {
		t.Fatal("Validate accepted undeclared bundle file")
	}
}

func TestValidateRejectsDuplicateBundlePath(t *testing.T) {
	manifest := Manifest{
		Version: FormatVersion,
		Workspace: []Entry{{
			Path: "dup.txt",
			Type: entryTypeFile,
			Mode: 0o644,
			Size: 3,
		}},
	}
	var bundle bytes.Buffer
	gzipWriter := gzip.NewWriter(&bundle)
	tarWriter := tar.NewWriter(gzipWriter)
	writeManifestEntry(t, tarWriter, manifest)
	writeTarFileEntry(t, tarWriter, "workspace/dup.txt", "one")
	writeTarFileEntry(t, tarWriter, "workspace/dup.txt", "two")
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	if err := Validate(bytes.NewReader(bundle.Bytes())); err == nil {
		t.Fatal("Validate accepted duplicate bundle path")
	}
}

func TestValidateRejectsNonNormalizedBundlePath(t *testing.T) {
	manifest := Manifest{
		Version: FormatVersion,
		Workspace: []Entry{{
			Path: "bar.txt",
			Type: entryTypeFile,
			Mode: 0o644,
			Size: 3,
		}},
	}
	var bundle bytes.Buffer
	writeTestBundle(t, &bundle, manifest, map[string]string{
		"workspace/foo/../bar.txt": "bar",
	})
	if err := Validate(bytes.NewReader(bundle.Bytes())); err == nil {
		t.Fatal("Validate accepted non-normalized bundle path")
	}
}

func TestReadManifestRejectsNonNormalizedBundlePathBeforeManifest(t *testing.T) {
	manifest := Manifest{Version: FormatVersion}
	var bundle bytes.Buffer
	gzipWriter := gzip.NewWriter(&bundle)
	tarWriter := tar.NewWriter(gzipWriter)
	writeTarFileEntry(t, tarWriter, "workspace/foo/../bar.txt", "bar")
	writeManifestEntry(t, tarWriter, manifest)
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	if _, err := ReadManifest(bytes.NewReader(bundle.Bytes())); err == nil || !strings.Contains(err.Error(), "not normalized") {
		t.Fatalf("ReadManifest non-normalized path error = %v, want normalization rejection", err)
	}
}

func TestReadManifestRejectsTrailingJSON(t *testing.T) {
	var bundle bytes.Buffer
	gzipWriter := gzip.NewWriter(&bundle)
	tarWriter := tar.NewWriter(gzipWriter)
	data := `{"version":1}{}`
	writeTarFileEntry(t, tarWriter, "manifest.json", data)
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	if _, err := ReadManifest(bytes.NewReader(bundle.Bytes())); err == nil || !strings.Contains(err.Error(), "trailing JSON") {
		t.Fatalf("ReadManifest trailing JSON error = %v, want trailing JSON rejection", err)
	}
}

func TestValidateRejectsTrailingJSONManifest(t *testing.T) {
	var bundle bytes.Buffer
	gzipWriter := gzip.NewWriter(&bundle)
	tarWriter := tar.NewWriter(gzipWriter)
	data := `{"version":1}{}`
	writeTarFileEntry(t, tarWriter, "manifest.json", data)
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	if err := Validate(bytes.NewReader(bundle.Bytes())); err == nil || !strings.Contains(err.Error(), "trailing JSON") {
		t.Fatalf("Validate trailing JSON error = %v, want trailing JSON rejection", err)
	}
}

func TestValidateRejectsOversizedDecompressedEntry(t *testing.T) {
	manifest := Manifest{
		Version: FormatVersion,
		Workspace: []Entry{{
			Path: "huge.txt",
			Type: entryTypeFile,
			Mode: 0o644,
			Size: MaxExpandedBundleBytes + 1,
		}},
	}
	var bundle bytes.Buffer
	gzipWriter := gzip.NewWriter(&bundle)
	tarWriter := tar.NewWriter(gzipWriter)
	writeManifestEntry(t, tarWriter, manifest)
	if err := tarWriter.WriteHeader(&tar.Header{
		Name:     "workspace/huge.txt",
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     MaxExpandedBundleBytes + 1,
	}); err != nil {
		t.Fatalf("write huge header: %v", err)
	}
	if err := tarWriter.Close(); err == nil {
		t.Fatal("close tar succeeded for intentionally undersized huge entry")
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}

	if err := Validate(bytes.NewReader(bundle.Bytes())); err == nil {
		t.Fatal("Validate accepted oversized decompressed entry")
	}
}

func TestValidateRejectsTooManyBundleEntriesWithLowLimit(t *testing.T) {
	manifest := Manifest{Version: FormatVersion}
	var bundle bytes.Buffer
	writeTestBundle(t, &bundle, manifest, map[string]string{
		"workspace/extra.txt": "extra",
	})

	_, _, err := readBundleFilesWithLimits(bytes.NewReader(bundle.Bytes()), bundleReadLimits{
		MaxExpandedBytes: MaxExpandedBundleBytes,
		MaxManifestBytes: MaxManifestBytes,
		MaxEntries:       1,
	})
	if err == nil {
		t.Fatal("readBundleFilesWithLimits accepted too many entries")
	}
}

func TestReadBundleContentsWithLimitsUsesDefaultsForZeroValues(t *testing.T) {
	manifest := Manifest{
		Version: FormatVersion,
		Workspace: []Entry{{
			Path: "file.txt",
			Type: entryTypeFile,
			Mode: 0o644,
			Size: int64(len("ok")),
		}},
	}
	var bundle bytes.Buffer
	writeTestBundle(t, &bundle, manifest, map[string]string{
		"workspace/file.txt": "ok",
	})
	if _, _, err := ReadBundleContentsWithLimits(bytes.NewReader(bundle.Bytes()), BundleReadLimits{}); err != nil {
		t.Fatalf("ReadBundleContentsWithLimits with zero defaults: %v", err)
	}
	if _, _, err := ReadBundleContentsWithLimits(bytes.NewReader(bundle.Bytes()), BundleReadLimits{MaxEntries: -1}); err == nil {
		t.Fatal("ReadBundleContentsWithLimits accepted negative max entries")
	}
}

func TestReadBundleContentsRejectsChangedFilesThatOmitWorkspaceChanges(t *testing.T) {
	workspace := []ArchiveEntry{{
		Entry: Entry{Path: "modified.go", Type: entryTypeFile, Mode: 0o644, Size: int64(len("changed\n"))},
		Data:  []byte("changed\n"),
	}}
	digest, err := WorkspaceContentDigest(nil, workspace, nil)
	if err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{
		Version:        FormatVersion,
		SnapshotDigest: digest,
		ChangedFiles:   []string{},
		Workspace:      []Entry{workspace[0].Entry},
	}
	var bundle bytes.Buffer
	writeTestBundle(t, &bundle, manifest, map[string]string{"workspace/modified.go": "changed\n"})
	if _, _, err := ReadBundleContents(bytes.NewReader(bundle.Bytes())); err == nil || !strings.Contains(err.Error(), "changed_files") {
		t.Fatalf("ReadBundleContents error = %v, want changed_files mismatch", err)
	}
}

func TestValidateRejectsDecompressedMetadataWithLowLimit(t *testing.T) {
	manifest := Manifest{Version: FormatVersion}
	var bundle bytes.Buffer
	writeTestBundle(t, &bundle, manifest, nil)

	_, _, err := readBundleFilesWithLimits(bytes.NewReader(bundle.Bytes()), bundleReadLimits{
		MaxExpandedBytes: 64,
		MaxManifestBytes: MaxManifestBytes,
		MaxEntries:       MaxBundleEntries,
	})
	if err == nil {
		t.Fatal("readBundleFilesWithLimits accepted metadata beyond expanded limit")
	}
}

func TestBoundedDecompressedReaderAllowsExactLimitAtEOF(t *testing.T) {
	reader := &boundedDecompressedReader{
		reader:    strings.NewReader("abc"),
		limit:     3,
		remaining: 3,
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll exact limit: %v", err)
	}
	if string(data) != "abc" {
		t.Fatalf("ReadAll = %q, want abc", data)
	}
}

func initRepo(t *testing.T, repo string) {
	t.Helper()
	runGitTest(t, repo, "init")
	runGitTest(t, repo, "config", "user.name", "Preflight Test")
	runGitTest(t, repo, "config", "user.email", "preflight-test@example.invalid")
}

func runGitTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func writeFile(t *testing.T, root, rel, value string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func assertFile(t *testing.T, root, rel, want string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	if string(data) != want {
		t.Fatalf("%s = %q, want %q", rel, data, want)
	}
}

func assertNotExists(t *testing.T, root, rel string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
		t.Fatalf("%s exists, want absent", rel)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat %s: %v", rel, err)
	}
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func writeTestBundle(t *testing.T, writer *bytes.Buffer, manifest Manifest, files map[string]string) {
	t.Helper()
	manifest = testManifestWithDigest(manifest, files)
	gzipWriter := gzip.NewWriter(writer)
	tarWriter := tar.NewWriter(gzipWriter)
	writeManifestEntry(t, tarWriter, manifest)
	for name, data := range files {
		writeTarFileEntry(t, tarWriter, name, data)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
}

func writeTestBundleWithSymlinks(t *testing.T, writer *bytes.Buffer, manifest Manifest, files, symlinks map[string]string) {
	t.Helper()
	manifest = testManifestWithDigest(manifest, files)
	gzipWriter := gzip.NewWriter(writer)
	tarWriter := tar.NewWriter(gzipWriter)
	writeManifestEntry(t, tarWriter, manifest)
	for name, data := range files {
		writeTarFileEntry(t, tarWriter, name, data)
	}
	for name, target := range symlinks {
		if err := tarWriter.WriteHeader(&tar.Header{
			Name:     name,
			Typeflag: tar.TypeSymlink,
			Linkname: target,
			Mode:     0o777,
		}); err != nil {
			t.Fatalf("write %s symlink header: %v", name, err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
}

func testManifestWithDigest(manifest Manifest, files map[string]string) Manifest {
	if manifest.SnapshotDigest != "" {
		return manifest
	}
	entries := func(prefix string, declared []Entry) []ArchiveEntry {
		out := make([]ArchiveEntry, 0, len(declared))
		for _, entry := range declared {
			item := ArchiveEntry{Entry: entry}
			if entry.Type == entryTypeFile {
				item.Data = []byte(files[prefix+"/"+entry.Path])
			}
			out = append(out, item)
		}
		return out
	}
	digest, err := WorkspaceContentDigest(entries("baseline", manifest.Baseline), entries("workspace", manifest.Workspace), manifest.Deleted)
	if err != nil {
		manifest.SnapshotDigest = "sha256:" + strings.Repeat("0", 64)
		return manifest
	}
	manifest.SnapshotDigest = digest
	if manifest.ChangedFiles == nil {
		manifest.ChangedFiles, _ = WorkspaceChangedFiles(entries("baseline", manifest.Baseline), entries("workspace", manifest.Workspace), manifest.Deleted)
	}
	return manifest
}

func writeManifestEntry(t *testing.T, writer *tar.Writer, manifest Manifest) {
	t.Helper()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	writeTarFileEntry(t, writer, "manifest.json", string(data))
}

func writeTarFileEntry(t *testing.T, writer *tar.Writer, name, data string) {
	t.Helper()
	if err := writer.WriteHeader(&tar.Header{
		Name:     name,
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     int64(len(data)),
	}); err != nil {
		t.Fatalf("write %s header: %v", name, err)
	}
	if _, err := writer.Write([]byte(data)); err != nil {
		t.Fatalf("write %s data: %v", name, err)
	}
}
