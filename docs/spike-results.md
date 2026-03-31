# Claude Code Stream-JSON Spike Results

**Date:** 2026-03-31
**Conclusion:** Bidirectional stream-json works. Use a single long-lived process.

## Findings

### 1. `--verbose` is required

`--output-format stream-json` requires `--verbose` flag, otherwise Claude errors:

```
Error: When using --print, --output-format=stream-json requires --verbose
```

### 2. Bidirectional streaming works

**Input:** `--input-format stream-json` accepts NDJSON on stdin. Each line is:

```json
{"type":"user","message":{"role":"user","content":[{"type":"text","text":"your message here"}]}}
```

**Output:** `--output-format stream-json` emits NDJSON on stdout. Event types:

| Type | Subtype | Contains |
|------|---------|----------|
| `system` | `init` | `session_id`, `tools`, `model` |
| `system` | `hook_started` | Hook execution info (can be ignored) |
| `system` | `hook_response` | Hook result (can be ignored) |
| `assistant` | — | `message.content[]` with text/tool_use blocks |
| `rate_limit_event` | — | Rate limit status (can be ignored) |
| `result` | `success` | `session_id`, `result` (final text), `num_turns`, `total_cost_usd` |

### 3. Multiple messages in a single process work

Sending multiple JSON lines on stdin within a single process invocation works correctly.
The agent maintains context across messages (tested: "remember 42" then "what number?" — correctly responded "42").
Result showed `num_turns: 4`.

### 4. Session resume works

`--resume <session-id>` correctly resumes prior sessions. The `session_id` is available in the `result` event.

## Driver Strategy

**Use a single long-lived process per agent:**

```
claude -p --verbose --input-format stream-json --output-format stream-json --append-system-prompt "..."
```

- Send new room messages by writing JSON lines to stdin
- Parse structured events from stdout
- Capture `session_id` from result events for future resume
- Filter out `system.hook_*` and `rate_limit_event` noise

**Fallback (not needed):** Per-invocation with `--resume` also works if bidirectional streaming proves unreliable in production.

## Key flags for parley

```
claude -p \
  --verbose \
  --input-format stream-json \
  --output-format stream-json \
  --append-system-prompt "..." \
  --permission-mode <mode>
```
