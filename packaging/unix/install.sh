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

parent_dir=$(dirname "$app_dir")
mkdir -p "$parent_dir" "$bin_dir"
stage_dir=$(mktemp -d "$parent_dir/.luckyagent-stage.XXXXXX")
trap 'rm -rf "$stage_dir"' EXIT

cp -f "$source_dir/lh" "$stage_dir/lh"
if [ -f "$source_dir/install.sh" ]; then
  cp -f "$source_dir/install.sh" "$stage_dir/install.sh"
  chmod 0755 "$stage_dir/install.sh"
fi
if [ -f "$source_dir/luckyagent.service" ]; then
  cp -f "$source_dir/luckyagent.service" "$stage_dir/luckyagent.service"
fi

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
exec '$app_dir/lh' "\$@"
EOF

profile_file=${LH_PROFILE_FILE:-"$HOME/.profile"}
path_line="export PATH=\"$bin_dir:\$PATH\" # LuckyAgent"
if [ -f "$profile_file" ] && ! grep -F "$path_line" "$profile_file" >/dev/null 2>&1; then
  printf '\n%s\n' "$path_line" >> "$profile_file"
elif [ ! -e "$profile_file" ]; then
  printf '%s\n' "$path_line" > "$profile_file"
fi

"$bin_dir/lh" init >/dev/null 2>&1 || true

unit_src="$source_dir/luckyagent.service"
if [ ! -f "$unit_src" ] && [ -f "$app_dir/luckyagent.service" ]; then
  unit_src="$app_dir/luckyagent.service"
fi
if [ -f "$unit_src" ] && command -v systemctl >/dev/null 2>&1; then
  unit_dir="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
  mkdir -p "$unit_dir"
  unit_tmp=$(mktemp "$unit_dir/.luckyagent.service.XXXXXX")
  sed "s|ExecStart=%h/.local/bin/lh|ExecStart=$bin_dir/lh|" "$unit_src" > "$unit_tmp"
  chmod 0644 "$unit_tmp"
  mv -f "$unit_tmp" "$unit_dir/luckyagent.service"
  systemctl --user daemon-reload >/dev/null 2>&1 || true
  if systemctl --user is-enabled luckyagent.service >/dev/null 2>&1; then
    echo "user unit luckyagent.service already enabled"
  else
    systemctl --user enable luckyagent.service >/dev/null 2>&1 || true
  fi
  echo "systemd user unit: $unit_dir/luckyagent.service"
  echo "Do not also enable a system unit against the same home/port."
fi

echo "LuckyAgent installed to $app_dir"
echo "Command: lh"
echo "Open a new terminal or run: export PATH=\"$bin_dir:\$PATH\""
