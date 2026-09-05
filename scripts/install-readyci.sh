#!/usr/bin/env bash
set -euo pipefail

version="${READYCI_VERSION:-latest}"
install_dir="${READYCI_INSTALL_DIR:-$HOME/.local/bin}"
base_url="${READYCI_RELEASE_BASE_URL:-}"
embedded_minisign_public_key="__READYCI_MINISIGN_PUBLIC_KEY__"
minisign_public_key="${READYCI_MINISIGN_PUBLIC_KEY:-$embedded_minisign_public_key}"
allow_unsigned_install="${READYCI_ALLOW_UNSIGNED_INSTALL:-}"
if [ "$minisign_public_key" = "__READYCI_MINISIGN_PUBLIC_KEY__" ]; then
  minisign_public_key=""
fi

detect_os() {
  case "$(uname -s)" in
    Linux) printf 'linux\n' ;;
    Darwin) printf 'darwin\n' ;;
    *) echo "unsupported OS: $(uname -s)" >&2; exit 2 ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64 | amd64) printf 'amd64\n' ;;
    arm64 | aarch64) printf 'arm64\n' ;;
    *) echo "unsupported architecture: $(uname -m)" >&2; exit 2 ;;
  esac
}

download() {
  local url="$1"
  local dest="$2"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$url" -o "$dest"
    return
  fi
  if command -v wget >/dev/null 2>&1; then
    wget -qO "$dest" "$url"
    return
  fi
  echo "curl or wget is required" >&2
  exit 2
}

sha256_file() {
  local file="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$file" | awk '{print $1}'
    return
  fi
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$file" | awk '{print $1}'
    return
  fi
  echo "sha256sum or shasum is required" >&2
  exit 2
}

if [ -z "$base_url" ]; then
  if [ "$version" = "latest" ]; then
    base_url="https://github.com/DeanStr/readyci-cli/releases/latest/download"
  else
    base_url="https://github.com/DeanStr/readyci-cli/releases/download/$version"
  fi
fi
base_url="${base_url%/}"

os="$(detect_os)"
arch="$(detect_arch)"
archive="readyci_${os}_${arch}.tar.gz"
tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/readyci-cli.XXXXXX")"
cleanup() {
  rm -rf "$tmp_dir"
}
trap cleanup EXIT

download "$base_url/$archive" "$tmp_dir/$archive"
download "$base_url/SHA256SUMS" "$tmp_dir/SHA256SUMS"

if [ -z "$minisign_public_key" ]; then
  if [ "$allow_unsigned_install" != "1" ]; then
    echo "Ready CI installer signature verification is not configured." >&2
    echo "Use the generated release install.sh with an embedded minisign key, set READYCI_MINISIGN_PUBLIC_KEY, or set READYCI_ALLOW_UNSIGNED_INSTALL=1 to accept checksum-only installation." >&2
    exit 2
  fi
  echo "warning: Ready CI installer is running without signature verification; SHA256SUMS is trusted from the same origin as the archive" >&2
else
  download "$base_url/SHA256SUMS.minisig" "$tmp_dir/SHA256SUMS.minisig"
  if ! command -v minisign >/dev/null 2>&1; then
    echo "minisign is required to verify SHA256SUMS" >&2
    exit 2
  fi
  minisign -Vm "$tmp_dir/SHA256SUMS" -x "$tmp_dir/SHA256SUMS.minisig" -P "$minisign_public_key" >/dev/null
fi

expected="$(awk -v file="$archive" '$2 == file {print $1}' "$tmp_dir/SHA256SUMS")"
if [ -z "$expected" ]; then
  echo "checksum for $archive not found in SHA256SUMS" >&2
  exit 1
fi
actual="$(sha256_file "$tmp_dir/$archive")"
if [ "$actual" != "$expected" ]; then
  echo "checksum mismatch for $archive" >&2
  exit 1
fi

tar -xzf "$tmp_dir/$archive" -C "$tmp_dir"
mkdir -p "$install_dir"
install -m 0755 "$tmp_dir/readyci" "$install_dir/readyci"

echo "installed readyci to $install_dir/readyci"
echo "version: $("$install_dir/readyci" version)"
