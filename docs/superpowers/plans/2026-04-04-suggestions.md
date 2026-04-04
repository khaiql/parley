# Chat Input Suggestions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add autocomplete suggestions for slash commands (`/`) and agent mentions (`@`) in the TUI chat input.

**Architecture:** A standalone `Suggestions` component renders between chat and input. App detects trigger characters, builds item lists from the registry/sidebar, and routes keys. The Suggestions component is generic — it knows nothing about commands or participants.

**Tech Stack:** Go, Bubble Tea, Lipgloss

---

### Task 1: Extend Registry with `Commands()` method

**Files:**
- Modify: `internal/command/registry.go:60-65`
- Modify: `internal/command/command_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/command/command_test.go`:

```go
func TestRegistryCommands_ReturnsFullObjects(t *testing.T) {
	reg := NewRegistry()
	reg.Register(InfoCommand)
	reg.Register(SaveCommand)

	cmds := reg.Commands()
	if len(cmds) != 2 {
		t.Fatalf("expected 2 commands, got %d", len(cmds))
	}
	if cmds[0].Name != "info" {
		t.Errorf("expected first command 'info', got %q", cmds[0].Name)
	}
	if cmds[0].Description == "" {
		t.Error("expected non-empty description for info command")
	}
	if cmds[1].Name != "save" {
		t.Errorf("expected second command 'save', got %q", cmds[1].Name)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/command/ -run TestRegistryCommands -v`
Expected: FAIL — `reg.Commands undefined`

- [ ] **Step 3: Write minimal implementation**

Add to `internal/command/registry.go` after the `Available()` method:

```go
// Commands returns the registered commands in insertion order.
func (r *Registry) Commands() []*Command {
	out := make([]*Command, len(r.order))
	for i, name := range r.order {
		out[i] = r.commands[name]
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/command/ -run TestRegistryCommands -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/command/registry.go internal/command/command_test.go
git commit -m "feat(command): add Commands() method returning full objects"
```

---

### Task 2: Create `Suggestions` component — data model and filtering

**Files:**
- Create: `internal/tui/suggestions.go`
- Create: `internal/tui/suggestions_test.go`

- [ ] **Step 1: Write failing tests for SuggestionItem, SetItems, and Filter**

Create `internal/tui/suggestions_test.go`:

```go
package tui

import "testing"

func TestSuggestions_SetItems_ReplacesAll(t *testing.T) {
	s := NewSuggestions(80)
	s.SetItems([]SuggestionItem{
		{Label: "/info", Description: "Room info"},
		{Label: "/save", Description: "Save state"},
	})

	if len(s.filtered) != 2 {
		t.Fatalf("expected 2 filtered items, got %d", len(s.filtered))
	}
	if !s.Visible() {
		t.Error("expected suggestions to be visible after SetItems")
	}
}

func TestSuggestions_Filter_PrefixMatch(t *testing.T) {
	s := NewSuggestions(80)
	s.SetItems([]SuggestionItem{
		{Label: "/info", Description: "Room info"},
		{Label: "/save", Description: "Save state"},
		{Label: "/send_command", Description: "Send to agent"},
	})
	s.Filter("sa")

	if len(s.filtered) != 1 {
		t.Fatalf("expected 1 match for 'sa', got %d", len(s.filtered))
	}
	if s.filtered[0].Label != "/save" {
		t.Errorf("expected /save, got %s", s.filtered[0].Label)
	}
}

func TestSuggestions_Filter_CaseInsensitive(t *testing.T) {
	s := NewSuggestions(80)
	s.SetItems([]SuggestionItem{
		{Label: "@Claude", Description: "agent"},
	})
	s.Filter("cl")

	if len(s.filtered) != 1 {
		t.Fatalf("expected 1 match for 'cl', got %d", len(s.filtered))
	}
}

func TestSuggestions_Filter_EmptyQuery_ShowsAll(t *testing.T) {
	s := NewSuggestions(80)
	s.SetItems([]SuggestionItem{
		{Label: "/info", Description: "Room info"},
		{Label: "/save", Description: "Save state"},
	})
	s.Filter("")

	if len(s.filtered) != 2 {
		t.Fatalf("expected 2 items with empty query, got %d", len(s.filtered))
	}
}

func TestSuggestions_Filter_NoMatch_Hides(t *testing.T) {
	s := NewSuggestions(80)
	s.SetItems([]SuggestionItem{
		{Label: "/info", Description: "Room info"},
	})
	s.Filter("xyz")

	if len(s.filtered) != 0 {
		t.Fatalf("expected 0 matches for 'xyz', got %d", len(s.filtered))
	}
	if s.Visible() {
		t.Error("expected suggestions to hide when no matches")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tui/ -run TestSuggestions -v`
