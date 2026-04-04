# /info Modal Overlay Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Display `/info` command output in a dismissable modal overlay instead of appending it to the chat history.

**Architecture:** Add a `ModalContent` struct to `command.Result` so commands can declare modal intent without coupling to the TUI layer. `InfoCommand` populates this struct. `App` holds a `*Modal` field; when non-nil it intercepts keyboard input and renders the modal full-screen centered via `lipgloss.Place()`, returning to normal view on Esc/q.

**Tech Stack:** Go, Bubble Tea (tea.Model), Lipgloss, bubbles/viewport

---

## File Map

| Action | File | Purpose |
|--------|------|---------|
| Modify | `internal/command/command.go` | Add `ModalContent` struct; add `Modal *ModalContent` to `Result` |
| Modify | `internal/command/cmd_info.go` | Return `Modal` instead of `LocalMessage` |
| Modify | `internal/command/command_test.go` | Update `TestInfoCommand` + `TestRegistryDispatch` to check `Modal` |
| Create | `internal/tui/modal.go` | `Modal` Bubble Tea component (viewport + title + footer) |
| Create | `internal/tui/modal_test.go` | Unit tests for `Modal` render and keyboard handling |
| Modify | `internal/tui/styles.go` | Add `modalStyle`, `modalTitleStyle`, `modalFooterStyle` |
| Modify | `internal/tui/app.go` | Add `modal *Modal`; route in `Update`; render in `View` |
| Modify | `internal/tui/app_test.go` | Add modal show/dismiss integration tests |
| Modify | `internal/tui/visual_test.go` | Add golden test for modal view |

---

### Task 1: Add `ModalContent` struct to command package

**Files:**
- Modify: `internal/command/command.go`

- [ ] **Step 1: Write the failing test**

In `internal/command/command_test.go`, add at the bottom:

```go
func TestResult_HasModalField(t *testing.T) {
	// Compile-time assertion that Result has a Modal field of the right type.
	r := Result{
		Modal: &ModalContent{
			Title:  "Test",
			Body:   "hello",
			Width:  80,
			Height: 24,
		},
	}
	if r.Modal == nil {
		t.Fatal("Modal field must not be nil")
	}
	if r.Modal.Title != "Test" {
		t.Errorf("unexpected Title: %s", r.Modal.Title)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /Users/sle/group_chat/.claude/worktrees/objective-mcnulty
go test ./internal/command/... -run TestResult_HasModalField -v
```

Expected: FAIL — `ModalContent` undefined / `Modal` field not found.

- [ ] **Step 3: Add `ModalContent` struct and `Modal` field to `Result`**

In `internal/command/command.go`, replace the `Result` block:

