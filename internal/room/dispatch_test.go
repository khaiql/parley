package room

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/khaiql/parley/internal/command"
	"github.com/khaiql/parley/internal/protocol"
)

// ---- Test helpers -----------------------------------------------------------

// rawMsg builds a *protocol.RawMessage with the given method and marshalled params.
func rawMsg(t *testing.T, method string, params interface{}) *protocol.RawMessage {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("rawMsg: marshal params: %v", err)
	}
	return &protocol.RawMessage{
		JSONRPC: "2.0",
		Method:  method,
		Params:  json.RawMessage(raw),
	}
}

// nextEvent reads the next event from ch, failing the test if none arrives within 1 second.
func nextEvent(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case evt := <-ch:
		return evt
	case <-time.After(time.Second):
		t.Fatal("nextEvent: timed out waiting for event")
		return nil
	}
}

// noMoreEvents asserts that ch has no pending events.
func noMoreEvents(t *testing.T, ch <-chan Event) {
	t.Helper()
	select {
	case evt := <-ch:
		t.Fatalf("noMoreEvents: unexpected event %T: %+v", evt, evt)
	default:
		// good — channel is empty
	}
}

// ---- Tests ------------------------------------------------------------------

func TestHandleServerMessage_RoomMessage_EmitsMessageReceived(t *testing.T) {
	s := New(nil, command.Context{})
	ch := s.Subscribe()

	msg := protocol.MessageParams{
		From:    "alice",
		Role:    "human",
		Content: []protocol.Content{{Type: "text", Text: "hello"}},
	}
	s.HandleServerMessage(rawMsg(t, protocol.MethodMessage, msg))

	evt := nextEvent(t, ch)
	mr, ok := evt.(MessageReceived)
	if !ok {
		t.Fatalf("expected MessageReceived, got %T", evt)
	}
	if mr.Message.From != "alice" {
		t.Errorf("From = %q, want %q", mr.Message.From, "alice")
	}
	if len(mr.Message.Content) != 1 || mr.Message.Content[0].Text != "hello" {
		t.Errorf("Content mismatch: %+v", mr.Message.Content)
	}

	// Verify message is stored internally.
	msgs := s.Messages()
	if len(msgs) != 1 {
		t.Fatalf("internal messages len = %d, want 1", len(msgs))
	}
	if msgs[0].From != "alice" {
		t.Errorf("stored message From = %q, want %q", msgs[0].From, "alice")
	}

	noMoreEvents(t, ch)
}

func TestHandleServerMessage_RoomJoined_EmitsParticipantsChanged(t *testing.T) {
	s := New(nil, command.Context{})
	ch := s.Subscribe()

	joined := protocol.JoinedParams{
		Name:      "bot-1",
		Role:      "agent",
		Directory: "/tmp",
		Repo:      "test/repo",
		AgentType: "claude",
	}
	s.HandleServerMessage(rawMsg(t, protocol.MethodJoined, joined))

	evt := nextEvent(t, ch)
	pc, ok := evt.(ParticipantsChanged)
	if !ok {
		t.Fatalf("expected ParticipantsChanged, got %T", evt)
	}
	if len(pc.Participants) != 1 {
		t.Fatalf("participants len = %d, want 1", len(pc.Participants))
	}
	p := pc.Participants[0]
	if p.Name != "bot-1" {
		t.Errorf("Name = %q, want %q", p.Name, "bot-1")
	}
	if !p.Online {
		t.Error("expected Online = true")
	}
	if p.AgentType != "claude" {
		t.Errorf("AgentType = %q, want %q", p.AgentType, "claude")
	}

	noMoreEvents(t, ch)
}

func TestHandleServerMessage_RoomLeft_EmitsParticipantsChanged(t *testing.T) {
	s := New(nil, command.Context{})
	// Pre-populate a participant.
	s.participants = []protocol.Participant{
		{Name: "bot-1", Role: "agent", Online: true},
	}
	ch := s.Subscribe()

	s.HandleServerMessage(rawMsg(t, protocol.MethodLeft, protocol.LeftParams{Name: "bot-1"}))

	evt := nextEvent(t, ch)
	pc, ok := evt.(ParticipantsChanged)
	if !ok {
		t.Fatalf("expected ParticipantsChanged, got %T", evt)
	}
	if len(pc.Participants) != 1 {
		t.Fatalf("participants len = %d, want 1", len(pc.Participants))
	}
	if pc.Participants[0].Online {
		t.Error("expected participant to be offline")
	}

	noMoreEvents(t, ch)
}

