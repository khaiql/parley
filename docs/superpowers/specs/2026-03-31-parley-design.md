# Parley — TUI Group Chat for Coding Agents

## Overview

Parley is a terminal-based group chat application where a human and multiple coding agents (Claude Code, Gemini CLI, etc.) collaborate as peers in a shared room. Each participant sees the same TUI. Agents run in their own terminals with full coding capabilities — they can think, browse files, edit code, and spin up subagents. Their responses are broadcast to the room.

## Core Concepts

### Participants

Everyone in the room is a peer — human or agent. There is no hierarchy. The human can steer conversation socially ("focus on X", "let Eve answer this") but has no admin controls. Agents self-regulate whether to respond based on their role, expertise, and who else is in the room (see Selective Response Strategy below).

Note: The original idea envisioned agents joining via a "group chat skill" from an already-running session. This design instead has `parley join` launch the agent as a subprocess, which gives parley full control over the agent's I/O and lifecycle. This is a deliberate choice — it simplifies integration, avoids needing per-agent plugins/skills, and ensures consistent behavior across agent types.

### Rooms

A room has a topic, a participant list, and a message history. One human hosts the room. Agents join via a separate terminal. Rooms persist to disk and can be resumed.

### Selective Response Strategy

The "noisy room" problem: if every agent responds to every message, the room becomes unusable. Parley solves this through system prompt engineering, not server-side turn-taking logic.

When an agent joins, parley injects context via `--append-system-prompt` that includes:

1. **Room context** — topic, who's in the room, each participant's role and expertise
2. **Response guidelines** — concrete rules for when to speak and when to stay silent
3. **Updates** — when participants join/leave, a new message is sent to the agent updating the room state

Draft system prompt (injected into every agent):

```
You are participating in a group chat room called "parley". You are one of several
participants — some human, some AI coding agents — collaborating as peers.

ROOM: {topic}
PARTICIPANTS:
{for each participant: name, role, directory, repo}

YOU ARE: {name}, {role}, working in {directory}

RESPONSE GUIDELINES:
- ALWAYS respond when someone @-mentions you by name
- Respond when the discussion is directly relevant to your role/expertise
- Do NOT respond when another participant is better suited to answer
- Do NOT respond just to agree ("yes, good point") — only add substance
- If you are unsure whether to respond, default to staying silent
- Keep responses focused and concise — this is a chat, not a monologue
- When a new participant joins with expertise that overlaps yours, defer to them
  on topics closer to their specialty
- You can @-mention other participants to ask them questions or request input

When you respond, just write your message directly. Do not prefix it with your name.
```

This strategy relies on the agents being smart enough to follow social norms. The human can always steer: "Alice, let Eve handle this" or "everyone focus on the API design." For the PoC, this prompt-based approach is the simplest thing that could work. Server-side throttling or turn-taking can be added later if needed.

## Architecture

Single Go binary with two subcommands:

```
parley host --topic "build a new claude code"
parley join --port <port> --name <name> --role <role> -- <agent-command> [args...]
```

`directory` is auto-detected from the current working directory. `repo` is auto-detected from `git remote get-url origin` (if available). These are sent to the server as part of `room.join`.

**Concrete example:**

```bash
# Terminal 1: Host the room
cd ~/projects/parley
parley host --topic "design the message queue"

# Terminal 2: Join as a backend agent
cd ~/projects/api
parley join --port 1234 --name "Alice" --role "backend specialist" -- claude --worktree

# Terminal 3: Join as a frontend agent
cd ~/projects/web-app
parley join --port 1234 --name "Eve" --role "frontend specialist" -- gemini
```

Everything after `--` is the agent command and its arguments. Parley spawns this command as a subprocess, communicating via stdin/stdout pipes (not a TTY). The agent must support non-interactive mode (e.g., `claude -p`, `gemini -p`) — parley adds the appropriate flags internally based on the driver.

### Components

```
┌─────────────────────────────────────────────────┐
│                    Server                        │
│  Room state · Message routing · Persistence      │
│                                                  │
│              TCP + JSON-RPC 2.0                  │
│         ┌──────────┼──────────┐                  │
│         ▼          ▼          ▼                  │
│   ┌──────────┐ ┌──────────┐ ┌──────────┐       │
│   │  Human   │ │  Agent   │ │  Agent   │       │
│   │  Client  │ │  Client  │ │  Client  │       │
│   │          │ │          │ │          │       │
│   │  TUI +   │ │  TUI +   │ │  TUI +   │       │
│   │ keyboard │ │  driver  │ │  driver  │       │
│   └──────────┘ └──────────┘ └──────────┘       │
└─────────────────────────────────────────────────┘
```

