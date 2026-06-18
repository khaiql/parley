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

### End-to-End Testing

```bash
go test ./internal/e2e -run TestHeadlessRoomTwoParticipants
go test ./internal/e2e -run TestHeadlessRoomTwoParticipants -count=100
```

The e2e package exercises the headless room flow: start a server, join a second participant, exchange messages through adapters, and verify wait/send behavior.

## Architecture Overview

**Parley** is a headless, JSON-first coordination room for coding agents. The server owns room sequencing and broadcast, participant adapters keep local mirrors, and short CLI commands talk to adapters through local control sockets.

### Package Map

```
cmd/parley/              Cobra CLI entrypoint and JSON command surface
  main.go                Root command, shared flags, version command
  start.go               Start room server and host participant adapter
  join.go                Join a descriptor as a participant adapter
  invite.go              Emit descriptor JSON for active room
  participant_commands.go info, status, inbox, history, wait, send, leave, stop

internal/model/          Event envelope, event types, participant and room metadata
  event.go               Transcript classification and payload structs

internal/descriptor/     parley:// descriptor parser and formatter
  descriptor.go          Host, port, room-id grammar with IPv6 support

internal/paths/          Per-user path layout and permissions
  paths.go               runtime root selection, room dirs, active pointer, socket paths

internal/jsonout/        JSON response helpers
  jsonout.go             Success and error envelopes for CLI commands

internal/eventlog/       Append-only JSONL event store
  log.go                 Sequence assignment, read, and transcript filtering

internal/protocol/       V1 NDJSON wire protocol
  protocol.go            Request, response, event, and codec types

internal/server/         Headless room server
  server.go              TCP accept loop, room state, event log, broadcast
  interfaces.go          Server interfaces for tests and CLI wiring

internal/adapter/        Participant runtime adapter
  adapter.go             TCP connection, local event mirror, wait semantics
  control.go             Unix socket control API for short CLI commands

internal/runtime/        Runtime metadata for active rooms and participants
  runtime.go             Room runtime, active participation, invite response

internal/e2e/            Headless integration coverage
  headless_test.go       Two-participant room workflow tests
```

### How It Works

```
parley start
    |
    |-- room server listens on TCP, assigns event seq, appends JSONL
    |
    `-- host adapter connects to the room and exposes a Unix control socket

parley invite
    |
    `-- reads runtime metadata and prints parley://host:port/room-id JSON

parley join <descriptor>
    |
    `-- participant adapter connects to the room and mirrors events locally

parley wait/send/inbox/history
    |
    `-- short-lived CLI commands use the active participant control socket
```

### Package Dependencies

```
model        <- eventlog, protocol, server, adapter, runtime, cmd
descriptor   <- runtime, cmd
paths        <- runtime, cmd
jsonout      <- cmd
eventlog     <- server, adapter, cmd
protocol     <- server, adapter
server       <- cmd, e2e                 (depends on: model, eventlog, protocol)
adapter      <- cmd, e2e                 (depends on: model, protocol, runtime)
runtime      <- cmd, e2e                 (depends on: descriptor, paths)
e2e          <- server, adapter, runtime
```

`model` defines the shared event vocabulary. `protocol` is only the on-the-wire shape. `server` is authoritative for event sequencing and broadcast, while `adapter` owns each participant's local mirror and control socket.

### Message Flow

1. `parley send` connects to the active participant control socket.
2. The adapter forwards a `send` request to the TCP room server.
3. The server assigns the next sequence, appends the event log, and broadcasts the event to connected adapters.
4. Each adapter appends the event to its local mirror.
5. `parley wait` returns unseen events from the local mirror and advances that participant's cursor.

### Concurrency Model

- **Room server**: serializes room mutation, sequence assignment, log append, and broadcast.
- **Adapter**: one TCP read loop mirrors server events; control socket handlers inspect mirror state or forward requests.
- **CLI commands**: short-lived processes, always JSON output, no long-lived UI state.
- **Storage**: room directories are private to the local OS user; JSONL logs are append-only.

### Key Design Decisions

- **JSON-only user surface**: every command prints either a success envelope or an error envelope.
- **Event-log-first state**: the server's event log is the durable room transcript.
- **Participant-local cursors**: wait/inbox behavior is tracked per adapter, not globally.
- **Descriptor handoff**: joining uses explicit `parley://host:port/room-id` descriptors.
- **Local trust boundary**: runtime files and Unix sockets live under protected per-user directories.
- **No agent process management**: Parley coordinates participants; agent skills decide when to call Parley commands.

## Conventions & Patterns

- **Go module**: `github.com/khaiql/parley` (remote: github.com/khaiql/parley)
- **Strict TDD**: Write failing test first, implement, verify green
- **One commit per logical change**: descriptive message, Co-Authored-By trailer

### Specs & Plans

- Design spec: `docs/superpowers/specs/2026-03-31-parley-design.md`
- PoC plan: `docs/superpowers/plans/2026-03-31-parley-poc.md`
- Spike results: `docs/spike-results.md`
