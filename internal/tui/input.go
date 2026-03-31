package tui

import (
	"strings"

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
	ta.Placeholder = "Type a message… (Enter to send)"
	ta.ShowLineNumbers = false
	ta.SetHeight(1)
	ta.Focus()

	return Input{ta: ta, mode: InputModeHuman}
}

// SetWidth updates the available width.
func (i *Input) SetWidth(w int) {
	i.width = w
	i.ta.SetWidth(w - 4) // account for inputStyle padding + border
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

// Update passes tea events to the textarea (human mode only).
func (i *Input) Update(msg tea.Msg) tea.Cmd {
	if i.mode != InputModeHuman {
		return nil
	}
	var cmd tea.Cmd
	i.ta, cmd = i.ta.Update(msg)
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
			content = lipgloss.NewStyle().Foreground(colorAgent).Render(lastLine) +
				systemMsgStyle.Render(" ▊")
		} else {
			content = lipgloss.NewStyle().Foreground(colorDimText).Render("(waiting for messages…)")
		}
	default:
		content = i.ta.View()
	}
	return inputStyle.Width(i.width).Render(content)
}
