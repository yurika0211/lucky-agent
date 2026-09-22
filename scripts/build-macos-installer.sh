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

mkdir -p "$root_dir/usr/local/lib/luckyagent" "$root_dir/usr/local/bin"
cp -R "$bundle_dir/." "$root_dir/usr/local/lib/luckyagent/"

cat > "$root_dir/usr/local/bin/lh" <<'EOF'
#!/usr/bin/env sh
set -eu
APP_ROOT=/usr/local/lib/luckyagent
export PATH="$APP_ROOT/runtime/node/bin:$PATH"
export LH_TUI_DIR="$APP_ROOT/UI"
export LH_DASHBOARD_STATIC="$APP_ROOT/UI/GUI/dist"
exec "$APP_ROOT/lh" "$@"
EOF
chmod 0755 "$root_dir/usr/local/bin/lh"

cat > "$root_dir/usr/local/bin/luckyagent-tui" <<'EOF'
#!/usr/bin/env sh
exec /usr/local/bin/lh tui "$@"
EOF
chmod 0755 "$root_dir/usr/local/bin/luckyagent-tui"

cat > "$root_dir/usr/local/bin/luckyagent-gui" <<'EOF'
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
  nohup /usr/local/bin/lh "$@" >"$log_dir/$name.log" 2>&1 &
  printf '%s' "$!" > "$pid_file"
}
start_component serve serve
start_component dashboard dashboard start
sleep 1
open http://127.0.0.1:8765 >/dev/null 2>&1 &
EOF
chmod 0755 "$root_dir/usr/local/bin/luckyagent-gui"

if [[ -x "$root_dir/usr/local/lib/luckyagent/luckyagent-desktop" ]]; then
  cat > "$root_dir/usr/local/bin/luckyagent-desktop" <<'EOF'
#!/usr/bin/env sh
set -eu
exec /usr/local/lib/luckyagent/luckyagent-desktop "$@"
EOF
  chmod 0755 "$root_dir/usr/local/bin/luckyagent-desktop"
fi

mkdir -p "$output_dir"
pkgbuild --root "$root_dir" --identifier com.luckyagent.app --version "${version#v}" "$output_dir/LuckyAgent-${version#v}-macos-${arch}.pkg"
