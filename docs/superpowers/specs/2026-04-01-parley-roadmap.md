# Parley Product Roadmap — PoC → Shippable Product

## Current State

Working PoC: human + Claude Code agent in a TUI group chat. Token-level streaming, thinking/tool indicators, basic persistence. Code review identified 5 critical bugs, 9 important issues, and 9 minor issues.

## Milestones

### Milestone 1: Foundation (Critical Fixes + Module)

Fix critical bugs from code review that will cause panics, data corruption, or goroutine leaks in production use. Must be done before any feature work.

### Milestone 2: UX Overhaul

Make the TUI production-quality. Sidebar status per participant, message wrapping, visual polish, agent activity in sidebar (not input box).

### Milestone 3: Selective Response Protocol

Solve the "noisy room" problem. Agents think about whether to respond, signal "listening" if they choose silence. Protocol-level, not chat-level.

### Milestone 4: Conversation History

New joiners get context. Message history replay in TUI, history injection into agent's initial prompt.

### Milestone 5: Gemini Driver

Support Gemini CLI as an agent. Per-invocation with `--resume`, same driver interface.

### Milestone 6: Session Resume

Save and restore full room sessions. Agents resume with `--resume <session-id>`.

---

## Milestone 1: Foundation

### 1.1 Fix module path mismatch

**What:** Change `go.mod` from `github.com/sle/parley` to `github.com/khaiql/parley`. Update all imports across every Go file.

**How:** `sed` or manual find-replace across all `.go` files, then `go mod tidy`.

**Done when:** `go build ./...` succeeds, `go test ./...` passes, all imports reference `github.com/khaiql/parley`.

---

### 1.2 Fix client double-close race condition

**What:** `client.Close()` can panic when called concurrently. `incoming` channel is never closed, causing goroutine leaks (consumers of `c.Incoming()` hang forever).

**How:**
- Use `sync.Once` for close operation
- Add `defer close(c.incoming)` at end of `readLoop`

**Done when:** `go test -race ./internal/client/` passes. Test: call `Close()` from two goroutines concurrently — no panic. Test: after `Close()`, `range c.Incoming()` terminates.

---

### 1.3 Fix room.left never broadcast

**What:** When a client disconnects, the sidebar shows stale participants. `room.left` notification is never sent — only a system message.

**How:** Add `BroadcastLeft(protocol.LeftParams)` method to Room. Call it from `handleConn` cleanup. Send `room.left` notification to all remaining participants.

**Done when:** Integration test: client A joins, client B joins, client B disconnects → client A receives `room.left` notification. Sidebar removes participant.

---

### 1.4 Fix duplicate message IDs

**What:** `generateID()` uses `time.Now().UnixNano()` which isn't unique under concurrent load.

**How:** Use atomic counter: `fmt.Sprintf("msg-%d", atomic.AddUint64(&counter, 1))`.

**Done when:** Test: call `generateID()` 1000 times concurrently, all IDs are unique.

---

### 1.5 Fix name collision on join

**What:** Two clients joining with the same name silently overwrites the first, orphaning its channels.

**How:** Check for existing name in `Room.Join`. If exists, return an error. Server sends JSON-RPC error response to the client.

**Done when:** Test: client A joins as "Alice", client B tries to join as "Alice" → client B receives error, client A is unaffected.

---

### 1.6 Fix driver scanner buffer

**What:** `ClaudeDriver.readLoop` uses default 64KB scanner buffer. Large Claude responses silently kill the event stream.

**How:** Set 1MB buffer: `scanner.Buffer(make([]byte, 1024*1024), 1024*1024)`.

**Done when:** Test: parse a stream-json line longer than 64KB without error.

---

### 1.7 Fix join timeout

**What:** `runJoin` blocks forever waiting for `room.state` if server doesn't send it.

**How:** Add 5-second timeout with `select` + `time.After`.

**Done when:** Test: connect to a port that accepts TCP but never sends room.state → program exits with error after timeout.

---

