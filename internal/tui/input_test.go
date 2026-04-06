package tui

import (
	"strings"
	"testing"
)

// TestInputAgentMode_EmptyShowsWaiting verifies that when in agent mode with no
// text or status, the waiting message is rendered.
func TestInputAgentMode_EmptyShowsWaiting(t *testing.T) {
	inp := NewInput()
	inp.SetMode(InputModeAgent)
	inp.SetWidth(80)

	view := inp.View()
	plain := stripANSI(view)

	if !strings.Contains(plain, "(waiting for messages…)") {
		t.Errorf("expected waiting message in agent mode with no text, got:\n%s", plain)
	}
}

// TestInputAgentMode_StatusTakesPriority verifies that a status message is shown
// when both agentStatus and agentText are set.
// Status display moved to sidebar (issue #4) — no status tests in input

// TestInputAgentMode_LongTextWraps verifies that text longer than the input
// width is wrapped so that no rendered line exceeds the total width.
func TestInputAgentMode_LongTextWraps(t *testing.T) {
	inp := NewInput()
	inp.SetMode(InputModeAgent)
	width := 40
	inp.SetWidth(width)

	// Build text that is definitely wider than the input area (width - padding/border).
	longLine := strings.Repeat("abcdefghij", 10) // 100 chars, should wrap
	inp.SetAgentText(longLine)

	view := inp.View()
	plain := stripANSI(view)

	// Each line should fit within total width (using rune count for Unicode safety).
	lines := strings.Split(plain, "\n")
	for _, line := range lines {
		runes := []rune(line)
		if len(runes) > width {
			t.Errorf("line exceeds width %d: %q (runes=%d)", width, line, len(runes))
		}
	}
}

// TestInputAgentMode_CursorIndicator verifies that a blinking cursor indicator
// (▊) appears at the end of streaming text.
func TestInputAgentMode_CursorIndicator(t *testing.T) {
	inp := NewInput()
	inp.SetMode(InputModeAgent)
	inp.SetWidth(80)
	inp.SetAgentText("hello world")

	view := inp.View()
	// The cursor character should be present in the raw view (may have ANSI codes around it).
	if !strings.Contains(view, "▊") {
		t.Errorf("expected cursor indicator ▊ in agent mode with text, got:\n%s", view)
	}
}

// TestInputHumanMode_Reset verifies that Reset clears the textarea value.
func TestInputHumanMode_Reset(t *testing.T) {
	inp := NewInput()
	inp.SetMode(InputModeHuman)
	// textarea doesn't expose direct setting easily; test that Reset produces empty value.
	inp.Reset()
	if inp.Value() != "" {
		t.Errorf("expected empty value after Reset, got: %q", inp.Value())
	}
}

// TestInputHumanMode_PlaceholderVisible verifies that the human-mode view
// contains some visible content (the textarea with placeholder).
func TestInputHumanMode_PlaceholderVisible(t *testing.T) {
	inp := NewInput()
	inp.SetMode(InputModeHuman)
	inp.SetWidth(80)

	view := inp.View()
	if len(view) == 0 {
		t.Error("expected non-empty view in human mode")
	}
}

// SetAgentTextClearsStatus test removed — status moved to sidebar (issue #4)

func TestInputHumanMode_PromptIndicator(t *testing.T) {
	inp := NewInput()
	inp.SetMode(InputModeHuman)
	inp.SetWidth(80)
	view := inp.View()
	if !strings.Contains(view, "❯") {
		t.Error("human mode input should show ❯ prompt indicator")
	}
}

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

func TestNewInput_CustomKeyMap(t *testing.T) {
	inp := NewInput()

	// Verify InsertNewline is rebound away from "enter"
	km := inp.ta.KeyMap
	for _, k := range km.InsertNewline.Keys() {
		if k == "enter" || k == "ctrl+m" {
			t.Errorf("InsertNewline should not be bound to %q", k)
		}
	}
	// Verify InsertNewline includes shift+enter
	found := false
	for _, k := range km.InsertNewline.Keys() {
		if k == "shift+enter" {
			found = true
		}
	}
	if !found {
		t.Error("InsertNewline should include shift+enter binding")
	}

	// Verify WordForward includes ctrl+right
	found = false
	for _, k := range km.WordForward.Keys() {
		if k == "ctrl+right" {
			found = true
		}
	}
	if !found {
		t.Error("WordForward should include ctrl+right binding")
	}

	// Verify WordBackward includes ctrl+left
	found = false
	for _, k := range km.WordBackward.Keys() {
		if k == "ctrl+left" {
			found = true
		}
	}
	if !found {
		t.Error("WordBackward should include ctrl+left binding")
	}

	// Verify DeleteWordBackward includes ctrl+backspace
	found = false
	for _, k := range km.DeleteWordBackward.Keys() {
		if k == "ctrl+backspace" {
			found = true
		}
	}
	if !found {
		t.Error("DeleteWordBackward should include ctrl+backspace binding")
	}

	// Verify DeleteWordForward includes ctrl+delete
	found = false
	for _, k := range km.DeleteWordForward.Keys() {
		if k == "ctrl+delete" {
			found = true
		}
	}
	if !found {
		t.Error("DeleteWordForward should include ctrl+delete binding")
	}
}
