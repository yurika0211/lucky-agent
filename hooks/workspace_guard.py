#!/usr/bin/env python3
"""PreToolUse hook: keep filesystem mutations inside allowed workspace roots.

Reads the hook Payload as JSON on stdin and emits a Decision on stdout.
Match this on: file_write, file_patch, file_delete, file_move, file_mkdir,
shell, terminal.

Allowed roots can be customized with LH_HOOK_ALLOWED_ROOTS using os.pathsep
separation. By default the repository root and two LuckyHarness temp artifact
directories are allowed.
"""
import json
import os
import re
import shlex
import sys


HOOK_DIR = os.path.dirname(os.path.abspath(__file__))
REPO_ROOT = os.path.abspath(os.path.join(HOOK_DIR, os.pardir))
DEFAULT_ALLOWED_ROOTS = (
    REPO_ROOT,
    "/tmp/luckyharness",
    "/tmp/luckyharness-artifacts",
)

FILE_TOOLS = {"file_write", "file_patch", "file_delete", "file_move", "file_mkdir"}
PATH_KEYS = ("path", "dst", "dest", "to", "target", "src", "source")
MUTATING_SHELL = re.compile(
    r"(>>?|"
    r"\brm\b|\bmv\b|\bcp\b|\btee\b|"
    r"sed\s+-i|\btruncate\b|\bchmod\b|\bchown\b|\bdd\b|\bmkdir\b)"
)


def decision_block(reason: str) -> None:
    print(json.dumps({"decision": "block", "reason": reason}))
    sys.exit(0)


def allowed_roots() -> list[str]:
    raw = os.environ.get("LH_HOOK_ALLOWED_ROOTS", "")
    roots = [p for p in raw.split(os.pathsep) if p.strip()] if raw else list(DEFAULT_ALLOWED_ROOTS)
    return [os.path.realpath(os.path.abspath(os.path.expanduser(p))) for p in roots]


def normalize_path(path: str, base: str) -> str:
    path = os.path.expanduser(path.strip())
    if not path:
        return ""
    if not os.path.isabs(path):
        path = os.path.join(base, path)
    return os.path.realpath(os.path.abspath(path))


def is_inside(path: str, roots: list[str]) -> bool:
    for root in roots:
        if path == root or path.startswith(root + os.sep):
            return True
    return False


def check_path(raw: str, base: str, roots: list[str]) -> None:
    if not raw or raw.startswith("-"):
        return
    path = normalize_path(raw, base)
    if path and not is_inside(path, roots):
        decision_block(f"filesystem mutation outside allowed workspace roots is blocked: {path}")


def check_file_tool(args: dict, roots: list[str]) -> None:
    base = normalize_path(str(args.get("workdir") or ""), REPO_ROOT) or REPO_ROOT
    for key in PATH_KEYS:
        value = args.get(key)
        if isinstance(value, str) and value.strip():
            check_path(value, base, roots)


def shell_path_tokens(command: str) -> list[str]:
    try:
        tokens = shlex.split(command)
    except ValueError:
        tokens = command.split()

    out: list[str] = []
    redirect_next = False
    for token in tokens:
        if redirect_next:
            out.append(token)
            redirect_next = False
            continue
        if token in (">", ">>", "2>", "2>>"):
            redirect_next = True
            continue
        if token.startswith((">", ">>")) and len(token) > token.count(">"):
            out.append(token.lstrip(">"))
            continue
        if token.startswith("of="):
            out.append(token[3:])
            continue
        if token.startswith("-"):
            continue
        if "/" in token or token.startswith(".") or token.startswith("~"):
            out.append(token)
    return out


def check_shell(args: dict, roots: list[str]) -> None:
    command = str(args.get("command") or "")
    if not MUTATING_SHELL.search(command):
        return
    base = normalize_path(str(args.get("workdir") or ""), REPO_ROOT) or REPO_ROOT
    for token in shell_path_tokens(command):
        check_path(token, base, roots)


def main() -> None:
    payload = json.load(sys.stdin)
    tool = payload.get("tool", "")
    try:
        args = json.loads(payload.get("arguments") or "{}")
    except (ValueError, TypeError):
        args = {}

    roots = allowed_roots()
    if tool in FILE_TOOLS:
        check_file_tool(args, roots)
    elif tool in ("shell", "terminal"):
        check_shell(args, roots)

    print(json.dumps({"decision": "allow"}))


if __name__ == "__main__":
    main()
