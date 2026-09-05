package verificationplan

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

type commandPhases struct {
	Env     map[string]string
	Prepare []string
	Command string
	Steps   []preflightapi.RunStepSpec
	Post    []string
	Always  []string
}

// CommandForRunSpec assembles the single shell command a run executes,
// wrapping prepare/post/always phases and steps as needed.
func CommandForRunSpec(options RunSpec) (string, error) {
	if strings.TrimSpace(options.Command) != "" && len(options.Steps) > 0 {
		return strings.TrimSpace(options.Command), nil
	}
	return buildCommand(commandPhases{
		Env:     options.Env,
		Prepare: options.PrepareCommands,
		Command: options.Command,
		Steps:   options.Steps,
		Post:    options.PostCommands,
		Always:  options.AlwaysCommands,
	}, options.CachePaths, options.CacheKeyFiles, options.KeepFailed)
}

// CommandAndSteps decides whether a run carries structured steps, a single
// wrapped command, or both.
func CommandAndSteps(options RunSpec, command string) (string, []preflightapi.RunStepSpec) {
	steps := CloneRunSteps(options.Steps)
	if len(steps) == 0 {
		return command, nil
	}
	if runRequiresCommandWrapper(options) {
		return command, nil
	}
	if strings.TrimSpace(options.Command) != "" {
		if canonicalStepsOnlyCommand(command, steps) {
			return command, steps
		}
		return command, nil
	}
	return "", steps
}

func runRequiresCommandWrapper(options RunSpec) bool {
	return len(options.Env) > 0 ||
		len(trimCommandList(options.PrepareCommands)) > 0 ||
		len(trimCommandList(options.PostCommands)) > 0 ||
		len(trimCommandList(options.AlwaysCommands)) > 0 ||
		len(options.CachePaths) > 0 ||
		options.KeepFailed
}

func canonicalStepsOnlyCommand(command string, steps []preflightapi.RunStepSpec) bool {
	canonical, err := buildCommand(commandPhases{Steps: steps}, nil, nil, false)
	return err == nil && command == canonical
}

func buildCommand(phases commandPhases, cachePaths, cacheKeyFiles []string, keepFailed bool) (string, error) {
	phases.Prepare = trimCommandList(phases.Prepare)
	phases.Command = strings.TrimSpace(phases.Command)
	phases.Steps = normalizeRunSteps(phases.Steps)
	phases.Post = trimCommandList(phases.Post)
	phases.Always = trimCommandList(phases.Always)
	envScript := matrixCommandPrefix(phases.Env)
	cacheScript, err := cachePathSetupScript(cachePaths, cacheKeyFiles)
	if err != nil {
		return "", err
	}
	if cacheScript == "" && envScript == "" && !keepFailed && len(phases.Prepare) == 0 && len(phases.Steps) == 0 && len(phases.Post) == 0 && len(phases.Always) == 0 {
		return phases.Command, nil
	}

	var script strings.Builder
	writeRemoteLogPhaseMarkerFunction(&script)
	if cacheScript != "" {
		script.WriteString(cacheScript)
		script.WriteString("\n__preflight_setup_cache_paths\n")
		script.WriteString("__preflight_setup_status=$?\n")
		script.WriteString("if [ $__preflight_setup_status -ne 0 ]; then exit $__preflight_setup_status; fi\n")
	}
	if envScript != "" {
		script.WriteString(envScript)
	}
	if len(phases.Prepare) > 0 {
		writeCommandFunction(&script, "__preflight_prepare", phases.Prepare)
	}
	if len(phases.Steps) > 0 {
		for index, step := range phases.Steps {
			writeStepFunction(&script, fmt.Sprintf("__preflight_step_%d", index), step)
		}
	} else {
		writeCommandFunction(&script, "__preflight_command", []string{phases.Command})
	}
	if len(phases.Post) > 0 {
		writeCommandFunction(&script, "__preflight_post", phases.Post)
	}
	if len(phases.Always) > 0 {
		writeCommandFunction(&script, "__preflight_always", phases.Always)
	}
	script.WriteString("__preflight_run_phases() {\n")
	if len(phases.Prepare) > 0 {
		script.WriteString("  __readyci_log_phase prepare\n")
		script.WriteString("  __preflight_prepare\n")
		script.WriteString("  __preflight_status=$?\n")
		script.WriteString("  if [ $__preflight_status -ne 0 ]; then return $__preflight_status; fi\n")
	}
	script.WriteString("  __readyci_log_phase command\n")
	if len(phases.Steps) > 0 {
		for index, step := range phases.Steps {
			if step.Always {
				continue
			}
			writeStepInvocation(&script, fmt.Sprintf("__preflight_step_%d", index), step)
			script.WriteString("  __preflight_status=$?\n")
			script.WriteString("  if [ $__preflight_status -ne 0 ]; then return $__preflight_status; fi\n")
		}
	} else {
		script.WriteString("  __preflight_command\n")
		script.WriteString("  __preflight_status=$?\n")
		script.WriteString("  if [ $__preflight_status -ne 0 ]; then return $__preflight_status; fi\n")
	}
	if len(phases.Post) > 0 {
		script.WriteString("  __readyci_log_phase post\n")
		script.WriteString("  __preflight_post\n")
		script.WriteString("  __preflight_status=$?\n")
		script.WriteString("  if [ $__preflight_status -ne 0 ]; then return $__preflight_status; fi\n")
	}
	script.WriteString("  return 0\n")
	script.WriteString("}\n")
	writeCommandInvocation(&script, "__preflight_run_phases", "command")
	script.WriteString("__preflight_status=$?\n")
	if len(phases.Steps) > 0 {
		for index, step := range phases.Steps {
			if !step.Always {
				continue
			}
			script.WriteString("(\n")
			script.WriteString("__readyci_log_phase command\n")
			writeStepInvocation(&script, fmt.Sprintf("__preflight_step_%d", index), step)
			script.WriteString(")\n")
			script.WriteString("__preflight_always_step_status=$?\n")
			script.WriteString("if [ $__preflight_status -eq 0 ] && [ $__preflight_always_step_status -ne 0 ]; then __preflight_status=$__preflight_always_step_status; fi\n")
		}
	}
	if len(phases.Always) > 0 {
		writeCommandInvocation(&script, "__preflight_always", "always")
		script.WriteString("__preflight_always_status=$?\n")
		script.WriteString("if [ $__preflight_status -eq 0 ] && [ $__preflight_always_status -ne 0 ]; then __preflight_status=$__preflight_always_status; fi\n")
	}
	if keepFailed {
		script.WriteString(failedWorkspaceRetentionScript())
	}
	script.WriteString("exit $__preflight_status\n")
	return script.String(), nil
}

