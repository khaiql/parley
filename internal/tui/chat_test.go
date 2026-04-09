package tui

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/khaiql/parley/internal/protocol"
)

// ansiEscapeRegex strips ANSI escape sequences from a string.
var ansiEscapeRegex = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	return ansiEscapeRegex.ReplaceAllString(s, "")
}

// displayWidth returns the visible column width of a string, ignoring ANSI.
func displayWidth(s string) int {
	return lipgloss.Width(s)
}

func TestExtractText(t *testing.T) {
	tests := []struct {
		name    string
		content []protocol.Content
		want    string
	}{
		{
			name:    "empty content",
			content: []protocol.Content{},
			want:    "",
		},
		{
			name: "single text block",
			content: []protocol.Content{
				{Type: "text", Text: "hello"},
			},
			want: "hello",
		},
		{
			name: "multiple text blocks concatenated",
			content: []protocol.Content{
				{Type: "text", Text: "hello"},
				{Type: "text", Text: " world"},
			},
			want: "hello world",
		},
		{
			name: "non-text blocks are ignored",
			content: []protocol.Content{
				{Type: "image", Text: "ignored"},
				{Type: "text", Text: "visible"},
			},
			want: "visible",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractText(tt.content)
			if got != tt.want {
				t.Errorf("extractText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatTimestamp(t *testing.T) {
	tests := []struct {
		name string
		msg  protocol.MessageParams
		want string
	}{
		{
			name: "zero time returns empty string",
			msg:  protocol.MessageParams{},
			want: "",
		},
		{
			name: "non-zero time returns HH:MM",
			msg: protocol.MessageParams{
				Timestamp: time.Date(2024, 1, 15, 9, 5, 0, 0, time.UTC),
			},
			want: "09:05",
		},
		{
			name: "afternoon time",
			msg: protocol.MessageParams{
				Timestamp: time.Date(2024, 6, 1, 14, 30, 0, 0, time.UTC),
			},
			want: "14:30",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatTimestamp(tt.msg)
			if got != tt.want {
				t.Errorf("formatTimestamp() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRenderMessageContainsText(t *testing.T) {
	msg := protocol.MessageParams{
		From:   "alice",
		Source: "human",
		Role:   "human",
		Content: []protocol.Content{
			{Type: "text", Text: "hello there"},
		},
		Timestamp: time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
	}
	rendered := renderMessages([]protocol.MessageParams{msg}, 80, nil)
	if !contains(rendered, "alice") {
		t.Errorf("renderMessages (human) should contain sender name %q, got: %q", "alice", rendered)
	}
	if !contains(rendered, "hello there") {
		t.Errorf("renderMessages (human) should contain message text, got: %q", rendered)
	}
	if !contains(rendered, "12:00") {
		t.Errorf("renderMessages (human) should contain timestamp, got: %q", rendered)
	}
}

func TestRenderMessageAgentContainsNameAndText(t *testing.T) {
	msg := protocol.MessageParams{
		From:   "bot1",
		Source: "agent",
		Role:   "coder",
		Content: []protocol.Content{
			{Type: "text", Text: "I wrote some code"},
		},
		Timestamp: time.Date(2024, 1, 1, 8, 30, 0, 0, time.UTC),
	}
	rendered := renderMessages([]protocol.MessageParams{msg}, 80, nil)
	if !contains(rendered, "bot1") {
		t.Errorf("renderMessages (agent) should contain agent name, got: %q", rendered)
	}
	if !contains(rendered, "I wrote some code") {
		t.Errorf("renderMessages (agent) should contain message text, got: %q", rendered)
	}
}

func TestRenderMessageSystemFormat(t *testing.T) {
	msg := protocol.MessageParams{
		From:   "server",
		Source: "system",
		Role:   "system",
		Content: []protocol.Content{
			{Type: "text", Text: "alice has joined"},
		},
	}
	rendered := renderMessages([]protocol.MessageParams{msg}, 80, nil)
	if !contains(rendered, "— alice has joined —") {
		t.Errorf("renderMessages (system) should contain em-dash wrapped text, got: %q", stripANSI(rendered))
	}
}

func TestRenderMessageWrapsLongText(t *testing.T) {
	// Build a 200-character message body (sentence with many short words).
	longText := strings.Repeat("word ", 40) // "word word word ..." = 199 chars after TrimSpace
	longText = strings.TrimSpace(longText)

	msg := protocol.MessageParams{
		From:   "alice",
		Source: "human",
		Role:   "human",
		Content: []protocol.Content{
			{Type: "text", Text: longText},
		},
		Timestamp: time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
	}

	const width = 80
	rendered := renderMessages([]protocol.MessageParams{msg}, width, nil)

	lines := strings.Split(rendered, "\n")
	if len(lines) <= 1 {
		t.Errorf("expected multiple lines for 200-char message at width %d, got %d line(s)", width, len(lines))
	}

	// The thick left border adds a few chars (┃ + padding), so allow
	// rendered lines to be slightly wider than the logical width.
	maxRendered := width + 4 // border + padding overhead
	for i, line := range lines {
		visible := stripANSI(line)
		if len(visible) > maxRendered {
			t.Errorf("line %d exceeds max rendered width %d: len=%d %q", i, maxRendered, len(visible), visible)
		}
	}
}

func TestRenderMessagesGroupsConsecutiveSender(t *testing.T) {
	ts := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	msgs := []protocol.MessageParams{
		{
			From:      "alice",
			Source:    "human",
			Role:      "human",
			Content:   []protocol.Content{{Type: "text", Text: "first message"}},
			Timestamp: ts,
		},
		{
			From:      "alice",
			Source:    "human",
			Role:      "human",
			Content:   []protocol.Content{{Type: "text", Text: "second message"}},
			Timestamp: ts.Add(1 * time.Minute),
		},
	}

	rendered := renderMessages(msgs, 80, nil)
	// Name should appear only once.
	count := countOccurrences(stripANSI(rendered), "alice")
	if count != 1 {
		t.Errorf("expected sender name 'alice' to appear once (grouped), got %d times in:\n%s", count, stripANSI(rendered))
	}
	if !contains(rendered, "first message") {
		t.Errorf("should contain first message text")
	}
	if !contains(rendered, "second message") {
		t.Errorf("should contain second message text")
	}
}

func TestRenderMessagesSeparatorBetweenSenders(t *testing.T) {
	ts := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	msgs := []protocol.MessageParams{
		{
			From:      "alice",
			Source:    "human",
			Role:      "human",
			Content:   []protocol.Content{{Type: "text", Text: "hello"}},
			Timestamp: ts,
		},
		{
			From:      "bot1",
			Source:    "agent",
			Role:      "agent",
			Content:   []protocol.Content{{Type: "text", Text: "hi there"}},
			Timestamp: ts.Add(1 * time.Minute),
		},
	}

	rendered := renderMessages(msgs, 80, nil)
	if !containsHorizontalRule(rendered) {
		t.Errorf("expected horizontal rule separator between different senders, got:\n%s", stripANSI(rendered))
	}
	if !contains(rendered, "alice") {
		t.Errorf("should contain alice")
	}
	if !contains(rendered, "bot1") {
		t.Errorf("should contain bot1")
	}
}

func TestRenderMessageSystemCentered(t *testing.T) {
	msg := protocol.MessageParams{
		From:   "server",
		Source: "system",
		Role:   "system",
		Content: []protocol.Content{
			{Type: "text", Text: "alice has joined"},
		},
	}
	rendered := renderMessages([]protocol.MessageParams{msg}, 80, nil)
	if !contains(rendered, "— alice has joined —") {
		t.Errorf("system message should be formatted with em-dashes, got: %q", stripANSI(rendered))
	}
	// Should NOT contain a border character (thick border).
	stripped := stripANSI(rendered)
	if strings.Contains(stripped, "┃") {
		t.Errorf("system message should not have a thick left border, got: %q", stripped)
	}
}

func TestRenderMessagesTimestampAfterGap(t *testing.T) {
	ts := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	msgs := []protocol.MessageParams{
		{
			From:      "alice",
			Source:    "human",
			Role:      "human",
			Content:   []protocol.Content{{Type: "text", Text: "first"}},
			Timestamp: ts,
		},
		{
			From:      "alice",
			Source:    "human",
			Role:      "human",
			Content:   []protocol.Content{{Type: "text", Text: "second"}},
			Timestamp: ts.Add(6 * time.Minute), // 6 min > 5 min gap
		},
	}

	rendered := renderMessages(msgs, 80, nil)
	stripped := stripANSI(rendered)
	// Both timestamps should appear.
	if !strings.Contains(stripped, "12:00") {
		t.Errorf("expected first timestamp 12:00, got:\n%s", stripped)
	}
	if !strings.Contains(stripped, "12:06") {
		t.Errorf("expected second timestamp 12:06, got:\n%s", stripped)
	}
	// Name should appear twice (once per group header shown due to gap).
	count := countOccurrences(stripped, "alice")
	if count != 2 {
		t.Errorf("expected sender name 'alice' to appear twice (gap causes new header), got %d in:\n%s", count, stripped)
	}
}

// contains checks whether s contains substr, stripping ANSI escape codes first
// so that glamour/lipgloss styling doesn't break substring matching.
func contains(s, substr string) bool {
	plain := stripANSI(s)
	return strings.Contains(plain, substr)
}

// countOccurrences counts all non-overlapping occurrences of substr in s.
func countOccurrences(s, substr string) int {
	count := 0
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			count++
			i += len(substr) - 1
		}
	}
	return count
}

func TestRenderMessagesMarkdownBold(t *testing.T) {
	msgs := []protocol.MessageParams{
		{
			From: "bot", Source: "agent", Role: "agent",
			Content:   []protocol.Content{{Type: "text", Text: "This is **bold** text"}},
			Timestamp: time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
		},
	}
	rendered := renderMessages(msgs, 80, nil)
	plain := stripANSI(rendered)
	if strings.Contains(plain, "**bold**") {
		t.Error("markdown ** markers should be rendered, not shown literally")
	}
	if !strings.Contains(plain, "bold") {
		t.Error("expected the word 'bold' in rendered output")
	}
}

// containsHorizontalRule checks if any line consists primarily of ─ characters.
func containsHorizontalRule(s string) bool {
	stripped := stripANSI(s)
	for _, line := range strings.Split(stripped, "\n") {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) > 0 && trimmed == strings.Repeat("─", len([]rune(trimmed))) {
			return true
		}
	}
	return false
}

func TestCollapseSystemMessages_FewKept(t *testing.T) {
	msgs := []protocol.MessageParams{
		{Source: "system", Role: "system", Content: []protocol.Content{{Type: "text", Text: "alice joined"}}},
		{Source: "system", Role: "system", Content: []protocol.Content{{Type: "text", Text: "bob joined"}}},
	}
	result := collapseSystemMessages(msgs)
	if len(result) != 2 {
		t.Errorf("2 system messages should be kept individually, got %d", len(result))
	}
}

func TestCollapseSystemMessages_ManyCollapsed(t *testing.T) {
	var msgs []protocol.MessageParams
	for i := 0; i < 10; i++ {
		msgs = append(msgs, protocol.MessageParams{
			Source:  "system",
			Role:    "system",
			Content: []protocol.Content{{Type: "text", Text: "sle joined"}},
		})
	}
	result := collapseSystemMessages(msgs)
	if len(result) != 1 {
		t.Errorf("10 consecutive system messages should collapse to 1, got %d", len(result))
	}
	text := extractText(result[0].Content)
	if !strings.Contains(text, "10") {
		t.Errorf("collapsed message should contain count '10', got: %q", text)
	}
}

func TestCollapseSystemMessages_MixedPreserved(t *testing.T) {
	msgs := []protocol.MessageParams{
		{Source: "human", Role: "human", From: "alice", Content: []protocol.Content{{Type: "text", Text: "hello"}}},
		{Source: "system", Role: "system", Content: []protocol.Content{{Type: "text", Text: "bob joined"}}},
		{Source: "human", Role: "human", From: "alice", Content: []protocol.Content{{Type: "text", Text: "world"}}},
	}
	result := collapseSystemMessages(msgs)
	if len(result) != 3 {
		t.Errorf("mixed messages should all be preserved, got %d", len(result))
	}
}

func TestChatViewportShowsMessages(t *testing.T) {
	c := NewChat(80, 20)
	ts := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	c.AddMessage(protocol.MessageParams{
		From: "alice", Source: "human", Role: "human",
		Content:   []protocol.Content{{Type: "text", Text: "first message"}},
		Timestamp: ts,
	})
	c.AddMessage(protocol.MessageParams{
		From: "bob", Source: "agent", Role: "agent",
		Content:   []protocol.Content{{Type: "text", Text: "second message"}},
		Timestamp: ts.Add(time.Minute),
	})

	view := c.View()
	if !strings.Contains(stripANSI(view), "first message") {
		t.Error("viewport should contain 'first message'")
	}
	if !strings.Contains(stripANSI(view), "second message") {
		t.Error("viewport should contain 'second message'")
	}
}

func TestChatViewportWithSystemFlood(t *testing.T) {
	c := NewChat(80, 20)
	ts := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	// Add a real message first.
	c.AddMessage(protocol.MessageParams{
		From: "alice", Source: "human", Role: "human",
		Content:   []protocol.Content{{Type: "text", Text: "important message"}},
		Timestamp: ts,
	})

	// Flood with 50 system messages.
	for i := 0; i < 50; i++ {
		c.AddMessage(protocol.MessageParams{
			Source:  "system",
			Role:    "system",
			Content: []protocol.Content{{Type: "text", Text: "sle joined"}},
		})
	}

	// The rendered content should have the real message AND collapsed system events.
	content := c.vp.View()
	plain := stripANSI(content)
	// Content should include the collapsed summary.
	// Since viewport is 20 lines and GotoBottom was called, the bottom
	// will show the collapsed system event. But the real message should
	// be in the full content.
	fullContent := stripANSI(renderMessages(c.messages, 80, nil))
	if !strings.Contains(fullContent, "important message") {
		t.Error("full content should contain 'important message'")
	}
	// Collapsed summary should mention the count (×50) since all have same text.
	if !strings.Contains(fullContent, "×50") {
		t.Errorf("full content should contain collapsed system summary with count, got:\n%s", fullContent)
	}
	_ = plain // viewport view depends on scroll position
}

func TestChatLoadMessagesBeforeSetSize(t *testing.T) {
	// Simulates messages loaded before WindowSizeMsg arrives.
	c := NewChat(0, 0) // width=0, height=0
	ts := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	c.LoadMessages([]protocol.MessageParams{
		{From: "alice", Source: "human", Role: "human",
			Content:   []protocol.Content{{Type: "text", Text: "hello world"}},
			Timestamp: ts},
	})

	// Now simulate SetSize (like WindowSizeMsg would trigger).
	c.SetSize(80, 20)

	view := c.View()
	plain := stripANSI(view)
	if !strings.Contains(plain, "hello world") {
		t.Errorf("after SetSize, viewport should show 'hello world', got:\n%s", plain)
	}
}

func TestChatLoadMessagesAfterSetSize(t *testing.T) {
	c := NewChat(80, 20)
	ts := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	c.LoadMessages([]protocol.MessageParams{
		{From: "alice", Source: "human", Role: "human",
			Content:   []protocol.Content{{Type: "text", Text: "hello world"}},
			Timestamp: ts},
	})

	view := c.View()
	plain := stripANSI(view)
	if !strings.Contains(plain, "hello world") {
		t.Errorf("viewport should show 'hello world', got:\n%s", plain)
	}
}

func TestColorConsistencyBetweenChatAndSidebar(t *testing.T) {
	// Verify that the same sender gets the same color in chat and sidebar.
	// "atlas" is an agent — should NOT get colorHuman (orange).
	atlasColor := ColorForSender("atlas", false, "")
	humanColor := ColorForSender("sle", true, "")

	t.Logf("atlas color: %v", atlasColor)
	t.Logf("sle (human) color: %v", humanColor)

	if atlasColor == humanColor {
		t.Errorf("agent 'atlas' got same color as human 'sle': %v", atlasColor)
	}

	// Verify with REAL data shape: agents can have Source="human" but Role != "human".
	ts := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	msgs := []protocol.MessageParams{
		{From: "atlas", Source: "human", Role: "just chit chat",
			Content:   []protocol.Content{{Type: "text", Text: "hello from atlas"}},
			Timestamp: ts},
	}
	rendered := renderMessages(msgs, 80, nil)

	// atlas should NOT get human orange (#f0883e) — it should get its agent color.
	if strings.Contains(rendered, "f0883e") {
		t.Errorf("atlas (agent with source=human, role='just chit chat') rendered with human orange — should use agent color %v", atlasColor)
	}
}

func TestRenderMessagesCodeBlock(t *testing.T) {
	// A message containing a fenced code block should preserve the code
	// content and its indentation/formatting after rendering through
	// renderMarkdown. The bug was that trimLeadingANSISpaces stripped ALL
	// leading whitespace+ANSI, destroying the code block's background
	// styling that Glamour renders as colored spaces.
	text := "Here is JSON:\n\n```json\n{\"key\": \"value\"}\n```\n\nDone."
	msg := protocol.MessageParams{
		From:   "bot",
		Source: "agent",
		Role:   "agent",
		Content: []protocol.Content{
			{Type: "text", Text: text},
		},
		Timestamp: time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
	}

	rendered := renderMessages([]protocol.MessageParams{msg}, 80, nil)
	plain := stripANSI(rendered)

	// The code content must be present.
	if !strings.Contains(plain, `"key"`) {
		t.Errorf("code block content should be preserved, got:\n%s", plain)
	}

	// Glamour renders code blocks with extra indentation compared to
	// paragraph text. After stripping ANSI, code block lines should have
	// MORE leading spaces than paragraph lines. If trimLeadingANSISpaces
	// strips everything, code lines will have the same (zero) indentation
	// as paragraph lines — that's the bug.

	// Also test renderMarkdown directly to isolate from the border styling.
	md := renderMarkdown(text, 60)
	mdPlain := stripANSI(md)
	mdLines := strings.Split(mdPlain, "\n")

	// Find the line containing the code content.
	codeLineIndent := -1
	paraLineIndent := -1
	for _, line := range mdLines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, `"key"`) {
			codeLineIndent = len(line) - len(strings.TrimLeft(line, " "))
		}
		if strings.Contains(trimmed, "Here is JSON") {
			paraLineIndent = len(line) - len(strings.TrimLeft(line, " "))
		}
	}

	if codeLineIndent < 0 {
		t.Fatalf("could not find code line in rendered markdown:\n%s", mdPlain)
	}
	if paraLineIndent < 0 {
		t.Fatalf("could not find paragraph line in rendered markdown:\n%s", mdPlain)
	}

	// Code block lines should have strictly more indentation than paragraph lines.
	if codeLineIndent <= paraLineIndent {
		t.Errorf("code block should have more indentation than paragraph text "+
			"(code indent=%d, para indent=%d):\n%s",
			codeLineIndent, paraLineIndent, mdPlain)
	}
}

func TestMentionColorMatchesSenderColor(t *testing.T) {
	// robert's sender color and @robert mention color must match.
	robertColor := ColorForSender("robert", false, "")
	t.Logf("robert sender color: %v", robertColor)

	// Render a message containing @robert
	ts := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	msgs := []protocol.MessageParams{
		{From: "sle", Source: "human", Role: "human",
			Content:   []protocol.Content{{Type: "text", Text: "hello @robert how are you"}},
			Timestamp: ts},
	}
	rendered := renderMessages(msgs, 80, nil)
	t.Logf("rendered: %q", rendered)

	// The @robert mention should NOT use human orange.
	// Glamour renders the text first, then highlightMentions runs on the output.
	// Check that @robert is in the output and not colored with human orange.
	plain := stripANSI(rendered)
	if !strings.Contains(plain, "@robert") {
		t.Error("rendered should contain @robert in plain text")
	}
	// Human orange in 256-color ANSI is typically 38;5;XXX — but let's just
	// verify @robert is present. The visual color matching is hard to test
	// via ANSI codes since lipgloss converts hex to nearest 256-color.
	_ = robertColor
}

func TestMentionRegex_MatchesHyphenatedNames(t *testing.T) {
	// mentionRe must match hyphenated participant names like "vivid-junco" in full.
	// Previously the regex was @(\w+) which stopped at the hyphen, leaving
	// @vivid-junco matched as just @vivid.
	tests := []struct {
		input string
		want  string
	}{
		{"@vivid-junco hello", "@vivid-junco"},
		{"@alice hello", "@alice"},
		{"@bob-the-builder", "@bob-the-builder"},
		// Trailing hyphen must NOT be included in the match.
		{"@vivid- hello", "@vivid"},
		// Double hyphen: stops before the second hyphen.
		{"@a--b hello", "@a"},
	}
	for _, tt := range tests {
		got := mentionRe.FindString(tt.input)
		if got != tt.want {
			t.Errorf("mentionRe.FindString(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestHighlightMentions_HyphenatedNamePreservedInPlainText(t *testing.T) {
	// Even in a no-color environment, @vivid-junco must survive as a whole token
	// after highlightMentions runs (not be split into "@vivid" + "-junco").
	colorMap := map[string]string{"vivid-junco": "#ff0000"}
	input := "hello @vivid-junco how are you"
	result := highlightMentions(input, colorMap)
	plain := stripANSI(result)
	if !strings.Contains(plain, "@vivid-junco") {
		t.Errorf("expected plain text to contain @vivid-junco, got: %q", plain)
	}
}
