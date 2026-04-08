package room

import (
	"encoding/json"
	"fmt"
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
	if act != ActivityIdle {
		t.Errorf("expected ActivityIdle (%d), got %d", ActivityIdle, act)
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
	_ = append(got, protocol.Participant{Name: "extra"})

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

func TestState_RoomQuerier_AfterState(t *testing.T) {
	rs := New(nil, command.Context{})

	if rs.GetID() == "" {
		t.Error("expected non-empty default ID from New()")
	}
	if rs.GetTopic() != "" {
		t.Errorf("expected empty topic before state, got %q", rs.GetTopic())
	}
	if rs.GetMessageCount() != 0 {
		t.Errorf("expected 0 messages, got %d", rs.GetMessageCount())
	}
	if rs.GetParticipants() != nil {
		t.Errorf("expected nil participants, got %v", rs.GetParticipants())
	}

	stateJSON, _ := json.Marshal(protocol.RoomStateParams{
		RoomID: "room-123",
		Topic:  "test topic",
		Participants: []protocol.Participant{
			{Name: "alice", Role: "human", Online: true},
		},
		Messages: []protocol.MessageParams{
			{ID: "msg-1", From: "alice", Content: []protocol.Content{{Type: "text", Text: "hi"}}},
		},
	})
	rs.HandleServerMessage(&protocol.RawMessage{
		Method: protocol.MethodState,
		Params: stateJSON,
	})

	if rs.GetID() != "room-123" {
		t.Errorf("GetID() = %q, want %q", rs.GetID(), "room-123")
	}
	if rs.GetTopic() != "test topic" {
		t.Errorf("GetTopic() = %q, want %q", rs.GetTopic(), "test topic")
	}
	if rs.GetMessageCount() != 1 {
		t.Errorf("GetMessageCount() = %d, want 1", rs.GetMessageCount())
	}
	participants := rs.GetParticipants()
	if len(participants) != 1 || participants[0].Name != "alice" {
		t.Errorf("GetParticipants() = %v, want [alice]", participants)
	}
}

// drainEvent reads one event from ch or fails the test if none arrives.
func drainEvent(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case e := <-ch:
		return e
	default:
		t.Fatal("expected an event but channel was empty")
		return nil
	}
}

func TestState_Join_NewParticipant(t *testing.T) {
	s := New(nil, command.Context{})
	ch := s.Subscribe()

	snap, err := s.Join("alice", "human", "/home/alice", "myrepo", "", "human")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if snap.RoomID == "" {
		t.Error("expected non-empty RoomID")
	}
	if len(snap.Participants) != 1 {
		t.Fatalf("expected 1 participant, got %d", len(snap.Participants))
	}
	if snap.Participants[0].Name != "alice" || !snap.Participants[0].Online {
		t.Errorf("unexpected participant: %+v", snap.Participants[0])
	}

	evt := drainEvent(t, ch)
	pc, ok := evt.(ParticipantsChanged)
	if !ok {
		t.Fatalf("expected ParticipantsChanged, got %T", evt)
	}
	if len(pc.Participants) != 1 || pc.Participants[0].Name != "alice" {
		t.Errorf("unexpected event participants: %+v", pc.Participants)
	}
}

func TestState_Join_DuplicateOnlineReturnsError(t *testing.T) {
	s := New(nil, command.Context{})

	_, err := s.Join("alice", "human", "/home/alice", "myrepo", "", "human")
	if err != nil {
		t.Fatalf("first join failed: %v", err)
	}

	_, err = s.Join("alice", "human", "/home/alice2", "myrepo", "", "human")
	if err == nil {
		t.Fatal("expected error for duplicate online join, got nil")
	}
}

func TestState_Join_ReconnectsOffline(t *testing.T) {
	s := New(nil, command.Context{})

	_, _ = s.Join("alice", "human", "/home/alice", "repo1", "", "human")
	s.Leave("alice")

	snap, err := s.Join("alice", "", "/home/alice2", "repo2", "claude", "agent")
	if err != nil {
		t.Fatalf("rejoin failed: %v", err)
	}

	if len(snap.Participants) != 1 {
		t.Fatalf("expected 1 participant, got %d", len(snap.Participants))
	}
	p := snap.Participants[0]
	if !p.Online {
		t.Error("expected participant to be online after rejoin")
	}
	// Empty role preserves previous
	if p.Role != "human" {
		t.Errorf("expected role 'human' preserved, got %q", p.Role)
	}
	if p.Directory != "/home/alice2" {
		t.Errorf("expected directory updated to /home/alice2, got %q", p.Directory)
	}
}

func TestState_Join_AssignsColour(t *testing.T) {
	s := New(nil, command.Context{})

	snap, err := s.Join("bot1", "agent", "", "", "claude", "agent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	p := snap.Participants[0]
	if p.Color == "" {
		t.Error("expected a colour to be assigned")
	}

	// Verify colour is from the palette
	found := false
	for _, c := range AgentPalette {
		if c == p.Color {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("assigned colour %q not in AgentPalette", p.Color)
	}
}

func TestState_Join_HumanGetsNoColour(t *testing.T) {
	s := New(nil, command.Context{})

	snap, err := s.Join("alice", "human", "", "", "", "human")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if snap.Participants[0].Color != "" {
		t.Errorf("expected human to have no server-assigned colour, got %q", snap.Participants[0].Color)
	}
}

func TestState_Join_UniqueColoursForAgents(t *testing.T) {
	s := New(nil, command.Context{})

	colours := make(map[string]bool)
	for i := 0; i < len(AgentPalette); i++ {
		name := fmt.Sprintf("agent%d", i)
		snap, err := s.Join(name, "agent", "", "", "claude", "agent")
		if err != nil {
			t.Fatalf("join %d failed: %v", i, err)
		}
		c := snap.Participants[len(snap.Participants)-1].Color
		if colours[c] {
			t.Errorf("duplicate colour %q for %s", c, name)
		}
		colours[c] = true
	}
}

func TestState_Join_ReconnectKeepsColour(t *testing.T) {
	s := New(nil, command.Context{})

	snap1, _ := s.Join("bot1", "agent", "", "", "claude", "agent")
	originalColour := snap1.Participants[0].Color

	s.Leave("bot1")

	snap2, err := s.Join("bot1", "", "", "", "claude", "agent")
	if err != nil {
		t.Fatalf("rejoin failed: %v", err)
	}

	if snap2.Participants[0].Color != originalColour {
		t.Errorf("expected colour %q preserved on reconnect, got %q", originalColour, snap2.Participants[0].Color)
	}
}

func TestState_Join_AfterRestoreAvoidsUsedColour(t *testing.T) {
	s := New(nil, command.Context{})

	// Restore a room containing an offline agent with an assigned colour.
	restoredColour := AgentPalette[0]
	s.Restore("room-1", "topic", []protocol.Participant{
		{Name: "old-bot", Role: "agent", Color: restoredColour, Source: "agent", Online: false},
	}, nil, false)

	// A new agent joins. It must not receive the restored colour.
	snap, err := s.Join("new-bot", "agent", "", "", "claude", "agent")
	if err != nil {
		t.Fatalf("join failed: %v", err)
	}

	var newBotColour string
	for _, p := range snap.Participants {
		if p.Name == "new-bot" {
			newBotColour = p.Color
			break
		}
	}

	if newBotColour == restoredColour {
		t.Errorf("new-bot got colour %q which was already used by restored old-bot", restoredColour)
	}
	if newBotColour == "" {
		t.Error("new-bot should have been assigned a colour")
	}
}

func TestState_Join_LegacyParticipantGetsColourOnReconnect(t *testing.T) {
	s := New(nil, command.Context{})

	// Simulate a legacy room: agent participant restored with no colour.
	s.Restore("room-1", "topic", []protocol.Participant{
		{Name: "legacy-bot", Role: "agent", Color: "", Source: "agent", Online: false},
	}, nil, false)

	snap, err := s.Join("legacy-bot", "agent", "", "", "claude", "agent")
	if err != nil {
		t.Fatalf("rejoin failed: %v", err)
	}

	if snap.Participants[0].Color == "" {
		t.Error("expected legacy participant to receive a colour on reconnect")
	}
}

func TestState_Leave(t *testing.T) {
	s := New(nil, command.Context{})
	ch := s.Subscribe()

	_, _ = s.Join("alice", "human", "", "", "", "human")
	// drain join event
	drainEvent(t, ch)

	s.Leave("alice")

	ps := s.Participants()
	if len(ps) != 1 {
		t.Fatalf("expected 1 participant, got %d", len(ps))
	}
	if ps[0].Online {
		t.Error("expected participant to be offline after leave")
	}

	evt := drainEvent(t, ch)
	pc, ok := evt.(ParticipantsChanged)
	if !ok {
		t.Fatalf("expected ParticipantsChanged, got %T", evt)
	}
	if pc.Participants[0].Online {
		t.Error("event should show participant offline")
	}
}

func TestState_AddMessage(t *testing.T) {
	s := New(nil, command.Context{})
	ch := s.Subscribe()

	_, _ = s.Join("alice", "human", "", "", "", "human")
	_, _ = s.Join("bob", "agent", "", "", "claude", "agent")
	// drain join events
	drainEvent(t, ch)
	drainEvent(t, ch)

	msg := s.AddMessage("alice", "human", "human", protocol.Content{Type: "text", Text: "hello @bob"})

	if msg.Seq != 1 {
		t.Errorf("expected seq 1, got %d", msg.Seq)
	}
	if msg.ID == "" {
		t.Error("expected non-empty message ID")
	}
	if len(msg.Mentions) != 1 || msg.Mentions[0] != "bob" {
		t.Errorf("expected mentions [bob], got %v", msg.Mentions)
	}
	if msg.Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}

	// Verify stored
	msgs := s.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}

	evt := drainEvent(t, ch)
	mr, ok := evt.(MessageReceived)
	if !ok {
		t.Fatalf("expected MessageReceived, got %T", evt)
	}
	if mr.Message.ID != msg.ID {
		t.Errorf("event message ID mismatch: %q vs %q", mr.Message.ID, msg.ID)
	}
}

func TestState_AddSystemMessage(t *testing.T) {
	s := New(nil, command.Context{})

	msg := s.AddSystemMessage("alice joined")

	if msg.From != "system" {
		t.Errorf("expected from 'system', got %q", msg.From)
	}
	if !msg.IsSystem() {
		t.Error("expected IsSystem() to return true")
	}
}

func TestState_RecentMessages(t *testing.T) {
	s := New(nil, command.Context{})

	// Add 5 real messages and intersperse system messages
	for i := 0; i < 5; i++ {
		s.AddMessage("alice", "human", "human", protocol.Content{Type: "text", Text: fmt.Sprintf("msg %d", i)})
		s.AddSystemMessage(fmt.Sprintf("system %d", i))
	}

	// Total: 10 messages (5 real + 5 system)
	recent := s.RecentMessages(3)

	// Should contain at least 3 non-system messages plus interspersed system ones
	nonSystem := 0
	for _, m := range recent {
		if !m.IsSystem() {
			nonSystem++
		}
	}
	if nonSystem < 3 {
		t.Errorf("expected at least 3 non-system messages, got %d", nonSystem)
	}
}

func TestState_UpdateStatus(t *testing.T) {
	s := New(nil, command.Context{})
	ch := s.Subscribe()

	s.UpdateStatus("alice", "generating")

	act := s.ParticipantActivity("alice")
	if act != ActivityGenerating {
		t.Errorf("expected ActivityGenerating, got %d", act)
	}

	evt := drainEvent(t, ch)
	pac, ok := evt.(ParticipantActivityChanged)
	if !ok {
		t.Fatalf("expected ParticipantActivityChanged, got %T", evt)
	}
	if pac.Name != "alice" || pac.Activity != ActivityGenerating {
		t.Errorf("unexpected event: %+v", pac)
	}
}

func TestState_Restore(t *testing.T) {
	s := New(nil, command.Context{})

	msgs := []protocol.MessageParams{
		{ID: "m1", Seq: 5, From: "alice"},
		{ID: "m2", Seq: 10, From: "bob"},
		{ID: "m3", Seq: 7, From: "alice"},
	}
	participants := []protocol.Participant{
		{Name: "alice", Role: "human", Online: true},
	}

	s.Restore("room-42", "test topic", participants, msgs, true)

	if s.GetID() != "room-42" {
		t.Errorf("expected roomID 'room-42', got %q", s.GetID())
	}
	if s.GetTopic() != "test topic" {
		t.Errorf("expected topic 'test topic', got %q", s.GetTopic())
	}
	if !s.AutoApprove() {
		t.Error("expected autoApprove to be true")
	}
	if len(s.Messages()) != 3 {
		t.Errorf("expected 3 messages, got %d", len(s.Messages()))
	}

	// Seq should continue from highest (10)
	msg := s.AddMessage("alice", "human", "human", protocol.Content{Type: "text", Text: "new"})
	if msg.Seq != 11 {
		t.Errorf("expected seq 11 after restore, got %d", msg.Seq)
	}
}

func TestState_ParticipantNames(t *testing.T) {
	s := New(nil, command.Context{})

	_, _ = s.Join("alice", "human", "", "", "", "human")
	_, _ = s.Join("bob", "agent", "", "", "claude", "agent")

	names := s.ParticipantNames()
	if len(names) != 2 {
		t.Fatalf("expected 2 names, got %d", len(names))
	}

	nameSet := map[string]bool{}
	for _, n := range names {
		nameSet[n] = true
	}
	if !nameSet["alice"] || !nameSet["bob"] {
		t.Errorf("expected alice and bob, got %v", names)
	}
}
