#!/usr/bin/env sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(CDPATH= cd -- "$script_dir/.." && pwd)
prefix=${LH_INSTALL_PREFIX:-"$HOME/.local"}
app_dir=${XDG_DATA_HOME:-"$HOME/.local/share"}/applications
icon_root=${XDG_DATA_HOME:-"$HOME/.local/share"}/icons/hicolor
bin_dir="$prefix/bin"

electron_bin=${LH_ELECTRON_BIN:-"$repo_root/UI/node_modules/electron/dist/electron"}
desktop_dir="$repo_root/UI/desktop"
icon_src="$desktop_dir/assets/icon.png"
launcher="$bin_dir/luckyagent-desktop"

if [ ! -x "$electron_bin" ]; then
  echo "Electron runtime is missing: $electron_bin" >&2
  echo "Run npm install from UI/ before installing the desktop entry." >&2
  exit 1
fi
if [ ! -f "$icon_src" ]; then
  echo "Desktop icon is missing: $icon_src" >&2
  exit 1
fi

mkdir -p "$bin_dir" "$app_dir"

copy_icon() {
  size=$1
  src="$desktop_dir/assets/icon-${size}.png"
  dest="$icon_root/${size}x${size}/apps"
  if [ -f "$src" ]; then
    mkdir -p "$dest"
    cp -f "$src" "$dest/luckyagent.png"
  fi
}

copy_icon 512
copy_icon 256
copy_icon 128
copy_icon 64
copy_icon 48
copy_icon 32
if [ ! -f "$icon_root/512x512/apps/luckyagent.png" ]; then
  mkdir -p "$icon_root/512x512/apps"
  cp -f "$icon_src" "$icon_root/512x512/apps/luckyagent.png"
fi

temporary=$(mktemp "$bin_dir/.luckyagent-desktop.XXXXXX")
cat > "$temporary" <<LAUNCHER
#!/usr/bin/env sh
set -eu
export LH_ELECTRON_LOAD="\${LH_ELECTRON_LOAD:-dist}"
export LH_APP_ROOT="\${LH_APP_ROOT:-$repo_root}"
cd "$desktop_dir"
exec "$electron_bin" "$desktop_dir" "\$@"
LAUNCHER
chmod 0755 "$temporary"
mv -f "$temporary" "$launcher"

cat > "$app_dir/luckyagent.desktop" <<ENTRY
[Desktop Entry]
Type=Application
Version=1.0
Name=LuckyAgent
GenericName=AI Agent
Comment=LuckyAgent desktop shell
Exec=$launcher
Icon=luckyagent
Terminal=false
Categories=Development;Utility;
StartupWMClass=LuckyAgent
StartupNotify=true
ENTRY

if command -v update-desktop-database >/dev/null 2>&1; then
  update-desktop-database "$app_dir" >/dev/null 2>&1 || true
fi
if command -v gtk-update-icon-cache >/dev/null 2>&1; then
  gtk-update-icon-cache -f -t "$icon_root" >/dev/null 2>&1 || true
fi

echo "Installed $app_dir/luckyagent.desktop"
echo "Launcher: $launcher"
