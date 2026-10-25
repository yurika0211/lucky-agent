"""Offline regression checks for release manifests and both installers."""

import hashlib
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile
import tempfile
import textwrap
import unittest
import zipfile
from datetime import datetime
from urllib.parse import quote


ROOT = Path(__file__).resolve().parent.parent


class ReleaseIntegrityTest(unittest.TestCase):
    def test_tag_push_manifest_has_pinned_assets_hashes_and_date(self):
        workflow = (ROOT / ".github/workflows/release.yml").read_text(encoding="utf-8")
        step = workflow.split("      - name: Generate update manifest\n", 1)[1]
        code = textwrap.dedent(step.split("python3 - <<'PY'\n", 1)[1].split("          PY\n", 1)[0])
        for published in ("", "2026-10-01T00:00:00Z"):
            with self.subTest(published=published), tempfile.TemporaryDirectory() as tmp:
                assets = Path(tmp) / "release-assets"
                assets.mkdir()
                archive = assets / "lh-linux-amd64.tar.gz"
                payload = b"release fixture"
                archive.write_bytes(payload)
                tag = "v1.2.3"
                result = subprocess.run(
                    [sys.executable, "-c", code], cwd=tmp, capture_output=True, text=True,
                    env={**os.environ, "RELEASE_TAG": tag, "RELEASE_PUBLISHED_AT": published},
                )
                self.assertEqual(result.returncode, 0, result.stderr)
                manifest = json.loads((assets / "update.json").read_text(encoding="utf-8"))
                date = manifest["published_at"]
                self.assertIsNotNone(datetime.fromisoformat(date.replace("Z", "+00:00")).tzinfo)
                if published:
                    self.assertEqual(date, published)
                self.assertEqual(manifest["tag_name"], tag)
                asset, = manifest["assets"]
                self.assertEqual(
                    asset["download_url"],
                    f"https://github.com/yurika0211/lucky-agent/releases/download/{quote(tag)}/{archive.name}",
                )
                self.assertEqual(asset["sha256"], hashlib.sha256(payload).hexdigest())
                self.assertEqual(asset["size"], len(payload))

    @staticmethod
    def write_manifest(directory, name, payload, checksum):
        manifest = {
            "tag_name": "v1.2.3",
            "assets": [{
                "name": name,
                "download_url": f"https://example.test/releases/download/v1.2.3/{name}",
                "sha256": checksum,
                "size": len(payload),
            }],
        }
        (directory / "manifest.json").write_text(json.dumps(manifest), encoding="utf-8")
        (directory / name).write_bytes(payload)

    @unittest.skipIf(os.name == "nt", "POSIX installer runs on Linux/macOS")
    def test_unix_installer_verifies_before_installing(self):
        for platform in ("Linux", "Darwin"):
            for checksum_kind in ("valid", "uppercase", "missing", "tampered"):
                with self.subTest(platform=platform, checksum=checksum_kind), tempfile.TemporaryDirectory() as tmp:
                    directory = Path(tmp)
                    name = f"lh-{platform.lower()}-amd64.tar.gz"
                    contents = b'#!/bin/sh\nmkdir -p "$LH_INSTALL_PREFIX"\nprintf installed > "$LH_INSTALL_PREFIX/installed"\n'
                    buffer = io.BytesIO()
                    with tarfile.open(fileobj=buffer, mode="w:gz") as archive:
                        info = tarfile.TarInfo("install.sh")
                        info.size, info.mode = len(contents), 0o755
                        archive.addfile(info, io.BytesIO(contents))
                    payload = buffer.getvalue()
                    digest = hashlib.sha256(payload).hexdigest()
                    checksum = {"valid": digest, "uppercase": digest.upper(), "missing": "", "tampered": "0" * 64}[checksum_kind]
                    self.write_manifest(directory, name, payload, checksum)
                    bin_dir = directory / "bin"
                    bin_dir.mkdir()
                    helpers = {
                        "uname": f'print("x86_64" if "-m" in sys.argv else "{platform}")',
                        "curl": (
                            'source = "manifest.json" if sys.argv[-1].endswith("/update.json") else os.environ["TEST_ARCHIVE"]\n'
                            'shutil.copyfile(pathlib.Path(os.environ["TEST_FIXTURES"]) / source, sys.argv[sys.argv.index("-o") + 1])'
                        ),
                        # macOS has no sha256sum; do not accidentally depend on it.
                        "sha256sum": "raise SystemExit(99)",
                    }
                    for helper, body in helpers.items():
                        path = bin_dir / helper
                        path.write_text(f"#!{sys.executable}\nimport os, pathlib, shutil, sys\n{body}\n", encoding="utf-8")
                        path.chmod(0o755)
                    prefix = directory / "install"
                    result = subprocess.run(
                        ["sh", str(ROOT / "scripts/install-lh.sh"), "v1.2.3", str(prefix)],
                        capture_output=True, text=True,
                        env={**os.environ, "PATH": f"{bin_dir}{os.pathsep}{os.environ['PATH']}",
                             "TEST_FIXTURES": str(directory), "TEST_ARCHIVE": name, "TMPDIR": tmp},
                    )
                    valid = checksum_kind in ("valid", "uppercase")
                    self.assertEqual(result.returncode == 0, valid, result.stderr)
                    self.assertEqual((prefix / "installed").exists(), valid, result.stderr)
                    if not valid:
                        message = "missing sha256" if checksum_kind == "missing" else "checksum mismatch"
                        self.assertIn(message, result.stderr)

    def test_windows_installer_verifies_before_extracting(self):
        shell = shutil.which("pwsh") or shutil.which("powershell")
        if not shell:
            self.skipTest("PowerShell is unavailable")
        for checksum_kind in ("valid", "uppercase", "missing", "tampered"):
            with self.subTest(checksum=checksum_kind), tempfile.TemporaryDirectory() as tmp:
                directory = Path(tmp)
                name = "lh-windows-amd64.zip"
                buffer = io.BytesIO()
                with zipfile.ZipFile(buffer, "w") as archive:
                    archive.writestr("Install-Portable.ps1", "param($SourceRoot, $InstallDir)\n")
                payload = buffer.getvalue()
                digest = hashlib.sha256(payload).hexdigest()
                checksum = {"valid": digest, "uppercase": digest.upper(), "missing": "", "tampered": "0" * 64}[checksum_kind]
                self.write_manifest(directory, name, payload, checksum)
                harness = directory / "run.ps1"
                harness.write_text(textwrap.dedent("""\
                    $ErrorActionPreference = "Stop"
                    function Invoke-WebRequest {
                      param($Uri, $OutFile)
                      $name = if ($Uri.EndsWith("/update.json")) { "manifest.json" } else { "lh-windows-amd64.zip" }
                      Copy-Item (Join-Path $env:TEST_FIXTURES $name) $OutFile
                    }
                    function Expand-Archive {
                      param($Path, $DestinationPath, [switch]$Force)
                      Set-Content (Join-Path $env:TEST_FIXTURES "extracted") "yes"
                      Microsoft.PowerShell.Archive\\Expand-Archive -Path $Path -DestinationPath $DestinationPath -Force
                    }
                    & $env:TEST_INSTALLER -Version v1.2.3 -Prefix (Join-Path $env:TEST_FIXTURES "install")
                    """), encoding="utf-8")
                result = subprocess.run(
                    [shell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", str(harness)],
                    capture_output=True, text=True,
                    env={**os.environ, "TEST_FIXTURES": tmp, "TEST_INSTALLER": str(ROOT / "scripts/install-lh.ps1"),
                         "TEMP": tmp, "LOCALAPPDATA": tmp},
                )
                valid = checksum_kind in ("valid", "uppercase")
                self.assertEqual(result.returncode == 0, valid, result.stderr)
                self.assertEqual((directory / "extracted").exists(), valid, result.stderr)
                if not valid:
                    message = "missing sha256" if checksum_kind == "missing" else "checksum mismatch"
                    self.assertIn(message, result.stderr)


if __name__ == "__main__":
    unittest.main()
