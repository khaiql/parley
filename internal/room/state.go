package room

import (
	"github.com/khaiql/parley/internal/command"
	"github.com/khaiql/parley/internal/protocol"
)

// State holds the authoritative room state and emits events when it changes.
type State struct {
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
// Returns ActivityListening if the participant is not found.
func (s *State) ParticipantActivity(name string) Activity {
	act, ok := s.activities[name]
	if !ok {
		return ActivityListening
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
