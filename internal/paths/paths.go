package paths

import (
	"os"
	"path/filepath"
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

func (p Paths) RoomDir(roomID string) string {
	return filepath.Join(p.RoomsDir(), roomID)
}

func (p Paths) EnsureRoomDir(roomID string) (string, error) {
	dir := p.RoomDir(roomID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, os.Chmod(dir, 0o700)
}

func (p Paths) ActivePath() string {
	return filepath.Join(p.Root, "active.json")
}
