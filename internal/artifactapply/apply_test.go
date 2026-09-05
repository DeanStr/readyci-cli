package artifactapply

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	. "github.com/DeanStr/readyci-cli/pkg/artifactbundle"
)

func TestApplyFiltersAndWritesArchiveFiles(t *testing.T) {
	source := t.TempDir()
	writeFile(t, filepath.Join(source, "tests", "fixtures", "summary.json"), "remote fixture")
	writeFile(t, filepath.Join(source, "docs", "calibration-artifacts", "report.md"), "remote report")

	var buf bytes.Buffer
	buildArchiveForApplyTest(t, source, []string{"tests/fixtures/**", "docs/calibration-artifacts/**"}, &buf)

	target := t.TempDir()
	writeFile(t, filepath.Join(target, "tests", "fixtures", "summary.json"), "local fixture")
	manifest, err := Apply(context.Background(), target, []string{"tests/fixtures/**"}, bytes.NewReader(buf.Bytes()), ApplyOptions{})
	if err != nil {
		t.Fatalf("apply archive: %v", err)
	}
	got := paths(manifest.Files)
	want := []string{"tests/fixtures/summary.json"}
	if !equalStrings(got, want) {
		t.Fatalf("applied files = %#v, want %#v", got, want)
	}
	data, err := os.ReadFile(filepath.Join(target, "tests", "fixtures", "summary.json"))
	if err != nil {
		t.Fatalf("read applied fixture: %v", err)
	}
	if string(data) != "remote fixture" {
		t.Fatalf("applied fixture = %q, want remote fixture", data)
	}
	if _, err := os.Stat(filepath.Join(target, "docs", "calibration-artifacts", "report.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unmatched report stat err = %v, want not exists", err)
	}
}

func TestApplyDryRunDoesNotWriteFiles(t *testing.T) {
	source := t.TempDir()
	writeFile(t, filepath.Join(source, "tests", "fixtures", "summary.json"), "remote fixture")

	var buf bytes.Buffer
	buildArchiveForApplyTest(t, source, []string{"tests/fixtures/**"}, &buf)

	target := t.TempDir()
	manifest, err := Apply(context.Background(), target, []string{"tests/fixtures/**"}, bytes.NewReader(buf.Bytes()), ApplyOptions{DryRun: true})
	if err != nil {
		t.Fatalf("dry-run apply archive: %v", err)
	}
	got := paths(manifest.Files)
	want := []string{"tests/fixtures/summary.json"}
	if !equalStrings(got, want) {
		t.Fatalf("dry-run files = %#v, want %#v", got, want)
	}
	if _, err := os.Stat(filepath.Join(target, "tests", "fixtures", "summary.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry-run fixture stat err = %v, want not exists", err)
	}
}

func TestApplyReplacesExistingRegularFileAfterValidation(t *testing.T) {
	source := t.TempDir()
	writeFile(t, filepath.Join(source, "dist", "report.txt"), "remote report")

	var buf bytes.Buffer
	buildArchiveForApplyTest(t, source, []string{"dist/**"}, &buf)

	target := t.TempDir()
	targetPath := filepath.Join(target, "dist", "report.txt")
	writeFile(t, targetPath, "local report")
	manifest, err := Apply(context.Background(), target, []string{"dist/**"}, bytes.NewReader(buf.Bytes()), ApplyOptions{})
	if err != nil {
		t.Fatalf("apply archive: %v", err)
	}
	if got, want := paths(manifest.Files), []string{"dist/report.txt"}; !equalStrings(got, want) {
		t.Fatalf("applied files = %#v, want %#v", got, want)
	}
	data, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read replaced report: %v", err)
	}
	if string(data) != "remote report" {
		t.Fatalf("replaced report = %q, want remote report", data)
	}
}