Expected: FAIL — `NewSuggestions undefined`

- [ ] **Step 3: Write minimal implementation**

Create `internal/tui/suggestions.go`:

```go
package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const maxSuggestionItems = 5

// SuggestionItem is a single autocomplete option.
type SuggestionItem struct {
	Label       string // displayed and inserted text (e.g., "/save", "@claude")
	Description string // shown next to label (e.g., "Save room state")
}

// Suggestions renders a filtered list of autocomplete options.
type Suggestions struct {
	items      []SuggestionItem
	filtered   []SuggestionItem
	query      string
	cursor     int
	visible    bool
	width      int
}

// NewSuggestions creates a Suggestions component with the given width.
func NewSuggestions(width int) Suggestions {
	return Suggestions{width: width}
}

// SetItems replaces the full item list, resets filter and cursor, and shows the list.
func (s *Suggestions) SetItems(items []SuggestionItem) {
	s.items = items
	s.query = ""
	s.cursor = 0
	s.filtered = make([]SuggestionItem, len(items))
	copy(s.filtered, items)
	s.visible = len(s.filtered) > 0
}

// Filter narrows the list by case-insensitive prefix match on Label.
// The prefix is the part of the label after the trigger character.
// For example, filtering "/save" with query "sa" matches because
// we strip the first character (trigger) before comparing.
func (s *Suggestions) Filter(query string) {
	s.query = query
	s.filtered = s.filtered[:0]
	q := strings.ToLower(query)
	for _, item := range s.items {
		// Strip the trigger character (first char) from label for matching.
		label := item.Label
		if len(label) > 1 {
			label = label[1:]
		}
		if strings.HasPrefix(strings.ToLower(label), q) {
			s.filtered = append(s.filtered, item)
		}
	}
	s.cursor = 0
	s.visible = len(s.filtered) > 0
}

// Visible reports whether the suggestion list is showing.
func (s Suggestions) Visible() bool {
	return s.visible
}

// Hide closes the suggestion list.
func (s *Suggestions) Hide() {
	s.visible = false
}

// Selected returns the item at the cursor position.
func (s Suggestions) Selected() SuggestionItem {
	if len(s.filtered) == 0 {
		return SuggestionItem{}
	}
	return s.filtered[s.cursor]
}

// SetWidth updates the rendering width.
func (s *Suggestions) SetWidth(width int) {
	s.width = width
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/tui/ -run TestSuggestions -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tui/suggestions.go internal/tui/suggestions_test.go
git commit -m "feat(tui): add Suggestions component with filtering"
```

---

### Task 3: Add navigation and View rendering to Suggestions

**Files:**
- Modify: `internal/tui/suggestions.go`
- Modify: `internal/tui/suggestions_test.go`

- [ ] **Step 1: Write failing tests for MoveUp, MoveDown, and View**

Append to `internal/tui/suggestions_test.go`:

