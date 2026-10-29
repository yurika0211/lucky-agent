#!/usr/bin/env python3
"""Refresh first-party zip sums that the public proxy has republished.

The selected versions stay in go.mod. CI drops only stale first-party zip
hashes, downloads through the module proxy, then records the zip hash the
proxy actually served. Other checksums are left untouched.
"""

import os
from pathlib import Path
import subprocess
import sys


ROOT = Path(__file__).resolve().parent.parent


def is_republished_zip_sum(line):
    parts = line.split()
    if len(parts) != 3 or "/go.mod" in parts[1] or not parts[2].startswith("h1:"):
        return False
    return parts[0].startswith("github.com/yurika0211/")


def filter_sum(path):
    lines = path.read_text(encoding="utf-8").splitlines(True)
    kept = [line for line in lines if not is_republished_zip_sum(line)]
    removed = len(lines) - len(kept)
    path.write_text("".join(kept), encoding="utf-8")
    label = path.name if not path.is_relative_to(ROOT) else path.relative_to(ROOT)
    print(f"ignored {removed} republished zip sums in {label}", flush=True)
    return removed


def download_env():
    env = os.environ.copy()
    env.update(GOWORK="off", GONOSUMDB="github.com/yurika0211/*")
    return env


def refresh(directory):
    if filter_sum(directory / "go.sum") == 0:
        raise SystemExit(f"no first-party zip sums found in {directory / 'go.sum'}")
    env = download_env()
    print(f"download {directory.relative_to(ROOT) or '.'}", flush=True)
    subprocess.run(["go", "mod", "download"], cwd=directory, env=env, check=True)
    subprocess.run(["go", "mod", "download", "-json", "all"], cwd=directory, env=env, check=True, capture_output=True)
    subprocess.run(["go", "mod", "tidy"], cwd=directory, env=env, check=True)
    print(f"recorded current first-party zip sums in {directory.relative_to(ROOT) or '.'}", flush=True)


def main():
    refresh(ROOT)
    refresh(ROOT / "agent")
    return 0


if __name__ == "__main__":
    sys.exit(main())
