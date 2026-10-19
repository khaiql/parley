#!/usr/bin/env python3
"""Fresh CLI installs and upgrades, without touching the user's HOME or network."""
import hashlib
import io
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]


class CLIInstallTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="parley-cli-install-")
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.home = self.root / "home"
        self.home.mkdir()
        self.tools = self.root / "tools"
        self.tools.mkdir()
        for name in ("awk", "cat", "chmod", "cp", "mkdir", "mktemp", "mv", "rm", "sh", "tar", "gzip", "shasum", "sha256sum"):
            if path := shutil.which(name):
                (self.tools / name).symlink_to(path)
        self.tool("uname", '#!/bin/sh\ncase "$1" in -s) echo Linux;; -m) echo x86_64;; esac\n')
        self.tool("curl", '#!/bin/sh\ncat "$FIXTURE/${2##*/}" > "$4"\n')
        self.env = {**os.environ, "HOME": str(self.home), "PATH": f"{self.home}/.local/bin:{self.tools}", "FIXTURE": str(self.root)}
        self.archive = self.root / "parley_Linux_x86_64.tar.gz"
        self.release("0.3.0")

    def tool(self, name, content):
        path = self.tools / name
        path.write_text(content)
        path.chmod(0o755)

    def release(self, version):
        data = f'#!/bin/sh\necho \'{{"status":"ok","version":"{version}"}}\'\n'.encode()
        with tarfile.open(self.archive, "w:gz") as archive:
            info = tarfile.TarInfo("parley")
            info.mode, info.size = 0o755, len(data)
            archive.addfile(info, io.BytesIO(data))
        digest = hashlib.sha256(self.archive.read_bytes()).hexdigest()
        (self.root / "checksums.txt").write_text(f"{digest}  {self.archive.name}\n")

    def install(self):
        return subprocess.run(["/bin/sh", str(ROOT / "install.sh")], env=self.env, capture_output=True, text=True, timeout=15)

    def test_fresh_install_and_upgrade_preserve_pinned_binary(self):
        result = self.install()
        target = self.home / ".local/bin/parley"
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), str(target))
        self.assertIn('"0.3.0"', subprocess.check_output(["parley", "version"], env=self.env, text=True))
        pinned = self.root / "pinned"
        os.link(target, pinned)
        self.release("0.4.0")
        result = self.install()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('"0.4.0"', subprocess.check_output(["parley", "version"], env=self.env, text=True))
        self.assertIn('"0.3.0"', pinned.read_text())
        self.assertFalse((self.home / ".parley/bin").exists())
        result = subprocess.run(["/bin/sh", str(ROOT / "skills/parley/scripts/ensure-parley")], env={**self.env, "PATH": str(self.tools), "PARLEY_BIN": ""}, capture_output=True, text=True, timeout=15)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), str(target))

    def test_checksum_failure_keeps_existing_binary(self):
        target = self.home / ".local/bin/parley"
        target.parent.mkdir(parents=True)
        target.write_text("old working binary")
        (self.root / "checksums.txt").write_text(f"{'0' * 64}  {self.archive.name}\n")
        result = self.install()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("checksum", result.stderr)
        self.assertEqual(target.read_text(), "old working binary")

    def test_existing_binary_shadow_is_explained(self):
        self.tool("parley", "#!/bin/sh\necho old\n")
        self.env["PATH"] = f"{self.tools}:{self.home}/.local/bin"
        result = self.install()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("appears earlier on PATH", result.stderr)
        self.assertIn(str(self.tools / "parley"), result.stderr)

    def test_homebrew_bootstrap_prints_only_binary_path(self):
        self.env["PATH"] = str(self.tools)
        self.tool("brew", '''#!/bin/sh
echo "Homebrew progress"
if [ "$1" = install ]; then
  mkdir -p "$HOME/.local/bin"
  echo '#!/bin/sh' > "$HOME/.local/bin/parley"
  chmod +x "$HOME/.local/bin/parley"
fi
''')
        result = subprocess.run(["/bin/sh", str(ROOT / "skills/parley/scripts/ensure-parley")], env={**self.env, "PARLEY_BIN": ""}, capture_output=True, text=True, timeout=15)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), str(self.home / ".local/bin/parley"))
        self.assertIn("Homebrew progress", result.stderr)

    def test_missing_path_is_explained(self):
        self.env["PATH"] = str(self.tools)
        result = self.install()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("PATH", result.stderr)
        self.assertIn(str(self.home / ".local/bin"), result.stderr)


if __name__ == "__main__":
    unittest.main()
