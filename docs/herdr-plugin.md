# Herdr Start and Join

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

Enable the intended saved SSH profiles in Herdr. Each remote profile must identify its named Herdr session, or select one uniquely verified running session. Install/link the same plugin on those hosts normally. The coordinator verifies the installed helper and claims its session before subscribing. It does not copy a helper to a remote host. A different remote controller owner, missing helper, disabled plugin, incompatible capability, authentication failure, or host-key refusal remains visible in the coordinator log. Configure SSH permission and trust normally; Parley never bypasses either.

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

With the default prefix, press **Ctrl+B**, release, then **Shift+S** for Start or **Shift+I** for Join, while the existing agent pane remains focused. Enter a topic or room ID in the popup. No separate shell pane is needed for normal invocation. Typing `herdr plugin action invoke parley.start` in a shell instead targets the server's current pane, which may be that shell; `running` only acknowledges asynchronous action launch.

Client keybindings normally come from the client's local config even when viewing an SSH machine. With `--remote-keybindings server`, configure the bindings on the selected server instead. Install the plugin and helper on every participating host; the action runs on the host of the focused pane. Notifications use the injected native socket, so remote failures stay in that Herdr context. Diagnostic CLI commands run on their host, or use `herdr --machine <enabled-profile-id-or-label>` from Local; selecting a machine in the UI does not retarget ordinary local commands.

### Native identity and visible failures

The agent must have a native `agent_session`, not just a detected process and an idle badge. In the intended session, check `herdr --session <name> agent get <pane-id>` for `agent_session`. From Local, use `herdr --machine <profile> agent get <remote-pane-id>` for a saved remote machine.

For a fresh Codex CLI 0.160.0 session, `SessionStart` hook delivery is deferred until its first turn. A ready prompt can therefore lack identity even when integration v8 is current and the hook is enabled and trusted. Use an existing Codex agent that has begun ordinary work and has reported its session, or another supported agent with a reported identity. Do not send a paid prompt solely to test Parley. Support before the first turn is tracked in [#131](https://github.com/khaiql/parley/issues/131).

If identity is still absent after normal work, inspect `/hooks` in that Codex session: the Herdr SessionStart hook must be enabled and trusted for its current definition. Check the effective hook sources and execution errors, and that the actual hook process inherits `HERDR_ENV=1`, the correct `HERDR_PANE_ID`, and the correct `HERDR_SOCKET_PATH`. A shared Codex daemon can retain a different pane's environment; see [Herdr #4814](https://github.com/herdrdev/herdr/issues/4814). Do not assume reinstalling hooks or restarting an agent fixes the cause. Parley never changes hook trust, restarts agents, fabricates identities, or reads guessed transcript paths. See [diagnosis and verification evidence](herdr-usability-proof.md).

Preflight and form failures request a silent Herdr notification and retain structured JSON/plugin-log diagnostics. Shell targets explain how to focus an agent; missing identities explain the Codex hook boundary; incompatible helpers and busy modals give recovery steps. Failed submitted outcomes stay visible in the form until Enter and then exit unsuccessfully. Escape or EOF cancels input without a failure notification.

Notifications follow Herdr's configured toast delivery. To display them inside Herdr, use `delivery = "herdr"` in your existing `[ui.toast]` table, then reload configuration. With delivery disabled, no foreground client, a busy toast, or rate limiting, delivery can fail; the original diagnostic includes `notification_failed` and the native reason. An unavailable socket also prevents notification; it never falls back to another local session. If the Parley executable cannot be launched at all, inspect Herdr's own action launch log and repair the plugin build/install:

```sh
herdr --session <name> plugin log list --plugin parley --limit 5
```

### Start and Join

Invoke **Parley: Start room** with its shortcut from an existing agent pane. Enter a nonempty topic and press Enter. The popup captures the original terminal and native agent identity before opening. It creates a local room and shows its room ID, session ID, and existing Parley command instructions. Escape, EOF, empty input, control characters, and topics over 512 bytes do not submit work.

Invoke **Parley: Join room** from another existing agent pane. Enter the room ID. A same-host room joins directly without SSH. Cross-host Join persists a request, reports its marker through the native socket, and waits for the local coordinator. The coordinator acquires both the message and HTTP artifact routes before invoking the fixed host-local Join helper. Both initiating-host directions work: local room with remote participant uses two reverse forwards; remote room with local participant uses two local forwards. Joining between two different remote hosts is unsupported. A remote-only UI needs the local controller's saved SSH profile active; the remote popup never starts another controller.

The form shows a request ID for pending or uncertain work. Inspect the original host's durable status before retrying:

```sh
parley herdr status
```

Pending requests remain saved if the popup times out. Lost events trigger a fresh acknowledged subscription and durable queue read. Duplicate delivery reuses the original binding/session; an unavailable route or disconnected adapter remains unavailable. Parley does not blindly recreate a participant or reset its cursor. Direct Join publishes its outcome before reporting completion. Completed receipts survive a later controller assignment; incomplete ownership conflicts require explicit recovery.

Plugin state and config use the full canonical Herdr socket namespace, including when named sessions share plugin roots. A private immutable runtime-root pin keeps native actions and SSH helpers on the same trusted Parley runtime. Requests cannot select a runtime root, executable, or socket. For an isolated fixture, set a short trusted `PARLEY_STATE_DIR` before activation and native actions; long runtime paths can exceed Unix socket limits.

Detaching the Herdr UI leaves the owned coordinator, event readers, and SSH masters running. Disabling the local plugin closes only those owned readers and masters. Disabling a remote plugin or profile closes that session's owned reader and master; its existing participant stays alive, with unavailable routes. Profile refresh runs every 30 seconds; plugin status checks run every two seconds. Empty queues do not trigger room discovery or forwarding.

Returned session instructions use existing Parley messaging and artifacts. This phase adds no automatic receive hooks, readiness prompts, harness changes, notification workers, participant resume/replay, adapter reconnection, or agent launch.

Native API contracts follow the [Herdr 0.9.3 plugin documentation](https://github.com/herdrdev/herdr/blob/v0.9.3/docs/next/website/src/content/docs/plugins.mdx) and [socket API](https://github.com/herdrdev/herdr/blob/v0.9.3/docs/next/website/src/content/docs/socket-api.mdx).
