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

### VHS Visual Testing

```bash
vhs <tape-file>.tape               # Record TUI to GIF
```

VHS tapes live in the project root. Use them to visually verify TUI changes.

## Architecture Overview

**Parley** is a TUI group chat where a human and coding agents (Claude Code, Gemini CLI, etc.) collaborate as peers.

### Components

```
cmd/parley/main.go          — CLI entrypoint (Cobra), wires host + join commands
internal/protocol/           — JSON-RPC 2.0 types, NDJSON encoding
internal/server/             — TCP server, room state, broadcast, persistence
internal/client/             — TCP client, send/receive
internal/driver/             — AgentDriver interface + Claude Code driver
internal/tui/                — Bubble Tea TUI (app, chat, sidebar, input, topbar, styles)
```

### Communication

- **Server ↔ Client**: JSON-RPC 2.0 over NDJSON-framed TCP
- **Parley ↔ Agent**: Claude Code's `--input-format stream-json --output-format stream-json --include-partial-messages`

### Key Design Decisions

- Server is embedded in the `host` process (no separate daemon)
- Agent drivers abstract different agent communication patterns (stdio, HTTP)
- Same TUI renders for both human and agent — only input source differs
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
