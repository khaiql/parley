# Host-Initiated Yolo Mode Design

**Issue:** [#49](https://github.com/khaiql/parley/issues/49)
**Date:** 2026-04-03

## Summary

Allow the host to start a room in "yolo mode" where all joining agents automatically get their CLI's auto-approve flag appended to their arguments. This is the quick path for trusted environments where permission prompts are unnecessary friction.

## Data Flow

```
host --yolo → Server.Room.AutoApprove=true
                    ↓
            agent joins, receives RoomStateParams{AutoApprove: true}
                    ↓
            join command sets AgentConfig.AutoApprove=true
                    ↓
            driver.Start() → appends flag to CLI args
```

## Changes by Layer

### CLI (`cmd/parley/main.go`)

- Add `--yolo` flag to host command (bool, default false)
- Pass value through to server/room creation

### Protocol (`internal/protocol/protocol.go`)

- Add `AutoApprove bool` field to `RoomStateParams` with JSON tag `"auto_approve,omitempty"`

### Server (`internal/server/room.go`, `internal/server/server.go`)

- Add `AutoApprove bool` field to `Room` struct
- Include `AutoApprove` in the `RoomStateParams` snapshot returned on join
- Accept auto-approve setting when creating the room (server config or direct field)

### Driver (`internal/driver/`)

- Add `AutoApprove bool` field to `AgentConfig` in `driver.go`
- **Claude driver** (`claude.go`): When `AutoApprove` is true, append `--dangerously-skip-permissions` to args in `BuildArgs()`
- **Gemini driver** (`gemini.go`): Make `--yolo` conditional on `AutoApprove` in both `BuildGeminiArgs()` and `BuildGeminiArgsWithResume()` (currently hardcoded)

### Join Side (`cmd/parley/main.go`)

- When the join command receives `RoomStateParams` with `AutoApprove: true`, set `AgentConfig.AutoApprove = true` before starting the driver

### Persistence

- `AutoApprove` is included in saved room state so `parley host --resume <id>` preserves yolo mode without re-specifying the flag

## Driver Flag Reference

| Driver | Auto-Approve Flag |
|--------|-------------------|
| Claude Code | `--dangerously-skip-permissions` |
| Gemini CLI | `--yolo` |
| Future agents | Each driver defines its own flag |

## Testing

- Unit tests for `BuildArgs()` / `BuildGeminiArgs()` with `AutoApprove: true` and `AutoApprove: false`
- Verify Gemini no longer hardcodes `--yolo` (only present when `AutoApprove` is set)
- Protocol serialization round-trip for the new `AutoApprove` field
- Persistence test that auto-approve survives save/load cycle
- Integration: flag propagation from host CLI through room state to driver args

## Acceptance Criteria

- `parley host --yolo` sets auto-approve mode on the room
- Joining agents receive the auto-approve flag via room state
- ClaudeDriver appends `--dangerously-skip-permissions` when auto-approve is set
- GeminiDriver appends `--yolo` only when auto-approve is set (not hardcoded)
- Room yolo state persists across resume
- Tests for flag propagation through the protocol and each driver
