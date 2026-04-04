package tui

import (
	"encoding/json"
	"testing"

	"github.com/khaiql/parley/internal/protocol"
)

// ---- handleServerMsg ---------------------------------------------------------

func makeApp() App {
	return NewApp("test-topic", 9000, InputModeHuman, "tester", nil)
}

func rawParams(t *testing.T, v interface{}) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestHandleServerMsg_RoomMessage_AddsToChat(t *testing.T) {
	a := makeApp()

	msg := protocol.MessageParams{
		ID:   "msg-1",
		From: "alice",
		Role: "human",
		Content: []protocol.Content{
			{Type: "text", Text: "hello"},
		},
	}
	raw := &protocol.RawMessage{
		Method: "room.message",
		Params: rawParams(t, msg),
	}

	a.handleServerMsg(raw)

	if len(a.chat.messages) != 1 {
		t.Fatalf("expected 1 message in chat, got %d", len(a.chat.messages))
	}
	if a.chat.messages[0].From != "alice" {
		t.Errorf("unexpected From: %s", a.chat.messages[0].From)
	}
}

func TestHandleServerMsg_RoomState_SetsParticipants(t *testing.T) {
	a := makeApp()

	state := protocol.RoomStateParams{
		Topic: "test-topic",
		Participants: []protocol.Participant{
			{Name: "alice", Role: "human"},
			{Name: "bot", Role: "agent"},
		},
	}
	raw := &protocol.RawMessage{
		Method: "room.state",
		Params: rawParams(t, state),
	}

	a.handleServerMsg(raw)

	if len(a.sidebar.participants) != 2 {
		t.Fatalf("expected 2 participants, got %d", len(a.sidebar.participants))
	}
	if a.sidebar.participants[0].Name != "alice" {
		t.Errorf("unexpected first participant: %s", a.sidebar.participants[0].Name)
	}
}

func TestHandleServerMsg_RoomJoined_AddsParticipant(t *testing.T) {
	a := makeApp()

	joined := protocol.JoinedParams{
		Name: "carol",
		Role: "agent",
	}
	raw := &protocol.RawMessage{
		Method: "room.joined",
		Params: rawParams(t, joined),
	}

	a.handleServerMsg(raw)

	if len(a.sidebar.participants) != 1 {
		t.Fatalf("expected 1 participant, got %d", len(a.sidebar.participants))
	}
	if a.sidebar.participants[0].Name != "carol" {
		t.Errorf("unexpected participant name: %s", a.sidebar.participants[0].Name)
	}
}

func TestHandleServerMsg_RoomLeft_SetsParticipantOffline(t *testing.T) {
	a := makeApp()
	a.sidebar.SetParticipants([]protocol.Participant{
		{Name: "alice", Role: "human", Online: true},
		{Name: "bot", Role: "agent", Online: true},
	})

	left := protocol.LeftParams{Name: "bot"}
	raw := &protocol.RawMessage{
		Method: "room.left",
		Params: rawParams(t, left),
	}

	a.handleServerMsg(raw)

	if len(a.sidebar.participants) != 2 {
		t.Fatalf("expected 2 participants after leave, got %d", len(a.sidebar.participants))
	}
	if a.sidebar.participants[1].Name != "bot" || a.sidebar.participants[1].Online != false {
		t.Errorf("expected bot to be marked offline: %+v", a.sidebar.participants[1])
	}
}

func TestHandleServerMsg_RoomStatus_UpdatesSidebarStatus(t *testing.T) {
	a := makeApp()
	a.sidebar.SetParticipants([]protocol.Participant{
		{Name: "bot1", Role: "agent"},
	})

	sp := protocol.StatusParams{Name: "bot1", Status: "thinking…"}
	raw := &protocol.RawMessage{
		Method: "room.status",
		Params: rawParams(t, sp),
	}

	a.handleServerMsg(raw)

	if a.sidebar.statuses["bot1"] != "thinking…" {
		t.Errorf("expected sidebar status 'thinking…' for bot1, got %q", a.sidebar.statuses["bot1"])
	}
}

func TestHandleServerMsg_RoomStatusClear_ClearsSidebarStatus(t *testing.T) {
	a := makeApp()
	a.sidebar.SetParticipants([]protocol.Participant{
		{Name: "bot1", Role: "agent"},
	})
	a.sidebar.SetParticipantStatus("bot1", "thinking…")

	sp := protocol.StatusParams{Name: "bot1", Status: ""}
	raw := &protocol.RawMessage{
		Method: "room.status",
		Params: rawParams(t, sp),
	}

	a.handleServerMsg(raw)

	if a.sidebar.statuses["bot1"] != "" {
		t.Errorf("expected empty status after clear, got %q", a.sidebar.statuses["bot1"])
	}
}

func TestHandleServerMsg_RoomState_ReplayesMessageHistory(t *testing.T) {
	a := makeApp()

	state := protocol.RoomStateParams{
		Topic: "test-topic",
		Participants: []protocol.Participant{
			{Name: "alice", Role: "human"},
		},
		Messages: []protocol.MessageParams{
			{ID: "msg-1", From: "alice", Role: "human", Content: []protocol.Content{{Type: "text", Text: "hello"}}},
			{ID: "msg-2", From: "alice", Role: "human", Content: []protocol.Content{{Type: "text", Text: "world"}}},
		},
	}
	raw := &protocol.RawMessage{
		Method: "room.state",
		Params: rawParams(t, state),
	}

	a.handleServerMsg(raw)

	// History is now loaded asynchronously: handleServerMsg sets pendingHistory,
	// then Update dispatches HistoryLoadedMsg.
	if len(a.pendingHistory) != 2 {
		t.Fatalf("expected 2 pending messages after room.state, got %d", len(a.pendingHistory))
	}

	// Simulate the async load completing.
	model, _ := a.Update(ServerMsg{Raw: raw})
	a = model.(App)
	model, _ = a.Update(HistoryLoadedMsg{Messages: state.Messages})
	a = model.(App)

	if len(a.chat.messages) != 2 {
		t.Fatalf("expected 2 messages in chat after history load, got %d", len(a.chat.messages))
	}
	if a.chat.messages[0].From != "alice" {
		t.Errorf("unexpected first message From: %s", a.chat.messages[0].From)
	}
	if a.chat.messages[1].Content[0].Text != "world" {
		t.Errorf("unexpected second message text: %s", a.chat.messages[1].Content[0].Text)
	}
}

func TestHandleServerMsg_NilRaw_NoOp(t *testing.T) {
	a := makeApp()
	// Should not panic.
	a.handleServerMsg(nil)
}

func TestHandleServerMsg_UnknownMethod_NoOp(t *testing.T) {
	a := makeApp()
	raw := &protocol.RawMessage{Method: "unknown.method"}
	// Should not panic.
	a.handleServerMsg(raw)
	if len(a.sidebar.participants) != 0 {
		t.Error("expected no participants after unknown method")
	}
}
