package server

import (
	"testing"

	"github.com/khaiql/parley/internal/protocol"
)

// TestJoinDuplicateNameReturnsError verifies that Room.Join returns an error
// when a participant with the same name is already in the room.
func TestJoinDuplicateNameReturnsError(t *testing.T) {
	room := NewRoom("test-topic")

	alice := &ClientConn{Name: "Alice", Role: "user"}
	_, err := room.Join(alice)
	if err != nil {
		t.Fatalf("first Join for Alice should succeed, got error: %v", err)
	}

	// Verify Alice is in the room.
	parts := room.GetParticipants()
	if len(parts) != 1 {
		t.Fatalf("expected 1 participant after first join, got %d", len(parts))
	}

	// Second join with same name should fail.
	alice2 := &ClientConn{Name: "Alice", Role: "user"}
	_, err = room.Join(alice2)
	if err == nil {
		t.Fatal("second Join with same name should return an error, got nil")
	}

	// First Alice's entry must still be intact and channels must be the originals.
	parts = room.GetParticipants()
	if len(parts) != 1 {
		t.Fatalf("expected 1 participant after rejected join, got %d", len(parts))
	}
	if parts[0].Send != alice.Send {
		t.Error("original Alice's Send channel was replaced")
	}
}

func TestRoom_JoinReturnsAutoApprove(t *testing.T) {
	room := NewRoom("topic")
	room.AutoApprove = true

	cc := &ClientConn{Name: "agent1", Role: "agent", Source: "agent"}
	state, err := room.Join(cc)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if !state.AutoApprove {
		t.Error("expected RoomStateParams.AutoApprove to be true")
	}
}

func TestBroadcast_MentionsMatchParticipantNames(t *testing.T) {
	room := NewRoom("topic")

	alice := &ClientConn{Name: "alice", Role: "user"}
	room.Join(alice)
	bob := &ClientConn{Name: "bob", Role: "agent"}
	room.Join(bob)

	// Text mentions @alice and @bob with punctuation, plus a non-participant @love's.
	msg := room.Broadcast("alice", "human", "human",
		protocol.Content{Type: "text", Text: "hey @bob, I @love's @alice!"},
		nil, // client-supplied mentions ignored
	)

	// Only real participant names should appear.
	if len(msg.Mentions) != 2 {
		t.Fatalf("expected 2 mentions, got %d: %v", len(msg.Mentions), msg.Mentions)
	}
	has := make(map[string]bool)
	for _, m := range msg.Mentions {
		has[m] = true
	}
	if !has["alice"] || !has["bob"] {
		t.Errorf("expected mentions [alice, bob], got %v", msg.Mentions)
	}
}

func TestRoom_JoinAutoApproveDefaultFalse(t *testing.T) {
	room := NewRoom("topic")

	cc := &ClientConn{Name: "agent1", Role: "agent", Source: "agent"}
	state, err := room.Join(cc)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if state.AutoApprove {
		t.Error("expected RoomStateParams.AutoApprove to default to false")
	}
}
