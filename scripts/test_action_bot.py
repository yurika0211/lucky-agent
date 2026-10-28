"""Offline checks for the Action bot parsers and digest stability."""

import io
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(ROOT / "scripts"))
import action_bot  # noqa: E402
import benchstat_json  # noqa: E402


class ActionBotTest(unittest.TestCase):
    def test_benchmark_parser_keeps_custom_metrics(self):
        raw = json.dumps({
            "Action": "output",
            "Output": "BenchmarkCheckpointTrajectory/operations=100/digests=true-2  1  123 ns/op  456 B/op  7 allocs/op  890 encoded-bytes/trajectory\n",
        })
        rows = benchstat_json.parse(io.StringIO(raw + "\n"))
        self.assertEqual(rows[0]["ops"], 1)
        self.assertEqual(rows[0]["metrics"]["encoded-bytes/trajectory"], 890)

    def test_digest_fingerprint_ignores_timestamp(self):
        first = action_bot.render([], [], [], "", "abc123")
        second = first.replace("2026-01-01 00:00 UTC", "2026-01-02 01:00 UTC")
        self.assertEqual(action_bot.fingerprint(first), action_bot.fingerprint(second))
        self.assertNotEqual(action_bot.fingerprint(first), action_bot.fingerprint(first + "\nchanged"))

    def test_direct_dependency_parser_skips_indirect(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "go.mod"
            path.write_text(
                "module example\n\ngo 1.25.0\n\n"
                "require github.com/yurika0211/luckyagent-app v0.4.2\n\n"
                "require (\n\tgithub.com/yurika0211/aestus v0.1.0 // indirect\n)\n",
                encoding="utf-8",
            )
            self.assertEqual(
                action_bot.module_deps(path),
                [("github.com/yurika0211/luckyagent-app", "v0.4.2")],
            )

    def test_parser_script_exits_nonzero_without_rows(self):
        result = subprocess.run(
            [sys.executable, str(ROOT / "scripts/benchstat_json.py"), "--output", "/tmp/unused-bench.json"],
            input="{}\n", text=True, capture_output=True,
        )
        self.assertEqual(result.returncode, 1)


if __name__ == "__main__":
    unittest.main()
