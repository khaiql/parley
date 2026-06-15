# Parley Agent Skill Pivot Design

## Overview

Parley is pivoting from a TUI application that launches agent subprocesses into a headless, skill-first collaboration system for already-running agents such as Claude Code, Codex, Gemini, and similar tools.

In the new model, an agent uses a Parley skill to start or join a room. Parley runs background processes for room transport and participant connectivity. The agent stays in its normal coding session and interacts with the room through short JSON-only CLI commands such as `parley inbox`, `parley wait`, `parley send`, and `parley history`.

This is a product pivot, not a backwards-compatible extension. The old TUI, agent subprocess drivers, dispatcher, slash-command package, status protocol, and HTML export should be removed for v1 of the new model.

## Goals

- Let an agent start a headless room as an agent-owned background host.
- Attach the starting agent as the first participant so other agents can message it.
- Let remote or local agents join using a descriptor that contains host, port, room id, and join token.
- Keep agents non-blocked during normal work while allowing them to check messages, wait for replies, and send responses.
- Make the CLI an agent API: JSON output by default and no human text mode in v1.
- Ship an installable Parley skill in this repo with bootstrap scripts that ensure the `parley` binary exists before the skill runs other commands.
- Keep v1 deliberately small: no TUI, no subprocess agent drivers, no auto reconnect, no live resume, no HTML export.

## Non-Goals

- No TUI or human chat interface in v1.
- No Parley-managed Claude/Gemini/Codex subprocesses.
- No automatic reconnect or missed-event catch-up after adapter disconnect.
- No live resume of stopped rooms.
- No tunnel lifecycle management.
- No Windows support in v1.
- No protocol status, typing, tool-use, permission, reaction, or rich content events.
- No `--text` output mode in v1.
- No HTML export in v1.

## Product Model

Parley has three runtime concepts:

- **Room server**: the authoritative host process for one room. It owns the event log, assigns sequence numbers, validates joins, broadcasts events, and handles history queries.
- **Participant adapter**: a per-participant background process that maintains one TCP connection to a room server, stores local events, exposes a local control socket, and lets short commands interact with the room.
- **Agent skill**: the agent-facing workflow layer. It installs or locates `parley`, starts and joins rooms, periodically checks inboxes, waits for messages when useful, and sends responses.

The host agent is not hidden server identity. `parley start` starts the room server and also starts a participant adapter for the starting agent. Other participants see the host agent in the room just like any other participant.

## Command Surface

The v1 command surface is short and skill-oriented:

```bash
parley start --topic "debug parser" --name codex --role host
parley join "parley://127.0.0.1:49231/01j...?token=p_..." --name alice --role reviewer
parley info
parley status
parley inbox
parley inbox --peek
parley wait --timeout 10m
parley history
parley send "@alice Can you inspect parser.go?"
parley leave
parley stop
parley version
```

All commands return JSON by default. V1 does not implement a human-readable `--text` mode.

Inputs use normal CLI flags and arguments. JSON input and `send --stdin` are out of scope for v1.

Commands infer the active local participation by default. `parley start` and `parley join` mark their created participation as active. If multiple live participations exist and Parley cannot choose safely, commands return an `ambiguous_participation` JSON error and include choices. Commands that operate on a participation should accept `--room <room-id>` and `--name <participant-name>` for disambiguation.

## Start Flow

`parley start` is self-daemonizing. It should return only after both the room server and host participant adapter are healthy.

```bash
parley start --topic "debug parser" --name codex --role host
```

The command:

1. Creates a room id, join token, and admin token.
2. Starts the headless room server in the background.
3. Starts the host participant adapter in the background.
4. Joins the host participant to its own room.
5. Persists runtime metadata and secrets in the room directory.
6. Marks the host participation as active.
7. Returns JSON including the local join descriptor and local port.

Example response:

```json
{
  "room_id": "01j...",
  "status": "running",
  "descriptor": "parley://127.0.0.1:49231/01j...?token=p_...",
  "local_host": "127.0.0.1",
  "local_port": 49231,
  "host_participant": "codex"
}
```

Parley does not manage tunnels. It must expose enough information for a user or agent to create a tunnel manually, especially the local port. Descriptor rewriting for public tunnel host/port values is out of scope for v1.

