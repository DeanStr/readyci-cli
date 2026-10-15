package artifactbundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteArchiveRejectsExcessTarOverhead(t *testing.T) {
	// Each long basename requires PAX metadata. Content and file count remain
	// within their own limits, while metadata and padding exhaust the stream.
	data := make([]byte, MaxExpandedBytes/MaxEntryCount)
	files := make([]ArchiveFile, MaxEntryCount)
	for i := range files {
		files[i] = ArchiveFile{Path: fmt.Sprintf("%s%05d", strings.Repeat("a", 195), i), Data: data}
	}
	var archive bytes.Buffer
	if _, err := WriteArchive(&archive, files); !errors.Is(err, ErrTooLarge) {
		_, validationErr := Validate(bytes.NewReader(archive.Bytes()))
		t.Fatalf("writer error=%v, consumer error=%v; want producer size rejection", err, validationErr)
	}
}

func TestValidateRejectsUnsafeArchivePaths(t *testing.T) {
	var buf bytes.Buffer
	gzipWriter := gzip.NewWriter(&buf)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: "../secret.txt", Typeflag: tar.TypeReg, Size: 1}); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if _, err := tarWriter.Write([]byte("x")); err != nil {
		t.Fatalf("write body: %v", err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	if _, err := Validate(bytes.NewReader(buf.Bytes())); err == nil {
		t.Fatal("unsafe archive path was accepted")
	}
}

func TestValidateArchivePathRejectsWindowsAbsoluteAndBackslashTraversal(t *testing.T) {
	for _, value := range []string{`dir\file.txt`, `..\secret.txt`, `C:\secret.txt`, `C:/secret.txt`, `\\server\share\secret.txt`, `.git\config`, `./report.txt`, `reports/../report.txt`, `reports//report.txt`} {
		t.Run(value, func(t *testing.T) {
			if _, err := ValidateArchivePath(value); err == nil {
				t.Fatalf("ValidateArchivePath(%q) succeeded, want rejection", value)
			}
		})
	}
}

func TestNormalizePatternRejectsWindowsAbsoluteAndBackslashTraversal(t *testing.T) {
	for _, value := range []string{`..\secret.txt`, `reports/../report.txt`, `reports\..\secret.txt`, `C:\secret.txt`, `C:/secret.txt`, `\\server\share\secret.txt`, `.git\config`} {
		t.Run(value, func(t *testing.T) {
			if _, err := NormalizePattern(value); err == nil {
				t.Fatalf("NormalizePattern(%q) succeeded, want rejection", value)
			}
		})
	}
}

func TestNormalizePatternRejectsControlCharacters(t *testing.T) {
	for _, value := range []string{"reports/\nsecret.txt", "reports/\x7fsecret.txt"} {
		t.Run(value, func(t *testing.T) {
			if _, err := NormalizePattern(value); err == nil {
				t.Fatalf("NormalizePattern(%q) succeeded, want rejection", value)
			}
		})
	}
}

func TestNormalizePatternPreservesFailedWorkspaceEquivalent(t *testing.T) {
	tests := []struct {
		pattern string
		want    string
	}{
		{".readyci/../.readyci/failed-workspace.tar.gz", ".readyci/failed-workspace.tar.gz"},
		{"./.readyci/../.readyci/../.readyci/failed-workspace.tar.gz", ".readyci/failed-workspace.tar.gz"},
	}
	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			got, err := NormalizePattern(tt.pattern)
			if err != nil {
				t.Fatalf("NormalizePattern(%q): %v", tt.pattern, err)
			}
			if got != tt.want {
				t.Fatalf("NormalizePattern(%q) = %q, want %q", tt.pattern, got, tt.want)
			}
			if !IsFailedWorkspaceArtifactPattern(tt.pattern) {
				t.Fatalf("IsFailedWorkspaceArtifactPattern(%q) = false, want true", tt.pattern)
			}
		})
	}
}

func TestValidateArchivePathRejectsControlCharacters(t *testing.T) {
	for _, value := range []string{"reports/\nsecret.txt", "reports/\x7fsecret.txt"} {
		t.Run(value, func(t *testing.T) {
			if _, err := ValidateArchivePath(value); err == nil {
				t.Fatalf("ValidateArchivePath(%q) succeeded, want rejection", value)
			}
		})
	}
}

