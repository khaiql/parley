package room

import (
	"crypto/rand"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/khaiql/parley/internal/command"
	"github.com/khaiql/parley/internal/protocol"
)

var msgCounter uint64

// newUUID generates a random UUID v4.
func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// generateID returns a unique message ID using an atomic counter.
func generateID() string {
	return fmt.Sprintf("msg-%d", atomic.AddUint64(&msgCounter, 1))
}

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
	seq          int
}

// New creates a State with empty collections and the given command registry.
func New(registry *command.Registry, ctx command.Context) *State {
	return &State{
		roomID:     newUUID(),
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

// Restore sets all fields from persisted data and sets seq to the highest
// Seq value found in the provided messages.
func (s *State) Restore(roomID, topic string, participants []protocol.Participant, messages []protocol.MessageParams, autoApprove bool) {
	s.roomID = roomID
	s.topic = topic
	s.participants = make([]protocol.Participant, len(participants))
	copy(s.participants, participants)
	s.messages = make([]protocol.MessageParams, len(messages))
	copy(s.messages, messages)
	s.autoApprove = autoApprove

	s.seq = 0
	for _, m := range messages {
		if m.Seq > s.seq {
			s.seq = m.Seq
		}
	}
}

// Join adds a participant to the room. If a participant with the same name
// exists and is online, an error is returned. If they exist but are offline,
// they are reconnected. Returns the current room state snapshot.
func (s *State) Join(name, role, dir, repo, agentType, source string) (protocol.RoomStateParams, error) {
	for i, p := range s.participants {
		if p.Name == name {
			if p.Online {
				return protocol.RoomStateParams{}, fmt.Errorf("name already taken: %q", name)
			}
			// Reconnect offline participant.
			if role != "" {
				s.participants[i].Role = role
			}
			s.participants[i].Directory = dir
			s.participants[i].Repo = repo
			s.participants[i].AgentType = agentType
			s.participants[i].Source = source
			s.participants[i].Online = true
			s.emitParticipantsChanged()
			return s.stateSnapshot(), nil
		}
	}

	// Assign colour for non-human participants.
	var colour string
	if source != "human" {
		colour = AssignColour(s.usedColours())
	}

	// New participant.
	s.participants = append(s.participants, protocol.Participant{
		Name:      name,
		Role:      role,
		Color:     colour,
		Directory: dir,
		Repo:      repo,
		AgentType: agentType,
		Source:    source,
		Online:    true,
	})
	s.emitParticipantsChanged()
	return s.stateSnapshot(), nil
}

// usedColours returns all colours currently assigned to participants.
func (s *State) usedColours() []string {
	var colours []string
	for _, p := range s.participants {
		if p.Color != "" {
			colours = append(colours, p.Color)
		}
	}
	return colours
}

// Leave marks the named participant as offline. No-op if name is not found.
func (s *State) Leave(name string) {
	found := false
	for i, p := range s.participants {
		if p.Name == name {
			s.participants[i].Online = false
			found = true
			break
		}
	}
	if found {
		s.emitParticipantsChanged()
	}
}

// AddMessage creates and stores a new message, computing mentions and emitting
// a MessageReceived event. Returns the created message.
func (s *State) AddMessage(from, source, role string, content protocol.Content) protocol.MessageParams {
	s.seq++
	msg := protocol.MessageParams{
		ID:        generateID(),
		Seq:       s.seq,
		From:      from,
		Source:    source,
		Role:      role,
		Timestamp: time.Now().UTC(),
		Mentions:  protocol.MatchMentions(content.Text, s.ParticipantNames()),
		Content:   []protocol.Content{content},
	}
	s.messages = append(s.messages, msg)
	// Emit with a copy of Content so subscribers can't alias the stored slice.
	emitMsg := msg
	emitMsg.Content = make([]protocol.Content, len(msg.Content))
	copy(emitMsg.Content, msg.Content)
	s.emit(MessageReceived{Message: emitMsg})
	return msg
}

// AddSystemMessage is a convenience wrapper that adds a system message.
func (s *State) AddSystemMessage(text string) protocol.MessageParams {
	return s.AddMessage("system", "system", "system", protocol.Content{Type: "text", Text: text})
}

// UpdateStatus parses the status string, updates the activity map, and emits
// a ParticipantActivityChanged event.
func (s *State) UpdateStatus(name, status string) {
	act := ParseActivity(status)
	s.activities[name] = act
	s.emit(ParticipantActivityChanged{
		Name:     name,
		Activity: act,
	})
}

// RecentMessages returns up to n most recent non-system messages, plus any
// system messages interspersed. This prevents join/leave floods from pushing
// real messages out of the history window.
func (s *State) RecentMessages(n int) []protocol.MessageParams {
	if len(s.messages) == 0 {
		return nil
	}

	contentCount := 0
	start := len(s.messages)
	for i := len(s.messages) - 1; i >= 0; i-- {
		if !s.messages[i].IsSystem() {
			contentCount++
			if contentCount >= n {
				start = i
				break
			}
		}
		if i == 0 {
			start = 0
		}
	}

	msgs := s.messages[start:]
	out := make([]protocol.MessageParams, len(msgs))
	copy(out, msgs)
	return out
}

// ParticipantNames returns a slice of all participant names.
func (s *State) ParticipantNames() []string {
	names := make([]string, len(s.participants))
	for i, p := range s.participants {
		names[i] = p.Name
	}
	return names
}

// stateSnapshot builds a RoomStateParams from the current state.
func (s *State) stateSnapshot() protocol.RoomStateParams {
	outP := make([]protocol.Participant, len(s.participants))
	copy(outP, s.participants)
	return protocol.RoomStateParams{
		RoomID:       s.roomID,
		Topic:        s.topic,
		AutoApprove:  s.autoApprove,
		Participants: outP,
		Messages:     s.RecentMessages(50),
	}
}

// emitParticipantsChanged emits a ParticipantsChanged event with a defensive copy.
func (s *State) emitParticipantsChanged() {
	out := make([]protocol.Participant, len(s.participants))
	copy(out, s.participants)
	s.emit(ParticipantsChanged{Participants: out})
}
