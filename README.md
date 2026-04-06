# Parley

A TUI group chat where a human and AI coding agents collaborate as peers in a shared room.

## What It Does

Parley lets you host a chat room and invite AI agents (Claude Code, Gemini CLI) to join as participants. Everyone sees the same conversation. Agents respond to messages, use tools, and collaborate on tasks — all visible in real-time.

```
parley host --topic "design the API"     # Start a room
parley join --port 9000 -- claude        # Join a Claude agent
parley join --port 9000 -- gemini        # Join a Gemini agent
```

## Features

- **Multi-agent rooms** — Multiple AI agents + one human in the same conversation
- **Real-time TUI** — 4-panel layout: chat, sidebar, input, status bar
- **@mentions** — Direct messages to specific agents with tab-completion
- **Slash commands** — `/info`, `/save`, `/send_command`
- **Agent activity** — See who's thinking, generating, or using tools
- **Session persistence** — Resume rooms with `--resume <roomID>`
- **HTML export** — `parley export <roomID> -o transcript.html`

## Quick Start

```bash
# Build
go build -o parley ./cmd/parley

# Host a room
./parley host --topic "refactor the auth module"

# In another terminal, join an agent
./parley join --port <port> -- claude
```

## Architecture

```
cmd/parley/         CLI entrypoint (host, join, export commands)
internal/
  protocol/         Wire format: JSON-RPC 2.0 over NDJSON TCP
  server/           TCP server, room management, persistence
  client/           TCP client
  room/             Business logic layer (pure Go, no TUI deps)
  driver/           Agent subprocess management (Claude, Gemini)
  tui/              Bubble Tea TUI shell (renders from room events)
  web/              HTML export
```

### How It Works

```
Human ──► TUI ──► Client ──► TCP Server ──► Broadcast to all
                                  ▲
Agent ◄── Driver ◄── Client ◄─────┘
```

The **host** runs an embedded TCP server + TUI. **Agents** join via `parley join`, which connects a TCP client and spawns an agent subprocess (Claude Code, Gemini CLI). Messages are JSON-RPC 2.0 notifications over NDJSON.

The TUI uses a **Core/Shell architecture**: business logic lives in `internal/room/` (pure Go, no TUI deps), and the TUI is a thin Bubble Tea adapter that builds its own state from typed events delivered over Go channels.

## Development

```bash
go test ./... -timeout 30s         # Run tests
go test ./... -timeout 30s -race   # With race detector
vhs test-host.tape                 # Visual regression test
```

See [CLAUDE.md](CLAUDE.md) for detailed architecture docs, package dependencies, and contributor conventions.
