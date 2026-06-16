#!/usr/bin/env python3
"""PreToolUse hook: append every tool call to a JSONL audit file.

Reads the hook Payload as JSON on stdin, records it, and always allows.
Match this on: [] (all tools). Put it FIRST in pre_tool_use so attempts are
logged even when a later hook blocks them.

Audit file path: $LH_HOOK_AUDIT, else ~/.luckyharness/hook-audit.jsonl
"""
import sys
import json
import os
from datetime import datetime


def main() -> None:
    payload = json.load(sys.stdin)
    record = {
        "time": datetime.now().isoformat(timespec="seconds"),
        "event": payload.get("event", ""),
        "tool": payload.get("tool", ""),
        "source": payload.get("source", ""),
        "session_id": payload.get("session_id", ""),
        "arguments": payload.get("arguments", ""),
    }

    path = os.environ.get(
        "LH_HOOK_AUDIT",
        os.path.join(os.path.expanduser("~"), ".luckyharness", "hook-audit.jsonl"),
    )
    try:
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "a", encoding="utf-8") as f:
            f.write(json.dumps(record, ensure_ascii=False) + "\n")
    except OSError:
        # Never let an audit failure break tool execution.
        pass

    print(json.dumps({"decision": "allow"}))


if __name__ == "__main__":
    main()
