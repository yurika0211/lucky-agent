#!/usr/bin/env python3
"""PostToolUse hook: cap oversized tool output before it enters context.

Reads the hook Payload as JSON on stdin and emits a Decision on stdout.
Match this on: [] (all tools).

Customize the byte budget with LH_HOOK_MAX_OUTPUT_BYTES. The default keeps
40 KiB total, preserving the head and tail for debuggability.
"""
import json
import os
import sys


DEFAULT_MAX_BYTES = 40 * 1024
OMITTED_MARKER = "\n\n[... hook truncated oversized tool output ...]\n\n"


def max_bytes() -> int:
    raw = os.environ.get("LH_HOOK_MAX_OUTPUT_BYTES", "").strip()
    if not raw:
        return DEFAULT_MAX_BYTES
    try:
        value = int(raw)
    except ValueError:
        return DEFAULT_MAX_BYTES
    return max(1024, value)


def truncate_utf8(text: str, budget: int) -> str:
    encoded = text.encode("utf-8")
    if len(encoded) <= budget:
        return text

    marker = OMITTED_MARKER.encode("utf-8")
    room = max(0, budget - len(marker))
    head_budget = room * 2 // 3
    tail_budget = room - head_budget

    head = encoded[:head_budget].decode("utf-8", errors="ignore")
    tail = encoded[-tail_budget:].decode("utf-8", errors="ignore") if tail_budget else ""
    return head + OMITTED_MARKER + tail


def main() -> None:
    payload = json.load(sys.stdin)
    out = payload.get("output", "") or ""
    limit = max_bytes()
    if len(out.encode("utf-8")) <= limit:
        print(json.dumps({"decision": "allow"}))
        return

    shortened = truncate_utf8(out, limit)
    print(json.dumps({"decision": "modify", "modified_output": shortened}))


if __name__ == "__main__":
    main()
