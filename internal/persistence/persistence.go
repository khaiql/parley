package persistence

import "github.com/khaiql/parley/internal/protocol"

// Store is the interface for persisting and loading room state.
type Store interface {
	Save(snapshot protocol.RoomSnapshot) error
	Load(roomID string) (protocol.RoomSnapshot, error)
	SaveAgentSession(roomID, agentName, sessionID string) error
	FindAgentSession(roomID, agentName string) (string, error)
}