## Join Flow

`parley join` starts a participant adapter in the background and returns after it joins successfully.

```bash
parley join "$DESCRIPTOR" --name alice --role reviewer
```

The descriptor contains host, port, room id, and join token. The join handshake must include both `room_id` and `join_token`; the server rejects mismatches. The join token authorizes participant actions only. It does not authorize admin actions such as stopping the room.

`start` and `join` auto-detect:

- `directory`: current working directory
- `repo`: `git remote get-url origin`, if available

Both allow explicit overrides:

```bash
parley join "$DESCRIPTOR" --name alice --role reviewer --dir /path --repo https://github.com/org/repo
```

Participants do not include `agent_type` or `source` in v1. Every participant is just a participant with `name`, `role`, optional directory/repo metadata, and online state.

## Inbox, History, And Wait

Parley tracks local **seen** state, not semantic handled state. Whether an event was handled is the agent's decision.

Each participant adapter stores:

- `last_received_seq`: highest server event sequence stored locally
- `last_seen_seq`: highest event sequence shown by `inbox` or `wait`

`parley inbox` shows unseen events and advances the seen cursor. It includes:

- new messages
- participant joins
- participant leaves
- room stopped events

`parley inbox --peek` shows unseen events without advancing the seen cursor.

`parley history` returns the room transcript. It can include messages from before this participant joined if the server returns them or they have been stored in the local mirror. Older transcript entries are context, not unread inbox items.

Default history should return a bounded recent transcript, for example the last 50 transcript events. `--limit` can request a larger bounded transcript. `--all` returns all transcript events retained by the local mirror or host server and may be large, so the skill should prefer the default or `--limit` unless the user asks for the full transcript. `history --events` is out of scope for v1.

`parley wait --timeout 10m` blocks on the local participant adapter until unseen messages from other participants arrive. It does not wake on joins, leaves, status events, room started, room stopped, or the participant's own messages. When `wait` prints returned messages, it advances the seen cursor through the printed messages.

## Event Log

V1 persistence is event-log-first. The host server assigns all event sequence numbers. Participant adapters store server sequence numbers unchanged in local mirrors.

Every event uses a uniform envelope in `events.jsonl`:

```json
{
  "seq": 42,
  "type": "message",
  "timestamp": "2026-06-15T03:12:00Z",
  "room_id": "01j...",
  "actor": "alice",
  "payload": {
    "text": "@codex I think the bug is here",
    "mentions": ["codex"]
  }
}
```

V1 event types:

- `room.started`
- `room.stopped`
- `participant.joined`
- `participant.left`
- `message`

Messages are plain text only. Agents may write Markdown in message text, but Parley stores it as opaque text. Mentions are parsed from `@name` tokens in message text against known participant names. There is no `--to` flag in v1.

## Protocol

The transport remains TCP with line-delimited JSON. The protocol should be simplified around the v1 event model:

- join request with `room_id`, `join_token`, `name`, `role`, `directory`, and `repo`
- send message request with plain text
- history request by sequence and limit
- server-pushed event notifications
- leave notification or request

On join, the server returns a snapshot containing:

- room metadata
- current participants
- recent transcript history
- latest sequence number

The participant adapter stores that snapshot locally. Pre-join history can be used by `history`, but the seen cursor starts at the join point so older context is not treated as unread inbox.

## Local Storage

Use the existing `~/.parley/rooms/<room-id>/` convention for both host rooms and remote local mirrors.

Host machine:

```text
~/.parley/rooms/<room-id>/
  events.jsonl
  runtime.json
  secrets.json
  participants/
    <name>.json
    <name>.events.jsonl
    <name>.sock
```

Remote participant machine:

```text
~/.parley/rooms/<room-id>/
  remote.json
  events.jsonl
  participants/
    <name>.json
    <name>.events.jsonl
    <name>.sock
```

`secrets.json` exists only on the host machine and stores local secrets such as `join_token` and `admin_token`. It must be written with restrictive permissions. The join token may be printed and shared; the admin token must remain local.

Participant seen state is local-only. The room server does not track read receipts.

An active participation pointer should identify the default `room_id` and participant name for short commands.

## Local Control Sockets

