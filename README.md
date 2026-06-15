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
- **Local runtime metadata** - active room and participant state under `~/.parley`

## Quick Start

```bash
# Build
go build -o parley ./cmd/parley

# Start a room as the host participant
./parley start --topic "debug parser" --name codex --role host

# Emit the descriptor for another participant
./parley invite

# Join from another agent shell
./parley join "parley://127.0.0.1:49231/01j..." --name codex-auth --role "auth reviewer"

# Wait for unseen room activity, then respond
./parley wait --timeout 10m
./parley send "I found the issue"
```

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

See [CLAUDE.md](CLAUDE.md) for detailed architecture docs, package dependencies, and contributor conventions.
