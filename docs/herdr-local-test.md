# Test Herdr shortcuts and the room picker locally

Successful Herdr shortcut Start/Join has not been verified end to end by the implementation agent. Automated regressions, genuine Codex hook timing, and native negative-action notification acknowledgement were verified. Computer-use tools blocked Terminal and Ghostty. This procedure supplies the missing manual proof and checks the resulting real sessions.

Use two existing supported agent panes on the same machine and Herdr session. Use a disposable test room: the verifier sends two uniquely marked messages and uploads two small binary artifacts. It fetches them into a temporary directory and deletes only those temporary local files. It does not stop rooms, change seen cursors, prompt agents, or change hook trust. This procedure does not prove either SSH forwarding direction.

## 1. Build and activate the child patch

From the PR checkout, with the intended local `default` Herdr server already running:

```sh
git status --short
git branch --show-current  # codex/herdr-room-picker includes parent PR #132
go build -o bin/parley ./cmd/parley
herdr --version           # 0.9.3 or newer
herdr --session default plugin list --json  # note the old parley plugin_root for rollback
herdr --session default plugin link "$PWD"
HERDR_SESSION=default "$PWD/bin/parley" herdr setup
```

Linking changes which checkout the local Parley plugin uses. The old checkout and its binary remain intact. Setup starts the coordinator if necessary; do not restart Herdr or existing agents. Run setup from a shell on the intended host/session; an inherited `HERDR_SOCKET_PATH` selects its socket over `HERDR_SESSION`. For a named session, replace `default` consistently. If your controller uses a custom `PARLEY_STATE_DIR`, keep that same value for setup and the verifier; do not change an existing immutable root pin.

Every enabled saved SSH profile participates in discovery. Install and link Parley in that profile's selected Herdr session, then let the existing controller activate it. An enabled profile without a helper or activated session makes lookup incomplete, but confirmed local rows remain selectable. Do not disable unrelated profiles for a local-only test. A participant-only remote popup can list its own host rooms; it has no controller-wide peer catalog unless peers are already activated there. Use manual ID entry for other controller-routed rooms. Browsing never installs helpers or claims controller ownership.

