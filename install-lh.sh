#!/usr/bin/env sh
set -eu

repo="${LH_REPO:-yurika0211/lucky-agent}"
repo_ref="${LH_REPO_REF:-}"
version="${1:-latest}"
prefix="${2:-${LH_INSTALL_PREFIX:-$HOME/.local}}"

os="$(uname | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"

case "$os" in
  linux|darwin) ;;
  *)
    echo "unsupported OS: $os" >&2
    exit 1
    ;;
esac

case "$arch" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *)
    echo "unsupported arch: $arch" >&2
    exit 1
    ;;
esac

archive_name="lh-${os}-${arch}.tar.gz"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

if [ "$version" = "latest" ]; then
  manifest_url="https://github.com/${repo}/releases/latest/download/update.json"
else
  case "$version" in
    v*) tag="$version" ;;
    *) tag="v$version" ;;
  esac
  manifest_url="https://github.com/${repo}/releases/download/${tag}/update.json"
fi

curl -fsSL -o "$tmp_dir/update.json" "$manifest_url"
release_metadata="$(
  python3 - "$tmp_dir/update.json" "$archive_name" <<'PY'
import json
import sys

manifest_path = sys.argv[1]
archive_name = sys.argv[2]

with open(manifest_path, encoding="utf-8") as fh:
    data = json.load(fh)

for asset in data.get("assets", []):
    if asset.get("name") == archive_name:
        print(asset.get("download_url", ""))
        print(data.get("tag_name", ""))
        print(asset.get("sha256", ""))
        break
PY
)"
download_url="$(printf '%s\n' "$release_metadata" | sed -n '1p')"
release_tag="$(printf '%s\n' "$release_metadata" | sed -n '2p')"
asset_sha="$(printf '%s\n' "$release_metadata" | sed -n '3p')"

if [ -z "$download_url" ]; then
  echo "could not find release asset: $archive_name" >&2
  exit 1
fi
if [ -z "$asset_sha" ]; then
  echo "release asset is missing sha256: $archive_name" >&2
  exit 1
fi

mkdir -p "$tmp_dir"
curl -fsSL -o "$tmp_dir/$archive_name" "$download_url"
# Python is already required for the manifest; unlike sha256sum it is also
# available on the supported macOS installation path. Hash before extraction.
python3 - "$tmp_dir/$archive_name" "$asset_sha" <<'PY'
import hashlib
import sys

digest = hashlib.sha256()
with open(sys.argv[1], "rb") as archive:
    for block in iter(lambda: archive.read(1024 * 1024), b""):
        digest.update(block)
if digest.hexdigest() != sys.argv[2].strip().lower():
    raise SystemExit("checksum mismatch for " + sys.argv[1])
PY
tar -xzf "$tmp_dir/$archive_name" -C "$tmp_dir"
installer="$tmp_dir/install.sh"
if [ ! -x "$installer" ]; then
  echo "release asset is missing install.sh" >&2
  exit 1
fi
LH_INSTALL_PREFIX="$prefix" "$installer"
