# Input History Navigation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add shell-style Up/Down arrow history navigation to the human chat input so users can cycle through previously sent messages.

**Architecture:** Store history as a `[]string` (newest-first) in `App`, with a cursor index (`historyIdx`) and a saved draft (`historyDraft`). Intercept Up/Down keys in `handleKeyMsg` before they fall through to `forwardScrollKey`. Push messages to history on Enter.

**Tech Stack:** Go, Bubble Tea (`github.com/charmbracelet/bubbletea`), existing TUI package patterns

---

## File Map

| File | Change |
|---|---|
| `internal/tui/app.go` | Add history fields to `App`, update `handleEnterKey`, add history key handling in `handleKeyMsg` |
| `internal/tui/input.go` | Add `SetValue(text string)` method to `Input` |
| `internal/tui/app_test.go` | Add history navigation tests |
| `internal/tui/input_test.go` | Add `SetValue` test |

---

### Task 1: Add `SetValue` to `Input`

**Files:**
- Modify: `internal/tui/input.go`
- Test: `internal/tui/input_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/tui/input_test.go`:

```go
func TestInput_SetValue_SetsTextAndMovesCaretToEnd(t *testing.T) {
	inp := NewInput()
	inp.SetValue("hello world")
	if inp.Value() != "hello world" {
		t.Errorf("expected value 'hello world', got %q", inp.Value())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /Users/sle/group_chat/.claude/worktrees/keen-matsumoto
go test ./internal/tui/ -run TestInput_SetValue -v
```

Expected: `FAIL — undefined: (inp).SetValue`

- [ ] **Step 3: Implement `SetValue`**

Add after `Reset()` in `internal/tui/input.go`:

```go
// SetValue sets the textarea content and positions the cursor at the end.
func (i *Input) SetValue(text string) {
	i.ta.SetValue(text)
	i.ta.SetCursor(len([]rune(text)))
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/tui/ -run TestInput_SetValue -v
```

Expected: `PASS`

- [ ] **Step 5: Commit**

```bash
git add internal/tui/input.go internal/tui/input_test.go
git commit -m "feat(tui): add Input.SetValue helper"
```

---

### Task 2: Add history fields to `App` and push on send

**Files:**
- Modify: `internal/tui/app.go`
- Test: `internal/tui/app_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/tui/app_test.go`:

```go
func TestApp_History_PushedOnSend(t *testing.T) {
	app := makeApp()

	// Type "hello" and press Enter.
	for _, ch := range "hello" {
		model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		app = model.(App)
	}
	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	app = model.(App)

	if len(app.history) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(app.history))
	}
	if app.history[0] != "hello" {
		t.Errorf("expected history[0]='hello', got %q", app.history[0])
	}
	if app.historyIdx != -1 {
		t.Errorf("expected historyIdx=-1 after send, got %d", app.historyIdx)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/tui/ -run TestApp_History_PushedOnSend -v
```

Expected: `FAIL — app.history undefined`

- [ ] **Step 3: Add history fields to `App` struct**

In `internal/tui/app.go`, add three fields to the `App` struct (after `mouseEnabled`):

```go
// Message history for Up/Down navigation (newest-first).
history      []string
historyIdx   int    // -1 = current draft; ≥0 = browsing history
historyDraft string // saved draft before entering history
```

`historyIdx` must be initialized to `-1`. Update `NewApp` to set it:

```go
a := App{
    // ... existing fields ...
    historyIdx: -1,
}
```

- [ ] **Step 4: Push to history in `handleEnterKey`**

In `internal/tui/app.go`, in `handleEnterKey`, prepend the trimmed text to history **before** calling `a.input.Reset()`:

```go
func (a *App) handleEnterKey() {
	text := strings.TrimSpace(a.input.Value())
	if text == "" {
		return
	}
	// Push to history (newest-first) before resetting input.
	if !command.IsCommand(text) {
		a.history = append([]string{text}, a.history...)
	}
	a.historyIdx = -1
	a.historyDraft = ""
	a.input.Reset()
	// ... rest of existing logic unchanged ...
```

Leave the rest of `handleEnterKey` (the slash command / send branch) exactly as-is.

- [ ] **Step 5: Run test to verify it passes**

```bash
go test ./internal/tui/ -run TestApp_History_PushedOnSend -v
```

Expected: `PASS`

- [ ] **Step 6: Commit**

```bash
git add internal/tui/app.go internal/tui/app_test.go
git commit -m "feat(tui): track sent message history in App"
```

---

### Task 3: Intercept Up/Down in `handleKeyMsg` for history navigation

**Files:**
- Modify: `internal/tui/app.go`
- Test: `internal/tui/app_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `internal/tui/app_test.go`:

```go
// sendMessage is a test helper that types text and presses Enter.
func sendMessage(app App, text string) App {
	for _, ch := range text {
		model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		app = model.(App)
	}
	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return model.(App)
}

func TestApp_History_UpCyclesBack(t *testing.T) {
	app := makeApp()
	app = sendMessage(app, "first")
	app = sendMessage(app, "second")

	// Press Up once — should show most recent ("second").
	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyUp})
	app = model.(App)
	if app.input.Value() != "second" {
		t.Errorf("after 1× Up: expected 'second', got %q", app.input.Value())
	}
	if app.historyIdx != 0 {
		t.Errorf("expected historyIdx=0, got %d", app.historyIdx)
	}

	// Press Up again — should show "first".
	model, _ = app.Update(tea.KeyMsg{Type: tea.KeyUp})
	app = model.(App)
	if app.input.Value() != "first" {
		t.Errorf("after 2× Up: expected 'first', got %q", app.input.Value())
	}
	if app.historyIdx != 1 {
		t.Errorf("expected historyIdx=1, got %d", app.historyIdx)
	}

	// Press Up at end of history — should stay on "first".
	model, _ = app.Update(tea.KeyMsg{Type: tea.KeyUp})
	app = model.(App)
	if app.input.Value() != "first" {
		t.Errorf("Up at end should stay on 'first', got %q", app.input.Value())
	}
}

