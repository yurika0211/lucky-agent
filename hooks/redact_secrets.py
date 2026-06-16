#!/usr/bin/env python3
"""PostToolUse hook: redact secrets from tool output.

Reads the hook Payload as JSON on stdin and emits a Decision on stdout.
Match this on: [] (all tools) — secrets can surface in any output, and lh
forwards output to Telegram/QQ/Weixin, so redact before it leaves.
"""
import sys
import json
import re

# (pattern, replacement). Patterns are intentionally broad; false positives
# only cost a redaction, never a leak.
RULES = [
    (re.compile(r"sk-[A-Za-z0-9_\-]{8,}"), "sk-[REDACTED]"),
    (re.compile(r"gh[pousr]_[A-Za-z0-9]{20,}"), "[REDACTED_GH_TOKEN]"),
    (re.compile(r"(?i)\bbearer\s+[A-Za-z0-9._\-]{8,}"), "Bearer [REDACTED]"),
    (re.compile(
        r"(?i)\b(api[_-]?key|secret|token|password|passwd|access[_-]?token)\b\s*[=:]\s*[\"']?[A-Za-z0-9._\-]{6,}"
    ), r"\1=[REDACTED]"),
]


def main() -> None:
    payload = json.load(sys.stdin)
    out = payload.get("output", "") or ""

    redacted = out
    for pattern, repl in RULES:
        redacted = pattern.sub(repl, redacted)

    if redacted != out:
        print(json.dumps({"decision": "modify", "modified_output": redacted}))
    else:
        print(json.dumps({"decision": "allow"}))


if __name__ == "__main__":
    main()