func TestApplyDoesNotCommitBeforeArchiveValidationCompletes(t *testing.T) {
	var buf bytes.Buffer
	gzipWriter := gzip.NewWriter(&buf)
	tarWriter := tar.NewWriter(gzipWriter)
	writeTarFile(t, tarWriter, "tests/fixtures/summary.json", "remote fixture")
	writeTarFile(t, tarWriter, "../escape.txt", "bad")
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}

	target := t.TempDir()
	fixturePath := filepath.Join(target, "tests", "fixtures", "summary.json")
	_, err := Apply(context.Background(), target, []string{"tests/fixtures/**"}, bytes.NewReader(buf.Bytes()), ApplyOptions{})
	if err == nil || !strings.Contains(err.Error(), "invalid artifact path") {
		t.Fatalf("apply invalid archive error = %v, want invalid artifact path", err)
	}
	if _, err := os.Stat(fixturePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fixture after failed apply stat err = %v, want not exists", err)
	}
	if _, err := os.Stat(filepath.Join(target, "tests")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target directory after failed apply stat err = %v, want not exists", err)
	}
}

func TestApplyRejectsInvalidGzipFooterBeforeCommit(t *testing.T) {
	source := t.TempDir()
	writeFile(t, filepath.Join(source, "tests", "fixtures", "summary.json"), "remote fixture")

	var buf bytes.Buffer
	buildArchiveForApplyTest(t, source, []string{"tests/fixtures/**"}, &buf)
	archive := corruptGzipFooter(t, buf.Bytes())

	target := t.TempDir()
	_, err := Apply(context.Background(), target, []string{"tests/fixtures/**"}, bytes.NewReader(archive), ApplyOptions{})
	if err == nil {
		t.Fatal("apply accepted invalid gzip footer")
	}
	if _, err := os.Stat(filepath.Join(target, "tests", "fixtures", "summary.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fixture after invalid gzip apply stat err = %v, want not exists", err)
	}
	if _, err := os.Stat(filepath.Join(target, "tests")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target directory after invalid gzip apply stat err = %v, want not exists", err)
	}
}

func TestApplyRejectsTrailingGzipPayloadBeforeCommit(t *testing.T) {
	archive := archiveWithTrailingGzipPayload(t, strings.Repeat("x", 1024))

	target := t.TempDir()
	_, err := Apply(context.Background(), target, []string{"tests/fixtures/**"}, bytes.NewReader(archive), ApplyOptions{})
	if !errors.Is(err, ErrTrailingData) {
		t.Fatalf("apply trailing payload error = %v, want %v", err, ErrTrailingData)
	}
	if _, err := os.Stat(filepath.Join(target, "tests", "fixtures", "summary.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fixture after trailing payload apply stat err = %v, want not exists", err)
	}
	if _, err := os.Stat(filepath.Join(target, "tests")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target directory after trailing payload apply stat err = %v, want not exists", err)
	}
}

func TestApplyAllowsTarEOFZeroPadding(t *testing.T) {
	archive := archiveWithTrailingGzipBytes(t, bytes.Repeat([]byte{0}, 18*512))

	target := t.TempDir()
	manifest, err := Apply(context.Background(), target, []string{"tests/fixtures/**"}, bytes.NewReader(archive), ApplyOptions{})
	if err != nil {
		t.Fatalf("apply archive with tar EOF padding: %v", err)
	}
	got := paths(manifest.Files)
	want := []string{"tests/fixtures/summary.json"}
	if !equalStrings(got, want) {
		t.Fatalf("applied files = %#v, want %#v", got, want)
	}
	data, err := os.ReadFile(filepath.Join(target, "tests", "fixtures", "summary.json"))
	if err != nil {
		t.Fatalf("read applied fixture: %v", err)
	}
	if string(data) != "remote fixture" {
		t.Fatalf("applied fixture = %q, want remote fixture", data)
	}
}

