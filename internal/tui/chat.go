package tui

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/khaiql/parley/internal/protocol"
)

const timestampGap = 5 * time.Minute

// Chat wraps a viewport and holds the message history.
type Chat struct {
	vp       viewport.Model
	messages []protocol.MessageParams
	colors   map[string]int // sender name → server-assigned color index
	loading  bool
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
	// Scroll to bottom so resumed history shows the latest messages.
	if len(c.messages) > 0 {
		c.vp.GotoBottom()
	}
}

// AddMessage appends a message and refreshes the viewport content.
func (c *Chat) AddMessage(msg protocol.MessageParams) {
	c.messages = append(c.messages, msg)
	c.rebuildContent()
	c.vp.GotoBottom()
}

// LoadMessages bulk-loads messages without re-rendering after each one.
func (c *Chat) LoadMessages(msgs []protocol.MessageParams) {
	c.messages = append(c.messages, msgs...)
	c.rebuildContent()
	c.vp.GotoBottom()
}

// SetColors replaces the sender→colorIndex map and rebuilds the viewport.
func (c *Chat) SetColors(m map[string]int) {
	c.colors = m
	c.rebuildContent()
}

// AddColor adds or updates a single sender's color index and rebuilds the viewport.
func (c *Chat) AddColor(name string, colorIndex int) {
	if c.colors == nil {
		c.colors = make(map[string]int)
	}
	c.colors[name] = colorIndex
	c.rebuildContent()
}

// rebuildContent re-renders all messages into the viewport.
func (c *Chat) rebuildContent() {
	c.vp.SetContent(renderMessages(c.messages, c.width, c.colors))
}

// Update passes tea.Msg events to the underlying viewport (for scrolling).
func (c *Chat) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	c.vp, cmd = c.vp.Update(msg)
	return cmd
}

// SetLoading shows or hides the loading indicator.
func (c *Chat) SetLoading(loading bool) {
	c.loading = loading
	if loading {
		msg := lipgloss.NewStyle().Foreground(colorDimText).Render("Loading history…")
		c.vp.SetContent(msg)
	}
}

// View renders the chat area.
func (c Chat) View() string {
	return c.vp.View()
}

// isSystemMessage returns true if the message should be rendered as a system message.
func isSystemMessage(msg protocol.MessageParams) bool {
	return msg.IsSystem()
}

// collapseSystemMessages merges consecutive runs of system messages into a
// single summary message. For example, 10 "sle joined"/"sle left" messages
// become "10 system events".
func collapseSystemMessages(msgs []protocol.MessageParams) []protocol.MessageParams {
	var result []protocol.MessageParams
	i := 0
	for i < len(msgs) {
		if !isSystemMessage(msgs[i]) {
			result = append(result, msgs[i])
			i++
			continue
		}
		// Count consecutive system messages.
		j := i + 1
		for j < len(msgs) && isSystemMessage(msgs[j]) {
			j++
		}
		count := j - i
		if count <= 2 {
			// Few enough to show individually.
			for k := i; k < j; k++ {
				result = append(result, msgs[k])
			}
		} else {
			// Collapse into a summary that shows what happened.
			summary := summarizeSystemRun(msgs[i:j])
			result = append(result, protocol.MessageParams{
				Source: "system",
				Role:   "system",
				Content: []protocol.Content{
					{Type: "text", Text: summary},
				},
			})
		}
		i = j
	}
	return result
}

