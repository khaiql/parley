package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/khaiql/parley/internal/adapter"
	"github.com/khaiql/parley/internal/descriptor"
	"github.com/khaiql/parley/internal/paths"
)

type RoomRuntime struct {
	RoomID    string `json:"room_id"`
	Topic     string `json:"topic,omitempty"`
	LocalHost string `json:"local_host"`
	LocalPort int    `json:"local_port"`
	ServerPID int    `json:"server_pid,omitempty"`
}

type InviteResponse struct {
	RoomID              string `json:"room_id"`
	Descriptor          string `json:"descriptor"`
	LocalHost           string `json:"local_host"`
	LocalPort           int    `json:"local_port"`
	JoinCommandTemplate string `json:"join_command_template"`
	AgentInstruction    string `json:"agent_instruction"`
}

type ActiveParticipation struct {
	RoomID string `json:"room_id"`
	Name   string `json:"name,omitempty"`
}

func SaveRoomRuntime(p paths.Paths, meta RoomRuntime) error {
	if meta.RoomID == "" {
		return fmt.Errorf("room id is required")
	}
	dir, err := p.EnsureRoomDir(meta.RoomID)
	if err != nil {
		return err
	}
	return writeJSONFile(filepath.Join(dir, "runtime.json"), meta)
}

func LoadRoomRuntime(p paths.Paths, roomID string) (RoomRuntime, error) {
	roomDir, err := p.RoomDir(roomID)
	if err != nil {
		return RoomRuntime{}, err
	}
	data, err := os.ReadFile(filepath.Join(roomDir, "runtime.json"))
	if err != nil {
		return RoomRuntime{}, err
	}
	var meta RoomRuntime
	if err := json.Unmarshal(data, &meta); err != nil {
		return RoomRuntime{}, err
	}
	return meta, nil
}

func Invite(p paths.Paths, roomID string) (InviteResponse, error) {
	meta, err := LoadRoomRuntime(p, roomID)
	if err != nil {
		return InviteResponse{}, err
	}
	desc := descriptor.Descriptor{
		Host:   meta.LocalHost,
		Port:   meta.LocalPort,
		RoomID: meta.RoomID,
	}.String()
	return InviteResponse{
		RoomID:              meta.RoomID,
		Descriptor:          desc,
		LocalHost:           meta.LocalHost,
		LocalPort:           meta.LocalPort,
		JoinCommandTemplate: fmt.Sprintf("parley join %q --name <participant-name> --role <participant-role>", desc),
		AgentInstruction:    fmt.Sprintf("Use your Parley skill to join this room: %s", desc),
	}, nil
}

func SaveActive(p paths.Paths, active ActiveParticipation) error {
	if active.RoomID == "" {
		return fmt.Errorf("room id is required")
	}
	if err := paths.ValidateRoomID(active.RoomID); err != nil {
		return err
	}
	if active.Name != "" {
		if err := validatePathSegment(active.Name, "participant name"); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(p.Root, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(p.Root, 0o700); err != nil {
		return err
	}
	return writeJSONFile(p.ActivePath(), active)
}

func LoadActive(p paths.Paths) (ActiveParticipation, error) {
	data, err := os.ReadFile(p.ActivePath())
	if err != nil {
		return ActiveParticipation{}, err
	}
	var active ActiveParticipation
	if err := json.Unmarshal(data, &active); err != nil {
		return ActiveParticipation{}, err
	}
	if active.RoomID == "" {
		return ActiveParticipation{}, fmt.Errorf("active room id is required")
	}
	if err := paths.ValidateRoomID(active.RoomID); err != nil {
		return ActiveParticipation{}, err
	}
	if active.Name != "" {
		if err := validatePathSegment(active.Name, "participant name"); err != nil {
			return ActiveParticipation{}, err
		}
	}
	return active, nil
}

func RoomRuntimePath(p paths.Paths, roomID string) string {
	return filepath.Join(p.RoomsDir(), roomID, "runtime.json")
}

func RoomEventsPath(p paths.Paths, roomID string) string {
	return filepath.Join(p.RoomsDir(), roomID, "events.jsonl")
}

func ParticipantMetaPath(p paths.Paths, roomID, name string) string {
	return filepath.Join(p.RoomsDir(), roomID, "participants", name+".json")
}

func ParticipantEventsPath(p paths.Paths, roomID, name string) string {
	return filepath.Join(p.RoomsDir(), roomID, "participants", name+".events.jsonl")
}

func ParticipantSocketPath(p paths.Paths, roomID, name string) string {
	return filepath.Join(p.RoomsDir(), roomID, "participants", name+".sock")
}

func ServerSocketPath(p paths.Paths, roomID string) string {
	return filepath.Join(p.RoomsDir(), roomID, "server.sock")
}

func ParticipantStore(p paths.Paths, roomID, name string) (*adapter.Store, error) {
	if err := validateRoomAndName(roomID, name); err != nil {
		return nil, err
	}
	return adapter.NewStore(ParticipantMetaPath(p, roomID, name), ParticipantEventsPath(p, roomID, name)), nil
}

func validateRoomAndName(roomID, name string) error {
	if err := paths.ValidateRoomID(roomID); err != nil {
		return err
	}
	return validatePathSegment(name, "participant name")
}

func validatePathSegment(value, label string) error {
	if value == "" {
		return fmt.Errorf("%s is required", label)
	}
	if value == "." || value == ".." {
		return fmt.Errorf("%s must be a safe path segment", label)
	}
	if filepath.Clean(value) != value {
		return fmt.Errorf("%s must be a clean path segment", label)
	}
	if filepath.Base(value) != value {
		return fmt.Errorf("%s must not contain path separators", label)
	}
	if strings.ContainsAny(value, `/\`) {
		return fmt.Errorf("%s must not contain path separators", label)
	}
	return nil
}

func writeJSONFile(path string, value interface{}) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".runtime-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
