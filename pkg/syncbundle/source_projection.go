package syncbundle

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"path"
	"slices"
	"sort"
	"strings"
	"time"
)

// ErrManifestNotLeading reports an archive that a single forward pass cannot
// project: its manifest is not the first entry.
var ErrManifestNotLeading = errors.New("bundle manifest.json is not the leading entry")

// ScopedSource is a deterministic projection of a validated, coherent upload.
// Entries share immutable file bytes with the input rather than copying them.
type ScopedSource struct {
	Manifest  Manifest
	Baseline  []ArchiveEntry
	Workspace []ArchiveEntry
}

// ProjectionIdentity is the sealed identity of one scope's projection. It is
// derived once from validated canonical bytes; transfers verify against it
// instead of decoding the canonical upload again.
type ProjectionIdentity struct {
	SnapshotDigest string
	BaselineKey    string
	ChangedFiles   []string
	// ManifestDigest is ManifestDigest of the exact projected manifest.
	ManifestDigest string
}

// ManifestDigest identifies every source semantic a manifest declares: file
// metadata, content identities, Git view, policy and change selection. It
// excludes only the diagnostic creation timestamp, so archives of the same
// source share it regardless of when or how they were compressed.
func ManifestDigest(manifest Manifest) string {
	manifest.CreatedAt = time.Time{}
	// Manifest has only strings, numbers, booleans, slices and a time value,
	// so encoding cannot fail.
	data, _ := json.Marshal(manifest)
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:])
}

// ProjectSource derives a scope's complete projection, including content
// identities, from an upload whose files are in memory.
func ProjectSource(manifest Manifest, files map[string]BundleFile, policy SourcePolicyBinding) (ScopedSource, error) {
	metadata, err := projectManifestEntries(manifest, policy)
	if err != nil {
		return ScopedSource{}, err
	}
	projected := ScopedSource{Manifest: metadata}
	for _, group := range []struct {
		name    string
		entries []Entry
		target  *[]ArchiveEntry
	}{
		{"baseline", metadata.Baseline, &projected.Baseline},
		{"workspace", metadata.Workspace, &projected.Workspace},
	} {
		for _, entry := range group.entries {
			file, exists := files[group.name+"/"+entry.Path]
			if !exists {
				return ScopedSource{}, fmt.Errorf("source projection is missing %s entry %q", group.name, entry.Path)
			}
			*group.target = append(*group.target, ArchiveEntry{Entry: entry, Data: file.Data})
		}
	}
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

// projectManifestEntries derives the file metadata of a scope's projection.
// Content identities need file bytes and are supplied by the caller.
func projectManifestEntries(manifest Manifest, policy SourcePolicyBinding) (Manifest, error) {
	if manifest.BaselineRef || manifest.SourcePolicy == nil || len(manifest.SourcePolicy.Scopes) < 2 || len(policy.Scopes) != 0 {
		return Manifest{}, fmt.Errorf("source projection requires a complete registered multi-scope upload and one member scope")
	}
	key := SourcePolicyScopeKey(policy)
	found := false
	for _, scope := range manifest.SourcePolicy.Scopes {
		found = found || SourcePolicyScopeKey(scope) == key
	}
	if !found {
		return Manifest{}, fmt.Errorf("source projection filter is absent from the registered upload contract")
	}
	untracked := make(map[string]bool, len(manifest.UntrackedFiles))
	for _, rel := range manifest.UntrackedFiles {
		untracked[rel] = true
	}
	projected := Manifest{
		Version: FormatVersion, CreatedAt: manifest.CreatedAt, Git: manifest.Git,
		ChangeMode: manifest.ChangeMode, SourcePolicy: &policy,
		ReviewedFixtures: append([]ReviewedFixture(nil), policy.ReviewedFixtures...),
	}
	for _, entry := range manifest.Baseline {
		if SourcePolicyAllowsPath(policy, entry.Path, false) {
			projected.Baseline = append(projected.Baseline, entry)
		}
	}
	for _, entry := range manifest.Workspace {
		if SourcePolicyAllowsPath(policy, entry.Path, untracked[entry.Path]) {
			projected.Workspace = append(projected.Workspace, entry)
		}
	}
	for _, rel := range manifest.Deleted {
		if SourcePolicyAllowsPath(policy, rel, false) {
			projected.Deleted = append(projected.Deleted, rel)
		}
	}
	for _, module := range manifest.Submodules {
		if SourcePolicyAllowsPath(policy, module.Path, false) {
			projected.Submodules = append(projected.Submodules, module)
		}
	}
	return projected, nil
}

// StreamProjection writes one registered scope's projection of a validated
// multi-scope bundle in a single forward pass over the canonical archive. It
// holds the manifests in memory but never file contents; entries outside the
// scope are skipped. The derived manifest must match identity before any byte
// is written, and every emitted entry must match its declaration. inspect, if
// set, checks the canonical manifest first.
//
// Reviewed fixture bytes are re-hashed as they stream. Other file bytes are
// not: every reader of a bundle validates its contents against the sealed
// snapshot digest before use, and the canonical upload was validated whole.
func StreamProjection(writer io.Writer, canonical io.Reader, policy SourcePolicyBinding, identity ProjectionIdentity, inspect func(Manifest) error) (Manifest, error) {
	gzipReader, err := gzip.NewReader(canonical)
	if err != nil {
		return Manifest{}, err
	}
	defer gzipReader.Close()
	reader := tar.NewReader(&boundedDecompressedReader{reader: gzipReader, limit: MaxExpandedBundleBytes, remaining: MaxExpandedBundleBytes})
	source, err := readLeadingManifest(reader)
	if err != nil {
		return Manifest{}, err
	}
	if inspect != nil {
		if err := inspect(source); err != nil {
			return Manifest{}, err
		}
	}
	projected, err := projectManifestEntries(source, policy)
	if err != nil {
		return Manifest{}, err
	}
	projected.SnapshotDigest = identity.SnapshotDigest
	projected.BaselineKey = identity.BaselineKey
	projected.ChangedFiles = slices.Clone(identity.ChangedFiles)
	if err := validateManifest(projected); err != nil {
		return Manifest{}, err
	}
	if err := ValidateSnapshotDigest(projected.SnapshotDigest); err != nil {
		return Manifest{}, err
	}
	if ManifestDigest(projected) != identity.ManifestDigest {
		return Manifest{}, fmt.Errorf("source projection does not match its sealed manifest digest")
	}
	if _, err := ManifestAdmission(projected); err != nil {
		return Manifest{}, err
	}
	manifestBytes, err := json.Marshal(projected)
	if err != nil {
		return Manifest{}, err
	}
	expected := make(map[string]Entry, len(projected.Baseline)+len(projected.Workspace))
	for _, entry := range projected.Baseline {
		expected["baseline/"+entry.Path] = entry
	}
	for _, entry := range projected.Workspace {
		expected["workspace/"+entry.Path] = entry
	}
	fixtures := make(map[string]string, len(projected.ReviewedFixtures))
	for _, fixture := range projected.ReviewedFixtures {
		fixtures[fixture.Path] = fixture.SHA256
	}

	gzipWriter := gzip.NewWriter(&admissionWriter{writer: writer, resource: "compressed_bytes", limit: MaxCompressedBundleBytes})
	gzipWriter.Name = "bundle.tar"
	gzipWriter.ModTime = time.Unix(0, 0).UTC()
	tarWriter := tar.NewWriter(&admissionWriter{writer: gzipWriter, resource: "expanded_bytes", limit: MaxExpandedBundleBytes})
	if err := writeTarFile(tarWriter, "manifest.json", 0o644, manifestBytes); err != nil {
		return Manifest{}, err
	}
	for entries := 1; ; {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Manifest{}, err
		}
		entries++
		if entries > MaxBundleEntries {
			return Manifest{}, &AdmissionError{"entries", int64(entries), MaxBundleEntries}
		}
		group, _, _ := strings.Cut(header.Name, "/")
		if header.Name != path.Clean(header.Name) || group != "baseline" && group != "workspace" {
			return Manifest{}, fmt.Errorf("unexpected bundle path %q", header.Name)
		}
		entry, included := expected[header.Name]
		if !included {
			continue
		}
		delete(expected, header.Name)
		if err := streamProjectedEntry(tarWriter, reader, header, group, entry, fixtures); err != nil {
			return Manifest{}, err
		}
	}
	if len(expected) > 0 {
		missing := make([]string, 0, len(expected))
		for name := range expected {
			missing = append(missing, name)
		}
		sort.Strings(missing)
		return Manifest{}, fmt.Errorf("canonical source is missing projected entry %s", missing[0])
	}
	if err := tarWriter.Close(); err != nil {
		return Manifest{}, err
	}
	if err := gzipWriter.Close(); err != nil {
		return Manifest{}, err
	}
	return projected, nil
}

