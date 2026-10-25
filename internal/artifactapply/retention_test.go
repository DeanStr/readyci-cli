package artifactapply

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/artifactbundle"
)

func TestApplyPreservesMaximumPayloadWithRetentionEnvelope(t *testing.T) {
	metadata := retentionApplyMetadata(t, 1)
	archive := tarGzipArchive(t, func(t *testing.T, writer *tar.Writer) {
		writeTarFile(t, writer, artifactbundle.RetentionManifestPath, string(metadata))
		if err := writer.WriteHeader(&tar.Header{Name: "full.txt", Mode: 0600, Size: artifactbundle.MaxExpandedBytes}); err != nil {
			t.Fatal(err)
		}
		if _, err := io.CopyN(writer, artifactZeroReader{}, artifactbundle.MaxExpandedBytes); err != nil {
			t.Fatal(err)
		}
	})
	applied, err := Apply(context.Background(), t.TempDir(), []string{"**"}, bytes.NewReader(archive), ApplyOptions{DryRun: true})
	if err != nil || applied.TotalExpandedBytes != artifactbundle.MaxExpandedBytes || len(applied.Files) != 1 || applied.Retention == nil {
		t.Fatalf("retention metadata displaced maximum payload: %+v err=%v", applied, err)
	}
}

func TestApplyPreservesMaximumEntryCountWithRetentionEnvelope(t *testing.T) {
	for _, metadataFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("metadata-first-%v", metadataFirst), func(t *testing.T) {
			metadata := retentionApplyMetadata(t, artifactbundle.MaxEntryCount)
			archive := tarGzipArchive(t, func(t *testing.T, writer *tar.Writer) {
				if metadataFirst {
					writeTarFile(t, writer, artifactbundle.RetentionManifestPath, string(metadata))
				}
				for i := 0; i < artifactbundle.MaxEntryCount; i++ {
					writeTarFile(t, writer, fmt.Sprintf("files/%05d.txt", i), "")
				}
				if !metadataFirst {
					writeTarFile(t, writer, artifactbundle.RetentionManifestPath, string(metadata))
				}
			})
			applied, err := Apply(context.Background(), t.TempDir(), []string{"**"}, bytes.NewReader(archive), ApplyOptions{DryRun: true})
			if err != nil || len(applied.Files) != artifactbundle.MaxEntryCount || applied.Retention == nil {
				t.Fatalf("retention metadata displaced maximum entry count: applied=%d err=%v", len(applied.Files), err)
			}
		})
	}
}

func TestApplyTreatsRetentionMetadataAsDiagnosticsOnly(t *testing.T) {
	archive := tarGzipArchive(t, func(t *testing.T, writer *tar.Writer) {
		writeTarFile(t, writer, "reports/result.txt", "retained report")
		writeTarFile(t, writer, artifactbundle.RetentionManifestPath, string(retentionApplyMetadata(t, 1)))
	})
	root := t.TempDir()
	writeFile(t, filepath.Join(root, artifactbundle.RetentionManifestPath), "existing local state")
	applied, err := Apply(context.Background(), root, []string{"**"}, bytes.NewReader(archive), ApplyOptions{})
	if err != nil || len(applied.Files) != 1 || applied.Retention == nil || applied.Files[0].Path != "reports/result.txt" {
		t.Fatalf("retention metadata was selected for apply or lost: %+v err=%v", applied, err)
	}
	data, err := os.ReadFile(filepath.Join(root, artifactbundle.RetentionManifestPath))
	if err != nil || string(data) != "existing local state" {
		t.Fatalf("diagnostic metadata overwrote local state: %q err=%v", data, err)
	}
}

