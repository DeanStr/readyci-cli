package syncbundlebuild

import (
	"context"
	"io"
	"os"

	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func Build(ctx context.Context, cwd string, writer io.Writer) (BuildResult, error) {
	return BuildWithOptions(ctx, cwd, writer, BuildOptions{})
}

func ChangedFiles(ctx context.Context, cwd string, options BuildOptions) (ChangedFilesResult, error) {
	changeMode, err := syncbundle.NormalizeChangeMode(options.ChangeMode)
	if err != nil {
		return ChangedFilesResult{}, err
	}
	root, err := repositoryRoot(ctx, cwd)
	if err != nil {
		return ChangedFilesResult{}, err
	}
	ignorePatterns, err := readProjectIgnore(root)
	if err != nil {
		return ChangedFilesResult{}, err
	}
	excludePatterns := append([]string(nil), options.ExcludePatterns...)
	ignorePatterns = append(ignorePatterns, excludePatterns...)
	matcher := newIgnoreMatcher(ignorePatterns)
	includeUntracked := true
	if options.IncludeUntracked != nil {
		includeUntracked = *options.IncludeUntracked
	}
	if changeMode == syncbundle.ChangeModeStaged {
		includeUntracked = false
	}
	untrackedFiles, err := planIncludedUntrackedFiles(ctx, root, matcher, includeUntracked, changeMode)
	if err != nil {
		return ChangedFilesResult{}, err
	}
	changedFiles, err := planChangedFiles(ctx, root, gitHead(ctx, root), matcher, changeMode, includeUntracked)
	if err != nil {
		return ChangedFilesResult{}, err
	}
	return ChangedFilesResult{
		Root:             root,
		ChangeMode:       changeMode,
		IncludeUntracked: includeUntracked,
		ExcludePatterns:  excludePatterns,
		ChangedFiles:     changedFiles,
		UntrackedFiles:   untrackedFiles,
	}, nil
}

func BuildWithOptions(ctx context.Context, cwd string, writer io.Writer, options BuildOptions) (BuildResult, error) {
	planned, err := planBundle(ctx, cwd, options)
	if err != nil {
		return BuildResult{}, err
	}
	manifest, err := writePlannedBundle(writer, planned, options.OmitBaseline)
	if err != nil {
		return BuildResult{}, err
	}
	return BuildResult{
		Root:             planned.root,
		Manifest:         manifest,
		IncludeUntracked: planned.includeUntracked,
		UntrackedFiles:   append([]string(nil), planned.untrackedFiles...),
	}, nil
}

func BuildToTemp(ctx context.Context, cwd string) (string, BuildResult, error) {
	return BuildToTempWithOptions(ctx, cwd, BuildOptions{})
}

func BuildToTempWithOptions(ctx context.Context, cwd string, options BuildOptions) (string, BuildResult, error) {
	file, err := os.CreateTemp("", "preflight-bundle-*.tar.gz")
	if err != nil {
		return "", BuildResult{}, err
	}
	path := file.Name()
	cleanup := true
	defer func() {
		file.Close()
		if cleanup {
			_ = os.Remove(path)
		}
	}()

	result, err := BuildWithOptions(ctx, cwd, file, options)
	if err != nil {
		return "", BuildResult{}, err
	}
	if result.Size, err = file.Seek(0, io.SeekEnd); err != nil {
		return "", BuildResult{}, err
	}
	cleanup = false
	return path, result, nil
}

func BuildFullAndDeltaToTempWithOptions(ctx context.Context, cwd string, options BuildOptions) (string, BuildResult, string, BuildResult, error) {
	planned, err := planBundle(ctx, cwd, options)
	if err != nil {
		return "", BuildResult{}, "", BuildResult{}, err
	}

	fullFile, err := os.CreateTemp("", "preflight-bundle-*.tar.gz")
	if err != nil {
		return "", BuildResult{}, "", BuildResult{}, err
	}
	fullPath := fullFile.Name()
	cleanupFull := true
	defer func() {
		fullFile.Close()
		if cleanupFull {
			_ = os.Remove(fullPath)
		}
	}()

	fullManifest, err := writePlannedBundle(fullFile, planned, false)
	if err != nil {
		return "", BuildResult{}, "", BuildResult{}, err
	}
	fullSize, err := fullFile.Seek(0, io.SeekEnd)
	if err != nil {
		return "", BuildResult{}, "", BuildResult{}, err
	}
	fullResult := BuildResult{
		Root:             planned.root,
		Manifest:         fullManifest,
		Size:             fullSize,
		IncludeUntracked: planned.includeUntracked,
		UntrackedFiles:   append([]string(nil), planned.untrackedFiles...),
	}
	if fullManifest.BaselineKey == "" {
		cleanupFull = false
		return fullPath, fullResult, "", BuildResult{}, nil
	}

	deltaFile, err := os.CreateTemp("", "preflight-bundle-*.tar.gz")
	if err != nil {
		return "", BuildResult{}, "", BuildResult{}, err
	}
	deltaPath := deltaFile.Name()
	cleanupDelta := true
	defer func() {
		deltaFile.Close()
		if cleanupDelta {
			_ = os.Remove(deltaPath)
		}
	}()

	deltaManifest, err := writePlannedBundle(deltaFile, planned, true)
	if err != nil {
		return "", BuildResult{}, "", BuildResult{}, err
	}
	deltaSize, err := deltaFile.Seek(0, io.SeekEnd)
	if err != nil {
		return "", BuildResult{}, "", BuildResult{}, err
	}

	cleanupFull = false
	cleanupDelta = false
	return fullPath, fullResult, deltaPath, BuildResult{
		Root:             planned.root,
		Manifest:         deltaManifest,
		Size:             deltaSize,
		IncludeUntracked: planned.includeUntracked,
		UntrackedFiles:   append([]string(nil), planned.untrackedFiles...),
	}, nil
}
