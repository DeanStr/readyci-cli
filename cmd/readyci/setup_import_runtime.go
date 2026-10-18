package main

import (
	"os"
	"path/filepath"
	"strings"
)

func setupImportRuntimes(root string, candidate repoImportCandidate) []string {
	runtimes := append([]string(nil), candidate.Runtimes...)
	directories := append([]string{"."}, candidate.WorkingDirectories...)
	for _, dir := range directories {
		if dir == "" {
			dir = "."
		}
		for _, runtime := range []struct{ file, name string }{{"go.mod", "go"}, {"package.json", "node"}, {"Cargo.toml", "rust"}} {
			if importManifestExists(root, dir, runtime.file) {
				runtimes = appendUniqueString(runtimes, runtime.name)
			}
		}
	}
	return runtimes
}

func setupImportRustTargetPaths(root string, candidate repoImportCandidate) []string {
	var paths []string
	for _, selected := range append([]string{"."}, candidate.WorkingDirectories...) {
		if selected == "" {
			selected = "."
		}
		if !filepath.IsLocal(selected) && selected != "." {
			continue
		}
		for dir := filepath.Clean(selected); ; dir = filepath.Dir(dir) {
			if importManifestExists(root, dir, "Cargo.toml") {
				paths = appendUniqueString(paths, filepath.ToSlash(filepath.Join(dir, "target")))
			}
			if dir == "." {
				break
			}
		}
	}
	return paths
}

// Runtime inference must not follow a selected directory or manifest outside
// the uploaded repository, including through symlinked parent directories.
func importManifestExists(root, dir, manifest string) bool {
	if !filepath.IsLocal(dir) {
		return false
	}
	parts := strings.Split(filepath.Join(dir, manifest), string(filepath.Separator))
	current := root
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return false
		}
		if i == len(parts)-1 {
			return info.Mode().IsRegular()
		}
		if !info.IsDir() {
			return false
		}
	}
	return false
}
