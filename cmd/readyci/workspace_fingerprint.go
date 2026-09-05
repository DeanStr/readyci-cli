package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DeanStr/readyci-cli/internal/syncbundlebuild"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

type workspaceFingerprintOptions struct {
	ExcludePatterns  []string
	IncludeUntracked *bool
	ChangeMode       string
}

func workspaceFingerprint(ctx context.Context, optionValues ...workspaceFingerprintOptions) (string, error) {
	var options workspaceFingerprintOptions
	if len(optionValues) > 0 {
		options = optionValues[0]
	}
	changeMode, err := syncbundle.NormalizeChangeMode(options.ChangeMode)
	if err != nil {
		return "", err
	}
	digest := sha256.New()
	if err := hashGitOutput(ctx, digest, "head", "rev-parse", "HEAD"); err != nil {
		return "", err
	}
	repoRoot, err := gitOutput(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("git rev-parse --show-toplevel: %w", err)
	}
	filter, err := syncbundlebuild.NewPathFilter(ctx, repoRoot, syncbundlebuild.BuildOptions{
		ChangeMode:       changeMode,
		ExcludePatterns:  append([]string(nil), options.ExcludePatterns...),
		IncludeUntracked: options.IncludeUntracked,
	})
	if err != nil {
		return "", err
	}
	var unstagedPaths []string
	if changeMode != syncbundle.ChangeModeStaged {
		unstagedPaths, err = gitPathList(ctx, repoRoot, "diff", "--name-only", "-z", "--no-ext-diff")
		if err != nil {
			return "", err
		}
		if err := hashGitNameStatus(ctx, digest, filter, "unstaged-status", "-C", repoRoot, "diff", "--name-status", "-z", "--no-ext-diff", "-M"); err != nil {
			return "", err
		}
	}
	stagedPaths, err := gitPathList(ctx, repoRoot, "diff", "--cached", "--name-only", "-z", "--no-ext-diff")
	if err != nil {
		return "", err
	}
	if err := hashGitNameStatus(ctx, digest, filter, "staged-status", "-C", repoRoot, "diff", "--cached", "--name-status", "-z", "--no-ext-diff", "-M"); err != nil {
		return "", err
	}
	for _, path := range unstagedPaths {
		included, err := filter.Includes(path)
		if err != nil {
			return "", err
		}
		if !included {
			continue
		}
		if err := hashGitOutput(ctx, digest, "unstaged-diff:"+path, "-C", repoRoot, "diff", "--no-ext-diff", "--binary", "--", gitLiteralPathspec(path)); err != nil {
			return "", err
		}
	}
	for _, path := range stagedPaths {
		included, err := filter.Includes(path)
		if err != nil {
			return "", err
		}
		if !included {
			continue
		}
		if err := hashGitOutput(ctx, digest, "staged-diff:"+path, "-C", repoRoot, "diff", "--cached", "--no-ext-diff", "--binary", "--", gitLiteralPathspec(path)); err != nil {
			return "", err
		}
	}
	if changeMode != syncbundle.ChangeModeStaged {
		untrackedPaths, err := gitPathList(ctx, repoRoot, "ls-files", "-z", "--others", "--exclude-standard")
		if err != nil {
			return "", err
		}
		for _, path := range untrackedPaths {
			included, err := filter.Includes(path)
			if err != nil {
				return "", err
			}
			if !included {
				continue
			}
			if err := hashUntrackedPath(digest, repoRoot, path); err != nil {
				return "", err
			}
		}
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

type gitNameStatusRecord struct {
	Status string
	Paths  []string
}

func hashGitNameStatus(ctx context.Context, digest hash.Hash, filter syncbundlebuild.PathFilter, label string, args ...string) error {
	out, err := gitOutputBytes(ctx, args...)
	if err != nil {
		return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	records, err := parseGitNameStatus(out)
	if err != nil {
		return err
	}
	var included []gitNameStatusRecord
	for _, record := range records {
		includeRecord := false
		for _, path := range record.Paths {
			includedPath, err := filter.Includes(path)
			if err != nil {
				return err
			}
			if includedPath {
				includeRecord = true
			}
		}
		if includeRecord {
			included = append(included, record)
		}
	}
	sort.Slice(included, func(i, j int) bool {
		left := included[i].Status + "\x00" + strings.Join(included[i].Paths, "\x00")
		right := included[j].Status + "\x00" + strings.Join(included[j].Paths, "\x00")
		return left < right
	})
	for _, record := range included {
		payload := record.Status + "\x00" + strings.Join(record.Paths, "\x00")
		hashSection(digest, label, []byte(payload))
	}
	return nil
}

func parseGitNameStatus(out []byte) ([]gitNameStatusRecord, error) {
	fields := splitNULBytes(out)
	records := make([]gitNameStatusRecord, 0, len(fields))
	for index := 0; index < len(fields); {
		status := string(fields[index])
		index++
		if status == "" {
			continue
		}
		pathCount := 1
		switch status[0] {
		case 'R', 'C':
			pathCount = 2
		}
		if index+pathCount > len(fields) {
			return nil, fmt.Errorf("git name-status record %q is missing path data", status)
		}
		paths := make([]string, 0, pathCount)
		for _, raw := range fields[index : index+pathCount] {
			paths = append(paths, filepath.ToSlash(string(raw)))
		}
		index += pathCount
		records = append(records, gitNameStatusRecord{Status: status, Paths: paths})
	}
	return records, nil
}

func splitNULBytes(out []byte) [][]byte {
	var fields [][]byte
	for len(out) > 0 {
		index := bytes.IndexByte(out, 0)
		if index < 0 {
			fields = append(fields, out)
			break
		}
		if index > 0 {
			fields = append(fields, out[:index])
		}
		out = out[index+1:]
	}
	return fields
}

func gitPathList(ctx context.Context, repoRoot string, args ...string) ([]string, error) {
	out, err := gitOutputBytes(ctx, append([]string{"-C", repoRoot}, args...)...)
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	var paths []string
	for _, path := range strings.Split(string(out), "\x00") {
		if path == "" {
			continue
		}
		paths = append(paths, filepath.ToSlash(path))
	}
	paths = uniqueStrings(paths)
	sort.Strings(paths)
	return paths, nil
}

func gitLiteralPathspec(path string) string {
	return ":(literal)" + path
}

func hashGitOutput(ctx context.Context, digest hash.Hash, label string, args ...string) error {
	out, err := gitOutputBytes(ctx, args...)
	if err != nil {
		return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	hashSection(digest, label, out)
	return nil
}

func hashSection(digest hash.Hash, label string, value []byte) {
	_, _ = digest.Write([]byte(label))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write(value)
	_, _ = digest.Write([]byte{0})
}

func hashUntrackedPath(digest hash.Hash, repoRoot string, path string) error {
	cleaned := filepath.Clean(filepath.FromSlash(path))
	if cleaned == "." || cleaned == ".." || filepath.IsAbs(cleaned) || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return fmt.Errorf("invalid untracked path %q", path)
	}
	fullPath := filepath.Join(repoRoot, cleaned)
	info, err := os.Lstat(fullPath)
	if errors.Is(err, os.ErrNotExist) {
		hashSection(digest, "untracked-missing:"+filepath.ToSlash(cleaned), nil)
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat untracked path %s: %w", cleaned, err)
	}
	metadata := fmt.Sprintf("%s\x00%s\x00%d", filepath.ToSlash(cleaned), info.Mode().String(), info.Size())
	hashSection(digest, "untracked-meta", []byte(metadata))
	switch {
	case info.Mode().IsRegular():
		file, err := os.Open(fullPath)
		if err != nil {
			return fmt.Errorf("open untracked path %s: %w", cleaned, err)
		}
		defer file.Close()
		if _, err := io.Copy(digest, file); err != nil {
			return fmt.Errorf("hash untracked path %s: %w", cleaned, err)
		}
		_, _ = digest.Write([]byte{0})
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(fullPath)
		if err != nil {
			return fmt.Errorf("read untracked symlink %s: %w", cleaned, err)
		}
		hashSection(digest, "untracked-symlink", []byte(target))
	case info.IsDir():
		hashSection(digest, "untracked-dir", []byte(filepath.ToSlash(cleaned)))
	default:
		hashSection(digest, "untracked-special", []byte(info.Mode().String()))
	}
	return nil
}

func gitOutput(ctx context.Context, args ...string) (string, error) {
	out, err := gitOutputBytes(ctx, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func gitOutputBytes(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return out, nil
}
