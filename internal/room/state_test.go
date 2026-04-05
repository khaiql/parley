package room

import (
	"testing"

	"github.com/khaiql/parley/internal/command"
	"github.com/khaiql/parley/internal/protocol"
)

func TestNew_ReturnsEmptyState(t *testing.T) {
	reg := command.NewRegistry()
	ctx := command.Context{}
	s := New(reg, ctx)

	if len(s.Participants()) != 0 {
		t.Errorf("expected 0 participants, got %d", len(s.Participants()))
	}
	if len(s.Messages()) != 0 {
		t.Errorf("expected 0 messages, got %d", len(s.Messages()))
	}
	if len(s.PendingPermissions()) != 0 {
		t.Errorf("expected 0 pending permissions, got %d", len(s.PendingPermissions()))
	}
	if s.AutoApprove() {
		t.Error("expected autoApprove to be false")
	}
	if s.IsAnyoneGenerating() {
		t.Error("expected IsAnyoneGenerating to be false")
	}
}

func TestIsAnyoneGenerating_TrueWhenGenerating(t *testing.T) {
	s := New(nil, command.Context{})
	s.activities["alice"] = ActivityGenerating

	if !s.IsAnyoneGenerating() {
		t.Error("expected IsAnyoneGenerating to be true when a participant is generating")
	}
}

func TestIsAnyoneGenerating_FalseWhenThinking(t *testing.T) {
	s := New(nil, command.Context{})
	s.activities["alice"] = ActivityThinking

	if s.IsAnyoneGenerating() {
		t.Error("expected IsAnyoneGenerating to be false when participant is only thinking")
	}
}

func TestParticipantActivity_ReturnsListeningByDefault(t *testing.T) {
	s := New(nil, command.Context{})

	act := s.ParticipantActivity("unknown-user")
	if act != ActivityListening {
		t.Errorf("expected ActivityListening (%d), got %d", ActivityListening, act)
	}
}

func TestParticipants_ReturnsCopy(t *testing.T) {
	s := New(nil, command.Context{})
	s.participants = []protocol.Participant{
		{Name: "alice", Role: "host"},
	}

	got := s.Participants()
	if len(got) != 1 {
		t.Fatalf("expected 1 participant, got %d", len(got))
	}

	// Mutate the returned slice
	got[0].Name = "mutated"
	got = append(got, protocol.Participant{Name: "extra"})

	// Internal state must be unchanged
	if s.participants[0].Name != "alice" {
		t.Error("internal participant was mutated via returned slice")
	}
	if len(s.participants) != 1 {
		t.Errorf("internal participants length changed: got %d, want 1", len(s.participants))
	}
}

func TestMessages_ReturnsCopy(t *testing.T) {
	s := New(nil, command.Context{})
	s.messages = []protocol.MessageParams{
		{From: "alice", ID: "1"},
	}

	got := s.Messages()
	got[0].From = "mutated"

	if s.messages[0].From != "alice" {
		t.Error("internal message was mutated via returned slice")
	}
}

func TestPendingPermissions_ReturnsCopy(t *testing.T) {
	s := New(nil, command.Context{})
	s.permissions = []PermissionRequest{
		{ID: "p1", AgentName: "agent", Tool: "bash"},
	}

	got := s.PendingPermissions()
	got[0].ID = "mutated"

	if s.permissions[0].ID != "p1" {
		t.Error("internal permission was mutated via returned slice")
	}
}

func TestSetSendFn(t *testing.T) {
	s := New(nil, command.Context{})
	called := false
	s.SetSendFn(func(msg string, targets []string) {
		called = true
	})

	if s.sendFn == nil {
		t.Error("expected sendFn to be set")
	}
	s.sendFn("test", nil)
	if !called {
		t.Error("sendFn was not called")
	}
}

func TestSetAutoApprove(t *testing.T) {
	s := New(nil, command.Context{})
	if s.AutoApprove() {
		t.Error("expected autoApprove to start false")
	}

	s.SetAutoApprove(true)
	if !s.AutoApprove() {
		t.Error("expected autoApprove to be true after SetAutoApprove(true)")
	}
}

func TestAvailableCommands_DelegatesToRegistry(t *testing.T) {
	reg := command.NewRegistry()
	reg.Register(&command.Command{
		Name:        "info",
		Usage:       "/info",
		Description: "Show info",
	})
	s := New(reg, command.Context{})

	cmds := s.AvailableCommands()
	if len(cmds) != 1 {
		t.Fatalf("expected 1 command, got %d", len(cmds))
	}
	if cmds[0].Name != "info" {
		t.Errorf("expected command name 'info', got %q", cmds[0].Name)
	}
}

func TestAvailableCommands_NilRegistry(t *testing.T) {
	s := New(nil, command.Context{})
	cmds := s.AvailableCommands()
	if cmds != nil {
		t.Errorf("expected nil commands with nil registry, got %v", cmds)
	}
}
