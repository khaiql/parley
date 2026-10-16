#!/usr/bin/env python3
"""Inspect all four GoReleaser bundles and the unchanged standalone CLI layout."""
import hashlib
from pathlib import Path
import struct
import sys
import tarfile
import tomllib

root = Path(__file__).resolve().parents[1]
dist = Path(sys.argv[1] if len(sys.argv) > 1 else "dist")
checksums = dict(line.split()[::-1] for line in (dist / "checksums.txt").read_text().splitlines())
for os_name in ("Linux", "Darwin"):
    for arch in ("x86_64", "arm64"):
        matches = list(dist.glob(f"parley-herdr_v*_{os_name}_{arch}.tar.gz"))
        assert len(matches) == 1, (os_name, arch, matches)
        bundle = matches[0]
        assert hashlib.sha256(bundle.read_bytes()).hexdigest() == checksums[bundle.name], bundle
        with tarfile.open(bundle) as archive:
            members = {m.name: m for m in archive.getmembers()}
            assert all(m.isfile() and not Path(m.name).is_absolute() and ".." not in Path(m.name).parts
                       for m in members.values()), members
            manifest = tomllib.loads(archive.extractfile("herdr-plugin.toml").read().decode())
            assert manifest["id"] == "parley" and not manifest.get("build"), manifest
            assert bundle.name == f'parley-herdr_v{manifest["version"]}_{os_name}_{arch}.tar.gz'
            source_manifest = tomllib.loads((root / "herdr-plugin.toml").read_text())
            source_manifest["version"] = manifest["version"]
            assert manifest == source_manifest, "packaged manifest changed beyond release version"
            for section in ("startup", "actions", "panes"):
                for entry in manifest[section]:
                    assert entry["command"][0] == "bin/parley", entry
                    assert members[entry["command"][0]].mode & 0o111, entry
            for resource in (root / "skills/parley").rglob("*"):
                if resource.is_file():
                    name = resource.relative_to(root).as_posix()
                    assert archive.extractfile(name).read() == resource.read_bytes(), name
            assert members["skills/parley/scripts/ensure-parley"].mode & 0o111
            binary = archive.extractfile("bin/parley").read()
            if os_name == "Linux":
                assert binary[:6] == b"\x7fELF\x02\x01", bundle
                assert struct.unpack_from("<H", binary, 18)[0] == (62 if arch == "x86_64" else 183), bundle
            else:
                assert binary[:4] == b"\xcf\xfa\xed\xfe", bundle
                assert struct.unpack_from("<I", binary, 4)[0] == (0x1000007 if arch == "x86_64" else 0x100000C), bundle
        cli_path = dist / f"parley_{os_name}_{arch}.tar.gz"
        assert hashlib.sha256(cli_path.read_bytes()).hexdigest() == checksums[cli_path.name]
        with tarfile.open(cli_path) as cli:
            assert cli.extractfile("parley").read() == binary, cli_path
            assert cli.getmember("parley").mode & 0o111
        print(f"PASS {bundle.name}: manifest, skills, executable, architecture, checksum, CLI parity")

casks = list((dist / "homebrew").rglob("*.rb"))
assert casks, "Homebrew cask was not generated"
for cask in casks:
    assert "parley-herdr" not in cask.read_text(), "Homebrew must use standalone CLI archives"
