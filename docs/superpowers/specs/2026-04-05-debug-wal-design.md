# Debug Write-Ahead Log

## Overview

A `--debug` flag on the `host` command that enables raw write-ahead logging for all agent drivers. When enabled, every raw stdin/stdout line exchanged between parley and agent subprocesses is captured to a timestamped NDJSON log file. This provides full visibility into agent behavior: thinking, tool calls, tool results, partial messages, and all other stream-json events.

The flag propagates via `room.state` to all joining agents, following the same pattern as `--yolo` / `AutoApprove`.

## Motivation

Agent communication happens over stream-json, producing rich structured events (thinking blocks, tool use, partial deltas, results). The TUI only surfaces a subset of this data. When debugging agent behavior, having the complete raw stream with timestamps and direction metadata is essential.

## Design

### 1. Protocol: Room State Carries Debug Flag

Add `Debug bool` field to `server.Room` and `protocol.RoomStateParams`. The server includes this in the `room.state` notification sent to joining clients, identical to how `AutoApprove` is propagated.

### 2. CLI: `host --debug`

New boolean flag on the `host` command:

```
parley host --debug --topic "my session"
```

Sets `srv.Room().Debug = true` after server creation. Persisted in room state JSON so resumed rooms retain the debug setting.

### 3. WAL Writer (`internal/wal`)

New package with a simple append-only NDJSON writer.

**Types:**

```go
type Writer struct {
    f   *os.File
    enc *json.Encoder
    mu  sync.Mutex
}

type Entry struct {
    Timestamp string          `json:"ts"`
    Agent     string          `json:"agent"`
    Direction string          `json:"dir"` // "in" or "out"
    Raw       json.RawMessage `json:"raw"`
}
```

**API:**

- `New(path string, agent string) (*Writer, error)` -- creates parent dirs, opens file for append
- `(w *Writer) Log(direction string, raw []byte) error` -- appends one NDJSON entry with current timestamp
- `(w *Writer) Close() error` -- flushes and closes the file

Each line written to the WAL file is a self-contained JSON object:

```json
{"ts":"2026-04-05T12:00:00.000Z","agent":"babbage","dir":"out","raw":{"type":"stream_event","event":{...}}}
```

### 4. Driver Integration

Add `DebugWriter *wal.Writer` to `AgentConfig`. When non-nil, drivers log all I/O:

**ClaudeDriver:**
- `readLoop`: after reading each stdout line, calls `DebugWriter.Log("out", line)`
- `Send`: before writing to stdin, calls `DebugWriter.Log("in", message)`

**GeminiDriver:**
- `readLoop`: after reading each stdout line, calls `DebugWriter.Log("out", line)`
- `invoke` / initial prompt: calls `DebugWriter.Log("in", prompt)`

The WAL writer is a passive observer -- it does not affect parsing, event emission, or any other driver behavior.

### 5. Join Command Wiring

In `runJoin`, after receiving `room.state`:

1. Check if `roomState.Debug` is true
2. If true, create WAL directory and writer:
   - Path: `~/.parley/rooms/<roomID>/debug/<agentName>.wal`
   - Pass writer into `AgentConfig.DebugWriter`
3. Defer `writer.Close()` for cleanup

### 6. File Location

```
~/.parley/rooms/<roomID>/debug/<agentName>.wal
```

The `debug/` subdirectory is created on demand when the first agent joins with debug enabled. Each agent gets its own file to avoid interleaving.

## Scope Boundaries

**Included:**
- `--debug` flag on `host`
- Propagation via room state
- WAL writer package
- Driver integration for Claude and Gemini
- Room persistence of debug flag

**Not included:**
- Log rotation, compression, or size limits (debug is opt-in, short-lived sessions)
- Replay tooling
- `join --debug` standalone override
- TUI indicator for debug mode (could be added later)