func TestApplyMetadataOnlyArchiveDoesNotChangeWorkspace(t *testing.T) {
	archive := tarGzipArchive(t, func(t *testing.T, writer *tar.Writer) {
		writeTarFile(t, writer, artifactbundle.RetentionManifestPath, string(retentionApplyMetadata(t, 0)))
	})
	root := t.TempDir()
	applied, err := Apply(context.Background(), root, []string{"**"}, bytes.NewReader(archive), ApplyOptions{})
	if err != nil || len(applied.Files) != 0 || applied.Retention == nil || applied.TotalExpandedBytes != 0 {
		t.Fatalf("metadata-only collection was applied as source: %+v err=%v", applied, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("metadata-only archive changed workspace: entries=%d err=%v", len(entries), err)
	}
}

func TestApplyRejectsInvalidUnmatchedRetentionBeforeCommit(t *testing.T) {
	for _, metadata := range []string{
		`{"schema_version":"unknown"}`,
		string(retentionApplyMetadata(t, 2)), // Only one customer file is present.
	} {
		archive := tarGzipArchive(t, func(t *testing.T, writer *tar.Writer) {
			writeTarFile(t, writer, "reports/result.txt", "remote")
			writeTarFile(t, writer, artifactbundle.RetentionManifestPath, metadata)
		})
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "reports/result.txt"), "local")
		if _, err := Apply(context.Background(), root, []string{"reports/**"}, bytes.NewReader(archive), ApplyOptions{}); err == nil {
			t.Fatal("invalid unmatched retention metadata accepted")
		}
		data, err := os.ReadFile(filepath.Join(root, "reports/result.txt"))
		if err != nil || string(data) != "local" {
			t.Fatalf("invalid archive changed workspace before validation: %q err=%v", data, err)
		}
	}
}

func TestApplyRetentionEnvelopeDoesNotIncreasePayloadAllowance(t *testing.T) {
	archive := tarGzipArchive(t, func(t *testing.T, writer *tar.Writer) {
		writeTarFile(t, writer, artifactbundle.RetentionManifestPath, string(retentionApplyMetadata(t, 1)))
		if err := writer.WriteHeader(&tar.Header{Name: "too-large.txt", Mode: 0600, Size: artifactbundle.MaxExpandedBytes + 1}); err != nil {
			t.Fatal(err)
		}
		if _, err := io.CopyN(writer, artifactZeroReader{}, artifactbundle.MaxExpandedBytes+1); err != nil {
			t.Fatal(err)
		}
	})
	if _, err := Apply(context.Background(), t.TempDir(), []string{"**"}, bytes.NewReader(archive), ApplyOptions{DryRun: true}); !errors.Is(err, artifactbundle.ErrTooLarge) {
		t.Fatalf("retention envelope enlarged customer payload allowance: %v", err)
	}
}

func TestApplyRetentionEnvelopeDoesNotIncreaseCustomerEntryAllowance(t *testing.T) {
	archive := tarGzipArchive(t, func(t *testing.T, writer *tar.Writer) {
		writeTarFile(t, writer, artifactbundle.RetentionManifestPath, string(retentionApplyMetadata(t, artifactbundle.MaxEntryCount)))
		for i := 0; i <= artifactbundle.MaxEntryCount; i++ {
			writeTarFile(t, writer, fmt.Sprintf("files/%05d.txt", i), "")
		}
	})
	if _, err := Apply(context.Background(), t.TempDir(), []string{"**"}, bytes.NewReader(archive), ApplyOptions{DryRun: true}); !errors.Is(err, artifactbundle.ErrTooLarge) {
		t.Fatalf("retention envelope enlarged customer entry allowance: %v", err)
	}
}

func retentionApplyMetadata(t *testing.T, retained int) []byte {
	t.Helper()
	data, err := json.Marshal(artifactbundle.Retention{SchemaVersion: artifactbundle.RetentionSchemaVersion, MatchedFileCount: retained + 1, RetainedFileCount: retained, OmittedFileCount: 1, FailedWorkspaceOmitted: true,
		OmittedFiles: []artifactbundle.OmittedFile{{Path: ".readyci/failed-workspace.tar.gz", SizeBytes: artifactbundle.MaxExpandedBytes + 1, Reason: "bundle_limit"}}})
	if err != nil {
		t.Fatal(err)
	}
	return data
}
