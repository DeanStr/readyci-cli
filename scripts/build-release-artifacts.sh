#!/usr/bin/env bash
set -euo pipefail

cli_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
repo_root="$(git -C "$cli_root" rev-parse --show-toplevel)"
release_smoke="${READYCI_RELEASE_SMOKE:-false}"
case "$release_smoke" in
  true | false) ;;
  *)
    echo "READYCI_RELEASE_SMOKE must be true or false" >&2
    exit 2
    ;;
esac
if [ "$release_smoke" != "true" ]; then
  if ! git -C "$repo_root" diff --quiet || ! git -C "$repo_root" diff --cached --quiet; then
    echo "refusing dirty release build; commit or stash changes first" >&2
    exit 2
  fi
  if [ -n "$(git -C "$repo_root" ls-files --others --exclude-standard)" ]; then
    echo "refusing dirty release build; untracked files are present" >&2
    exit 2
  fi
fi

version="${READYCI_RELEASE_VERSION:-${1:-}}"
if [ -z "$version" ]; then
  version="$(git -C "$repo_root" describe --tags --always --dirty 2>/dev/null || true)"
fi
if [ -z "$version" ]; then
  version="0.1.0-dev"
fi
semver_identifier='(0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)'
semver_regex="^v?(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)(-(${semver_identifier})(\\.(${semver_identifier}))*)?$"
if [[ ! "$version" =~ $semver_regex ]]; then
  echo "invalid release version: $version" >&2
  echo "expected vMAJOR.MINOR.PATCH[-PRERELEASE] or MAJOR.MINOR.PATCH[-PRERELEASE]" >&2
  exit 2
fi
commit="$(git -C "$repo_root" rev-parse --short=12 HEAD 2>/dev/null || true)"
if [ -z "$commit" ]; then
  commit="unknown"
fi
signing_key="${READYCI_RELEASE_SIGNING_KEY:-}"
require_signatures="${READYCI_RELEASE_REQUIRE_SIGNATURES:-false}"
minisign_public_key="${READYCI_RELEASE_MINISIGN_PUBLIC_KEY:-}"
case "$require_signatures" in
  true | false) ;;
  *)
    echo "READYCI_RELEASE_REQUIRE_SIGNATURES must be true or false" >&2
    exit 2
    ;;
esac
signing_enabled=false
if [ -n "$signing_key" ]; then
  if [ ! -r "$signing_key" ]; then
    echo "release signing key is not readable: $signing_key" >&2
    exit 2
  fi
  if ! command -v minisign >/dev/null 2>&1; then
    echo "minisign is required when READYCI_RELEASE_SIGNING_KEY is set" >&2
    exit 2
  fi
  signing_key="$(cd "$(dirname "$signing_key")" && pwd)/$(basename "$signing_key")"
  signing_enabled=true
elif [ "$require_signatures" = "true" ]; then
  echo "READYCI_RELEASE_SIGNING_KEY is required when signatures are required" >&2
  exit 2
fi
if [ "$signing_enabled" = "true" ] && [ -z "$minisign_public_key" ]; then
  echo "READYCI_RELEASE_MINISIGN_PUBLIC_KEY is required when READYCI_RELEASE_SIGNING_KEY is set" >&2
  exit 2
fi
if [ "$signing_enabled" = "true" ]; then
  if [[ ! "$minisign_public_key" =~ ^RW[A-Za-z0-9+/]{54}$ ]]; then
    echo "READYCI_RELEASE_MINISIGN_PUBLIC_KEY must be a valid Minisign public key when signing is enabled" >&2
    exit 2
  fi
elif [ -n "$minisign_public_key" ] &&
  [ "$minisign_public_key" != "__READYCI_MINISIGN_PUBLIC_KEY__" ] &&
  [[ ! "$minisign_public_key" =~ ^RW[A-Za-z0-9+/]{54}$ ]]; then
  echo "READYCI_RELEASE_MINISIGN_PUBLIC_KEY must be empty, the build placeholder, or a valid Minisign public key" >&2
  exit 2
fi
if [ "$signing_enabled" != "true" ] && [ "$minisign_public_key" != "__READYCI_MINISIGN_PUBLIC_KEY__" ]; then
  minisign_public_key=""
fi
escaped_minisign_public_key="$(printf '%s' "$minisign_public_key" | sed 's/[&|\\]/\\&/g')"

out_dir="${READYCI_RELEASE_OUT_DIR:-$repo_root/dist/readyci-cli-$version}"
mkdir -p "$out_dir"
out_dir="$(cd "$out_dir" && pwd)"
case "$out_dir" in
  "" | "/" | "$HOME" | "$repo_root")
    echo "refusing unsafe release output directory: $out_dir" >&2
    exit 2
    ;;