func TestValidateRejectsNonNormalizedArchivePaths(t *testing.T) {
	for _, name := range []string{"./report.txt", "reports/../report.txt", "reports//report.txt"} {
		t.Run(name, func(t *testing.T) {
			archive := tarGzipArchive(t, func(t *testing.T, writer *tar.Writer) {
				writeTarFile(t, writer, name, "bad")
			})
			if _, err := Validate(bytes.NewReader(archive)); err == nil {
				t.Fatal("Validate succeeded, want non-normalized archive path rejection")
			}
		})
	}
}

func TestValidateRejectsArchivePathControlCharacters(t *testing.T) {
	archive := tarGzipArchive(t, func(t *testing.T, writer *tar.Writer) {
		writeTarFile(t, writer, "reports/\nsecret.txt", "bad")
	})
	if _, err := Validate(bytes.NewReader(archive)); err == nil {
		t.Fatal("Validate succeeded, want control-character path rejection")
	}
}

func TestValidateArchivePathPreservesWhitespace(t *testing.T) {
	got, err := ValidateArchivePath(" report.txt ")
	if err != nil {
		t.Fatalf("ValidateArchivePath with spaces: %v", err)
	}
	if got != " report.txt " {
		t.Fatalf("ValidateArchivePath trimmed path = %q, want spaces preserved", got)
	}
}

func TestValidateRejectsNestedGitControlPaths(t *testing.T) {
	var buf bytes.Buffer
	gzipWriter := gzip.NewWriter(&buf)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: "nested/.git/config", Typeflag: tar.TypeReg}); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	if _, err := Validate(bytes.NewReader(buf.Bytes())); err == nil {
		t.Fatal("nested git control path was accepted")
	}
}

func TestValidateRejectsCaseInsensitiveGitControlPaths(t *testing.T) {
	var buf bytes.Buffer
	gzipWriter := gzip.NewWriter(&buf)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: ".GIT/config", Typeflag: tar.TypeReg}); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	if _, err := Validate(bytes.NewReader(buf.Bytes())); err == nil {
		t.Fatal("case-insensitive git control path was accepted")
	}
}

func TestValidateRejectsInvalidGzipFooter(t *testing.T) {
	var buf bytes.Buffer
	if _, err := WriteArchive(&buf, []ArchiveFile{{Path: "artifact.txt", Data: []byte("artifact")}}); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	if _, err := Validate(bytes.NewReader(corruptGzipFooter(t, buf.Bytes()))); err == nil {
		t.Fatal("validate accepted invalid gzip footer")
	}
}

func TestValidateRejectsTrailingGzipPayload(t *testing.T) {
	archive := archiveWithTrailingGzipPayload(t, strings.Repeat("x", 1024))
	if _, err := Validate(bytes.NewReader(archive)); !errors.Is(err, ErrTrailingData) {
		t.Fatalf("validate trailing payload error = %v, want %v", err, ErrTrailingData)
	}
}

func TestValidateAllowsTarEOFZeroPadding(t *testing.T) {
	archive := archiveWithTrailingGzipBytes(t, bytes.Repeat([]byte{0}, 18*512))
	manifest, err := Validate(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("validate archive with tar EOF padding: %v", err)
	}
	got := paths(manifest.Files)
	want := []string{"tests/fixtures/summary.json"}
	if !equalStrings(got, want) {
		t.Fatalf("validated files = %#v, want %#v", got, want)
	}
}

func TestValidateRejectsOversizedTarEOFZeroPadding(t *testing.T) {
	archive := archiveWithTrailingGzipBytes(t, bytes.Repeat([]byte{0}, maxTarEOFZeroPaddingSize+1))
	if _, err := Validate(bytes.NewReader(archive)); !errors.Is(err, ErrTrailingData) {
		t.Fatalf("validate oversized padding error = %v, want %v", err, ErrTrailingData)
	}
}

func TestValidateExpandedSizeOverflowIsTooLarge(t *testing.T) {
	var buf bytes.Buffer
	gzipWriter := gzip.NewWriter(&buf)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: "first.txt", Typeflag: tar.TypeReg, Size: 1}); err != nil {
		t.Fatalf("write first header: %v", err)
	}
	if _, err := tarWriter.Write([]byte("x")); err != nil {
		t.Fatalf("write first body: %v", err)
	}
	if err := tarWriter.WriteHeader(&tar.Header{Name: "huge.txt", Typeflag: tar.TypeReg, Size: math.MaxInt64}); err != nil {
		t.Fatalf("write huge header: %v", err)
	}
	_ = tarWriter.Close()
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	if _, err := Validate(bytes.NewReader(buf.Bytes())); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("validate error = %v, want %v", err, ErrTooLarge)
	}
}

