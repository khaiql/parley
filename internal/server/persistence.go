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
	Topic       string `json:"topic"`
	ID          string `json:"id"`
	AutoApprove bool   `json:"auto_approve,omitempty"`
	Debug       bool   `json:"debug,omitempty"`
}

// ParticipantData is the JSON representation of a participant.
type ParticipantData struct {
	Name      string `json:"name"`
	Role      string `json:"role"`
	Directory string `json:"directory"`
	Repo      string `json:"repo,omitempty"`
	AgentType string `json:"agent_type,omitempty"`
	Source    string `json:"source"`
	SessionID string `json:"session_id,omitempty"`
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
		Topic:       room.Topic,
		ID:          room.ID,
		AutoApprove: room.AutoApprove,
		Debug:       room.Debug,
	}
	if err := writeJSON(filepath.Join(dir, "room.json"), rd); err != nil {
		return fmt.Errorf("write room.json: %w", err)
	}

	// Write messages.json.
	msgs := room.GetMessages()
	if err := writeJSON(filepath.Join(dir, "messages.json"), msgs); err != nil {
		return fmt.Errorf("write messages.json: %w", err)
	}

	// Write agents.json — all participants (online + offline), preserving
	// session IDs that were previously saved by agent processes.
	existing, _ := LoadAgents(dir)
	sessionIDs := make(map[string]string)
	for _, a := range existing {
		if a.SessionID != "" {
			sessionIDs[a.Name] = a.SessionID
		}
	}

	participants := room.GetParticipants()
	pdata := make([]ParticipantData, 0, len(participants))
	for _, cc := range participants {
		pd := ParticipantData{
			Name:      cc.Name,
			Role:      cc.Role,
			Directory: cc.Directory,
			Repo:      cc.Repo,
			AgentType: cc.AgentType,
			Source:    cc.Source,
		}
		if sid, ok := sessionIDs[cc.Name]; ok {
			pd.SessionID = sid
		}
		pdata = append(pdata, pd)
	}

	if err := writeJSON(filepath.Join(dir, "agents.json"), pdata); err != nil {
		return fmt.Errorf("write agents.json: %w", err)
	}

	return nil
}

// LoadRoom loads a Room from a previously saved directory.
// It restores the topic, message history, and all participants as offline.
func LoadRoom(dir string) (*Room, error) {
	var rd RoomData
	if err := readJSON(filepath.Join(dir, "room.json"), &rd); err != nil {
		return nil, fmt.Errorf("read room.json: %w", err)
	}

	room := NewRoom(rd.Topic)
	room.ID = rd.ID
	room.AutoApprove = rd.AutoApprove
	room.Debug = rd.Debug

	var msgs []protocol.MessageParams
	if err := readJSON(filepath.Join(dir, "messages.json"), &msgs); err != nil {
		return nil, fmt.Errorf("read messages.json: %w", err)
	}
	room.Messages = msgs
	if len(msgs) > 0 {
		room.seq = msgs[len(msgs)-1].Seq
	}

	// Restore all saved agents as offline participants.
	agents, _ := LoadAgents(dir)
	for _, a := range agents {
		room.Participants[a.Name] = &ClientConn{
			Name:      a.Name,
			Role:      a.Role,
			Directory: a.Directory,
			Repo:      a.Repo,
			AgentType: a.AgentType,
			Source:    a.Source,
			Online:    false,
		}
	}

	return room, nil
}

// AgentsFile is the canonical filename for agent session data within a room dir.
const AgentsFile = "agents.json"

// SaveAgents writes agent participant data (including session IDs) to
// <dir>/agents.json. It creates dir if it does not exist.
func SaveAgents(dir string, agents []ParticipantData) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create room dir: %w", err)
	}
	return writeJSON(filepath.Join(dir, AgentsFile), agents)
}

// LoadAgents reads <dir>/agents.json and returns the slice of ParticipantData.
// Returns nil (no error) if the file does not exist.
func LoadAgents(dir string) ([]ParticipantData, error) {
	path := filepath.Join(dir, AgentsFile)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, nil
	}
	var agents []ParticipantData
	if err := readJSON(path, &agents); err != nil {
		return nil, fmt.Errorf("read agents.json: %w", err)
	}
	return agents, nil
}

// FindAgentSessionID returns the session_id stored for the named agent in
// <dir>/agents.json. Returns empty string if the agent is not found or the
// file does not exist.
func FindAgentSessionID(dir, name string) (string, error) {
	agents, err := LoadAgents(dir)
	if err != nil {
		return "", err
	}
	for _, a := range agents {
		if a.Name == name {
			return a.SessionID, nil
		}
	}
	return "", nil
}

// UpdateAgentSessionID updates the session_id for the named agent in
// <dir>/agents.json. If the agent is not already present, it is appended.
// If the file does not exist, a new one is created.
func UpdateAgentSessionID(dir, name, sessionID string) error {
	agents, err := LoadAgents(dir)
	if err != nil {
		return err
	}
	found := false
	for i, a := range agents {
		if a.Name == name {
			agents[i].SessionID = sessionID
			found = true
			break
		}
	}
	if !found {
		agents = append(agents, ParticipantData{Name: name, SessionID: sessionID})
	}
	return SaveAgents(dir, agents)
}

// writeJSON marshals v with indentation and writes it to path.
func writeJSON(path string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// readJSON reads path and unmarshals it into v.
func readJSON(path string, v interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