esac
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/readyci-cli-release.XXXXXX")"
package_root="$work_dir/packages"
artifact_root="$work_dir/artifacts"
targets=(
  linux/amd64
  linux/arm64
  darwin/amd64
  darwin/arm64
  windows/amd64
  windows/arm64
)
release_archives=()

if ! command -v zip >/dev/null 2>&1; then
  echo "zip is required to build Windows release archives" >&2
  exit 2
fi

archive_for_target() {
  local goos="$1"
  local goarch="$2"
  case "$goos" in
    windows) printf 'readyci_%s_%s.zip\n' "$goos" "$goarch" ;;
    *) printf 'readyci_%s_%s.tar.gz\n' "$goos" "$goarch" ;;
  esac
}

sha256_line() {
  local file="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$file"
    return
  fi
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$file"
    return
  fi
  echo "sha256sum or shasum is required" >&2
  exit 2
}

sha256_digest() {
  local file="$1"
  sha256_line "$file" | awk '{print $1}'
}

cleanup() {
  rm -rf "$work_dir"
}
trap cleanup EXIT

mkdir -p "$package_root" "$artifact_root"

for target in "${targets[@]}"; do
  goos="${target%/*}"
  goarch="${target#*/}"
  archive="$(archive_for_target "$goos" "$goarch")"
  release_archives+=("$archive")
  package_dir="$package_root/readyci_${goos}_${goarch}"
  mkdir -p "$package_dir"

  binary_name="readyci"
  if [ "$goos" = "windows" ]; then
    binary_name="readyci.exe"
  fi

  (
    cd "$cli_root"
    GOWORK=off GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 GOTOOLCHAIN=go1.25.13+auto \
      go build -trimpath -ldflags "-s -w -X main.version=$version -X main.releaseDefaultAPIURL=https://api.readyci.com" \
      -o "$package_dir/$binary_name" ./cmd/readyci
  )
  cp "$cli_root/README.md" "$package_dir/README.md"
  cp "$cli_root/docs/install.md" "$package_dir/INSTALL.md"
  cp "$cli_root/LICENSE" "$package_dir/LICENSE"
  cp "$cli_root/NOTICE" "$package_dir/NOTICE"
  cp "$cli_root/THIRD_PARTY_NOTICES" "$package_dir/THIRD_PARTY_NOTICES"
  cp "$cli_root/SECURITY.md" "$package_dir/SECURITY.md"
  chmod 0755 "$package_dir/$binary_name"
  chmod 0644 \
    "$package_dir/README.md" \
    "$package_dir/INSTALL.md" \
    "$package_dir/LICENSE" \
    "$package_dir/NOTICE" \
    "$package_dir/THIRD_PARTY_NOTICES" \
    "$package_dir/SECURITY.md"
  if [ "$goos" = "windows" ]; then
    (
      cd "$package_dir"
      zip -X -q "$artifact_root/$archive" "$binary_name" README.md INSTALL.md LICENSE NOTICE THIRD_PARTY_NOTICES SECURITY.md
    )
  else
    tar -C "$package_dir" -czf "$artifact_root/$archive" "$binary_name" README.md INSTALL.md LICENSE NOTICE THIRD_PARTY_NOTICES SECURITY.md
  fi
done

sed -e "s|^embedded_minisign_public_key=\"__READYCI_MINISIGN_PUBLIC_KEY__\"$|embedded_minisign_public_key=\"$escaped_minisign_public_key\"|" \
  -e 's|^version="${READYCI_VERSION:-latest}"$|version="${READYCI_VERSION:-'"$version"'}"|' \
  "$cli_root/scripts/install-readyci.sh" >"$artifact_root/install.sh"
chmod +x "$artifact_root/install.sh"
if ! grep -Fq 'if [ "$minisign_public_key" = "__READYCI_MINISIGN_PUBLIC_KEY__" ]; then' "$artifact_root/install.sh"; then
  echo "generated installer no longer preserves the minisign placeholder guard" >&2
  exit 2
fi
if [ -n "$minisign_public_key" ] && ! grep -Fq "embedded_minisign_public_key=\"$minisign_public_key\"" "$artifact_root/install.sh"; then
  echo "generated installer does not embed the configured minisign public key" >&2
  exit 2
fi
sed -e "s|^\$embeddedMinisignPublicKey = '__READYCI_MINISIGN_PUBLIC_KEY__'$|\$embeddedMinisignPublicKey = '$escaped_minisign_public_key'|" \
  -e "s|^\$version = if (\$env:READYCI_VERSION) { \$env:READYCI_VERSION } else { 'latest' }$|\$version = if (\$env:READYCI_VERSION) { \$env:READYCI_VERSION } else { '$version' }|" \
  "$cli_root/scripts/install-readyci.ps1" >"$artifact_root/install.ps1"
chmod 0644 "$artifact_root/install.ps1"
if ! grep -Fq "if (\$minisignPublicKey -eq '__READYCI_MINISIGN_PUBLIC_KEY__')" "$artifact_root/install.ps1"; then
  echo "generated PowerShell installer no longer preserves the minisign placeholder guard" >&2
  exit 2