### 1.8 Clean up dead code and duplication

**What:** `parseAssistantEvent` is dead code. `parseMentions` is duplicated. Unused `leftPad` in topbar. Discarded errors in critical send paths.

**How:**
- Remove `parseAssistantEvent` and its tests
- Move `parseMentions` to `internal/protocol` package
- Remove dead `leftPad` computation
- Log errors from `c.Send()` calls in main.go (at minimum to stderr)
- Run `go mod tidy` to fix indirect markers

**Done when:** `go vet ./...` clean. No duplicate functions. No dead code.

---

### 1.9 Fix driver Stop() race and graceful shutdown

**What:** `Stop()` doesn't wait for `readLoop` to finish. Process is killed with SIGKILL (no graceful shutdown).

**How:**
- Add `sync.WaitGroup` that `readLoop` signals on exit
- `Stop()` sends SIGTERM first, waits 2s, then SIGKILL
- `Stop()` waits for readLoop via WaitGroup

**Done when:** Test: after `Stop()`, no goroutines from the driver are still running.

---

## Milestone 2: UX Overhaul

### 2.1 Agent status in sidebar

**What:** Move agent activity indicators (thinking, tool use, idle, listening) from the input box to the sidebar. Each participant shows their current status.

**How:**
- Add `Status string` to sidebar participant data
- New protocol notification `room.status` — client sends status updates, server broadcasts
- Sidebar renders: `● GoExpert  thinking…` / `● GoExpert  reading main.go…` / `● GoExpert  listening`
- Remove status display from input box (input box only shows streaming text on agent terminals)

**Done when:** Host TUI sidebar shows real-time status for each agent. Status updates as agent thinks → uses tools → responds → idle.

---

### 2.2 Message text wrapping

**What:** Long messages overflow the chat viewport width instead of wrapping.

**How:** Wrap message text to `chatWidth - padding` before rendering. Use `lipgloss.NewStyle().Width(maxWidth).Render(text)` or manual word wrapping.

**Done when:** Visual regression test at 80x24 with a 200-char message shows wrapped text. VHS tape confirms wrapping.

---

### 2.3 Improve top bar contrast and layout

**What:** Port number is dim. Layout spacing is inconsistent.

**How:** Use `colorPrimary` for port. Add "Topic:" label. Ensure consistent padding.

**Done when:** VHS tape at 120x35 shows clearly readable topic and port.

---

### 2.4 Input box improvements

**What:** Agent input box on agent's own terminal should show streaming text clearly. Human input should support multi-line with Shift+Enter.

**How:**
- Agent mode: show full streaming text (scrollable if long), cursor indicator
- Human mode: textarea height auto-expands for multi-line, Enter sends, Shift+Enter for newline (already default in bubbles textarea)

**Done when:** Agent TUI shows streaming text that wraps properly. Human can compose multi-line messages.

---

### 2.5 VHS test automation

**What:** Automated VHS tapes for visual regression of key scenarios.

**How:** Create tape files for: host-only view, host with messages, agent join view. Run via `make vhs` or CI. Compare output GIFs/PNGs.

**Done when:** `make vhs` generates screenshots for 3 scenarios. Screenshots are committed and reviewable.

---

## Milestone 3: Selective Response Protocol

### 3.1 Response declaration protocol

**What:** When a message arrives, each agent decides whether to respond. If it chooses silence, it signals "listening" (protocol-level, not a chat message). The sidebar shows the status.

**How:**
- Agent system prompt updated: "If you decide not to respond, output exactly `[LISTENING]` and nothing else."
- Driver detects `[LISTENING]` in the accumulated text, converts it to a `room.status` notification with status "listening" instead of sending a chat message
- Server does NOT store this as a message — it's a status update only
- Sidebar shows: `● GoExpert  listening` (with a 👂 or similar indicator)
- After a response or new message, status resets

**Done when:** Test with 2 agents: send a message relevant to only one agent's expertise. The irrelevant agent shows "listening" in sidebar. No "pass" or "[LISTENING]" appears in chat history.

