package artifactapply

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/artifactbundle"
)

type ApplyOptions struct {
	DryRun bool
}

type stagedArtifactFile struct {
	rel    string
	source string
	target string
}

func Apply(ctx context.Context, workspaceRoot string, patterns []string, reader io.Reader, options ApplyOptions) (artifactbundle.Manifest, error) {
	normalizedPatterns, err := artifactbundle.NormalizePatterns(patterns)
	if err != nil {
		return artifactbundle.Manifest{}, err
	}
	if len(normalizedPatterns) == 0 {
		return artifactbundle.Manifest{}, nil
	}
	workspaceRoot, err = filepath.Abs(workspaceRoot)
	if err != nil {
		return artifactbundle.Manifest{}, err
	}
	stagingRoot := ""
	if !options.DryRun {
		stagingRoot, err = os.MkdirTemp(workspaceRoot, ".readyci-apply-*")
		if err != nil {
			return artifactbundle.Manifest{}, err
		}
		defer os.RemoveAll(stagingRoot)
	}
	gzipReader, err := gzip.NewReader(io.LimitReader(reader, artifactbundle.MaxCompressedBytes+1))
	if err != nil {
		return artifactbundle.Manifest{}, err
	}
	gzipClosed := false
	defer func() {
		if !gzipClosed {
			_ = gzipReader.Close()
		}
	}()

	tarReader := tar.NewReader(gzipReader)
	seen := make(map[string]struct{})
	var applied artifactbundle.Manifest
	var staged []stagedArtifactFile
	var archivePaths []string
	var archiveTotal int64
	var archiveCount int
	for {
		if err := ctx.Err(); err != nil {
			return artifactbundle.Manifest{}, err
		}
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			if err := artifactbundle.VerifyGzipFooter(gzipReader); err != nil {
				return artifactbundle.Manifest{}, err
			}
			gzipClosed = true
			if parent, nested, ok := artifactbundle.FindPathPrefixConflict(archivePaths); ok {
				return artifactbundle.Manifest{}, fmt.Errorf("artifact path %q conflicts with nested artifact path %q", parent, nested)
			}
			if !options.DryRun {
				if err := validateStagedArtifactTargets(workspaceRoot, staged); err != nil {
					return artifactbundle.Manifest{}, err
				}
				for _, file := range staged {
					if _, err := ensureSafeArtifactParent(workspaceRoot, file.rel); err != nil {
						return artifactbundle.Manifest{}, err
					}
					if err := os.Rename(file.source, file.target); err != nil {
						return artifactbundle.Manifest{}, err
					}
				}
			}
			return applied, nil
		}
		if err != nil {
			return artifactbundle.Manifest{}, err
		}
		rel, err := artifactbundle.ValidateArchivePath(header.Name)
		if err != nil {
			return artifactbundle.Manifest{}, err
		}
		if stagingRoot != "" && conflictsWithStagingRoot(workspaceRoot, stagingRoot, rel) {
			return artifactbundle.Manifest{}, fmt.Errorf("artifact path %q conflicts with apply staging directory", rel)
		}
		if header.Typeflag != tar.TypeReg {
			return artifactbundle.Manifest{}, fmt.Errorf("artifact %q is not a regular file", rel)
		}
		if _, ok := seen[rel]; ok {
			return artifactbundle.Manifest{}, fmt.Errorf("duplicate artifact path %q", rel)
		}
		seen[rel] = struct{}{}
		archivePaths = append(archivePaths, rel)
		if header.Size < 0 {
			return artifactbundle.Manifest{}, fmt.Errorf("artifact %q has invalid size", rel)
		}
		if header.Size > artifactbundle.MaxExpandedBytes-archiveTotal {
			return artifactbundle.Manifest{}, artifactbundle.ErrTooLarge
		}
		if archiveCount >= artifactbundle.MaxEntryCount {
			return artifactbundle.Manifest{}, artifactbundle.ErrTooLarge
		}
		matched := false
		for _, pattern := range normalizedPatterns {
			if artifactbundle.PatternMatches(pattern, rel) {
				matched = true
				break
			}
		}
		var n int64
		if matched {
			if options.DryRun {
				n, err = io.Copy(io.Discard, tarReader)
			} else {
				var stagedFile stagedArtifactFile
				n, stagedFile, err = stageArchiveFile(workspaceRoot, stagingRoot, rel, header, tarReader)
				if err == nil {
					staged = append(staged, stagedFile)
				}
			}
			if err != nil {
				return artifactbundle.Manifest{}, err
			}
			applied.Files = append(applied.Files, artifactbundle.File{Path: rel, SizeBytes: header.Size})
			applied.TotalExpandedBytes += header.Size
		} else {
			n, err = io.Copy(io.Discard, tarReader)
			if err != nil {
				return artifactbundle.Manifest{}, err
			}
		}
		if n != header.Size {
			return artifactbundle.Manifest{}, io.ErrUnexpectedEOF
		}
		archiveTotal += header.Size
		archiveCount++
	}
}

