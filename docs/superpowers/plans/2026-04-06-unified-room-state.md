# Unified Room State Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate `server.Room` by making `room.State` the single source of truth. Extract `ConnectionManager` for wire-level concerns. Move persistence behind an interface.

**Architecture:** Strangler migration — each task is independently shippable. `server.Room` is gradually hollowed out until it can be deleted. The server protects `room.State` with its own mutex (state stays single-threaded).

**Tech Stack:** Go, `internal/room`, `internal/server`, `internal/persistence`, `internal/protocol`

---

## File Structure

| File | Action | Responsibility |
|------|--------|----------------|
| `internal/protocol/protocol.go` | Modify | Add `RoomSnapshot`, `MatchMentions`, `IsNameChar` |
| `internal/protocol/protocol_test.go` | Modify | Tests for `MatchMentions` |
| `internal/server/connmanager.go` | Create | `ConnectionManager` + slimmed `ClientConn` |
| `internal/server/connmanager_test.go` | Create | Tests for ConnectionManager |
| `internal/server/room.go` | Modify → Delete | Gradually hollowed, then deleted in Task 6 |
| `internal/server/server.go` | Modify | `TCPServer` uses `room.State` + `ConnectionManager` |
| `internal/server/server_test.go` | Modify | Update to new constructor/API |
| `internal/server/interfaces.go` | Modify | Remove `Room()` from `Server` interface |
| `internal/server/persistence.go` | Modify → Delete | Replaced by `internal/persistence/` in Task 3, deleted in Task 6 |
| `internal/room/state.go` | Modify | Add `Join`, `Leave`, `AddMessage`, `RecentMessages`, `Restore`, `seq` |
| `internal/room/state_test.go` | Modify | Tests for new server-side mutations |
| `internal/room/dispatch.go` | No change | Client-side `HandleServerMessage` stays as-is |
| `internal/persistence/persistence.go` | Create | `Store` interface + `RoomSnapshot` reference |
| `internal/persistence/json_store.go` | Create | `JSONStore` implementation |
| `internal/persistence/json_store_test.go` | Create | Tests for JSONStore |
| `cmd/parley/host.go` | Modify | Wire new persistence, remove `RoomAdapter`, pass state to server |
| `cmd/parley/join.go` | Modify | Use new persistence for agent session lookup |
| `cmd/parley/main_test.go` | Modify | Update resume test |

---

### Task 1: Extract `ConnectionManager` from `server.Room`

**Files:**
- Create: `internal/server/connmanager.go`
- Create: `internal/server/connmanager_test.go`

Extract connection tracking into its own type. `server.Room` still exists and still works — this task only adds the new type alongside it.

- [ ] **Step 1: Write the failing tests**

Create `internal/server/connmanager_test.go`:

```go
package server

import (
	"testing"
)

func TestConnectionManager_AddAndBroadcast(t *testing.T) {
	cm := NewConnectionManager()

	ch1 := make(chan []byte, 8)
	ch2 := make(chan []byte, 8)
	cm.Add("alice", &ClientConn{Name: "alice", Send: ch1, Done: make(chan struct{})})
	cm.Add("bob", &ClientConn{Name: "bob", Send: ch2, Done: make(chan struct{})})

	cm.Broadcast([]byte("hello"))

	if msg := <-ch1; string(msg) != "hello" {
		t.Errorf("alice got %q, want %q", msg, "hello")
	}
	if msg := <-ch2; string(msg) != "hello" {
		t.Errorf("bob got %q, want %q", msg, "hello")
	}
}

func TestConnectionManager_BroadcastExcept(t *testing.T) {
	cm := NewConnectionManager()

	ch1 := make(chan []byte, 8)
	ch2 := make(chan []byte, 8)
	cm.Add("alice", &ClientConn{Name: "alice", Send: ch1, Done: make(chan struct{})})
	cm.Add("bob", &ClientConn{Name: "bob", Send: ch2, Done: make(chan struct{})})

	cm.BroadcastExcept("alice", []byte("for bob only"))

	select {
	case msg := <-ch2:
		if string(msg) != "for bob only" {
			t.Errorf("bob got %q, want %q", msg, "for bob only")
		}
	default:
		t.Error("bob should have received a message")
	}

	select {
	case msg := <-ch1:
		t.Errorf("alice should NOT have received a message, got %q", msg)
	default:
		// correct
	}
}

func TestConnectionManager_Remove(t *testing.T) {
	cm := NewConnectionManager()

	done := make(chan struct{})
	cm.Add("alice", &ClientConn{Name: "alice", Send: make(chan []byte, 8), Done: done})
	cm.Remove("alice")

	// Done channel should be closed.
	select {
	case <-done:
		// correct
	default:
		t.Error("expected Done channel to be closed after Remove")
	}

	// Broadcast should not panic with no connections.
	cm.Broadcast([]byte("nobody home"))
}

func TestConnectionManager_BroadcastDropsFullBuffer(t *testing.T) {
	cm := NewConnectionManager()

	// Unbuffered channel — Broadcast should drop, not block.
	ch := make(chan []byte)
	cm.Add("slow", &ClientConn{Name: "slow", Send: ch, Done: make(chan struct{})})

	// Should not block.
	cm.Broadcast([]byte("dropped"))
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/server/ -run TestConnectionManager -v`
Expected: FAIL — `NewConnectionManager` not defined.

- [ ] **Step 3: Write the implementation**

Create `internal/server/connmanager.go`:

```go
package server

import "sync"

// ClientConn represents a connected participant's network connection.
type ClientConn struct {
	Name string
	Send chan []byte
	Done chan struct{}
}

// ConnectionManager tracks active client connections and provides
// broadcast primitives. It is safe for concurrent use.
type ConnectionManager struct {
	mu    sync.RWMutex
	conns map[string]*ClientConn
}

// NewConnectionManager creates an empty ConnectionManager.
func NewConnectionManager() *ConnectionManager {
	return &ConnectionManager{
		conns: make(map[string]*ClientConn),
	}
}

// Add registers a client connection. If a connection with the same name
// exists, it is replaced (the old Done channel is NOT closed — caller
// should Remove first if needed).
func (cm *ConnectionManager) Add(name string, cc *ClientConn) {
	cm.mu.Lock()
	cm.conns[name] = cc
	cm.mu.Unlock()
}

// Remove closes the client's Done channel and deletes it from the map.
func (cm *ConnectionManager) Remove(name string) {
	cm.mu.Lock()
	cc, ok := cm.conns[name]
	if ok {
		delete(cm.conns, name)
	}
	cm.mu.Unlock()

	if ok {
		close(cc.Done)
	}
}

// Broadcast sends data to all connected clients. Drops messages for
// clients whose Send buffer is full.
func (cm *ConnectionManager) Broadcast(data []byte) {
	cm.mu.RLock()
	targets := make([]chan []byte, 0, len(cm.conns))
	for _, cc := range cm.conns {
		targets = append(targets, cc.Send)
	}
	cm.mu.RUnlock()

	for _, ch := range targets {
		select {
		case ch <- data:
		default:
		}
	}
}

// BroadcastExcept sends data to all connected clients except the named one.
func (cm *ConnectionManager) BroadcastExcept(name string, data []byte) {
	cm.mu.RLock()
	targets := make([]chan []byte, 0, len(cm.conns))
	for n, cc := range cm.conns {
		if n != name {
			targets = append(targets, cc.Send)
		}
	}
	cm.mu.RUnlock()

	for _, ch := range targets {
		select {
		case ch <- data:
		default:
		}
	}
}
```

- [ ] **Step 4: Remove `ClientConn` from `room.go`**

The `ClientConn` type is now defined in `connmanager.go`. Remove the struct definition and its metadata fields from `internal/server/room.go`. `server.Room` continues to use `*ClientConn` — it just comes from the new file now.

In `internal/server/room.go`, delete the `ClientConn` struct (lines 26-36). The `Room` struct's `Participants` field type stays `map[string]*ClientConn` — it still compiles because `ClientConn` is in the same package.

But `Room.Join` currently sets metadata fields on `ClientConn` (`Role`, `Directory`, etc.) and `Room.snapshot` reads them. These fields no longer exist on the slimmed `ClientConn`. **Don't slim `ClientConn` yet** — keep the old fields in `room.go` and move only `Name`, `Send`, `Done` to `connmanager.go`. That creates a duplicate. Better approach: keep `ClientConn` in `room.go` for now (Task 1 doesn't change it), and move it to `connmanager.go` only in Task 4 when `server.Room` is deleted.

Revised: **Do NOT move `ClientConn` in this task.** Just create `ConnectionManager` with its own slim `ConnEntry` or accept `*ClientConn` as-is. Since they're in the same package, `ConnectionManager` can accept the existing `*ClientConn`:

Update `connmanager.go` to remove the `ClientConn` definition — it stays in `room.go`:

```go
// connmanager.go uses *ClientConn defined in room.go
```

