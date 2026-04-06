package persistence

import (
	"testing"
	"time"

	"github.com/khaiql/parley/internal/protocol"
)

func TestJSONStore_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	store := NewJSONStore(dir)

	ts := time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC)
	snap := protocol.RoomSnapshot{
		RoomID:      "room-1",
		Topic:       "Test Room",
		AutoApprove: true,
		Participants: []protocol.Participant{
			{
				Name:      "alice",
				Role:      "human",
				Directory: "/home/alice",
				Source:    "human",
				Online:    true,
			},
		},
		Messages: []protocol.MessageParams{
			{
				ID:        "msg-1",
				Seq:       1,
				From:      "alice",
				Source:    "human",
				Role:      "human",
				Timestamp: ts,
				Content:   []protocol.Content{{Type: "text", Text: "hello"}},
			},
		},
	}

	if err := store.Save(snap); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load("room-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.RoomID != snap.RoomID {
		t.Errorf("RoomID = %q, want %q", loaded.RoomID, snap.RoomID)
	}
	if loaded.Topic != snap.Topic {
		t.Errorf("Topic = %q, want %q", loaded.Topic, snap.Topic)
	}
	if loaded.AutoApprove != snap.AutoApprove {
		t.Errorf("AutoApprove = %v, want %v", loaded.AutoApprove, snap.AutoApprove)
	}

	if len(loaded.Participants) != 1 {
		t.Fatalf("Participants len = %d, want 1", len(loaded.Participants))
	}
	p := loaded.Participants[0]
	if p.Name != "alice" {
		t.Errorf("Participant.Name = %q, want %q", p.Name, "alice")
	}
	if p.Online {
		t.Errorf("Participant.Online = %v, want false (loaded participants are offline)", p.Online)
	}

	if len(loaded.Messages) != 1 {
		t.Fatalf("Messages len = %d, want 1", len(loaded.Messages))
	}
	m := loaded.Messages[0]
	if m.ID != "msg-1" {
		t.Errorf("Message.ID = %q, want %q", m.ID, "msg-1")
	}
	if m.Seq != 1 {
		t.Errorf("Message.Seq = %d, want 1", m.Seq)
	}
	if m.From != "alice" {
		t.Errorf("Message.From = %q, want %q", m.From, "alice")
	}
	if m.Source != "human" {
		t.Errorf("Message.Source = %q, want %q", m.Source, "human")
	}
	if m.Role != "human" {
		t.Errorf("Message.Role = %q, want %q", m.Role, "human")
	}
	if !m.Timestamp.Equal(ts) {
		t.Errorf("Message.Timestamp = %v, want %v", m.Timestamp, ts)
	}
	if len(m.Content) != 1 || m.Content[0].Text != "hello" {
		t.Errorf("Message.Content = %v, want [{text hello}]", m.Content)
	}
}

func TestJSONStore_FindAgentSession_NonexistentRoom(t *testing.T) {
	dir := t.TempDir()
	store := NewJSONStore(dir)

	sid, err := store.FindAgentSession("no-such-room", "bot")
	if err != nil {
		t.Fatalf("FindAgentSession on nonexistent room: %v", err)
	}
	if sid != "" {
		t.Errorf("expected empty session, got %q", sid)
	}
}

func TestJSONStore_SaveAgentSession_CreatesDir(t *testing.T) {
	dir := t.TempDir()
	store := NewJSONStore(dir)

	// SaveAgentSession on a room that has never been saved should create the dir.
	if err := store.SaveAgentSession("new-room", "bot", "sess-1"); err != nil {
		t.Fatalf("SaveAgentSession on new room: %v", err)
	}

	sid, err := store.FindAgentSession("new-room", "bot")
	if err != nil {
		t.Fatalf("FindAgentSession: %v", err)
	}
	if sid != "sess-1" {
		t.Errorf("session = %q, want %q", sid, "sess-1")
	}
}

func TestJSONStore_LoadNotFound(t *testing.T) {
	dir := t.TempDir()
	store := NewJSONStore(dir)

	_, err := store.Load("nonexistent-room")
	if err == nil {
		t.Fatal("Load of nonexistent room should return error")
	}
}

func TestJSONStore_AgentSessions(t *testing.T) {
	dir := t.TempDir()
	store := NewJSONStore(dir)

	// Save a room first so the directory exists.
	snap := protocol.RoomSnapshot{
		RoomID: "room-2",
		Topic:  "Agent Session Test",
		Participants: []protocol.Participant{
			{Name: "bot", Role: "agent", Source: "agent"},
		},
	}
	if err := store.Save(snap); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Save an agent session.
	if err := store.SaveAgentSession("room-2", "bot", "sess-100"); err != nil {
		t.Fatalf("SaveAgentSession: %v", err)
	}

	// Find it.
	sid, err := store.FindAgentSession("room-2", "bot")
	if err != nil {
		t.Fatalf("FindAgentSession: %v", err)
	}
	if sid != "sess-100" {
		t.Errorf("session = %q, want %q", sid, "sess-100")
	}

	// Update it.
	if err := store.SaveAgentSession("room-2", "bot", "sess-200"); err != nil {
		t.Fatalf("SaveAgentSession (update): %v", err)
	}
	sid, err = store.FindAgentSession("room-2", "bot")
	if err != nil {
		t.Fatalf("FindAgentSession (updated): %v", err)
	}
	if sid != "sess-200" {
		t.Errorf("session = %q, want %q", sid, "sess-200")
	}

	// Unknown agent returns empty string.
	sid, err = store.FindAgentSession("room-2", "unknown")
	if err != nil {
		t.Fatalf("FindAgentSession (unknown): %v", err)
	}
	if sid != "" {
		t.Errorf("session for unknown agent = %q, want empty", sid)
	}
}

func TestJSONStore_SavePreservesExistingSessionIDs(t *testing.T) {
	dir := t.TempDir()
	store := NewJSONStore(dir)

	snap := protocol.RoomSnapshot{
		RoomID: "room-3",
		Topic:  "Session Preserve Test",
		Participants: []protocol.Participant{
			{Name: "bot", Role: "agent", Source: "agent"},
		},
	}
	if err := store.Save(snap); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Save a session ID via SaveAgentSession.
	if err := store.SaveAgentSession("room-3", "bot", "sess-abc"); err != nil {
		t.Fatalf("SaveAgentSession: %v", err)
	}

	// Re-save the snapshot (simulates server persisting room state).
	if err := store.Save(snap); err != nil {
		t.Fatalf("Save (re-save): %v", err)
	}

	// The session ID should be preserved.
	sid, err := store.FindAgentSession("room-3", "bot")
	if err != nil {
		t.Fatalf("FindAgentSession: %v", err)
	}
	if sid != "sess-abc" {
		t.Errorf("session = %q, want %q (should be preserved after re-save)", sid, "sess-abc")
	}
}
