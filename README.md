# Parley

Parley is a headless, JSON-first room server for coordinating coding agents through short CLI commands.

## What It Does

Parley starts a local room, writes a JSONL event log, and gives each participant a local adapter with a Unix control socket. Agent skills can call small commands such as `wait`, `inbox`, `send`, and `history` without owning a long-running terminal UI.

## Features

- **Headless rooms** - TCP room server with line-delimited JSON protocol
- **Participant adapters** - local mirrors and control sockets per agent
- **JSON command output** - scriptable responses for agent skills and automation
- **Event log first** - append-only room history with transcript filtering
- **Descriptors** - `parley://host:port/room-id` invite strings for handoff
- **Local runtime metadata** - session-scoped room and participant state under `~/.parley`

## Quick Start

### Install

With Homebrew on macOS:

```bash
brew install --cask khaiql/parley/parley
```

With the installer on macOS or Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/khaiql/parley/main/install.sh | sh
```

With Go:

```bash
GOBIN="$HOME/.parley/bin" go install github.com/khaiql/parley/cmd/parley@latest
```

The installer and Go commands place the binary at `~/.parley/bin/parley`.

```bash
PARLEY="$(command -v parley 2>/dev/null || printf '%s\n' "$HOME/.parley/bin/parley")"
```

### Use

```bash
# Start a room as the host participant
"$PARLEY" start --topic "debug parser" --role host
SESSION_ARGS="--session psn_..."

# Emit the descriptor for another participant
"$PARLEY" invite $SESSION_ARGS

# Join from another agent shell
"$PARLEY" join "parley://127.0.0.1:49231/01j..." --role "auth reviewer"

# Save the command_args returned by start or join, then use it for room and participant commands
SESSION_ARGS="--session psn_..."
"$PARLEY" wait $SESSION_ARGS --timeout 10m
"$PARLEY" send $SESSION_ARGS "I found the issue"

# Recover local session handles if needed
"$PARLEY" sessions
```

`start` and `join` generate a participant name when `--name` is omitted, using `adjective_noun_number` format. Prefer `--session` for room and participant commands. Use `sessions` to list local session handles on the machine. Use `--room` and `--name` as an explicit fallback for participant commands. Bare participant commands only work when exactly one local participation exists.

`wait` is non-consuming: it blocks until unread message events are available and returns them with `status: "ready"`, but only `inbox` advances the seen cursor. Use `inbox --peek` to inspect unread events without acknowledging them.

For remote participants, create your own tunnel to the `local_port` returned by `start` or `invite`, then share a descriptor that uses the tunnel host and port with the same room id. Parley does not create or manage tunnels.

## Agent Skill

Parley ships a Codex-compatible skill at `skills/parley/SKILL.md`. Agents should run `skills/parley/scripts/ensure-parley` before every Parley workflow, use the binary path it prints, and branch on command JSON `status` values for descriptors, inbox events, wait results, and errors.

## Architecture

```
cmd/parley/         CLI commands and JSON error handling
internal/
  model/            Event envelope, payloads, participant and room metadata
  descriptor/       parley:// descriptor parse/format helpers
  paths/            Per-user room paths and file permissions
  jsonout/          JSON success and error response helpers
  eventlog/         Append/read/query JSONL event logs
  protocol/         V1 request/response/event wire types over NDJSON
  server/           Headless TCP room server and local control socket
  adapter/          Participant TCP adapter, mirror log, and control API
  runtime/          Active room, invite, and participant runtime metadata
  e2e/              Headless room integration coverage
```

### How It Works

```
parley start -> room server -> event log
      |             ^             |
      v             |             v
 host adapter <- NDJSON -> participant adapter
      |                           |
      v                           v
 parley send/wait         parley send/wait
```

The server assigns sequence numbers, appends room events, and broadcasts them to connected adapters. Each adapter keeps a local event mirror and exposes a Unix control socket so short-lived CLI commands can inspect state, wait for unseen messages, or send responses.

## Development

```bash
go test ./... -timeout 30s
go test ./... -timeout 30s -race
go test ./internal/e2e -run TestHeadlessRoomTwoParticipants -count=100
```

## Release

Push a SemVer tag such as `v0.1.0` to run the GoReleaser workflow. The workflow publishes GitHub release archives and updates the Homebrew cask in `khaiql/homebrew-parley`; it requires a `HOMEBREW_TAP_GITHUB_TOKEN` secret with contents write access to that tap repository.

See [CLAUDE.md](CLAUDE.md) for detailed architecture docs, package dependencies, and contributor conventions.