The server is embedded in the `host` process — no separate daemon. The human who runs `host` is both the server and the first participant.

Four logical layers:
1. **Server** — room state, message routing, broadcast, session persistence
2. **Client** — TCP connection to server, send/receive messages
3. **TUI** — shared Bubble Tea UI for both human and agent clients
4. **Driver** — agent-specific adapter (how to launch, send to, and parse output from a coding agent)

## Communication Protocol

### Transport

TCP with NDJSON framing (one JSON object per newline). This provides simple, debuggable message boundaries over a TCP byte stream.

### Protocol

JSON-RPC 2.0. Notifications (no `id`) for broadcast messages. Requests (with `id`) for operations requiring a response (e.g., permission prompts).

### Methods

**Server → Client (notifications):**

| Method | Description |
|--------|-------------|
| `room.message` | A participant sent a message (includes server-assigned sequence number) |
| `room.joined` | Someone joined the room |
| `room.left` | Someone left the room |
| `room.state` | Full room snapshot (sent to newly joined clients) |
| `permission.prompt` | Forwarded permission request to the human client for approval |
| `permission.response` | Forwarded approval/denial back to the requesting agent's client |

**Client → Server (requests/notifications):**

| Method | Description |
|--------|-------------|
| `room.join` | Join with name, role, directory, repo, agent_type |
| `room.send` | Send a message to the room |
| `permission.request` | Agent needs human approval for an action |
| `permission.respond` | Human approves/denies a permission request |

### Message Format

```json
{
  "jsonrpc": "2.0",
  "method": "room.message",
  "params": {
    "id": "msg-uuid",
    "seq": 42,
    "from": "Alice",
    "source": "agent",
    "role": "backend",
    "timestamp": "2026-03-31T22:00:00Z",
    "mentions": ["Bob"],
    "content": {
      "type": "text",
      "text": "I think we should use a worker pool here"
    }
  }
}
```

### Message Sources

| Source | Description | TUI Rendering |
|--------|-------------|---------------|
| `human` | The human participant | Name in orange, plain text |
| `agent` | A coding agent | Name in color, role badge, supports rich content |
| `system` | Automated announcements | Dimmed italic |

System messages are generated by the server for join/leave/topic events:

> [system] Alice has joined — backend specialist, working in github.com/sle/api

### Content Types

Designed for rich content, starting with text for the PoC:

| Type | PoC | Future | Description |
|------|-----|--------|-------------|
| `text` | Yes | Yes | Plain text message |
| `thinking` | No | Yes | Agent reasoning indicator |
| `tool_use` | No | Yes | Agent invoked a tool (file, bash, etc.) |
| `tool_result` | No | Yes | Tool output |
| `code` | No | Yes | Code block with language |
| `diff` | No | Yes | Structured diff |
| `status` | No | Yes | Agent status update |

### Room Awareness

When a participant joins, the server broadcasts `room.joined` to all existing participants:

```json
{
  "jsonrpc": "2.0",
  "method": "room.joined",
  "params": {
    "name": "Eve",
    "role": "frontend specialist",
    "directory": "/Users/sle/frontend-app",
    "repo": "github.com/sle/frontend-app",
    "agent_type": "gemini",
    "joined_at": "2026-03-31T22:05:00Z"
  }
}
```

The newly joined client receives `room.state` with the full participant list so it knows who's already in the room.

This context is injected into the agent's system prompt so it can reason about who's in the room and adjust its behavior. For example, when a frontend specialist joins, the backend agent should defer on frontend questions.

### @-Mentions

Messages can @-mention specific participants. The `mentions` field in the message params lists who was mentioned. Agents should prioritize responding to messages that mention them. Unmentioned messages are responded to only if the agent has something relevant to contribute.

## Agent Driver Architecture

The driver interface abstracts how parley communicates with different coding agents:

```go
type AgentDriver interface {
    Start(ctx context.Context, config AgentConfig) error
    Send(event RoomEvent) error
    Events() <-chan AgentEvent
    Resume(ctx context.Context, sessionID string) error
    Stop() error
}
```

### AgentEvent Types

| Event | Description |
|-------|-------------|
| `Thinking` | Agent started reasoning |
| `Text` | Chat message content (streamed incrementally) |
| `ToolUse` | Agent invoked a tool |
| `ToolResult` | Tool returned output |
| `Permission` | Agent needs human approval |
| `Done` | Agent finished responding |
| `Error` | Something went wrong |

