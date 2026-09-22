#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 4 ]]; then
  echo "usage: $0 <version> <arch> <bundle-dir> <output-dir>" >&2
  exit 2
fi

version=$1
arch=$2
bundle_dir=$3
output_dir=$4
root_dir=$(mktemp -d)
trap 'rm -rf "$root_dir"' EXIT

case "$arch" in
  amd64|arm64) ;;
  *) echo "unsupported Debian architecture: $arch" >&2; exit 1 ;;
esac

mkdir -p "$root_dir/DEBIAN" "$root_dir/opt/luckyagent" "$root_dir/usr/bin"
cp -R "$bundle_dir/." "$root_dir/opt/luckyagent/"

cat > "$root_dir/DEBIAN/control" <<EOF
Package: luckyagent
Version: ${version#v}
Section: utils
Priority: optional
Architecture: $arch
Maintainer: LuckyAgent
Description: LuckyAgent CLI, dashboard, and terminal UI
 Bundled Node runtime, GUI assets, and TUI are included.
EOF

cat > "$root_dir/usr/bin/lh" <<'EOF'
#!/usr/bin/env sh
set -eu
APP_ROOT=/opt/luckyagent
export PATH="$APP_ROOT/runtime/node/bin:$PATH"
export LH_TUI_DIR="$APP_ROOT/UI"
export LH_DASHBOARD_STATIC="$APP_ROOT/UI/GUI/dist"
exec "$APP_ROOT/lh" "$@"
EOF
chmod 0755 "$root_dir/usr/bin/lh"

cat > "$root_dir/usr/bin/luckyagent-tui" <<'EOF'
#!/usr/bin/env sh
exec /usr/bin/lh tui "$@"
EOF
chmod 0755 "$root_dir/usr/bin/luckyagent-tui"

cat > "$root_dir/usr/bin/luckyagent-gui" <<'EOF'
#!/usr/bin/env sh
set -eu
runtime_dir="${HOME}/.luckyagent/runtime"
log_dir="${HOME}/.luckyagent/logs"
mkdir -p "$runtime_dir" "$log_dir"
start_component() {
  name="$1"
  shift
  pid_file="$runtime_dir/$name.pid"
  if [ -f "$pid_file" ] && kill -0 "$(cat "$pid_file")" 2>/dev/null; then
    return
  fi
  nohup /usr/bin/lh "$@" >"$log_dir/$name.log" 2>&1 &
  printf '%s' "$!" > "$pid_file"
}
start_component serve serve
start_component dashboard dashboard start
sleep 1
if command -v xdg-open >/dev/null 2>&1; then
  xdg-open http://127.0.0.1:8765 >/dev/null 2>&1 &
fi
EOF
chmod 0755 "$root_dir/usr/bin/luckyagent-gui"


if [[ -x "$root_dir/opt/luckyagent/luckyagent-desktop" ]]; then
  mkdir -p "$root_dir/usr/share/applications" "$root_dir/usr/share/icons/hicolor/512x512/apps"
  cat > "$root_dir/usr/bin/luckyagent-desktop" <<'EOF'
#!/usr/bin/env sh
exec /opt/luckyagent/luckyagent-desktop "$@"
EOF
  chmod 0755 "$root_dir/usr/bin/luckyagent-desktop"
  if [[ -f "$root_dir/opt/luckyagent/desktop/assets/icon.png" ]]; then
    cp -f "$root_dir/opt/luckyagent/desktop/assets/icon.png" "$root_dir/usr/share/icons/hicolor/512x512/apps/luckyagent.png"
  fi
  cat > "$root_dir/usr/share/applications/luckyagent.desktop" <<'EOF'
[Desktop Entry]
Type=Application
Version=1.0
Name=LuckyAgent
GenericName=AI Agent
Comment=LuckyAgent desktop shell
Exec=/usr/bin/luckyagent-desktop
Icon=luckyagent
Terminal=false
Categories=Development;Utility;
StartupWMClass=LuckyAgent
StartupNotify=true
EOF
fi

mkdir -p "$output_dir"
dpkg-deb --build "$root_dir" "$output_dir/luckyagent_${version#v}_${arch}.deb"
