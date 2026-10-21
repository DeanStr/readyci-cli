__readyci_log_phase() {
  printf '\036READYCI_PHASE:%s\036\n' "$1"
  printf '\036READYCI_PHASE:%s\036\n' "$1" >&2
  __readyci_log_step ''
}
__readyci_log_step() {
  printf '\036READYCI_STEP:%s\036\n' "$1"
  printf '\036READYCI_STEP:%s\036\n' "$1" >&2
}
__preflight_cache_attached() {
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
  __preflight_compute_cache_key || return $?

  __preflight_link_cache_path runtime-2a2d2990393e800d6f31bddb-npm-82ec3afe4d98ab1a '~/.npm' || return $?
  __preflight_link_cache_path runtime-2a2d2990393e800d6f31bddb-cache-yarn-1763e51e508324bc '~/.cache/yarn' || return $?
  __preflight_link_cache_path runtime-2a2d2990393e800d6f31bddb-local-share-pnpm-store-73dcc88b60c03d6f '~/.local/share/pnpm/store' || return $?
}

__preflight_setup_cache_paths
__preflight_setup_status=$?
if [ $__preflight_setup_status -ne 0 ]; then exit $__preflight_setup_status; fi
__preflight_step_0() {
  (
    CI=true
    export CI
    PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1
    export PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD
    /bin/bash --noprofile --norc -c 'bash -e -c '\''pnpm install --frozen-lockfile'\'''
  )
}
__preflight_step_1() {
  (
    CI=true
    export CI
    PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1
    export PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD
    /bin/bash --noprofile --norc -c 'bash -e -c '\''pnpm playwright install chromium'\'''
  )
}
__preflight_step_2() {
  (
    CI=true
    export CI
    PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1
    export PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD
    /bin/bash --noprofile --norc -c 'bash -e -c '\''pnpm test'\'''
  )
}
__preflight_run_phases() {
  __readyci_log_phase command
  __readyci_log_step step-1
  __preflight_step_0
  __preflight_status=$?
  if [ $__preflight_status -ne 0 ]; then return $__preflight_status; fi
  __readyci_log_step step-2
  __preflight_step_1
  __preflight_status=$?
  if [ $__preflight_status -ne 0 ]; then return $__preflight_status; fi
  __readyci_log_step step-3
  __preflight_step_2
  __preflight_status=$?
  if [ $__preflight_status -ne 0 ]; then return $__preflight_status; fi
  return 0
}
(
__readyci_log_phase command
__preflight_run_phases
)
__preflight_status=$?
exit $__preflight_status
