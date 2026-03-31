package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
	"github.com/khaiql/parley/internal/protocol"
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
	var sb strings.Builder
	for i, msg := range c.messages {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(renderMessage(msg, c.width))
	}
	c.vp.SetContent(sb.String())
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

// renderMessage renders a single message according to its source.
func renderMessage(msg protocol.MessageParams, width int) string {
	text := extractText(msg.Content)

	// Leave at least 1 column for content; body is inset by 0 extra chars but
	// we cap at the viewport width so long text wraps instead of overflowing.
	bodyWidth := width
	if bodyWidth < 1 {
		bodyWidth = 1
	}
	bodyStyle := lipgloss.NewStyle().Foreground(colorText).Width(bodyWidth)

	switch msg.Source {
	case "system", "":
		if msg.Source == "" && msg.Role == "system" {
			return systemMsgStyle.Width(bodyWidth).Render(fmt.Sprintf("[system] %s", text))
		}
		if msg.Source == "system" {
			return systemMsgStyle.Width(bodyWidth).Render(fmt.Sprintf("[system] %s", text))
		}
		// Unknown — render as plain text.
		return bodyStyle.Render(text)

	case "human":
		ts := formatTimestamp(msg)
		header := lipgloss.JoinHorizontal(
			lipgloss.Top,
			humanNameStyle.Render(msg.From),
			" ",
			timestampStyle.Render(ts),
		)
		return header + "\n" + bodyStyle.Render(text)

	default:
		// agent
		ts := formatTimestamp(msg)
		namePart := agentNameStyle.Render(msg.From)
		rolePart := ""
		if msg.Role != "" && msg.Role != "agent" {
			rolePart = " " + roleBadgeStyle.Render(msg.Role)
		}
		header := lipgloss.JoinHorizontal(
			lipgloss.Top,
			namePart,
			rolePart,
			" ",
			timestampStyle.Render(ts),
		)
		return header + "\n" + bodyStyle.Render(text)
	}
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
