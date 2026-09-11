#!/usr/bin/env sh
set -eu

source_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
prefix=${LH_INSTALL_PREFIX:-"$HOME/.local"}
app_dir=${LH_APP_INSTALL_DIR:-"$prefix/share/luckyagent"}
bin_dir=${LH_BIN_INSTALL_DIR:-"$prefix/bin"}

if [ ! -x "$source_dir/lh" ]; then
  echo "LuckyAgent payload is missing lh" >&2
  exit 1
fi
if [ ! -f "$source_dir/UI/TUI/dist/tui.mjs" ]; then
  echo "LuckyAgent payload is missing the bundled TUI" >&2
  exit 1
fi

parent_dir=$(dirname "$app_dir")
mkdir -p "$parent_dir" "$bin_dir"
stage_dir=$(mktemp -d "$parent_dir/.luckyagent-stage.XXXXXX")
trap 'rm -rf "$stage_dir"' EXIT
cp -R "$source_dir/lh" "$source_dir/UI" "$source_dir/runtime" "$stage_dir/"
rm -rf "$app_dir"
mv "$stage_dir" "$app_dir"
trap - EXIT

install_launcher() {
  target=$1
  temporary=$(mktemp "$bin_dir/.luckyagent-launcher.XXXXXX")
  cat > "$temporary"
  chmod 0755 "$temporary"
  mv -f "$temporary" "$target"
}

install_launcher "$bin_dir/lh" <<EOF
#!/usr/bin/env sh
set -eu
APP_ROOT='$app_dir'
export PATH="\$APP_ROOT/runtime/node/bin:\$PATH"
export LH_TUI_DIR="\$APP_ROOT/UI"
export LH_DASHBOARD_STATIC="\$APP_ROOT/UI/GUI/dist"
exec "\$APP_ROOT/lh" "\$@"
EOF

install_launcher "$bin_dir/luckyagent-tui" <<EOF
#!/usr/bin/env sh
exec '$bin_dir/lh' tui "\$@"
EOF

install_launcher "$bin_dir/luckyagent-gui" <<EOF
#!/usr/bin/env sh
set -eu
runtime_dir="\${HOME}/.luckyagent/runtime"
log_dir="\${HOME}/.luckyagent/logs"
mkdir -p "\$runtime_dir" "\$log_dir"
start_component() {
  name="\$1"
  shift
  pid_file="\$runtime_dir/\$name.pid"
  if [ -f "\$pid_file" ] && kill -0 "\$(cat "\$pid_file")" 2>/dev/null; then
    return
  fi
  nohup '$bin_dir/lh' "\$@" >"\$log_dir/\$name.log" 2>&1 &
  printf '%s' "\$!" > "\$pid_file"
}
start_component serve serve
start_component dashboard dashboard start
sleep 1
if command -v xdg-open >/dev/null 2>&1; then
  xdg-open http://127.0.0.1:8765 >/dev/null 2>&1 &
elif command -v open >/dev/null 2>&1; then
  open http://127.0.0.1:8765 >/dev/null 2>&1 &
fi
EOF

profile_file=${LH_PROFILE_FILE:-"$HOME/.profile"}
path_line="export PATH=\"$bin_dir:\$PATH\" # LuckyAgent"
if [ -f "$profile_file" ] && ! grep -F "$path_line" "$profile_file" >/dev/null 2>&1; then
  printf '\n%s\n' "$path_line" >> "$profile_file"
elif [ ! -e "$profile_file" ]; then
  printf '%s\n' "$path_line" > "$profile_file"
fi

"$bin_dir/lh" init >/dev/null 2>&1 || true
echo "LuckyAgent installed to $app_dir"
echo "Commands: lh, luckyagent-gui, luckyagent-tui"
echo "Open a new terminal or run: export PATH=\"$bin_dir:\$PATH\""
