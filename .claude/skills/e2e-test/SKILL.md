---
name: e2e-test
description: >
  Use when verifying parley TUI works end-to-end after code changes.
  Builds binary, hosts a room, joins a Claude agent, verifies message
  exchange between human and agent. Requires agent-tui CLI.
  Invoke with /e2e-test.
---

# End-to-End TUI Test

Automated smoke test that verifies the full message pipeline: host room, join agent, exchange messages, verify rendering.

## Prerequisites

- `agent-tui` CLI installed (`agent-tui --version`)
- Go toolchain (`go build`)

If agent-tui is not installed:
```bash
curl -fsSL https://raw.githubusercontent.com/pproenca/agent-tui/master/install.sh | sh
```

## Workflow

```bash
# 1. Build
go build -o parley ./cmd/parley

# 2. Host a room
agent-tui run "$(pwd)/parley" -- host --topic "e2e test"
# Save the host session ID from output

# 3. Wait for UI, extract port
agent-tui wait --stable -s <host-session>
agent-tui screenshot --strip-ansi -s <host-session>
# Parse port from top bar (e.g., ":59470")

# 4. Join an agent
agent-tui run "$(pwd)/parley" -- join --port <port> --name test-agent --role "test agent"
# Save the join session ID

# 5. Verify agent joined (host view)
sleep 5
agent-tui screenshot --strip-ansi -s <host-session>
# Assert: "test-agent joined" in chat, "test-agent" in sidebar

# 6. Wait for agent hello
sleep 15
agent-tui screenshot --strip-ansi -s <host-session>
# Assert: message from test-agent visible in chat

# 7. Send question from host
agent-tui type "@test-agent what is 2+2?" -s <host-session>
agent-tui press Enter -s <host-session>

# 8. Wait for agent response
sleep 20
agent-tui screenshot --strip-ansi -s <host-session>
# Assert: agent response visible (e.g., "4")

# 9. Cleanup
agent-tui kill -s <host-session>
agent-tui kill -s <join-session>
rm -f parley
```

## Verification Checklist

Each screenshot should confirm:

| Step | What to check |
|------|---------------|
| After host start | Top bar shows topic and port, sidebar shows host user, status bar shows "connected" |
| After agent join | "test-agent joined" system message, sidebar shows both participants with badge |
| After agent hello | Agent's intro message visible in chat area |
| After host question | Human message "@test-agent what is 2+2?" visible |
| After agent response | Agent's answer visible below the question |

## Failure Recovery

| Problem | Fix |
|---------|-----|
| `agent-tui` not found | Install: `curl -fsSL https://raw.githubusercontent.com/pproenca/agent-tui/master/install.sh \| sh` |
| Binary not found by agent-tui | Use absolute path: `"$(pwd)/parley"` |
| Agent never responds | Check Claude API key, increase wait time to 30s |
| Port parse fails | Screenshot host, look for `:<port>` in top-right |
| Orphaned sessions | `agent-tui sessions` to list, `agent-tui kill -s <id>` each |

## Tips

- Use `agent-tui screenshot --strip-ansi` for text assertions (no ANSI codes)
- Use `-s <session-id>` to target specific sessions when running host + join
- The agent needs ~15s to start Claude and send its intro
- The agent needs ~20s to respond to a question
- Always clean up sessions at the end, even if tests fail
