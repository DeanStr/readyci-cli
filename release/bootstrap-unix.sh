#!/usr/bin/env bash
# HTTPS bootstrap for the pinned, independently signed Ready CI release.
# Keep the release pin and key aligned with cli/docs/install.md.
(
  set -euo pipefail
  command -v minisign >/dev/null || { echo 'Install Minisign, then retry: https://jedisct1.github.io/minisign/' >&2; exit 2; }
  export READYCI_VERSION='v0.1.0-beta.14'
  export READYCI_RELEASE_BASE_URL="https://github.com/DeanStr/readyci-cli/releases/download/$READYCI_VERSION"
  export READYCI_MINISIGN_PUBLIC_KEY='RWSiWC6F4tE+M458zvmqGzOcQZvYD/Y6ww1/eo3Q88S+d3HuiXGK5wAE'
  unset READYCI_ALLOW_UNSIGNED_INSTALL
  installer_dir="$(mktemp -d)"
  trap 'rm -rf "$installer_dir"' EXIT
  curl -fsSLo "$installer_dir/install.sh" "$READYCI_RELEASE_BASE_URL/install.sh"
  curl -fsSLo "$installer_dir/install.sh.minisig" "$READYCI_RELEASE_BASE_URL/install.sh.minisig"
  minisign -Vm "$installer_dir/install.sh" -x "$installer_dir/install.sh.minisig" -P "$READYCI_MINISIGN_PUBLIC_KEY"
  bash "$installer_dir/install.sh"
  printf '\nOpen your repository and run readyci setup.\n'
  printf 'If readyci is not found, add the install directory to PATH: %s\n' "${READYCI_INSTALL_DIR:-$HOME/.local/bin}"
)
