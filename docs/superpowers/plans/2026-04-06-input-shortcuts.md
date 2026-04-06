# Input Shortcuts Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add standard text editing shortcuts (word nav, double-Esc clear, Shift+Enter newline) and remove backslash-newline workaround.

**Architecture:** Customize the bubbles textarea KeyMap in `NewInput()` for additional bindings. Add double-Esc detection with a 300ms timeout window on the Input struct. Remove `handleBackslashNewline` and its call site.

**Tech Stack:** Go, Bubble Tea, bubbles textarea, bubbles key

---

### Task 1: Customize textarea KeyMap with additional bindings

**Files:**
- Modify: `internal/tui/input.go:36-43` (NewInput function)

- [ ] **Step 1: Write the failing test**

In `internal/tui/input_test.go`, add a test that verifies the custom keymap is applied:

```go
func TestNewInput_CustomKeyMap(t *testing.T) {
	inp := NewInput()

	// Verify InsertNewline is rebound away from "enter"
	km := inp.ta.KeyMap
	for _, k := range km.InsertNewline.Keys() {
		if k == "enter" || k == "ctrl+m" {
			t.Errorf("InsertNewline should not be bound to %q", k)
		}
	}
	// Verify InsertNewline includes shift+enter
	found := false
	for _, k := range km.InsertNewline.Keys() {
		if k == "shift+enter" {
			found = true
		}
	}
	if !found {
		t.Error("InsertNewline should include shift+enter binding")
	}

	// Verify WordForward includes ctrl+right
	found = false
	for _, k := range km.WordForward.Keys() {
		if k == "ctrl+right" {
			found = true
		}
	}
	if !found {
		t.Error("WordForward should include ctrl+right binding")
	}

	// Verify WordBackward includes ctrl+left
	found = false
	for _, k := range km.WordBackward.Keys() {
		if k == "ctrl+left" {
			found = true
		}
	}
	if !found {
		t.Error("WordBackward should include ctrl+left binding")
	}

	// Verify DeleteWordBackward includes ctrl+backspace
	found = false
	for _, k := range km.DeleteWordBackward.Keys() {
		if k == "ctrl+backspace" {
			found = true
		}
	}
	if !found {
		t.Error("DeleteWordBackward should include ctrl+backspace binding")
	}

	// Verify DeleteWordForward includes ctrl+delete
	found = false
	for _, k := range km.DeleteWordForward.Keys() {
		if k == "ctrl+delete" {
			found = true
		}
	}
	if !found {
		t.Error("DeleteWordForward should include ctrl+delete binding")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run TestNewInput_CustomKeyMap -v`
Expected: FAIL — InsertNewline still bound to "enter", missing ctrl+right etc.

- [ ] **Step 3: Write minimal implementation**

In `internal/tui/input.go`, add the `key` import and modify `NewInput()`:

```go
import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)
```

Replace the `NewInput` function body:

```go
func NewInput() Input {
	ta := textarea.New()
	ta.Placeholder = "Type a message… (Enter to send, Shift+Enter for newline)"
	ta.ShowLineNumbers = false
	ta.SetHeight(1)
	ta.Focus()

	// Customize keymap: add Ctrl+arrow word nav, Ctrl+backspace/delete word
	// deletion, and rebind InsertNewline to Shift+Enter/Alt+Enter (Enter is
	// used by App.Update for message sending).
	km := textarea.DefaultKeyMap
	km.WordForward = key.NewBinding(key.WithKeys("alt+right", "alt+f", "ctrl+right"))
	km.WordBackward = key.NewBinding(key.WithKeys("alt+left", "alt+b", "ctrl+left"))
	km.DeleteWordBackward = key.NewBinding(key.WithKeys("alt+backspace", "ctrl+w", "ctrl+backspace"))
	km.DeleteWordForward = key.NewBinding(key.WithKeys("alt+delete", "alt+d", "ctrl+delete"))
	km.InsertNewline = key.NewBinding(key.WithKeys("shift+enter", "alt+enter"))
	ta.KeyMap = km

	return Input{ta: ta, mode: InputModeHuman}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tui/ -run TestNewInput_CustomKeyMap -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tui/input.go internal/tui/input_test.go
git commit -m "feat: customize textarea keymap with Ctrl word nav and Shift+Enter newline (#48)"
```

---

### Task 2: Double Esc to clear input

**Files:**
- Modify: `internal/tui/input.go:28-33` (Input struct — add lastEscTime)
- Modify: `internal/tui/app.go:148-156` (Update — add Esc handling before Layer 3)

- [ ] **Step 1: Write the failing test**

In `internal/tui/app_test.go`, add:

