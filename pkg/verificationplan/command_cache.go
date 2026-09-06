package verificationplan

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"strings"
)

func cachePathSetupScript(cachePaths, cacheKeyFiles []string) (string, error) {
	paths, err := NormalizeCachePaths(cachePaths)
	if err != nil {
		return "", err
	}
	keyFiles, err := NormalizeCacheKeyFiles(cacheKeyFiles)
	if err != nil {
		return "", err
	}
	if len(paths) == 0 {
		return "", nil
	}

	var script strings.Builder
	script.WriteString(`__preflight_cache_attached() {
  [ -n "${READYCI_CACHE_LANE_ID:-}" ] && [ -d /cache ]
}
__preflight_link_cache_path() {
  __preflight_key="$1"
  __preflight_path="$2"
  __preflight_workspace="${READYCI_WORKSPACE_DIR:-$PWD}"
  if ! __preflight_cache_attached; then
    return 0
  fi
  case "$__preflight_path" in
    "~/"*) __preflight_path="$HOME/${__preflight_path#\~/}" ;;
    /*) ;;
    *) __preflight_path="$__preflight_workspace/$__preflight_path" ;;
  esac
  if [ -n "${__preflight_cache_key_suffix:-}" ]; then
    __preflight_base="/cache/paths/$__preflight_key"
    __preflight_prune_cache_path_generations "$__preflight_base" "$__preflight_cache_key_suffix" || return $?
    __preflight_target="$__preflight_base/$__preflight_cache_key_suffix"
  else
    __preflight_target="/cache/paths/$__preflight_key"
  fi
  mkdir -p "$__preflight_target" "$(dirname "$__preflight_path")" || {
    __preflight_status=$?
    __preflight_report_cache_path_error "$__preflight_status" "mkdir" "$__preflight_path" "$__preflight_target"
    return $?
  }
  if [ -d "$__preflight_path" ] && [ ! -L "$__preflight_path" ] && [ -z "$(find "$__preflight_target" -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null)" ]; then
    cp -a "$__preflight_path"/. "$__preflight_target"/ || {
      __preflight_status=$?
      __preflight_report_cache_path_error "$__preflight_status" "copy" "$__preflight_path" "$__preflight_target"
      return $?
    }
  fi
  rm -rf "$__preflight_path" || {
    __preflight_status=$?
    __preflight_report_cache_path_error "$__preflight_status" "replace" "$__preflight_path" "$__preflight_target"
    return $?
  }
  ln -s "$__preflight_target" "$__preflight_path" || {
    __preflight_status=$?
    __preflight_report_cache_path_error "$__preflight_status" "link" "$__preflight_path" "$__preflight_target"
    return $?
  }
}
__preflight_report_cache_path_error() {
  __preflight_status="$1"
  __preflight_operation="$2"
  __preflight_path="$3"
  __preflight_target="$4"
  echo "readyci: cache path setup failed during $__preflight_operation for $__preflight_path -> $__preflight_target (exit $__preflight_status)" >&2
  if [ -d /cache ]; then
    echo "readyci: cache filesystem usage:" >&2
    df -h /cache >&2 || true
  fi
  return "$__preflight_status"
}
__preflight_prune_failed_run_cache() {
  if ! __preflight_cache_attached || [ ! -d /cache/failed-runs ]; then
    return 0
  fi
  find /cache/failed-runs -mindepth 1 -maxdepth 1 -exec rm -rf -- {} + || return $?
}
__preflight_prune_cache_path_generations() {
  __preflight_base="$1"
  __preflight_keep="$2"
  if [ -z "$__preflight_keep" ] || [ ! -d "$__preflight_base" ]; then
    return 0
  fi
  case "$__preflight_keep" in
    *[!0123456789abcdef]*) return 0 ;;
  esac
  find "$__preflight_base" -mindepth 1 -maxdepth 1 -type d ! -name "$__preflight_keep" -exec rm -rf -- {} + || return $?
}
__preflight_compute_cache_key() {
  __preflight_cache_key_suffix=""
  if [ "$#" -eq 0 ]; then
    return 0
  fi
  if ! command -v sha256sum >/dev/null 2>&1; then
    echo "readyci: cache.key_files requires sha256sum in the runner image" >&2
    return 69
  fi
  __preflight_workspace="${READYCI_WORKSPACE_DIR:-$PWD}"
  __preflight_key_tmp="${TMPDIR:-/tmp}/readyci-cache-key-$$"
  rm -f "$__preflight_key_tmp" || return $?
  for __preflight_key_file in "$@"; do
    case "$__preflight_key_file" in
      "~/"*) __preflight_resolved_key_file="$HOME/${__preflight_key_file#\~/}" ;;
      /*) __preflight_resolved_key_file="$__preflight_key_file" ;;
      *) __preflight_resolved_key_file="$__preflight_workspace/$__preflight_key_file" ;;
    esac
    if [ -e "$__preflight_resolved_key_file" ]; then
      __preflight_file_digest="$(sha256sum "$__preflight_resolved_key_file")" || return $?
      __preflight_file_digest="${__preflight_file_digest%% *}"
      if [ -z "$__preflight_file_digest" ]; then
        echo "readyci: cache.key_files produced an empty digest for $__preflight_key_file" >&2
        return 69
      fi
      printf 'file %s %s\n' "$__preflight_key_file" "$__preflight_file_digest" >> "$__preflight_key_tmp" || return $?
    else
      printf 'missing %s\n' "$__preflight_key_file" >> "$__preflight_key_tmp" || return $?
    fi
  done
  __preflight_cache_key_digest="$(sha256sum "$__preflight_key_tmp")" || {
    __preflight_hash_status=$?
    rm -f "$__preflight_key_tmp" >/dev/null 2>&1 || true
    return $__preflight_hash_status
  }
  __preflight_cache_key_suffix="${__preflight_cache_key_digest%% *}"
  if [ -z "$__preflight_cache_key_suffix" ]; then
    echo "readyci: cache.key_files produced an empty cache key" >&2
    rm -f "$__preflight_key_tmp" >/dev/null 2>&1 || true
    return 69
  fi
  rm -f "$__preflight_key_tmp" || return $?
}
__preflight_setup_cache_paths() {
  if ! __preflight_cache_attached; then
    return 0
  fi
  __preflight_prune_failed_run_cache || return $?
`)
	script.WriteString("  __preflight_compute_cache_key")
	for _, keyFile := range keyFiles {
		script.WriteByte(' ')
		script.WriteString(ShellQuoteArg(keyFile))
	}
	script.WriteString(" || return $?\n")
	script.WriteString(`
`)
	for _, cachePath := range paths {
		script.WriteString("  __preflight_link_cache_path ")
		script.WriteString(ShellQuoteArg(cachePathKey(cachePath)))
		script.WriteByte(' ')
		script.WriteString(ShellQuoteArg(cachePath))
		script.WriteString(" || return $?\n")
	}
	script.WriteString("}\n")
	return script.String(), nil
}

