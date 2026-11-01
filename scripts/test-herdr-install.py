#!/usr/bin/env python3
"""Installer regressions; isolated HOME and PATH, no Go or live Herdr access."""
import hashlib
import io
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import struct
import sys
import tarfile
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
TAG = "v9.8.7-rc.1"


class InstallerTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="parley-installer-")
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name).resolve()
        self.home = self.root / "home"
        self.home.mkdir()
        self.tools = self.root / "tools"
        self.tools.mkdir()
        for name in ("awk", "cat", "chmod", "diff", "grep", "head", "mkdir", "mktemp",
                     "mv", "rm", "sed", "sh", "tar", "tr", "gzip", "shasum", "sha256sum"):
            if path := shutil.which(name):
                (self.tools / name).symlink_to(path)
        self.env = {**os.environ, "HOME": str(self.home), "PATH": str(self.tools),
                    "TMPDIR": str(self.root), "FIXTURE": str(self.root)}
        self.write_tool("uname", '#!/bin/sh\ncase "$1" in -s) echo Linux;; -m) echo x86_64;; esac\n')
        # Shell fixture helpers stand in for binaries; real archive tests use real od.
        cases = []
        for system in ("Linux", "Darwin"):
            for machine in ("x86_64", "aarch64"):
                if system == "Linux":
                    header = b"\x7fELF\x02\x01" + bytes(12) + struct.pack("<H", 62 if machine == "x86_64" else 183)
                else:
                    header = b"\xcf\xfa\xed\xfe" + struct.pack("<I", 0x1000007 if machine == "x86_64" else 0x100000C) + bytes(12)
                cases.append(f'{system}/{machine}) echo "${{FIXTURE_HEADER:-{header.hex()}}}";;')
        self.write_tool("od", '#!/bin/sh\ncase "$(uname -s)/$(uname -m)" in\n' + "\n".join(cases) + '\nesac\n')
        self.write_tool("curl", '''#!/bin/sh
echo "$2" >> "$FIXTURE/downloads"
case "$2" in
  https://github.com/khaiql/parley/releases/download/v9.8.7-rc.1/*) ;;
  *) echo "unexpected unpinned URL" >&2; exit 1;;
esac
name=${2##*/}
cat "$FIXTURE/$name" > "$4"
''')
        self.archive = self.root / f"parley-herdr_{TAG}_Linux_x86_64.tar.gz"
        self.bundle()
        # Unrelated CLI, runtime/config, and previously linked release are sentinels.
        self.previous = self.home / ".local/share/parley/herdr/v9.8.6/Linux_x86_64"
        self.previous.mkdir(parents=True)
        for name in ("bin/parley", "config/coordinator.json", "rooms/room/state", "herdr/registry"):
            path = self.home / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("preserve me")
        (self.previous / "working").write_text("preserve me")
        self.before = self.files()

    def files(self):
        return {str(p.relative_to(self.home)): p.read_bytes()
                for p in self.home.rglob("*") if p.is_file()}

    def write_tool(self, name, text):
        path = self.tools / name
        path.write_text(text)
        path.chmod(0o755)

    def bundle(self, *, version="9.8.7-rc.1", helper=5, executable=True, missing=None, manifest_version=None, wrong_arch=False):
        manifest = (ROOT / "herdr-plugin.toml").read_text()
        manifest = manifest.replace('version = "0.3.0"', f'version = "{manifest_version or version}"')
        # Fixtures model the released manifest, including before the source fix exists.
        manifest = manifest.replace('[[build]]\ncommand = ["go", "build", "-o", "bin/parley", "./cmd/parley"]\n\n', "")
        binary = "#!/bin/sh\necho '" + json.dumps({"version": version, "herdr_helper_version": helper}, indent=2) + "'\n"
        files = {"herdr-plugin.toml": manifest.encode(), "bin/parley": b"\x7fELF\x00wrong-architecture" if wrong_arch else binary.encode(),
                 "skills/parley/SKILL.md": (ROOT / "skills/parley/SKILL.md").read_bytes(),
                 "skills/parley/scripts/ensure-parley": (ROOT / "skills/parley/scripts/ensure-parley").read_bytes()}
        with tarfile.open(self.archive, "w:gz") as archive:
            for name, data in files.items():
                if name == missing:
                    continue
                entry = tarfile.TarInfo(name)
                entry.size = len(data)
                entry.mode = 0o755 if executable and name in ("bin/parley", "skills/parley/scripts/ensure-parley") else 0o644
                archive.addfile(entry, io.BytesIO(data))
        self.checksum()

    def checksum(self):
        digest = hashlib.sha256(self.archive.read_bytes()).hexdigest()
        (self.root / "checksums.txt").write_text(f"{digest}  {self.archive.name}\n")

    def run_install(self, success=True, *args):
        result = subprocess.run(["/bin/sh", str(ROOT / "install-herdr.sh"), *(args or (TAG,))],
                                env=self.env, text=True, capture_output=True, timeout=15)
        self.assertEqual(result.returncode == 0, success, result.stdout + result.stderr)
        if not success:
            self.assertTrue(result.stderr.strip(), "failure has no diagnostic")
        for name, content in self.before.items():
            self.assertEqual((self.home / name).read_bytes(), content, name)
        if not success:
            self.assertEqual(self.files(), self.before, "failed install changed existing files")
        return result

    def test_install_reinstall_and_rollback_paths(self):
        result = self.run_install()
        installed = Path(result.stdout.strip())
        self.assertEqual(installed, self.previous.parent.parent / TAG / "Linux_x86_64")
        self.assertTrue((installed / "bin/parley").stat().st_mode & 0o111)
        # Agent bootstrap resolves the delivered helper without trying another installer.
        binary = subprocess.check_output(["/bin/sh", str(installed / "skills/parley/scripts/ensure-parley")],
                                         env={**self.env, "PARLEY_BIN": str(installed / "bin/parley")}, text=True)
        self.assertEqual(binary.strip(), str(installed / "bin/parley"))
        before = (installed / "bin/parley").stat().st_ino
        self.assertEqual(self.run_install().stdout, result.stdout)
        self.assertEqual((installed / "bin/parley").stat().st_ino, before)
        self.assertTrue((self.previous / "working").is_file())

    def test_failures_preserve_existing_install(self):
        for mode in ("missing-asset", "missing-checksums", "checksum", "missing-checksum-entry",
                     "wrong-arch", "helper", "manifest-version", "binary-version", "skill", "permissions"):
            with self.subTest(mode=mode):
                self.bundle()
                if mode == "missing-asset":
                    self.archive.unlink()
                elif mode == "missing-checksums":
                    (self.root / "checksums.txt").unlink()
                elif mode == "checksum":
                    self.archive.write_bytes(b"broken download")
                elif mode == "missing-checksum-entry":
                    (self.root / "checksums.txt").write_text("")
                elif mode == "wrong-arch":
                    self.bundle(wrong_arch=True)
                elif mode == "helper":
                    self.bundle(helper=999)
                elif mode == "manifest-version":
                    self.bundle(manifest_version="0.0.1")
                elif mode == "binary-version":
                    self.bundle(version="0.0.1")
                elif mode == "skill":
                    self.bundle(missing="skills/parley/SKILL.md")
                elif mode == "permissions":
                    self.bundle(executable=False)
                self.run_install(False)

    def test_changed_release_is_not_overwritten(self):
        installed = Path(self.run_install().stdout.strip())
        (installed / "skills/parley/SKILL.md").write_text("local change")
        self.before = self.files()
        self.run_install(False)

    def test_runnable_wrong_architecture_is_rejected(self):
        self.env["FIXTURE_HEADER"] = (b"\x7fELF\x02\x01" + bytes(12) + struct.pack("<H", 183)).hex()
        result = self.run_install(False)
        self.assertIn("wrong helper architecture", result.stderr)

    def test_platform_asset_selection(self):
        original = self.archive.read_bytes()
        for os_name in ("Linux", "Darwin"):
            for machine, arch in (("x86_64", "x86_64"), ("aarch64", "arm64")):
                with self.subTest(os=os_name, arch=machine):
                    self.write_tool("uname", f'#!/bin/sh\ncase "$1" in -s) echo {os_name};; -m) echo {machine};; esac\n')
                    self.archive = self.root / f"parley-herdr_{TAG}_{os_name}_{arch}.tar.gz"
                    self.archive.write_bytes(original)
                    self.checksum()
                    self.assertTrue(self.run_install().stdout.strip().endswith(f"{os_name}_{arch}"))

    def test_unsupported_platform_and_invalid_version_do_not_download(self):
        for os_name, arch in (("FreeBSD", "x86_64"), ("Linux", "riscv64")):
            self.write_tool("uname", f'#!/bin/sh\ncase "$1" in -s) echo {os_name};; -m) echo {arch};; esac\n')
            self.run_install(False)
        self.run_install(False, "../../bad")
        self.assertFalse((self.root / "downloads").exists())


