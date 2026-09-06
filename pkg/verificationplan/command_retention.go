package verificationplan

import (
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func failedWorkspaceRetentionScript() string {
	return `__preflight_available_kib() {
  __preflight_df_line="$(df -Pk "$1" 2>/dev/null | tail -n 1)" || return $?
  set -- $__preflight_df_line
  printf '%s\n' "${4:-}"
}
__preflight_has_failed_workspace_archive_space() {
  __preflight_available="$(__preflight_available_kib "$1" 2>/dev/null || true)"
  case "$__preflight_available" in
    ""|*[!0123456789]*) return 0 ;;
  esac
  [ "$__preflight_available" -ge 1048576 ]
}
__preflight_workspace="${READYCI_WORKSPACE_DIR:-$PWD}"
__preflight_failed_dir="$__preflight_workspace/.readyci"
__preflight_run_id="${READYCI_RUN_ID:-unknown}"
if [ $__preflight_status -eq 0 ]; then
  rm -f "$__preflight_failed_dir/failed-workspace.tar.gz"
elif [ $__preflight_status -ne 0 ]; then
  mkdir -p "$__preflight_failed_dir"
  rm -f "$__preflight_failed_dir/failed-workspace.tar.gz"
  if [ -z "${READYCI_CACHE_LANE_ID:-}" ] || [ ! -d /cache ]; then
    echo "readyci: failed workspace cache retention requested but no persistent cache is attached" >&2
  elif ! __preflight_has_failed_workspace_archive_space /cache; then
    echo "readyci: skipped failed workspace cache retention because /cache has less than 1 GiB available" >&2
  else
    __preflight_dest="/cache/failed-runs/$__preflight_run_id"
    __preflight_cache_tmp="$__preflight_dest/workspace.tar.gz.tmp"
    rm -rf "$__preflight_dest"
    mkdir -p "$__preflight_dest"
    rm -f "$__preflight_cache_tmp"
    if tar --exclude './.git' --exclude './.git/*' --exclude '*/.git' --exclude '*/.git/*' --exclude './.readyci/failed-workspace.tar.gz' -C "$__preflight_workspace" -czf "$__preflight_cache_tmp" . && mv "$__preflight_cache_tmp" "$__preflight_dest/workspace.tar.gz"; then
      echo "readyci: retained failed workspace at $__preflight_dest/workspace.tar.gz" >&2
    else
      rm -f "$__preflight_cache_tmp"
      echo "readyci: failed to retain workspace" >&2
    fi
  fi
  if ! __preflight_has_failed_workspace_archive_space "$__preflight_workspace"; then
    echo "readyci: skipped failed workspace artifact because the workspace filesystem has less than 1 GiB available" >&2
  else
    __preflight_failed_tmp="${TMPDIR:-/tmp}/readyci-failed-workspace-$__preflight_run_id.tar.gz"
    rm -f "$__preflight_failed_tmp"
    if tar --exclude './.git' --exclude './.git/*' --exclude '*/.git' --exclude '*/.git/*' --exclude './.readyci/failed-workspace.tar.gz' -C "$__preflight_workspace" -czf "$__preflight_failed_tmp" . && mv "$__preflight_failed_tmp" "$__preflight_failed_dir/failed-workspace.tar.gz"; then
      echo "` + preflightapi.FailedWorkspaceArtifactRetainedMessage + `" >&2
    else
      rm -f "$__preflight_failed_tmp"
      echo "readyci: failed to retain workspace artifact" >&2
    fi
  fi
fi
`
}
