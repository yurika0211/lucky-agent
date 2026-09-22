#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 || $# -gt 2 ]]; then
  echo "usage: $0 <stage-dir> [electron-dist]" >&2
  exit 2
fi

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
stage_dir=$1
electron_dist=${2:-"$repo_root/UI/node_modules/electron/dist"}
desktop_dir="$repo_root/UI/desktop"

resolve_electron_bin() {
  local dist=$1
  if [[ -x "$dist/electron" ]]; then
    printf '%s
' "$dist/electron"
    return 0
  fi
  if [[ -x "$dist/electron.exe" ]]; then
    printf '%s
' "$dist/electron.exe"
    return 0
  fi
  if [[ -x "$dist/Electron.app/Contents/MacOS/Electron" ]]; then
    printf '%s
' "$dist/Electron.app/Contents/MacOS/Electron"
    return 0
  fi
  return 1
}

if [[ ! -d "$electron_dist" ]]; then
  echo "Electron runtime directory is missing: $electron_dist" >&2
  exit 1
fi
if ! resolve_electron_bin "$electron_dist" >/dev/null; then
  echo "Electron binary is missing under: $electron_dist" >&2
  echo "Expected electron, electron.exe, or Electron.app/Contents/MacOS/Electron" >&2
  exit 1
fi
if [[ ! -f "$desktop_dir/main.cjs" || ! -f "$desktop_dir/preload.cjs" || ! -f "$desktop_dir/package.json" ]]; then
  echo "desktop shell assets are missing under $desktop_dir" >&2
  exit 1
fi
if [[ ! -f "$desktop_dir/assets/icon.png" ]]; then
  echo "desktop icon is missing: $desktop_dir/assets/icon.png" >&2
  exit 1
fi

mkdir -p "$stage_dir/desktop/assets" "$stage_dir/desktop/scripts" "$stage_dir/runtime/electron"
# Replace any previous electron runtime payload for reproducible staging.
rm -rf "$stage_dir/runtime/electron"
mkdir -p "$stage_dir/runtime/electron"
cp -R "$electron_dist"/. "$stage_dir/runtime/electron/"
install -m 0644 "$desktop_dir/main.cjs" "$desktop_dir/preload.cjs" "$desktop_dir/package.json" "$stage_dir/desktop/"
cp -R "$desktop_dir/assets/." "$stage_dir/desktop/assets/"
if [[ -f "$desktop_dir/scripts/set-x11-icon.py" ]]; then
  install -m 0755 "$desktop_dir/scripts/set-x11-icon.py" "$stage_dir/desktop/scripts/set-x11-icon.py"
fi

cat > "$stage_dir/luckyagent-desktop" <<'LAUNCHER'
#!/usr/bin/env sh
set -eu
app_root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
export LH_ELECTRON_LOAD="${LH_ELECTRON_LOAD:-dist}"
export LH_APP_ROOT="${LH_APP_ROOT:-$app_root}"

electron_bin=
if [ -x "$app_root/runtime/electron/electron" ]; then
  electron_bin="$app_root/runtime/electron/electron"
elif [ -x "$app_root/runtime/electron/electron.exe" ]; then
  electron_bin="$app_root/runtime/electron/electron.exe"
elif [ -x "$app_root/runtime/electron/Electron.app/Contents/MacOS/Electron" ]; then
  electron_bin="$app_root/runtime/electron/Electron.app/Contents/MacOS/Electron"
else
  echo "LuckyAgent desktop runtime is missing under $app_root/runtime/electron" >&2
  exit 1
fi

exec "$electron_bin" "$app_root/desktop" "$@"
LAUNCHER
chmod 0755 "$stage_dir/luckyagent-desktop"

echo "Desktop shell staged into $stage_dir"
