package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultRootUsesParleyStateDirOverride(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	t.Setenv("PARLEY_STATE_DIR", root)

	if got := DefaultRoot(); got != root {
		t.Fatalf("DefaultRoot() = %q, want %q", got, root)
	}
}

func TestDefaultRootUsesXDGRuntimeDirWhenHomeIsNotWritable(t *testing.T) {
	base := t.TempDir()
	homeFile := filepath.Join(base, "home-file")
	if err := os.WriteFile(homeFile, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write home file: %v", err)
	}
	runtimeDir := filepath.Join(base, "runtime")
	if err := os.Mkdir(runtimeDir, 0o700); err != nil {
		t.Fatalf("mkdir runtime dir: %v", err)
	}
	t.Setenv("HOME", homeFile)
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	t.Setenv("PARLEY_STATE_DIR", "")

	want := filepath.Join(runtimeDir, "parley")
	if got := DefaultRoot(); got != want {
		t.Fatalf("DefaultRoot() = %q, want %q", got, want)
	}
}

func TestDefaultRootFallsBackToTempWhenHomeIsNotWritable(t *testing.T) {
	base := t.TempDir()
	homeFile := filepath.Join(base, "home-file")
	if err := os.WriteFile(homeFile, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write home file: %v", err)
	}
	tmp := filepath.Join(base, "tmp")
	if err := os.Mkdir(tmp, 0o700); err != nil {
		t.Fatalf("mkdir tmp: %v", err)
	}
	t.Setenv("HOME", homeFile)
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("TMPDIR", tmp)
	t.Setenv("PARLEY_STATE_DIR", "")

	want := filepath.Join(tmp, "parley")
	if got := DefaultRoot(); got != want {
		t.Fatalf("DefaultRoot() = %q, want %q", got, want)
	}
}

func TestDefaultRootFallsBackToWorkingDirectoryWhenHomeAndTempAreNotWritable(t *testing.T) {
	base := t.TempDir()
	homeFile := filepath.Join(base, "home-file")
	if err := os.WriteFile(homeFile, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write home file: %v", err)
	}
	tempFile := filepath.Join(base, "temp-file")
	if err := os.WriteFile(tempFile, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	workspace := filepath.Join(base, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("chdir workspace: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalWD); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})
	t.Setenv("HOME", homeFile)
	t.Setenv("XDG_RUNTIME_DIR", tempFile)
	t.Setenv("XDG_STATE_HOME", tempFile)
	t.Setenv("TMPDIR", tempFile)
	t.Setenv("PARLEY_STATE_DIR", "")

	currentWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("get current cwd: %v", err)
	}
	want := filepath.Join(currentWD, ".parley")
	if got := DefaultRoot(); got != want {
		t.Fatalf("DefaultRoot() = %q, want %q", got, want)
	}
}

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
