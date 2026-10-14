# Herdr Start and Join

For a review checkout, use the [local test procedure and verifier](herdr-local-test.md) to exercise the real shortcuts and both message/artifact endpoints.

Install Parley on every participating host. Herdr 0.9.3 or newer, Go, and OpenSSH are required. The first manifest builds the CLI during installation.

```sh
herdr plugin install khaiql/parley
parley herdr setup
```

Run setup on the local controller, with the intended Herdr session selected. Setup records a unique controller identity for that server socket and starts its detached coordinator immediately, including when the server is already running. Later Herdr server startups restore that coordinator. Linking or enabling a plugin does not run Herdr startup hooks; run setup after first activation. Unconfigured and participant sessions do not launch a coordinator.

For development, build before linking the absolute checkout; link does not run builds:

```sh
go build -o bin/parley ./cmd/parley
herdr plugin link /absolute/path/to/parley
parley herdr setup
```

Enable the intended saved SSH profiles in Herdr. Each remote profile must identify its named Herdr session, or select one uniquely verified running session. Install/link the same plugin on those hosts normally. Run `parley herdr setup` only on the controller (the Mac in a Mac-to-Ubuntu flow), not on its remote participants; setup on both hosts creates conflicting controllers. The coordinator verifies the installed helper and claims its session before subscribing. It does not copy a helper to a remote host. Its helper and tunnel connections require noninteractive SSH authentication; verify `ssh -o BatchMode=yes <saved-target> true`. A Herdr connection authenticated with a server password does not by itself authenticate those separate connections. Fixed helper calls use the login shell environment so Herdr installed in `~/.local/bin` is found. A different remote controller owner, missing helper, disabled plugin, incompatible capability, authentication failure, or host-key refusal remains visible in the coordinator log. Configure SSH permission and trust normally; Parley never bypasses either.

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

Notifications follow Herdr's configured toast delivery. To display them inside Herdr, use `delivery = "herdr"` in your existing `[ui.toast]` table, then reload configuration. With delivery disabled, no foreground client, a busy toast, or rate limiting, delivery can fail; the original diagnostic includes `notification_failed` and the native reason. An unavailable socket also prevents notification; it never falls back to another local session. If the Parley executable cannot be launched at all, inspect Herdr's own action launch log and repair the plugin build/install:

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

### Upgrading from the identity-based helper

This flow uses helper protocol 3. Install/build this plugin on every participating host; incompatible helpers are rejected before participant mutation. Exact binary version equality is unnecessary, but all helpers and the controller must support protocol 3.

A running older coordinator is not replaced silently: doing so would close its active SSH routes. Finish existing routed work, rebuild the plugin, then restart **only the Parley plugin** on the controller and run setup:

```sh
herdr plugin disable parley
herdr plugin enable parley
parley herdr setup
```

Agents and Herdr servers remain running. Existing rooms, participant IDs, runtime pins, ownership, and cursors remain stored; routes close during the plugin restart. Startup and local cross-host submission report `coordinator_upgrade_required` until this restart. No participant is recreated to repair an incompatible helper.

Native API contracts follow the [Herdr 0.9.3 plugin documentation](https://github.com/herdrdev/herdr/blob/v0.9.3/docs/next/website/src/content/docs/plugins.mdx) and [socket API](https://github.com/herdrdev/herdr/blob/v0.9.3/docs/next/website/src/content/docs/socket-api.mdx).