Remove the `ClientConn` struct from `connmanager.go` and update the tests to use the existing `ClientConn` from `room.go` (which has `Send`, `Done`, `Name`, plus the metadata fields).

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/server/ -run TestConnectionManager -v -race`
Expected: All 4 tests PASS.

- [ ] **Step 6: Run all tests**

Run: `go test ./... -timeout 30s -race`
Expected: All pass.

- [ ] **Step 7: Commit**

```bash
git add internal/server/connmanager.go internal/server/connmanager_test.go
git commit -m "feat(server): add ConnectionManager for connection tracking"
```

---

### Task 2: Add `protocol.MatchMentions` and `protocol.RoomSnapshot`

**Files:**
- Modify: `internal/protocol/protocol.go`
- Modify: `internal/protocol/protocol_test.go`

Extract mention-matching from `server.Room.extractMentions` into a pure function. Add `RoomSnapshot` for persistence.

- [ ] **Step 1: Write the failing tests**

Add to `internal/protocol/protocol_test.go`:

```go
func TestMatchMentions(t *testing.T) {
	names := []string{"alice", "bob"}

	tests := []struct {
		name string
		text string
		want []string
	}{
		{"exact match", "hey @alice", []string{"alice"}},
		{"multiple", "hey @bob, and @alice!", []string{"bob", "alice"}},
		{"with punctuation", "@bob's idea", []string{"bob"}},
		{"no match", "hey @charlie", nil},
		{"no at sign", "hey alice", nil},
		{"case insensitive", "hey @Alice", []string{"alice"}},
		{"partial non-match", "@bobcat", nil},
		{"empty text", "", nil},
		{"deduplicates", "@alice @alice", []string{"alice"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MatchMentions(tt.text, names)
			if len(got) != len(tt.want) {
				t.Errorf("MatchMentions(%q) = %v, want %v", tt.text, got, tt.want)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("MatchMentions(%q)[%d] = %q, want %q", tt.text, i, got[i], tt.want[i])
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/protocol/ -run TestMatchMentions -v`
Expected: FAIL — `MatchMentions` not defined.

- [ ] **Step 3: Implement `MatchMentions` and `RoomSnapshot`**

Add to `internal/protocol/protocol.go`:

```go
// RoomSnapshot is a plain data container for persisting and restoring room state.
type RoomSnapshot struct {
	RoomID       string          `json:"room_id"`
	Topic        string          `json:"topic"`
	AutoApprove  bool            `json:"auto_approve,omitempty"`
	Participants []Participant   `json:"participants"`
	Messages     []MessageParams `json:"messages"`
}

// MatchMentions matches @tokens in text against a list of known names.
// Returns the matched names in order of appearance. Case-insensitive.
// Handles punctuation after names (e.g. "@bob's", "@alice!").
func MatchMentions(text string, names []string) []string {
	var mentions []string
	seen := make(map[string]bool)
	for _, word := range strings.Fields(text) {
		if !strings.HasPrefix(word, "@") || len(word) < 2 {
			continue
		}
		token := word[1:]
		for _, name := range names {
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/protocol/ -run TestMatchMentions -v`
Expected: All pass.

- [ ] **Step 5: Run all tests**

Run: `go test ./... -timeout 30s -race`
Expected: All pass.

- [ ] **Step 6: Commit**

```bash
git add internal/protocol/protocol.go internal/protocol/protocol_test.go
git commit -m "feat(protocol): add MatchMentions, RoomSnapshot"
```

---

### Task 3: Add Server-Side Mutations to `room.State`

**Files:**
- Modify: `internal/room/state.go`
- Modify: `internal/room/state_test.go`

Add `Join`, `Leave`, `AddMessage`, `AddSystemMessage`, `UpdateStatus`, `RecentMessages`, `Restore`, `ParticipantNames`, and the `seq` counter. These are the business logic methods the server will call (wired in Task 5).

- [ ] **Step 1: Write the failing tests**

Add to `internal/room/state_test.go`:

```go
func TestState_Join_NewParticipant(t *testing.T) {
	s := New(nil, command.Context{})
	s.Restore("room-1", "topic", nil, nil, false)

	events := s.Subscribe()
	state, err := s.Join("alice", "human", "/dir", "repo", "", "human")
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if state.RoomID != "room-1" {
		t.Errorf("RoomID = %q, want %q", state.RoomID, "room-1")
	}
	if len(state.Participants) != 1 || state.Participants[0].Name != "alice" {
		t.Errorf("Participants = %v, want [alice]", state.Participants)
	}

	// Should emit ParticipantsChanged.
	select {
	case evt := <-events:
		if _, ok := evt.(ParticipantsChanged); !ok {
			t.Errorf("expected ParticipantsChanged, got %T", evt)
		}
	default:
		t.Error("expected event, got none")
	}
}

func TestState_Join_DuplicateOnlineReturnsError(t *testing.T) {
	s := New(nil, command.Context{})
	s.Restore("room-1", "topic", nil, nil, false)

	_, err := s.Join("alice", "human", "/dir", "", "", "human")
	if err != nil {
		t.Fatalf("first Join: %v", err)
	}
	_, err = s.Join("alice", "human", "/dir", "", "", "human")
	if err == nil {
		t.Error("second Join with same online name should fail")
	}
}

func TestState_Join_ReconnectsOffline(t *testing.T) {
	s := New(nil, command.Context{})
	s.Restore("room-1", "topic", nil, nil, false)

	s.Join("alice", "human", "/dir", "", "", "human")
	s.Leave("alice")

	// Reconnect.
	state, err := s.Join("alice", "agent", "/new-dir", "", "claude", "agent")
	if err != nil {
		t.Fatalf("reconnect Join: %v", err)
	}
	// Should be online with updated fields.
	for _, p := range state.Participants {
		if p.Name == "alice" {
			if !p.Online {
				t.Error("alice should be online after reconnect")
			}
			if p.Role != "agent" {
				t.Errorf("role = %q, want %q", p.Role, "agent")
			}
			return
		}
	}
	t.Error("alice not found in participants")
}

func TestState_Leave(t *testing.T) {
	s := New(nil, command.Context{})
	s.Restore("room-1", "topic", nil, nil, false)

	s.Join("alice", "human", "/dir", "", "", "human")
	events := s.Subscribe()
	s.Leave("alice")

	participants := s.Participants()
	if len(participants) != 1 || participants[0].Online {
		t.Errorf("expected alice offline, got %v", participants)
	}

	select {
	case evt := <-events:
		if _, ok := evt.(ParticipantsChanged); !ok {
			t.Errorf("expected ParticipantsChanged, got %T", evt)
		}
	default:
		t.Error("expected event, got none")
	}
}

func TestState_AddMessage(t *testing.T) {
	s := New(nil, command.Context{})
	s.Restore("room-1", "topic", nil, nil, false)
	s.Join("alice", "human", "/dir", "", "", "human")
	s.Join("bob", "agent", "/dir", "", "claude", "agent")

	events := s.Subscribe()
	msg := s.AddMessage("alice", "human", "human", protocol.Content{Type: "text", Text: "hey @bob"}, nil)

	if msg.From != "alice" {
		t.Errorf("From = %q, want %q", msg.From, "alice")
	}
	if msg.Seq != 1 {
		t.Errorf("Seq = %d, want 1", msg.Seq)
	}
	if msg.ID == "" {
		t.Error("ID should not be empty")
	}
	// Mentions should be server-computed.
	if len(msg.Mentions) != 1 || msg.Mentions[0] != "bob" {
		t.Errorf("Mentions = %v, want [bob]", msg.Mentions)
	}
	// Should be stored.
	if s.GetMessageCount() != 1 {
		t.Errorf("message count = %d, want 1", s.GetMessageCount())
	}

	select {
	case evt := <-events:
		if mr, ok := evt.(MessageReceived); !ok {
			t.Errorf("expected MessageReceived, got %T", evt)
		} else if mr.Message.From != "alice" {
			t.Errorf("event message from = %q, want %q", mr.Message.From, "alice")
		}
	default:
		t.Error("expected event, got none")
	}
}

func TestState_AddSystemMessage(t *testing.T) {
	s := New(nil, command.Context{})
	msg := s.AddSystemMessage("alice joined")

	if msg.From != "system" {
		t.Errorf("From = %q, want %q", msg.From, "system")
	}
	if !msg.IsSystem() {
		t.Error("expected IsSystem() to be true")
	}
}

func TestState_RecentMessages(t *testing.T) {
	s := New(nil, command.Context{})
	// Add 5 real messages and 3 system messages interspersed.
	for i := 1; i <= 5; i++ {
		s.AddMessage("alice", "human", "human", protocol.Content{Type: "text", Text: fmt.Sprintf("msg %d", i)}, nil)
		if i == 2 || i == 4 {
			s.AddSystemMessage("system event")
		}
	}
	// 8 total messages. RecentMessages(3) should return last 3 non-system + interspersed system.
	recent := s.RecentMessages(3)
	nonSystem := 0
	for _, m := range recent {
		if !m.IsSystem() {
			nonSystem++
		}
	}
	if nonSystem != 3 {
		t.Errorf("expected 3 non-system messages, got %d (total %d)", nonSystem, len(recent))
	}
}

func TestState_UpdateStatus(t *testing.T) {
	s := New(nil, command.Context{})
	events := s.Subscribe()

	s.UpdateStatus("alice", "thinking")

	if s.ParticipantActivity("alice") != ActivityThinking {
		t.Error("expected ActivityThinking")
	}

	select {
	case evt := <-events:
		if pac, ok := evt.(ParticipantActivityChanged); !ok {
			t.Errorf("expected ParticipantActivityChanged, got %T", evt)
		} else if pac.Name != "alice" || pac.Activity != ActivityThinking {
			t.Errorf("unexpected event: %+v", pac)
		}
	default:
		t.Error("expected event, got none")
	}
}

func TestState_Restore(t *testing.T) {
	s := New(nil, command.Context{})
	participants := []protocol.Participant{
		{Name: "alice", Role: "human", Online: false},
	}
	messages := []protocol.MessageParams{
		{ID: "msg-1", Seq: 5, From: "alice"},
		{ID: "msg-2", Seq: 7, From: "bob"},
	}
	s.Restore("room-42", "restored topic", participants, messages, true)

	if s.GetID() != "room-42" {
		t.Errorf("roomID = %q, want %q", s.GetID(), "room-42")
	}
	if s.GetTopic() != "restored topic" {
		t.Errorf("topic = %q, want %q", s.GetTopic(), "restored topic")
	}
	if !s.AutoApprove() {
		t.Error("expected autoApprove true")
	}
	if s.GetMessageCount() != 2 {
		t.Errorf("message count = %d, want 2", s.GetMessageCount())
	}
	// Seq should be set to the highest seq from restored messages.
	msg := s.AddMessage("carol", "human", "human", protocol.Content{Type: "text", Text: "new"}, nil)
	if msg.Seq != 8 {
		t.Errorf("next seq = %d, want 8 (after restore max 7)", msg.Seq)
	}
}

func TestState_ParticipantNames(t *testing.T) {
	s := New(nil, command.Context{})
	s.Restore("room-1", "topic", nil, nil, false)
	s.Join("alice", "human", "/dir", "", "", "human")
	s.Join("bob", "agent", "/dir", "", "claude", "agent")

	names := s.ParticipantNames()
	if len(names) != 2 {
		t.Fatalf("expected 2 names, got %d", len(names))
	}
	has := make(map[string]bool)
	for _, n := range names {
		has[n] = true
	}
	if !has["alice"] || !has["bob"] {
		t.Errorf("expected [alice, bob], got %v", names)
	}
}
```

Note: add `"fmt"` to the import block in state_test.go for `fmt.Sprintf`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/room/ -run "TestState_Join|TestState_Leave|TestState_Add|TestState_Recent|TestState_Update|TestState_Restore|TestState_Participant" -v`
Expected: FAIL — methods not defined.

- [ ] **Step 3: Implement the methods**

Add to `internal/room/state.go`:

```go
import (
	"crypto/rand"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/khaiql/parley/internal/command"
	"github.com/khaiql/parley/internal/protocol"
)
```

Add fields to `State`:

```go
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
```

Add methods:

```go
// newUUID generates a random UUID v4.
func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

var msgCounter uint64

func generateID() string {
	return fmt.Sprintf("msg-%d", atomic.AddUint64(&msgCounter, 1))
}

// Restore sets room state from persisted data. Called on resume before
// the server starts. Sets seq to the highest message seq in the snapshot.
func (s *State) Restore(roomID, topic string, participants []protocol.Participant, messages []protocol.MessageParams, autoApprove bool) {
	s.roomID = roomID
	s.topic = topic
	s.autoApprove = autoApprove
	s.participants = make([]protocol.Participant, len(participants))
	copy(s.participants, participants)
	s.messages = make([]protocol.MessageParams, len(messages))
	copy(s.messages, messages)
	// Set seq to highest message seq so new messages continue from there.
	for _, m := range messages {
		if m.Seq > s.seq {
			s.seq = m.Seq
		}
	}
}

// Join adds a participant or reconnects an offline one. Returns the current
// room state snapshot. If a participant with the same name is already online,
// returns an error.
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

			snap := s.stateSnapshot()
			s.emitParticipantsChanged()
			return snap, nil
		}
	}

	// New participant.
	s.participants = append(s.participants, protocol.Participant{
		Name:      name,
		Role:      role,
		Directory: dir,
		Repo:      repo,
		AgentType: agentType,
		Source:    source,
		Online:    true,
	})

	snap := s.stateSnapshot()
	s.emitParticipantsChanged()
	return snap, nil
}

// Leave marks the named participant as offline.
func (s *State) Leave(name string) {
	for i, p := range s.participants {
		if p.Name == name {
			s.participants[i].Online = false
			break
		}
	}
	s.emitParticipantsChanged()
}

// AddMessage creates a new message, stores it, and emits MessageReceived.
// Mentions are computed by matching @tokens against participant names.
// Returns the fully populated message.
func (s *State) AddMessage(from, source, role string, content protocol.Content, _ []string) protocol.MessageParams {
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
	s.emit(MessageReceived{Message: msg})
	return msg
}

// AddSystemMessage is a convenience wrapper for system-generated messages.
func (s *State) AddSystemMessage(text string) protocol.MessageParams {
	return s.AddMessage("system", "system", "system", protocol.Content{Type: "text", Text: text}, nil)
}

// UpdateStatus updates a participant's activity and emits an event.
func (s *State) UpdateStatus(name, status string) {
	act := ParseActivity(status)
	s.activities[name] = act
	s.emit(ParticipantActivityChanged{
		Name:     name,
		Activity: act,
	})
}

// RecentMessages returns up to n most recent non-system messages, plus any
// system messages interspersed.
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

// ParticipantNames returns the names of all participants.
func (s *State) ParticipantNames() []string {
	names := make([]string, len(s.participants))
	for i, p := range s.participants {
		names[i] = p.Name
	}
	return names
}

// stateSnapshot returns a RoomStateParams for the current state.
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

// emitParticipantsChanged emits a ParticipantsChanged event with a copy.
func (s *State) emitParticipantsChanged() {
	out := make([]protocol.Participant, len(s.participants))
	copy(out, s.participants)
	s.emit(ParticipantsChanged{Participants: out})
}
```

Also update `New` to generate a roomID if one isn't set (for new rooms):

```go
func New(registry *command.Registry, ctx command.Context) *State {
	return &State{
		roomID:     newUUID(),
		activities: make(map[string]Activity),
		commands:   registry,
		cmdCtx:     ctx,
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/room/ -v -race`
Expected: All pass.

- [ ] **Step 5: Run all tests**

Run: `go test ./... -timeout 30s -race`
Expected: All pass.

- [ ] **Step 6: Commit**

```bash
git add internal/room/state.go internal/room/state_test.go
git commit -m "feat(room): add server-side mutations — Join, Leave, AddMessage, Restore"
```

---

### Task 4: Create `internal/persistence/` Package

**Files:**
- Create: `internal/persistence/persistence.go`
- Create: `internal/persistence/json_store.go`
- Create: `internal/persistence/json_store_test.go`

Persistence interface + JSON implementation. Same file format as today.

- [ ] **Step 1: Write the failing tests**

Create `internal/persistence/json_store_test.go`:

```go
package persistence

import (
	"testing"

	"github.com/khaiql/parley/internal/protocol"
)

func TestJSONStore_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	store := NewJSONStore(dir)

	snap := protocol.RoomSnapshot{
		RoomID:      "room-123",
		Topic:       "test topic",
		AutoApprove: true,
		Participants: []protocol.Participant{
			{Name: "alice", Role: "human", Online: true},
		},
		Messages: []protocol.MessageParams{
			{ID: "msg-1", Seq: 1, From: "alice", Content: []protocol.Content{{Type: "text", Text: "hello"}}},
		},
	}

	if err := store.Save(snap); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load("room-123")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.RoomID != "room-123" {
		t.Errorf("RoomID = %q, want %q", loaded.RoomID, "room-123")
	}
	if loaded.Topic != "test topic" {
		t.Errorf("Topic = %q, want %q", loaded.Topic, "test topic")
	}
	if !loaded.AutoApprove {
		t.Error("expected AutoApprove true")
	}
	if len(loaded.Participants) != 1 || loaded.Participants[0].Name != "alice" {
		t.Errorf("Participants = %v", loaded.Participants)
	}
	if len(loaded.Messages) != 1 || loaded.Messages[0].From != "alice" {
		t.Errorf("Messages = %v", loaded.Messages)
	}
}

func TestJSONStore_LoadNotFound(t *testing.T) {
	dir := t.TempDir()
	store := NewJSONStore(dir)

	_, err := store.Load("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent room")
	}
}

func TestJSONStore_AgentSessions(t *testing.T) {
	dir := t.TempDir()
	store := NewJSONStore(dir)

	// Save a room first so the directory exists.
	snap := protocol.RoomSnapshot{RoomID: "room-1", Topic: "t"}
	if err := store.Save(snap); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// No session yet.
	sid, err := store.FindAgentSession("room-1", "bot")
	if err != nil {
		t.Fatalf("FindAgentSession: %v", err)
	}
	if sid != "" {
		t.Errorf("expected empty session, got %q", sid)
	}

	// Save a session.
	if err := store.SaveAgentSession("room-1", "bot", "session-abc"); err != nil {
		t.Fatalf("SaveAgentSession: %v", err)
	}

	// Find it.
	sid, err = store.FindAgentSession("room-1", "bot")
	if err != nil {
		t.Fatalf("FindAgentSession: %v", err)
	}
	if sid != "session-abc" {
		t.Errorf("session = %q, want %q", sid, "session-abc")
	}

	// Update it.
	if err := store.SaveAgentSession("room-1", "bot", "session-def"); err != nil {
		t.Fatalf("SaveAgentSession update: %v", err)
	}
	sid, _ = store.FindAgentSession("room-1", "bot")
	if sid != "session-def" {
		t.Errorf("updated session = %q, want %q", sid, "session-def")
	}
}

func TestJSONStore_SavePreservesExistingSessionIDs(t *testing.T) {
	dir := t.TempDir()
	store := NewJSONStore(dir)

	// Save initial room.
	snap := protocol.RoomSnapshot{
		RoomID: "room-1",
		Topic:  "t",
		Participants: []protocol.Participant{
			{Name: "bot", Role: "agent"},
		},
	}
	store.Save(snap)

	// Save a session ID for bot.
	store.SaveAgentSession("room-1", "bot", "session-123")

	// Re-save the room (simulating auto-save). Session IDs should be preserved.
	store.Save(snap)

	sid, _ := store.FindAgentSession("room-1", "bot")
	if sid != "session-123" {
		t.Errorf("session ID lost after re-save, got %q", sid)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/persistence/ -v`
Expected: FAIL — package doesn't exist.

- [ ] **Step 3: Create the interface**

Create `internal/persistence/persistence.go`:

```go
// Package persistence provides storage backends for room state.
package persistence

import "github.com/khaiql/parley/internal/protocol"

// Store is the interface for persisting and loading room state.
type Store interface {
	// Save persists a room snapshot.
	Save(snapshot protocol.RoomSnapshot) error
	// Load restores a room snapshot by room ID.
	Load(roomID string) (protocol.RoomSnapshot, error)
	// SaveAgentSession stores an agent's session ID for resume.
	SaveAgentSession(roomID, agentName, sessionID string) error
	// FindAgentSession looks up a previously saved session ID.
	FindAgentSession(roomID, agentName string) (string, error)
}
```

- [ ] **Step 4: Create the JSON implementation**

Create `internal/persistence/json_store.go`:

```go
package persistence

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/khaiql/parley/internal/protocol"
)

// JSONStore persists room state as JSON files in a base directory.
// Each room gets a subdirectory: <basePath>/<roomID>/{room.json, messages.json, agents.json}.
type JSONStore struct {
	basePath string
}

// NewJSONStore creates a store rooted at basePath.
func NewJSONStore(basePath string) *JSONStore {
	return &JSONStore{basePath: basePath}
}

// roomData is the on-disk format for room.json.
type roomData struct {
	Topic       string `json:"topic"`
	ID          string `json:"id"`
	AutoApprove bool   `json:"auto_approve,omitempty"`
}

// agentData is the on-disk format for one entry in agents.json.
type agentData struct {
	Name      string `json:"name"`
	Role      string `json:"role"`
	Directory string `json:"directory"`
	Repo      string `json:"repo,omitempty"`
	AgentType string `json:"agent_type,omitempty"`
	Source    string `json:"source"`
	SessionID string `json:"session_id,omitempty"`
}

// RoomDir returns the directory for a room. Exported for callers that need the path.
func (s *JSONStore) RoomDir(roomID string) string {
	return filepath.Join(s.basePath, roomID)
}

func (s *JSONStore) Save(snapshot protocol.RoomSnapshot) error {
	dir := s.RoomDir(snapshot.RoomID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create room dir: %w", err)
	}

	// room.json
	rd := roomData{Topic: snapshot.Topic, ID: snapshot.RoomID, AutoApprove: snapshot.AutoApprove}
	if err := writeJSON(filepath.Join(dir, "room.json"), rd); err != nil {
		return fmt.Errorf("write room.json: %w", err)
	}

	// messages.json
	if err := writeJSON(filepath.Join(dir, "messages.json"), snapshot.Messages); err != nil {
		return fmt.Errorf("write messages.json: %w", err)
	}

	// agents.json — preserve existing session IDs.
	existing := s.loadAgents(dir)
	sessionIDs := make(map[string]string)
	for _, a := range existing {
		if a.SessionID != "" {
			sessionIDs[a.Name] = a.SessionID
		}
	}

	agents := make([]agentData, 0, len(snapshot.Participants))
	for _, p := range snapshot.Participants {
		ad := agentData{
			Name:      p.Name,
			Role:      p.Role,
			Directory: p.Directory,
			Repo:      p.Repo,
			AgentType: p.AgentType,
			Source:    p.Source,
		}
		if sid, ok := sessionIDs[p.Name]; ok {
			ad.SessionID = sid
		}
		agents = append(agents, ad)
	}
	if err := writeJSON(filepath.Join(dir, "agents.json"), agents); err != nil {
		return fmt.Errorf("write agents.json: %w", err)
	}

	return nil
}

func (s *JSONStore) Load(roomID string) (protocol.RoomSnapshot, error) {
	dir := s.RoomDir(roomID)

	var rd roomData
	if err := readJSON(filepath.Join(dir, "room.json"), &rd); err != nil {
		return protocol.RoomSnapshot{}, fmt.Errorf("read room.json: %w", err)
	}

	var msgs []protocol.MessageParams
	if err := readJSON(filepath.Join(dir, "messages.json"), &msgs); err != nil {
		return protocol.RoomSnapshot{}, fmt.Errorf("read messages.json: %w", err)
	}

	agents := s.loadAgents(dir)
	participants := make([]protocol.Participant, 0, len(agents))
	for _, a := range agents {
		participants = append(participants, protocol.Participant{
			Name:      a.Name,
			Role:      a.Role,
			Directory: a.Directory,
			Repo:      a.Repo,
			AgentType: a.AgentType,
			Source:    a.Source,
			Online:    false, // all offline on load
		})
	}

	return protocol.RoomSnapshot{
		RoomID:       rd.ID,
		Topic:        rd.Topic,
		AutoApprove:  rd.AutoApprove,
		Participants: participants,
		Messages:     msgs,
	}, nil
}

func (s *JSONStore) SaveAgentSession(roomID, agentName, sessionID string) error {
	dir := s.RoomDir(roomID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	agents := s.loadAgents(dir)
	found := false
	for i, a := range agents {
		if a.Name == agentName {
			agents[i].SessionID = sessionID
			found = true
			break
		}
	}
	if !found {
		agents = append(agents, agentData{Name: agentName, SessionID: sessionID})
	}
	return writeJSON(filepath.Join(dir, "agents.json"), agents)
}

func (s *JSONStore) FindAgentSession(roomID, agentName string) (string, error) {
	dir := s.RoomDir(roomID)
	agents := s.loadAgents(dir)
	for _, a := range agents {
		if a.Name == agentName {
			return a.SessionID, nil
		}
	}
	return "", nil
}

func (s *JSONStore) loadAgents(dir string) []agentData {
	path := filepath.Join(dir, "agents.json")
	var agents []agentData
	_ = readJSON(path, &agents) // ignore error — file may not exist yet
	return agents
}

func writeJSON(path string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func readJSON(path string, v interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/persistence/ -v -race`
Expected: All pass.

- [ ] **Step 6: Run all tests**

Run: `go test ./... -timeout 30s -race`
Expected: All pass.

- [ ] **Step 7: Commit**

```bash
git add internal/persistence/
git commit -m "feat(persistence): add Store interface with JSONStore implementation"
```

---

### Task 5: Rewire `TCPServer` to Use `room.State` + `ConnectionManager`

**Files:**
- Modify: `internal/server/server.go`
- Modify: `internal/server/interfaces.go`
- Modify: `internal/server/server_test.go`
- Modify: `cmd/parley/host.go`
- Modify: `cmd/parley/join.go`
- Modify: `cmd/parley/main_test.go`

This is the core rewiring. `TCPServer` stops using `server.Room` and instead uses `room.State` (passed in by caller) + `ConnectionManager`. The `Server` interface drops `Room()`.

- [ ] **Step 1: Update `Server` interface**

In `internal/server/interfaces.go`, remove `Room() *Room`:

```go
type Server interface {
	Addr() string
	Port() int
	Serve()
	Close() error
}

var _ Server = (*TCPServer)(nil)
```

- [ ] **Step 2: Rewrite `TCPServer`**

In `internal/server/server.go`:

```go
type TCPServer struct {
	listener net.Listener
	state    *room.State
	conns    *ConnectionManager
	mu       sync.Mutex // protects state from concurrent handleConn goroutines
}

func New(addr string, state *room.State) (*TCPServer, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	return &TCPServer{
		listener: ln,
		state:    state,
		conns:    NewConnectionManager(),
	}, nil
}
```

Remove `NewWithRoom`, `Room()`. Add import for `room` package and `sync`.

Rewrite `handleConn`:

```go
func (s *TCPServer) handleConn(conn net.Conn) {
	defer conn.Close()

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, scanBufSize), scanBufSize)

	var name string
	var source string

	for sc.Scan() {
		line := sc.Bytes()
		raw, err := protocol.DecodeLine(line)
		if err != nil {
			continue
		}

		switch raw.Method {
		case protocol.MethodJoin:
			var params protocol.JoinParams
			if err := json.Unmarshal(raw.Params, &params); err != nil {
				continue
			}
			src := "human"
			if params.AgentType != "" {
				src = "agent"
			}
			name = params.Name
			source = src

			s.mu.Lock()
			stateParams, joinErr := s.state.Join(params.Name, params.Role, params.Directory, params.Repo, params.AgentType, src)
			s.mu.Unlock()

			if joinErr != nil {
				resp := protocol.Response{
					JSONRPC: "2.0",
					ID:      0,
					Error:   &protocol.RPCError{Code: -1, Message: joinErr.Error()},
				}
				if data, err := protocol.EncodeLine(resp); err == nil {
					_, _ = conn.Write(data)
				}
				return
			}

			// Send room.state to joining client.
			notif := protocol.NewNotification(protocol.MethodState, stateParams)
			if data, err := protocol.EncodeLine(notif); err == nil {
				_, _ = conn.Write(data)
			}

			// Register connection.
			cc := &ClientConn{
				Name: params.Name,
				Send: make(chan []byte, 64),
				Done: make(chan struct{}),
			}
			s.conns.Add(params.Name, cc)

			// Notify others.
			effectiveRole := params.Role
			for _, p := range stateParams.Participants {
				if p.Name == params.Name {
					effectiveRole = p.Role
					break
				}
			}
			jp := protocol.JoinedParams{
				Name:      params.Name,
				Role:      effectiveRole,
				Directory: params.Directory,
				Repo:      params.Repo,
				AgentType: params.AgentType,
				JoinedAt:  time.Now().UTC(),
			}
			joinedNotif := protocol.NewNotification(protocol.MethodJoined, jp)
			if data, err := protocol.EncodeLine(joinedNotif); err == nil {
				s.conns.BroadcastExcept(params.Name, data)
			}

			s.mu.Lock()
			sysMsg := s.state.AddSystemMessage(fmt.Sprintf("%s joined", params.Name))
			s.mu.Unlock()
			sysNotif := protocol.NewNotification(protocol.MethodMessage, sysMsg)
			if data, err := protocol.EncodeLine(sysNotif); err == nil {
				s.conns.Broadcast(data)
			}

			// Start writer goroutine.
			go func(c net.Conn, client *ClientConn) {
				for {
					select {
					case data := <-client.Send:
						_, _ = c.Write(data)
					case <-client.Done:
						return
					}
				}
			}(conn, cc)

		case protocol.MethodSend:
			if name == "" {
				continue
			}
			var params protocol.SendParams
			if err := json.Unmarshal(raw.Params, &params); err != nil {
				continue
			}
			if len(params.Content) == 0 {
				continue
			}

			s.mu.Lock()
			msg := s.state.AddMessage(name, source, "", params.Content[0], params.Mentions)
			s.mu.Unlock()

			msgNotif := protocol.NewNotification(protocol.MethodMessage, msg)
			if data, err := protocol.EncodeLine(msgNotif); err == nil {
				s.conns.Broadcast(data)
			}

		case protocol.MethodStatus:
			if name == "" {
				continue
			}
			var params protocol.StatusParams
			if err := json.Unmarshal(raw.Params, &params); err != nil {
				continue
			}
			params.Name = name

			s.mu.Lock()
			s.state.UpdateStatus(name, params.Status)
			s.mu.Unlock()

			statusNotif := protocol.NewNotification(protocol.MethodStatus, params)
			if data, err := protocol.EncodeLine(statusNotif); err == nil {
				s.conns.BroadcastExcept(name, data)
			}
		}
	}

	// Client disconnected.
	if name != "" {
		s.mu.Lock()
		s.state.Leave(name)
		sysMsg := s.state.AddSystemMessage(fmt.Sprintf("%s left", name))
		s.mu.Unlock()

		s.conns.Remove(name)

		leftNotif := protocol.NewNotification(protocol.MethodLeft, protocol.LeftParams{Name: name})
		if data, err := protocol.EncodeLine(leftNotif); err == nil {
			s.conns.Broadcast(data)
		}
		sysNotif := protocol.NewNotification(protocol.MethodMessage, sysMsg)
		if data, err := protocol.EncodeLine(sysNotif); err == nil {
			s.conns.Broadcast(data)
		}
	}
}
```

Note: the role in `AddMessage` for `MethodSend` — the server needs to know the role. Store it alongside `name` and `source` at the top of `handleConn`. Update:

```go
var name string
var source string
var role string
```

And in the join case: `role = params.Role` (or the effective role from reconnection). In the send case: `s.state.AddMessage(name, source, role, ...)`.

Actually, on reconnection the effective role comes from `stateParams.Participants`. Update the join handler to capture it:

```go
// After join succeeds, capture effective role.
for _, p := range stateParams.Participants {
    if p.Name == params.Name {
        role = p.Role
        source = p.Source
        break
    }
}
```

- [ ] **Step 3: Update host.go**

Major changes:
- Host creates `room.State` and passes it to `server.New(addr, roomState)`
- For resume, load snapshot via `persistence.JSONStore`, call `roomState.Restore()`
- Persistence uses `JSONStore` throughout
- Remove `RoomAdapter` — `room.State` satisfies `RoomQuerier` except for `GetPort`
- For `GetPort`, add it to `command.Context` or keep a minimal adapter

```go
func runHost(_ *cobra.Command, _ []string) error {
    roomState := room.New(nil, command.Context{})

    store := persistence.NewJSONStore(defaultParleyDir())

    if hostResume != "" {
        snap, err := store.Load(hostResume)
        if err != nil {
            return fmt.Errorf("host: load room %q: %w", hostResume, err)
        }
        roomState.Restore(snap.RoomID, snap.Topic, snap.Participants, snap.Messages, snap.AutoApprove)
        if hostTopic == "" {
            hostTopic = snap.Topic
        }
    } else {
        if hostTopic == "" {
            return fmt.Errorf("host: --topic is required when not using --resume")
        }
        roomState.Restore(roomState.GetID(), hostTopic, nil, nil, false)
    }

    if hostYolo {
        roomState.SetAutoApprove(true)
    }

    addr := fmt.Sprintf(":%d", hostPort)
    srv, err := server.New(addr, roomState)
    if err != nil {
        return fmt.Errorf("host: create server: %w", err)
    }
    go srv.Serve()
    // ... rest follows same pattern, using store instead of server.SaveRoom
}
```

Add `defaultParleyDir()` helper to main.go:

```go
func defaultParleyDir() string {
    home, err := os.UserHomeDir()
    if err != nil {
        home = "."
    }
    return filepath.Join(home, ".parley", "rooms")
}
```

For persistence calls, replace `server.SaveRoom(roomDir, srv.Room())` with:

```go
store.Save(protocol.RoomSnapshot{
    RoomID:       roomState.GetID(),
    Topic:        roomState.GetTopic(),
    AutoApprove:  roomState.AutoApprove(),
    Participants: roomState.GetParticipants(),
    Messages:     roomState.Messages(),
})
```

For `RoomQuerier`, keep a minimal adapter or add `GetPort` to the command context. Simplest: keep `RoomAdapter` but it wraps `*room.State` (already does from earlier work), just update the `SaveFn` to use the new store.

- [ ] **Step 4: Update join.go**

Replace `server.RoomDir` and `server.FindAgentSessionID` / `server.UpdateAgentSessionID` with:

```go
store := persistence.NewJSONStore(defaultParleyDir())
// ...
sid, err := store.FindAgentSession(roomID, joinName)
// ...
store.SaveAgentSession(roomID, joinName, sid)
```

- [ ] **Step 5: Update server_test.go**

Tests currently use `server.New("addr", "topic")` and `server.NewWithRoom(addr, room)`. Update to create `room.State`, call `Restore` for topic, and pass to `server.New(addr, state)`.

```go
func newTestServer(t *testing.T) *server.TCPServer {
    t.Helper()
    state := room.New(nil, command.Context{})
    state.Restore(state.GetID(), "test-topic", nil, nil, false)
    s, err := server.New("127.0.0.1:0", state)
    if err != nil {
        t.Fatalf("server.New: %v", err)
    }
    go s.Serve()
    t.Cleanup(func() { s.Close() })
    return s
}
```

- [ ] **Step 6: Update main_test.go**

`TestHostResumeLoadsHistory` currently uses `server.NewRoom`, `server.SaveRoom`, `server.LoadRoom`, `server.NewWithRoom`. Replace with persistence store + room.State:

```go
func TestHostResumeLoadsHistory(t *testing.T) {
    dir := t.TempDir()
    store := persistence.NewJSONStore(dir)

    // Create a room state, add messages, and save.
    state := room.New(nil, command.Context{})
    state.Restore(state.GetID(), "resume-topic", nil, nil, false)
    state.AddMessage("alice", "human", "human", protocol.Content{Type: "text", Text: "message one"}, nil)
    state.AddMessage("alice", "human", "human", protocol.Content{Type: "text", Text: "message two"}, nil)

    store.Save(protocol.RoomSnapshot{
        RoomID:   state.GetID(),
        Topic:    state.GetTopic(),
        Messages: state.Messages(),
    })

    // Load and verify.
    snap, err := store.Load(state.GetID())
    // ... verify snap has 2 messages, correct topic

    // Start server with restored state.
    restored := room.New(nil, command.Context{})
    restored.Restore(snap.RoomID, snap.Topic, snap.Participants, snap.Messages, snap.AutoApprove)
    srv, err := server.New("127.0.0.1:0", restored)
    // ... join and verify room.state has messages
}
```

- [ ] **Step 7: Verify all tests pass**

Run: `go build ./... && go test ./... -timeout 30s -race`
Expected: All pass.

- [ ] **Step 8: Run linter**

Run: `go run github.com/golangci/golangci-lint/cmd/golangci-lint@latest run ./... --timeout=5m`
Expected: Clean.

- [ ] **Step 9: Commit**

```bash
git add internal/server/ cmd/parley/ internal/persistence/
git commit -m "refactor: rewire TCPServer to use room.State + ConnectionManager"
```

---

### Task 6: Delete `server.Room` and Old Persistence

**Files:**
- Delete: `internal/server/room.go` (the entire file)
- Delete: `internal/server/room_test.go`
- Modify: `internal/server/persistence.go` — delete entirely or keep only as thin wrappers if anything still references it
- Modify: `internal/server/persistence_test.go` — delete or move to persistence package

- [ ] **Step 1: Delete `server.Room`**

```bash
rm internal/server/room.go internal/server/room_test.go
```

- [ ] **Step 2: Delete old persistence (if fully replaced)**

Check if anything still imports the old functions:

```bash
grep -rn 'server\.SaveRoom\|server\.LoadRoom\|server\.RoomDir\|server\.FindAgentSessionID\|server\.UpdateAgentSessionID' cmd/ internal/
```

If nothing references them, delete:

```bash
rm internal/server/persistence.go internal/server/persistence_test.go
```

If the integration tests or smoke tests reference them, update those first.

- [ ] **Step 3: Clean up imports**

Check for unused imports across all files:

```bash
go build ./...
```

Fix any compilation errors from removed types/functions.

- [ ] **Step 4: Run full test suite**

Run: `go test ./... -timeout 30s -race`
Expected: All pass.

- [ ] **Step 5: Run linter**

Run: `go run github.com/golangci/golangci-lint/cmd/golangci-lint@latest run ./... --timeout=5m`
Expected: Clean.

- [ ] **Step 6: Run e2e verification**

Build and run a full e2e test with agent-tui:

1. `go build -o parley ./cmd/parley`
2. Host a room with `--yolo`
3. Join a Claude agent
4. Send a message from host mentioning the agent
5. Verify agent responds
6. Kill sessions, clean up

Also test resume:

1. Host a room, send a message, exit
2. Resume with `--resume <roomID>`
3. Verify messages load

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "refactor: delete server.Room — room.State is single source of truth"
```

---

## Verification Summary

After all tasks:

```bash
go build ./...
go test ./... -timeout 30s -race
go run github.com/golangci/golangci-lint/cmd/golangci-lint@latest run ./... --timeout=5m
```

Plus e2e with agent-tui (host + join + message exchange + resume).

## What Gets Deleted

| Deleted | Replaced by |
|---------|------------|
| `server.Room` struct + all methods | `room.State` + `server.ConnectionManager` |
| `server.ClientConn` metadata fields | `room.State` participant tracking |
| `server.extractMentions` / `isNameChar` | `protocol.MatchMentions` / `protocol.isNameChar` |
| `server.NewRoom` / `newUUID` | `room.New` / `room.newUUID` |
| `server.generateID` / `msgCounter` | `room.generateID` / `room.msgCounter` |
| `server.SaveRoom` / `LoadRoom` / `RoomDir` | `persistence.JSONStore` |
| `server.RoomData` / `ParticipantData` | `persistence.roomData` / `persistence.agentData` |
| `server.SaveAgents` / `LoadAgents` / `FindAgentSessionID` / `UpdateAgentSessionID` | `persistence.JSONStore` methods |
| `Server.Room()` interface method | Removed — host owns `room.State` directly |
