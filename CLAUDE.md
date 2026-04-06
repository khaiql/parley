# Project Instructions for AI Agents

This file provides instructions and context for AI coding agents working on this project.

<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:ca08a54f -->
## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full workflow context and commands.

### Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
```

### Rules

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown TODO lists
- Run `bd prime` for detailed command reference and session close protocol
- Use `bd remember` for persistent knowledge — do NOT use MEMORY.md files

## Session Completion

**When ending a work session**, you MUST complete ALL steps below. Work is NOT complete until `git push` succeeds.

**MANDATORY WORKFLOW:**

1. **File issues for remaining work** - Create issues for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **PUSH TO REMOTE** - This is MANDATORY:
   ```bash
   git pull --rebase
   bd dolt push
   git push
   git status  # MUST show "up to date with origin"
   ```
5. **Clean up** - Clear stashes, prune remote branches
6. **Verify** - All changes committed AND pushed
7. **Hand off** - Provide context for next session

**CRITICAL RULES:**
- Work is NOT complete until `git push` succeeds
- NEVER stop before pushing - that leaves work stranded locally
- NEVER say "ready to push when you are" - YOU must push
- If push fails, resolve and retry until it succeeds
<!-- END BEADS INTEGRATION -->

## Build & Test

```bash
go build -o parley ./cmd/parley    # Build binary
go test ./... -timeout 30s         # Run all tests
go test ./... -timeout 30s -v      # Verbose test output
```

### CI Quality Gates

Before creating a PR or finalizing work, ensure CI will pass by running:

```bash
go build ./...                                                                    # Must compile
go run github.com/golangci/golangci-lint/cmd/golangci-lint@latest run ./... --timeout=5m  # Lint (gofmt, unused, etc.)
go test ./... -timeout 30s -race                                                  # Tests with race detector
```

All three must pass. The CI workflow (`.github/workflows/ci.yml`) runs these exact steps.

### VHS Visual Testing

```bash
vhs <tape-file>.tape               # Record TUI to GIF
```

VHS tapes live in the project root. Use them to visually verify TUI changes.

### End-to-End Testing

Use `/e2e-test` to run a full smoke test of the TUI using `agent-tui`. It builds the binary, hosts a room, joins an agent, and verifies message exchange. See `.claude/skills/e2e-test/SKILL.md` for details.

## Architecture Overview

**Parley** is a TUI group chat where a human and coding agents (Claude Code, Gemini CLI, etc.) collaborate as peers.

### Package Map

```
cmd/parley/              CLI entrypoint — wires everything together
  main.go                Cobra root command, shared helpers (detectRepo, defaultParleyDir)
  host.go                Host command: creates server + TUI, persistence auto-save
  join.go                Join command: connects agent to room, dispatcher, driver bridge
  export.go              Export command: HTML transcript

internal/protocol/       Wire format — shared by all packages
  protocol.go            JSON-RPC 2.0 types, NDJSON encoding, Method*/Status* constants
                         Participant, MessageParams, Content, RoomSnapshot, MatchMentions

internal/server/         TCP server — transport layer only
  server.go              TCPServer: accept loop, handleConn routes messages via room.State
  interfaces.go          Server interface (Addr, Port, Serve, Close)
  connmanager.go         ConnectionManager: client connections + broadcast primitives

internal/client/         TCP client — send/receive messages
  client.go              TCPClient: Connect, Join, Send, SendStatus, Incoming() channel
  interfaces.go          Client interface

internal/room/           Business logic — single source of truth, zero TUI dependencies
  events.go              Event types (ParticipantsChanged, MessageReceived, etc.)
                         Activity enum, channel-based pub/sub (Subscribe/emit/Close)
  state.go               Room state: Join, Leave, AddMessage, Restore, RecentMessages
                         Query methods (Participants, GetID, GetTopic, etc.)
  dispatch.go            HandleServerMessage — client-side: translates protocol → events
  commands.go            ExecuteCommand, SendMessage

internal/dispatcher/     Message delivery policies for agent drivers
  dispatcher.go          Dispatcher interface + Debounce implementation
                         Subscribes to room events, debounces non-mentioned messages

internal/persistence/    Room state storage
  persistence.go         Store interface (Save, Load, SaveAgentSession, FindAgentSession)
  json_store.go          JSONStore: saves to ~/.parley/rooms/<id>/ as JSON files

internal/driver/         Agent subprocess management
  driver.go              AgentDriver interface, AgentConfig, AgentEvent types
  claude.go              Claude Code driver (stream-json protocol)
  gemini.go              Gemini CLI driver
  prompt.go              System prompt builder

internal/tui/            Bubble Tea TUI shell — renders from room events
  app.go                 Root model, three-layer key routing, event-sourced state
  inputfsm.go            Input FSM (Normal ↔ Completing) via qmuntal/stateless
  chat.go                Chat viewport with markdown rendering
  sidebar.go             Participant list with activity status + spinner
  suggestions.go         Autocomplete dropdown for / commands and @ mentions
  modal.go               Dismissable overlay for command output
  input.go               Text input (human interactive / agent read-only)
  topbar.go, statusbar.go, styles.go

internal/web/            Web export
  export.go              HTML transcript export from saved room data