```go
func TestSuggestions_MoveDown_Wraps(t *testing.T) {
	s := NewSuggestions(80)
	s.SetItems([]SuggestionItem{
		{Label: "/a", Description: "A"},
		{Label: "/b", Description: "B"},
		{Label: "/c", Description: "C"},
	})

	s.MoveDown()
	if s.cursor != 1 {
		t.Errorf("expected cursor 1 after MoveDown, got %d", s.cursor)
	}
	s.MoveDown()
	s.MoveDown() // wraps
	if s.cursor != 0 {
		t.Errorf("expected cursor 0 after wrap, got %d", s.cursor)
	}
}

func TestSuggestions_MoveUp_Wraps(t *testing.T) {
	s := NewSuggestions(80)
	s.SetItems([]SuggestionItem{
		{Label: "/a", Description: "A"},
		{Label: "/b", Description: "B"},
	})

	s.MoveUp() // wraps to end
	if s.cursor != 1 {
		t.Errorf("expected cursor 1 after MoveUp wrap, got %d", s.cursor)
	}
}

func TestSuggestions_Selected_ReturnsCursorItem(t *testing.T) {
	s := NewSuggestions(80)
	s.SetItems([]SuggestionItem{
		{Label: "/info", Description: "Info"},
		{Label: "/save", Description: "Save"},
	})
	s.MoveDown()

	sel := s.Selected()
	if sel.Label != "/save" {
		t.Errorf("expected /save, got %s", sel.Label)
	}
}

func TestSuggestions_View_ContainsLabels(t *testing.T) {
	s := NewSuggestions(60)
	s.SetItems([]SuggestionItem{
		{Label: "/info", Description: "Room info"},
		{Label: "/save", Description: "Save state"},
	})

	view := stripANSI(s.View())
	if !strings.Contains(view, "/info") {
		t.Errorf("view should contain /info, got:\n%s", view)
	}
	if !strings.Contains(view, "/save") {
		t.Errorf("view should contain /save, got:\n%s", view)
	}
	if !strings.Contains(view, "Room info") {
		t.Errorf("view should contain description, got:\n%s", view)
	}
}

func TestSuggestions_View_MaxVisible(t *testing.T) {
	s := NewSuggestions(60)
	items := make([]SuggestionItem, 8)
	for i := range items {
		items[i] = SuggestionItem{Label: fmt.Sprintf("/cmd%d", i), Description: "desc"}
	}
	s.SetItems(items)

	view := stripANSI(s.View())
	// Should show at most 5 items.
	count := 0
	for i := 0; i < 8; i++ {
		if strings.Contains(view, fmt.Sprintf("/cmd%d", i)) {
			count++
		}
	}
	if count > maxSuggestionItems {
		t.Errorf("expected at most %d visible items, got %d", maxSuggestionItems, count)
	}
}

func TestSuggestions_View_Hidden_Empty(t *testing.T) {
	s := NewSuggestions(60)
	// Not visible by default.
	if s.View() != "" {
		t.Errorf("expected empty view when not visible, got: %q", s.View())
	}
}

func TestSuggestions_Height(t *testing.T) {
	s := NewSuggestions(60)
	if s.Height() != 0 {
		t.Errorf("expected height 0 when not visible, got %d", s.Height())
	}

	s.SetItems([]SuggestionItem{
		{Label: "/a", Description: "A"},
		{Label: "/b", Description: "B"},
	})
	// 2 items + 2 border lines
	if s.Height() != 4 {
		t.Errorf("expected height 4, got %d", s.Height())
	}
}
```

Add `"fmt"` and `"strings"` to the imports in the test file.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tui/ -run TestSuggestions -v`
Expected: FAIL — `s.MoveDown undefined`, `s.View undefined`, `s.Height undefined`

- [ ] **Step 3: Write MoveUp, MoveDown, Height, and View**

Append to `internal/tui/suggestions.go`:

```go
// MoveDown advances the cursor, wrapping at the end.
func (s *Suggestions) MoveDown() {
	if len(s.filtered) == 0 {
		return
	}
	s.cursor = (s.cursor + 1) % len(s.filtered)
}

// MoveUp moves the cursor back, wrapping to the end.
func (s *Suggestions) MoveUp() {
	if len(s.filtered) == 0 {
		return
	}
	s.cursor = (s.cursor - 1 + len(s.filtered)) % len(s.filtered)
}

// Height returns the total rendered height (0 when hidden).
func (s Suggestions) Height() int {
	if !s.visible || len(s.filtered) == 0 {
		return 0
	}
	n := len(s.filtered)
	if n > maxSuggestionItems {
		n = maxSuggestionItems
	}
	return n + 2 // items + top/bottom border
}

