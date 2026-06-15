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
	if err := validateRoomIDSegment(roomID); err != nil {
		return err
	}
	decoded := decodePercentEscapes(roomID)
	if decoded != roomID {
		if err := validateRoomIDSegment(decoded); err != nil {
			return fmt.Errorf("room id contains encoded unsafe path segment: %w", err)
		}
	}
	return nil
}

func validateRoomIDSegment(roomID string) error {
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

func decodePercentEscapes(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}

	var decoded strings.Builder
	decoded.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			hi, okHi := fromHex(s[i+1])
			lo, okLo := fromHex(s[i+2])
			if okHi && okLo {
				decoded.WriteByte(hi<<4 | lo)
				i += 2
				continue
			}
		}
		decoded.WriteByte(s[i])
	}
	return decoded.String()
}

func fromHex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
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
