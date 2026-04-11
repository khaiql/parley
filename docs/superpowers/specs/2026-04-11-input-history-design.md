# Input History Navigation Design

**Date**: 2026-04-11  
**Issue**: #113 — KeyUp in chat input to get back previous sent messages

## Overview

Add shell/REPL-style message history navigation to the human chat input. Pressing Up cycles backward through previously sent messages; Down cycles forward. This is a familiar UX pattern from bash, zsh, and REPL environments.

## Scope

Single feature addition to `internal/tui/app.go` and `internal/tui/app_test.go`. No new packages, no protocol changes.

## Design

### State

Add three fields to `App`:

```go
history      []string // sent messages, newest-first (index 0 = most recent)
historyIdx   int      // -1 = at current draft; 0..len-1 = browsing history
historyDraft string   // saved draft text before entering history navigation
```

`historyIdx` starts at `-1` (no history navigation active).

### Message submission (`handleEnterKey`)

After trimming and before resetting the input, prepend the text to `history` (newest-first ordering, so Up always goes toward most recent). Reset `historyIdx = -1` and `historyDraft = ""`.

### Key handling

In `handleKeyMsg`, before the `forwardScrollKey` fallthrough, handle Up/Down when:
- `a.input.mode == InputModeHuman`
- `a.inputFSM.Current() == StateNormal`

**Up pressed**:
1. If `historyIdx == -1`: save current input value to `historyDraft`, set `historyIdx = 0`
2. Else if `historyIdx < len(history)-1`: increment `historyIdx`
3. Set input value to `history[historyIdx]` (move cursor to end)

**Down pressed**:
1. If `historyIdx == -1`: no-op (already at draft, nothing newer)
2. If `historyIdx > 0`: decrement `historyIdx`, set input value to `history[historyIdx]`
3. If `historyIdx == 0`: set `historyIdx = -1`, restore `historyDraft`, move cursor to end

### Effect on chat scrolling

Up/Down no longer forward to the chat viewport when in `InputModeHuman` + `StateNormal`. PageUp/PageDown remain available for chat scroll. This is consistent with how shells work — the input captures arrow keys when focused.

## Testing

- Unit tests in `app_test.go` using the existing test helpers
- Cases: Up on empty history (no-op), Up cycles back, Down returns draft, multiple Up/Down round-trips, history grows with each sent message, slash commands are NOT added to history

## Trade-offs Considered

| Approach | Trade-off |
|---|---|
| Up/Down always captured in human mode | Simpler, matches shell convention, chat scroll via PgUp/PgDown |
| Only capture when textarea cursor is on first/last line | More nuanced but complex to implement (textarea line detection not exposed) |
| Store history in `Input` struct | Would require key logic in Input; key handling lives in App — bad separation |

**Chosen**: Always capture in human mode + normal state. Matches user expectation from shells.
