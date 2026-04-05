// Package room provides the business logic layer for a Parley chat room,
// decoupled from the TUI and network layers.
package room

import (
	"log"

	"github.com/khaiql/parley/internal/protocol"
)

// Event is the marker interface for all room events.
type Event interface{}

// Activity represents what a participant is currently doing.
type Activity int

const (
	ActivityListening  Activity = iota // idle / listening
	ActivityThinking                   // processing input
	ActivityGenerating                 // producing output
	ActivityUsingTool                  // executing a tool
)

// ---- Event types -----------------------------------------------------------

// ParticipantsChanged is emitted when the participant list changes.
type ParticipantsChanged struct {
	Participants []protocol.Participant
}

// MessageReceived is emitted when a new chat message arrives.
type MessageReceived struct {
	Message protocol.MessageParams
}

// HistoryLoaded is emitted when the full room state is received on join.
type HistoryLoaded struct {
	Messages     []protocol.MessageParams
	Participants []protocol.Participant
	Activities   map[string]Activity
}

// ParticipantActivityChanged is emitted when a participant's activity changes.
type ParticipantActivityChanged struct {
	Name     string
	Activity Activity
}

// PermissionRequest represents a pending permission request from an agent.
type PermissionRequest struct {
	ID        string
	AgentName string
	Tool      string
	Args      string
}

// PermissionRequested is emitted when an agent asks for permission.
type PermissionRequested struct {
	Request PermissionRequest
}

// PermissionResolved is emitted when a permission request is approved or denied.
type PermissionResolved struct {
	RequestID string
	Approved  bool
}

// ErrorOccurred is emitted when an error occurs in the room layer.
type ErrorOccurred struct {
	Error error
}

// ---- Pub/Sub ---------------------------------------------------------------

const subscriberBufferSize = 64

// Subscribe returns a channel that receives all events emitted by this State.
// The channel is buffered; events are dropped (with a warning log) if the
// subscriber falls behind.
func (s *State) Subscribe() <-chan Event {
	ch := make(chan Event, subscriberBufferSize)
	s.subscribers = append(s.subscribers, ch)
	return ch
}

// emit sends an event to all subscribers. It never blocks — if a subscriber's
// channel is full the event is dropped and a warning is logged.
func (s *State) emit(evt Event) {
	for _, ch := range s.subscribers {
		select {
		case ch <- evt:
		default:
			log.Printf("room: dropped event %T for slow subscriber", evt)
		}
	}
}
