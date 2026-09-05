package main

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/artifactbundle"
)

func buildArtifactArchiveForTest(t *testing.T, root string, patterns []string, writer io.Writer) artifactbundle.Manifest {
	t.Helper()
	normalized, err := artifactbundle.NormalizePatterns(patterns)
	if err != nil {
		t.Fatal(err)
	}
	files := artifactArchiveFilesForTest(t, root, normalized)
	manifest, err := artifactbundle.WriteArchive(writer, files)
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func artifactArchiveFilesForTest(t *testing.T, root string, patterns []string) []artifactbundle.ArchiveFile {
	t.Helper()
	seen := make(map[string]struct{})
	var files []artifactbundle.ArchiveFile
	for _, walkRoot := range artifactbundle.PatternWalkRoots(patterns) {
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
			if artifactbundle.IsGitControlPath(rel) {
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
				if !artifactbundle.PatternMatches(pattern, rel) {
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
				files = append(files, artifactbundle.ArchiveFile{
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
