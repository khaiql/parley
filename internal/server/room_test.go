package server

import (
	"testing"
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
