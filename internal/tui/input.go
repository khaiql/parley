package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// InputMode controls whether the input box accepts keyboard input or displays
// agent-driven output.
type InputMode int

const (
	// InputModeHuman allows the user to type freely.
	InputModeHuman InputMode = iota
	// InputModeAgent shows read-only agent output with a typing indicator.
	InputModeAgent
)

const (
	minInputLines = 2
	maxInputLines = 6
)

// Input is the bottom input component.
type Input struct {
	ta        textarea.Model
	mode      InputMode
	agentText string
	width     int
}

// NewInput creates an Input component in human mode.
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

// SetWidth updates the available width.
func (i *Input) SetWidth(w int) {
	i.width = w
	i.ta.SetWidth(w - 4 - 2) // border/padding (4) + prompt (2)
}

// SetMode switches between human and agent input modes.
func (i *Input) SetMode(m InputMode) {
	i.mode = m
	if m == InputModeHuman {
		i.ta.Focus()
	} else {
		i.ta.Blur()
	}
}

// SetAgentText updates the streaming text shown in agent mode.
func (i *Input) SetAgentText(text string) {
	i.agentText = text
}

// Value returns the current textarea content (human mode only).
func (i Input) Value() string {
	return i.ta.Value()
}

// Reset clears the textarea.
func (i *Input) Reset() {
	i.ta.Reset()
}

// Height returns the total height the input component needs (content lines + border).
func (i Input) Height() int {
	lines := minInputLines
	if i.mode == InputModeHuman {
		// Count newlines in the textarea value to determine needed lines.
		val := i.ta.Value()
		n := strings.Count(val, "\n") + 1
		if n > lines {
			lines = n
		}
		if lines > maxInputLines {
			lines = maxInputLines
		}
	}
	return lines + 1 // +1 for border-top
}

// Update passes tea events to the textarea (human mode only).
func (i *Input) Update(msg tea.Msg) tea.Cmd {
	if i.mode != InputModeHuman {
		return nil
	}
	var cmd tea.Cmd
	i.ta, cmd = i.ta.Update(msg)
	// Resize textarea to fit content.
	lines := strings.Count(i.ta.Value(), "\n") + 1
	if lines < minInputLines {
		lines = minInputLines
	}
	if lines > maxInputLines {
		lines = maxInputLines
	}
	i.ta.SetHeight(lines)
	return cmd
}

// contentWidth returns the usable text width inside the input box, accounting
// for the border and padding applied by inputStyle.
func (i Input) contentWidth() int {
	// inputStyle has Padding(0,1) on each side = 2 chars, no left/right border.
	w := i.width - 2
	if w < 1 {
		w = 1
	}
	return w
}

// View renders the input area.
func (i Input) View() string {
	var content string
	cw := i.contentWidth()
	switch i.mode {
	case InputModeAgent:
		if i.agentText != "" {
			// Wrap the streaming text to the content width, then show the last line
			wrapped := lipgloss.NewStyle().Width(cw).Render(i.agentText)
			lines := strings.Split(wrapped, "\n")
			lastLine := lines[len(lines)-1]
			content = lipgloss.NewStyle().Foreground(colorPrimary).Render(lastLine) +
				systemMsgStyle.Render(" ▊")
		} else {
			content = lipgloss.NewStyle().Foreground(colorDimText).Render("(waiting for messages…)")
		}
	default:
		prompt := lipgloss.NewStyle().Foreground(colorPrimary).Render("❯ ")
		content = prompt + i.ta.View()
	}
	return inputStyle.Width(i.width).Render(content)
}

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

// handleBackslashNewline checks if text ends with a backslash.
// If so, returns the text with the backslash replaced by a newline and true.
// Otherwise returns the original text and false.
func handleBackslashNewline(text string) (string, bool) {
	if len(text) > 0 && text[len(text)-1] == '\\' {
		return text[:len(text)-1] + "\n", true
	}
	return text, false
}