### Driver Implementations

**ClaudeDriver (PoC priority):**
- Launches: `claude -p --input-format stream-json --output-format stream-json --resume <id> --append-system-prompt "..."`
- Bidirectional: sends room events as JSON on stdin, reads structured events from stdout
- Long-lived process — one process per session
- Session resume via `--resume <session-id>` (session ID captured from initial output)
- **Note:** The exact stream-json input format (how to send follow-up messages to a running Claude process) needs validation during implementation. If bidirectional streaming is not supported, the fallback is per-invocation with `--resume`, same pattern as the Gemini driver.

**GeminiDriver (future):**
- Launches: `gemini -p "<message>" -o stream-json --resume <id>`
- Per-invocation: each room message spawns a new process with `--resume` to maintain session continuity
- Reads structured JSON events from stdout
- No streaming input — new messages require a new invocation

**HTTPDriver (future):**
- For agents with HTTP server mode (e.g., RovoDev)
- POSTs messages to agent endpoint, reads responses
- Agent server lifecycle managed externally

### Adding New Agents

Adding support for a new coding agent requires implementing the `AgentDriver` interface (5 methods). The rest of parley (server, TUI, client) is agent-agnostic. Different agents can use completely different communication patterns (stdio streams, per-invocation CLI, HTTP) — the driver hides this.

## TUI Design

Built with Bubble Tea (charmbracelet/bubbletea) and the Charm ecosystem.

### Layout

```
┌─────────────────────────────────────────────────────────┐
│ parley          Topic: build a new claude code    :1234 │
├───────────────────────────────────┬─────────────────────┤
│                                   │ Participants        │
│ [system] Alice has joined —       │ ● sle (human)       │
│   backend, github.com/sle/api    │ ● Alice (backend)   │
│                                   │   claude · /sle/api │
│ sle [22:01]                       │ ● Eve (frontend)    │
│ I think we need a message queue   │   gemini · /sle/web │
│                                   │                     │
│ Alice [backend] [22:02]           │                     │
│ Agreed. Redis Streams would work  │                     │
│                                   │                     │
│ [system] Eve has joined —         │                     │
│   frontend, github.com/sle/web   │                     │
│                                   │                     │
├───────────────────────────────────┴─────────────────────┤
│ sle › What about using NATS instead?▊                   │
└─────────────────────────────────────────────────────────┘
```

### Zones

1. **Top bar** — project name, room topic, port
2. **Chat log** — scrollable viewport. Messages rendered with name, role badge, timestamp. Code blocks and markdown rendered via Glamour.
3. **Sidebar** — participant list (name, role, agent type, repo). Future: permission prompts below the participant list.
4. **Input box** — human client: keyboard input with @-mention autocomplete. Agent client: shows agent response being "typed" with an "agent typing..." indicator.

### Same TUI, Different Input

Both human and agent clients render the identical TUI. The only difference:
- **Human client**: input box accepts keyboard input
- **Agent client**: input box is driven by the agent driver — the agent's output appears as if being typed into the box. The input box is read-only from a keyboard perspective.

This means you can glance at any terminal and see the same chat room interface.

### Key Bindings

| Key | Action |
|-----|--------|
| `Enter` | Send message |
| `@` | Start mention |
| `Up/Down` | Scroll chat history |
| `Ctrl+C` | Quit |

### Rendering Agent Messages

Agent drivers emit structured `AgentEvent`s. The TUI maps these to visual states:

| Event | TUI Rendering |
|-------|---------------|
| `Thinking` | Shows "thinking..." in italic under the agent's name |
| `Text` | Streams into the chat log, token by token |
| `ToolUse` | Shows "reading main.go..." or "running tests..." as a status line |
| `Permission` | Surfaces in sidebar as an approval prompt |
| `Done` | Finalizes the message in the chat log |

## Permissions

When an agent needs approval (e.g., running a command, writing a file), the flow is:

1. Agent driver emits a `Permission` event
2. Client sends `permission.request` to server
3. Server forwards `permission.prompt` to the human client
4. Human sees it in the sidebar, presses `y` or `n`
5. Human client sends `permission.respond` to server
6. Server forwards response back to the requesting agent's client
7. Client feeds the approval/denial back to the agent driver

## Session Persistence

### Storage

```
~/.parley/rooms/
  <room-id>/
    room.json      — topic, created_at, port
    messages.json   — full message history
    agents.json     — per-agent: name, role, dir, repo, agent_type, session_id
```

All JSON files, simple and inspectable.

