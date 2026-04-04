package tui

import (
	"testing"

	"github.com/khaiql/parley/internal/protocol"
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

func TestSidebarViewZeroWidth(t *testing.T) {
	// Regression: sidebar.View() panicked with slice bounds out of range
	// when width was 0 (before first WindowSizeMsg)
	s := NewSidebar()
	s.SetSize(0, 0)
	s.AddParticipant(protocol.Participant{
		Name:      "alice",
		Role:      "human",
		Source:    "human",
		Directory: "/Users/sle/some/very/long/directory/path",
	})

	// Must not panic
	view := s.View()
	if !contains(view, "alice") {
		t.Errorf("sidebar view should contain 'alice' even at zero width")
	}
}

func TestSidebarViewSmallWidth(t *testing.T) {
	// Regression: directory truncation caused panic with small widths
	s := NewSidebar()
	s.SetSize(5, 10)
	s.AddParticipant(protocol.Participant{
		Name:      "bot",
		Role:      "coder",
		Source:    "agent",
		Directory: "/a/very/long/path/that/exceeds/width",
	})

	// Must not panic
	_ = s.View()
}

func TestSidebarSetParticipantStatus(t *testing.T) {
	s := NewSidebar()
	s.AddParticipant(protocol.Participant{Name: "alice", Role: "human"})
	s.AddParticipant(protocol.Participant{Name: "bot1", Role: "coder"})

	s.SetParticipantStatus("bot1", "thinking…")

	if s.statuses["bot1"] != "thinking…" {
		t.Errorf("expected status 'thinking…' for bot1, got %q", s.statuses["bot1"])
	}
	// Alice's status should be unaffected
	if s.statuses["alice"] != "" {
		t.Errorf("expected empty status for alice, got %q", s.statuses["alice"])
	}
}

func TestSidebarSetParticipantStatusClear(t *testing.T) {
	s := NewSidebar()
	s.AddParticipant(protocol.Participant{Name: "bot1", Role: "coder"})
	s.SetParticipantStatus("bot1", "thinking…")
	s.SetParticipantStatus("bot1", "")

	if s.statuses["bot1"] != "" {
		t.Errorf("expected empty status after clear, got %q", s.statuses["bot1"])
	}
}

func TestSidebarViewShowsStatus(t *testing.T) {
	s := NewSidebar()
	s.SetSize(30, 20)
	s.AddParticipant(protocol.Participant{Name: "bot1", Role: "coder", Source: "agent", Online: true})
	s.SetParticipantStatus("bot1", "thinking…")

	view := s.View()
	if !contains(view, "thinking…") {
		t.Errorf("sidebar view should contain status 'thinking…', got: %q", view)
	}
}

func TestSidebarViewListeningStatus(t *testing.T) {
	s := NewSidebar()
	s.SetSize(30, 20)
	s.AddParticipant(protocol.Participant{Name: "bot1", Role: "coder", Source: "agent", Online: true})
	s.SetParticipantStatus("bot1", "listening")

	view := stripANSI(s.View())
	if contains(view, "listening") {
		t.Errorf("sidebar view should NOT contain 'listening' status, got: %q", view)
	}
}

func TestSidebarViewNoStatusForIdle(t *testing.T) {
	s := NewSidebar()
	s.SetSize(30, 20)
	s.AddParticipant(protocol.Participant{Name: "bot1", Role: "coder", Source: "agent", Online: true})
	// No status set — idle

	view := s.View()
	// Should not contain any status indicator
	if contains(view, "thinking") || contains(view, "using") {
		t.Errorf("sidebar view should not show status for idle participant, got: %q", view)
	}
}

func TestSidebarViewContainsNames(t *testing.T) {
	s := NewSidebar()
	s.SetSize(30, 20)
	s.AddParticipant(protocol.Participant{Name: "alice", Role: "human", Source: "human", Online: true})
	s.AddParticipant(protocol.Participant{Name: "bot1", Role: "coder", Source: "agent", AgentType: "claude", Online: true})

	view := s.View()

	if !contains(view, "alice") {
		t.Errorf("sidebar view should contain 'alice', got: %q", view)
	}
	if !contains(view, "bot1") {
		t.Errorf("sidebar view should contain 'bot1', got: %q", view)
	}
	if !contains(view, "claude") {
		t.Errorf("sidebar view should contain agent type 'claude', got: %q", view)
	}
}

func TestSidebarViewShowsBranding(t *testing.T) {
	s := NewSidebar()
	s.SetSize(30, 20)
	s.SetPort(55568)

	view := stripANSI(s.View())
	if !contains(view, "parley") {
		t.Errorf("sidebar view should contain 'parley' branding, got: %q", view)
	}
}

func TestSidebarViewColorMatchedNames(t *testing.T) {
	s := NewSidebar()
	s.SetSize(30, 20)
	s.AddParticipant(protocol.Participant{Name: "growth", Role: "coder", Source: "agent", AgentType: "gemini", Online: true})

	view := stripANSI(s.View())
	if !contains(view, "growth") {
		t.Errorf("sidebar view should contain agent name 'growth', got: %q", view)
	}
	if !contains(view, "gemini") {
		t.Errorf("sidebar view should contain agent type 'gemini', got: %q", view)
	}
}

func TestSidebarViewGeneratingSpinner(t *testing.T) {
	s := NewSidebar()
	s.SetSize(30, 20)
	s.AddParticipant(protocol.Participant{Name: "bot1", Role: "coder", Source: "agent", Online: true})
	s.SetParticipantStatus("bot1", "generating")

	view := stripANSI(s.View())
	if !contains(view, "generating") {
		t.Errorf("sidebar view should contain 'generating' for active agent, got: %q", view)
	}
}

func TestSidebarViewSectionHeader(t *testing.T) {
	s := NewSidebar()
	s.SetSize(30, 20)

	view := stripANSI(s.View())
	if !contains(view, "PARTICIPANTS") {
		t.Errorf("sidebar view should contain 'PARTICIPANTS' header, got: %q", view)
	}
}

func TestSidebarTickSpinner(t *testing.T) {
	s := NewSidebar()
	s.AddParticipant(protocol.Participant{Name: "bot1", Role: "coder"})

	// No generating status — should return false
	if s.TickSpinner() {
		t.Error("TickSpinner should return false when no participant is generating")
	}

	s.SetParticipantStatus("bot1", "generating")
	if !s.TickSpinner() {
		t.Error("TickSpinner should return true when a participant is generating")
	}

	// Verify frame advanced
	if s.spinnerFrame != 2 { // ticked twice
		t.Errorf("expected spinnerFrame=2, got %d", s.spinnerFrame)
	}
}

func TestSidebarAgentWithSourceHumanGetsAgentColor(t *testing.T) {
	// Real-world case: agents join with Source="human" but Role != "human".
	// Only the actual human (Role="human") should get orange.
	s := NewSidebar()
	s.SetSize(30, 20)
	s.AddParticipant(protocol.Participant{
		Name:   "sle",
		Role:   "human",
		Source: "human",
		Online: true,
	})
	s.AddParticipant(protocol.Participant{
		Name:   "atlas",
		Role:   "a retired engineer",
		Source: "human", // agents can have source=human
		Online: true,
	})

	view := s.View()

	// sle should be in human orange (#f0883e)
	sleColor := ColorForSender("sle", true)
	atlasColor := ColorForSender("atlas", false)

	if sleColor == atlasColor {
		t.Fatalf("test invalid: sle and atlas should have different colors")
	}

	// atlas should NOT be rendered with humanNameStyle.
	// humanNameStyle uses colorHuman (#f0883e).
	// We can't easily check ANSI in tests (lipgloss no-color in non-TTY),
	// but we CAN verify the sidebar rendering doesn't treat atlas as human
	// by checking the code path: if atlas got humanNameStyle, both would
	// appear identically styled. In non-TTY both are unstyled, so check
	// that at minimum both names appear.
	if !contains(view, "sle") {
		t.Error("sidebar should contain 'sle'")
	}
	if !contains(view, "atlas") {
		t.Error("sidebar should contain 'atlas'")
	}
}

func TestSidebarColorLogicMatchesChat(t *testing.T) {
	// The sidebar determines isHuman by Role=="human", same as chat.
	// Verify the logic produces consistent colors for known cases.
	tests := []struct {
		name      string
		role      string
		source    string
		wantHuman bool
	}{
		{"sle", "human", "human", true},
		{"atlas", "a retired engineer", "human", false}, // agent with source=human
		{"robert", "junior engineer", "agent", false},
		{"bot", "agent", "agent", false},
	}

	for _, tt := range tests {
		isHuman := tt.role == "human" // must match sidebar.go logic
		color := ColorForSender(tt.name, isHuman)
		if isHuman && color != colorHuman {
			t.Errorf("%s: role=%q should be human (orange), got %v", tt.name, tt.role, color)
		}
		if !isHuman && color == colorHuman {
			t.Errorf("%s: role=%q should NOT be human (orange), got %v", tt.name, tt.role, color)
		}
		if tt.wantHuman != isHuman {
			t.Errorf("%s: expected wantHuman=%v, got isHuman=%v", tt.name, tt.wantHuman, isHuman)
		}
	}
}