func TestApplyRejectsCaseInsensitiveGitControlPathsBeforeCommit(t *testing.T) {
	var buf bytes.Buffer
	gzipWriter := gzip.NewWriter(&buf)
	tarWriter := tar.NewWriter(gzipWriter)
	writeTarFile(t, tarWriter, "tests/fixtures/summary.json", "remote fixture")
	writeTarFile(t, tarWriter, ".GIT/config", "malicious git config")
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}

	target := t.TempDir()
	_, err := Apply(context.Background(), target, []string{"**"}, bytes.NewReader(buf.Bytes()), ApplyOptions{})
	if err == nil || !strings.Contains(err.Error(), "invalid artifact path") {
		t.Fatalf("apply case-insensitive git path error = %v, want invalid artifact path", err)
	}
	if _, err := os.Stat(filepath.Join(target, "tests", "fixtures", "summary.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fixture after git-control apply stat err = %v, want not exists", err)
	}
	if _, err := os.Stat(filepath.Join(target, ".GIT", "config")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(".GIT config after failed apply stat err = %v, want not exists", err)
	}
}

func TestApplyRejectsSymlinkParents(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions are not portable on windows")
	}
	source := t.TempDir()
	writeFile(t, filepath.Join(source, "tests", "fixtures", "summary.json"), "remote fixture")

	var buf bytes.Buffer
	buildArchiveForApplyTest(t, source, []string{"tests/fixtures/**"}, &buf)

	target := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(target, "tests"), 0o755); err != nil {
		t.Fatalf("mkdir target tests: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(target, "tests", "fixtures")); err != nil {
		t.Fatalf("symlink fixtures: %v", err)
	}
	_, err := Apply(context.Background(), target, []string{"tests/fixtures/**"}, bytes.NewReader(buf.Bytes()), ApplyOptions{})
	if err == nil || !strings.Contains(err.Error(), "refusing to write through symlink parent") {
		t.Fatalf("apply symlink parent error = %v, want symlink parent error", err)
	}
}

func TestApplyRejectsExistingTargetSymlinkBeforeCommit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions are not portable on windows")
	}
	source := t.TempDir()
	writeFile(t, filepath.Join(source, "tests", "fixtures", "summary.json"), "remote fixture")
	writeFile(t, filepath.Join(source, "tests", "fixtures", "other.json"), "other fixture")

	var buf bytes.Buffer
	buildArchiveForApplyTest(t, source, []string{"tests/fixtures/**"}, &buf)

	target := t.TempDir()
	outside := t.TempDir()
	outsidePath := filepath.Join(outside, "summary.json")
	writeFile(t, outsidePath, "outside sentinel")
	if err := os.MkdirAll(filepath.Join(target, "tests", "fixtures"), 0o755); err != nil {
		t.Fatalf("mkdir target fixtures: %v", err)
	}
	if err := os.Symlink(outsidePath, filepath.Join(target, "tests", "fixtures", "summary.json")); err != nil {
		t.Fatalf("symlink summary target: %v", err)
	}

	_, err := Apply(context.Background(), target, []string{"tests/fixtures/**"}, bytes.NewReader(buf.Bytes()), ApplyOptions{})
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite non-regular file") {
		t.Fatalf("apply target symlink error = %v, want non-regular overwrite rejection", err)
	}
	data, err := os.ReadFile(outsidePath)
	if err != nil {
		t.Fatalf("read outside sentinel: %v", err)
	}
	if string(data) != "outside sentinel" {
		t.Fatalf("outside sentinel = %q, want unchanged", data)
	}
	if _, err := os.Stat(filepath.Join(target, "tests", "fixtures", "other.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("other fixture after failed apply stat err = %v, want not exists", err)
	}
}

func TestApplyRejectsDuplicateArchivePathBeforeCommit(t *testing.T) {
	archive := tarGzipArchive(t, func(t *testing.T, writer *tar.Writer) {
		writeTarFile(t, writer, "tests/fixtures/summary.json", "first")
		writeTarFile(t, writer, "tests/fixtures/summary.json", "second")
	})

	target := t.TempDir()
	_, err := Apply(context.Background(), target, []string{"tests/fixtures/**"}, bytes.NewReader(archive), ApplyOptions{})
	if err == nil || !strings.Contains(err.Error(), "duplicate artifact path") {
		t.Fatalf("apply duplicate path error = %v, want duplicate artifact path", err)
	}
	if _, err := os.Stat(filepath.Join(target, "tests", "fixtures", "summary.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fixture after duplicate apply stat err = %v, want not exists", err)
	}
}

