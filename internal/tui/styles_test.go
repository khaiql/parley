package tui

import "testing"

func TestColorForSenderHumanAlwaysOrange(t *testing.T) {
	c := ColorForSender("Alice", true, "")
	if c != colorHuman {
		t.Errorf("expected human color %v, got %v", colorHuman, c)
	}
	// Different name, still human.
	c2 := ColorForSender("Bob", true, "")
	if c2 != colorHuman {
		t.Errorf("expected human color %v for Bob, got %v", colorHuman, c2)
	}
}

func TestColorForSender_UsesAssignedColour(t *testing.T) {
	c := ColorForSender("bot1", false, "#a78bfa")
	if string(c) != "#a78bfa" {
		t.Errorf("expected assigned colour #a78bfa, got %v", c)
	}
}

func TestColorForSender_FallsBackToHash(t *testing.T) {
	// No assigned colour — should fall back to hash-based
	c1 := ColorForSender("claude-code", false, "")
	c2 := ColorForSender("claude-code", false, "")
	if c1 != c2 {
		t.Errorf("same name should return same fallback color: got %v and %v", c1, c2)
	}
}
