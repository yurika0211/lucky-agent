#!/usr/bin/env sh
set -eu

repo="${LH_REPO:-yurika0211/luckyharness}"
repo_ref="${LH_REPO_REF:-}"
version="${1:-latest}"
prefix="${2:-$HOME/.local/bin}"

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

archive_name="la-${os}-${arch}.tar.gz"
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
mkdir -p "$prefix"
install -m 0755 "$tmp_dir/la" "$prefix/la"

echo "installed la to $prefix/la"

if [ ! -d "$tmp_dir/UI" ]; then
  source_ref="${repo_ref:-$release_tag}"
  if [ -z "$source_ref" ]; then
    source_ref="main"
  fi
  repo_archive="$tmp_dir/repo-${source_ref}.tar.gz"
  repo_dir="$tmp_dir/repo"
  if curl -fsSL -o "$repo_archive" "https://github.com/${repo}/archive/refs/tags/${source_ref}.tar.gz" ||
    curl -fsSL -o "$repo_archive" "https://github.com/${repo}/archive/refs/heads/${source_ref}.tar.gz"; then
    mkdir -p "$repo_dir"
    tar -xzf "$repo_archive" -C "$repo_dir"
    ui_index="$(find "$repo_dir" -path '*/UI/TUI/src/index.tsx' -print | head -n 1 || true)"
    if [ -n "$ui_index" ]; then
      ui_source="$(dirname "$(dirname "$(dirname "$ui_index")")")"
      cp -R "$ui_source" "$tmp_dir/UI"
    fi
  fi
fi

if [ -d "$tmp_dir/UI" ]; then
  ui_dir="${LH_UI_INSTALL_DIR:-$HOME/.local/share/luckyharness/UI}"
  mkdir -p "$(dirname "$ui_dir")"
  rm -rf "$ui_dir"
  cp -R "$tmp_dir/UI" "$ui_dir"

  if command -v npm >/dev/null 2>&1; then
    (
      cd "$ui_dir"
      npm ci --silent --omit=optional
    )
  else
    echo "warning: npm was not found; install Node.js/npm before running la tui" >&2
  fi

  mkdir -p "$HOME/.luckyharness/runtime"
  printf '%s\n' "$ui_dir" > "$HOME/.luckyharness/runtime/tui-ui-dir"
  echo "installed TUI files to $ui_dir"
fi