func TestApplyRejectsUnmatchedPrefixConflictBeforeCommit(t *testing.T) {
	archive := tarGzipArchive(t, func(t *testing.T, writer *tar.Writer) {
		writeTarFile(t, writer, "tests/fixtures/summary.json", "remote fixture")
		writeTarFile(t, writer, "docs", "file")
		writeTarFile(t, writer, "docs/report.txt", "report")
	})

	target := t.TempDir()
	_, err := Apply(context.Background(), target, []string{"tests/fixtures/**"}, bytes.NewReader(archive), ApplyOptions{})
	if err == nil || !strings.Contains(err.Error(), "conflicts with nested artifact path") {
		t.Fatalf("apply prefix conflict error = %v, want prefix conflict", err)
	}
	if _, err := os.Stat(filepath.Join(target, "tests", "fixtures", "summary.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fixture after conflict apply stat err = %v, want not exists", err)
	}
}

func TestConflictsWithStagingRoot(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "repo")
	staging := filepath.Join(workspace, ".readyci-apply-123")
	if !conflictsWithStagingRoot(workspace, staging, ".readyci-apply-123/report.txt") {
		t.Fatal("staging-root child path was not reported as a conflict")
	}
	if conflictsWithStagingRoot(workspace, staging, "tests/fixtures/summary.json") {
		t.Fatal("unrelated artifact path was reported as a staging-root conflict")
	}
}

func TestApplyValidatesUnmatchedArchiveEntriesBeforeCommit(t *testing.T) {
	archive := tarGzipArchive(t, func(t *testing.T, writer *tar.Writer) {
		writeTarFile(t, writer, "keep/report.txt", "keep")
		writeTarFile(t, writer, "../escape.txt", "escape")
	})

	target := t.TempDir()
	_, err := Apply(context.Background(), target, []string{"unmatched/**"}, bytes.NewReader(archive), ApplyOptions{})
	if err == nil || !strings.Contains(err.Error(), "invalid artifact path") {
		t.Fatalf("apply unmatched invalid entry error = %v, want invalid artifact path", err)
	}
	if _, err := os.Stat(filepath.Join(target, "keep", "report.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unmatched safe file after failed apply stat err = %v, want not exists", err)
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

func buildArchiveForApplyTest(t *testing.T, root string, patterns []string, writer io.Writer) Manifest {
	t.Helper()
	normalized, err := NormalizePatterns(patterns)
	if err != nil {
		t.Fatal(err)
	}
	files := archiveFilesForApplyTest(t, root, normalized)
	manifest, err := WriteArchive(writer, files)
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func archiveFilesForApplyTest(t *testing.T, root string, patterns []string) []ArchiveFile {
	t.Helper()
	seen := make(map[string]struct{})
	var files []ArchiveFile
	for _, walkRoot := range PatternWalkRoots(patterns) {
		fullRoot := filepath.Join(root, filepath.FromSlash(walkRoot))
		if walkRoot == "" {
			fullRoot = root
		}
		err := filepath.WalkDir(fullRoot, func(fullPath string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				if fullPath == fullRoot && os.IsNotExist(walkErr) {
					return nil
				}
				return walkErr
			}
			if fullPath == root {
				return nil
			}
			rel, err := filepath.Rel(root, fullPath)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if IsGitControlPath(rel) {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.IsDir() {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			for _, pattern := range patterns {
				if !PatternMatches(pattern, rel) {
					continue
				}
				if _, ok := seen[rel]; ok {
					return nil
				}
				seen[rel] = struct{}{}
				data, err := os.ReadFile(fullPath)
				if err != nil {
					return err
				}
				files = append(files, ArchiveFile{
					Path:    rel,
					Mode:    int64(info.Mode().Perm()),
					ModTime: info.ModTime(),
					Data:    data,
				})
				return nil
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})
	return files
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
