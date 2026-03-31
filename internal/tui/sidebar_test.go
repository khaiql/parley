package tui

import (
	"testing"

	"github.com/sle/parley/internal/protocol"
)

func TestSidebarAddParticipant(t *testing.T) {
	s := NewSidebar()

	s.AddParticipant(protocol.Participant{Name: "alice", Role: "human"})
	s.AddParticipant(protocol.Participant{Name: "bot1", Role: "coder"})

	if len(s.participants) != 2 {
		t.Fatalf("expected 2 participants, got %d", len(s.participants))
	}
}

func TestSidebarAddParticipantDeduplicates(t *testing.T) {
	s := NewSidebar()

	s.AddParticipant(protocol.Participant{Name: "alice", Role: "human"})
	s.AddParticipant(protocol.Participant{Name: "alice", Role: "human", AgentType: "claude"})

	if len(s.participants) != 1 {
		t.Fatalf("expected 1 participant after dedup, got %d", len(s.participants))
	}
	if s.participants[0].AgentType != "claude" {
		t.Errorf("expected updated AgentType, got %q", s.participants[0].AgentType)
	}
}

func TestSidebarRemoveParticipant(t *testing.T) {
	s := NewSidebar()

	s.AddParticipant(protocol.Participant{Name: "alice", Role: "human"})
	s.AddParticipant(protocol.Participant{Name: "bot1", Role: "coder"})
	s.RemoveParticipant("alice")

	if len(s.participants) != 1 {
		t.Fatalf("expected 1 participant after removal, got %d", len(s.participants))
	}
	if s.participants[0].Name != "bot1" {
		t.Errorf("expected remaining participant to be bot1, got %q", s.participants[0].Name)
	}
}

func TestSidebarRemoveNonExistent(t *testing.T) {
	s := NewSidebar()

	s.AddParticipant(protocol.Participant{Name: "alice", Role: "human"})
	s.RemoveParticipant("nobody")

	if len(s.participants) != 1 {
		t.Fatalf("expected 1 participant unchanged, got %d", len(s.participants))
	}
}

func TestSidebarSetParticipants(t *testing.T) {
	s := NewSidebar()
	s.AddParticipant(protocol.Participant{Name: "old", Role: "human"})

	newList := []protocol.Participant{
		{Name: "alice", Role: "human"},
		{Name: "bob", Role: "coder"},
	}
	s.SetParticipants(newList)

	if len(s.participants) != 2 {
		t.Fatalf("expected 2 participants after SetParticipants, got %d", len(s.participants))
	}
}

func TestSidebarViewContainsNames(t *testing.T) {
	s := NewSidebar()
	s.SetSize(30, 20)
	s.AddParticipant(protocol.Participant{Name: "alice", Role: "human", Source: "human"})
	s.AddParticipant(protocol.Participant{Name: "bot1", Role: "coder", Source: "agent", AgentType: "claude"})

	view := s.View()

	if !contains(view, "alice") {
		t.Errorf("sidebar view should contain 'alice', got: %q", view)
	}
	if !contains(view, "bot1") {
		t.Errorf("sidebar view should contain 'bot1', got: %q", view)
	}
	if !contains(view, "coder") {
		t.Errorf("sidebar view should contain role badge 'coder', got: %q", view)
	}
	if !contains(view, "claude") {
		t.Errorf("sidebar view should contain agent type 'claude', got: %q", view)
	}
}
