package verificationplan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func cachePathSetupScript(cachePaths, cacheKeyFiles []string, requirements map[string]string, dependencyCheck string) (string, error) {
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
	requirements, err = preflightapi.NormalizeRuntimeRequirements(requirements)
	if err != nil {
		return "", err
	}
	// Incompatible runtime groups can reuse one physical lane. Put their
	// dependency files in different bases, so lockfile-generation pruning in
	// one group cannot delete another group's prepared dependencies either.
	runtimeNamespace := ""
	if len(requirements) > 0 {
		encoded, _ := json.Marshal(requirements)
		digest := sha256.Sum256(encoded)
		runtimeNamespace = "runtime-" + hex.EncodeToString(digest[:12]) + "-"
	}

	if dependencyCheck != "" {
		digest := sha256.Sum256([]byte(dependencyCheck))
		runtimeNamespace = "dependency-" + hex.EncodeToString(digest[:12]) + "-" + runtimeNamespace
	}

	var script strings.Builder
	script.WriteString(`__preflight_cache_attached() {
  [ -n "${READYCI_CACHE_LANE_ID:-}" ] && [ -d /cache ]
}
__preflight_cache_validate_mapping() (
  __preflight_path="$1"
  __preflight_target="$2"
  if [ -L "$__preflight_target" ] || { [ -e "$__preflight_target" ] && [ ! -d "$__preflight_target" ] && [ ! -f "$__preflight_target" ]; }; then
    return 65
  fi
  __preflight_parent="$(dirname "$__preflight_target")"
  while [ "$__preflight_parent" != / ]; do
    [ ! -L "$__preflight_parent" ] || return 65
    __preflight_parent="$(dirname "$__preflight_parent")"
  done
  __preflight_parent="$(dirname "$__preflight_path")"
  while [ "$__preflight_parent" != / ]; do
    if [ -L "$__preflight_parent" ]; then
      __preflight_cache_known_link "$__preflight_parent" || return 65
    fi
    __preflight_parent="$(dirname "$__preflight_parent")"
  done
  if [ -L "$__preflight_path" ]; then
    [ "$(readlink -- "$__preflight_path")" = "$__preflight_target" ] || return 65
  elif [ -e "$__preflight_path" ] && [ ! -d "$__preflight_path" ] && [ ! -f "$__preflight_path" ]; then
    return 65
  fi
)
__preflight_publish_cache_path() (
  __preflight_path="$1"
  __preflight_target="$2"
  __preflight_pending="$__preflight_target.readyci-pending"
  __preflight_stage="$__preflight_target.readyci-stage"
  __preflight_cache_validate_mapping "$__preflight_path" "$__preflight_target" || {
    __preflight_report_cache_path_error "$?" "publication validation" "$__preflight_path" "$__preflight_target"
    return $?
  }
  [ -d "$__preflight_pending" ] && [ ! -L "$__preflight_pending" ] || {
    __preflight_report_cache_path_error 65 "publication state" "$__preflight_path" "$__preflight_target"
    return $?
  }
  if [ -L "$__preflight_path" ]; then
    if [ ! -e "$__preflight_target" ]; then
      mkdir -- "$__preflight_target" || {
        __preflight_report_cache_path_error "$?" "publication deletion" "$__preflight_path" "$__preflight_target"
        return $?
      }
    fi
    return 0
  fi
  # The pending marker was synced before payload execution. A replacement is
  # authoritative, including deletions: never merge the old retained contents.
  mkdir -m 700 -- "$__preflight_stage" || {
    __preflight_report_cache_path_error "$?" "publication staging" "$__preflight_path" "$__preflight_target"
    return $?
  }
  rm -rf -- "$__preflight_target" || {
    __preflight_report_cache_path_error "$?" "publication invalidation" "$__preflight_path" "$__preflight_target"
    return $?
  }
  if [ -e "$__preflight_path" ]; then
    __preflight_before="$(stat -c '%d:%i:%f:%s:%y:%z' -- "$__preflight_path")" || return $?
    cp -a -- "$__preflight_path" "$__preflight_stage/data" || {
      __preflight_report_cache_path_error "$?" "publication copy" "$__preflight_path" "$__preflight_target"
      return $?
    }
    [ "$__preflight_before" = "$(stat -c '%d:%i:%f:%s:%y:%z' -- "$__preflight_path")" ] || {
      __preflight_report_cache_path_error 65 "publication source changed" "$__preflight_path" "$__preflight_target"
      return $?
    }
  else
    mkdir -- "$__preflight_stage/data" || {
      __preflight_report_cache_path_error "$?" "publication deletion" "$__preflight_path" "$__preflight_target"
      return $?
    }
  fi
  __preflight_cache_validate_mapping "$__preflight_path" "$__preflight_target" || return $?
  [ ! -L "$__preflight_stage/data" ] && { [ -d "$__preflight_stage/data" ] || [ -f "$__preflight_stage/data" ]; } || return 65
  mv -T -- "$__preflight_stage/data" "$__preflight_target" || {
    __preflight_report_cache_path_error "$?" "publication rename" "$__preflight_path" "$__preflight_target"
    return $?
  }
  rm -rf -- "$__preflight_path" || {
    __preflight_report_cache_path_error "$?" "publication replace" "$__preflight_path" "$__preflight_target"
    return $?
  }
  ln -s -- "$__preflight_target" "$__preflight_path" || {
    __preflight_report_cache_path_error "$?" "publication link" "$__preflight_path" "$__preflight_target"
    return $?
  }
  rmdir -- "$__preflight_stage" || return $?
)
__preflight_resolve_cache_path() {
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
  __preflight_path="$(realpath -m -s -- "$__preflight_path")" || return $?
  if [ -n "${__preflight_cache_key_suffix:-}" ]; then
    __preflight_base="/cache/paths/$__preflight_key"
    __preflight_target="$__preflight_base/$__preflight_cache_key_suffix"
  else
    __preflight_target="/cache/paths/$__preflight_key"
  fi
}
__preflight_link_cache_path() {
  __preflight_resolve_cache_path "$1" "$2" || return $?
  __preflight_cache_validate_mapping "$__preflight_path" "$__preflight_target" || {
    __preflight_report_cache_path_error "$?" "mapping validation" "$__preflight_path" "$__preflight_target"
    return $?
  }
  if [ -n "${__preflight_cache_key_suffix:-}" ]; then
    __preflight_prune_cache_path_generations "$__preflight_base" "$__preflight_cache_key_suffix" || return $?
  fi
  mkdir -p "$(dirname "$__preflight_target")" "$(dirname "$__preflight_path")" || {
    __preflight_report_cache_path_error "$?" "mkdir" "$__preflight_path" "$__preflight_target"
    return $?
  }
  __preflight_pending="$__preflight_target.readyci-pending"
  __preflight_stage="$__preflight_target.readyci-stage"
  if [ -e "$__preflight_pending" ] || [ -L "$__preflight_pending" ]; then
    [ -d "$__preflight_pending" ] && [ ! -L "$__preflight_pending" ] && [ ! -L "$__preflight_stage" ] || return 65
    # An interrupted/failed writer may have modified even an unchanged link.
    # Invalidate it rather than treating partial data as prepared dependencies.
    rm -rf -- "$__preflight_target" "$__preflight_stage" "$__preflight_pending" || return $?
  fi
  mkdir -m 700 -- "$__preflight_pending" || return $?
  sync -f -- "$__preflight_pending" || return $?
  if { [ -d "$__preflight_path" ] || [ -f "$__preflight_path" ]; } && [ ! -L "$__preflight_path" ]; then
    if [ ! -e "$__preflight_target" ] || { [ -d "$__preflight_target" ] && [ -z "$(find "$__preflight_target" -mindepth 1 -maxdepth 1 -print -quit)" ]; }; then
      __preflight_publish_cache_path "$__preflight_path" "$__preflight_target" || return $?
      return 0
    fi
  fi
  if [ ! -e "$__preflight_target" ]; then
    mkdir -- "$__preflight_target" || {
      __preflight_report_cache_path_error "$?" "mkdir" "$__preflight_path" "$__preflight_target"
      return $?
    }
  fi
  if [ -L "$__preflight_path" ]; then
    return 0
  fi
  rm -rf -- "$__preflight_path" || {
    __preflight_report_cache_path_error "$?" "replace" "$__preflight_path" "$__preflight_target"
    return $?
  }
  ln -s -- "$__preflight_target" "$__preflight_path" || {
    __preflight_report_cache_path_error "$?" "link" "$__preflight_path" "$__preflight_target"
    return $?
  }
}
__preflight_report_cache_path_error() (
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
)
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
  find "$__preflight_base" -mindepth 1 -maxdepth 1 \( -type d -o -type f \) ! -name "$__preflight_keep" ! -name "$__preflight_keep.readyci-pending" ! -name "$__preflight_keep.readyci-stage" -exec rm -rf -- {} + || return $?
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
  __preflight_cache_enabled=0
  if ! __preflight_cache_attached; then
    return 0
  fi
  __preflight_cache_enabled=1
`)
	script.WriteString("  __preflight_compute_cache_key")
	for _, keyFile := range keyFiles {
		script.WriteByte(' ')
		script.WriteString(ShellQuoteArg(keyFile))
	}
	script.WriteString(" || return $?\n")
	script.WriteString(`
`)
	// Resolve and validate every mapping before changing any retained or source
	// data. Runtime HOME/workspace expansion can alias otherwise distinct paths.
	for index, cachePath := range paths {
		script.WriteString("  __preflight_resolve_cache_path ")
		script.WriteString(ShellQuoteArg(runtimeNamespace + cachePathKey(cachePath)))
		script.WriteByte(' ')
		script.WriteString(ShellQuoteArg(cachePath))
		script.WriteString(" || return $?\n")
		fmt.Fprintf(&script, "  __preflight_cache_source_%d=\"$__preflight_path\"\n  __preflight_cache_target_%d=\"$__preflight_target\"\n", index, index)
	}
	for index := range paths {
		fmt.Fprintf(&script, "  __preflight_cache_validate_mapping \"$__preflight_cache_source_%d\" \"$__preflight_cache_target_%d\" || { __preflight_report_cache_path_error \"$?\" \"mapping validation\" \"$__preflight_cache_source_%d\" \"$__preflight_cache_target_%d\"; return $?; }\n", index, index, index, index)
		for other := 0; other < index; other++ {
			fmt.Fprintf(&script, "  case \"$__preflight_cache_source_%d/\" in \"$__preflight_cache_source_%d/\"*) __preflight_report_cache_path_error 65 \"overlapping mappings\" \"$__preflight_cache_source_%d\" \"$__preflight_cache_target_%d\"; return $? ;; esac\n", index, other, index, index)
			fmt.Fprintf(&script, "  case \"$__preflight_cache_source_%d/\" in \"$__preflight_cache_source_%d/\"*) __preflight_report_cache_path_error 65 \"overlapping mappings\" \"$__preflight_cache_source_%d\" \"$__preflight_cache_target_%d\"; return $? ;; esac\n", other, index, index, index)
		}
	}
	script.WriteString("  __preflight_prune_failed_run_cache || return $?\n")
	for _, cachePath := range paths {
		script.WriteString("  __preflight_link_cache_path ")
		script.WriteString(ShellQuoteArg(runtimeNamespace + cachePathKey(cachePath)))
		script.WriteByte(' ')
		script.WriteString(ShellQuoteArg(cachePath))
		script.WriteString(" || return $?\n")
	}

	script.WriteString("}\n__preflight_cache_known_link() {\n")
	for index := range paths {
		fmt.Fprintf(&script, "  if [ \"$1\" = \"${__preflight_cache_source_%d:-}\" ] && [ \"$(readlink -- \"$1\")\" = \"${__preflight_cache_target_%d:-}\" ]; then return 0; fi\n", index, index)
	}
	script.WriteString("  return 1\n}\n__preflight_publish_cache_paths() (\n  [ \"${__preflight_cache_enabled:-0}\" = 1 ] || return 0\n")
	for index := range paths {
		fmt.Fprintf(&script, "  __preflight_publish_cache_path \"$__preflight_cache_source_%d\" \"$__preflight_cache_target_%d\" || return $?\n", index, index)
	}
	script.WriteString("  return 0\n)\n__preflight_finalize_cache_paths() (\n  [ \"${__preflight_cache_enabled:-0}\" = 1 ] || return 0\n  __preflight_publish_cache_paths || return $?\n  sync -f -- /cache || return $?\n")
	for index := range paths {
		fmt.Fprintf(&script, "  rmdir -- \"$__preflight_cache_target_%d.readyci-pending\" || return $?\n", index)
	}
	script.WriteString("  sync -f -- /cache\n)\n")
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
