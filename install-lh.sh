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

if [ "$version" = "latest" ]; then
  api_url="https://api.github.com/repos/${repo}/releases/latest"
else
  api_url="https://api.github.com/repos/${repo}/releases/tags/${version}"
fi

archive_name="lh-${os}-${arch}.tar.gz"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

release_metadata="$(
  python3 - "$api_url" "$archive_name" <<'PY'
import json
import sys
import urllib.request

api_url = sys.argv[1]
archive_name = sys.argv[2]

req = urllib.request.Request(api_url, headers={"Accept": "application/vnd.github+json"})
with urllib.request.urlopen(req) as resp:
    data = json.load(resp)

for asset in data.get("assets", []):
    if asset.get("name") == archive_name:
        print(asset.get("browser_download_url", ""))
        print(data.get("tag_name", ""))
        break
PY
)"
download_url="$(printf '%s\n' "$release_metadata" | sed -n '1p')"
release_tag="$(printf '%s\n' "$release_metadata" | sed -n '2p')"

if [ -z "$download_url" ]; then
  echo "could not find release asset: $archive_name" >&2
  exit 1
fi

mkdir -p "$tmp_dir"
curl -fsSL -o "$tmp_dir/$archive_name" "$download_url"
tar -xzf "$tmp_dir/$archive_name" -C "$tmp_dir"
installer="$tmp_dir/install.sh"
if [ ! -x "$installer" ]; then
  echo "release asset is missing install.sh" >&2
  exit 1
fi
LH_INSTALL_PREFIX="$prefix" "$installer"
