#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: $0 <stage-dir> <binary>" >&2
  exit 2
fi

stage_dir=$1
binary=$2

if [[ ! -f "$binary" ]]; then
  echo "release binary is missing: $binary" >&2
  exit 1
fi

mkdir -p "$stage_dir"
install -m 0755 "$binary" "$stage_dir/lh"
if [[ -f packaging/unix/install.sh ]]; then
  install -m 0755 packaging/unix/install.sh "$stage_dir/install.sh"
fi
if [[ -f packaging/unix/luckyagent.service ]]; then
  install -m 0644 packaging/unix/luckyagent.service "$stage_dir/luckyagent.service"
fi
