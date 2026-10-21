package syncbundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
)

var ErrMissingManifest = errors.New("bundle manifest is missing")

func ReadManifest(reader io.Reader) (Manifest, error) {
	gzipReader, err := gzip.NewReader(reader)
	if err != nil {
		return Manifest{}, err
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			return Manifest{}, ErrMissingManifest
		}
		if err != nil {
			return Manifest{}, err
		}
		name := path.Clean(header.Name)
		if header.Name != name {
			return Manifest{}, fmt.Errorf("bundle path %q is not normalized", header.Name)
		}
		if name == "." || strings.HasPrefix(name, "../") || path.IsAbs(name) {
			return Manifest{}, fmt.Errorf("bundle path %q is invalid", header.Name)
		}
		if name != "manifest.json" {
			continue
		}
		if header.Typeflag != tar.TypeReg {
			return Manifest{}, fmt.Errorf("manifest.json is not a regular file")
		}
		var manifest Manifest
		data, err := readManifestEntryData(tarReader, header)
		if err != nil {
			return Manifest{}, err
		}
		manifest, err = decodeManifestJSON(data)
		if err != nil {
			return Manifest{}, err
		}
		if err := validateManifest(manifest); err != nil {
			return Manifest{}, err
		}
		if err := validateSnapshotDigestForRead(manifest); err != nil {
			return Manifest{}, err
		}
		return manifest, nil
	}
}

func readManifestEntryData(reader io.Reader, header *tar.Header) ([]byte, error) {
	if header.Size < 0 {
		return nil, fmt.Errorf("manifest.json has invalid size")
	}
	if header.Size > MaxManifestBytes {
		return nil, &AdmissionError{"manifest_bytes", header.Size, MaxManifestBytes}
	}
	data, err := io.ReadAll(io.LimitReader(reader, header.Size+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != header.Size {
		return nil, fmt.Errorf("manifest.json size mismatch")
	}
	return data, nil
}

func decodeManifestJSON(data []byte) (Manifest, error) {
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Manifest{}, fmt.Errorf("manifest.json contains trailing JSON data")
		}
		return Manifest{}, err
	}
	return manifest, nil
}

func Validate(reader io.Reader) error {
	_, _, err := readBundleFiles(reader)
	return err
}

func ValidateWithManifest(reader io.Reader) (Manifest, error) {
	manifest, _, err := readBundleFiles(reader)
	return manifest, err
}

// ValidateManifest validates source metadata and symlink containment in both
// the Git baseline and the materialized workspace. It does not read file bytes
// or validate their digest; bundle readers and writers validate those as well.
func ValidateManifest(manifest Manifest) error {
	return validateManifest(manifest)
}

func validateManifest(manifest Manifest) error {
	if manifest.Version != FormatVersion {
		return fmt.Errorf("unsupported bundle format version %d", manifest.Version)
	}
	if err := validateSubmodules(manifest); err != nil {
		return err
	}
	if manifest.BaselineKey != "" {
		normalized, err := NormalizeBaselineKey(manifest.BaselineKey)
		if err != nil {
			return err
		}
		if manifest.BaselineKey != normalized {
			return fmt.Errorf("baseline key %q is not normalized", manifest.BaselineKey)
		}
	}
	if manifest.BaselineRef && manifest.BaselineKey == "" {
		return fmt.Errorf("baseline_ref requires baseline_key")
	}
	if manifest.ChangeMode != "" {
		normalized, err := NormalizeChangeMode(manifest.ChangeMode)
		if err != nil {
			return err
		}
		if manifest.ChangeMode != normalized {
			return fmt.Errorf("change mode %q is not normalized", manifest.ChangeMode)
		}
	}
	if manifest.SourcePolicy != nil {
		if err := validateSourcePolicyBinding(*manifest.SourcePolicy); err != nil {
			return err
		}
	}
	if err := validateReviewedFixtureDeclaration(manifest); err != nil {
		return err
	}
	for _, changed := range manifest.ChangedFiles {
		rel, err := validateRelativePath(changed)
		if err != nil {
			return err
		}
		if changed != rel {
			return fmt.Errorf("changed path %q is not normalized", changed)
		}
		if isGitControlPath(rel) {
			return fmt.Errorf("changed path %q targets git control metadata", changed)
		}
	}
	seen := make(map[string]string)
	baselinePaths := make(map[string]struct{})
	workspacePaths := make(map[string]struct{})
	for _, group := range []struct {
		name    string
		entries []Entry
	}{
		{name: "baseline", entries: manifest.Baseline},
		{name: "workspace", entries: manifest.Workspace},
	} {
		for _, entry := range group.entries {
			rel, err := validateRelativePath(entry.Path)
			if err != nil {
				return err
			}
			if entry.Path != rel {
				return fmt.Errorf("%s entry path %q is not normalized", group.name, entry.Path)
			}
			if isGitControlPath(rel) {
				return fmt.Errorf("%s entry path %q targets git control metadata", group.name, entry.Path)
			}
			if entry.Mode < 0 || entry.Mode > 0o777 {
				return fmt.Errorf("%s entry %q has invalid mode %o", group.name, entry.Path, entry.Mode)
			}
			switch entry.Type {
			case entryTypeFile:
				if entry.Size < 0 {
					return fmt.Errorf("%s entry %q has negative size", group.name, entry.Path)
				}
				if entry.Linkname != "" {
					return fmt.Errorf("%s file entry %q must not include linkname", group.name, entry.Path)
				}
			case entryTypeSymlink:
				if entry.Size != 0 {
					return fmt.Errorf("%s symlink entry %q must not include size", group.name, entry.Path)
				}
				if err := ValidateSymlinkTargetSyntax(entry.Linkname); err != nil {
					return fmt.Errorf("%s symlink %q has invalid target: %w", group.name, entry.Path, err)
				}
			default:
				return fmt.Errorf("%s entry %q has unsupported type %q", group.name, entry.Path, entry.Type)
			}
			key := group.name + "/" + entry.Path
			if previous, ok := seen[key]; ok {
				return fmt.Errorf("duplicate %s entry %q and %q", group.name, previous, entry.Path)
			}
			seen[key] = entry.Path
			if group.name == "baseline" {
				baselinePaths[entry.Path] = struct{}{}
			} else {
				workspacePaths[entry.Path] = struct{}{}
			}
		}
		if err := rejectPathPrefixConflicts(group.name, group.entries); err != nil {
			return err
		}
	}
	if err := validateSourceFilterEntries(manifest); err != nil {
		return err
	}
	if err := validateUntrackedSourceOrigins(manifest, baselinePaths, workspacePaths); err != nil {
		return err
	}
	deletedPaths := make(map[string]struct{}, len(manifest.Deleted))
	for _, deleted := range manifest.Deleted {
		rel, err := validateRelativePath(deleted)
		if err != nil {
			return err
		}
		if deleted != rel {
			return fmt.Errorf("deleted path %q is not normalized", deleted)
		}
		if isGitControlPath(rel) {
			return fmt.Errorf("deleted path %q targets git control metadata", deleted)
		}
		if _, ok := baselinePaths[deleted]; !ok {
			return fmt.Errorf("deleted path %q is not a baseline entry", deleted)
		}
		if _, ok := workspacePaths[deleted]; ok {
			return fmt.Errorf("deleted path %q conflicts with workspace entry", deleted)
		}
		deletedPaths[deleted] = struct{}{}
	}
	if err := validateWorkspaceBaselineReplacements(baselinePaths, workspacePaths, deletedPaths); err != nil {
		return err
	}
	return validateSymlinkContainment(manifest)
}

