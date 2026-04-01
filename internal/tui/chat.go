package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/khaiql/parley/internal/protocol"
)

// Chat wraps a viewport and holds the message history.
type Chat struct {
	vp         viewport.Model
	messages   []protocol.MessageParams
	nameColors map[string]lipgloss.Color
	width      int
	height     int
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

// SetNameColors updates the per-participant color map and re-renders.
func (c *Chat) SetNameColors(colors map[string]lipgloss.Color) {
	c.nameColors = colors
	c.rebuildContent()
}

// rebuildContent re-renders all messages into the viewport.
func (c *Chat) rebuildContent() {
	var sb strings.Builder
	for i, msg := range c.messages {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(renderMessage(msg, c.width, c.nameColors))
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

// nameStyle returns a bold style using the participant's assigned color,
// falling back to colorAgent if no color is assigned.
func nameStyle(name string, colors map[string]lipgloss.Color) lipgloss.Style {
	c := colorAgent
	if colors != nil {
		if assigned, ok := colors[name]; ok {
			c = assigned
		}
	}
	return lipgloss.NewStyle().Bold(true).Foreground(c)
}

// borderColor returns the color for a participant's left border.
func borderColor(name string, colors map[string]lipgloss.Color) lipgloss.Color {
	if colors != nil {
		if c, ok := colors[name]; ok {
			return c
		}
	}
	return colorAgent
}

// renderMarkdown renders markdown text using Glamour, falling back to plain
// text if rendering fails.
func renderMarkdown(text string, width int) string {
	if width < 10 {
		width = 10
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return text
	}
	rendered, err := r.Render(text)
	if err != nil {
		return text
	}
	// Glamour adds trailing newlines; trim them.
	return strings.TrimRight(rendered, "\n")
}

// renderMessage renders a single message according to its source.
func renderMessage(msg protocol.MessageParams, width int, colors map[string]lipgloss.Color) string {
	text := extractText(msg.Content)

	if width < 1 {
		width = 1
	}

	switch msg.Source {
	case "system", "":
		if msg.Source == "system" || msg.Role == "system" {
			return systemMsgStyle.Width(width).Render(fmt.Sprintf("[system] %s", text))
		}
		return lipgloss.NewStyle().Foreground(colorText).Width(width).Render(text)

	default:
		// Human or agent message — render with thick left border.
		ts := formatTimestamp(msg)
		namePart := nameStyle(msg.From, colors).Render(msg.From)

		headerParts := []string{namePart}
		if ts != "" {
			headerParts = append(headerParts, " ", timestampStyle.Render(ts))
		}
		header := lipgloss.JoinHorizontal(lipgloss.Top, headerParts...)

		// Render body as markdown. Account for border (1 col) + padding (1 col).
		contentWidth := width - 2
		if contentWidth < 10 {
			contentWidth = 10
		}
		body := renderMarkdown(highlightMentions(text, colors), contentWidth)

		content := header + "\n" + body

		bc := borderColor(msg.From, colors)
		msgStyle := lipgloss.NewStyle().
			BorderLeft(true).
			BorderStyle(lipgloss.ThickBorder()).
			BorderForeground(bc).
			PaddingLeft(1)

		return msgStyle.Render(content)
	}
}

// highlightMentions renders @mentions in the text with a highlight style.
// It splits on whitespace boundaries, highlights tokens starting with "@",
// and reassembles the result.
func highlightMentions(text string, colors map[string]lipgloss.Color) string {
	mentionStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#58a6ff"))

	words := strings.Fields(text)
	for i, word := range words {
		if strings.HasPrefix(word, "@") && len(word) > 1 {
			// Strip trailing punctuation for color lookup.
			name := strings.TrimRight(word[1:], ".,;:!?")
			if colors != nil {
				if c, ok := colors[name]; ok {
					mentionStyle = lipgloss.NewStyle().Bold(true).Foreground(c)
				}
			}
			words[i] = mentionStyle.Render(word)
		}
	}
	return strings.Join(words, " ")
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
