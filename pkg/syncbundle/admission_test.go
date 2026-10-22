package syncbundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func manifestAdmissionFixture(count int, prefix string) (Manifest, []ArchiveEntry) {
	entries := make([]Entry, count)
	archive := make([]ArchiveEntry, count)
	for i := range entries {
		entries[i] = Entry{Path: fmt.Sprintf("%s/component-%05d/tests/fixtures/manifest-admission-empty.txt", prefix, i), Type: EntryTypeFile, Mode: 0o644}
		archive[i] = ArchiveEntry{Entry: entries[i]}
	}
	return Manifest{Version: FormatVersion, Baseline: entries}, archive
}

func sizedManifestAdmissionFixture(t *testing.T, size int) (Manifest, []ArchiveEntry) {
	t.Helper()
	manifest, entries := manifestAdmissionFixture(32000, "src")
	manifest.SnapshotDigest = "sha256:" + strings.Repeat("a", 64)
	manifest.ChangedFiles = []string{}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	padding := size - len(encoded)
	if padding < 0 {
		t.Fatalf("fixture metadata already exceeds %d bytes", size)
	}
	for i := range entries {
		extra := padding / len(entries)
		if i < padding%len(entries) {
			extra++
		}
		prefix := strings.Repeat("p/", extra/2) + strings.Repeat("p", extra%2)
		manifest.Baseline[i].Path = prefix + manifest.Baseline[i].Path
		entries[i].Entry = manifest.Baseline[i]
	}
	manifest.SnapshotDigest, err = WorkspaceContentDigest(entries, nil, nil, manifest.ContentBinding())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err = json.Marshal(manifest)
	if err != nil || len(encoded) != size {
		t.Fatalf("fixture metadata size = %d, want %d: %v", len(encoded), size, err)
	}
	return manifest, entries
}

func TestCompactManifestAdmissionAgreesWithReader(t *testing.T) {
	manifest, entries := manifestAdmissionFixture(32000, "src")
	pretty, err := json.MarshalIndent(manifest, "", "  ")
	const formerManifestLimit = 4 << 20
	if err != nil || len(pretty) <= formerManifestLimit {
		t.Fatalf("fixture must reproduce the former pretty-encoding rejection: bytes=%d err=%v", len(pretty), err)
	}
	usage, err := ManifestAdmission(manifest)
	if err != nil || usage.ManifestBytes >= formerManifestLimit {
		t.Fatalf("compact metadata must fit the former admission limit: %+v err=%v", usage, err)
	}
	var bundle bytes.Buffer
	if err := WriteBundle(&bundle, manifest, entries, nil); err != nil {
		t.Fatalf("write supported source: %v", err)
	}
	if _, err := ValidateWithManifest(bytes.NewReader(bundle.Bytes())); err != nil {
		t.Fatalf("reader rejected source accepted by packaging: %v", err)
	}
}

func TestLargerManifestAdmissionAgreesWithReaders(t *testing.T) {
	manifest, entries := sizedManifestAdmissionFixture(t, 16<<20)
	usage, err := ManifestAdmission(manifest)
	if err != nil || usage.ManifestBytes != 16<<20 {
		t.Fatalf("source must fit exactly at the 16 MiB manifest limit: %+v err=%v", usage, err)
	}
	var bundle bytes.Buffer
	if err := WriteBundle(&bundle, manifest, entries, nil); err != nil {
		t.Fatalf("write larger source: %v", err)
	}
	if _, err := ReadManifest(bytes.NewReader(bundle.Bytes())); err != nil {
		t.Fatalf("manifest reader rejected larger source: %v", err)
	}
	if _, _, err := ReadBundleContents(bytes.NewReader(bundle.Bytes())); err != nil {
		t.Fatalf("full reader rejected larger source: %v", err)
	}
}

func TestManifestReadersEnforce16MiBBoundary(t *testing.T) {
	var empty bytes.Buffer
	if err := WriteBundle(&empty, Manifest{Version: FormatVersion}, nil, nil); err != nil {
		t.Fatal(err)
	}
	manifest, err := ReadManifest(bytes.NewReader(empty.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{16 << 20, (16 << 20) + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			var bundle bytes.Buffer
			compressed := gzip.NewWriter(&bundle)
			archive := tar.NewWriter(compressed)
			if err := archive.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0o644, Size: int64(size)}); err != nil {
				t.Fatal(err)
			}
			// Legal JSON whitespace makes the exact encoded boundary independent
			// of manifest fields or archive entry counts.
			padded := append(bytes.Clone(data), bytes.Repeat([]byte{' '}, size-len(data))...)
			if _, err := archive.Write(padded); err != nil {
				t.Fatal(err)
			}
			if err := archive.Close(); err != nil {
				t.Fatal(err)
			}
			if err := compressed.Close(); err != nil {
				t.Fatal(err)
			}
			for name, read := range map[string]func() error{
				"manifest": func() error {
					_, err := ReadManifest(bytes.NewReader(bundle.Bytes()))
					return err
				},
				"contents": func() error {
					_, _, err := ReadBundleContents(bytes.NewReader(bundle.Bytes()))
					return err
				},
			} {
				t.Run(name, func(t *testing.T) {
					err := read()
					if size == 16<<20 && err != nil {
						t.Fatalf("rejected manifest at 16 MiB: %v", err)
					}
					if size > 16<<20 && (err == nil || !strings.Contains(err.Error(), "16777216")) {
						t.Fatalf("oversized manifest error = %v, want 16 MiB limit", err)
					}
				})
			}
		})
	}
}

func TestOversizedManifestRejectedBeforeWriting(t *testing.T) {
	manifest, entries := sizedManifestAdmissionFixture(t, (16<<20)+1)
	var output bytes.Buffer
	err := WriteBundle(&output, manifest, entries, nil)
	var admission *AdmissionError
	if !errors.As(err, &admission) || admission.Resource != "manifest_bytes" || admission.Limit != MaxManifestBytes {
		t.Fatalf("oversized source error = %v, want manifest admission error", err)
	}
	if output.Len() != 0 {
		t.Fatalf("wrote %d bytes for unsupported source", output.Len())
	}
}

func TestAdmissionWriterCountsPAXMetadata(t *testing.T) {
	var output bytes.Buffer
	bounded := &admissionWriter{writer: &output, resource: "expanded_bytes", limit: 2048}
	archive := tar.NewWriter(bounded)
	err := archive.WriteHeader(&tar.Header{Name: "workspace/" + strings.Repeat("long-path/", 400) + "file", Typeflag: tar.TypeReg, Mode: 0o644, Format: tar.FormatPAX})
	var admission *AdmissionError
	if !errors.As(err, &admission) || admission.Resource != "expanded_bytes" {
		t.Fatalf("PAX metadata escaped expanded stream budget: %v", err)
	}
	if output.Len() > 2048 {
		t.Fatalf("wrote %d bytes beyond stream budget", output.Len())
	}
}

func TestAdmissionWriterLimitsCompressedOutput(t *testing.T) {
	var output bytes.Buffer
	bounded := &admissionWriter{writer: &output, resource: "compressed_bytes", limit: 8}
	if _, err := bounded.Write([]byte("accepted")); err != nil {
		t.Fatal(err)
	}
	_, err := bounded.Write([]byte("overflow"))
	var admission *AdmissionError
	if !errors.As(err, &admission) || admission.Resource != "compressed_bytes" || output.String() != "accepted" {
		t.Fatalf("compressed budget result output=%q error=%v", output.String(), err)
	}
}