func TestApp_History_DownRestoresDraft(t *testing.T) {
	app := makeApp()
	app = sendMessage(app, "hello")

	// Type a draft.
	for _, ch := range "draft text" {
		model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		app = model.(App)
	}

	// Up — goes into history (saves draft).
	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyUp})
	app = model.(App)
	if app.input.Value() != "hello" {
		t.Fatalf("expected 'hello' after Up, got %q", app.input.Value())
	}

	// Down — back to draft.
	model, _ = app.Update(tea.KeyMsg{Type: tea.KeyDown})
	app = model.(App)
	if app.input.Value() != "draft text" {
		t.Errorf("expected draft restored after Down, got %q", app.input.Value())
	}
	if app.historyIdx != -1 {
		t.Errorf("expected historyIdx=-1 after returning to draft, got %d", app.historyIdx)
	}
}

func TestApp_History_DownOnEmptyHistoryIsNoop(t *testing.T) {
	app := makeApp()

	// Type some text.
	for _, ch := range "current" {
		model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		app = model.(App)
	}

	// Down with no history — input unchanged.
	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyDown})
	app = model.(App)
	if app.input.Value() != "current" {
		t.Errorf("Down with no history should not change input, got %q", app.input.Value())
	}
}

func TestApp_History_SlashCommandsNotStored(t *testing.T) {
	app := makeAppWithRegistry()

	// Send a slash command.
	for _, ch := range "/info" {
		model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		app = model.(App)
	}
	// Dismiss the autocomplete if triggered.
	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyEsc})
	app = model.(App)
	model, _ = app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	app = model.(App)

	if len(app.history) != 0 {
		t.Errorf("expected slash commands not stored in history, got %d entries: %v", len(app.history), app.history)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/tui/ -run "TestApp_History_Up|TestApp_History_Down|TestApp_History_Slash" -v
```

Expected: All `FAIL` (history navigation not implemented yet).

- [ ] **Step 3: Add `navigateHistory` helper to `app.go`**

Add to `internal/tui/app.go` (after `handleEnterKey`):

```go
// navigateHistory moves the history cursor by delta (+1 = back, -1 = forward).
// Call only in InputModeHuman + StateNormal. Returns true when the key was consumed.
func (a *App) navigateHistory(delta int) bool {
	if a.input.mode != InputModeHuman || a.inputFSM.Current() != StateNormal {
		return false
	}
	switch {
	case delta > 0: // Up — go back in history
		if len(a.history) == 0 {
			return true // consumed but no-op
		}
		if a.historyIdx == -1 {
			// Save current draft before entering history.
			a.historyDraft = a.input.Value()
			a.historyIdx = 0
		} else if a.historyIdx < len(a.history)-1 {
			a.historyIdx++
		}
		a.input.SetValue(a.history[a.historyIdx])
	case delta < 0: // Down — go forward
		if a.historyIdx == -1 {
			return true // already at draft, no-op
		}
		if a.historyIdx > 0 {
			a.historyIdx--
			a.input.SetValue(a.history[a.historyIdx])
		} else {
			// Return to draft.
			a.historyIdx = -1
			a.input.SetValue(a.historyDraft)
			a.historyDraft = ""
		}
	}
	return true
}
```

- [ ] **Step 4: Call `navigateHistory` from `handleKeyMsg`**

In `internal/tui/app.go`, in `handleKeyMsg`, add history key handling **after** the `handleCompletingKeys` block and **before** the Enter key check:

```go
	// History navigation: Up/Down in human mode cycle through sent messages.
	if a.input.mode == InputModeHuman && a.inputFSM.Current() == StateNormal {
		switch m.Type {
		case tea.KeyUp:
			a.navigateHistory(+1)
			return a, nil, true
		case tea.KeyDown:
			a.navigateHistory(-1)
			return a, nil, true
		}
	}
```

This goes between the `handleCompletingKeys` call and the `tea.KeyEnter` check, around line 456 of the current `handleKeyMsg`.

- [ ] **Step 5: Run tests to verify they pass**

```bash
go test ./internal/tui/ -run "TestApp_History" -v
```

Expected: All `PASS`

- [ ] **Step 6: Run full test suite**

```bash
go test ./internal/tui/ -timeout 30s -v 2>&1 | tail -30
```

Expected: All tests pass. No regressions.

- [ ] **Step 7: Commit**

```bash
git add internal/tui/app.go internal/tui/app_test.go
git commit -m "feat(tui): Up/Down arrow history navigation in chat input (#113)"
```

---

### Task 4: CI quality gates

**Files:** None (verification only)

- [ ] **Step 1: Build**

```bash
go build ./...
```

Expected: no output (clean compile)

- [ ] **Step 2: Lint**

```bash
go run github.com/golangci/golangci-lint/cmd/golangci-lint@latest run ./... --timeout=5m
```

Expected: no issues reported

- [ ] **Step 3: Race detector**

```bash
go test ./... -timeout 30s -race
```

Expected: `ok` for all packages

- [ ] **Step 4: Push and open PR**

```bash
git pull --rebase
git push -u origin claude/keen-matsumoto
```

Then open a PR from `claude/keen-matsumoto` → `main` with title:

> feat(tui): Up/Down arrow history navigation in chat input (#113)

Body: "Pressing Up/Down in the human chat input cycles through previously sent messages (shell-style history). Slash commands are excluded from history. Down restores the unsaved draft when returning from history."
