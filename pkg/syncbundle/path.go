package syncbundle

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

func CleanSlashPath(value string) string {
	value = slashPath(value)
	value = strings.TrimPrefix(value, "./")
	if value == "." {
		return ""
	}
	return strings.Trim(path.Clean(value), "/")
}

func cleanSlashPath(value string) string {
	return CleanSlashPath(value)
}

func ValidateRelativePath(value string) (string, error) {
	if strings.Contains(value, "\\") {
		return "", fmt.Errorf("path %q contains backslash", value)
	}
	if containsControlCharacter(value) {
		return "", fmt.Errorf("path %q contains control character", value)
	}
	raw := filepath.ToSlash(value)
	if path.IsAbs(raw) || filepath.IsAbs(value) || hasWindowsVolumePrefix(raw) {
		return "", fmt.Errorf("absolute path %q is not allowed", value)
	}
	rel := CleanSlashPath(value)
	if rel == "" {
		return "", fmt.Errorf("empty path")
	}
	if strings.HasPrefix(rel, "../") || strings.Contains(rel, "/../") || rel == ".." {
		return "", fmt.Errorf("path %q escapes workspace", value)
	}
	return rel, nil
}

func validateRelativePath(value string) (string, error) {
	return ValidateRelativePath(value)
}

func ValidateSymlinkTarget(value string) error {
	if containsControlCharacter(value) {
		return fmt.Errorf("symlink target %q contains control character", value)
	}
	target := strings.TrimSpace(value)
	if target == "" {
		return fmt.Errorf("symlink target is empty")
	}
	if strings.Contains(target, "\\") {
		return fmt.Errorf("symlink target %q contains backslash", value)
	}
	slashTarget := filepath.ToSlash(target)
	if path.IsAbs(slashTarget) || filepath.IsAbs(target) || hasWindowsVolumePrefix(slashTarget) {
		return fmt.Errorf("symlink target %q must be workspace-relative", value)
	}
	for _, part := range strings.Split(slashTarget, "/") {
		if part == ".." {
			return fmt.Errorf("symlink target %q must not contain ..", value)
		}
	}
	return nil
}

func validateSymlinkTarget(value string) error {
	return ValidateSymlinkTarget(value)
}

func IsGitControlPath(rel string) bool {
	for _, part := range strings.Split(slashPath(rel), "/") {
		if strings.EqualFold(part, ".git") {
			return true
		}
	}
	return false
}

func isGitControlPath(rel string) bool {
	return IsGitControlPath(rel)
}

func slashPath(value string) string {
	return strings.ReplaceAll(filepath.ToSlash(value), "\\", "/")
}

func hasWindowsVolumePrefix(value string) bool {
	if len(value) < 2 || value[1] != ':' {
		return false
	}
	c := value[0]
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
}

func containsControlCharacter(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}
