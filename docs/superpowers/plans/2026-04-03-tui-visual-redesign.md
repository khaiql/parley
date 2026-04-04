# TUI Visual Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Redesign the Parley TUI for clear sender differentiation, better contrast, markdown rendering, a status bar, and sidebar improvements.

**Architecture:** Update the existing Bubble Tea components in `internal/tui/`. The color system becomes per-sender (hash-based). Chat rendering gains Glamour markdown, left borders, and message grouping. A new status bar component is added below the input. The sidebar gets branding, color-matched names, and a spinner for generating agents.

**Tech Stack:** Go, Bubble Tea, Lipgloss, Glamour (new dependency)

---

## File Structure

| File | Responsibility |
|------|---------------|
| `internal/tui/styles.go` | Color palette, per-sender color function, all component styles |
| `internal/tui/chat.go` | Message rendering with borders, grouping, separators, timestamps, Glamour |
| `internal/tui/sidebar.go` | Branding, color-matched names, badges, spinner, separator lines |
| `internal/tui/statusbar.go` | New: status bar with segments (help, participants, room ID, connection) |
| `internal/tui/input.go` | Prompt indicator, backslash-newline support |
| `internal/tui/topbar.go` | Contrast fixes |
| `internal/tui/app.go` | Layout changes (status bar, sidebar width), spinner tick, wire new components |
| `go.mod` / `go.sum` | Add glamour dependency |

---

### Task 1: Color Palette & Per-Sender Color Assignment

**Files:**
- Modify: `internal/tui/styles.go`
- Test: `internal/tui/styles_test.go` (create)

- [ ] **Step 1: Write failing test for `ColorForSender`**

```go
// internal/tui/styles_test.go
package tui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestColorForSenderHumanAlwaysOrange(t *testing.T) {
	got := ColorForSender("sle", true)
	want := lipgloss.Color("#f0883e")
	if got != want {
		t.Errorf("human sender color = %v, want %v", got, want)
	}
}

func TestColorForSenderAgentDeterministic(t *testing.T) {
	c1 := ColorForSender("growth", false)
	c2 := ColorForSender("growth", false)
	if c1 != c2 {
		t.Errorf("same name should produce same color: %v vs %v", c1, c2)
	}
}

func TestColorForSenderDifferentAgentsDifferentColors(t *testing.T) {
	// With 8 colors and 3 names, collisions are unlikely but possible.
	// Test that at least 2 of 3 get different colors.
	c1 := ColorForSender("growth", false)
	c2 := ColorForSender("love", false)
	c3 := ColorForSender("engineer", false)
	unique := map[lipgloss.Color]bool{c1: true, c2: true, c3: true}
	if len(unique) < 2 {
		t.Errorf("expected at least 2 unique colors for 3 agents, got %d", len(unique))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run TestColorForSender -v`
Expected: FAIL — `ColorForSender` not defined

- [ ] **Step 3: Implement color palette and `ColorForSender`**

Replace the color variables section in `internal/tui/styles.go`:

```go
package tui

import (
	"hash/fnv"

	"github.com/charmbracelet/lipgloss"
)

var (
	colorPrimary    = lipgloss.Color("#58a6ff")
	colorHuman      = lipgloss.Color("#f0883e")
	colorSystem     = lipgloss.Color("#8b949e")
	colorText       = lipgloss.Color("#e1e4e8")
	colorDimText    = lipgloss.Color("#6e7681")
	colorBorder     = lipgloss.Color("#3b3f47")
	colorSeparator  = lipgloss.Color("#21262d")
	colorSidebarBg  = lipgloss.Color("#161b22")
	colorConnected  = lipgloss.Color("#3fb950")
	colorStatusBarBg = lipgloss.Color("#30363d")
)

// agentPalette holds 8 visually distinct colors for agent senders.
var agentPalette = []lipgloss.Color{
	"#a78bfa", // Violet
	"#7dd3fc", // Sky Blue
	"#34d399", // Emerald
	"#fbbf24", // Amber
	"#f472b6", // Pink
	"#60a5fa", // Blue
	"#a3e635", // Lime
	"#fb923c", // Tangerine
}

// ColorForSender returns a deterministic color for a sender name.
// Human senders always get orange. Agent senders get a color from the
// 8-color palette based on an FNV hash of their name.
func ColorForSender(name string, isHuman bool) lipgloss.Color {
	if isHuman {
		return colorHuman
	}
	h := fnv.New32a()
	h.Write([]byte(name))
	idx := int(h.Sum32()) % len(agentPalette)
	return agentPalette[idx]
}
```

Also remove the old `colorAgent`, `colorRoleBadge`, `colorDimText` (old value) variables and update all styles that reference them. The `agentNameStyle` and `roleBadgeStyle` become functions that take a color:

