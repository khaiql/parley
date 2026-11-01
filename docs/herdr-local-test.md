# Test Herdr shortcuts and the room picker locally

Successful Herdr shortcut Start/Join has not been verified end to end by the implementation agent. Automated regressions, genuine Codex hook timing, and native negative-action notification acknowledgement were verified. Computer-use tools blocked Terminal and Ghostty. This procedure supplies the missing manual proof and checks the resulting real sessions.

Use two existing supported agent panes on the same machine and Herdr session. Use a disposable test room: the verifier sends two uniquely marked messages and uploads two small binary artifacts. It fetches them into a temporary directory and deletes only those temporary local files. It does not stop rooms, change seen cursors, prompt agents, or change hook trust. This procedure does not prove either SSH forwarding direction.

## 1. Build and activate the child patch

From the PR checkout, with the intended local `default` Herdr server already running:

```sh
git status --short
git branch --show-current  # codex/herdr-controller-catalog includes the handoff and picker parents
go build -o bin/parley ./cmd/parley
herdr --version           # 0.9.3 or newer
herdr --session default plugin list --json  # note the old parley plugin_root for rollback
herdr --session default plugin link "$PWD"
HERDR_SESSION=default "$PWD/bin/parley" herdr setup
```

Linking changes which checkout the local Parley plugin uses. The old checkout and its binary remain intact. Setup starts the coordinator if necessary; do not restart Herdr or existing agents. Run setup from a shell on the intended host/session; an inherited `HERDR_SOCKET_PATH` selects its socket over `HERDR_SESSION`. For a named session, replace `default` consistently. If your controller uses a custom `PARLEY_STATE_DIR`, keep that same value for setup and the verifier; do not change an existing immutable root pin.

Every enabled saved SSH profile participates in discovery. Install and link Parley in that profile's selected Herdr session, then let the existing controller activate it. An enabled profile without a helper or activated session makes lookup incomplete, but confirmed local rows remain selectable. Do not disable unrelated profiles for a local-only test. An activated participant-only remote popup requests the controller’s authoritative catalog through its durable queue, so Mac-hosted rooms can be selected by number on Ubuntu. Missing activation or unavailable controller responses must produce warnings, rather than a complete empty list. Browsing never installs helpers or claims controller ownership.