```

### How It Works

```
┌──────────────────────────────────────────────────────────┐
│                      cmd/parley                           │
│                                                           │
│  host command:                                            │
│    serverState = room.New()                               │
│    srv = server.New(addr, serverState)                    │
│    tuiState = room.New()   ◄── client-side event emitter  │
│         │           │               │                     │
│         │      c.Incoming()    Subscribe()                │
│         ▼           │               │                     │
│    Serve (TCP)      ▼               ▼                     │
│    (mutates    HandleServerMsg  program.Send()            │
│     serverState                     │                     │
│     under s.mu)                ┌────▼─────┐               │
│         │                      │   TUI    │               │
│         │                      │  (app)   │               │
│         │                      └──────────┘               │
│         │                                                 │
│    persistence.JSONStore ◄── srv.Snapshot()               │
│                                                           │
│  join command:                                            │
│    tuiState = room.New()  ──► dispatcher.Start()          │
│         │          │               │                      │
│    c.Incoming()  Subscribe()   driver.Send()              │
│         │          │               │                      │
│         ▼          ▼               ▼                      │
│    HandleServerMsg  program.Send()  agent stdin/out       │
│                         │                                 │
│                    ┌────▼─────┐                            │
│                    │   TUI    │                            │
│                    │  (app)   │                            │
│                    └──────────┘                            │
└──────────────────────────────────────────────────────────┘
```

### Package Dependencies

```
protocol     ◄── server, client, room, dispatcher, persistence, driver, tui, cmd
command      ◄── room, tui, cmd
room         ◄── dispatcher, tui, cmd     (depends on: protocol, command)
server       ◄── cmd                      (depends on: protocol, room)
client       ◄── cmd                      (depends on: protocol)
dispatcher   ◄── cmd                      (depends on: protocol, room)
persistence  ◄── cmd                      (depends on: protocol)
driver       ◄── cmd                      (depends on: protocol)
tui          ◄── cmd                      (depends on: protocol, command, room)
```

`protocol` is the foundation — all packages depend on it. `room` is the business logic layer (single source of truth). The server borrows `room.State` for mutations; the TUI and dispatcher subscribe to its events.

### Message Flow

1. **Server-side**: `handleConn` receives JSON-RPC over TCP, calls `room.State.AddMessage()` / `Join()` / `Leave()` under `s.mu`, then `ConnectionManager.Broadcast()` fans out the notification
2. **Client-side**: `room.State.HandleServerMessage()` processes incoming notifications, updates internal state, emits typed events (`MessageReceived`, `ParticipantsChanged`, etc.)
3. **Bridge**: A goroutine drains the event channel and calls `tea.Program.Send()` to inject events into the Bubble Tea loop
4. **TUI**: `App.Update()` handles events, builds TUI-local state, and returns commands. `View()` renders from local state — never reads from `room.State`

### Concurrency Model

- `room.State` is **single-threaded** — no internal mutex
- **Server-side**: The server's `s.mu sync.Mutex` serializes all `room.State` mutations from multiple `handleConn` goroutines
- **Client-side** (host TUI, join TUI): One goroutine calls `HandleServerMessage` from the network loop
- **Host process**: Two `room.State` instances — `serverState` (authoritative, server-owned) and `tuiState` (client-side, fed by network). Persistence reads via `srv.Snapshot()` which acquires `s.mu`
- **ConnectionManager**: Has its own `sync.RWMutex` for connection map access

### Key Design Decisions

- Server is embedded in the `host` process (no separate daemon)
- **Single source of truth**: `room.State` owns all business logic (Join, Leave, AddMessage, etc.). The server is pure transport.
- **Core/Shell split**: Room business logic (`internal/room/`) has zero TUI dependencies — reusable by web UI (#51)
- **Event-sourced TUI**: The TUI builds its own state from room events, no shared mutable pointers
- **Dispatcher pattern**: Message delivery to agents is a pluggable policy (`internal/dispatcher/`). Currently debounce-based; extensible to priority, batching, etc.
- **Persistence interface**: `persistence.Store` decouples storage from file format. `JSONStore` is the default implementation.
- **Input FSM**: Explicit state machine for input modes (Normal ↔ Completing) via `qmuntal/stateless`
- **Three-layer key routing**: Overlay → Permission → Input FSM, with consumed-bool early returns
- **Reactive spinner**: Self-terminating tick pattern, no `spinnerActive` flag
- Agent drivers abstract different agent communication patterns (stdio)
- Agents self-regulate responses via system prompt (no server-side turn-taking)

## Conventions & Patterns

- **Go module**: `github.com/khaiql/parley` (remote: github.com/khaiql/parley)
- **TUI framework**: Bubble Tea (Elm architecture) + Lipgloss + Glamour
- **Strict TDD**: Write failing test first, implement, verify green
- **One commit per logical change**: descriptive message, Co-Authored-By trailer
- **Visual regression tests**: Golden file snapshots in `internal/tui/testdata/`
- **VHS tapes**: For visual testing of TUI rendering

### Specs & Plans

- Design spec: `docs/superpowers/specs/2026-03-31-parley-design.md`
- PoC plan: `docs/superpowers/plans/2026-03-31-parley-poc.md`
- Spike results: `docs/spike-results.md`