```go
var (
	topBarStyle = lipgloss.NewStyle().
		Background(colorSidebarBg).
		Foreground(colorText).
		Padding(0, 1).
		Bold(true)

	sidebarStyle = lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).
		BorderLeft(true).
		BorderForeground(colorBorder).
		Foreground(colorText).
		Background(colorSidebarBg).
		Padding(0, 1)

	sidebarTitleStyle = lipgloss.NewStyle().
		Foreground(colorDimText).
		MarginBottom(1)

	sidebarBrandStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(colorPrimary).
		Align(lipgloss.Center)

	sidebarPortStyle = lipgloss.NewStyle().
		Foreground(colorDimText).
		Align(lipgloss.Center)

	inputStyle = lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).
		BorderTop(true).
		BorderForeground(colorBorder).
		Padding(0, 1)

	humanNameStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(colorHuman)

	systemMsgStyle = lipgloss.NewStyle().
		Foreground(colorSystem).
		Italic(true)

	timestampStyle = lipgloss.NewStyle().
		Foreground(colorDimText)

	separatorStyle = lipgloss.NewStyle().
		Foreground(colorSeparator)

	participantStatusStyle = lipgloss.NewStyle().
		Foreground(colorSystem).
		Italic(true)
)

// agentNameStyleFor returns a bold style in the given sender color.
func agentNameStyleFor(c lipgloss.Color) lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).Foreground(c)
}

// agentBadgeStyleFor returns a badge style with the agent's color on dim bg.
func agentBadgeStyleFor(c lipgloss.Color) lipgloss.Style {
	return lipgloss.NewStyle().
		Background(colorStatusBarBg).
		Foreground(c).
		Padding(0, 1)
}
```

Remove the old `listeningStatusStyle`, `agentNameStyle`, `roleBadgeStyle` variables — they are replaced by functions above.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tui/ -run TestColorForSender -v`
Expected: PASS

- [ ] **Step 5: Fix compilation of other files**

After removing `agentNameStyle`, `roleBadgeStyle`, `listeningStatusStyle`, and `colorAgent`, the sidebar.go, chat.go, and input.go files will fail to compile. Update them to compile with temporary stubs:

In `chat.go`, replace `agentNameStyle.Render(msg.From)` with `agentNameStyleFor(ColorForSender(msg.From, false)).Render(msg.From)` and `roleBadgeStyle.Render(msg.Role)` with `agentBadgeStyleFor(ColorForSender(msg.From, false)).Render(msg.Role)`.

In `sidebar.go`, replace `agentNameStyle.Render(p.Name)` with `agentNameStyleFor(ColorForSender(p.Name, false)).Render(p.Name)`, replace `roleBadgeStyle.Render(p.Role)` with `agentBadgeStyleFor(ColorForSender(p.Name, false)).Render(p.Role)`, and replace `listeningStatusStyle.Render(...)` with `participantStatusStyle.Render(...)`.

In `input.go`, replace `colorAgent` with `colorPrimary` (for the agent streaming text color).

- [ ] **Step 6: Run all tests to verify nothing is broken**

Run: `go test ./internal/tui/ -v`
Expected: Some golden file tests will fail (expected — visuals changed). All non-golden tests PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/tui/styles.go internal/tui/styles_test.go internal/tui/chat.go internal/tui/sidebar.go internal/tui/input.go
git commit -m "feat(tui): per-sender color palette with deterministic assignment"
```

---

### Task 2: Message Borders, Grouping & Separators

**Files:**
- Modify: `internal/tui/chat.go`
- Modify: `internal/tui/chat_test.go`

- [ ] **Step 1: Write failing tests for new rendering behavior**

Add to `internal/tui/chat_test.go`:

```go
func TestRenderMessagesGroupsConsecutiveSender(t *testing.T) {
	msgs := []protocol.MessageParams{
		{
			From: "growth", Source: "agent", Role: "agent",
			Content:   []protocol.Content{{Type: "text", Text: "first message"}},
			Timestamp: time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
		},
		{
			From: "growth", Source: "agent", Role: "agent",
			Content:   []protocol.Content{{Type: "text", Text: "second message"}},
			Timestamp: time.Date(2024, 1, 1, 12, 0, 30, 0, time.UTC),
		},
	}
	rendered := renderMessages(msgs, 80)
	// Name should appear only once for grouped messages
	count := countOccurrences(stripANSI(rendered), "growth")
	if count != 1 {
		t.Errorf("expected sender name 'growth' once in grouped messages, got %d", count)
	}
	if !contains(rendered, "first message") || !contains(rendered, "second message") {
		t.Error("expected both message texts in output")
	}
}

func TestRenderMessagesSeparatorBetweenSenders(t *testing.T) {
	msgs := []protocol.MessageParams{
		{
			From: "growth", Source: "agent", Role: "agent",
			Content:   []protocol.Content{{Type: "text", Text: "hello"}},
			Timestamp: time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
		},
		{
			From: "love", Source: "agent", Role: "agent",
			Content:   []protocol.Content{{Type: "text", Text: "hi"}},
			Timestamp: time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
		},
	}
	rendered := renderMessages(msgs, 80)
	// Should contain a horizontal rule/separator between the two senders
	plain := stripANSI(rendered)
	if !containsHorizontalRule(plain) {
		t.Error("expected separator line between different senders")
	}
}

func TestRenderMessageSystemCentered(t *testing.T) {
	msgs := []protocol.MessageParams{
		{
			From: "server", Source: "system", Role: "system",
			Content: []protocol.Content{{Type: "text", Text: "sle joined"}},
		},
	}
	rendered := renderMessages(msgs, 80)
	plain := stripANSI(rendered)
	if !strings.Contains(plain, "— sle joined —") {
		t.Errorf("system message should be formatted as '— text —', got: %q", plain)
	}
}

func TestRenderMessagesTimestampAfterGap(t *testing.T) {
	msgs := []protocol.MessageParams{
		{
			From: "growth", Source: "agent", Role: "agent",
			Content:   []protocol.Content{{Type: "text", Text: "first"}},
			Timestamp: time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
		},
		{
			From: "growth", Source: "agent", Role: "agent",
			Content:   []protocol.Content{{Type: "text", Text: "second after gap"}},
			Timestamp: time.Date(2024, 1, 1, 12, 6, 0, 0, time.UTC),
		},
	}
	rendered := renderMessages(msgs, 80)
	plain := stripANSI(rendered)
	// Both timestamps should appear because 6min > 5min threshold
	if !strings.Contains(plain, "12:00") {
		t.Error("expected first timestamp 12:00")
	}
	if !strings.Contains(plain, "12:06") {
		t.Error("expected second timestamp 12:06 due to 5min gap rule")
	}
}

// Helper: count occurrences of substr in s
func countOccurrences(s, substr string) int {
	count := 0
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			count++
		}
	}
	return count
}

// Helper: check for a horizontal rule (line of dashes or ─ chars)
func containsHorizontalRule(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) >= 3 {
			allRule := true
			for _, r := range trimmed {
				if r != '─' && r != '-' {
					allRule = false
					break
				}
			}
			if allRule {
				return true
			}
		}
	}
	return false
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tui/ -run "TestRenderMessages|TestRenderMessageSystem" -v`
Expected: FAIL — `renderMessages` not defined