// summarizeSystemRun creates a human-readable summary of a run of system messages.
// E.g. "sle joined/left ×12, bob joined ×1" or "13 join/leave events".
func summarizeSystemRun(msgs []protocol.MessageParams) string {
	counts := make(map[string]int)
	for _, m := range msgs {
		text := extractText(m.Content)
		counts[text]++
	}
	if len(counts) <= 3 {
		var parts []string
		for text, n := range counts {
			if n > 1 {
				parts = append(parts, fmt.Sprintf("%s (×%d)", text, n))
			} else {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, ", ")
	}
	return fmt.Sprintf("%d system events", len(msgs))
}

// resolveAgentColor returns the color for an agent sender. It uses the
// server-assigned index from the colors map when available, falling back to
// the FNV hash for senders not in the map (e.g. historical messages from
// disconnected participants).
func resolveAgentColor(name string, colors map[string]int) lipgloss.Color {
	if colors != nil {
		if idx, ok := colors[name]; ok {
			return ColorForIndex(idx)
		}
	}
	return ColorForSender(name, false)
}

// renderMessages renders a slice of messages with grouping, borders, and separators.
func renderMessages(msgs []protocol.MessageParams, width int, colors map[string]int) string {
	if len(msgs) == 0 {
		return ""
	}

	// Pre-process: collapse consecutive system messages.
	collapsed := collapseSystemMessages(msgs)

	var parts []string
	var lastSender string
	var lastTimestamp time.Time

	for i, msg := range collapsed {
		if isSystemMessage(msg) {
			text := extractText(msg.Content)
			rendered := systemMsgStyle.Width(width).Align(lipgloss.Center).Render(fmt.Sprintf("— %s —", text))
			if i > 0 && lastSender != "" {
				parts = append(parts, separatorStyle.Render(strings.Repeat("─", width)))
			}
			parts = append(parts, rendered)
			lastSender = ""
			lastTimestamp = time.Time{}
			continue
		}

		text := extractText(msg.Content)
		isHuman := msg.IsHuman()
		var senderColor lipgloss.Color
		if isHuman {
			senderColor = colorHuman
		} else {
			senderColor = resolveAgentColor(msg.From, colors)
		}
		sameSender := msg.From == lastSender && lastSender != ""

		// Determine whether to show timestamp.
		showTimestamp := false
		if !sameSender {
			showTimestamp = true
		} else if !msg.Timestamp.IsZero() && !lastTimestamp.IsZero() && msg.Timestamp.Sub(lastTimestamp) >= timestampGap {
			showTimestamp = true
		}

		// Separator between different senders.
		if i > 0 && !sameSender {
			parts = append(parts, separatorStyle.Render(strings.Repeat("─", width)))
		}

		// Body width accounts for thick left border (3 chars: border + padding).
		bodyWidth := width - 3
		if bodyWidth < 1 {
			bodyWidth = 1
		}
		body := highlightMentions(renderMarkdown(text, bodyWidth), colors)
		var content string
		if !sameSender {
			// First message in group: show header + blank line + body.
			header := renderHeader(msg, isHuman, senderColor)
			content = header + "\n\n" + body
		} else if showTimestamp {
			// Same sender but timestamp gap: show header again.
			header := renderHeader(msg, isHuman, senderColor)
			content = header + "\n\n" + body
		} else {
			// Continuation: just the body.
			content = body
		}

		// Apply thick left border in sender's color.
		bordered := lipgloss.NewStyle().
			BorderStyle(lipgloss.ThickBorder()).
			BorderLeft(true).
			BorderTop(false).
			BorderRight(false).
			BorderBottom(false).
			BorderForeground(senderColor).
			PaddingLeft(1).
			Render(content)

		parts = append(parts, bordered)
		lastSender = msg.From
		if showTimestamp && !msg.Timestamp.IsZero() {
			lastTimestamp = msg.Timestamp
		} else if !sameSender && !msg.Timestamp.IsZero() {
			lastTimestamp = msg.Timestamp
		}
	}

	return strings.Join(parts, "\n")
}

// renderHeader builds the name + optional badge + timestamp header line.
func renderHeader(msg protocol.MessageParams, isHuman bool, senderColor lipgloss.Color) string {
	ts := formatTimestamp(msg)

	if isHuman {
		return lipgloss.JoinHorizontal(
			lipgloss.Top,
			humanNameStyle.Render(msg.From),
			" ",
			timestampStyle.Render(ts),
		)
	}

	// Agent header.
	namePart := agentNameStyleFor(senderColor).Render(msg.From)
	return lipgloss.JoinHorizontal(
		lipgloss.Top,
		namePart,
		" ",
		timestampStyle.Render(ts),
	)
}

// highlightMentions renders @mentions in the text with sender colors.
// Handles Glamour-rendered text where ANSI codes may be interleaved with the @mention.
var ansiSeq = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func highlightMentions(text string, colors map[string]int) string {
	// Strip ANSI to find mention positions in plain text.
	plain := ansiSeq.ReplaceAllString(text, "")
	mentionRe := regexp.MustCompile(`@(\w+)`)
	mentions := mentionRe.FindAllStringIndex(plain, -1)
	if len(mentions) == 0 {
		return text
	}

	// Build a mapping from plain-text index to original-text index.
	plainIdx := 0
	origIdx := 0
	plainToOrig := make([]int, len(plain)+1)
	for origIdx < len(text) && plainIdx <= len(plain) {
		if text[origIdx] == '\x1b' {
			// Skip entire ANSI sequence.
			loc := ansiSeq.FindStringIndex(text[origIdx:])
			if loc != nil && loc[0] == 0 {
				origIdx += loc[1]
				continue
			}
		}
		plainToOrig[plainIdx] = origIdx
		plainIdx++
		origIdx++
	}
	plainToOrig[plainIdx] = origIdx

	// Replace mentions from end to start so indices stay valid.
	result := text
	for i := len(mentions) - 1; i >= 0; i-- {
		pStart := mentions[i][0]
		pEnd := mentions[i][1]
		name := plain[pStart+1 : pEnd] // strip @
		oStart := plainToOrig[pStart]
		oEnd := plainToOrig[pEnd]

		c := resolveAgentColor(name, colors)
		styled := lipgloss.NewStyle().Bold(true).Foreground(c).Render("@" + name)
		result = result[:oStart] + styled + result[oEnd:]
	}
	return result
}

// trimLeadingANSISpaces strips up to 2 leading spaces from a string that may
// have ANSI escape codes interleaved with spaces (e.g. "\x1b[0m  text").
// Glamour adds a 2-space paragraph margin; we remove that margin but preserve
// any additional indentation (e.g. code block indentation of 4+ spaces).
func trimLeadingANSISpaces(s string) string {
	i := 0
	spacesStripped := 0
	const maxStripSpaces = 2
	for i < len(s) {
		if s[i] == ' ' && spacesStripped < maxStripSpaces {
			i++
			spacesStripped++
		} else if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			// Skip the entire ANSI escape sequence (don't count as space).
			j := i + 2
			for j < len(s) && !((s[j] >= 'A' && s[j] <= 'Z') || (s[j] >= 'a' && s[j] <= 'z')) {
				j++
			}
			if j < len(s) {
				j++ // skip the final letter
			}
			i = j
		} else {
			break
		}
	}
	return s[i:]
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

// mdRenderer is a cached Glamour renderer to avoid recreating it per message
// and to prevent OSC terminal queries from glamour.WithAutoStyle().
var mdRenderer *glamour.TermRenderer
var mdRendererWidth int

func getMarkdownRenderer(width int) *glamour.TermRenderer {
	if mdRenderer != nil && mdRendererWidth == width {
		return mdRenderer
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStylePath("dark"),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil
	}
	mdRenderer = r
	mdRendererWidth = width
	return r
}

// renderMarkdown renders text as markdown using Glamour.
func renderMarkdown(text string, width int) string {
	if width < 10 {
		width = 10
	}
	r := getMarkdownRenderer(width)
	if r == nil {
		return text
	}
	rendered, err := r.Render(text)
	if err != nil {
		return text
	}
	// Glamour adds leading/trailing whitespace — trim it so body aligns
	// with the header inside the border. Spaces may be interleaved with
	// ANSI escape sequences, so we strip leading ANSI+space sequences.
	lines := strings.Split(strings.Trim(rendered, "\n"), "\n")
	for i, line := range lines {
		lines[i] = trimLeadingANSISpaces(line)
	}
	return strings.Join(lines, "\n")
}
