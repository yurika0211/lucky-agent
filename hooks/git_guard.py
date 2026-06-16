#!/usr/bin/env python3
"""PreToolUse hook: block high-risk git mutations.

Reads the hook Payload as JSON on stdin and emits a Decision on stdout.
Match this on: shell, terminal.
"""
import json
import re
import shlex
import sys


def block(reason: str) -> None:
    print(json.dumps({"decision": "block", "reason": reason}))
    sys.exit(0)


def split_command(command: str) -> list[str]:
    try:
        return shlex.split(command)
    except ValueError:
        return command.split()


def contains_git_command(tokens: list[str]) -> bool:
    return any(token == "git" or token.endswith("/git") for token in tokens)


def check_git(command: str) -> None:
    low = command.lower()
    tokens = split_command(command)
    if not contains_git_command(tokens):
        return

    if re.search(r"\bgit\s+push\b", low):
        block("git push is blocked; ask the user to push manually")
    if re.search(r"\bgit\s+clean\b", low):
        block("git clean is blocked because it can delete untracked files")
    if re.search(r"\bgit\s+reset\s+--hard\b", low):
        block("git reset --hard is blocked")
    if re.search(r"\bgit\s+(checkout|restore)\b", low) and (
        re.search(r"\s--\s", low) or re.search(r"\s\\?\\.($|\s)", low)
    ):
        block("bulk git checkout/restore is blocked")
    if re.search(r"\bgit\s+rebase\b", low):
        block("git rebase is blocked because it rewrites history")
    if re.search(r"\bgit\s+commit\b", low) and "--amend" in low:
        block("git commit --amend is blocked because it rewrites history")


def main() -> None:
    payload = json.load(sys.stdin)
    tool = payload.get("tool", "")
    if tool not in ("shell", "terminal"):
        print(json.dumps({"decision": "allow"}))
        return

    try:
        args = json.loads(payload.get("arguments") or "{}")
    except (ValueError, TypeError):
        args = {}
    check_git(str(args.get("command", "")))
    print(json.dumps({"decision": "allow"}))


if __name__ == "__main__":
    main()
