package model

import (
	"time"

	"github.com/khaiql/parley/internal/artifact"
)

type EventType string

const (
	EventRoomStarted       EventType = "room.started"
	EventRoomStopped       EventType = "room.stopped"
	EventParticipantJoined EventType = "participant.joined"
	EventParticipantLeft   EventType = "participant.left"
	EventMessage           EventType = "message"
)

func (t EventType) IsTranscript() bool {
	switch t {
	case EventRoomStarted, EventRoomStopped, EventParticipantJoined, EventParticipantLeft, EventMessage:
		return true
	default:
		return false
	}
}

type Event struct {
	Seq       int64       `json:"seq"`
	Type      EventType   `json:"type"`
	Timestamp time.Time   `json:"timestamp"`
	RoomID    string      `json:"room_id"`
	Actor     string      `json:"actor"`
	Payload   interface{} `json:"payload,omitempty"`
}

type Participant struct {
	Name      string `json:"name"`
	Role      string `json:"role"`
	Directory string `json:"directory,omitempty"`
	Repo      string `json:"repo,omitempty"`
	Online    bool   `json:"online"`
}

type RoomMetadata struct {
	RoomID            string          `json:"room_id"`
	Topic             string          `json:"topic"`
	LocalHost         string          `json:"local_host,omitempty"`
	LocalPort         int             `json:"local_port,omitempty"`
	ArtifactLocalPort int             `json:"artifact_local_port,omitempty"`
	ArtifactPath      string          `json:"artifact_path,omitempty"`
	ArtifactLimits    artifact.Limits `json:"artifact_limits,omitempty"`
}

type MessagePayload struct {
	Text      string             `json:"text"`
	Mentions  []string           `json:"mentions,omitempty"`
	Artifacts []ArtifactMetadata `json:"artifacts,omitempty"`
}

type ArtifactMetadata struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type ParticipantPayload struct {
	Role      string `json:"role"`
	Directory string `json:"directory,omitempty"`
	Repo      string `json:"repo,omitempty"`
}

type RoomStoppedPayload struct {
	Reason string `json:"reason"`
}
