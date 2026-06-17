#!/usr/bin/env python3
"""PreToolUse hook: keep filesystem mutations inside allowed LuckyHarness roots.

Reads the hook Payload as JSON on stdin and emits a Decision on stdout.
Match this on: file_write, file_patch, file_delete, file_move, file_mkdir,
shell, terminal.

Allowed roots can be customized with LH_HOOK_ALLOWED_ROOTS using os.pathsep
separation. By default ~/.luckyharness is allowed, except private credential,
session, log, and database paths. Relative paths without an explicit workdir
are resolved under ~/.luckyharness/workspace.
"""
import json
import os
import re
import shlex
import sys


DEFAULT_WORKSPACE = os.path.join(os.path.expanduser("~"), ".luckyharness", "workspace")
DEFAULT_LUCKYHARNESS = os.path.join(os.path.expanduser("~"), ".luckyharness")
DEFAULT_ALLOWED_ROOTS = (DEFAULT_LUCKYHARNESS,)
DEFAULT_PROTECTED_ROOTS = (
    os.path.join(DEFAULT_LUCKYHARNESS, "sessions"),
    os.path.join(DEFAULT_LUCKYHARNESS, "logs"),
    os.path.join(DEFAULT_LUCKYHARNESS, "tokens"),
    os.path.join(DEFAULT_LUCKYHARNESS, "profiles"),
)
DEFAULT_PROTECTED_FILES = (
    os.path.join(DEFAULT_LUCKYHARNESS, "config.json"),
    os.path.join(DEFAULT_LUCKYHARNESS, "config.prod.json"),
    os.path.join(DEFAULT_LUCKYHARNESS, "SOUL.md"),
    os.path.join(DEFAULT_LUCKYHARNESS, "hook-audit.jsonl"),
    os.path.join(DEFAULT_LUCKYHARNESS, "luckyharness.db"),
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


def protected_roots() -> list[str]:
    raw = os.environ.get("LH_HOOK_PROTECTED_ROOTS", "")
    roots = [p for p in raw.split(os.pathsep) if p.strip()] if raw else list(DEFAULT_PROTECTED_ROOTS)
    return [os.path.realpath(os.path.abspath(os.path.expanduser(p))) for p in roots]


def protected_files() -> list[str]:
    raw = os.environ.get("LH_HOOK_PROTECTED_FILES", "")
    files = [p for p in raw.split(os.pathsep) if p.strip()] if raw else list(DEFAULT_PROTECTED_FILES)
    return [os.path.realpath(os.path.abspath(os.path.expanduser(p))) for p in files]


def default_workspace() -> str:
    raw = os.environ.get("LH_HOOK_WORKSPACE", "").strip()
    if raw:
        return os.path.realpath(os.path.abspath(os.path.expanduser(raw)))
    return os.path.realpath(os.path.abspath(DEFAULT_WORKSPACE))


def normalize_path(path: str, base: str) -> str:
    path = os.path.expanduser(path.strip())
    if not path:
        return ""
    if not os.path.isabs(path):
        path = os.path.join(base, path)
    return os.path.realpath(os.path.abspath(path))


def has_explicit_workdir(args: dict) -> bool:
    value = args.get("workdir")
    return isinstance(value, str) and value.strip() != ""


def is_relative_path(path: str) -> bool:
    path = os.path.expanduser(path.strip())
    return path != "" and not os.path.isabs(path)


def is_inside(path: str, roots: list[str]) -> bool:
    for root in roots:
        if path == root or path.startswith(root + os.sep):
            return True
    return False


def is_protected_file(path: str, files: list[str]) -> bool:
    base = os.path.basename(path).lower()
    if base in ("config", "config.json", "config.yaml", "config.yml", "soul.md", "hook-audit.jsonl"):
        return True
    if base.startswith("config."):
        return True
    if base in (
        "auth.json",
        "chat_sessions.json",
        "credentials.json",
        "keys.json",
        "oauth.json",
        "token.json",
        "tokens.json",
        "secrets.json",
    ):
        return True
    if "_chat_completions_" in base:
        return True
    if base == ".env" or base.startswith(".env."):
        return True
    if base.endswith((".db", ".sqlite", ".sqlite3", ".pem", ".key", ".crt")):
        return True
    return path in files


def check_path(raw: str, base: str, roots: list[str], private_roots: list[str], private_files: list[str]) -> None:
    if not raw or raw.startswith("-"):
        return
    path = normalize_path(raw, base)
    if not path:
        return
    if is_inside(path, private_roots) or is_protected_file(path, private_files):
        decision_block(f"filesystem mutation of protected LuckyHarness private data is blocked: {path}")
    if not is_inside(path, roots):
        decision_block(f"filesystem mutation outside allowed roots is blocked: {path}")


def check_file_tool(args: dict, roots: list[str]) -> None:
    private_roots = protected_roots()
    private_files = protected_files()
    explicit_workdir = has_explicit_workdir(args)
    base = normalize_path(str(args.get("workdir") or ""), default_workspace()) or default_workspace()
    for key in PATH_KEYS:
        value = args.get(key)
        if isinstance(value, str) and value.strip():
            if not explicit_workdir and is_relative_path(value):
                decision_block(
                    "relative filesystem mutation without explicit workdir is blocked; "
                    "use an absolute path under ~/.luckyharness or set workdir to an allowed root"
                )
            check_path(value, base, roots, private_roots, private_files)


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
    private_roots = protected_roots()
    private_files = protected_files()
    command = str(args.get("command") or "")
    if not MUTATING_SHELL.search(command):
        return
    explicit_workdir = has_explicit_workdir(args)
    base = normalize_path(str(args.get("workdir") or ""), default_workspace()) or default_workspace()
    for token in shell_path_tokens(command):
        if not explicit_workdir and is_relative_path(token):
            decision_block(
                "relative shell filesystem mutation without explicit workdir is blocked; "
                "use an absolute path under ~/.luckyharness or set workdir to an allowed root"
            )
        check_path(token, base, roots, private_roots, private_files)


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