fi
if [ -n "$minisign_public_key" ] && ! grep -Fq "\$embeddedMinisignPublicKey = '$minisign_public_key'" "$artifact_root/install.ps1"; then
  echo "generated PowerShell installer does not embed the configured minisign public key" >&2
  exit 2
fi

(
  cd "$artifact_root"
  : > SHA256SUMS
  for file in "${release_archives[@]}" install.sh install.ps1; do
    sha256_line "$file" >> SHA256SUMS
  done
)

{
  printf '{\n'
  printf '  "name": "readyci-cli",\n'
  printf '  "version": "%s",\n' "$version"
  printf '  "commit": "%s",\n' "$commit"
  printf '  "artifacts": [\n'
  first=1
  for target in "${targets[@]}"; do
    goos="${target%/*}"
    goarch="${target#*/}"
    archive="$(archive_for_target "$goos" "$goarch")"
    digest="$(awk -v file="$archive" '$2 == file {print $1}' "$artifact_root/SHA256SUMS")"
    if [ "$first" -eq 0 ]; then
      printf ',\n'
    fi
    first=0
    printf '    {"os": "%s", "arch": "%s", "file": "%s", "sha256": "%s"}' "$goos" "$goarch" "$archive" "$digest"
  done
  printf '\n  ]\n'
  printf '}\n'
} >"$artifact_root/manifest.json"

homebrew_template="$cli_root/homebrew/readyci.rb.template"
if [ -f "$homebrew_template" ]; then
  linux_amd64_sha="$(sha256_digest "$artifact_root/readyci_linux_amd64.tar.gz")"
  linux_arm64_sha="$(sha256_digest "$artifact_root/readyci_linux_arm64.tar.gz")"
  darwin_amd64_sha="$(sha256_digest "$artifact_root/readyci_darwin_amd64.tar.gz")"
  darwin_arm64_sha="$(sha256_digest "$artifact_root/readyci_darwin_arm64.tar.gz")"
  sed \
    -e "s/{{VERSION}}/$version/g" \
    -e "s/{{LINUX_AMD64_SHA256}}/$linux_amd64_sha/g" \
    -e "s/{{LINUX_ARM64_SHA256}}/$linux_arm64_sha/g" \
    -e "s/{{DARWIN_AMD64_SHA256}}/$darwin_amd64_sha/g" \
    -e "s/{{DARWIN_ARM64_SHA256}}/$darwin_arm64_sha/g" \
    "$homebrew_template" >"$artifact_root/readyci.rb"
fi

(
  cd "$artifact_root"
  : > SHA256SUMS
  for file in "${release_archives[@]}" install.sh install.ps1 manifest.json readyci.rb; do
    [ -f "$file" ] || continue
    sha256_line "$file" >> SHA256SUMS
  done
)

if [ "$signing_enabled" = "true" ]; then
  (
    cd "$artifact_root"
    for file in "${release_archives[@]}" SHA256SUMS manifest.json install.sh install.ps1 readyci.rb; do
      [ -f "$file" ] || continue
      minisign -Sm "$file" -s "$signing_key"
      if ! minisign -Vm "$file" -x "$file.minisig" -P "$minisign_public_key" >/dev/null; then
        echo "release signing key does not match READYCI_RELEASE_MINISIGN_PUBLIC_KEY" >&2
        exit 2
      fi
    done
  )
fi

mkdir -p "$out_dir"
find "$out_dir" -maxdepth 1 -type f \( \
  -name 'preflight_*.tar.gz' \
  -o -name 'readyci_*.tar.gz' \
  -o -name 'readyci_*.zip' \
  -o -name 'SHA256SUMS' \
  -o -name 'manifest.json' \
  -o -name 'install.sh' \
  -o -name 'install.ps1' \
  -o -name 'readyci.rb' \
  -o -name '*.minisig' \
\) -delete
cp "$artifact_root"/readyci_*.tar.gz "$out_dir"/
cp "$artifact_root"/readyci_*.zip "$out_dir"/
cp "$artifact_root/SHA256SUMS" "$artifact_root/manifest.json" "$artifact_root/install.sh" "$artifact_root/install.ps1" "$out_dir"/
if [ -f "$artifact_root/readyci.rb" ]; then
  cp "$artifact_root/readyci.rb" "$out_dir"/
fi
if compgen -G "$artifact_root/*.minisig" >/dev/null; then
  cp "$artifact_root"/*.minisig "$out_dir"/
fi

echo "release_artifacts=$out_dir"
echo "version=$version"
echo "commit=$commit"
echo "sha256sums=$out_dir/SHA256SUMS"
if [ "$signing_enabled" = "true" ]; then
  echo "signatures=minisign"
fi