---

### 3.2 Agent response delay

**What:** Brief delay before agents respond to unmentioned messages, giving other agents (or the human) a chance to speak first.

**How:** When a message arrives without an @-mention, the driver waits 2-3 seconds before forwarding to the agent. If another message arrives in that window, batch them. @-mentioned messages bypass the delay.

**Done when:** Test: send an unmentioned message. Agent response starts after ~2s delay. Send an @-mentioned message. Agent responds immediately.

---

## Milestone 4: Conversation History

### 4.1 Message history replay on join

**What:** When a new participant joins, the TUI shows previous messages. Currently the chat starts empty for new joiners.

**How:**
- Add `Messages []MessageParams` to `RoomStateParams`
- Server includes last N messages (configurable, default 50) in `room.state`
- Client TUI renders them on join (before any new messages)

**Done when:** Test: host sends 5 messages. New client joins. New client's TUI shows all 5 previous messages.

---

### 4.2 History injection into agent context

**What:** When an agent joins mid-conversation, inject conversation history into its initial prompt so it has context.

**How:** Format the last N messages as a conversation transcript and include in the initial message sent to the agent:
```
Here is the conversation so far:
[sle]: I think we need a message queue
[Alice]: Agreed, Redis Streams would work
[sle]: What about NATS?
---
You are joining this conversation now. Read the above for context.
```

**Done when:** Agent joins mid-conversation and can reference prior messages without anyone repeating them.

---

## Milestone 5: Gemini Driver

### 5.1 Gemini CLI driver

**What:** Support Gemini CLI as an agent in the chat room.

**How:**
- Create `internal/driver/gemini.go` implementing `AgentDriver`
- Per-invocation model: each incoming message spawns `gemini -p "<message>" -o stream-json --resume <id>`
- Parse stream-json events (same format as Claude or Gemini-specific)
- Session resume via `--resume <index>` between invocations
- Auto-detect agent type from command name in `parley join`

**Done when:** `parley join --port 1234 --name "Gemini" --role "reviewer" -- gemini` works. Gemini agent can participate in chat, receive messages, respond.

---

### 5.2 Driver auto-detection

**What:** Parley automatically selects the right driver based on the command name (claude → ClaudeDriver, gemini → GeminiDriver).

**How:** Registry pattern: map command names to driver constructors. Fallback to a generic driver or error.

**Done when:** `-- claude` uses ClaudeDriver, `-- gemini` uses GeminiDriver, `-- unknown` returns a clear error.

---

## Milestone 6: Session Resume

### 6.1 Stable room IDs

**What:** Rooms use ephemeral port numbers as IDs. Need stable, persistent IDs.

**How:** Generate UUID at room creation. Store in `room.json`. Use for directory name and resume reference.

**Done when:** `parley host --topic "test"` creates `~/.parley/rooms/<uuid>/`. Same room can be resumed regardless of port.

---

### 6.2 Host resume

**What:** `parley host --resume <room-id>` reloads a previous room with its message history.

**How:** Load room state from JSON. Start server (new port). Display message history in TUI.

**Done when:** Host a room, send messages, quit. Resume. All messages visible. New agents can join.

---

### 6.3 Agent session resume

**What:** When an agent rejoins a resumed room, resume its coding session with `--resume <session-id>`.

**How:** Store agent session IDs in `agents.json`. On rejoin, match by name, pass `--resume <id>` to the driver.

**Done when:** Agent joins, has a conversation, quits. Room resumes. Agent rejoins. Agent remembers prior conversation context.

---

## Priority Order

1. **Milestone 1** — Foundation fixes (blocks everything)
2. **Milestone 2** — UX Overhaul (makes the product usable)
3. **Milestone 3** — Selective Response (makes multi-agent usable)
4. **Milestone 4** — Conversation History (makes joining useful)
5. **Milestone 5** — Gemini Driver (multi-agent variety)
6. **Milestone 6** — Session Resume (persistence)