If setup reports `coordinator_upgrade_required`, follow the [plugin-only upgrade procedure](herdr-plugin.md#upgrading-from-the-identity-based-helper) before testing. Finish existing routed work first: restarting the owned coordinator closes its SSH routes. Agents do not need restarting.

## 2. Add and reload shortcuts

In `~/.config/herdr/config.toml`, add these bindings only if absent. Preserve existing entries; resolve any custom key conflicts first:

```toml
[[keys.command]]
key = "prefix+shift+s"
type = "plugin_action"
command = "parley.start"
description = "Parley: Start room"

[[keys.command]]
key = "prefix+shift+i"
type = "plugin_action"
command = "parley.join"
description = "Parley: Join room"
```

For visible in-app toasts, set `delivery = "herdr"` in the existing `[ui.toast]` table, or create that table if absent. Open Herdr's global menu and choose **reload config**. Use `prefix+?`, search for `Parley`, and check both bindings. Default prefix means press Ctrl+B, release, then the next key.

## 3. Check the actual agent terminals

Get pane IDs from `herdr --session default agent list`, then check both:

```sh
herdr --session default agent get <pane-A>
herdr --session default agent get <pane-B>
```

Each must have a terminal ID and detected supported harness kind. Native `agent_session` is optional. Use existing agents; welcome prompts and a shared Codex daemon are supported without flags, hooks, or restarting agents. For cross-harness acceptance, use two already-running different harnesses supported by Herdr.

## 4. Exercise the real UI

Start a short screen recording with your normal recorder, if available.

1. Focus agent A. Press Ctrl+B, release, then Shift+S. A topic popup must open while that agent remains the underlying target.
2. Enter `parley-local-test-<date-or-unique-suffix>` and press Enter. Require a `started` result, room ID, and host session ID. Require `Agent instructions: submitted`; save both session IDs for the transport verifier, then press Enter to close. The workflow itself needs no copying.
3. Focus agent B. Press Ctrl+B, release, then Shift+I. Require a room list containing your topic, host, room ID, and `open` status. Enter its number and press Enter. Require a `joined` result and a different session ID and `Agent instructions: submitted`; press Enter to close. Do not copy the room ID to join.
4. Open either shortcut again and press Escape before submitting. No room submission or failure toast should occur; focus must return to the original agent pane.
5. Reopen Join from B. Require `already joined` for this room; use R + Enter to refresh. N/P + Enter changes pages when needed. Escape without submitting. An offline or unactivated enabled peer must produce an incomplete warning while confirmed local rows remain selectable; browsing must not create forwards or participants.
6. In a fresh disposable room, open Join from B, stop that disposable room from its host shell, then choose the old row. Require a validation error, never `joined`. R refresh must show its current state. M + Enter keeps explicit room-ID entry available. Preserve evidence of failed or blocked selections separately from successful joins.

An action launch record saying `running`, an `opened` acknowledgement, or a `pending`/`error` outcome does not establish successful room creation or Join. Save genuine screen/terminal evidence of the final `started` and `joined` outcomes. Do not use ordinary `parley start`/`join` commands to substitute for this UI test.

## 5. Verify agent awareness, then both endpoints

Read each original terminal with `herdr --session default agent read <pane-id> --lines 50`. Require the delivered room/session instructions, the skill read, and a successful presence `send` by each agent. If an agent requests sandbox approval, handle that through its normal approval UI. Submission alone is not a complete agent-to-agent test. A failed prompt permits R + Enter in the result popup after the harness becomes ready; an uncertain prompt must not be retried blindly.

Inspect `bin/parley herdr status`: `handoffs` records the delivery status by session ID even after a popup closes. Repeat Join for the same room and terminal: require the same session ID, preserved cursor, and no duplicate prompt after submitted delivery.


Run the script with the two **session IDs**, not pane IDs or the room ID:

```sh
python3 scripts/check-herdr-local.py <host-session-id> <joined-session-id>
```

It uses this checkout's `bin/parley`; set `PARLEY_BIN=/absolute/path/to/parley` only if testing another built binary. It checks that the IDs identify distinct running participants in the same room, sends a unique message plus artifact in each direction, checks the message's actor/room and artifact metadata, requires the receiver's own `state.last_received_seq` to reach that message's sequence, downloads each file, and compares exact bytes including binary values. Same-host history can read a shared server log, so history alone is not delivery proof. Status/history reads preserve seen cursors. Any missing receipt, wrong identity, metadata mismatch, or failed artifact download exits unsuccessfully.

Expected output:

```text
PASS host-to-joined: message received; artifact bytes match.
PASS joined-to-host: message received; artifact bytes match.
PASS local transport: room <room-id>, two distinct participants.
```

Only completing the UI, agent-awareness, and transport checks establishes the full same-host flow. The verifier has itself been exercised against isolated real headless Parley sessions, including a TCP relay that drops the receiver's message stream while its adapter remains available. The earlier verifier falsely reported the first direction as received through shared history; the corrected verifier rejects that case. It passes healthy message/artifact transfer both ways and rejects duplicate session IDs before sending. These checks validate the test tool, not Herdr shortcut behavior.

Room-picker implementation checks also exercised a freshly built binary in PTYs: native action capture for two fixture agent identities, Start, explicit refresh, numbered Join, and this verifier against the resulting real participants. Both message/artifact directions passed. A separate read-only check against the real local Herdr server listed the existing test room as `open, already joined`, displayed its enabled unactivated peer warning, and closed on Escape. The former uses a private native API fixture; the latter proves live discovery only. Neither substitutes for the actual shortcut UI steps above or SSH proof.

## 6. Check visible failures

- Focus an ordinary shell pane and press the Start shortcut. Require a visible error telling you to focus an agent; no room popup or successful result.
- If an existing Codex pane lacks `agent_session`, invoke its Start shortcut. Require Start and handoff to work without the conversation identity.
- Open a Herdr modal that still accepts the prefix shortcut and invoke Start. If the action reaches Herdr's pane API, require a busy-modal error. If the modal consumes the shortcut instead, record that the action was not invoked; do not count it as an exercised error path.
- In the Join popup, press M + Enter and enter a unique nonexistent room ID. Require an error or an explicitly pending request, never `joined`. Cross-host coordinator discovery can leave requests pending; check status before retrying.

Capture the matching diagnostics after each failure:

```sh
herdr --session default plugin log list --plugin parley --limit 10
HERDR_SESSION=default "$PWD/bin/parley" herdr status
```

Structured failure diagnostics must remain available. If `notification_failed` appears, its reason identifies disabled/busy/rate-limited delivery or native connectivity; that is not a visible-toast pass. Do not damage your real helper installation or downgrade Herdr to test helper failures; those paths have focused regression coverage.

## 7. Clean up only the test room

After saving evidence, leave the joined test participant and stop the disposable host room using the IDs from its popups:

```sh
bin/parley leave --session <joined-session-id>
bin/parley stop --session <host-session-id>
```

To restore your former plugin checkout, link the exact old `plugin_root` recorded in step 1. Restore any manually changed bindings/toast setting from your own backup if desired. Do not unlink the plugin globally or delete another room's state. Report the two UI outcomes, both verifier PASS lines, visible-error results, and any unexercised cases separately.

## 8. SSH and harness coverage

Install helper protocol 2 on both hosts and upgrade the owned coordinator using [the plugin procedure](herdr-plugin.md#upgrading-from-the-identity-based-helper). Repeat Start/Join with a local host and remote participant, then with a remote host and local participant. Verify both agents received their own host-local skill, binary, state root, and session ID. Exercise messages and artifacts in both directions; same-host proof does not establish SSH proof.

Repeat with at least two Herdr-supported harness kinds. A working agent uses its harness's ordinary prompt behavior; no delayed-delivery guarantee is added. Record native shortcut, cross-harness, SSH authentication, sandbox approval, or acknowledgement failures separately. Do not claim an unexercised environment passes.
