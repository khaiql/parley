package herdr

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
)

type Machine struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Target  string `json:"target"`
	Session string `json:"session"`
	Enabled bool   `json:"enabled"`
}
type AgentTarget struct {
	MachineID     string `json:"machine_id"`
	HerdrSocket   string `json:"herdr_socket"`
	WorkspaceID   string `json:"workspace_id"`
	PaneID        string `json:"pane_id"`
	TerminalID    string `json:"terminal_id"`
	Kind          string `json:"kind"`
	NativeSession string `json:"native_session"`
}
type Endpoints struct {
	Descriptor  string `json:"descriptor"`
	ArtifactURL string `json:"artifact_url"`
}
type Room struct {
	ID            string      `json:"id"`
	Topic         string      `json:"topic"`
	Host          AgentTarget `json:"host"`
	HostSessionID string      `json:"host_session_id"`
	Source        Endpoints   `json:"source"`
	Closed        bool        `json:"closed"`
}
type Binding struct {
	RoomHost        *RoomHostRef `json:"room_host,omitempty"`
	Topic           string       `json:"topic,omitempty"`
	RoomID          string       `json:"room_id"`
	Target          AgentTarget  `json:"target"`
	SessionID       string       `json:"session_id"`
	ParticipantName string       `json:"participant_name"`
	StateDir        string       `json:"state_dir"`
	Routed          Endpoints    `json:"routed"`
	Status          string       `json:"status"`
	JoinPhase       string       `json:"join_phase"`
	JoinRequestID   string       `json:"join_request_id"`
}
type SessionConfig struct {
	Role         string `json:"role"`
	ControllerID string `json:"controller_id"`
	MachineID    string `json:"machine_id"`
	HerdrSocket  string `json:"herdr_socket"`
	SessionKey   string `json:"session_key"`
}
type Peer struct {
	Machine Machine       `json:"machine"`
	Config  SessionConfig `json:"config"`
	Binary  string        `json:"binary"`
}
type Request struct {
	ID        string       `json:"id"`
	Op        string       `json:"op"`
	RoomID    string       `json:"room_id,omitempty"`
	Topic     string       `json:"topic,omitempty"`
	Target    AgentTarget  `json:"target"`
	RoomHost  *RoomHostRef `json:"room_host,omitempty"`
	RoomTopic string       `json:"room_topic,omitempty"`
}

type RoomHostRef struct {
	HostSessionID string `json:"host_session_id"`
	MachineID     string `json:"machine_id"`
	SessionKey    string `json:"session_key"`
}

func RoomHostReference(room Room, localMachine string) RoomHostRef {
	id := room.Host.MachineID
	if id == "self" {
		id = localMachine
	}
	key, _ := SessionKey(room.Host.HerdrSocket)
	return RoomHostRef{MachineID: id, SessionKey: key, HostSessionID: room.HostSessionID}
}

type Response struct {
	Catalog *RoomCatalog   `json:"catalog,omitempty"`
	Version int            `json:"version,omitempty"`
	Status  string         `json:"status"`
	Code    string         `json:"code,omitempty"`
	Message string         `json:"message,omitempty"`
	Room    *Room          `json:"room,omitempty"`
	Binding *Binding       `json:"binding,omitempty"`
	Handoff *HandoffResult `json:"handoff,omitempty"`
}
type Outcome struct {
	Digest       string   `json:"digest"`
	ControllerID string   `json:"controller_id"`
	Phase        string   `json:"phase"`
	Response     Response `json:"response"`
}
type State struct {
	ControllerID string             `json:"controller_id"`
	Peers        map[string]Peer    `json:"peers"`
	Rooms        map[string]Room    `json:"rooms"`
	Bindings     map[string]Binding `json:"bindings"`
	Requests     map[string]Outcome `json:"requests"`
}

// SessionKey accepts a socket canonicalized by the trusted caller, including EvalSymlinks.
func SessionKey(canonicalSocket string) (string, error) {
	if !filepath.IsAbs(canonicalSocket) || canonicalSocket != filepath.Clean(canonicalSocket) {
		return "", fmt.Errorf("session socket must be canonical and absolute")
	}
	return hashBytes([]byte(canonicalSocket)), nil
}
func SessionDirs(configRoot, stateRoot, canonicalSocket string) (string, string, error) {
	k, err := SessionKey(canonicalSocket)
	if err != nil {
		return "", "", err
	}
	return filepath.Join(configRoot, "sessions", k), filepath.Join(stateRoot, "sessions", k), nil
}

// HostBindingKey accepts a validated target; host identity excludes machine/profile aliases.
func HostBindingKey(roomID string, target AgentTarget) string {
	k, _ := SessionKey(target.HerdrSocket)
	b, _ := json.Marshal([5]string{k, roomID, target.TerminalID, target.Kind, target.NativeSession})
	return hashBytes(b)
}

func terminalBindingKey(roomID string, target AgentTarget) string {
	target.NativeSession = ""
	return HostBindingKey(roomID, target)
}

// NativeSession remains part of old storage keys, but does not identify participation.
func SameTerminal(a, b AgentTarget) bool {
	return a.HerdrSocket == b.HerdrSocket && a.TerminalID == b.TerminalID && a.Kind == b.Kind
}

// RequestDigest must be called after DecodeRequest normalizes the trusted origin.
func RequestDigest(req Request) string { b, _ := json.Marshal(req); return hashBytes(b) }
func hashBytes(b []byte) string        { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