func validateUntrackedSourceOrigins(manifest Manifest, baseline, workspace map[string]struct{}) error {
	if len(manifest.UntrackedFiles) == 0 {
		return nil
	}
	if manifest.SourcePolicy == nil || len(manifest.SourcePolicy.Scopes) == 0 || manifest.ChangeMode == ChangeModeStaged {
		return fmt.Errorf("untracked origin metadata requires a working multi-scope source")
	}
	previous := ""
	for _, rel := range manifest.UntrackedFiles {
		if rel <= previous {
			return fmt.Errorf("untracked source paths must be unique and sorted")
		}
		previous = rel
		if _, exists := workspace[rel]; !exists {
			return fmt.Errorf("untracked source path %q must be a workspace entry", rel)
		}
		if _, exists := baseline[rel]; exists {
			return fmt.Errorf("multi-scope source cannot reclassify baseline path %q as untracked; stage its removal or restore its tracked state", rel)
		}
	}
	return nil
}

func validateSnapshotDigestForRead(manifest Manifest) error {
	return ValidateSnapshotDigest(manifest.SnapshotDigest)
}

// ValidateSnapshotDigest validates the public digest representation used by
// bundles and verification sessions.
func ValidateSnapshotDigest(value string) error {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return fmt.Errorf("snapshot_digest must match sha256:<64 lowercase hex characters>")
	}
	for _, r := range strings.TrimPrefix(value, "sha256:") {
		if r >= '0' && r <= '9' || r >= 'a' && r <= 'f' {
			continue
		}
		return fmt.Errorf("snapshot_digest must match sha256:<64 lowercase hex characters>")
	}
	return nil
}

func validateWorkspaceBaselineReplacements(baselinePaths, workspacePaths, deletedPaths map[string]struct{}) error {
	sortedBaselinePaths := make([]string, 0, len(baselinePaths))
	for baselinePath := range baselinePaths {
		sortedBaselinePaths = append(sortedBaselinePaths, baselinePath)
	}
	sort.Strings(sortedBaselinePaths)

	for workspacePath := range workspacePaths {
		for ancestor := parentPath(workspacePath); ancestor != ""; ancestor = parentPath(ancestor) {
			if _, ok := baselinePaths[ancestor]; !ok {
				continue
			}
			if _, deleted := deletedPaths[ancestor]; !deleted {
				return fmt.Errorf("workspace entry %q requires deleted baseline ancestor %q", workspacePath, ancestor)
			}
		}
		prefix := workspacePath + "/"
		for index := sort.SearchStrings(sortedBaselinePaths, prefix); index < len(sortedBaselinePaths); index++ {
			baselinePath := sortedBaselinePaths[index]
			if !strings.HasPrefix(baselinePath, prefix) {
				break
			}
			if _, deleted := deletedPaths[baselinePath]; !deleted {
				return fmt.Errorf("workspace entry %q replaces undeleted baseline entry %q", workspacePath, baselinePath)
			}
		}
	}
	return nil
}

func parentPath(rel string) string {
	if index := strings.LastIndex(rel, "/"); index >= 0 {
		return rel[:index]
	}
	return ""
}

func rejectPathPrefixConflicts(group string, entries []Entry) error {
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		paths = append(paths, entry.Path)
	}
	sort.Strings(paths)
	for i := 1; i < len(paths); i++ {
		previous := paths[i-1]
		current := paths[i]
		if strings.HasPrefix(current, previous+"/") {
			return fmt.Errorf("%s entry %q conflicts with nested entry %q", group, previous, current)
		}
	}
	return nil
}