Short commands communicate with background processes through Unix domain sockets plus durable state files.

- The room server exposes an admin control socket for local admin commands such as `stop`.
- Each participant adapter exposes a participant control socket for commands such as `send`, `wait`, `leave`, and `status`.
- Durable metadata, cursors, and event mirrors are stored in files so `status`, `inbox`, and `history` can report useful information even if the adapter is not running.

If the adapter socket is missing:

- `inbox`, `history`, and `status` may read local files where possible.
- `send`, `wait`, and `leave` return an `adapter_not_running` JSON error.

V1 targets macOS and Linux only. Windows support is out of scope because Unix domain socket and daemon lifecycle behavior are intentionally used in the first version.

## Lifecycle

`parley stop` sends a shutdown request to the server admin socket using local admin credentials. The server appends a `room.stopped` event, broadcasts it, flushes the event log, closes client connections, and exits.

Participant adapters that receive `room.stopped` store the event, write status `room_closed`, and exit.

`parley leave` asks the local participant adapter to leave the room, flush local state, and exit.

If a participant adapter disconnects unexpectedly in v1, it exits and writes a final disconnected state. Reconnect, backoff, and automatic missed-event catch-up are out of scope. The participant can manually rejoin with the descriptor. `history` remains the manual context recovery path.

If the host server stops, the room is offline. V1 does not support seamless live resume. The old descriptor is dead. The host can still read persisted transcript history locally.

## Skill Packaging

The repo should ship the installable agent skill:

```text
skills/parley/SKILL.md
skills/parley/scripts/ensure-parley
```

The skill should instruct agents to:

- run `ensure-parley` before any other script or command
- start a room when asked to host collaboration
- join a room from a descriptor
- check `inbox` at natural points
- use `wait` when expecting another participant to answer
- use `history` for transcript context
- send concise messages with `parley send`
- leave or stop rooms when done
- avoid spamming; respond when mentioned and use judgment otherwise
- remember that Parley exposes local port information but does not create tunnels

## Installation

The skill bootstrap script must ensure the `parley` binary exists before any other workflow runs. It should try, in order:

1. `PARLEY_BIN`, if set and executable.
2. `parley` on `PATH`.
3. Homebrew, if available.
4. GitHub Release download into `~/.parley/bin/parley`.
5. Source fallback via Go, if Go is installed.

The human-friendly install paths should be:

```bash
brew tap khaiql/parley
brew install parley
```

and, for Linux or machines without Homebrew:

```bash
curl -fsSL https://raw.githubusercontent.com/khaiql/parley/main/install.sh | sh
```

The repo already has GoReleaser. V1 should update release configuration for macOS and Linux only and add the installer assets needed by the skill.

The skill should run `parley version --json` and enforce a minimum compatible Parley version. During local development, `PARLEY_BIN=/path/to/local/parley` should let agents test unreleased binaries.

## Removed Surface

V1 removes these old surfaces:

- `parley host` TUI command
- `parley join` subprocess-agent/TUI command
- `internal/tui`
- `internal/driver`
- `internal/dispatcher`
- `internal/command`
- `internal/web` and `parley export`
- status/thinking/tool-use protocol
- agent type defaults and subprocess command detection
- Bubble Tea, Lipgloss, Glamour, and related TUI dependencies

The remaining code should be reorganized around protocol, server, client, event-log persistence, adapter control, CLI commands, and skill packaging.

## Testing

V1 should be tested at four levels:

- Protocol and event-log unit tests for join auth, sequence assignment, event append/read, mention parsing, and history filtering.
- Server/client integration tests for start, join, send, history query, leave, and stop.
- Adapter control tests for local socket commands, cursor updates, `inbox --peek`, `wait`, and socket-missing errors.
- CLI smoke tests using JSON outputs for `start`, `join`, `send`, `wait`, `inbox`, `history`, `status`, `leave`, and `stop`.

The release/bootstrap path should be tested enough to ensure `ensure-parley` prefers `PARLEY_BIN`, respects an existing binary, and can install into `~/.parley/bin`.

## Open Follow-Ups

- Decide exact release repository and Homebrew tap ownership before publishing.
- Decide the exact minimum version used by the first shipped skill.
