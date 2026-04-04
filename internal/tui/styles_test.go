package tui

import "testing"

func TestColorForSenderHumanAlwaysOrange(t *testing.T) {
	c := ColorForSender("Alice", true)
	if c != colorHuman {
		t.Errorf("expected human color %v, got %v", colorHuman, c)
	}
	// Different name, still human.
	c2 := ColorForSender("Bob", true)
	if c2 != colorHuman {
		t.Errorf("expected human color %v for Bob, got %v", colorHuman, c2)
	}
}

func TestColorForSenderAgentDeterministic(t *testing.T) {
	c1 := ColorForSender("claude-code", false)
	c2 := ColorForSender("claude-code", false)
	if c1 != c2 {
		t.Errorf("same agent name should return same color: got %v and %v", c1, c2)
	}
}

func TestColorForSenderDifferentAgentsDifferentColors(t *testing.T) {
	agents := []string{"claude-code", "gemini-cli", "copilot"}
	colors := make(map[string]bool)
	for _, a := range agents {
		c := ColorForSender(a, false)
		colors[string(c)] = true
	}
	if len(colors) < 2 {
		t.Errorf("expected at least 2 distinct colors from 3 agents, got %d", len(colors))
	}
}