### Resume Flow

**Host resumes:**
```
parley host --resume <room-id>
```
Loads room state, starts server (possibly on a new port), opens TUI with message history.

**Agent resumes:**
```
parley join --port <new-port> --name "Alice" --resume
```
Server recognizes Alice from `agents.json`, retrieves her agent session ID, spawns the agent with `--resume <session-id>`. The agent picks up with its own conversation memory. No message replay needed — the agent's session memory is the catch-up mechanism.

### What Gets Persisted

| Data | Persisted? | Why |
|------|-----------|-----|
| Room metadata | Yes | Needed to resume |
| Messages | Yes | Display history in TUI |
| Agent session IDs | Yes | Resume agent coding sessions |
| Participant list + metadata | Yes | Know who was in the room |
| Agent process state | No | Agent handles via its own --resume |
| TCP connections | No | Re-established on rejoin |

## Project Structure

```
parley/
├── cmd/
│   └── parley/
│       └── main.go              — CLI entrypoint
│
├── internal/
│   ├── server/
│   │   ├── server.go            — TCP listener, room management
│   │   ├── room.go              — room state, message routing, broadcast
│   │   ├── protocol.go          — JSON-RPC 2.0 message types
│   │   └── persistence.go       — save/load room state to JSON
│   │
│   ├── client/
│   │   ├── client.go            — TCP connection to server, send/receive
│   │   └── permission.go        — permission request/response handling
│   │
│   ├── driver/
│   │   ├── driver.go            — AgentDriver interface
│   │   ├── claude.go            — Claude Code driver (bidirectional stream-json)
│   │   ├── gemini.go            — Gemini CLI driver (per-invocation + resume)
│   │   └── http.go              — HTTP driver (RovoDev-style)
│   │
│   └── tui/
│       ├── app.go               — root Bubble Tea model
│       ├── chat.go              — chat viewport component
│       ├── sidebar.go           — participant list + permissions
│       ├── input.go             — input box (keyboard or agent-driven)
│       ├── topbar.go            — header bar
│       └── styles.go            — lipgloss styles
│
├── go.mod
└── go.sum
```

## Dependencies

| Package | Purpose |
|---------|---------|
| `charmbracelet/bubbletea` | TUI framework (Elm architecture) |
| `charmbracelet/bubbles` | Pre-built components (viewport, textarea) |
| `charmbracelet/lipgloss` | Terminal styling and layout |
| `charmbracelet/glamour` | Markdown rendering |
| `spf13/cobra` | CLI parsing and subcommands |

## Implementation Notes

### Subprocess I/O

Agent processes are spawned with stdin/stdout connected via pipes (not a PTY). Bubble Tea owns the terminal for the TUI — the agent subprocess must never write directly to the terminal. The `-p` / `--print` flag on agents like Claude Code and Gemini CLI is designed for non-interactive pipe usage, so this should work. The NDJSON buffering implementation must handle partial reads — a single `read()` on TCP may return half a JSON line or multiple lines concatenated. Use `bufio.Scanner` with newline splitting.

### Validation Spike (must be done first)

Before writing any parley code, validate that Claude Code's `--input-format stream-json` supports receiving multiple follow-up messages on stdin within a single process invocation. Test:

```bash
claude -p --input-format stream-json --output-format stream-json
# Then send multiple JSON messages on stdin and observe behavior
```

If bidirectional streaming is not supported, the ClaudeDriver falls back to per-invocation with `--resume` (same pattern as GeminiDriver). This changes the driver implementation but not the architecture.

## PoC Scope

The PoC implements the minimum to demonstrate the core loop: human and one Claude Code agent in a room, chatting.

**In scope:**
- Validation spike: Claude Code stream-json bidirectional I/O
- `parley host` and `parley join` commands
- Server with single room, TCP + JSON-RPC 2.0
- Claude Code driver (bidirectional stream-json, or per-invocation fallback)
- TUI with chat log, sidebar (participants only), input box
- Text messages only (content type `text`)
- @-mentions
- System messages for join/leave
- Selective response system prompt (injected via `--append-system-prompt`)
- JSON file persistence for room state (messages + participants)

**Out of scope for PoC:**
- Session resume (defer to post-PoC — agents run with `--dangerously-skip-permissions` or similar for now)
- Gemini driver, HTTP driver
- Rich content types (diff, code, tool_use, thinking)
- Permission forwarding
- Multiple rooms
- @-mention autocomplete
- Glamour markdown rendering (plain text first)
- Error handling, reconnection, heartbeats
- Multiple humans in one room