func TestHandleServerMessage_RoomStatus_EmitsParticipantActivityChanged(t *testing.T) {
	s := New(nil, command.Context{})
	ch := s.Subscribe()

	s.HandleServerMessage(rawMsg(t, protocol.MethodStatus, protocol.StatusParams{
		Name:   "bot-1",
		Status: "generating",
	}))

	evt := nextEvent(t, ch)
	pac, ok := evt.(ParticipantActivityChanged)
	if !ok {
		t.Fatalf("expected ParticipantActivityChanged, got %T", evt)
	}
	if pac.Name != "bot-1" {
		t.Errorf("Name = %q, want %q", pac.Name, "bot-1")
	}
	if pac.Activity != ActivityGenerating {
		t.Errorf("Activity = %v, want ActivityGenerating", pac.Activity)
	}

	// Verify internal state updated.
	if s.ParticipantActivity("bot-1") != ActivityGenerating {
		t.Error("internal activity not updated")
	}

	noMoreEvents(t, ch)
}

func TestHandleServerMessage_RoomState_EmitsHistoryLoaded(t *testing.T) {
	s := New(nil, command.Context{})
	ch := s.Subscribe()

	state := protocol.RoomStateParams{
		AutoApprove: true,
		Participants: []protocol.Participant{
			{Name: "alice", Role: "human", Online: true},
			{Name: "bot-1", Role: "agent", Online: true, AgentType: "claude"},
		},
		Messages: []protocol.MessageParams{
			{From: "alice", Content: []protocol.Content{{Type: "text", Text: "hi"}}},
		},
	}
	s.HandleServerMessage(rawMsg(t, protocol.MethodState, state))

	evt := nextEvent(t, ch)
	hl, ok := evt.(HistoryLoaded)
	if !ok {
		t.Fatalf("expected HistoryLoaded, got %T", evt)
	}
	if len(hl.Participants) != 2 {
		t.Errorf("participants len = %d, want 2", len(hl.Participants))
	}
	if len(hl.Messages) != 1 {
		t.Errorf("messages len = %d, want 1", len(hl.Messages))
	}

	// Verify autoApprove set.
	if !s.AutoApprove() {
		t.Error("expected autoApprove = true")
	}

	// Verify internal state.
	if len(s.Participants()) != 2 {
		t.Errorf("internal participants len = %d, want 2", len(s.Participants()))
	}
	if len(s.Messages()) != 1 {
		t.Errorf("internal messages len = %d, want 1", len(s.Messages()))
	}

	noMoreEvents(t, ch)
}

func TestHandleServerMessage_Ordering_ParticipantBeforeMessage(t *testing.T) {
	s := New(nil, command.Context{})
	ch := s.Subscribe()

	// Send joined then message.
	s.HandleServerMessage(rawMsg(t, protocol.MethodJoined, protocol.JoinedParams{
		Name: "bot-1",
		Role: "agent",
	}))
	s.HandleServerMessage(rawMsg(t, protocol.MethodMessage, protocol.MessageParams{
		From:    "bot-1",
		Content: []protocol.Content{{Type: "text", Text: "hi"}},
	}))

	evt1 := nextEvent(t, ch)
	if _, ok := evt1.(ParticipantsChanged); !ok {
		t.Fatalf("first event: expected ParticipantsChanged, got %T", evt1)
	}

	evt2 := nextEvent(t, ch)
	if _, ok := evt2.(MessageReceived); !ok {
		t.Fatalf("second event: expected MessageReceived, got %T", evt2)
	}

	noMoreEvents(t, ch)
}

func TestHandleServerMessage_NilRaw_NoOp(t *testing.T) {
	s := New(nil, command.Context{})
	ch := s.Subscribe()

	s.HandleServerMessage(nil) // should not panic or emit

	noMoreEvents(t, ch)
}

func TestParseActivity(t *testing.T) {
	tests := []struct {
		input string
		want  Activity
	}{
		{"generating", ActivityGenerating},
		{"thinking", ActivityThinking},
		{"using_tool", ActivityUsingTool},
		{"", ActivityIdle},
		{"unknown", ActivityIdle},
	}
	for _, tt := range tests {
		if got := ParseActivity(tt.input); got != tt.want {
			t.Errorf("ParseActivity(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestHandleServerMessage_UnmarshalError_EmitsErrorOccurred(t *testing.T) {
	s := New(nil, command.Context{})
	ch := s.Subscribe()

	// Send a message with invalid JSON params.
	bad := &protocol.RawMessage{
		JSONRPC: "2.0",
		Method:  protocol.MethodMessage,
		Params:  json.RawMessage(`{invalid`),
	}
	s.HandleServerMessage(bad)

	evt := nextEvent(t, ch)
	eo, ok := evt.(ErrorOccurred)
	if !ok {
		t.Fatalf("expected ErrorOccurred, got %T", evt)
	}
	if eo.Error == nil {
		t.Error("expected non-nil error")
	}

	noMoreEvents(t, ch)
}