- [ ] **Step 3: Implement `renderMessages` and update `rebuildContent`**

Replace `chat.go` rendering logic. The key changes:

1. New function `renderMessages(msgs []protocol.MessageParams, width int) string` that handles grouping, borders, separators, and timestamps
2. `rebuildContent` calls `renderMessages` instead of rendering one-by-one
3. System messages render as centered `— text —`
4. Each message gets a thick left border in sender's color via `lipgloss.ThickBorder()` with `BorderLeft(true)`
5. Consecutive same-sender messages share a border block — only the first shows name/timestamp
6. A thin separator line (`strings.Repeat("─", width)` in `colorSeparator`) appears between different senders
7. Timestamp shown on first-in-group and when 5+ minutes elapsed since last shown timestamp

```go
package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/khaiql/parley/internal/protocol"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/bubbles/viewport"
)

// Chat wraps a viewport and holds the message history.
type Chat struct {
	vp       viewport.Model
	messages []protocol.MessageParams
	width    int
	height   int
}

// NewChat creates a Chat component with the given dimensions.
func NewChat(width, height int) Chat {
	vp := viewport.New(width, height)
	return Chat{vp: vp, width: width, height: height}
}

// SetSize resizes the chat viewport.
func (c *Chat) SetSize(width, height int) {
	c.width = width
	c.height = height
	c.vp.Width = width
	c.vp.Height = height
	c.rebuildContent()
}

// AddMessage appends a message and refreshes the viewport content.
func (c *Chat) AddMessage(msg protocol.MessageParams) {
	c.messages = append(c.messages, msg)
	c.rebuildContent()
	c.vp.GotoBottom()
}

// rebuildContent re-renders all messages into the viewport.
func (c *Chat) rebuildContent() {
	c.vp.SetContent(renderMessages(c.messages, c.width))
}

// Update passes tea.Msg events to the underlying viewport (for scrolling).
func (c *Chat) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	c.vp, cmd = c.vp.Update(msg)
	return cmd
}

// View renders the chat area.
func (c Chat) View() string {
	return c.vp.View()
}

const timestampGap = 5 * time.Minute

// renderMessages renders all messages with grouping, borders, and separators.
func renderMessages(msgs []protocol.MessageParams, width int) string {
	if len(msgs) == 0 {
		return ""
	}

	// Reserve space for the thick left border (3 chars: ┃ + space)
	bodyWidth := width - 3
	if bodyWidth < 1 {
		bodyWidth = 1
	}

	var sections []string
	var lastSender string
	var lastTimestamp time.Time

	for i, msg := range msgs {
		isSystem := msg.Source == "system" || (msg.Source == "" && msg.Role == "system")

		if isSystem {
			text := extractText(msg.Content)
			centered := systemMsgStyle.Width(width).Align(lipgloss.Center).Render("— " + text + " —")
			if lastSender != "" {
				sections = append(sections, separatorLine(width))
			}
			sections = append(sections, centered)
			lastSender = ""
			continue
		}

		isHuman := msg.Source == "human"
		senderColor := ColorForSender(msg.From, isHuman)
		sameSender := msg.From == lastSender && i > 0
		text := extractText(msg.Content)

		// Determine if we should show a timestamp
		showTimestamp := false
		if !sameSender {
			showTimestamp = true
		} else if !msg.Timestamp.IsZero() && !lastTimestamp.IsZero() &&
			msg.Timestamp.Sub(lastTimestamp) >= timestampGap {
			showTimestamp = true
		}

		// Build the content for inside the border
		var lines []string

		if !sameSender {
			// Add separator before new sender (unless first message)
			if len(sections) > 0 {
				sections = append(sections, separatorLine(width))
			}

			// Header: name [badge] timestamp
			var headerParts []string
			if isHuman {
				headerParts = append(headerParts, humanNameStyle.Render(msg.From))
			} else {
				headerParts = append(headerParts, agentNameStyleFor(senderColor).Render(msg.From))
			}
			if msg.Role != "" && msg.Role != "agent" && msg.Role != "human" {
				headerParts = append(headerParts, " ", agentBadgeStyleFor(senderColor).Render(msg.Role))
			}
			if showTimestamp {
				ts := formatTimestamp(msg)
				if ts != "" {
					headerParts = append(headerParts, " ", timestampStyle.Render(ts))
				}
			}
			lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top, headerParts...))
		} else if showTimestamp {
			// Same sender but time gap — show timestamp line
			ts := formatTimestamp(msg)
			if ts != "" {
				lines = append(lines, timestampStyle.Render(ts))
			}
		}

		// Body text
		bodyStyle := lipgloss.NewStyle().Foreground(colorText).Width(bodyWidth)
		lines = append(lines, bodyStyle.Render(text))

		// Apply thick left border in sender color
		block := strings.Join(lines, "\n")
		bordered := lipgloss.NewStyle().
			BorderStyle(lipgloss.ThickBorder()).
			BorderLeft(true).
			BorderForeground(senderColor).
			PaddingLeft(1).
			Render(block)

		sections = append(sections, bordered)

		lastSender = msg.From
		if !msg.Timestamp.IsZero() {
			lastTimestamp = msg.Timestamp
		}
	}

	return strings.Join(sections, "\n")
}

// separatorLine returns a thin horizontal rule.
func separatorLine(width int) string {
	if width < 1 {
		width = 1
	}
	return separatorStyle.Render(strings.Repeat("─", width))
}

// extractText concatenates all text-type content blocks.
func extractText(content []protocol.Content) string {
	var parts []string
	for _, c := range content {
		if c.Type == "text" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "")
}

// formatTimestamp returns a short HH:MM timestamp string.
func formatTimestamp(msg protocol.MessageParams) string {
	if msg.Timestamp.IsZero() {
		return ""
	}
	return msg.Timestamp.Format("15:04")
}
```

