package server

import (
	"fmt"
	"sync"
	"time"

	"github.com/sle/parley/internal/protocol"
)

// Room holds the shared state for a single chat room.
type Room struct {
	Topic        string
	Participants map[string]*ClientConn
	Messages     []protocol.MessageParams
	seq          int
	mu           sync.RWMutex
}

// ClientConn represents a connected participant.
type ClientConn struct {
	Name      string
	Role      string
	Directory string
	Repo      string
	AgentType string
	Source    string
	Send      chan []byte
	Done      chan struct{}
}

// NewRoom creates a new Room with the given topic.
func NewRoom(topic string) *Room {
	return &Room{
		Topic:        topic,
		Participants: make(map[string]*ClientConn),
	}
}

// Join adds cc to the room and returns a snapshot of the current room state.
// If cc.Send or cc.Done are nil they are initialized here.
func (r *Room) Join(cc *ClientConn) protocol.RoomStateParams {
	if cc.Send == nil {
		cc.Send = make(chan []byte, 64)
	}
	if cc.Done == nil {
		cc.Done = make(chan struct{})
	}

	r.mu.Lock()
	r.Participants[cc.Name] = cc
	participants := r.snapshot()
	topic := r.Topic
	r.mu.Unlock()

	return protocol.RoomStateParams{
		Topic:        topic,
		Participants: participants,
	}
}

// Leave removes the named participant from the room and closes their Done channel.
func (r *Room) Leave(name string) {
	r.mu.Lock()
	cc, ok := r.Participants[name]
	if ok {
		delete(r.Participants, name)
	}
	r.mu.Unlock()

	if ok {
		close(cc.Done)
	}
}

// Broadcast creates a new message, stores it, and fans it out to all participants.
func (r *Room) Broadcast(from, source, role string, content protocol.Content, mentions []string) protocol.MessageParams {
	r.mu.Lock()
	r.seq++
	msg := protocol.MessageParams{
		ID:        generateID(),
		Seq:       r.seq,
		From:      from,
		Source:    source,
		Role:      role,
		Timestamp: time.Now().UTC(),
		Mentions:  mentions,
		Content:   []protocol.Content{content},
	}
	r.Messages = append(r.Messages, msg)

	// Collect send channels while holding the lock to avoid races.
	targets := make([]chan []byte, 0, len(r.Participants))
	for _, cc := range r.Participants {
		targets = append(targets, cc.Send)
	}
	r.mu.Unlock()

	notif := protocol.NewNotification("room.message", msg)
	data, _ := protocol.EncodeLine(notif)

	for _, ch := range targets {
		select {
		case ch <- data:
		default:
			// Drop if the client's buffer is full — avoid blocking the broadcaster.
		}
	}

	return msg
}

// BroadcastSystem sends a system message to all participants.
func (r *Room) BroadcastSystem(text string) {
	r.Broadcast("system", "system", "system", protocol.Content{Type: "text", Text: text}, nil)
}

// BroadcastJoined sends a room.joined notification to all participants except
// the newly joined one.
func (r *Room) BroadcastJoined(jp protocol.JoinedParams) {
	notif := protocol.NewNotification("room.joined", jp)
	data, _ := protocol.EncodeLine(notif)

	r.mu.RLock()
	targets := make([]chan []byte, 0, len(r.Participants))
	for name, cc := range r.Participants {
		if name != jp.Name {
			targets = append(targets, cc.Send)
		}
	}
	r.mu.RUnlock()

	for _, ch := range targets {
		select {
		case ch <- data:
		default:
		}
	}
}

// snapshot returns the current participant list. Must be called with r.mu held (at least RLock).
func (r *Room) snapshot() []protocol.Participant {
	out := make([]protocol.Participant, 0, len(r.Participants))
	for _, cc := range r.Participants {
		out = append(out, protocol.Participant{
			Name:      cc.Name,
			Role:      cc.Role,
			Directory: cc.Directory,
			Repo:      cc.Repo,
			AgentType: cc.AgentType,
			Source:    cc.Source,
		})
	}
	return out
}

// generateID returns a simple timestamp-based ID.
func generateID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