func TestValidateRejectsMalformedArchiveEntries(t *testing.T) {
	tests := []struct {
		name  string
		build func(t *testing.T) []byte
	}{
		{
			name: "duplicate path",
			build: func(t *testing.T) []byte {
				return tarGzipArchive(t, func(t *testing.T, writer *tar.Writer) {
					writeTarFile(t, writer, "report.txt", "first")
					writeTarFile(t, writer, "report.txt", "second")
				})
			},
		},
		{
			name: "prefix conflict",
			build: func(t *testing.T) []byte {
				return tarGzipArchive(t, func(t *testing.T, writer *tar.Writer) {
					writeTarFile(t, writer, "report.txt", "file")
					writeTarFile(t, writer, "report.txt/details.json", "{}")
				})
			},
		},
		{
			name: "directory entry",
			build: func(t *testing.T) []byte {
				return tarGzipArchive(t, func(t *testing.T, writer *tar.Writer) {
					if err := writer.WriteHeader(&tar.Header{Name: "report-dir", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
						t.Fatalf("write directory header: %v", err)
					}
				})
			},
		},
		{
			name: "symlink entry",
			build: func(t *testing.T) []byte {
				return tarGzipArchive(t, func(t *testing.T, writer *tar.Writer) {
					if err := writer.WriteHeader(&tar.Header{Name: "report.txt", Typeflag: tar.TypeSymlink, Linkname: "outside"}); err != nil {
						t.Fatalf("write symlink header: %v", err)
					}
				})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Validate(bytes.NewReader(tt.build(t))); err == nil {
				t.Fatal("Validate succeeded, want malformed archive error")
			}
		})
	}
}

func TestWriteArchiveRejectsPrefixConflictsBeforeWriting(t *testing.T) {
	var buf bytes.Buffer
	_, err := WriteArchive(&buf, []ArchiveFile{
		{Path: "report.txt", Data: []byte("file")},
		{Path: "report.txt/details.json", Data: []byte("{}")},
	})
	if err == nil || !strings.Contains(err.Error(), "conflicts with nested artifact path") {
		t.Fatalf("WriteArchive conflict error = %v, want prefix conflict", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("WriteArchive wrote %d bytes before rejecting prefix conflict", buf.Len())
	}
}

func TestWriteArchiveRejectsNonNormalizedPathsBeforeWriting(t *testing.T) {
	var buf bytes.Buffer
	_, err := WriteArchive(&buf, []ArchiveFile{{Path: "reports/../report.txt", Data: []byte("bad")}})
	if err == nil || !strings.Contains(err.Error(), "not normalized") {
		t.Fatalf("WriteArchive non-normalized path error = %v, want normalization rejection", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("WriteArchive wrote %d bytes before rejecting non-normalized path", buf.Len())
	}
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

func writeTarFile(t *testing.T, writer *tar.Writer, name, data string) {
	t.Helper()
	if err := writer.WriteHeader(&tar.Header{
		Name: name,
		Mode: 0o644,
		Size: int64(len(data)),
	}); err != nil {
		t.Fatalf("write tar header %s: %v", name, err)
	}
	if _, err := writer.Write([]byte(data)); err != nil {
		t.Fatalf("write tar file %s: %v", name, err)
	}
}

func tarGzipArchive(t *testing.T, build func(t *testing.T, writer *tar.Writer)) []byte {
	t.Helper()
	var buf bytes.Buffer
	gzipWriter := gzip.NewWriter(&buf)
	tarWriter := tar.NewWriter(gzipWriter)
	build(t, tarWriter)
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

func corruptGzipFooter(t *testing.T, data []byte) []byte {
	t.Helper()
	if len(data) == 0 {
		t.Fatal("empty gzip data")
	}
	out := append([]byte(nil), data...)
	out[len(out)-1] ^= 0xff
	return out
}

func archiveWithTrailingGzipPayload(t *testing.T, trailing string) []byte {
	t.Helper()
	return archiveWithTrailingGzipBytes(t, []byte(trailing))
}

func archiveWithTrailingGzipBytes(t *testing.T, trailing []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gzipWriter := gzip.NewWriter(&buf)
	tarWriter := tar.NewWriter(gzipWriter)
	writeTarFile(t, tarWriter, "tests/fixtures/summary.json", "remote fixture")
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if _, err := gzipWriter.Write(trailing); err != nil {
		t.Fatalf("write trailing gzip payload: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

func paths(files []File) []string {
	out := make([]string, 0, len(files))
	for _, file := range files {
		out = append(out, file.Path)
	}
	return out
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