- [ ] **Step 4: Update existing tests**

The old `renderMessage` (singular) function is removed. Update existing tests that call it:

- `TestRenderMessageContainsText` → call `renderMessages([]protocol.MessageParams{msg}, 80)` instead
- `TestRenderMessageAgentContainsRoleBadge` → same change
- `TestRenderMessageSystemFormat` → update to check for `— alice has joined —` instead of `[system]`
- `TestRenderMessageWrapsLongText` → use `renderMessages` with single-element slice

- [ ] **Step 5: Run all tests**

Run: `go test ./internal/tui/ -v`
Expected: All non-golden tests PASS. Golden tests expected to fail (visual changes).

- [ ] **Step 6: Commit**

```bash
git add internal/tui/chat.go internal/tui/chat_test.go
git commit -m "feat(tui): message borders, grouping, separators, and timestamp logic"
```

---

### Task 3: Glamour Markdown Rendering

**Files:**
- Modify: `go.mod`
- Modify: `internal/tui/chat.go`
- Modify: `internal/tui/chat_test.go`

- [ ] **Step 1: Add glamour dependency**

Run: `go get github.com/charmbracelet/glamour`

- [ ] **Step 2: Write failing test for markdown rendering**

Add to `internal/tui/chat_test.go`:

```go
func TestRenderMessagesMarkdownBold(t *testing.T) {
	msgs := []protocol.MessageParams{
		{
			From: "bot", Source: "agent", Role: "agent",
			Content:   []protocol.Content{{Type: "text", Text: "This is **bold** text"}},
			Timestamp: time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
		},
	}
	rendered := renderMessages(msgs, 80)
	// Glamour renders **bold** with ANSI bold escape codes.
	// The word "bold" should still be present, and the ** markers should be gone.
	plain := stripANSI(rendered)
	if strings.Contains(plain, "**bold**") {
		t.Error("markdown ** markers should be rendered, not shown literally")
	}
	if !strings.Contains(plain, "bold") {
		t.Error("expected the word 'bold' in rendered output")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/tui/ -run TestRenderMessagesMarkdownBold -v`
Expected: FAIL — `**bold**` appears literally

- [ ] **Step 4: Integrate Glamour into `renderMessages`**

Add a package-level Glamour renderer and use it for message body rendering. In `chat.go`:

```go
import (
	"github.com/charmbracelet/glamour"
)

// glamourRenderer creates a Glamour renderer for the given width.
// We recreate it when width changes rather than caching, since width
// changes are infrequent (only on terminal resize).
func renderMarkdown(text string, width int) string {
	if width < 10 {
		width = 10
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		// Fallback to plain text
		return lipgloss.NewStyle().Foreground(colorText).Width(width).Render(text)
	}
	rendered, err := r.Render(text)
	if err != nil {
		return lipgloss.NewStyle().Foreground(colorText).Width(width).Render(text)
	}
	// Glamour adds trailing newlines — trim them
	return strings.TrimRight(rendered, "\n")
}
```

Then in `renderMessages`, replace the body rendering line:

```go
// OLD:
bodyStyle := lipgloss.NewStyle().Foreground(colorText).Width(bodyWidth)
lines = append(lines, bodyStyle.Render(text))

// NEW:
lines = append(lines, renderMarkdown(text, bodyWidth))
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/tui/ -run TestRenderMessagesMarkdownBold -v`
Expected: PASS

- [ ] **Step 6: Run all tests**

Run: `go test ./internal/tui/ -v`
Expected: All non-golden tests PASS.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum internal/tui/chat.go internal/tui/chat_test.go
git commit -m "feat(tui): glamour markdown rendering for chat messages"
```

---

### Task 4: Sidebar Redesign

**Files:**
- Modify: `internal/tui/sidebar.go`
- Modify: `internal/tui/sidebar_test.go`
- Modify: `internal/tui/app.go` (sidebar width constant)

- [ ] **Step 1: Write failing tests for new sidebar features**

Add to `internal/tui/sidebar_test.go`:

```go
func TestSidebarViewShowsBranding(t *testing.T) {
	s := NewSidebar()
	s.SetPort(55568)
	s.SetSize(30, 20)
	s.AddParticipant(protocol.Participant{Name: "sle", Role: "human", Source: "human"})

	view := s.View()
	if !contains(view, "parley") {
		t.Error("sidebar should contain app name 'parley'")
	}
	if !contains(view, ":55568") {
		t.Error("sidebar should contain port ':55568'")
	}
}

