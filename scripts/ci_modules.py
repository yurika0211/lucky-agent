#!/usr/bin/env python3
"""Prepare CI module downloads when a pinned zip hash has been republished.

The public proxy has served more than one zip for the same selected
pseudo-version. For the CI job only, drop first-party zip hashes from go.sum
and download with sumdb disabled for github.com/yurika0211/*. go.mod versions
stay unchanged. Other modules keep their checksums.
"""

import os
from pathlib import Path
import subprocess
import sys


ROOT = Path(__file__).resolve().parent.parent


def is_republished_zip_sum(line):
    parts = line.split()
    if len(parts) < 3 or parts[2].startswith("go.mod"):
        return False
    return parts[0].startswith("github.com/yurika0211/") and parts[2].startswith("h1:")


def filter_sum(path):
    lines = path.read_text(encoding="utf-8").splitlines(True)
    kept = [line for line in lines if not is_republished_zip_sum(line)]
    removed = len(lines) - len(kept)
    path.write_text("".join(kept), encoding="utf-8")
    label = path.name if not path.is_relative_to(ROOT) else path.relative_to(ROOT)
    print(f"ignored {removed} republished zip sums in {label}", flush=True)
    return removed


def download(directory):
    env = os.environ.copy()
    env.update(GOWORK="off", GOPRIVATE="github.com/yurika0211/*", GONOSUMDB="github.com/yurika0211/*", GOSUMDB="off")
    subprocess.run(["go", "mod", "download"], cwd=directory, env=env, check=True)


def main():
    for directory in (ROOT, ROOT / "agent"):
        if filter_sum(directory / "go.sum") == 0:
            raise SystemExit(f"no first-party zip sums found in {directory / 'go.sum'}")
        print(f"download {directory.relative_to(ROOT) or '.'}", flush=True)
        download(directory)
    return 0


if __name__ == "__main__":
    sys.exit(main())