```go
func TestApp_DoubleEsc_ClearsInput(t *testing.T) {
	a := makeApp()
	model, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = model.(App)

	// Type some text.
	for _, ch := range "hello world" {
		model, _ = a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		a = model.(App)
	}
	if a.input.Value() == "" {
		t.Fatal("precondition: input should have text")
	}

	// First Esc — should NOT clear.
	model, _ = a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	a = model.(App)
	if a.input.Value() == "" {
		t.Error("single Esc should not clear input")
	}

	// Second Esc immediately — should clear.
	model, _ = a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	a = model.(App)
	if a.input.Value() != "" {
		t.Errorf("double Esc should clear input, got %q", a.input.Value())
	}
}

func TestApp_SingleEsc_DoesNotClear(t *testing.T) {
	a := makeApp()
	model, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = model.(App)

	// Type some text.
	for _, ch := range "hello" {
		model, _ = a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		a = model.(App)
	}

	// Single Esc.
	model, _ = a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	a = model.(App)

	if a.input.Value() == "" {
		t.Error("single Esc should not clear input")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run 'TestApp_DoubleEsc|TestApp_SingleEsc_DoesNotClear' -v`
Expected: FAIL — double Esc does nothing currently.

- [ ] **Step 3: Write minimal implementation**

Add `lastEscTime` to `Input` struct in `internal/tui/input.go`:

```go
import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type Input struct {
	ta          textarea.Model
	mode        InputMode
	agentText   string
	width       int
	lastEscTime time.Time
}
```

In `internal/tui/app.go`, add double-Esc handling in the `tea.KeyMsg` case, after the modal check (Layer 1) and before Layer 3. Insert between the `Ctrl+C` check and the `handleCompletingKeys` call:

```go
		// Double-Esc clears the input (300ms window).
		if m.Type == tea.KeyEsc && a.input.mode == InputModeHuman && a.inputFSM.Current() == StateNormal {
			now := time.Now()
			if !a.input.lastEscTime.IsZero() && now.Sub(a.input.lastEscTime) < 300*time.Millisecond {
				a.input.Reset()
				a.input.lastEscTime = time.Time{}
			} else {
				a.input.lastEscTime = now
			}
			return a, nil
		}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tui/ -run 'TestApp_DoubleEsc|TestApp_SingleEsc_DoesNotClear' -v`
Expected: PASS

- [ ] **Step 5: Run all tests to check for regressions**

Run: `go test ./internal/tui/ -v -timeout 30s`
Expected: All pass. The modal dismiss test (`TestApp_ModalDismissedByEsc`) still works because modal Esc is handled in Layer 1 before our new check.

- [ ] **Step 6: Commit**

```bash
git add internal/tui/input.go internal/tui/app.go internal/tui/app_test.go
git commit -m "feat: double Esc clears input with 300ms timeout window (#48)"
```

---

### Task 3: Remove backslash-newline

**Files:**
- Modify: `internal/tui/input.go:165-173` (delete `handleBackslashNewline` function)
- Modify: `internal/tui/app.go:160-166` (remove call site in Enter handler)
- Modify: `internal/tui/input_test.go:103-120` (delete two backslash-newline tests)

- [ ] **Step 1: Delete the backslash-newline tests**

In `internal/tui/input_test.go`, delete `TestInputBackslashNewline` and `TestInputBackslashNewline_NoTrailingBackslash` (the two test functions at lines 103-120).

- [ ] **Step 2: Delete the `handleBackslashNewline` function**

In `internal/tui/input.go`, delete the `handleBackslashNewline` function (lines 165-173):

```go
// DELETE THIS ENTIRE BLOCK:
// handleBackslashNewline checks if text ends with a backslash.
// If so, returns the text with the backslash replaced by a newline and true.
// Otherwise returns the original text and false.
func handleBackslashNewline(text string) (string, bool) {
	if len(text) > 0 && text[len(text)-1] == '\\' {
		return text[:len(text)-1] + "\n", true
	}
	return text, false
}
```

- [ ] **Step 3: Remove the call site in app.go**

In `internal/tui/app.go`, in the Enter handler (inside `case tea.KeyMsg`), remove the backslash-newline check. Delete these lines:

```go
			if newText, consumed := handleBackslashNewline(text); consumed {
				a.input.ta.SetValue(newText)
				return a, nil
			}
```

The Enter handler should go directly from `text := a.input.Value()` to `text = strings.TrimSpace(text)`.

- [ ] **Step 4: Run all tests**

Run: `go test ./internal/tui/ -v -timeout 30s`
Expected: All pass — no references to the deleted function remain.

- [ ] **Step 5: Run full project tests**

Run: `go test ./... -timeout 30s`
Expected: All pass.

- [ ] **Step 6: Commit**

```bash
git add internal/tui/input.go internal/tui/app.go internal/tui/input_test.go
git commit -m "refactor: remove backslash-newline in favor of Shift+Enter (#48)"
```

---

### Task 4: Final verification

**Files:** None (read-only checks)

- [ ] **Step 1: Run full test suite with race detector**

Run: `go test ./... -timeout 30s -race`
Expected: All pass, no races.

- [ ] **Step 2: Run linter**

Run: `go run github.com/golangci/golangci-lint/cmd/golangci-lint@latest run ./... --timeout=5m`
Expected: No issues.

- [ ] **Step 3: Build binary**

Run: `go build -o parley ./cmd/parley`
Expected: Compiles successfully.
