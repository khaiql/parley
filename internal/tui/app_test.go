package tui

import (
	"encoding/json"
	"testing"

	"github.com/sle/parley/internal/protocol"
)

// ---- parseMentions -----------------------------------------------------------

func TestParseMentions_None(t *testing.T) {
	mentions := parseMentions("hello world, no mentions here")
	if len(mentions) != 0 {
		t.Fatalf("expected no mentions, got %v", mentions)
	}
}

func TestParseMentions_Single(t *testing.T) {
	mentions := parseMentions("hey @alice how are you")
	if len(mentions) != 1 || mentions[0] != "alice" {
		t.Fatalf("expected [alice], got %v", mentions)
	}
}

func TestParseMentions_Multiple(t *testing.T) {
	mentions := parseMentions("@alice and @bob please review")
	if len(mentions) != 2 {
		t.Fatalf("expected 2 mentions, got %v", mentions)
	}
	if mentions[0] != "alice" || mentions[1] != "bob" {
		t.Errorf("unexpected mentions: %v", mentions)
	}
}

func TestParseMentions_AtSignAlone(t *testing.T) {
	// A bare "@" should not count as a mention.
	mentions := parseMentions("email me @ home")
	if len(mentions) != 0 {
		t.Fatalf("expected no mentions, got %v", mentions)
	}
}

func TestParseMentions_LeadingAtWithPunctuation(t *testing.T) {
	// The whole token starting with @ is captured as-is (minus the @).
	mentions := parseMentions("@alice!")
	if len(mentions) != 1 || mentions[0] != "alice!" {
		t.Fatalf("expected [alice!], got %v", mentions)
	}
}

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

func TestHandleServerMsg_RoomLeft_RemovesParticipant(t *testing.T) {
	a := makeApp()
	a.sidebar.SetParticipants([]protocol.Participant{
		{Name: "alice", Role: "human"},
		{Name: "bot", Role: "agent"},
	})

	left := protocol.LeftParams{Name: "bot"}
	raw := &protocol.RawMessage{
		Method: "room.left",
		Params: rawParams(t, left),
	}

	a.handleServerMsg(raw)

	if len(a.sidebar.participants) != 1 {
		t.Fatalf("expected 1 participant after leave, got %d", len(a.sidebar.participants))
	}
	if a.sidebar.participants[0].Name != "alice" {
		t.Errorf("wrong participant remains: %s", a.sidebar.participants[0].Name)
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
