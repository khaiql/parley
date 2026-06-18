package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type Paths struct {
	Root string
}

func New(root string) Paths {
	return Paths{Root: root}
}

func DefaultRoot() string {
	if root := os.Getenv("PARLEY_STATE_DIR"); root != "" {
		return root
	}
	for _, root := range defaultRootCandidates() {
		if writableRoot(root) {
			return root
		}
	}
	return filepath.Join(".", ".parley")
}

func defaultRootCandidates() []string {
	var roots []string
	add := func(root string) {
		if root == "" {
			return
		}
		for _, existing := range roots {
			if existing == root {
				return
			}
		}
		roots = append(roots, root)
	}

	home, err := os.UserHomeDir()
	if err == nil {
		add(filepath.Join(home, ".parley"))
	}
	if root := os.Getenv("XDG_RUNTIME_DIR"); root != "" {
		add(filepath.Join(root, "parley"))
	}
	if root := os.Getenv("XDG_STATE_HOME"); root != "" {
		add(filepath.Join(root, "parley"))
	}
	if root, err := os.UserCacheDir(); err == nil {
		add(filepath.Join(root, "parley"))
	}
	if root := os.TempDir(); root != "" {
		add(filepath.Join(root, "parley"))
	}
	if root, err := os.Getwd(); err == nil {
		add(filepath.Join(root, ".parley"))
	}
	return roots
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

func writableRoot(root string) bool {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return false
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return false
	}
	probe, err := os.CreateTemp(root, ".write-test-*")
	if err != nil {
		return false
	}
	name := probe.Name()
	if err := syscall.Flock(int(probe.Fd()), syscall.LOCK_EX); err != nil {
		_ = probe.Close()
		_ = os.Remove(name)
		return false
	}
	if err := syscall.Flock(int(probe.Fd()), syscall.LOCK_UN); err != nil {
		_ = probe.Close()
		_ = os.Remove(name)
		return false
	}
	if err := probe.Close(); err != nil {
		_ = os.Remove(name)
		return false
	}
	return os.Remove(name) == nil
}
