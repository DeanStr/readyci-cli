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

// ValidateSymlinkTargetSyntax checks the representation of a relative symlink
// target. Containment depends on the complete entry namespace, including any
// symlink hops before a parent component; ValidateManifest performs that check.
func ValidateSymlinkTargetSyntax(value string) error {
	// Linux symlink contents must fit in one page, including their terminator.
	if len(value) > 4095 {
		return fmt.Errorf("symlink target exceeds 4095 bytes supported by the runner")
	}
	if containsControlCharacter(value) {
		return fmt.Errorf("symlink target %q contains control character", value)
	}
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("symlink target is empty")
	}
	target := value
	if strings.Contains(target, "\\") {
		return fmt.Errorf("symlink target %q contains backslash", value)
	}
	slashTarget := filepath.ToSlash(target)
	if path.IsAbs(slashTarget) || filepath.IsAbs(target) || hasWindowsVolumePrefix(slashTarget) {
		return fmt.Errorf("symlink target %q must be workspace-relative", value)
	}
	return nil
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
