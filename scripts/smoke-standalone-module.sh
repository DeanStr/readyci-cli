#!/usr/bin/env bash
set -euo pipefail

cli_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/readyci-cli-module.XXXXXX")"
cleanup() {
  rm -rf "$work_dir"
}
trap cleanup EXIT

cp -a "$cli_root" "$work_dir/cli"

if grep -Eq '^[[:space:]]*(replace|exclude)[[:space:]]' "$work_dir/cli/go.mod"; then
  echo "public CLI module must not contain replace or exclude directives" >&2
  exit 1
fi

GOWORK=off GOTOOLCHAIN=go1.25.13+auto go -C "$work_dir/cli" list ./... >/dev/null
GOWORK=off GOTOOLCHAIN=go1.25.13+auto go -C "$work_dir/cli" test ./...
