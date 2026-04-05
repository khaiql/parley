---
name: e2e-test
description: >
  Use when verifying parley TUI works end-to-end after code changes.
  Builds binary, hosts a room, joins agents, verifies message exchange,
  autocomplete, activity status, and multi-agent interaction.
  Requires agent-tui CLI. Invoke with /e2e-test.
---

# End-to-End TUI Test

Automated smoke test that verifies the full message pipeline and TUI features.

## Prerequisites

- `agent-tui` CLI installed (`agent-tui --version`)
- Go toolchain (`go build`)

If agent-tui is not installed:
```bash
curl -fsSL https://raw.githubusercontent.com/pproenca/agent-tui/master/install.sh | sh
```

## Test Scenarios

### Scenario 1: Basic Message Exchange

Build, host, join one agent, verify hello and Q&A.

```
1. go build -o parley ./cmd/parley
2. agent-tui run "$(pwd)/parley" -- host --topic "e2e test"
3. Wait stable, extract port from top bar
4. agent-tui run "$(pwd)/parley" -- join --port <port> --name agent-1 --role "test agent"
5. Verify: "agent-1 joined" in chat, "agent-1" in sidebar with badge
6. Wait ~15s for agent intro message
7. Type "@agent-1 what is 2+2?" and press Enter
8. Wait ~20s, verify agent response visible
```

**Assert:** Host sees join message, agent hello, human question, agent answer.

### Scenario 2: Multi-Agent Room

Join a second agent while first is still connected.

```
1. agent-tui run "$(pwd)/parley" -- join --port <port> --name agent-2 --role "second agent"
2. Verify: "agent-2 joined" in chat
3. Verify sidebar shows 3 participants (host + agent-1 + agent-2)
4. Wait ~15s for agent-2 intro message
5. Type "hey everyone, who is here?" and press Enter
6. Wait ~25s, verify at least one agent responds
```

**Assert:** Sidebar shows all 3 participants. Both agents visible in chat.

### Scenario 3: Slash Command Suggestions

Verify `/` triggers autocomplete with available commands.

```
1. Type "/" in the host input
2. Screenshot immediately
3. Verify: suggestion dropdown visible showing /info, /save, /send_command
4. Type "i" to filter
5. Screenshot: only /info should remain
6. Press Escape to dismiss
7. Screenshot: suggestions hidden
```

**Assert:** Suggestion box appears on `/`, filters on typing, dismisses on Esc.

### Scenario 4: Mention Suggestions

Verify `@` triggers autocomplete with participant names.

```
1. Type "@" in the host input
2. Screenshot immediately
3. Verify: suggestion dropdown visible showing @agent-1, @agent-2
4. Type "a" to filter
5. Screenshot: both agents shown (both start with "a")
6. Press Tab to accept first suggestion
7. Screenshot: input contains "@agent-1 " (with trailing space)
8. Press Escape or clear input
```

**Assert:** `@` shows online participants, Tab inserts selected name.

### Scenario 5: Agent Activity Status

Verify sidebar shows agent activity (generating, thinking) during responses.

```
1. Type "@agent-1 write a paragraph about Go" and press Enter
2. Take rapid screenshots (every 2s) for 15s
3. Check sidebar for activity indicators:
   - "thinking" or "generating" next to agent-1's name
   - Spinner animation character (one of ⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏)
4. After agent responds, screenshot again
5. Verify: activity status cleared (no "generating" text)
```

**Assert:** Sidebar shows activity during generation, clears after response.

### Scenario 6: /info Modal

Verify the `/info` command opens a modal overlay.

```
1. Type "/info" and press Enter
2. Screenshot
3. Verify: modal visible with room info (topic, port, participants)
4. Press Escape
5. Screenshot: modal dismissed, normal view restored
```

**Assert:** Modal shows room details, Esc dismisses it.

### Cleanup

Always run at the end, even if a scenario fails:

```
agent-tui kill -s <host-session>
agent-tui kill -s <agent-1-session>
agent-tui kill -s <agent-2-session>
rm -f parley
```

## Verification Checklist

| Scenario | What to check |
|----------|---------------|
| 1. Basic | Join message, agent hello, Q&A exchange |
| 2. Multi-agent | 3 participants in sidebar, both agents chat |
| 3. Slash suggestions | `/` shows commands, filters, Esc dismisses |
| 4. Mention suggestions | `@` shows participants, Tab accepts |
| 5. Activity status | Spinner + "generating" during response, clears after |
| 6. /info modal | Modal renders, Esc dismisses |

## Failure Recovery

| Problem | Fix |
|---------|-----|
| `agent-tui` not found | Install via curl script above |
| Binary not found | Use absolute path: `"$(pwd)/parley"` |
| Agent never responds | Check Claude API key, increase wait to 30s |
| Suggestions don't appear | Verify InputFSM wired, check completionStart |
| Activity not shown | Verify room.State emits ParticipantActivityChanged |
| Orphaned sessions | `agent-tui sessions` then `agent-tui kill -s <id>` each |

## Tips

- Use `--strip-ansi` for text assertions
- Use `-s <id>` to target specific sessions
- Agents need ~15s to start and send intro
- For activity status, take screenshots every 2s — the window is short
- Always clean up sessions, even on failure
