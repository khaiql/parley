package server

import (
	"regexp"
	"testing"
)

var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestNewUUIDFormat(t *testing.T) {
	id := newUUID()
	if !uuidRE.MatchString(id) {
		t.Errorf("newUUID() = %q, does not match UUID v4 format", id)
	}
}

func TestNewUUIDNoDuplicates(t *testing.T) {
	seen := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		id := newUUID()
		if seen[id] {
			t.Fatalf("newUUID() produced a duplicate after %d calls: %q", i, id)
		}
		seen[id] = true
	}
}

func TestNewRoomSetsID(t *testing.T) {
	room := NewRoom("test-topic")
	if room.ID == "" {
		t.Error("NewRoom() did not set a non-empty ID")
	}
	if !uuidRE.MatchString(room.ID) {
		t.Errorf("NewRoom().ID = %q, does not match UUID v4 format", room.ID)
	}
}

func TestNewRoomIDsAreUnique(t *testing.T) {
	ids := make(map[string]bool, 100)
	for i := 0; i < 100; i++ {
		room := NewRoom("topic")
		if ids[room.ID] {
			t.Fatalf("NewRoom() produced a duplicate ID: %q", room.ID)
		}
		ids[room.ID] = true
	}
}
