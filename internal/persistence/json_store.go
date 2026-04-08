package persistence

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/khaiql/parley/internal/protocol"
)

// Compile-time interface check.
var _ Store = (*JSONStore)(nil)

// JSONStore persists room state as JSON files on disk.
type JSONStore struct {
	basePath string
}

// NewJSONStore creates a new JSONStore rooted at basePath.
// The basePath is the parent directory that will contain per-room subdirectories.
func NewJSONStore(basePath string) *JSONStore {
	return &JSONStore{basePath: basePath}
}

// RoomDir returns the directory for a room's persisted state.
// Exported for callers that need the path.
func (s *JSONStore) RoomDir(roomID string) string {
	return filepath.Join(s.basePath, roomID)
}

// Internal JSON representations (file format matches the original server persistence).

type roomData struct {
	Topic       string `json:"topic"`
	ID          string `json:"id"`
	AutoApprove bool   `json:"auto_approve,omitempty"`
}

type agentData struct {
	Name      string `json:"name"`
	Role      string `json:"role"`
	Color     string `json:"color,omitempty"`
	Directory string `json:"directory"`
	Repo      string `json:"repo,omitempty"`
	AgentType string `json:"agent_type,omitempty"`
	Source    string `json:"source"`
	SessionID string `json:"session_id,omitempty"`
}

// Save writes room.json, messages.json, and agents.json for the given snapshot.
// It preserves existing session IDs from agents.json when re-saving.
func (s *JSONStore) Save(snapshot protocol.RoomSnapshot) error {
	dir := s.RoomDir(snapshot.RoomID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create room dir: %w", err)
	}

	// Write room.json.
	rd := roomData{
		Topic:       snapshot.Topic,
		ID:          snapshot.RoomID,
		AutoApprove: snapshot.AutoApprove,
	}
	if err := writeJSON(filepath.Join(dir, "room.json"), rd); err != nil {
		return fmt.Errorf("write room.json: %w", err)
	}

	// Write messages.json.
	if err := writeJSON(filepath.Join(dir, "messages.json"), snapshot.Messages); err != nil {
		return fmt.Errorf("write messages.json: %w", err)
	}

	// Write agents.json — preserve existing session IDs.
	existing, _ := loadAgents(dir)
	sessionIDs := make(map[string]string)
	for _, a := range existing {
		if a.SessionID != "" {
			sessionIDs[a.Name] = a.SessionID
		}
	}

	agents := make([]agentData, 0, len(snapshot.Participants))
	for _, p := range snapshot.Participants {
		ad := agentData{
			Name:      p.Name,
			Role:      p.Role,
			Color:     p.Color,
			Directory: p.Directory,
			Repo:      p.Repo,
			AgentType: p.AgentType,
			Source:    p.Source,
		}
		if sid, ok := sessionIDs[p.Name]; ok {
			ad.SessionID = sid
		}
		agents = append(agents, ad)
	}

	if err := writeJSON(filepath.Join(dir, "agents.json"), agents); err != nil {
		return fmt.Errorf("write agents.json: %w", err)
	}

	return nil
}

// Load reads room state from disk and returns a RoomSnapshot.
// All participants are marked Online: false.
func (s *JSONStore) Load(roomID string) (protocol.RoomSnapshot, error) {
	dir := s.RoomDir(roomID)

	var rd roomData
	if err := readJSON(filepath.Join(dir, "room.json"), &rd); err != nil {
		return protocol.RoomSnapshot{}, fmt.Errorf("read room.json: %w", err)
	}

	var msgs []protocol.MessageParams
	if err := readJSON(filepath.Join(dir, "messages.json"), &msgs); err != nil {
		return protocol.RoomSnapshot{}, fmt.Errorf("read messages.json: %w", err)
	}

	agents, _ := loadAgents(dir)
	participants := make([]protocol.Participant, 0, len(agents))
	for _, a := range agents {
		participants = append(participants, protocol.Participant{
			Name:      a.Name,
			Role:      a.Role,
			Color:     a.Color,
			Directory: a.Directory,
			Repo:      a.Repo,
			AgentType: a.AgentType,
			Source:    a.Source,
			Online:    false,
		})
	}

	return protocol.RoomSnapshot{
		RoomID:       rd.ID,
		Topic:        rd.Topic,
		AutoApprove:  rd.AutoApprove,
		Participants: participants,
		Messages:     msgs,
	}, nil
}

// SaveAgentSession updates the session_id for the named agent in agents.json.
// If the agent is not already present, it is appended.
func (s *JSONStore) SaveAgentSession(roomID, agentName, sessionID string) error {
	dir := s.RoomDir(roomID)
	agents, err := loadAgents(dir)
	if err != nil {
		return err
	}
	found := false
	for i, a := range agents {
		if a.Name == agentName {
			agents[i].SessionID = sessionID
			found = true
			break
		}
	}
	if !found {
		agents = append(agents, agentData{Name: agentName, SessionID: sessionID})
	}
	return saveAgents(dir, agents)
}

// FindAgentSession returns the session_id for the named agent.
// Returns empty string if the agent is not found or the file does not exist.
func (s *JSONStore) FindAgentSession(roomID, agentName string) (string, error) {
	dir := s.RoomDir(roomID)
	agents, err := loadAgents(dir)
	if err != nil {
		return "", err
	}
	for _, a := range agents {
		if a.Name == agentName {
			return a.SessionID, nil
		}
	}
	return "", nil
}

// --- internal helpers ---

func loadAgents(dir string) ([]agentData, error) {
	path := filepath.Join(dir, "agents.json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, nil
	}
	var agents []agentData
	if err := readJSON(path, &agents); err != nil {
		return nil, fmt.Errorf("read agents.json: %w", err)
	}
	return agents, nil
}

func saveAgents(dir string, agents []agentData) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create room dir: %w", err)
	}
	return writeJSON(filepath.Join(dir, "agents.json"), agents)
}

func writeJSON(path string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func readJSON(path string, v interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
