package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Paths struct {
	Root string
}

func New(root string) Paths {
	return Paths{Root: root}
}

func DefaultRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, ".parley")
}

func (p Paths) RoomsDir() string {
	return filepath.Join(p.Root, "rooms")
}

func ValidateRoomID(roomID string) error {
	if roomID == "" {
		return fmt.Errorf("room id is required")
	}
	if roomID == "." || roomID == ".." {
		return fmt.Errorf("room id must be a safe path segment")
	}
	if strings.ContainsAny(roomID, `/\`) {
		return fmt.Errorf("room id must not contain path separators")
	}
	if filepath.Clean(roomID) != roomID {
		return fmt.Errorf("room id must be a clean path segment")
	}
	return nil
}

func (p Paths) RoomDir(roomID string) (string, error) {
	if err := ValidateRoomID(roomID); err != nil {
		return "", err
	}
	roomsDir := filepath.Clean(p.RoomsDir())
	dir := filepath.Clean(filepath.Join(roomsDir, roomID))
	rel, err := filepath.Rel(roomsDir, dir)
	if err != nil {
		return "", err
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("room id escapes rooms directory")
	}
	return dir, nil
}

func (p Paths) EnsureRoomDir(roomID string) (string, error) {
	dir, err := p.RoomDir(roomID)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, os.Chmod(dir, 0o700)
}

func (p Paths) ActivePath() string {
	return filepath.Join(p.Root, "active.json")
}
