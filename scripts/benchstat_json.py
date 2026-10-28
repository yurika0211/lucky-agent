#!/usr/bin/env python3
"""Turn `go test -bench -json` output into a stable performance summary."""

import argparse
import json
import re
import sys


BENCH = re.compile(
    r"^(?P<name>Benchmark\S+)\s+(?P<ops>\d+)\s+(?P<ns>[\d.]+)\s+ns/op"
    r"(?:\s+(?P<bytes>\d+)\s+B/op)?(?:\s+(?P<allocs>\d+)\s+allocs/op)?"
    r"(?P<rest>.*)$"
)
METRIC = re.compile(r"\s+([\d.eE+-]+)\s+([A-Za-z0-9_./+-]+)")


def parse(stream):
    rows = []
    for line in stream:
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        if event.get("Action") != "output":
            continue
        match = BENCH.match(event.get("Output", "").strip())
        if not match:
            continue
        metrics = {name: float(value) for value, name in METRIC.findall(match.group("rest"))}
        rows.append({
            "name": match.group("name"),
            "ops": int(match.group("ops")),
            "ns_per_op": float(match.group("ns")),
            "bytes_per_op": int(match.group("bytes") or 0),
            "allocs_per_op": int(match.group("allocs") or 0),
            "metrics": metrics,
        })
    return rows


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    rows = parse(sys.stdin)
    if not rows:
        print("no benchmark rows found", file=sys.stderr)
        return 1
    with open(args.output, "w", encoding="utf-8") as handle:
        json.dump(rows, handle, indent=2)
        handle.write("\n")
    for row in rows:
        print(f"{row['name']}\t{row['ns_per_op']:.0f} ns/op\t{row['bytes_per_op']} B/op")
    return 0


if __name__ == "__main__":
    sys.exit(main())