func normalizeRunSteps(steps []preflightapi.RunStepSpec) []preflightapi.RunStepSpec {
	out := make([]preflightapi.RunStepSpec, 0, len(steps))
	for _, step := range steps {
		step.Name = strings.TrimSpace(step.Name)
		step.Command = strings.TrimSpace(step.Command)
		if step.Name == "" || step.Command == "" {
			continue
		}
		step.Env = copyStringMap(step.Env)
		out = append(out, step)
	}
	return out
}

func trimCommandList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func writeRemoteLogPhaseMarkerFunction(script *strings.Builder) {
	script.WriteString("__readyci_log_phase() {\n")
	script.WriteString("  printf '\\036READYCI_PHASE:%s\\036\\n' \"$1\"\n")
	script.WriteString("  printf '\\036READYCI_PHASE:%s\\036\\n' \"$1\" >&2\n")
	script.WriteString("  __readyci_log_step ''\n")
	script.WriteString("}\n")
	script.WriteString("__readyci_log_step() {\n")
	script.WriteString("  printf '\\036READYCI_STEP:%s\\036\\n' \"$1\"\n")
	script.WriteString("  printf '\\036READYCI_STEP:%s\\036\\n' \"$1\" >&2\n")
	script.WriteString("}\n")
}

func writeCommandFunction(script *strings.Builder, label string, commands []string) {
	script.WriteString(label)
	script.WriteString("() {\n")
	for _, command := range commands {
		script.WriteString(command)
		script.WriteString("\n")
		script.WriteString("  __preflight_phase_status=$?\n")
		script.WriteString("  if [ $__preflight_phase_status -ne 0 ]; then return $__preflight_phase_status; fi\n")
	}
	script.WriteString("}\n")
}

func writeCommandInvocation(script *strings.Builder, label, phase string) {
	script.WriteString("(\n")
	script.WriteString("__readyci_log_phase ")
	script.WriteString(ShellQuoteArg(phase))
	script.WriteString("\n")
	script.WriteString(label)
	script.WriteString("\n)\n")
}

func writeStepFunction(script *strings.Builder, label string, step preflightapi.RunStepSpec) {
	script.WriteString(label)
	script.WriteString("() {\n")
	script.WriteString("  (\n")
	for _, key := range sortedStringMapKeys(step.Env) {
		script.WriteString("    ")
		script.WriteString(key)
		script.WriteString("=")
		script.WriteString(ShellQuoteArg(step.Env[key]))
		script.WriteString("\n")
		script.WriteString("    export ")
		script.WriteString(key)
		script.WriteString("\n")
	}
	if step.TimeoutSeconds > 0 {
		script.WriteString("    timeout ")
		script.WriteString(strconv.Itoa(step.TimeoutSeconds))
		script.WriteString("s /bin/bash -lc ")
		script.WriteString(ShellQuoteArg(step.Command))
		script.WriteString("\n")
	} else {
		script.WriteString("    /bin/bash -lc ")
		script.WriteString(ShellQuoteArg(step.Command))
		script.WriteString("\n")
	}
	script.WriteString("  )\n")
	script.WriteString("}\n")
}

func writeStepInvocation(script *strings.Builder, label string, step preflightapi.RunStepSpec) {
	script.WriteString("  __readyci_log_step ")
	script.WriteString(ShellQuoteArg(step.Name))
	script.WriteString("\n")
	script.WriteString("  ")
	script.WriteString(label)
	script.WriteString("\n")
}

func matrixCommandPrefix(env map[string]string) string {
	if len(env) == 0 {
		return ""
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+ShellQuoteArg(env[key]), "export "+key)
	}
	return strings.Join(parts, "\n") + "\n"
}

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

func sortedStringMapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// ShellQuoteArg quotes an argument for the generated shell script, leaving
// already-safe arguments unquoted.
func ShellQuoteArg(arg string) string {
	if arg == "" {
		return "''"
	}
	if isShellSafeArg(arg) {
		return arg
	}

	var b strings.Builder
	b.WriteByte('\'')
	for _, r := range arg {
		if r == '\'' {
			b.WriteString("'\\''")
			continue
		}
		b.WriteRune(r)
	}
	b.WriteByte('\'')
	return b.String()
}

func isShellSafeArg(arg string) bool {
	for _, r := range arg {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			continue
		}
		switch r {
		case '_', '@', '%', '+', '=', ':', ',', '.', '/', '-':
			continue
		default:
			return false
		}
	}
	return true
}
