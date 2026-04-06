# Improved Text Input Editing

**Issue**: #48
**Date**: 2026-04-06

## Summary

Add standard text editing shortcuts to the chat input box: word navigation with Ctrl modifiers, double-Esc to clear, and Shift+Enter for multi-line input. Remove the backslash-newline workaround.

## Scope

### In scope
- Customize textarea KeyMap with additional key bindings
- Double-Esc (300ms window) to clear input
- Shift+Enter / Alt+Enter for newline insertion
- Remove `handleBackslashNewline`

### Out of scope
- Mouse text selection (separate issue)

## Design

### 1. Textarea KeyMap customization (`NewInput()`)

Override `textarea.DefaultKeyMap` to extend bindings:

| Action | Existing bindings (keep) | New bindings (add) |
|---|---|---|
| WordForward | `alt+right`, `alt+f` | `ctrl+right` |
| WordBackward | `alt+left`, `alt+b` | `ctrl+left` |
| DeleteWordBackward | `alt+backspace`, `ctrl+w` | `ctrl+backspace` |
| DeleteWordForward | `alt+delete`, `alt+d` | `ctrl+delete` |
| InsertNewline | ~~`enter`, `ctrl+m`~~ | `shift+enter`, `alt+enter` |

InsertNewline is **rebound** (not extended) because Enter is used for message sending.

### 2. Double Esc to clear input

Add `lastEscTime time.Time` field to `Input` struct.

In `App.Update`, when `tea.KeyEsc` is received and FSM is in `StateNormal`:
- If `time.Since(lastEscTime) < 300ms` → call `input.Reset()`, zero out `lastEscTime`
- Otherwise → record `time.Now()` in `lastEscTime`

Single Esc in `StateCompleting` continues to dismiss autocomplete (handled by `handleCompletingKeys` which runs first).

### 3. Remove backslash-newline

Delete `handleBackslashNewline` from `input.go` and its call site in `app.go`. Shift+Enter replaces this functionality.

## Files changed

- `internal/tui/input.go` — KeyMap customization in `NewInput()`, add `lastEscTime`, remove `handleBackslashNewline`
- `internal/tui/app.go` — Double-Esc handling in `Update`, remove backslash-newline call
- `internal/tui/input_test.go` — Tests for new behavior (if exists)
- `internal/tui/app_test.go` — Update tests for removed backslash-newline
