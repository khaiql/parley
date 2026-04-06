package room

import (
	"github.com/khaiql/parley/internal/command"
	"github.com/khaiql/parley/internal/protocol"
)

// State holds the authoritative room state and emits events when it changes.
// State is NOT safe for concurrent use. All mutations (HandleServerMessage,
// SendMessage, etc.) must happen from a single goroutine. Subscribers receive
// events on buffered channels and can process them concurrently.
type State struct {
	roomID       string
	topic        string
	participants []protocol.Participant
	activities   map[string]Activity
	messages     []protocol.MessageParams
	permissions  []PermissionRequest
	commands     *command.Registry
	cmdCtx       command.Context
	autoApprove  bool
	sendFn       func(string, []string)
	subscribers  []chan Event
}

// New creates a State with empty collections and the given command registry.
func New(registry *command.Registry, ctx command.Context) *State {
	return &State{
		activities: make(map[string]Activity),
		commands:   registry,
		cmdCtx:     ctx,
	}
}

// SetSendFn sets the function used to send messages to the server.
func (s *State) SetSendFn(fn func(string, []string)) {
	s.sendFn = fn
}

// SetAutoApprove sets whether agent tool calls are auto-approved.
func (s *State) SetAutoApprove(v bool) {
	s.autoApprove = v
}

// Participants returns a copy of the participant list.
func (s *State) Participants() []protocol.Participant {
	if len(s.participants) == 0 {
		return nil
	}
	out := make([]protocol.Participant, len(s.participants))
	copy(out, s.participants)
	return out
}

// ParticipantActivity returns the activity for the named participant.
// Returns ActivityIdle if the participant is not found.
func (s *State) ParticipantActivity(name string) Activity {
	act, ok := s.activities[name]
	if !ok {
		return ActivityIdle
	}
	return act
}

// IsAnyoneGenerating reports whether any participant is currently generating.
func (s *State) IsAnyoneGenerating() bool {
	for _, act := range s.activities {
		if act == ActivityGenerating {
			return true
		}
	}
	return false
}

// Messages returns a copy of the message history.
func (s *State) Messages() []protocol.MessageParams {
	if len(s.messages) == 0 {
		return nil
	}
	out := make([]protocol.MessageParams, len(s.messages))
	copy(out, s.messages)
	return out
}

// AvailableCommands delegates to the command registry. Returns nil if no registry.
func (s *State) AvailableCommands() []*command.Command {
	if s.commands == nil {
		return nil
	}
	return s.commands.Commands()
}

// PendingPermissions returns a copy of the pending permission requests.
func (s *State) PendingPermissions() []PermissionRequest {
	if len(s.permissions) == 0 {
		return nil
	}
	out := make([]PermissionRequest, len(s.permissions))
	copy(out, s.permissions)
	return out
}

// AutoApprove reports whether agent tool calls are auto-approved.
func (s *State) AutoApprove() bool {
	return s.autoApprove
}

// GetID returns the room ID.
func (s *State) GetID() string { return s.roomID }

// GetTopic returns the room topic.
func (s *State) GetTopic() string { return s.topic }

// GetParticipants returns a copy of the participant list.
// This satisfies command.RoomQuerier.
func (s *State) GetParticipants() []protocol.Participant { return s.Participants() }

// GetMessageCount returns the number of messages.
func (s *State) GetMessageCount() int { return len(s.messages) }

// SetCommands sets the command registry and context after construction.
func (s *State) SetCommands(reg *command.Registry, ctx command.Context) {
	s.commands = reg
	s.cmdCtx = ctx
}
