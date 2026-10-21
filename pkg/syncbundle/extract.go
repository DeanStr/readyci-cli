package syncbundle

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
)

type BundleFile struct {
	Data      []byte
	Linkname  string
	EntryType byte
}

type bundledFile = BundleFile

type BundleReadLimits struct {
	MaxExpandedBytes int64
	MaxManifestBytes int64
	MaxEntries       int
}

type bundleReadLimits = BundleReadLimits

type decompressedSizeLimiter struct {
	limit     int64
	remaining int64
}

func (limiter *decompressedSizeLimiter) reserve(name string, size int64) error {
	if size > limiter.remaining {
		return fmt.Errorf("bundle decompressed contents exceed %d bytes at %s", limiter.limit, name)
	}
	limiter.remaining -= size
	return nil
}

type boundedDecompressedReader struct {
	reader    io.Reader
	limit     int64
	remaining int64
}

func (reader *boundedDecompressedReader) Read(p []byte) (int, error) {
	if reader.remaining <= 0 {
		var probe [1]byte
		n, err := reader.reader.Read(probe[:])
		if n > 0 {
			return 0, fmt.Errorf("bundle decompressed stream exceeds %d bytes", reader.limit)
		}
		return 0, err
	}
	if int64(len(p)) > reader.remaining {
		p = p[:reader.remaining]
	}
	n, err := reader.reader.Read(p)
	reader.remaining -= int64(n)
	return n, err
}

func readBundleFiles(reader io.Reader) (Manifest, map[string]bundledFile, error) {
	return readBundleFilesWithLimits(reader, bundleReadLimits{
		MaxExpandedBytes: MaxExpandedBundleBytes,
		MaxManifestBytes: MaxManifestBytes,
		MaxEntries:       MaxBundleEntries,
	})
}

func ReadBundleContents(reader io.Reader) (Manifest, map[string]BundleFile, error) {
	return readBundleFiles(reader)
}

func ReadBundleContentsWithLimits(reader io.Reader, limits BundleReadLimits) (Manifest, map[string]BundleFile, error) {
	return readBundleFilesWithLimits(reader, limits)
}

func readBundleFilesWithLimits(reader io.Reader, limits bundleReadLimits) (Manifest, map[string]bundledFile, error) {
	limits, err := normalizeBundleReadLimits(limits)
	if err != nil {
		return Manifest{}, nil, err
	}
	gzipReader, err := gzip.NewReader(reader)
	if err != nil {
		return Manifest{}, nil, err
	}
	defer gzipReader.Close()

	boundedReader := &boundedDecompressedReader{
		reader:    gzipReader,
		limit:     limits.MaxExpandedBytes,
		remaining: limits.MaxExpandedBytes,
	}
	tarReader := tar.NewReader(boundedReader)
	files := make(map[string]bundledFile)
	limiter := decompressedSizeLimiter{limit: limits.MaxExpandedBytes, remaining: limits.MaxExpandedBytes}
	var manifest Manifest
	manifestSeen := false
	entriesSeen := 0
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Manifest{}, nil, err
		}
		entriesSeen++
		if entriesSeen > limits.MaxEntries {
			return Manifest{}, nil, &AdmissionError{"entries", int64(entriesSeen), int64(limits.MaxEntries)}
		}
		name := path.Clean(header.Name)
		if header.Name != name {
			return Manifest{}, nil, fmt.Errorf("bundle path %q is not normalized", header.Name)
		}
		if name == "." || strings.HasPrefix(name, "../") || path.IsAbs(name) {
			return Manifest{}, nil, fmt.Errorf("bundle path %q is invalid", header.Name)
		}
		switch {
		case name == "manifest.json":
			if manifestSeen {
				return Manifest{}, nil, fmt.Errorf("bundle contains multiple manifest.json entries")
			}
			manifestSeen = true
			if header.Typeflag != tar.TypeReg {
				return Manifest{}, nil, fmt.Errorf("manifest.json is not a regular file")
			}
			data, err := readEntryData(tarReader, header, limits.MaxManifestBytes, &limiter, name)
			if err != nil {
				return Manifest{}, nil, err
			}
			manifest, err = decodeManifestJSON(data)
			if err != nil {
				return Manifest{}, nil, err
			}
		case strings.HasPrefix(name, "baseline/") || strings.HasPrefix(name, "workspace/"):
			_, rel, _ := strings.Cut(name, "/")
			if isGitControlPath(rel) {
				return Manifest{}, nil, fmt.Errorf("bundle path %q targets git control metadata", name)
			}
			if _, ok := files[name]; ok {
				return Manifest{}, nil, fmt.Errorf("bundle contains duplicate path %s", name)
			}
			if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeSymlink {
				return Manifest{}, nil, fmt.Errorf("bundle path %q has unsupported type", name)
			}
			if header.Typeflag == tar.TypeSymlink {
				files[name] = bundledFile{Linkname: header.Linkname, EntryType: header.Typeflag}
				continue
			}
			if header.Size < 0 {
				return Manifest{}, nil, fmt.Errorf("bundle path %q has invalid size", name)
			}
			data, err := readEntryData(tarReader, header, MaxExpandedBundleBytes, &limiter, name)
			if err != nil {
				return Manifest{}, nil, err
			}
			files[name] = bundledFile{Data: data, EntryType: header.Typeflag}
		default:
			return Manifest{}, nil, fmt.Errorf("unexpected bundle path %q", name)
		}
	}
	if manifest.IsZero() {
		return Manifest{}, nil, ErrMissingManifest
	}
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, nil, err
	}
	if err := validateSnapshotDigestForRead(manifest); err != nil {
		return Manifest{}, nil, err
	}
	if err := validateBundleFiles(manifest, files); err != nil {
		return Manifest{}, nil, err
	}
	if err := validateBaselineKey(manifest, files); err != nil {
		return Manifest{}, nil, err
	}
	var baseline []ArchiveEntry
	if !manifest.BaselineRef {
		baseline, err = archiveEntriesFromBundleFiles("baseline", manifest.Baseline, files)
		if err != nil {
			return Manifest{}, nil, err
		}
	}
	workspace, err := archiveEntriesFromBundleFiles("workspace", manifest.Workspace, files)
	if err != nil {
		return Manifest{}, nil, err
	}
	if err := ValidateReviewedFixtureContents(manifest, baseline, workspace); err != nil {
		return Manifest{}, nil, err
	}
	if !manifest.BaselineRef {
		digest, err := WorkspaceContentDigest(baseline, workspace, manifest.Deleted, manifest.ContentBinding())
		if err != nil {
			return Manifest{}, nil, err
		}
		if manifest.SnapshotDigest != "" && digest != manifest.SnapshotDigest {
			return Manifest{}, nil, fmt.Errorf("snapshot_digest does not match materialized workspace")
		}
		changed, err := WorkspaceChangedFiles(baseline, workspace, manifest.Deleted)
		if err != nil {
			return Manifest{}, nil, err
		}
		if manifest.Version >= FormatVersion && !equalStrings(changed, manifest.ChangedFiles) {
			return Manifest{}, nil, fmt.Errorf("changed_files does not match materialized workspace changes")
		}
	}
	return manifest, files, nil
}

