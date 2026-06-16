#!/usr/bin/env python3
"""PreToolUse hook: block destructive shell and database commands.

Reads the hook Payload as JSON on stdin and emits a Decision on stdout.
Match this on: shell, terminal, sql_query.

The shell checks are heuristics — harden them for your threat model.
"""
import sys
import json
import re


def block(reason: str) -> None:
    print(json.dumps({"decision": "block", "reason": reason}))
    sys.exit(0)


def check_shell(cmd: str) -> None:
    low = cmd.lower()
    # rm -rf / -fr / -r ... -f / --recursive --force
    if re.search(r"\brm\b", low) and (
        re.search(r"-\s*[a-z]*r[a-z]*f|-\s*[a-z]*f[a-z]*r", low)
        or (re.search(r"-[a-z]*r", low) and re.search(r"-[a-z]*f", low))
        or ("--recursive" in low and "--force" in low)
    ):
        block("rm -rf (recursive force delete) is blocked")
    if "git push" in low and ("--force" in low or re.search(r"\s-f(\s|$)", low) or "+refs" in low):
        block("git force-push is blocked")
    if "git reset --hard" in low:
        block("git reset --hard is blocked")
    if re.search(r":\s*\(\s*\)\s*\{\s*:\s*\|\s*:\s*&\s*\}\s*;\s*:", cmd):
        block("fork bomb is blocked")
    if re.search(r"\bdd\b.*\bof=/dev/", low) or "mkfs" in low:
        block("raw disk write is blocked")


def check_sql(args: dict) -> None:
    query = str(args.get("query", args.get("sql", ""))).upper()
    if re.search(r"\b(DROP|DELETE|UPDATE|INSERT|ALTER|TRUNCATE|REPLACE)\b", query):
        block("only read-only SELECT queries are allowed")


def main() -> None:
    payload = json.load(sys.stdin)
    tool = payload.get("tool", "")
    try:
        args = json.loads(payload.get("arguments") or "{}")
    except (ValueError, TypeError):
        args = {}

    if tool in ("shell", "terminal"):
        check_shell(str(args.get("command", "")))
    elif tool == "sql_query":
        check_sql(args)

    print(json.dumps({"decision": "allow"}))


if __name__ == "__main__":
    main()
