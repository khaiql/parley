package server

import (
	"crypto/rand"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/khaiql/parley/internal/protocol"
)

// Room holds the shared state for a single chat room.
type Room struct {
	ID           string
	Topic        string
	AutoApprove  bool
	Debug        bool
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
	Online    bool
	Send      chan []byte
	Done      chan struct{}
}

// NewRoom creates a new Room with the given topic and a fresh UUID as its ID.
func NewRoom(topic string) *Room {
	return &Room{
		ID:           newUUID(),
		Topic:        topic,
		Participants: make(map[string]*ClientConn),
	}
}

// newUUID generates a random UUID v4.
func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Join adds cc to the room and returns a snapshot of the current room state,
// including recent message history (up to 50 messages).
// If cc.Send or cc.Done are nil they are initialized here.
// If a participant with the same name exists and is offline, they are
// reconnected (brought back online). If they are online, an error is returned.
// When reconnecting, an empty Role in cc preserves the previously saved role.
func (r *Room) Join(cc *ClientConn) (protocol.RoomStateParams, error) {
	if cc.Send == nil {
		cc.Send = make(chan []byte, 64)
	}
	if cc.Done == nil {
		cc.Done = make(chan struct{})
	}

	r.mu.Lock()
	if existing, exists := r.Participants[cc.Name]; exists {
		if existing.Online {
			r.mu.Unlock()
			return protocol.RoomStateParams{}, fmt.Errorf("name already taken: %q", cc.Name)
		}
		// Reconnecting offline participant — update and bring online.
		if cc.Role != "" {
			existing.Role = cc.Role
		}
		existing.Directory = cc.Directory
		existing.Repo = cc.Repo
		existing.AgentType = cc.AgentType
		existing.Source = cc.Source
		existing.Online = true
		existing.Send = cc.Send
		existing.Done = cc.Done
	} else {
		cc.Online = true
		r.Participants[cc.Name] = cc
	}
	participants := r.snapshot()
	topic := r.Topic
	recent := r.recentMessages(50)
	r.mu.Unlock()

	return protocol.RoomStateParams{
		RoomID:       r.ID,
		Topic:        topic,
		AutoApprove:  r.AutoApprove,
		Debug:        r.Debug,
		Participants: participants,
		Messages:     recent,
	}, nil
}

// extractMentions scans text for @name tokens that match known participant names.
// Must be called with r.mu held.
func (r *Room) extractMentions(text string) []string {
	var mentions []string
	seen := make(map[string]bool)
	for _, word := range strings.Fields(text) {
		if !strings.HasPrefix(word, "@") || len(word) < 2 {
			continue
		}
		token := word[1:]
		// Check if any participant name is a prefix of the token (handles @bob, @bob's, @bob! etc.)
		for name := range r.Participants {
			lower := strings.ToLower(token)
			lowerName := strings.ToLower(name)
			if strings.EqualFold(token, name) ||
				(strings.HasPrefix(lower, lowerName) && len(token) > len(name) && !isNameChar(token[len(name)])) {
				if !seen[name] {
					mentions = append(mentions, name)
					seen[name] = true
				}
			}
		}
	}
	return mentions
}

// isNameChar returns true if c could be part of a participant name.
func isNameChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-'
}

// recentMessages returns up to n most recent non-system messages, plus any
// system messages interspersed. This prevents a flood of join/leave events
// from pushing all real messages out of the history window.
func (r *Room) recentMessages(n int) []protocol.MessageParams {
	if len(r.Messages) == 0 {
		return nil
	}

	// Walk backward to find enough non-system messages.
	contentCount := 0
	start := len(r.Messages)
	for i := len(r.Messages) - 1; i >= 0; i-- {
		if !r.Messages[i].IsSystem() {
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

	msgs := r.Messages[start:]
	out := make([]protocol.MessageParams, len(msgs))
	copy(out, msgs)
	return out
}

// RecentMessages returns a copy of the last n messages, safe for concurrent use.
func (r *Room) RecentMessages(n int) []protocol.MessageParams {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.recentMessages(n)
}

// Leave marks the named participant as offline and closes their Done channel.
// The participant remains in the map so mentions, colors, and persistence
// continue to work.
func (r *Room) Leave(name string) {
	r.mu.Lock()
	cc, ok := r.Participants[name]
	if ok {
		cc.Online = false
	}
	r.mu.Unlock()

	if ok {
		close(cc.Done)
	}
}

// Broadcast creates a new message, stores it, and fans it out to all participants.
// Mentions are computed server-side by matching @name tokens against known participants.
func (r *Room) Broadcast(from, source, role string, content protocol.Content, _ []string) protocol.MessageParams {
	r.mu.Lock()
	r.seq++
	msg := protocol.MessageParams{
		ID:        generateID(),
		Seq:       r.seq,
		From:      from,
		Source:    source,
		Role:      role,
		Timestamp: time.Now().UTC(),
		Mentions:  r.extractMentions(content.Text),
		Content:   []protocol.Content{content},
	}
	r.Messages = append(r.Messages, msg)

	// Collect send channels for online participants only.
	targets := make([]chan []byte, 0, len(r.Participants))
	for _, cc := range r.Participants {
		if cc.Online {
			targets = append(targets, cc.Send)
		}
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

// BroadcastStatus sends a room.status notification to all participants except
// the sender (identified by sp.Name).
func (r *Room) BroadcastStatus(sp protocol.StatusParams) {
	notif := protocol.NewNotification("room.status", sp)
	data, _ := protocol.EncodeLine(notif)

	r.mu.RLock()
	targets := make([]chan []byte, 0, len(r.Participants))
	for name, cc := range r.Participants {
		if name != sp.Name {
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

// BroadcastLeft sends a room.left notification to all remaining participants.
func (r *Room) BroadcastLeft(lp protocol.LeftParams) {
	notif := protocol.NewNotification("room.left", lp)
	data, _ := protocol.EncodeLine(notif)

	r.mu.RLock()
	targets := make([]chan []byte, 0, len(r.Participants))
	for _, cc := range r.Participants {
		targets = append(targets, cc.Send)
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
			Online:    cc.Online,
		})
	}
	return out
}

// GetMessages returns a copy of the room's message history, safe for concurrent use.
func (r *Room) GetMessages() []protocol.MessageParams {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]protocol.MessageParams, len(r.Messages))
	copy(out, r.Messages)
	return out
}

// GetParticipants returns a snapshot of all participants (online and offline),
// safe for concurrent use.
func (r *Room) GetParticipants() []*ClientConn {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*ClientConn, 0, len(r.Participants))
	for _, cc := range r.Participants {
		out = append(out, cc)
	}
	return out
}

// GetOnlineParticipants returns only the online participants, safe for
// concurrent use.
func (r *Room) GetOnlineParticipants() []*ClientConn {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*ClientConn, 0, len(r.Participants))
	for _, cc := range r.Participants {
		if cc.Online {
			out = append(out, cc)
		}
	}
	return out
}

// MessageCount returns the number of messages in the room, safe for concurrent use.
func (r *Room) MessageCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.Messages)
}

var msgCounter uint64

// generateID returns a unique message ID using an atomic counter.
func generateID() string {
	return fmt.Sprintf("msg-%d", atomic.AddUint64(&msgCounter, 1))
}
