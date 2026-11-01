# Herdr Start and Join

For a new installation, start with the [README's Herdr plugin section](../README.md#herdr-plugin): install the CLI, run `parley herdr install`, choose the controller, and add shortcuts. That command embeds the matching plugin resources and reuses the installed executable. It is new after v0.3.0; the pinned bundle procedure below remains available for v0.3.0 and for rollback.

For a review checkout, use the [local test procedure and verifier](herdr-local-test.md) to exercise the real shortcuts and both message/artifact endpoints.

### Install a prebuilt release

Install the same release on every participating host. Requirements: Herdr 0.9.3+, OpenSSH, `sh`, `curl`, `tar`, standard Unix tools, and `sha256sum` (Linux) or `shasum` (macOS). Go and a compiler are development/CI dependencies only.

v0.3.0 is the first release with plugin bundles. The following commands install that published release without a compiler. Use the same tag on every host. Older CLI-only releases do not contain the plugin.

On the controller and on each participant host, with the intended Herdr session running:

```sh
VERSION=v0.3.0
INSTALLER="$(mktemp)"
curl -fsSL "https://github.com/khaiql/parley/releases/download/$VERSION/install-herdr.sh" -o "$INSTALLER" &&
  PLUGIN_ROOT="$(sh "$INSTALLER" "$VERSION")" &&
  rm -f "$INSTALLER" &&
  printf 'Staged plugin: %s\n' "$PLUGIN_ROOT"
```

After successful staging, link/enable **only for first installation**:

```sh
herdr plugin link "$PLUGIN_ROOT" --disabled &&
  herdr plugin enable parley &&
  "$PLUGIN_ROOT/bin/parley" version
```

If Parley is already installed or linked, follow [update and rollback](#update-and-rollback) before changing its registration.

The installer selects Linux/macOS and amd64/arm64 using the host's OS/architecture. It downloads only the selected tagged bundle plus that release's `checksums.txt`, verifies SHA-256, and checks the executable, manifest version, bundled skill, and helper protocol before staging an immutable directory under `~/.local/share/parley/herdr/<tag>/<OS>_<arch>`. It prints the absolute directory to link. Repeating the same installation reuses matching files; a differing existing release is refused. Missing assets, unsupported platforms, download/checksum failures, and incompatible or unlaunchable helpers leave existing releases usable. No Go process, module fetch, checkout, or manual executable copy is involved. Checksums protect download integrity; the release publisher and downloaded installer remain trusted code.

[Herdr 0.9.3 installation](https://github.com/herdrdev/herdr/blob/v0.9.3/docs/next/website/src/content/docs/plugins.mdx#install-and-link) supports GitHub source clones and local directory links, not release-archive URLs or manifest download fields. Use the staged bundle with `plugin link`; `herdr plugin install khaiql/parley` is not the prebuilt install path. The v0.3.0 standalone CLI from Homebrew or `install.sh` does not supply plugin resources. Newer source builds (and the next release) embed them and support `parley herdr install`; use that command instead of this bundle procedure when available.

Run setup **only on the controller**, with the intended Herdr session selected:

```sh
"$PLUGIN_ROOT/bin/parley" herdr setup
```

Setup records a unique controller identity for that server socket and starts its detached coordinator immediately, including when the server is already running. Later Herdr server startups restore that coordinator. Linking or enabling a plugin does not run Herdr startup hooks; run setup after first controller activation. Unconfigured and participant sessions do not launch a coordinator.

### SSH participants

For a Mac controller and Ubuntu participant, install/link/enable the same version on both hosts using the commands above. Run setup on the Mac only; setup on Ubuntu would create a conflicting controller. Keep each host's own config/state and credentials local. The controller verifies helper protocol/capabilities and claims the remote session before subscribing; it never copies an executable or credentials to the remote host.

Enable the intended saved SSH profiles in Herdr. Each profile must identify its named Herdr session, or select one uniquely verified running session. Helper and tunnel connections require noninteractive SSH authentication; verify `ssh -o BatchMode=yes <saved-target> true`. A Herdr connection authenticated with a server password does not authenticate those separate connections. Fixed helper calls use the login shell environment so Herdr installed in `~/.local/bin` is found. A different remote controller owner, missing helper, disabled plugin, incompatible capability, authentication failure, or host-key refusal remains visible in the coordinator log. Configure SSH permission and trust normally; installing the bundle does not configure either.

Add the [focused-pane shortcuts](#set-up-focused-pane-shortcuts) on each host too. On Ubuntu, opening Join requests the controller's room catalog through the existing activated connection. Validate Start/Join, the agent's delivered binary/skill paths, and actual message/artifact exchange; listing rooms alone does not prove collaboration. See the [native acceptance procedure](herdr-distribution-proof.md).

### Build from source (contributors)

Contributors need Go matching `go.mod`. Build before linking an absolute checkout; link does not run builds, and the manifest has no build command:

```sh
go build -o bin/parley ./cmd/parley
herdr plugin link /absolute/path/to/parley
/absolute/path/to/parley/bin/parley herdr setup  # Controller only.
```

The source checkout must retain `skills/parley/`, including `scripts/ensure-parley`. Release packaging uses the existing GoReleaser build matrix and stamps the packaged manifest with the same version as the binary. Standalone CLI archives and Homebrew retain their original layout.

### Set up focused-pane shortcuts

Herdr 0.9.3 supports plugin action keybindings. Manifest actions do not automatically appear in a palette or pane context menu. Add these entries to your existing `~/.config/herdr/config.toml` (Linux/macOS); preserve other settings and add each binding only once:

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

These keys are unbound in the v0.9.3 defaults. Check your existing bindings first and choose different keys if needed: `prefix+p` already selects the previous tab and `prefix+j` moves down a pane. Open Herdr's global menu and choose **reload config**, which reloads the client's keybindings and the selected server's configuration without restarting panes. `herdr server reload-config` reloads the local server; it does not reload a remote machine merely because that machine is selected in the UI. Press `prefix+?` and search for `Parley` to check the active shortcuts.

With the default prefix, press **Ctrl+B**, release, then **Shift+S** for Start or **Shift+I** for Join, while the existing agent pane remains focused. Start accepts a topic; Join lists rooms for numbered selection. No separate shell pane is needed for normal invocation. Typing `herdr plugin action invoke parley.start` in a shell instead targets the server's current pane, which may be that shell; `running` only acknowledges asynchronous action launch.

Herdr's ordinary navigation bindings can come from the local client, but plugin actions are custom commands advertised by the selected server. For saved SSH machines, add the Parley `[[keys.command]]` blocks on the remote host too, then reload that server with `herdr --machine <profile> server reload-config`. Mac-only plugin bindings do not configure the Ubuntu server. Standalone `herdr --remote` in its default local-keybinding mode omits custom commands entirely; configure the remote blocks and attach with `herdr --remote <host> --remote-keybindings server`. Reloading config does not change that attach policy. See the [remote-access documentation](https://github.com/herdrdev/herdr/blob/v0.9.3/docs/next/website/src/content/docs/persistence-remote.mdx#remote-attach-over-ssh). Install the plugin and helper on every participating host; the action runs on the host of the focused pane. Notifications use the injected native socket, so remote failures stay in that Herdr context. Diagnostic CLI commands run on their host, or use `herdr --machine <enabled-profile-id-or-label>` from Local; selecting a machine in the UI does not retarget ordinary local commands.

### Agent targeting and visible failures

Parley targets the captured Herdr socket, terminal ID, and detected harness kind. Any harness supported by Herdr can participate. Native `agent_session` is optional; Codex welcome prompts and shared daemons do not require hooks, flags, or agent restarts. The terminal is re-resolved before joining and prompting, so changing focus or moving a pane does not redirect instructions. A different terminal or harness is rejected. Existing bindings retain their original storage keys, participant IDs, and cursors.

Preflight and form failures request a silent Herdr notification and retain structured JSON/plugin-log diagnostics. Shell targets explain how to focus an agent; missing terminal or harness detection explains the native targeting requirement; incompatible helpers and busy modals give recovery steps. Failed submitted outcomes stay visible in the form until Enter and then exit unsuccessfully. Escape or EOF cancels input without a failure notification.

Notifications follow Herdr's configured toast delivery. To display them inside Herdr, use `delivery = "herdr"` in your existing `[ui.toast]` table, then reload configuration. With delivery disabled, no foreground client, a busy toast, or rate limiting, delivery can fail; the original diagnostic includes `notification_failed` and the native reason. An unavailable socket also prevents notification; it never falls back to another local session. If the Parley executable cannot be launched at all, inspect Herdr's own action launch log and repair the plugin bundle installation:

```sh
herdr --session <name> plugin log list --plugin parley --limit 5
```

### Start and Join

Invoke **Parley: Start room** with its shortcut from an existing agent pane. Enter a nonempty topic and press Enter. The popup captures the original terminal and harness kind before opening. It creates a local room and shows its room ID, session ID, and existing Parley command instructions. Escape, EOF, empty input, control characters, and topics over 512 bytes do not submit work.

Invoke **Parley: Join room** from another existing agent pane. The popup lists topic, host, room ID, live status, and whether this agent already joined. Enter a visible room number and press Enter. No room ID needs copying. Use **R + Enter** to refresh, **N/P + Enter** for pages, **M + Enter** for manual room-ID entry, or **Escape** to close.

Discovery runs only on opening, explicit refresh, and selection validation. It reads the selected session's durable room records and its already activated enabled peers; it never starts a controller, claims a peer, creates a route, or joins a participant while browsing. Closed, stale, unreachable, and ambiguous records cannot be selected. Incomplete peer lookup is shown explicitly. Confirmed rooms remain selectable; selection revalidates only the selected authoritative host and the room’s original host participant session, including delayed queued requests. Manual ID lookup still requires complete discovery to avoid choosing the wrong host. Removed or changed saved profiles cannot silently reuse an old peer. The original agent and selected room are checked again before submission. Sibling local Herdr sessions and cached coordinator room records are not scanned.

Numbered selection rejects legacy forwarded participants whose room host cannot be proved. Their existing sessions remain usable; manual room-ID entry can reuse the original participant without creating another one.

A same-host room joins directly without SSH. Cross-host Join persists a request, reports its marker through the native socket, and waits for the local coordinator. The coordinator acquires both the message and HTTP artifact routes before invoking the fixed host-local Join helper. Both initiating-host directions work: local room with remote participant uses two reverse forwards; remote room with local participant uses two local forwards. Joining between two different remote hosts is unsupported. A remote-only UI needs the local controller's saved SSH profile active; the remote popup never starts another controller. Opening or refreshing its Join picker queues a read-only catalog request on the participant host. The existing controller subscription collects authoritative local and peer rooms and publishes the response back to that host. The picker lists controller-hosted rooms without copied IDs, rechecks the selected room through a fresh request, and carries its host/session pin into Join. Browsing creates no adapter, prompt, or SSH port forward. Same-host rooms remain usable during controller failure; discovery warnings identify missing activation, unavailable response, or helper errors. Rooms on other remote hosts are shown as unsupported for a remote participant.

The form shows a request ID for pending or uncertain work. Inspect the original host's durable status before retrying:

```sh
parley herdr status
```

Pending requests remain saved if the popup times out. Lost events trigger a fresh acknowledged subscription and durable queue read. Duplicate delivery reuses the original binding/session; an unavailable route or disconnected adapter remains unavailable. Parley does not blindly recreate a participant or reset its cursor. Direct Join publishes its outcome before reporting completion. Completed receipts survive a later controller assignment; incomplete ownership conflicts require explicit recovery.

Plugin state and config use the full canonical Herdr socket namespace, including when named sessions share plugin roots. A private immutable runtime-root pin keeps native actions and SSH helpers on the same trusted Parley runtime. Requests cannot select a runtime root, executable, or socket. For an isolated fixture, set a short trusted `PARLEY_STATE_DIR` before activation and native actions; long runtime paths can exceed Unix socket limits.

Detaching the Herdr UI leaves the owned coordinator, event readers, and SSH masters running. Disabling the local plugin closes only those owned readers and masters. Disabling a remote plugin or profile closes that session's owned reader and master; its existing participant stays alive, with unavailable routes. Profile refresh runs every 30 seconds; plugin status checks run every two seconds. Empty queues do not trigger room discovery or forwarding.

### Automatic agent handoff

After confirmed Start/Join, the participant host calls Herdr `agent.prompt` for the captured terminal. The short handoff supplies the room, topic, existing session, bundled Parley skill, executable, and pinned runtime root. The skill covers session reuse and communication: check the inbox for task context, continue work, and send useful task messages without automatic join announcements or acknowledgement-only replies. No session ID copying or second agent-side Join is needed. An operation that completes after the popup closes still submits its instructions.

The popup reports **submitted**, **failed**, or **uncertain** separately from room creation. Submitted means Herdr accepted the input; it does not prove the agent finished processing it. Each harness controls its normal input behavior, including input while working. Parley adds no next-turn queue, agent restart, hook change, or receive worker.

Known pre-submission failures such as a blocked approval dialog are recorded. After resolving that dialog, **R + Enter** in the result popup retries only the instructions, preserving the participant and cursor. Reopening Join also shows the recorded result for that participant. Timeout, lost acknowledgement, or corrupt receipt stays uncertain and is never automatically retried. `parley herdr status` exposes durable handoff results keyed by participant session ID, including late completions. Agent sandbox permissions can still require normal user approval for participant socket access.

### Automatic terminal cleanup

Parley subscribes to Herdr's `pane.closed`, `pane.exited`, `pane.moved`, `tab.closed`, and `workspace.closed` events. These events trigger an authoritative `session.snapshot` scan; event payloads, pane IDs, focus, and agent status do not identify an owned terminal. Parley matches the persisted terminal ID, harness kind, and Herdr socket. A pane move preserves its terminal ID and does not trigger cleanup. A replacement pane or changed harness cannot match the saved identity.

When a snapshot proves the original terminal is absent, the host helper takes the same terminal transaction lock as Start and Join. It leaves each completed participant with its persisted Parley session ID. If the terminal owns a room, it stops that exact host session and waits for the room-stopped record and control socket shutdown before marking the room closed. The existing broker reconciliation then releases only that room's Parley-owned SSH routes. Other participants and rooms stay untouched.

The coordinator scans on event subscription startup, lifecycle events, reconnection, and its two-second reconciliation loop. This recovers missed events and retries pending cleanup after restart. An unavailable snapshot or SSH helper preserves the room and participant. Failed cleanup appears as `cleanup_status: "pending"` and `cleanup_error` in `parley herdr status`. Idle/readiness changes and a harness ending while its shell remains open do not trigger cleanup; Herdr 0.9.3 exposes no definitive agent-end event for that case, so cleanup waits for terminal closure.

### Update and rollback

For registrations made with `parley herdr install`:

1. Finish active room work. Disable Parley on the host that manages SSH connections with `herdr plugin disable parley`. Wait for its old `parley herdr broker` processes to exit; check `ps -axo pid,command`. Disabling affects every Herdr session for that OS user.
2. Upgrade the CLI on every host using its original install method. Use the same release everywhere.
3. On each other host, run `herdr plugin disable parley`, then `parley herdr install`.
4. On the host that manages SSH connections, run `parley herdr install`, then `parley herdr setup` in the intended Herdr session.

To roll back, disable Parley and wait for old coordinators to exit. Run `herdr plugin link <previous-plugin-root> --enabled` on each host, then `<previous-plugin-root>/bin/parley herdr setup` on the host that manages SSH connections only. Get `plugin_root` from `herdr plugin list --json` before upgrading and keep those directories.

The remaining procedure applies to explicitly downloaded pinned bundles.

The bundle installer only stages files. It does not link/enable plugins, run setup, restart coordinators, edit configuration, touch standalone binaries, or change SSH authentication. Keep the old release directories: running coordinators/agents may still reference their executable and skill paths.

Record each host's currently linked `plugin_root` with `herdr plugin list --json`. Stage the new **same tag on all hosts** with the download/staging commands above (stop before the first-install link/enable commands), before changing any registration. If staging fails, keep using the old registration. Plugin registration and enabled state are global to the OS user across Herdr sessions. Before activation, finish routed work in every affected session: disabling or relinking the plugin can make routes unavailable. Do not restart Herdr or agents.

After routed work is finished, disable only Parley on the controller. Relink/enable the matching staged bundles on participants, then relink/enable the controller and run its setup:

```sh
# Controller first, after draining routed work:
herdr plugin disable parley
```

Keep the controller plugin disabled until all its previous coordinator processes exit. Use `ps -axo pid,command` to identify the exact old bundle executable running `herdr broker --native-session <name>` for each affected session; confirm those PIDs disappear before continuing. The coordinator observes disable on its two-second polling loop, and shutdown may wait for owned work to finish. A quick disable/enable can leave the old process running, even when setup reports success for the same protocol. If it does not exit, keep Parley disabled and inspect its coordinator log; do not replace a live coordinator.

Then activate the staged bundles:

```sh
# Each participant: NEW_ROOT is its staged absolute bundle directory. No setup.
herdr plugin link "$NEW_ROOT" --disabled
herdr plugin enable parley

# Controller last: NEW_ROOT is this host's staged absolute bundle directory.
herdr plugin link "$NEW_ROOT" --disabled
herdr plugin enable parley
"$NEW_ROOT/bin/parley" herdr setup
```

Herdr link keeps the plugin ID `parley` and its separate config/state directories. Do not uninstall, delete config/state, or copy another host's files. Existing controller/participant ownership, runtime-root pins, rooms, participant IDs, and cursors remain stored. After relinking, run setup only in each existing controller session to restore its coordinator; never run setup in participant sessions. Plugin restart closes owned SSH routes; an existing disconnected participant is not recreated or silently repaired. Use new disposable work to validate the upgraded flow, and inspect durable status for prior work.

For rollback, finish any new routed work, disable Parley on the controller, confirm its coordinators exit, and repeat the same sequence using each host's recorded **old root**. Enable participants first and run setup only on the controller. Retain the same compatible release across hosts. This distribution change does not migrate runtime data; rollback to a release with incompatible runtime data is not supported. Neither update nor rollback restarts agents or Herdr.

### Room message fallback

Source builds with helper protocol 5 add delayed inbox reminders. Agents still own normal listening, inbox reads, and room replies. The local coordinator sends a fixed reminder only after a verified native conversation has remained ready (`idle` or `done`) with unread messages for 30 seconds. It does not inject room message bodies or consume inboxes. Pane focus does not pause reminders.

Successful Start/Join attempts enroll the detected native conversation separately from routing membership. The popup reports `enrolled`, `identity_unavailable`, `registration_pending`, or `coordinator_unavailable`. `enrolled` means that identity was recorded and its host-local invalidation was accepted; it does not guarantee that a remote coordinator is currently reachable. A failed invalidation keeps the enrollment for later reconnect or refresh. The configured local coordinator must be running for both local and remote fallback.

Existing participants without a verified native reference remain ineligible. To enroll one explicitly, run this on its participant host in the intended Herdr server session:

```sh
parley herdr notification-enroll --participant-session <parley-session-id>
```

Enrollment never overwrites an already recorded conversation with a replacement. If Herdr cannot report the original native conversation, fallback stays suspended; ordinary Parley commands remain available.

Inspect the controller's `parley herdr status` output for `notifications` and `notification_attempts`. Notification coverage is distinct from the participant's seen cursor. There is one outstanding reminder per binding; later messages do not create repeated nudges until the covered inbox batch is consumed. An interrupted submission remains `prepared` or `uncertain` because terminal input might already have arrived. Only an explicit retry authorizes another attempt:

```sh
# Run on the local coordinator host/session. Previous input may already have arrived.
parley herdr notification-retry --attempt <attempt-id>
```

A retry waits through fresh readiness and unread grace, preserves all rooms covered by a shared attempt, and repeats identity checks. It does not acknowledge messages. A rejected retry retains the original delivery uncertainty; further retry must explicitly name the rejected replacement's attempt ID. First-attempt rejections that sent no input can be reconsidered after a new grace period.

SSH loss invalidates the live pane mapping, pending timers, and readiness history. After reconnect, the coordinator searches fresh Herdr state for exactly one instance of the original native conversation, verifies its current terminal/pane and original participant connectivity, and starts a fresh grace period. It never assumes the original agent remains in the old pane. A shell, replacement conversation, missing native reference, or ambiguous match receives no fallback input. An enrolled conversation that moved to another terminal is protected from cleanup of its old terminal while its identity is verifiable.

Reboot recovery remains separate. Herdr restoring a pane or resuming a conversation does not restart the old Parley adapter or background listener. A disconnected participant stays suspended; this feature does not rejoin, reset cursors, replay messages missed during downtime, or launch agents. Closing SSH during a prompt request does not prove the remote helper stopped, so that attempt is never blindly repeated.

Current Herdr APIs still have a final race between live verification and prompt submission. The helper rechecks the authorized readiness sequence and native identity immediately before input, but atomic delivery guarded by conversation identity requires upstream API support.

The [disposable end-to-end suite](herdr-e2e.md) runs real Herdr servers, OpenSSH helpers/forwards, original Parley adapters, and terminal input recorders in two isolated host namespaces. Its coverage matrix and execution evidence distinguish real transport and process-restart checks from deterministic fault tests. Physical host reboot, GUI shortcuts and LLM behavior remain outside that suite. Installing this feature or restarting an existing coordinator is an explicit deployment step; development tests do neither.

### Upgrading the helper protocol

Source builds with room fallback use helper protocol 5; published v0.3.0 bundles use protocol 4. The installer bundled with this source checks for protocol 5, and runtime activation checks matching protocol/capabilities before participant mutation. Install matching new helpers on the controller and each participant before enabling fallback. Use one release across hosts to keep the manifest, helper and skills aligned, even though the runtime protocol does not require exact version equality.

A running older coordinator is never replaced silently: doing so would close active SSH routes. After installing the new matching bundles, follow [update and rollback](#update-and-rollback). When staying on the same linked source root, finish routed work, rebuild, then disable only Parley, confirm its coordinators exit, then enable it and run that root's `bin/parley herdr setup` on the controller. `coordinator_upgrade_required` remains visible until the explicit plugin restart. Participant setup is never required.

Native API contracts follow the [Herdr 0.9.3 plugin documentation](https://github.com/herdrdev/herdr/blob/v0.9.3/docs/next/website/src/content/docs/plugins.mdx) and [socket API](https://github.com/herdrdev/herdr/blob/v0.9.3/docs/next/website/src/content/docs/socket-api.mdx).