func TestSidebarViewColorMatchedNames(t *testing.T) {
	s := NewSidebar()
	s.SetSize(30, 20)
	s.AddParticipant(protocol.Participant{Name: "growth", Role: "agent", Source: "agent", AgentType: "gemini"})

	view := s.View()
	if !contains(view, "growth") {
		t.Error("sidebar should contain agent name 'growth'")
	}
	if !contains(view, "gemini") {
		t.Error("sidebar should contain agent type badge 'gemini'")
	}
}

func TestSidebarViewGeneratingSpinner(t *testing.T) {
	s := NewSidebar()
	s.SetSize(30, 20)
	s.AddParticipant(protocol.Participant{Name: "bot1", Role: "agent", Source: "agent"})
	s.SetParticipantStatus("bot1", "generating")

	view := s.View()
	if !contains(view, "generating") {
		t.Errorf("sidebar should show 'generating' status, got: %q", view)
	}
}

func TestSidebarViewSectionHeader(t *testing.T) {
	s := NewSidebar()
	s.SetSize(30, 20)
	s.AddParticipant(protocol.Participant{Name: "sle", Role: "human", Source: "human"})

	view := s.View()
	plain := stripANSI(view)
	if !strings.Contains(strings.ToUpper(plain), "PARTICIPANTS") {
		t.Error("sidebar should contain uppercase 'PARTICIPANTS' section header")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tui/ -run "TestSidebarView(ShowsBranding|ColorMatched|Generating|SectionHeader)" -v`
Expected: FAIL — `SetPort` not defined, new assertions fail

- [ ] **Step 3: Implement sidebar redesign**

Rewrite `internal/tui/sidebar.go`:

```go
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/khaiql/parley/internal/protocol"
)

// spinnerFrames are braille characters for the generating animation.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Sidebar renders the participant list panel.
type Sidebar struct {
	participants []protocol.Participant
	statuses     map[string]string
	port         int
	spinnerFrame int
	width        int
	height       int
}

// NewSidebar creates an empty Sidebar.
func NewSidebar() Sidebar {
	return Sidebar{statuses: make(map[string]string)}
}

// SetSize updates the sidebar dimensions.
func (s *Sidebar) SetSize(width, height int) {
	s.width = width
	s.height = height
}

// SetPort sets the port displayed in the branding section.
func (s *Sidebar) SetPort(port int) {
	s.port = port
}

// SetParticipants replaces the full participant list.
func (s *Sidebar) SetParticipants(participants []protocol.Participant) {
	s.participants = participants
}

// AddParticipant appends a participant, replacing any existing entry with the
// same name.
func (s *Sidebar) AddParticipant(p protocol.Participant) {
	for i, existing := range s.participants {
		if existing.Name == p.Name {
			s.participants[i] = p
			return
		}
	}
	s.participants = append(s.participants, p)
}

// SetParticipantStatus updates the activity status for a named participant.
func (s *Sidebar) SetParticipantStatus(name, status string) {
	if s.statuses == nil {
		s.statuses = make(map[string]string)
	}
	s.statuses[name] = status
}

// RemoveParticipant removes a participant by name.
func (s *Sidebar) RemoveParticipant(name string) {
	filtered := s.participants[:0]
	for _, p := range s.participants {
		if p.Name != name {
			filtered = append(filtered, p)
		}
	}
	s.participants = filtered
}

// TickSpinner advances the spinner animation frame. Returns true if the
// sidebar has any generating participants (caller should keep ticking).
func (s *Sidebar) TickSpinner() bool {
	s.spinnerFrame = (s.spinnerFrame + 1) % len(spinnerFrames)
	hasGenerating := false
	for _, status := range s.statuses {
		if status == "generating" {
			hasGenerating = true
			break
		}
	}
	return hasGenerating
}

// View renders the sidebar as a string.
func (s Sidebar) View() string {
	innerWidth := s.width - 4
	if innerWidth < 1 {
		innerWidth = 1
	}

	var lines []string

	// Branding section
	brand := sidebarBrandStyle.Width(innerWidth).Render("parley")
	lines = append(lines, brand)
	if s.port > 0 {
		portLine := sidebarPortStyle.Width(innerWidth).Render(fmt.Sprintf(":%d", s.port))
		lines = append(lines, portLine)
	}
	lines = append(lines, separatorStyle.Render(strings.Repeat("─", innerWidth)))

	// Section header
	header := sidebarTitleStyle.Render("PARTICIPANTS")
	lines = append(lines, header)

	// Participants
	for i, p := range s.participants {
		if i > 0 {
			lines = append(lines, separatorStyle.Render(strings.Repeat("─", innerWidth)))
		}

		isHuman := p.Source == "human" || p.Role == "human"
		senderColor := ColorForSender(p.Name, isHuman)

		// Name line with optional badge
		var nameLine string
		if isHuman {
			nameLine = humanNameStyle.Render(p.Name)
		} else {
			nameLine = agentNameStyleFor(senderColor).Render(p.Name)
		}
		if p.AgentType != "" {
			badge := agentBadgeStyleFor(senderColor).Render(p.AgentType)
			nameLine = lipgloss.JoinHorizontal(lipgloss.Top, nameLine, " ", badge)
		}
		lines = append(lines, nameLine)

		// Status line (only "generating" is shown)
		if status := s.statuses[p.Name]; status == "generating" {
			frame := spinnerFrames[s.spinnerFrame]
			statusLine := agentNameStyleFor(senderColor).Render("  " + frame + " generating")
			lines = append(lines, statusLine)
		} else if status != "" && status != "listening" {
			lines = append(lines, participantStatusStyle.Render("  "+status))
		}

		// Directory
		if p.Directory != "" {
			dir := p.Directory
			maxLen := innerWidth - 2
			if maxLen > 4 && len(dir) > maxLen {
				dir = "…" + dir[len(dir)-(maxLen-1):]
			}
			lines = append(lines, timestampStyle.Render("  "+dir))
		}
	}

	content := strings.Join(lines, "\n")
	return sidebarStyle.Width(s.width).Height(s.height).Render(content)
}
```

- [ ] **Step 4: Update sidebar width constant in `app.go`**

Change `const sidebarWidth = 28` to `const sidebarWidth = 30` in `app.go`.

Also pass the port to the sidebar in `NewApp`:

```go
func NewApp(topic string, port int, mode InputMode, name string, sendFn func(string, []string), participants ...protocol.Participant) App {
	sb := NewSidebar()
	sb.SetPort(port)
	a := App{
		topbar:  NewTopBar(topic, port),
		chat:    NewChat(0, 0),
		sidebar: sb,
		input:   NewInput(),
		sendFn:  sendFn,
	}
	// ...
}
```

- [ ] **Step 5: Run all tests**

Run: `go test ./internal/tui/ -v`
Expected: All non-golden tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/tui/sidebar.go internal/tui/sidebar_test.go internal/tui/app.go
git commit -m "feat(tui): sidebar redesign with branding, color-matched names, spinner"
```

---

### Task 5: Status Bar Component

**Files:**
- Create: `internal/tui/statusbar.go`
- Create: `internal/tui/statusbar_test.go`
- Modify: `internal/tui/app.go`

- [ ] **Step 1: Write failing test**

```go
// internal/tui/statusbar_test.go
package tui

import (
	"strings"
	"testing"
)

func TestStatusBarShowsParticipantCount(t *testing.T) {
	sb := NewStatusBar()
	sb.SetParticipantCount(4)
	sb.SetWidth(80)

	view := sb.View()
	if !contains(view, "4 participants") {
		t.Errorf("status bar should show '4 participants', got: %q", stripANSI(view))
	}
}

func TestStatusBarShowsRoomID(t *testing.T) {
	sb := NewStatusBar()
	sb.SetRoomID("3a7f1234-5678-abcd-efgh-ijklmnopqrst")
	sb.SetWidth(80)

	view := sb.View()
	plain := stripANSI(view)
	if !strings.Contains(plain, "3a7f…") {
		t.Errorf("status bar should show truncated room ID '3a7f…', got: %q", plain)
	}
}

func TestStatusBarShowsConnected(t *testing.T) {
	sb := NewStatusBar()
	sb.SetConnected(true)
	sb.SetWidth(80)

	view := sb.View()
	if !contains(view, "connected") {
		t.Errorf("status bar should show 'connected', got: %q", stripANSI(view))
	}
}

func TestStatusBarShowsDisconnected(t *testing.T) {
	sb := NewStatusBar()
	sb.SetConnected(false)
	sb.SetWidth(80)

	view := sb.View()
	if !contains(view, "disconnected") {
		t.Errorf("status bar should show 'disconnected', got: %q", stripANSI(view))
	}
}

func TestStatusBarShowsHelp(t *testing.T) {
	sb := NewStatusBar()
	sb.SetWidth(80)

	view := sb.View()
	if !contains(view, "? help") {
		t.Errorf("status bar should show '? help', got: %q", stripANSI(view))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tui/ -run TestStatusBar -v`
Expected: FAIL — `NewStatusBar` not defined

- [ ] **Step 3: Implement status bar**

```go
// internal/tui/statusbar.go
package tui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

// StatusBar renders the bottom status line.
type StatusBar struct {
	participantCount int
	roomID           string
	connected        bool
	width            int
}

// NewStatusBar creates a StatusBar with default values.
func NewStatusBar() StatusBar {
	return StatusBar{connected: true}
}

// SetWidth updates the available width.
func (s *StatusBar) SetWidth(w int) { s.width = w }

// SetParticipantCount updates the displayed participant count.
func (s *StatusBar) SetParticipantCount(n int) { s.participantCount = n }

// SetRoomID sets the room ID (will be truncated to first 4 chars in display).
func (s *StatusBar) SetRoomID(id string) { s.roomID = id }

// SetConnected sets the connection status indicator.
func (s *StatusBar) SetConnected(c bool) { s.connected = c }

// View renders the status bar.
func (s StatusBar) View() string {
	helpStyle := lipgloss.NewStyle().
		Background(colorStatusBarBg).
		Foreground(colorText).
		Bold(true).
		Padding(0, 1)
	segmentStyle := lipgloss.NewStyle().
		Foreground(colorDimText).
		Padding(0, 1)

	// Left segments
	help := helpStyle.Render("? help")
	participants := segmentStyle.Render(fmt.Sprintf("%d participants", s.participantCount))

	roomDisplay := ""
	if s.roomID != "" {
		short := s.roomID
		if len(short) > 4 {
			short = short[:4] + "…"
		}
		roomDisplay = segmentStyle.Render("room: " + short)
	}

	left := help + participants + roomDisplay

	// Right segment: connection status
	var connStatus string
	if s.connected {
		connStyle := lipgloss.NewStyle().Foreground(colorConnected).Padding(0, 1)
		connStatus = connStyle.Render("● connected")
	} else {
		connStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#f85149")).Padding(0, 1)
		connStatus = connStyle.Render("● disconnected")
	}

	// Fill gap between left and right
	leftWidth := lipgloss.Width(left)
	rightWidth := lipgloss.Width(connStatus)
	gap := s.width - leftWidth - rightWidth
	if gap < 0 {
		gap = 0
	}
	filler := lipgloss.NewStyle().Width(gap).Render("")

	barStyle := lipgloss.NewStyle().
		Background(colorSidebarBg).
		Width(s.width)

	return barStyle.Render(left + filler + connStatus)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/tui/ -run TestStatusBar -v`
Expected: PASS

- [ ] **Step 5: Wire status bar into `app.go`**

Add `statusbar StatusBar` field to `App` struct. Initialize in `NewApp`. Update `layout()` to account for status bar height (1 line). Update `View()` to include status bar below input. Update `handleServerMsg` to update participant count and room ID.

In `App` struct:
```go
type App struct {
	topbar    TopBar
	chat      Chat
	sidebar   Sidebar
	input     Input
	statusbar StatusBar
	// ...
}
```

In `NewApp`:
```go
a.statusbar = NewStatusBar()
```

In `layout()`:
```go
topbarHeight := 1
inputHeight := 2
statusbarHeight := 1
chatHeight := a.height - topbarHeight - inputHeight - statusbarHeight
// ...
a.statusbar.SetWidth(a.width)
```

In `View()`:
```go
return lipgloss.JoinVertical(
	lipgloss.Left,
	a.topbar.View(),
	middle,
	a.input.View(),
	a.statusbar.View(),
)
```

In `handleServerMsg` for `"room.state"`:
```go
a.statusbar.SetParticipantCount(len(params.Participants))
if params.RoomID != "" {
	a.statusbar.SetRoomID(params.RoomID)
}
```

In `handleServerMsg` for `"room.joined"`: increment count. For `"room.left"`: decrement count.

- [ ] **Step 6: Run all tests**

Run: `go test ./internal/tui/ -v`
Expected: All non-golden tests PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/tui/statusbar.go internal/tui/statusbar_test.go internal/tui/app.go
git commit -m "feat(tui): add status bar with help, participant count, room ID, connection status"
```

---

### Task 6: Input Area Improvements

**Files:**
- Modify: `internal/tui/input.go`
- Modify: `internal/tui/input_test.go`
- Modify: `internal/tui/app.go`

- [ ] **Step 1: Write failing tests**

Add to `internal/tui/input_test.go`:

```go
func TestInputHumanMode_PromptIndicator(t *testing.T) {
	inp := NewInput()
	inp.SetMode(InputModeHuman)
	inp.SetWidth(80)

	view := inp.View()
	if !strings.Contains(view, "❯") {
		t.Error("human mode input should show ❯ prompt indicator")
	}
}

func TestInputBackslashNewline(t *testing.T) {
	// When text ends with \, handleBackslashNewline should return true
	// and the text should have the \ replaced with \n
	text := `hello world\`
	result, consumed := handleBackslashNewline(text)
	if !consumed {
		t.Error("expected backslash-newline to be consumed")
	}
	if result != "hello world\n" {
		t.Errorf("expected trailing \\ replaced with newline, got: %q", result)
	}
}

func TestInputBackslashNewline_NoTrailingBackslash(t *testing.T) {
	text := "hello world"
	_, consumed := handleBackslashNewline(text)
	if consumed {
		t.Error("should not consume when no trailing backslash")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tui/ -run "TestInput(HumanMode_Prompt|BackslashNewline)" -v`
Expected: FAIL

- [ ] **Step 3: Implement prompt indicator and backslash-newline**

In `input.go`, add the `handleBackslashNewline` function:

```go
// handleBackslashNewline checks if text ends with a backslash. If so,
// returns the text with the backslash replaced by a newline and true.
// Otherwise returns the original text and false.
func handleBackslashNewline(text string) (string, bool) {
	if len(text) > 0 && text[len(text)-1] == '\\' {
		return text[:len(text)-1] + "\n", true
	}
	return text, false
}
```

Update `View()` to add prompt indicator in human mode:

```go
case InputModeHuman:
	prompt := lipgloss.NewStyle().Foreground(colorPrimary).Render("❯ ")
	content = prompt + i.ta.View()
```

Adjust textarea width in `SetWidth` to account for the 2-char prompt:
```go
func (i *Input) SetWidth(w int) {
	i.width = w
	i.ta.SetWidth(w - 4 - 2) // border/padding + prompt
}
```

- [ ] **Step 4: Wire backslash-newline into `app.go`**

In `app.go`, update the `tea.KeyEnter` handler:

```go
case tea.KeyEnter:
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
			mentions := protocol.ParseMentions(text)
			if a.sendFn != nil {
				a.sendFn(text, mentions)
			}
		}
		return a, nil
	}
```

- [ ] **Step 5: Run all tests**

Run: `go test ./internal/tui/ -v`
Expected: All non-golden tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/tui/input.go internal/tui/input_test.go internal/tui/app.go
git commit -m "feat(tui): prompt indicator and backslash-newline for input"
```

---

### Task 7: Spinner Tick Wiring & Top Bar Contrast

**Files:**
- Modify: `internal/tui/app.go`
- Modify: `internal/tui/topbar.go`

- [ ] **Step 1: Add spinner tick message and wiring in `app.go`**

```go
import "time"

// SpinnerTickMsg triggers a sidebar spinner frame advance.
type SpinnerTickMsg struct{}

// spinnerTick returns a tea.Cmd that sends a SpinnerTickMsg after 100ms.
func spinnerTick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg {
		return SpinnerTickMsg{}
	})
}
```

In `Update`, add handler:

```go
case SpinnerTickMsg:
	if a.sidebar.TickSpinner() {
		cmds = append(cmds, spinnerTick())
	}
	return a, tea.Batch(cmds...)