def stage_release(dist):
    """Stage the real native archive for the separate precompiled runtime test."""
    dist = Path(dist).resolve()
    os_name = platform.system()
    arch = {"x86_64": "x86_64", "aarch64": "arm64", "arm64": "arm64"}[platform.machine()]
    archives = list(dist.glob(f"parley-herdr_v*_{os_name}_{arch}.tar.gz"))
    assert len(archives) == 1, archives
    tag = archives[0].name.removeprefix("parley-herdr_").removesuffix(f"_{os_name}_{arch}.tar.gz")
    # Use the same restricted PATH as failure tests, with real platform detection.
    fixture = InstallerTest()
    fixture.setUp()
    try:
        fixture.write_tool("uname", f'#!/bin/sh\ncase "$1" in -s) echo {os_name};; -m) echo {arch};; esac\n')
        fixture.write_tool("curl", '#!/bin/sh\ncat "$FIXTURE/${2##*/}" > "$4"\n')
        (fixture.tools / "od").unlink()
        (fixture.tools / "od").symlink_to(shutil.which("od"))
        home = dist / "test-home"
        home.mkdir(exist_ok=True)
        env = {**fixture.env, "HOME": str(home), "FIXTURE": str(dist)}
        assert shutil.which("go", path=env["PATH"]) is None
        command = ["/bin/sh", str(ROOT / "install-herdr.sh"), tag]
        installed = subprocess.check_output(command, env=env, text=True).strip()
        assert subprocess.check_output(command, env=env, text=True).strip() == installed
        binary = str(Path(installed) / "bin/parley")
        info = json.loads(subprocess.check_output([binary, "version"], env=env))
        assert info["version"] == tag[1:] and info["herdr_helper_version"] == 5, info
        skill = str(Path(installed) / "skills/parley/scripts/ensure-parley")
        assert subprocess.check_output([skill], env={**env, "PARLEY_BIN": binary}, text=True).strip() == binary
        (dist / "test-plugin-root.txt").write_text(installed + "\n")
        print(f"PASS native {os_name}/{arch}: real bundle installed/reinstalled; skill resolves pinned binary; no Go on PATH")
    finally:
        fixture.doCleanups()


if __name__ == "__main__":
    if len(sys.argv) == 3 and sys.argv[1] == "--release-dist":
        stage_release(sys.argv[2])
    else:
        unittest.main()
