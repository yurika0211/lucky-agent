#!/usr/bin/env python3
"""PreToolUse hook: block writes/deletes to sensitive files.

Reads the hook Payload as JSON on stdin and emits a Decision on stdout.
Match this on: file_write, file_patch, file_delete, file_move, file_mkdir,
shell, terminal.

This is a starter policy — tune PROTECTED to your deployment.
"""
import sys
import json
import re

# Substrings that mark a path as protected. Conservative: ".env" also covers
# ".env.prod" etc., which is the safe direction.
PROTECTED = (
    "config.json",
    ".env",
    "accounts.db",
    ".git/",
    "/.ssh/",
    "/etc/",
    "/tokens/",
    ".luckyharness/",
)

# Shell tokens that indicate the command mutates the filesystem.
MUTATING = re.compile(r"(>>?|\brm\b|\bmv\b|\bcp\b|\btee\b|sed\s+-i|truncate|chmod|chown|dd\b)")


def hits(value: str) -> bool:
    return any(p in (value or "") for p in PROTECTED)


def main() -> None:
    payload = json.load(sys.stdin)
    tool = payload.get("tool", "")
    try:
        args = json.loads(payload.get("arguments") or "{}")
    except (ValueError, TypeError):
        args = {}

    blocked = False
    if tool in ("file_write", "file_patch", "file_delete", "file_move", "file_mkdir"):
        for key in ("path", "dest", "to", "target", "src", "source"):
            if hits(str(args.get(key, ""))):
                blocked = True
                break
    elif tool in ("shell", "terminal"):
        cmd = str(args.get("command", ""))
        if MUTATING.search(cmd) and hits(cmd):
            blocked = True

    if blocked:
        print(json.dumps({
            "decision": "block",
            "reason": "writing or deleting protected files (config/secrets/db/.git) is not allowed",
        }))
    else:
        print(json.dumps({"decision": "allow"}))


if __name__ == "__main__":
    main()
