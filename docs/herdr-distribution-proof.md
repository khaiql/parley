# Herdr bundle verification

This change adds repeatable release packaging and installation. It does not publish a release, alter a live installation, or restart user rooms.

## Automated checks

Run from the checkout with Go, GoReleaser v2, and Python 3.11+ installed (development/CI only):

```sh
python3 scripts/test-herdr-install.py
goreleaser check
goreleaser release --snapshot --clean --skip=publish
python3 scripts/check-herdr-release.py dist
python3 scripts/test-herdr-install.py --release-dist dist
go test -c -o /tmp/parley-herdr.test ./internal/herdr
PARLEY_HERDR_TEST_BUNDLE="$(cat dist/test-plugin-root.txt)" PATH=/usr/bin:/bin \
  /tmp/parley-herdr.test -test.run '^TestIntegrationNativeFormsAndActivation$' -test.timeout 30s -test.v
```

The distribution CI runs these checks on Ubuntu and macOS. The snapshot command uses the existing GoReleaser build/checksum/Homebrew pipeline without publishing. It produces all four plugin bundles and the four existing standalone CLI archives. Archive inspection verifies architecture headers, executable permissions, each manifest command path, release version, complete skill resources, SHA-256 checksums, and byte equality between plugin and standalone binaries. Homebrew is restricted to CLI archives.

Installer tests use an isolated HOME and restricted PATH containing standard Unix tools plus a fixture downloader, with no Go. They cover platform/architecture asset selection, prerelease pinning, missing assets/checksums/checksum entries, checksum corruption, unlaunchable binaries, incompatible helpers, manifest/binary version mismatch, missing skills, missing executable permissions, idempotence, and refusal to overwrite a modified release. Sentinels represent unrelated binaries, config, runtime state and a previously working release; failed installation must preserve them byte for byte.

The native archive test installs the actual snapshot bundle using a fixture download transport, verifies the real binary and bundled skill bootstrap, and reinstalls without replacing the executable. The precompiled Go integration test then runs with Go absent from PATH. It exercises real room/adapter binaries, Start and Join submission, participant activation, handoff acknowledgement, and participant reuse against a private native API fixture. This is a helper integration test, not a graphical Herdr popup test or live SSH test.

## Local observations (2026-10-15)

- Repository build, golangci-lint, and fresh `go test ./... -timeout 30s -race -count=1` passed after rebasing onto PR #129 head `70fc78c`.
- GoReleaser v2.15.0 generated all four plugin archives and standalone CLI archives. All archive checks passed, including Homebrew selection.
- macOS arm64: the real snapshot archive installed/reinstalled with Go absent from PATH. The delivered skill bootstrap resolved the supplied bundled executable. The packaged-binary integration test passed.
- Ubuntu 24.04 amd64 container on an arm64 Docker host (emulated, not a physical Linux machine): the actual Linux amd64 snapshot installed/reinstalled with no Go installed. Native Herdr 0.9.3 link/enable, helper startup, controller setup and repeated setup passed. This container test used the same fixture download transport.
- A separate actual Herdr 0.9.3 macOS headless server, with private HOME/XDG roots and no Go on PATH, accepted bundle link/enable, helper startup, controller setup and repeated setup. Repeated setup returned the same ownership metadata. Explicit disable, confirmed coordinator process exit, relink/enable and setup also preserved that metadata. Only the owned test plugin/server were stopped afterward.

These observations use local snapshot assets and a fixture download transport. They do not prove GitHub publication, downloading a published bundle, a fresh physical Linux machine, keyboard shortcuts, popup GUI operation, or a live Mac-controller/Ubuntu-participant collaboration session. No existing user agents, installs, SSH authentication or rooms were changed.

## Release acceptance still required

Before declaring the clean-machine end-user outcome verified:

1. Publish a release/prerelease through the existing release workflow only when explicitly requested. On a clean Linux amd64 machine and supported Mac, remove Go from PATH and follow the [normal release install](herdr-plugin.md#install-a-prebuilt-release). Confirm the downloaded script, archive, checksums, manifest, binary and skills come from that tag. Link/enable the bundle in a running Herdr session and verify helper startup.
2. Run setup only on the Mac controller. Install/link/enable the identical release on Ubuntu without setup. Keep passwordless SSH and normal host-key checking enabled. Configure saved profiles and shortcuts on their own hosts; a binary install cannot supply them.
3. From existing supported agent panes, use the actual Start shortcut on the Mac and Join shortcut on Ubuntu. Verify the Ubuntu picker discovers the Mac's room, helpers activate with compatible protocol, and both popup submissions complete. Inspect handoff results and require each agent to resolve its delivered absolute `PARLEY_BIN`, runtime root and bundled skill/bootstrap paths. Have the agents exchange task messages and byte-exact artifacts in both directions. Catalog discovery or a prompt acknowledgement alone is insufficient.
4. Record the linked root on each host, stage another bundle version, drain routed work, confirm the disabled controller coordinator exits, and follow the documented plugin-only update. Confirm controller/participant ownership, pins, room IDs, participant IDs and cursors persist. Reinstall the same tag, then drain and roll back to each recorded root. Do not restart agents/Herdr or disable a coordinator with active work.
5. Retry with an unsupported platform, unavailable tag/asset, wrong-architecture binary and altered checksum in isolated fixtures. Confirm an existing working link remains usable after every failure.

Use the existing [local UI/transport verifier](herdr-local-test.md) for message/artifact checks. Clearly record native GUI, fixture, container/emulation and archive-only results separately.