If setup reports `coordinator_upgrade_required`, follow the [plugin-only upgrade procedure](herdr-plugin.md#upgrading-the-helper-protocol) before testing. Finish existing routed work first: restarting the owned coordinator closes its SSH routes. Agents do not need restarting.

## 2. Add and reload shortcuts

On each host where the Parley actions run, add these bindings to `~/.config/herdr/config.toml` only if absent. Mac-only bindings do not advertise custom plugin actions on a saved SSH machine. Preserve existing entries; resolve any custom key conflicts first:

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

For a saved SSH machine, reload its config from the Mac with `herdr --machine <profile> server reload-config`. For standalone remote attach, use `herdr --remote <host> --remote-keybindings server`; its default local-keybinding mode omits custom plugin commands.

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

Read each original terminal with `herdr --session default agent read <pane-id> --lines 50`. Require the delivered room/session context, the skill read, and an inbox check using the supplied session. Joining alone must not trigger greetings or acknowledgement-only replies. For message-exchange proof, explicitly ask each agent to send a useful task update. If an agent requests sandbox approval, handle that through its normal approval UI. Submission alone is not a complete agent-to-agent test. A failed prompt permits R + Enter in the result popup after the harness becomes ready; an uncertain prompt must not be retried blindly.

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

Install helper protocol 4 on both hosts and upgrade the owned coordinator using [the plugin procedure](herdr-plugin.md#upgrading-the-helper-protocol). Repeat Start/Join with a local host and remote participant, then with a remote host and local participant. Verify both agents received their own host-local skill, binary, state root, and session ID. Exercise messages and artifacts in both directions; same-host proof does not establish SSH proof.

Repeat with at least two Herdr-supported harness kinds. A working agent uses its harness's ordinary prompt behavior; no delayed-delivery guarantee is added. Record native shortcut, cross-harness, SSH authentication, sandbox approval, or acknowledgement failures separately. Do not claim an unexercised environment passes.

For Mac-hosted-room/Ubuntu-participant acceptance, keep a Mac room running, open Ubuntu Join from its existing agent, require that topic with host `Controller`, select its number, and require distinct participant/session plus submitted handoff. Reopen Join and refresh after closing a disposable Mac room; it must not remain selectable. This is the controller-catalog regression; showing only Ubuntu-local rooms is not a pass. Verify fresh SSH works without a password prompt and restart only an outdated Parley coordinator, never the agents or Herdr servers.

## Controller catalog live proof (2026-10-14)

Built helper protocol 3 for macOS and Linux amd64, linked separate prebuilt bundles on the live Mac and Ubuntu server, and restarted only Parley's coordinators. Existing checkout binaries were preserved. Ubuntu's unused second-controller configuration was backed up after verifying that it had no peers, rooms, bindings, requests, or handoffs; the Mac then activated Ubuntu as its participant. Run setup on the Mac controller only.

The actual Ubuntu Join form, launched in an SSH PTY against its existing Herdr socket and Codex terminal, listed Mac room `room-1e7a728868ca7f6204b8`, topic `testing`, as `Controller | open`. Escape closed the form without choosing Join. This proves live cross-host catalog discovery; it does not prove GUI shortcut invocation, cross-host Join, prompt delivery, or messages/artifacts. Build, golangci-lint, and the full race suite passed. Regressions cover stale host-session selection, unavailable controller fallback, unactivated host warnings, bounded catalog lookup, and browsing without participant or route creation.

## Terminal cleanup live proof

Run only with fresh, disposable Herdr workspaces, agent terminals, rooms, and participants. Never close an existing user pane or use an existing room. Link the protocol 4 build on each host and restart only its Parley coordinator after confirming no user routes depend on it.

1. On one host, create two disposable supported agent terminals. Start a room from one and Join from the other. Close only the joined terminal. Check the participant host's `parley herdr status`: its exact binding must be `left`; the host room and second participant must remain active.
2. Start another disposable room from a host terminal. Close only that terminal. Check that its exact room record is closed and that unrelated rooms remain open. If a participant is on another host, confirm its route disappears after coordinator reconciliation.
3. Repeat both cases with the participant and host on the other machine. Use at least two harness kinds when available; report unsupported or unavailable coverage.
4. Move a test terminal to another pane before closing it. The move alone must preserve its participant and room. Close the moved terminal, then confirm cleanup uses the stored participant/host session IDs.
5. Inspect `cleanup_status` and `cleanup_error` after any failed leave or stop. Do not report cleanup successful until Parley's leave acknowledgement or room-stopped record and socket shutdown are confirmed.

This checkout has automated lifecycle regressions but no live terminal-close proof yet. Report local and SSH coverage separately; catalog discovery evidence above does not prove cleanup.


## Remote-hosted Start and controller discovery

Use the saved SSH profile and selected Herdr session that fail at work. Direct `ssh <alias>` success only proves shell access: Parley also needs the helper installed and enabled in that profile's session, controller activation, and noninteractive SSH access.

1. Focus a supported agent on the remote machine. Use the Parley Start hotkey and enter a unique disposable topic. Record the final `started`, room ID, and session ID from the popup. An `opened` action result does not prove creation.
2. Focus a controller agent and use the Join hotkey. Refresh the picker and require that topic under the remote host with `open` status. Join by number, then verify both message/artifact directions with the SSH transport procedure. A local `herdr status` room cache is not the authoritative remote room list.
3. If creation fails, preserve the popup diagnostic and Herdr notification. If discovery fails, preserve picker warnings and the controller's `Parley remote unavailable` notification. Peer activation failures include the saved host, SSH target, Herdr session, and underlying error. Identical activation failures notify once until recovery; undelivered notifications retry on the next refresh. Request-stream failures also notify; undelivered stream notifications retry every two seconds until delivery succeeds or the subscription is canceled. SSH tunnel failures include SSH stderr instead of only `exit status 255`. Structured helper failures retain their error code and message instead of only `exit status 1`.
4. Confirm `[ui.toast] delivery = "herdr"` on the host displaying the notification. A native API acknowledgement proves delivery was accepted; visually confirm the notification in Herdr. Inspect the coordinator's `broker.log` if native notification delivery itself fails.

Automated integration coverage creates a real isolated room through `SubmitForm` after participant activation, then checks that the controller picker discovers it as open through the helper RPC boundary. SSH and native Herdr APIs are fixtures in that test; it does not prove the work host's actual hotkey, SSH connection, or toast configuration.


### Actual Ubuntu-to-Mac check — 2026-10-29

Tested the existing installed `parley-catalog-preview-ufwpdcoa` builds, without deploying this checkout's diagnostic changes. Remote is saved profile `ubuntu-server`, session `default`; local is the Mac default session. Created disposable genuine Codex agents on both machines.

Invoked the installed remote `parley.start` action with the remote agent focused. The action returned `opened`. Because the popup is not included in the headless pane inventory, opened the same installed `form` entrypoint as a split, with the original agent's native terminal/socket identity. Submitted topic `parley-remote-repro-20261029`. This exercises the actual form process and SSH hosts, but does not prove physical hotkey dispatch or popup rendering.

Observed remote form output:

```text
Request: b750ec4f3ed5765ded59420a3050c43d
Room: room-26c43bb33cd4ab4ab753
Session: psn_a6c2e525ed8ff19070e3
started
Agent instructions: submitted. Instructions submitted through Herdr.
```

Opened the installed local Join form for the disposable local Codex agent. The picker listed the topic as row 2, `ubuntu-server | open`, with the same room ID. Selected `2` and submitted; no room ID was copied into Join.

Observed local form output:

```text
Request: 62b7148b4cf48ab8f2d12d972f2857cc
Session: psn_2f090b473bbb77c6fbbd
joined
Agent instructions: submitted. Instructions submitted through Herdr.
```

Read-only session status then reported both participants `online`, both adapters running, and `last_received_seq: 3`. The remote host reported `server_running: true`, protocol port 40315 and artifact port 33073. The local participant reported `server_running: false`, forwarded protocol port 57818 and artifact port 57819. Therefore real remote creation, controller discovery, numbered Join, and SSH route establishment succeeded on these hosts. The work-laptop missing-room symptom was not reproduced. Message/artifact payload exchange and visible notification rendering were not tested in this run.

Stopped both disposable Parley sessions and closed only the test panes afterward. Existing plugin installations, saved SSH profiles, and pre-existing rooms were preserved.
