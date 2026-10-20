# Installation user testing — 2026-10-19

Tested on macOS arm64 with Homebrew, and on `ubuntu-server` over SSH (Ubuntu 24.04.5 LTS, x86_64), both with real Herdr 0.9.3. Script and plugin tests used isolated homes, config/state directories, and a separate Herdr server. No existing agent panes, room state, or plugin registration were changed.

## Findings before changes

| User journey | Observation | Cause |
| --- | --- | --- |
| Homebrew lookup before tapping | Cask unavailable; explicit `brew tap khaiql/parley` required | README skipped tap setup |
| Fresh Homebrew install after tapping | Installed published 0.3.0 | Release and tap publishing succeeded |
| Upgrade with a stale 0.2.0 tap | Reported 0.2.0 as latest | Homebrew used the local cask definition |
| `brew update`, then upgrade | Upgraded 0.2.0 to 0.3.0 | Refreshing the tap resolved the reproduced stale-version symptom |
| Run `parley` after Homebrew install | This Mac still selected `~/.local/bin/parley`, which exposed the older `host`/`export` CLI | Earlier binary on `PATH` hid Homebrew's executable |
| Install Herdr from README | Only a late paragraph and a long separate staging procedure | No dedicated installation/setup path; CLI package omitted plugin resources |
| Script installation | Installed outside the usual user binary directory | Default was `~/.parley/bin` |

The stale-tap test deliberately set the newly added test tap to its 0.2.0 commit, then used real Homebrew commands and published release archives. This reproduces a cause of the reported symptom; it does not prove the other laptop has that exact state.

## Checks after changes

| Journey | Result |
| --- | --- |
| Updated `install.sh`, real published 0.3.0 archive, fresh isolated home | Checksum verified; installed and ran `~/.local/bin/parley` |
| Script upgrade with an existing pinned binary | New executable replaced atomically; old inode remained usable |
| Corrupt checksum with an existing installation | Failed without changing the working executable |
| `~/.local/bin` absent from `PATH` | Installer printed the directory and shell instruction |
| Agent bootstrap with user bin absent from `PATH` | Found `~/.local/bin/parley` and printed only its path |
| `parley herdr install` with real Herdr, no server | Linked and enabled plugin without downloading or compiling another binary |
| Repeat plugin install through a symlinked CLI | Reused the same canonical plugin root |
| Registered plugin with a running isolated Herdr server | Exposed Start/Join actions |
| `parley herdr setup` from standalone CLI | Started coordinator from registered plugin root; status succeeded |
| Upgrade while a different plugin remained enabled | Refused with disable/drain instructions; registration stayed intact |
| Disable, replace CLI, install new plugin, setup | New plugin and coordinator worked; old plugin executable still ran |
| Relink previous plugin root, setup | Rollback worked |
| Homebrew uninstall with a retained binary hard link | Retained executable still ran after Homebrew removed its cask files |

Native plugin checks used source binaries stamped `0.4.0-test` and `0.5.0-test`; these are test versions, not published releases. v0.3.0 does not have `parley herdr install`. README explicitly distinguishes the new command from its legacy v0.3.0 bundle procedure.

Automated regressions: `python3 scripts/test-cli-install.py`, `python3 scripts/test-herdr-install.py`, Go installation/registry tests, build, lint, and the full race suite. The full race suite exceeds this Mac's original 30-second per-package budget; it passes with `-timeout 120s`. No runtime or CI timeout was changed for that unrelated timing issue.

## Ubuntu SSH verification

Ran the updated script against actual GitHub release downloads on `ubuntu-server`. The plugin command used Linux binaries cross-compiled from this PR, stamped `0.4.0-test` and `0.5.0-test`; no Go, checkout, or plugin download was needed on the server. All checks passed:

- Fresh 0.3.0 script install and repeat install, including checksum verification and retained executable links.
- Migration from authentic 0.2.0 release bytes in `~/.parley/bin` to 0.3.0 in `~/.local/bin`; old binary preserved and PATH shadowing warning confirmed.
- In-place 0.2.0-to-0.3.0 upgrade in `~/.local/bin`.
- One-command plugin install through a symlinked CLI, repeated without a running Herdr server; hard link reused the installed Linux binary.
- Start/Join action discovery, controller setup, status, and registered coordinator binary with a real isolated Herdr server.
- Enabled upgrade refusal; disable/drain, upgrade, and setup; previous executable still usable.
- Rollback to the previous plugin root and successful setup/status.
- Existing Ubuntu plugin registry matched its pre-test snapshot. Test server stopped; isolated homes, downloads, candidate binaries, and broker directory removed.

## Limits

Native archive installation and helper runtime checks also passed in Ubuntu/macOS distribution CI on [PR #142](https://github.com/khaiql/parley/pull/142); its main CI build, lint, race tests, and coverage checks passed. Installer failure and platform-selection cases are covered by fixtures. Actual cross-machine agent collaboration was not repeated for these installation changes. No release was published. Test Homebrew cask/tap and isolated Herdr servers were removed after testing; existing user binaries, room state, and Herdr registrations were left alone.