// NormalizeCacheKeyFiles validates and canonicalizes cache key files.
func NormalizeCacheKeyFiles(values []string) ([]string, error) {
	seen := make(map[string]struct{})
	paths := make([]string, 0, len(values))
	for _, value := range values {
		normalized, err := normalizeCacheKeyFile(value)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		paths = append(paths, normalized)
	}
	return paths, nil
}

func normalizeCacheKeyFile(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("cache key file cannot be empty")
	}
	if strings.ContainsAny(value, "\x00\r\n") {
		return "", fmt.Errorf("cache key file %q contains a control character", value)
	}
	if strings.HasPrefix(value, "~/") || strings.HasPrefix(value, "/") {
		return "", fmt.Errorf("cache key file %q must be workspace-relative", value)
	}
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("cache key file %q escapes the workspace", value)
	}
	if cleaned == ".git" || strings.HasPrefix(cleaned, ".git/") {
		return "", fmt.Errorf("cache key file %q targets git metadata", value)
	}
	return cleaned, nil
}

// NormalizeCachePaths validates and canonicalizes cache paths.
func NormalizeCachePaths(values []string) ([]string, error) {
	seen := make(map[string]struct{})
	paths := make([]string, 0, len(values))
	for _, value := range values {
		normalized, err := normalizeCachePath(value)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		paths = append(paths, normalized)
	}
	return paths, nil
}

func normalizeCachePath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("cache path cannot be empty")
	}
	if strings.ContainsAny(value, "\x00\r\n") {
		return "", fmt.Errorf("cache path %q contains a control character", value)
	}
	if strings.HasPrefix(value, "~/") {
		rest := path.Clean(strings.TrimPrefix(value, "~/"))
		if rest == "." || rest == ".." || strings.HasPrefix(rest, "../") {
			return "", fmt.Errorf("cache path %q escapes home", value)
		}
		if rest == ".git" || strings.HasPrefix(rest, ".git/") {
			return "", fmt.Errorf("cache path %q targets git metadata", value)
		}
		return "~/" + rest, nil
	}
	if strings.HasPrefix(value, "/") {
		cleaned := path.Clean(value)
		if cleaned == "/" || cleaned == "/workspace" || cleaned == "/cache" || strings.HasPrefix(cleaned, "/cache/") {
			return "", fmt.Errorf("cache path %q cannot be mapped", value)
		}
		return cleaned, nil
	}
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("cache path %q escapes the workspace", value)
	}
	if cleaned == ".git" || strings.HasPrefix(cleaned, ".git/") {
		return "", fmt.Errorf("cache path %q targets git metadata", value)
	}
	return cleaned, nil
}

func cachePathKey(value string) string {
	sum := sha256.Sum256([]byte(value))
	var label strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			label.WriteRune(r)
		default:
			label.WriteByte('-')
		}
	}
	trimmed := strings.Trim(label.String(), "-")
	if trimmed == "" {
		trimmed = "path"
	}
	if len(trimmed) > 48 {
		trimmed = trimmed[:48]
	}
	return trimmed + "-" + hex.EncodeToString(sum[:8])
}
