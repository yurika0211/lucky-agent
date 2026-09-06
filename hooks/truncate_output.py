#!/usr/bin/env python3
"""PostToolUse hook: last-resort safety guard against pathologically oversized
tool output (e.g. a runaway command dumping gigabytes to stdout).

This is NOT a routine content-shaping tool. LuckyAgent already stores/streams
full tool output to the user (session history, TUI/GUI live view) and applies
its own much smaller compaction just for what gets fed back into the model's
context on the next turn. Enabling this hook with a low threshold destroys
that full-fidelity guarantee, because the rewrite happens in-place before the
raw output ever reaches the agent. Only enable it (or lower the threshold)
if you've hit a real OOM/disk-pressure incident from a single tool call.

Reads the hook Payload as JSON on stdin and emits a Decision on stdout.
Match this on: [] (all tools).

Customize the byte budget with LH_HOOK_MAX_OUTPUT_BYTES. The default is a
high ceiling (8 MiB) meant to only catch truly pathological output, not to
shape normal tool results; it preserves head and tail for debuggability.
"""
import json
import os
import sys


DEFAULT_MAX_BYTES = 8 * 1024 * 1024
OMITTED_MARKER = "\n\n[... SAFETY GUARD: tool output exceeded LH_HOOK_MAX_OUTPUT_BYTES and was truncated to avoid OOM; raw output was NOT persisted or shown to the user in full ...]\n\n"


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