func normalizeBundleReadLimits(limits bundleReadLimits) (bundleReadLimits, error) {
	if limits.MaxExpandedBytes < 0 {
		return bundleReadLimits{}, fmt.Errorf("max expanded bytes must be non-negative")
	}
	if limits.MaxManifestBytes < 0 {
		return bundleReadLimits{}, fmt.Errorf("max manifest bytes must be non-negative")
	}
	if limits.MaxEntries < 0 {
		return bundleReadLimits{}, fmt.Errorf("max entries must be non-negative")
	}
	if limits.MaxExpandedBytes == 0 {
		limits.MaxExpandedBytes = MaxExpandedBundleBytes
	}
	if limits.MaxManifestBytes == 0 {
		limits.MaxManifestBytes = MaxManifestBytes
	}
	if limits.MaxEntries == 0 {
		limits.MaxEntries = MaxBundleEntries
	}
	return limits, nil
}

func validateBaselineKey(manifest Manifest, files map[string]bundledFile) error {
	if manifest.BaselineKey == "" || manifest.BaselineRef {
		return nil
	}
	entries, err := archiveEntriesFromBundleFiles("baseline", manifest.Baseline, files)
	if err != nil {
		return err
	}
	key, err := BaselineContentKey(entries, manifest.ContentBinding())
	if err != nil {
		return err
	}
	if key != manifest.BaselineKey {
		return fmt.Errorf("baseline_key does not match bundle baseline contents")
	}
	return nil
}

func readEntryData(reader io.Reader, header *tar.Header, maxSize int64, limiter *decompressedSizeLimiter, name string) ([]byte, error) {
	if header.Size < 0 {
		return nil, fmt.Errorf("bundle path %q has invalid size", name)
	}
	if header.Size > maxSize {
		return nil, fmt.Errorf("bundle path %q exceeds %d bytes", name, maxSize)
	}
	if err := limiter.reserve(name, header.Size); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(reader, header.Size+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != header.Size {
		return nil, fmt.Errorf("bundle path %q size mismatch", name)
	}
	return data, nil
}

func validateBundleFiles(manifest Manifest, files map[string]bundledFile) error {
	expected := make(map[string]Entry)
	if !manifest.BaselineRef {
		for _, entry := range manifest.Baseline {
			expected["baseline/"+entry.Path] = entry
		}
	}
	for _, entry := range manifest.Workspace {
		expected["workspace/"+entry.Path] = entry
	}
	for key, entry := range expected {
		file, ok := files[key]
		if !ok {
			return fmt.Errorf("bundle is missing %s", key)
		}
		switch entry.Type {
		case entryTypeFile:
			if file.EntryType != tar.TypeReg {
				return fmt.Errorf("%s is not a regular file in bundle", key)
			}
			if int64(len(file.Data)) != entry.Size {
				return fmt.Errorf("%s size mismatch", key)
			}
		case entryTypeSymlink:
			if file.EntryType != tar.TypeSymlink {
				return fmt.Errorf("%s is not a symlink in bundle", key)
			}
			if file.Linkname != entry.Linkname {
				return fmt.Errorf("%s symlink target mismatch", key)
			}
		}
	}
	for key := range files {
		if _, ok := expected[key]; !ok {
			return fmt.Errorf("bundle contains undeclared file %s", key)
		}
	}
	return nil
}

func ManifestPaths(manifest Manifest) []string {
	paths := make([]string, 0, len(manifest.Baseline)+len(manifest.Workspace)+len(manifest.Deleted))
	for _, entry := range manifest.Baseline {
		paths = append(paths, "baseline/"+entry.Path)
	}
	for _, entry := range manifest.Workspace {
		paths = append(paths, "workspace/"+entry.Path)
	}
	for _, deleted := range manifest.Deleted {
		paths = append(paths, "deleted/"+deleted)
	}
	sort.Strings(paths)
	return paths
}
