package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureRoomDirPermissions(t *testing.T) {
	root := t.TempDir()
	p := New(root)
	dir, err := p.EnsureRoomDir("room-1")
	if err != nil {
		t.Fatalf("EnsureRoomDir: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("mode = %o, want 700", got)
	}
	if filepath.Base(dir) != "room-1" {
		t.Fatalf("dir = %s", dir)
	}
}

func TestRoomDirRejectsUnsafeRoomIDs(t *testing.T) {
	root := t.TempDir()
	p := New(root)
	tests := []string{"", ".", "..", "../x", "a/b", "a\\b"}

	for _, roomID := range tests {
		if _, err := p.RoomDir(roomID); err == nil {
			t.Errorf("RoomDir(%q) error = nil, want error", roomID)
		}
	}
}

func TestRoomDirRejectsEncodedUnsafeRoomIDs(t *testing.T) {
	root := t.TempDir()
	p := New(root)
	tests := []string{"%2e%2e", "%2f", "%5c", "a%2Fb"}

	for _, roomID := range tests {
		if err := ValidateRoomID(roomID); err == nil {
			t.Errorf("ValidateRoomID(%q) error = nil, want error", roomID)
		}
		if _, err := p.RoomDir(roomID); err == nil {
			t.Errorf("RoomDir(%q) error = nil, want error", roomID)
		}
	}
}

func TestRoomDirAllowsSafeSingleSegmentRoomIDs(t *testing.T) {
	root := t.TempDir()
	p := New(root)
	tests := []string{"room-1", "room_1", "room.1", "100%done"}

	for _, roomID := range tests {
		if err := ValidateRoomID(roomID); err != nil {
			t.Errorf("ValidateRoomID(%q) error = %v, want nil", roomID, err)
		}
		if _, err := p.RoomDir(roomID); err != nil {
			t.Errorf("RoomDir(%q) error = %v, want nil", roomID, err)
		}
	}
}

func TestEnsureRoomDirRejectsUnsafeRoomIDs(t *testing.T) {
	root := t.TempDir()
	p := New(root)
	tests := []string{"", ".", "..", "../x", "a/b", "a\\b"}

	for _, roomID := range tests {
		if _, err := p.EnsureRoomDir(roomID); err == nil {
			t.Errorf("EnsureRoomDir(%q) error = nil, want error", roomID)
		}
	}
}
