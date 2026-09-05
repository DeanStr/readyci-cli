package main

func readyCIShellSyntaxHelperScript() string {
	return `#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$repo_root"

declare -A candidates=()
add_candidate() {
  local file="$1"
  [[ -n "$file" ]] || return 0
  [[ "$file" == *.sh || "$file" == scripts/git-hooks/* ]] || return 0
  candidates["$file"]=1
}

if [[ -n "${CHECK_TOUCHED_FILES_FROM:-}" && -r "${CHECK_TOUCHED_FILES_FROM:-}" ]]; then
  while IFS= read -r file; do
    add_candidate "$file"
  done < "$CHECK_TOUCHED_FILES_FROM"
else
  while IFS= read -r -d '' file; do
    add_candidate "$file"
  done < <(git ls-files -z -- '*.sh' 'scripts/git-hooks/*')
  while IFS= read -r -d '' file; do
    add_candidate "$file"
  done < <(git ls-files -z --others --exclude-standard -- '*.sh' 'scripts/git-hooks/*')
fi

if ((${#candidates[@]} == 0)); then
  echo "[readyci-shell-syntax] no shell files to check"
  exit 0
fi

status=0
while IFS= read -r -d '' file; do
  [[ -f "$file" ]] || continue
  echo "[readyci-shell-syntax] bash -n -- $file"
  bash -n -- "$file" || status=$?
done < <(printf '%s\0' "${!candidates[@]}" | sort -z)
exit "$status"
`
}

func readyCIOpsConfigHelperScript() string {
	return `#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$repo_root"

failures=0
fail() {
  echo "[readyci-ops-config] $*"
  failures=$((failures + 1))
}

declare -a roots=()
for candidate in deploy ops infra .github/workflows; do
  [[ -e "$candidate" ]] && roots+=("$candidate")
done

declare -a root_ops_files=()
for candidate in docker-compose*.yml docker-compose*.yaml compose.yml compose.yaml Caddyfile; do
  [[ -f "$candidate" ]] && root_ops_files+=("$candidate")
done

if ((${#roots[@]} == 0 && ${#root_ops_files[@]} == 0)); then
  echo "[readyci-ops-config] no common ops/config directories found"
  exit 0
fi

echo "[readyci-ops-config] checking shell-like env examples"
if ((${#roots[@]} > 0)); then
  while IFS= read -r -d '' file; do
    bash -n -- "$file" || fail "invalid shell syntax in $file"
  done < <(find "${roots[@]}" -type f \( -name "*.env" -o -name "*.env.example" \) -print0 2>/dev/null | sort -z)
fi

echo "[readyci-ops-config] checking YAML"
declare -a yaml_files=()
if ((${#roots[@]} > 0)); then
  while IFS= read -r -d '' file; do
    yaml_files+=("$file")
  done < <(find "${roots[@]}" -type f \( -name "*.yaml" -o -name "*.yml" \) -print0 2>/dev/null | sort -z)
fi
for file in "${root_ops_files[@]}"; do
  case "$file" in
    *.yaml|*.yml) yaml_files+=("$file") ;;
  esac
done
if ((${#yaml_files[@]} > 0)); then
  python3 - <<'PY' "${yaml_files[@]}" || fail "invalid YAML"
from pathlib import Path
import sys

try:
    import yaml
except Exception as exc:
    print(f"[readyci-ops-config] PyYAML is required to validate YAML: {exc}", file=sys.stderr)
    raise SystemExit(1)

failed = False
for raw_path in sys.argv[1:]:
    path = Path(raw_path)
    if not path.is_file():
        continue
    try:
        yaml.safe_load(path.read_text(encoding="utf-8"))
    except Exception as exc:
        print(f"[readyci-ops-config] {path}: {exc}", file=sys.stderr)
        failed = True
raise SystemExit(1 if failed else 0)
PY
fi

echo "[readyci-ops-config] checking systemd unit shape"
if [[ -d deploy ]]; then
  python3 - <<'PY' || fail "invalid systemd unit shape"
from pathlib import Path
import sys

failed = False
for path in sorted(Path("deploy").glob("**/*")):
    if not path.is_file() or path.suffix not in {".service", ".timer"}:
        continue
    text = path.read_text(encoding="utf-8")
    required = ["[Unit]"]
    if path.suffix == ".service":
        required.extend(["[Service]", "ExecStart="])
    if path.suffix == ".timer":
        required.append("[Timer]")
        if not any(marker in text for marker in ("OnCalendar=", "OnActiveSec=", "OnBootSec=", "OnStartupSec=", "OnUnitActiveSec=", "OnUnitInactiveSec=")):
            print(f"[readyci-ops-config] {path}: missing timer trigger", file=sys.stderr)
            failed = True
    for marker in required:
        if marker not in text:
            print(f"[readyci-ops-config] {path}: missing {marker}", file=sys.stderr)
            failed = True
raise SystemExit(1 if failed else 0)
PY
fi

echo "[readyci-ops-config] checking Caddyfile"
declare -a caddy_files=()
if ((${#roots[@]} > 0)); then
  while IFS= read -r -d '' file; do
    caddy_files+=("$file")
  done < <(find "${roots[@]}" -type f -name "Caddyfile" -print0 2>/dev/null | sort -z)
fi
for file in "${root_ops_files[@]}"; do
  [[ "$file" == Caddyfile ]] && caddy_files+=("$file")
done
if ((${#caddy_files[@]} > 0)); then
  while IFS= read -r -d '' file; do
    if command -v caddy >/dev/null 2>&1; then
      caddy validate --config "$file" || fail "invalid Caddyfile: $file"
    else
      python3 - "$file" <<'PY' || fail "Caddyfile brace check failed"
from pathlib import Path
import sys

path = Path(sys.argv[1])
balance = 0
for line_no, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
    stripped = line.split("#", 1)[0]
    balance += stripped.count("{")
    balance -= stripped.count("}")
    if balance < 0:
        print(f"[readyci-ops-config] {path}:{line_no}: unexpected closing brace", file=sys.stderr)
        raise SystemExit(1)
if balance != 0:
    print(f"[readyci-ops-config] {path}: unbalanced braces", file=sys.stderr)
    raise SystemExit(1)
PY
    fi
  done < <(printf '%s\0' "${caddy_files[@]}" | sort -z)
fi

if ((failures > 0)); then
  echo "[readyci-ops-config] FAILED with $failures issue(s)"
  exit 1
fi
echo "[readyci-ops-config] OK"
`
}