// View renders the suggestion list.
func (s Suggestions) View() string {
	if !s.visible || len(s.filtered) == 0 {
		return ""
	}

	// Determine the visible window of items.
	n := len(s.filtered)
	start := 0
	visible := n
	if visible > maxSuggestionItems {
		visible = maxSuggestionItems
		// Scroll so the cursor is always visible.
		if s.cursor >= start+visible {
			start = s.cursor - visible + 1
		}
		if s.cursor < start {
			start = s.cursor
		}
	}

	labelStyle := lipgloss.NewStyle().Foreground(colorText).Bold(true)
	descStyle := lipgloss.NewStyle().Foreground(colorDimText)
	selectedStyle := lipgloss.NewStyle().Foreground(colorPrimary).Bold(true)

	var rows []string
	for i := start; i < start+visible && i < n; i++ {
		item := s.filtered[i]
		if i == s.cursor {
			row := selectedStyle.Render(item.Label + "  " + item.Description)
			rows = append(rows, row)
		} else {
			row := labelStyle.Render(item.Label) + "  " + descStyle.Render(item.Description)
			rows = append(rows, row)
		}
	}

	content := strings.Join(rows, "\n")

	boxStyle := lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(colorBorder).
		Padding(0, 1).
		Width(s.width)

	return boxStyle.Render(content)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/tui/ -run TestSuggestions -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tui/suggestions.go internal/tui/suggestions_test.go
git commit -m "feat(tui): add navigation and rendering to Suggestions"
```

---

### Task 4: Add `ReplaceRange` to Input

**Files:**
- Modify: `internal/tui/input.go`
- Modify: `internal/tui/input_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/tui/input_test.go`:

```go
func TestInput_ReplaceRange(t *testing.T) {
	inp := NewInput()
	inp.SetWidth(80)

	// Simulate typing "hello @clau" by setting textarea value.
	inp.ta.SetValue("hello @clau")

	// Replace "@clau" (positions 6-11) with "@claude "
	inp.ReplaceRange(6, 11, "@claude ")

	got := inp.Value()
	if got != "hello @claude " {
		t.Errorf("expected 'hello @claude ', got %q", got)
	}
}

func TestInput_ReplaceRange_AtStart(t *testing.T) {
	inp := NewInput()
	inp.SetWidth(80)

	inp.ta.SetValue("/sa")

	// Replace entire input (positions 0-3) with "/save "
	inp.ReplaceRange(0, 3, "/save ")

	got := inp.Value()
	if got != "/save " {
		t.Errorf("expected '/save ', got %q", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tui/ -run TestInput_ReplaceRange -v`
Expected: FAIL — `inp.ReplaceRange undefined`

- [ ] **Step 3: Write minimal implementation**

Add to `internal/tui/input.go`:

```go
// ReplaceRange replaces characters from position start to end with the given
// text and positions the cursor after the inserted text.
func (i *Input) ReplaceRange(start, end int, text string) {
	val := i.ta.Value()
	runes := []rune(val)
	if start < 0 {
		start = 0
	}
	if end > len(runes) {
		end = len(runes)
	}
	newRunes := append(runes[:start], append([]rune(text), runes[end:]...)...)
	i.ta.SetValue(string(newRunes))
	// Position cursor after inserted text.
	cursorPos := start + len([]rune(text))
	i.ta.SetCursor(cursorPos)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/tui/ -run TestInput_ReplaceRange -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tui/input.go internal/tui/input_test.go
git commit -m "feat(tui): add ReplaceRange method to Input"
```

---

### Task 5: Wire Suggestions into App — trigger detection, key routing, selection

**Files:**
- Modify: `internal/tui/app.go`
- Modify: `internal/tui/app_test.go`

- [ ] **Step 1: Write failing tests for trigger detection and selection**

Append to `internal/tui/app_test.go`. Note: the file already imports `fmt`, `tea`, `protocol`, and `command` (via `"github.com/khaiql/parley/internal/command"`). Add the `command` import if not present.

```go
func TestApp_SlashTrigger_ActivatesSuggestions(t *testing.T) {
	a := makeApp()
	reg := command.NewRegistry()
	reg.Register(&command.Command{Name: "info", Usage: "/info", Description: "Room info"})
	reg.Register(&command.Command{Name: "save", Usage: "/save", Description: "Save state"})
	a.SetCommandRegistry(reg, command.Context{})

	// Type "/" into the input.
	a.input.ta.SetValue("/")
	a.checkSuggestionTrigger()

	if !a.suggestions.Visible() {
		t.Error("expected suggestions visible after typing /")
	}
	if a.completionTrigger != '/' {
		t.Errorf("expected trigger '/', got %c", a.completionTrigger)
	}
}

func TestApp_AtTrigger_ActivatesSuggestions(t *testing.T) {
	a := makeApp()
	a.sidebar.SetParticipants([]protocol.Participant{
		{Name: "claude", Role: "agent", Online: true},
		{Name: "gemini", Role: "agent", Online: true},
	})

	// Type "@" into the input.
	a.input.ta.SetValue("@")
	a.checkSuggestionTrigger()

	if !a.suggestions.Visible() {
		t.Error("expected suggestions visible after typing @")
	}
	if a.completionTrigger != '@' {
		t.Errorf("expected trigger '@', got %c", a.completionTrigger)
	}
}

func TestApp_AtTrigger_MidMessage(t *testing.T) {
	a := makeApp()
	a.sidebar.SetParticipants([]protocol.Participant{
		{Name: "claude", Role: "agent", Online: true},
	})

	// Type "hello @" — @ after whitespace should trigger.
	a.input.ta.SetValue("hello @")
	a.checkSuggestionTrigger()

	if !a.suggestions.Visible() {
		t.Error("expected suggestions visible after 'hello @'")
	}
	if a.completionStart != 6 {
		t.Errorf("expected completionStart 6, got %d", a.completionStart)
	}
}

func TestApp_AtTrigger_NotAfterAlpha(t *testing.T) {
	a := makeApp()
	a.sidebar.SetParticipants([]protocol.Participant{
		{Name: "claude", Role: "agent", Online: true},
	})

	// Type "email@" — @ not after whitespace should NOT trigger.
	a.input.ta.SetValue("email@")
	a.checkSuggestionTrigger()

	if a.suggestions.Visible() {
		t.Error("expected suggestions NOT visible after 'email@'")
	}
}

func TestApp_SlashTrigger_NilRegistry_NoActivation(t *testing.T) {
	a := makeApp()
	// No registry set.

	a.input.ta.SetValue("/")
	a.checkSuggestionTrigger()

	if a.suggestions.Visible() {
		t.Error("expected suggestions NOT visible when registry is nil")
	}
}

func TestApp_AcceptSuggestion_InsertsText(t *testing.T) {
	a := makeApp()
	a.sidebar.SetParticipants([]protocol.Participant{
		{Name: "claude", Role: "agent", Online: true},
		{Name: "gemini", Role: "agent", Online: true},
	})

	// Simulate typing "hello @cl" and triggering suggestions.
	a.input.ta.SetValue("hello @cl")
	a.completionTrigger = '@'
	a.completionStart = 6
	a.suggestions.SetItems([]SuggestionItem{
		{Label: "@claude", Description: "agent"},
		{Label: "@gemini", Description: "agent"},
	})
	a.suggestions.Filter("cl")

	a.acceptSuggestion()

	got := a.input.Value()
	if got != "hello @claude " {
		t.Errorf("expected 'hello @claude ', got %q", got)
	}
	if a.suggestions.Visible() {
		t.Error("expected suggestions hidden after accept")
	}
}

func TestApp_FilterSuggestions_NarrowsList(t *testing.T) {
	a := makeApp()
	reg := command.NewRegistry()
	reg.Register(&command.Command{Name: "info", Usage: "/info", Description: "Room info"})
	reg.Register(&command.Command{Name: "save", Usage: "/save", Description: "Save state"})
	a.SetCommandRegistry(reg, command.Context{})

	// Activate suggestions with "/".
	a.input.ta.SetValue("/")
	a.checkSuggestionTrigger()

	// Type "s" — should filter to just /save.
	a.input.ta.SetValue("/s")
	a.updateSuggestionFilter()

	if len(a.suggestions.filtered) != 1 {
		t.Fatalf("expected 1 filtered item, got %d", len(a.suggestions.filtered))
	}
	if a.suggestions.filtered[0].Label != "/save" {
		t.Errorf("expected /save, got %s", a.suggestions.filtered[0].Label)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tui/ -run "TestApp_(Slash|At|Accept|Filter)" -v`
Expected: FAIL — methods not defined

- [ ] **Step 3: Write the App integration**

Add new fields and methods to `internal/tui/app.go`.

Add three new fields to the `App` struct in `internal/tui/app.go`, after the existing `spinnerActive` field:

```go
	suggestions       Suggestions
	completionTrigger rune // '/' or '@', or 0 if inactive
	completionStart   int  // cursor position where trigger character was typed
```

Add helper methods:

```go
// checkSuggestionTrigger scans the current input value for a trigger character
// and activates suggestions if found.
func (a *App) checkSuggestionTrigger() {
	if a.suggestions.Visible() {
		return // already active
	}
	val := a.input.Value()
	if val == "" {
		return
	}
	runes := []rune(val)
	last := runes[len(runes)-1]

	switch last {
	case '/':
		// Only trigger at the very start of input.
		if len(runes) == 1 && a.registry != nil {
			a.completionTrigger = '/'
			a.completionStart = 0
			items := make([]SuggestionItem, 0)
			for _, cmd := range a.registry.Commands() {
				items = append(items, SuggestionItem{
					Label:       "/" + cmd.Name,
					Description: cmd.Description,
				})
			}
			a.suggestions.SetItems(items)
		}
	case '@':
		// Trigger at start of input or after whitespace.
		pos := len(runes) - 1
		if pos == 0 || runes[pos-1] == ' ' || runes[pos-1] == '\n' {
			a.completionTrigger = '@'
			a.completionStart = pos
			items := make([]SuggestionItem, 0)
			for _, p := range a.sidebar.participants {
				if p.Online {
					items = append(items, SuggestionItem{
						Label:       "@" + p.Name,
						Description: p.Role,
					})
				}
			}
			a.suggestions.SetItems(items)
		}
	}
}

// updateSuggestionFilter extracts the query from the current input and filters.
func (a *App) updateSuggestionFilter() {
	if !a.suggestions.Visible() {
		return
	}
	val := a.input.Value()
	runes := []rune(val)

	// If user deleted back past the trigger, dismiss.
	if len(runes) <= a.completionStart {
		a.dismissSuggestions()
		return
	}

	query := string(runes[a.completionStart+1:])
	a.suggestions.Filter(query)
}

// acceptSuggestion inserts the selected suggestion into the input.
func (a *App) acceptSuggestion() {
	sel := a.suggestions.Selected()
	if sel.Label == "" {
		a.dismissSuggestions()
		return
	}
	end := len([]rune(a.input.Value()))
	a.input.ReplaceRange(a.completionStart, end, sel.Label+" ")
	a.dismissSuggestions()
}

// dismissSuggestions hides the suggestion list and resets trigger state.
func (a *App) dismissSuggestions() {
	a.suggestions.Hide()
	a.completionTrigger = 0
	a.completionStart = 0
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/tui/ -run "TestApp_(Slash|At|Accept|Filter)" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tui/app.go internal/tui/app_test.go
git commit -m "feat(tui): wire Suggestions into App with trigger detection"
```

---

### Task 6: Integrate key routing and layout into App.Update and App.View

**Files:**
- Modify: `internal/tui/app.go`

- [ ] **Step 1: Update App.Update to route keys when suggestions are visible**

In `internal/tui/app.go`, modify the `Update` method's `tea.KeyMsg` handling. The current code has this structure:

```go
case tea.KeyMsg:
    switch m.Type {
    case tea.KeyCtrlC:
        return a, tea.Quit
    case tea.KeyEnter:
        // ... handleBackslashNewline + command dispatch + send
    default:
        // ignore other keys
    }
```

Replace the entire `case tea.KeyMsg:` block with:

```go
case tea.KeyMsg:
	switch m.Type {
	case tea.KeyCtrlC:
		return a, tea.Quit

	case tea.KeyUp:
		if a.suggestions.Visible() {
			a.suggestions.MoveUp()
			return a, nil
		}
	case tea.KeyDown:
		if a.suggestions.Visible() {
			a.suggestions.MoveDown()
			return a, nil
		}
	case tea.KeyTab:
		if a.suggestions.Visible() {
			a.acceptSuggestion()
			a.layout()
			return a, nil
		}
	case tea.KeyEsc, tea.KeyEscape:
		if a.suggestions.Visible() {
			a.dismissSuggestions()
			a.layout()
			return a, nil
		}

	case tea.KeyEnter:
		if a.suggestions.Visible() {
			a.acceptSuggestion()
			a.layout()
			return a, nil
		}
		if a.input.mode == InputModeHuman {
			text := a.input.Value()
			// Check for backslash-newline
			if newText, consumed := handleBackslashNewline(text); consumed {
				a.input.ta.SetValue(newText)
				return a, nil
			}
			text = strings.TrimSpace(text)
			if text != "" {
				a.input.Reset()
				// Slash command dispatch.
				if a.registry != nil && command.IsCommand(text) {
					result := a.registry.Execute(a.cmdCtx, text)
					if result.Error != nil {
						a.chat.AddMessage(systemMessage(result.Error.Error()))
					} else if result.LocalMessage != "" {
						a.chat.AddMessage(systemMessage(result.LocalMessage))
					}
					return a, nil
				}
				mentions := protocol.ParseMentions(text)
				if a.sendFn != nil {
					a.sendFn(text, mentions)
				}
			}
			return a, nil
		}
	default:
		// ignore other keys
	}
```

After the existing child component forwarding block (which currently forwards key events only to input, not chat):

```go
// Forward key events only to input, not chat (prevents scroll jumping).
cmds = append(cmds, a.input.Update(msg))
if _, isKey := msg.(tea.KeyMsg); !isKey {
    cmds = append(cmds, a.chat.Update(msg))
}
```

Add trigger detection and filter update right after:

```go
// Check for suggestion triggers and update filter after input changes.
if _, ok := msg.(tea.KeyMsg); ok && a.input.mode == InputModeHuman {
	if a.suggestions.Visible() {
		a.updateSuggestionFilter()
	} else {
		a.checkSuggestionTrigger()
	}
}
```

- [ ] **Step 2: Update App.View to render suggestions between chat and input**

Replace the `View` method. The current View renders: topbar, middle (chat+sidebar), input, statusbar. Insert suggestions between input and statusbar (just above the input visually makes sense, but since statusbar is at the very bottom, place suggestions between middle and input):

```go
func (a App) View() string {
	middle := lipgloss.JoinHorizontal(
		lipgloss.Top,
		a.chat.View(),
		a.sidebar.View(),
	)
	parts := []string{a.topbar.View(), middle}
	if a.suggestions.Visible() {
		parts = append(parts, a.suggestions.View())
	}
	parts = append(parts, a.input.View(), a.statusbar.View())
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}
```

- [ ] **Step 3: Update App.layout to account for suggestions height**

Replace the `layout` method. The current layout accounts for topbar (1), input, and statusbar (1). Add suggestionsHeight:

```go
func (a *App) layout() {
	topbarHeight := 1
	inputHeight := a.input.Height()
	statusbarHeight := 1
	suggestionsHeight := a.suggestions.Height()
	chatHeight := a.height - topbarHeight - inputHeight - statusbarHeight - suggestionsHeight
	if chatHeight < 0 {
		chatHeight = 0
	}
	chatWidth := a.width - sidebarWidth
	if chatWidth < 0 {
		chatWidth = 0
	}

	a.lastInputHeight = inputHeight
	a.topbar.SetWidth(a.width)
	a.chat.SetSize(chatWidth, chatHeight)
	a.sidebar.SetSize(sidebarWidth, chatHeight)
	a.input.SetWidth(a.width)
	a.statusbar.SetWidth(a.width)
	a.suggestions.SetWidth(a.width)
}
```

- [ ] **Step 4: Run all tests to verify nothing is broken**

Run: `go test ./internal/tui/ -v -timeout 30s`
Expected: All tests PASS

Run: `go test ./... -timeout 30s`
Expected: All tests PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tui/app.go
git commit -m "feat(tui): integrate key routing and layout for suggestions"
```

---

### Task 7: Build and manual smoke test

**Files:** None (verification only)

- [ ] **Step 1: Build the binary**

Run: `go build -o parley ./cmd/parley`
Expected: Builds successfully with no errors

- [ ] **Step 2: Run the full test suite one final time**

Run: `go test ./... -timeout 30s`
Expected: All tests PASS

- [ ] **Step 3: Commit any remaining changes and push**

```bash
git status
# If clean, nothing to do. If there are changes:
git add -A && git commit -m "chore: final cleanup for suggestions feature"
git push
```