```

In `handleServerMsg` for `"room.status"`, start ticker when status is "generating":

```go
case "room.status":
	var params protocol.StatusParams
	if err := json.Unmarshal(raw.Params, &params); err == nil {
		a.sidebar.SetParticipantStatus(params.Name, params.Status)
		// Start spinner tick if any participant is generating
		// (TickSpinner is called in Update to check if we should keep ticking)
	}
```

Actually, the tick should be started from Update. Add a flag `spinnerActive bool` to App. When a status becomes "generating", set it and return `spinnerTick()`. When `TickSpinner()` returns false, clear the flag.

- [ ] **Step 2: Update topbar contrast**

In `topbar.go`, the styles already use `colorText` which is now `#e1e4e8` (updated in Task 1). No code changes needed beyond what Task 1 already did. Verify the topbar still renders correctly.

- [ ] **Step 3: Run all tests**

Run: `go test ./internal/tui/ -v`
Expected: All non-golden tests PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/tui/app.go internal/tui/topbar.go
git commit -m "feat(tui): spinner tick wiring and topbar contrast update"
```

---

### Task 8: Regenerate Golden Files & Final Verification

**Files:**
- Modify: `internal/tui/visual_test.go`
- Update: `internal/tui/testdata/*.golden`

- [ ] **Step 1: Update `buildTestApp` for new sidebar API**

The `buildTestApp` in `visual_test.go` needs to call `sidebar.SetPort(1234)` and the status bar needs participant count set:

```go
func buildTestApp(t *testing.T, width, height int) App {
	t.Helper()

	app := NewApp("test topic", 1234, InputModeHuman, "sle", nil)

	app.sidebar.AddParticipant(protocol.Participant{
		Name:   "sle",
		Role:   "human",
		Source: "human",
	})
	app.sidebar.AddParticipant(protocol.Participant{
		Name:      "Alice",
		Role:      "backend",
		Directory: "/home/alice/project",
		AgentType: "claude",
		Source:    "agent",
	})

	app.statusbar.SetParticipantCount(2)

	ts := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)

	app.chat.AddMessage(protocol.MessageParams{
		ID: "msg-1", Seq: 1,
		From: "system", Source: "system", Role: "system",
		Timestamp: ts,
		Content:   []protocol.Content{{Type: "text", Text: "Alice has joined — backend"}},
	})

	app.chat.AddMessage(protocol.MessageParams{
		ID: "msg-2", Seq: 2,
		From: "sle", Source: "human", Role: "human",
		Timestamp: ts.Add(time.Minute),
		Content:   []protocol.Content{{Type: "text", Text: "Hello everyone"}},
	})

	model, _ := app.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return model.(App)
}
```

- [ ] **Step 2: Regenerate golden files**

Temporarily set `const updateGolden = true` in `visual_test.go`, then run:

Run: `go test ./internal/tui/ -run TestVisual -v`

Then set `const updateGolden = false` back.

- [ ] **Step 3: Review golden files**

Run: `cat internal/tui/testdata/layout_80x24.golden` and visually verify:
- Messages have thick left borders
- System message is centered with `— text —` format
- Sidebar shows branding, uppercase PARTICIPANTS header, color-matched names
- Status bar at bottom with `? help`, participant count, connection status
- Input has `❯` prompt

- [ ] **Step 4: Run full test suite**

Run: `go test ./... -timeout 30s`
Expected: ALL PASS

- [ ] **Step 5: Build binary**

Run: `go build -o parley ./cmd/parley`
Expected: Builds successfully

- [ ] **Step 6: Commit golden files**

```bash
git add internal/tui/visual_test.go internal/tui/testdata/
git commit -m "test(tui): regenerate golden files for visual redesign"
```

---

## Task Dependency Graph

```
Task 1 (colors) ──┬──> Task 2 (messages) ──> Task 3 (glamour)
                   │
                   ├──> Task 4 (sidebar)
                   │
                   └──> Task 6 (input)

Task 5 (status bar) ── independent, needs Task 1 styles

Task 4 + Task 5 ──> Task 7 (spinner wiring)

All tasks ──> Task 8 (golden files)
```

Tasks 2, 4, 5, and 6 can run in parallel after Task 1 completes. Task 3 depends on Task 2. Task 7 depends on Tasks 4 and 5. Task 8 is always last.

<function_calls>
<invoke name="Bash">
<parameter name="command">cd /Users/sle/group_chat/.claude/worktrees/glowing-riding-cupcake && git add docs/superpowers/plans/2026-04-03-tui-visual-redesign.md && git commit -m "$(cat <<'EOF'
docs: add TUI visual redesign implementation plan

8-task plan covering color palette, message borders/grouping, glamour
markdown, sidebar redesign, status bar, input improvements, spinner
wiring, and golden file regeneration.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>
EOF
)"