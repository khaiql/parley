package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/khaiql/parley/internal/protocol"
)

// RoomData is the JSON representation of a Room's metadata.
type RoomData struct {
	Topic string `json:"topic"`
	ID    string `json:"id"`
}

// ParticipantData is the JSON representation of a participant.
type ParticipantData struct {
	Name      string `json:"name"`
	Role      string `json:"role"`
	Directory string `json:"directory"`
	Repo      string `json:"repo,omitempty"`
	AgentType string `json:"agent_type,omitempty"`
	Source    string `json:"source"`
}

// RoomDir returns the canonical directory for a room's persisted state.
// It expands to ~/.parley/rooms/<roomID>.
func RoomDir(roomID string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".parley", "rooms", roomID)
}

// SaveRoom saves room state (room.json, messages.json, agents.json) to dir.
// It creates dir if it does not exist.
func SaveRoom(dir string, room *Room) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create room dir: %w", err)
	}

	// Write room.json.
	rd := RoomData{
		Topic: room.Topic,
		ID:    dir,
	}
	if err := writeJSON(filepath.Join(dir, "room.json"), rd); err != nil {
		return fmt.Errorf("write room.json: %w", err)
	}

	// Write messages.json.
	msgs := room.GetMessages()
	if err := writeJSON(filepath.Join(dir, "messages.json"), msgs); err != nil {
		return fmt.Errorf("write messages.json: %w", err)
	}

	// Write agents.json (participants snapshot).
	participants := room.GetParticipants()
	pdata := make([]ParticipantData, 0, len(participants))
	for _, cc := range participants {
		pdata = append(pdata, ParticipantData{
			Name:      cc.Name,
			Role:      cc.Role,
			Directory: cc.Directory,
			Repo:      cc.Repo,
			AgentType: cc.AgentType,
			Source:    cc.Source,
		})
	}
	if err := writeJSON(filepath.Join(dir, "agents.json"), pdata); err != nil {
		return fmt.Errorf("write agents.json: %w", err)
	}

	return nil
}

// LoadRoom loads a Room from a previously saved directory.
// It restores the topic and message history. Participants are not restored
// (the room starts empty).
func LoadRoom(dir string) (*Room, error) {
	var rd RoomData
	if err := readJSON(filepath.Join(dir, "room.json"), &rd); err != nil {
		return nil, fmt.Errorf("read room.json: %w", err)
	}

	room := NewRoom(rd.Topic)

	var msgs []protocol.MessageParams
	if err := readJSON(filepath.Join(dir, "messages.json"), &msgs); err != nil {
		return nil, fmt.Errorf("read messages.json: %w", err)
	}
	room.Messages = msgs
	if len(msgs) > 0 {
		room.seq = msgs[len(msgs)-1].Seq
	}

	return room, nil
}

// writeJSON marshals v with indentation and writes it to path.
func writeJSON(path string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// readJSON reads path and unmarshals it into v.
func readJSON(path string, v interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