```go
// ModalContent describes content to be displayed in a modal overlay.
// Using a struct (rather than a plain string) allows future callers to
// customize title, size hints, styling, and actions without changing the
// Result API.
type ModalContent struct {
	Title  string // header text displayed at the top of the modal
	Body   string // pre-formatted body text (plain text, not markdown)
	Width  int    // 0 = auto-sized (80 % of terminal width)
	Height int    // 0 = auto-sized (75 % of terminal height)
}

// Result is what a command returns to the TUI.
type Result struct {
	LocalMessage string        // displayed as a local system message in chat
	Modal        *ModalContent // when non-nil, display in a modal overlay
	Error        error
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/command/... -run TestResult_HasModalField -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/command/command.go internal/command/command_test.go
git commit -m "feat(command): add ModalContent struct and Modal field to Result

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

### Task 2: Update `InfoCommand` to return `Modal`

**Files:**
- Modify: `internal/command/cmd_info.go`
- Modify: `internal/command/command_test.go`

- [ ] **Step 1: Update `TestInfoCommand` to assert `Modal` (not `LocalMessage`)**

In `internal/command/command_test.go`, replace `TestInfoCommand`:

```go
func TestInfoCommand(t *testing.T) {
	ctx := Context{Room: newTestRoom()}
	result := InfoCommand.Execute(ctx, "")
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.LocalMessage != "" {
		t.Error("InfoCommand should not set LocalMessage; use Modal instead")
	}
	if result.Modal == nil {
		t.Fatal("expected non-nil Modal from InfoCommand")
	}
	if result.Modal.Title == "" {
		t.Error("Modal.Title must not be empty")
	}
	body := result.Modal.Body
	for _, want := range []string{"room-abc", "test-topic", "9000", "42", "host-user", "atlas"} {
		if !strings.Contains(body, want) {
			t.Errorf("Modal.Body should contain %q, got:\n%s", want, body)
		}
	}
	if !strings.Contains(body, "parley join --port 9000 -- claude") {
		t.Errorf("Modal.Body should contain join command, got:\n%s", body)
	}
	if !strings.Contains(body, "resume: parley join --port 9000 --name nova --resume -- claude") {
		t.Errorf("Modal.Body should contain resume command for nova, got:\n%s", body)
	}
	if !strings.Contains(body, "resume: parley join --port 9000 --name echo --resume -- gemini") {
		t.Errorf("Modal.Body should contain resume command for echo, got:\n%s", body)
	}
}
```

Also update `TestRegistryDispatch` — replace the `result.LocalMessage` check:

```go
func TestRegistryDispatch(t *testing.T) {
	reg := NewRegistry()
	reg.Register(InfoCommand)
	reg.Register(SaveCommand)

	ctx := Context{Room: newTestRoom()}
	result := reg.Execute(ctx, "/info")
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Modal == nil {
		t.Fatal("expected non-nil Modal from /info dispatch")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/command/... -run "TestInfoCommand|TestRegistryDispatch" -v
```

Expected: FAIL — `result.LocalMessage` not empty, `result.Modal` nil.

- [ ] **Step 3: Update `cmd_info.go` to return `Modal`**

Replace the final `return` statement in `internal/command/cmd_info.go`:

```go
return Result{Modal: &ModalContent{Title: "Room Info", Body: info}}
```

The full file becomes:

```go
package command

import "fmt"

// InfoCommand displays current room information.
var InfoCommand = &Command{
	Name:        "info",
	Usage:       "/info",
	Description: "Display current room information",
	Execute: func(ctx Context, _ string) Result {
		room := ctx.Room
		participants := room.GetParticipants()
		port := room.GetPort()

		info := fmt.Sprintf("Room: %s\nTopic: %s\nPort: %d\nParticipants: %d\nMessages: %d\n",
			room.GetID(),
			room.GetTopic(),
			port,
			len(participants),
			room.GetMessageCount(),
		)

		if len(participants) > 0 {
			info += "\nParticipants:\n"
			for _, p := range participants {
				status := "online"
				if !p.Online {
					status = "offline"
				}
				line := fmt.Sprintf("  • %s (%s) [%s]", p.Name, p.Role, status)
				if p.Directory != "" {
					line += fmt.Sprintf(" — %s", p.Directory)
				}
				info += line + "\n"

				if !p.Online && p.AgentType != "" {
					agentCmd := p.AgentType
					info += fmt.Sprintf("      resume: parley join --port %d --name %s --resume -- %s\n", port, p.Name, agentCmd)
				}
			}
		}

		info += fmt.Sprintf("\nJoin command:\n  parley join --port %d -- claude\n", port)

		return Result{Modal: &ModalContent{Title: "Room Info", Body: info}}
	},
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/command/... -v
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/command/cmd_info.go internal/command/command_test.go
git commit -m "feat(command): /info returns ModalContent instead of LocalMessage

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

### Task 3: Add modal styles

**Files:**
- Modify: `internal/tui/styles.go`

- [ ] **Step 1: Append modal styles**

Add to the bottom of `internal/tui/styles.go`:

```go
	modalStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(colorPrimary).
			Padding(0, 1)

	modalTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary).
			MarginBottom(1)

	modalFooterStyle = lipgloss.NewStyle().
			Foreground(colorDimText).
			Italic(true).
			MarginTop(1)
```

The full `var` block at the bottom of `styles.go` becomes:

```go
var (
	topBarStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("#161b22")).
			Foreground(colorText).
			Padding(0, 1).
			Bold(true)

	sidebarStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderLeft(true).
			BorderForeground(colorBorder).
			Foreground(colorText).
			Padding(0, 1)

	sidebarTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorPrimary).
				MarginBottom(1)

	inputStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderTop(true).
			BorderForeground(colorBorder).
			Padding(0, 1)

	systemMsgStyle = lipgloss.NewStyle().
			Foreground(colorSystem).
			Italic(true)

	timestampStyle = lipgloss.NewStyle().
			Foreground(colorDimText)

	participantStatusStyle = lipgloss.NewStyle().
				Foreground(colorSystem).
				Italic(true)

	listeningStatusStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#3fb950")).
				Italic(true)

	offlineNameStyle = lipgloss.NewStyle().
				Foreground(colorDimText).
				Italic(true)

	modalStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(colorPrimary).
			Padding(0, 1)

	modalTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary).
			MarginBottom(1)

	modalFooterStyle = lipgloss.NewStyle().
			Foreground(colorDimText).
			Italic(true).
			MarginTop(1)
)
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/tui/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/tui/styles.go
git commit -m "feat(tui): add modal overlay styles

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

### Task 4: Create `Modal` Bubble Tea component

**Files:**
- Create: `internal/tui/modal.go`
- Create: `internal/tui/modal_test.go`

- [ ] **Step 1: Write failing tests in `internal/tui/modal_test.go`**

```go
package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/khaiql/parley/internal/command"
)

func TestModal_View_ContainsTitleAndBody(t *testing.T) {
	content := &command.ModalContent{Title: "Room Info", Body: "Port: 9000\nTopic: test"}
	m := NewModal(content, 80, 24)
	view := m.View()

	if !strings.Contains(view, "Room Info") {
		t.Errorf("expected title 'Room Info' in view, got:\n%s", view)
	}
	if !strings.Contains(view, "Port: 9000") {
		t.Errorf("expected body content in view, got:\n%s", view)
	}
}

func TestModal_View_ContainsDismissHint(t *testing.T) {
	content := &command.ModalContent{Title: "Info", Body: "body"}
	m := NewModal(content, 80, 24)
	view := m.View()

	if !strings.Contains(view, "esc") && !strings.Contains(view, "Esc") {
		t.Errorf("expected dismiss hint in view, got:\n%s", view)
	}
}

func TestModal_Update_ScrollingDoesNotPanic(t *testing.T) {
	content := &command.ModalContent{Title: "Info", Body: "line1\nline2\nline3"}
	m := NewModal(content, 80, 24)

	// Sending a PageDown should not panic.
	m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
}

func TestModal_Resize_UpdatesDimensions(t *testing.T) {
	content := &command.ModalContent{Title: "Info", Body: "body"}
	m := NewModal(content, 80, 24)
	m.Resize(120, 40)

	if m.termWidth != 120 || m.termHeight != 40 {
		t.Errorf("expected 120x40 after resize, got %dx%d", m.termWidth, m.termHeight)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/tui/... -run "TestModal_" -v
```

Expected: FAIL — `NewModal` and `Modal` undefined.

- [ ] **Step 3: Create `internal/tui/modal.go`**

```go
package tui

import (
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/khaiql/parley/internal/command"
)

// Modal is a dismissable overlay component that renders a titled, scrollable
// content box centered within the terminal. Dismiss with Esc or q.
type Modal struct {
	content    *command.ModalContent
	vp         viewport.Model
	termWidth  int
	termHeight int
}

// NewModal creates a Modal sized to fit within the given terminal dimensions.
func NewModal(content *command.ModalContent, termWidth, termHeight int) Modal {
	m := Modal{content: content}
	m.applySize(termWidth, termHeight)
	return m
}

// Resize recalculates the modal dimensions for a new terminal size.
func (m *Modal) Resize(termWidth, termHeight int) {
	m.applySize(termWidth, termHeight)
}

// applySize computes box and viewport dimensions from terminal size and content hints.
func (m *Modal) applySize(termWidth, termHeight int) {
	m.termWidth = termWidth
	m.termHeight = termHeight

	boxW := termWidth * 4 / 5
	boxH := termHeight * 3 / 4
	if m.content.Width > 0 {
		boxW = m.content.Width
	}
	if m.content.Height > 0 {
		boxH = m.content.Height
	}
	// Clamp to terminal size.
	if boxW > termWidth {
		boxW = termWidth
	}
	if boxH > termHeight {
		boxH = termHeight
	}

	// Inner viewport: subtract border (2) + horizontal padding (2) for width;
	// subtract border (2) + title (1) + margin (1) + footer (1) + margin (1) for height.
	vpW := boxW - 4
	vpH := boxH - 6
	if vpW < 10 {
		vpW = 10
	}
	if vpH < 1 {
		vpH = 1
	}

	m.vp = viewport.New(vpW, vpH)
	m.vp.SetContent(m.content.Body)
}

// Update forwards scroll key events to the inner viewport.
// Dismiss keys (Esc, q) are handled by App, not here.
func (m *Modal) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return cmd
}

// View renders the modal box centered within the terminal.
func (m Modal) View() string {
	vpW := m.vp.Width

	title := modalTitleStyle.Render(m.content.Title)
	body := m.vp.View()
	footer := modalFooterStyle.Render("esc · q  close")

	inner := lipgloss.JoinVertical(lipgloss.Left, title, body, footer)
	box := modalStyle.Width(vpW).Render(inner)

	return lipgloss.Place(
		m.termWidth, m.termHeight,
		lipgloss.Center, lipgloss.Center,
		box,
	)
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/tui/... -run "TestModal_" -v
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/modal.go internal/tui/modal_test.go
git commit -m "feat(tui): add Modal overlay component

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

### Task 5: Integrate `Modal` into `App`

**Files:**
- Modify: `internal/tui/app.go`

- [ ] **Step 1: Add `modal *Modal` field and update command dispatch**

In `internal/tui/app.go`, add `modal *Modal` to the `App` struct:

```go
// App is the root Bubble Tea model that composes all TUI components.
type App struct {
	topbar          TopBar
	chat            Chat
	sidebar         Sidebar
	input           Input
	modal           *Modal                    // non-nil when a modal overlay is active
	sendFn          func(string, []string)    // callback to send messages over network
	registry        *command.Registry         // slash command registry (nil = no commands)
	cmdCtx          command.Context           // context passed to slash commands
	nameColors      map[string]lipgloss.Color // stable color per participant
	colorIdx        int                       // next color index to assign
	lastInputHeight int                       // cached to avoid redundant re-layouts
	pendingHistory  []protocol.MessageParams  // set during room.state, loaded async
	width           int
	height          int
}
```

- [ ] **Step 2: Route modal keyboard input in `Update`**

In `Update`, inside `case tea.KeyMsg:`, add modal interception BEFORE the existing `switch m.Type` block:

```go
case tea.KeyMsg:
	// Modal intercepts all keyboard input while visible.
	if a.modal != nil {
		switch {
		case m.Type == tea.KeyEsc, m.String() == "q":
			a.modal = nil
			return a, nil
		default:
			cmd := a.modal.Update(msg)
			return a, cmd
		}
	}
	switch m.Type {
	case tea.KeyCtrlC:
		return a, tea.Quit

	case tea.KeyEnter:
		if a.input.mode == InputModeHuman {
			text := strings.TrimSpace(a.input.Value())
			if text != "" {
				a.input.Reset()
				// Slash command dispatch.
				if a.registry != nil && command.IsCommand(text) {
					result := a.registry.Execute(a.cmdCtx, text)
					if result.Error != nil {
						a.chat.AddMessage(systemMessage(result.Error.Error()))
					} else if result.Modal != nil {
						modal := NewModal(result.Modal, a.width, a.height)
						a.modal = &modal
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

- [ ] **Step 3: Resize modal on window resize**

In `Update`, inside `case tea.WindowSizeMsg:`, add modal resize after `a.layout()`:

```go
case tea.WindowSizeMsg:
	a.width = m.Width
	a.height = m.Height
	a.layout()
	if a.modal != nil {
		a.modal.Resize(m.Width, m.Height)
	}
```

- [ ] **Step 4: Render modal in `View`**

Replace `View()`:

```go
// View satisfies tea.Model. When a modal is active it renders full-screen;
// otherwise renders topbar, chat+sidebar, and input stacked vertically.
func (a App) View() string {
	if a.modal != nil {
		return a.modal.View()
	}
	middle := lipgloss.JoinHorizontal(
		lipgloss.Top,
		a.chat.View(),
		a.sidebar.View(),
	)
	return lipgloss.JoinVertical(
		lipgloss.Left,
		a.topbar.View(),
		middle,
		a.input.View(),
	)
}
```

- [ ] **Step 5: Verify compilation**

```bash
go build ./internal/tui/...
```

Expected: no errors.

- [ ] **Step 6: Run all tests**

```bash
go test ./... -timeout 30s
```

Expected: all PASS. (Visual golden tests will auto-create files if they don't exist yet — check output.)

- [ ] **Step 7: Commit**

```bash
git add internal/tui/app.go
git commit -m "feat(tui): integrate Modal overlay into App — /info opens modal, Esc/q closes

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

### Task 6: Add App-level modal integration tests

**Files:**
- Modify: `internal/tui/app_test.go`

- [ ] **Step 1: Write failing tests**

Add to `internal/tui/app_test.go`:

```go
// ---- Modal integration -------------------------------------------------------

func makeAppWithRegistry() App {
	app := makeApp()
	reg := command.NewRegistry()
	reg.Register(command.InfoCommand)
	ctx := command.Context{Room: &fakeRoom{}}
	app.SetCommandRegistry(reg, ctx)
	// Give the app a size so the modal can compute dimensions.
	model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return model.(App)
}

// fakeRoom is a minimal RoomQuerier for testing.
type fakeRoom struct{}

func (f *fakeRoom) GetID() string                           { return "room-1" }
func (f *fakeRoom) GetTopic() string                        { return "test-topic" }
func (f *fakeRoom) GetPort() int                            { return 9000 }
func (f *fakeRoom) GetParticipants() []command.ParticipantInfo { return nil }
func (f *fakeRoom) GetMessageCount() int                    { return 0 }

func TestApp_InfoCommand_ShowsModal(t *testing.T) {
	app := makeAppWithRegistry()

	// Type "/info" and press Enter.
	for _, ch := range "/info" {
		model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		app = model.(App)
	}
	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	app = model.(App)

	if app.modal == nil {
		t.Fatal("expected modal to be shown after /info command")
	}
	if len(app.chat.messages) != 0 {
		t.Errorf("expected chat history to be clean, got %d messages", len(app.chat.messages))
	}
}

func TestApp_ModalDismissedByEsc(t *testing.T) {
	app := makeAppWithRegistry()

	// Open modal via /info.
	for _, ch := range "/info" {
		model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		app = model.(App)
	}
	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	app = model.(App)
	if app.modal == nil {
		t.Fatal("precondition: modal must be open")
	}

	// Dismiss with Esc.
	model, _ = app.Update(tea.KeyMsg{Type: tea.KeyEsc})
	app = model.(App)
	if app.modal != nil {
		t.Fatal("expected modal to be dismissed after Esc")
	}
}

func TestApp_ModalDismissedByQ(t *testing.T) {
	app := makeAppWithRegistry()

	// Open modal via /info.
	for _, ch := range "/info" {
		model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		app = model.(App)
	}
	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	app = model.(App)
	if app.modal == nil {
		t.Fatal("precondition: modal must be open")
	}

	// Dismiss with q.
	model, _ = app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	app = model.(App)
	if app.modal != nil {
		t.Fatal("expected modal to be dismissed after q")
	}
}

func TestApp_ModalView_ShowsModalContent(t *testing.T) {
	app := makeAppWithRegistry()

	for _, ch := range "/info" {
		model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		app = model.(App)
	}
	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	app = model.(App)

	view := app.View()
	if !strings.Contains(view, "Room Info") {
		t.Errorf("expected modal view to contain 'Room Info', got:\n%s", view)
	}
}
```

Also add `"strings"` to the import block in `app_test.go` if not present, and add `tea "github.com/charmbracelet/bubbletea"` and `"github.com/khaiql/parley/internal/command"`.

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/tui/... -run "TestApp_.*Modal" -v
```

Expected: FAIL — `fakeRoom` undefined, `app.modal` unexported access issues don't apply (same package), compilation errors if imports missing.

- [ ] **Step 3: Fix imports in `app_test.go`**

Ensure the import block contains:

```go
import (
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/khaiql/parley/internal/command"
	"github.com/khaiql/parley/internal/protocol"
)
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/tui/... -run "TestApp_.*Modal" -v
```

Expected: all PASS.

- [ ] **Step 5: Run full test suite**

```bash
go test ./... -timeout 30s
```

Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/tui/app_test.go
git commit -m "test(tui): add integration tests for /info modal show and dismiss

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

### Task 7: Add visual regression test for modal view

**Files:**
- Modify: `internal/tui/visual_test.go`

- [ ] **Step 1: Add golden test for modal layout**

Add to `internal/tui/visual_test.go`:

```go
func TestVisualModal80x24(t *testing.T) {
	app := buildTestApp(t, 80, 24)

	// Simulate typing /info + Enter to open the modal.
	for _, ch := range "/info" {
		model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		app = model.(App)
	}
	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	app = model.(App)

	output := app.View()
	assertGolden(t, "modal_80x24", output)
}
```

Note: `buildTestApp` does not wire a command registry, so `/info` via Enter will be treated as a regular message (not a command). Update `buildTestApp` to optionally accept a registry, **OR** directly set the modal on the app for this test:

Instead, set the modal directly to avoid coupling `buildTestApp` to the command package:

```go
func TestVisualModal80x24(t *testing.T) {
	app := buildTestApp(t, 80, 24)

	// Directly inject a modal to test the visual layout.
	content := &command.ModalContent{
		Title: "Room Info",
		Body:  "Room: test-room\nTopic: test topic\nPort: 1234\nParticipants: 2\nMessages: 5\n\nParticipants:\n  • sle (human) [online]\n  • Alice (backend) [online] — /home/alice/project\n\nJoin command:\n  parley join --port 1234 -- claude\n",
	}
	modal := NewModal(content, 80, 24)
	app.modal = &modal

	output := app.View()
	assertGolden(t, "modal_80x24", output)
}
```

Add `"github.com/khaiql/parley/internal/command"` to the imports in `visual_test.go`.

- [ ] **Step 2: Run test to auto-generate golden file**

```bash
go test ./internal/tui/... -run TestVisualModal80x24 -v
```

Expected: PASS (golden file auto-created on first run). Check the output:

```bash
cat internal/tui/testdata/modal_80x24.golden
```

Verify the modal title, body content, and dismiss hint are visible in the output.

- [ ] **Step 3: Run test again to confirm golden match**

```bash
go test ./internal/tui/... -run TestVisualModal80x24 -v
```

Expected: PASS (matches the newly created golden file).

- [ ] **Step 4: Commit**

```bash
git add internal/tui/visual_test.go internal/tui/testdata/modal_80x24.golden
git commit -m "test(tui): add visual golden test for /info modal overlay

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

### Task 8: Final quality gate and PR

- [ ] **Step 1: Run full test suite**

```bash
go test ./... -timeout 30s
```

Expected: all PASS.

- [ ] **Step 2: Build binary**

```bash
go build -o parley ./cmd/parley
```

Expected: success, no warnings.

- [ ] **Step 3: Push branch**

```bash
git pull --rebase origin main
git push -u origin claude/objective-mcnulty
```

- [ ] **Step 4: Open PR**

```bash
gh pr create \
  --title "feat: show /info output in modal overlay instead of chat history" \
  --body "$(cat <<'EOF'
## Summary

Closes #70.

- Adds `ModalContent` struct to `command.Result` so commands can declare modal intent with title, body, and optional size hints — extensible without changing the Result API
- Updates `/info` to return `Modal: &ModalContent{Title: "Room Info", Body: info}` instead of `LocalMessage`
- Adds `Modal` Bubble Tea component (`internal/tui/modal.go`) with scrollable viewport, title, and dismiss footer; centered via `lipgloss.Place()`
- `App` intercepts all keyboard input while modal is active; Esc or q dismisses
- Chat history stays clean — `/info` output never touches the message list

## Test plan

- [ ] `go test ./... -timeout 30s` — all pass
- [ ] `go build -o parley ./cmd/parley` — builds cleanly
- [ ] Manual: run `parley host`, type `/info`, confirm modal appears; press Esc to dismiss; confirm chat history is unchanged

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```