func stageArchiveFile(workspaceRoot, stagingRoot, rel string, header *tar.Header, reader io.Reader) (int64, stagedArtifactFile, error) {
	target := filepath.Join(workspaceRoot, filepath.FromSlash(rel))
	stagedPath := filepath.Join(stagingRoot, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(stagedPath), 0o755); err != nil {
		return 0, stagedArtifactFile{}, err
	}
	file, err := os.OpenFile(stagedPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, stagedArtifactFile{}, err
	}
	defer func() {
		_ = file.Close()
	}()
	n, err := io.Copy(file, reader)
	if err != nil {
		return n, stagedArtifactFile{}, err
	}
	mode := os.FileMode(header.Mode) & 0o777
	if mode == 0 {
		mode = 0o644
	}
	if err := file.Chmod(mode); err != nil {
		return n, stagedArtifactFile{}, err
	}
	if err := file.Close(); err != nil {
		return n, stagedArtifactFile{}, err
	}
	return n, stagedArtifactFile{rel: rel, source: stagedPath, target: target}, nil
}

func conflictsWithStagingRoot(workspaceRoot, stagingRoot, rel string) bool {
	stagingRel, err := filepath.Rel(workspaceRoot, stagingRoot)
	if err != nil {
		return true
	}
	stagingRel = filepath.ToSlash(stagingRel)
	return rel == stagingRel || strings.HasPrefix(rel, stagingRel+"/")
}

func validateStagedArtifactTargets(workspaceRoot string, staged []stagedArtifactFile) error {
	for _, file := range staged {
		if err := validateSafeArtifactParent(workspaceRoot, file.rel); err != nil {
			return err
		}
		if info, err := os.Lstat(file.target); err == nil {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("refusing to overwrite non-regular file %q", file.rel)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func validateSafeArtifactParent(workspaceRoot, rel string) error {
	parts := strings.Split(path.Dir(rel), "/")
	parent := workspaceRoot
	for _, part := range parts {
		if part == "." || part == "" {
			continue
		}
		parent = filepath.Join(parent, filepath.FromSlash(part))
		info, err := os.Lstat(parent)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to write through symlink parent %q", strings.TrimPrefix(filepath.ToSlash(parent), filepath.ToSlash(workspaceRoot)+"/"))
		}
		if !info.IsDir() {
			return fmt.Errorf("artifact parent %q is not a directory", strings.TrimPrefix(filepath.ToSlash(parent), filepath.ToSlash(workspaceRoot)+"/"))
		}
	}
	return nil
}

func ensureSafeArtifactParent(workspaceRoot, rel string) (string, error) {
	parts := strings.Split(path.Dir(rel), "/")
	parent := workspaceRoot
	for _, part := range parts {
		if part == "." || part == "" {
			continue
		}
		parent = filepath.Join(parent, filepath.FromSlash(part))
		info, err := os.Lstat(parent)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(parent, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
				return "", err
			}
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("refusing to write through symlink parent %q", strings.TrimPrefix(filepath.ToSlash(parent), filepath.ToSlash(workspaceRoot)+"/"))
		}
		if !info.IsDir() {
			return "", fmt.Errorf("artifact parent %q is not a directory", strings.TrimPrefix(filepath.ToSlash(parent), filepath.ToSlash(workspaceRoot)+"/"))
		}
	}
	return parent, nil
}
