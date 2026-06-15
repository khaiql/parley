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