func streamProjectedEntry(writer *tar.Writer, reader io.Reader, header *tar.Header, group string, entry Entry, fixtures map[string]string) error {
	name := group + "/" + entry.Path
	switch entry.Type {
	case EntryTypeSymlink:
		if header.Typeflag != tar.TypeSymlink || header.Linkname != entry.Linkname {
			return fmt.Errorf("%s does not match its declared symlink", name)
		}
		return writeTarEntryFrom(writer, group, entry, nil)
	case EntryTypeFile:
		if header.Typeflag != tar.TypeReg || header.Size != entry.Size {
			return fmt.Errorf("%s does not match its declared file", name)
		}
		reviewed, isFixture := fixtures[entry.Path]
		var digest hash.Hash
		if isFixture {
			digest = sha256.New()
			reader = io.TeeReader(reader, digest)
		}
		if err := writeTarEntryFrom(writer, group, entry, reader); err != nil {
			return err
		}
		if isFixture && hex.EncodeToString(digest.Sum(nil)) != reviewed {
			return fmt.Errorf("reviewed fixture %q in %s does not match its reviewed sha256", entry.Path, group)
		}
		return nil
	default:
		return fmt.Errorf("%s has unsupported type %q", name, entry.Type)
	}
}

func readLeadingManifest(reader *tar.Reader) (Manifest, error) {
	header, err := reader.Next()
	if errors.Is(err, io.EOF) {
		return Manifest{}, ErrMissingManifest
	}
	if err != nil {
		return Manifest{}, err
	}
	if header.Name != "manifest.json" {
		return Manifest{}, ErrManifestNotLeading
	}
	if header.Typeflag != tar.TypeReg {
		return Manifest{}, fmt.Errorf("manifest.json is not a regular file")
	}
	data, err := readManifestEntryData(reader, header)
	if err != nil {
		return Manifest{}, err
	}
	manifest, err := decodeManifestJSON(data)
	if err != nil {
		return Manifest{}, err
	}
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	if err := validateSnapshotDigestForRead(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}
