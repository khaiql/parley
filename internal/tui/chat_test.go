package tui

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/khaiql/parley/internal/protocol"
)

// ansiEscapeRegex strips ANSI escape sequences from a string so we can measure
// visible character width.
var ansiEscapeRegex = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	return ansiEscapeRegex.ReplaceAllString(s, "")
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
	rendered := renderMessage(msg, 80, nil)
	if !contains(rendered, "alice") {
		t.Errorf("renderMessage (human) should contain sender name %q, got: %q", "alice", rendered)
	}
	if !contains(rendered, "hello there") {
		t.Errorf("renderMessage (human) should contain message text, got: %q", rendered)
	}
	if !contains(rendered, "12:00") {
		t.Errorf("renderMessage (human) should contain timestamp, got: %q", rendered)
	}
}

func TestRenderMessageAgentContainsRoleBadge(t *testing.T) {
	msg := protocol.MessageParams{
		From:   "bot1",
		Source: "agent",
		Role:   "coder",
		Content: []protocol.Content{
			{Type: "text", Text: "I wrote some code"},
		},
		Timestamp: time.Date(2024, 1, 1, 8, 30, 0, 0, time.UTC),
	}
	rendered := renderMessage(msg, 80, nil)
	if !contains(rendered, "bot1") {
		t.Errorf("renderMessage (agent) should contain agent name, got: %q", rendered)
	}
	if !contains(rendered, "coder") {
		t.Errorf("renderMessage (agent) should contain role badge, got: %q", rendered)
	}
	if !contains(rendered, "I wrote some code") {
		t.Errorf("renderMessage (agent) should contain message text, got: %q", rendered)
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
	rendered := renderMessage(msg, 80, nil)
	if !contains(rendered, "[system]") {
		t.Errorf("renderMessage (system) should contain [system] prefix, got: %q", rendered)
	}
	if !contains(rendered, "alice has joined") {
		t.Errorf("renderMessage (system) should contain message text, got: %q", rendered)
	}
}

// contains checks whether s contains substr, ignoring ANSI escape codes by
// looking at the raw bytes (lipgloss wraps strings in ANSI codes but the
// plain text is still present).
func contains(s, substr string) bool {
	return len(s) >= len(substr) && indexOf(s, substr) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
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
	rendered := renderMessage(msg, width, nil)

	lines := strings.Split(rendered, "\n")
	if len(lines) <= 1 {
		t.Errorf("expected multiple lines for 200-char message at width %d, got %d line(s)", width, len(lines))
	}

	for i, line := range lines {
		visible := stripANSI(line)
		if len(visible) > width {
			t.Errorf("line %d exceeds width %d: len=%d %q", i, width, len(visible), visible)
		}
	}
}
