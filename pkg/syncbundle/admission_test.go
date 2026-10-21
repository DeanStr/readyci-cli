package syncbundle

import (
	"archive/tar"
	"bytes"
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

func TestCompactManifestAdmissionAgreesWithReader(t *testing.T) {
	manifest, entries := manifestAdmissionFixture(32000, "src")
	pretty, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil || len(pretty) <= MaxManifestBytes {
		t.Fatalf("fixture must reproduce the former pretty-encoding rejection: bytes=%d err=%v", len(pretty), err)
	}
	usage, err := ManifestAdmission(manifest)
	if err != nil || usage.ManifestBytes >= MaxManifestBytes {
		t.Fatalf("compact metadata must fit the unchanged admission limit: %+v err=%v", usage, err)
	}
	var bundle bytes.Buffer
	if err := WriteBundle(&bundle, manifest, entries, nil); err != nil {
		t.Fatalf("write supported source: %v", err)
	}
	if _, err := ValidateWithManifest(bytes.NewReader(bundle.Bytes())); err != nil {
		t.Fatalf("reader rejected source accepted by packaging: %v", err)
	}
}

func TestOversizedManifestRejectedBeforeWriting(t *testing.T) {
	manifest, entries := manifestAdmissionFixture(32000, strings.Repeat("long", 16))
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
