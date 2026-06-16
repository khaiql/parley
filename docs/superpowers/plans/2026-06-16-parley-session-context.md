# Parley Session Context Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add session-scoped participant identity so agents can run Parley commands without relying on machine-global active state.

**Architecture:** Persist opaque session records under the Parley runtime root, mapping `session_id` to `room_id` and participant `name`. Participant commands resolve identity from `--session`, then explicit `--room/--name`, then the existing bare-command fallback only when unambiguous.

**Tech Stack:** Go CLI with Cobra, Parley runtime JSON files, existing adapter stores.

---

### Task 1: Runtime Session Records

**Files:**
- Modify: `internal/runtime/runtime.go`
- Modify: `internal/runtime/runtime_test.go`

- [ ] Add `Session` with `id`, `room_id`, and `name` JSON fields.
- [ ] Add `NewSessionID`, `SaveSession`, `LoadSession`, and `SessionPath`.
- [ ] Test round-trip persistence and validation.

### Task 2: CLI Session Output

**Files:**
- Modify: `cmd/parley/start.go`
- Modify: `cmd/parley/join.go`
- Modify: `cmd/parley/main_test.go`

- [ ] Make `start` and `join` create a session after daemon readiness.
- [ ] Return `session_id` and `command_args`.
- [ ] Test both responses include a valid session mapping.

### Task 3: Participant Command Resolution

**Files:**
- Modify: `cmd/parley/participant_commands.go`
- Modify: `cmd/parley/main_test.go`

- [ ] Add `--session` to participant commands.
- [ ] Reject mixing `--session` with either `--room` or `--name`.
- [ ] Resolve `--session` through runtime session records.
- [ ] Test `inbox --session`, `send --session`, and invalid flag combinations.

### Task 4: Docs And Skill

**Files:**
- Modify: `README.md`
- Modify: `skills/parley/SKILL.md`

- [ ] Make `--session` the preferred command style.
- [ ] Keep `--room/--name` documented as explicit fallback.
- [ ] Refresh the globally installed Parley skill after verification.

### Task 5: Verification

**Commands:**
- `go test ./... -timeout 30s`
- `sh -n skills/parley/scripts/ensure-parley`
- Live smoke with installed binary if the local room is still running.
