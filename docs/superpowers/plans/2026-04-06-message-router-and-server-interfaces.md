# MessageRouter & Server/Client Interfaces Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate `bridgeNetworkToAgent` by making the router an event subscriber, introduce Server/Client interfaces to decouple transport from business logic, move room metadata into `room.State`, and split `cmd/parley/main.go` into focused files.

**Architecture:** The DebounceRouter subscribes to `room.State` events (like the TUI does) instead of reading raw messages from `c.Incoming()`. Server and Client become interfaces with TCP implementations. `room.State` gains roomID/topic fields and implements most of `RoomQuerier`, leaving `RoomAdapter` as a thin wrapper that only adds port.

**Tech Stack:** Go, `internal/room`, `internal/protocol`, `internal/server`, `internal/client`

---

## File Structure

| File | Action | Responsibility |
|------|--------|----------------|
| `internal/room/router.go` | Create | `MessageRouter` interface + `DebounceRouter` |
| `internal/room/router_test.go` | Create | 7 test cases for DebounceRouter |
| `internal/room/state.go` | Modify | Add roomID, topic fields; implement `RoomQuerier` methods |
| `internal/room/state_test.go` | Modify | Add tests for new RoomQuerier methods |
| `internal/room/dispatch.go` | Modify | Populate roomID/topic from `room.state` message |
| `internal/server/interfaces.go` | Create | `Server` interface |
| `internal/server/server.go` | Modify | Rename to `TcpServer`, implement `Server` interface |
| `internal/server/server_test.go` | Modify | Update to use `TcpServer` name |
| `internal/client/interfaces.go` | Create | `Client` interface |
| `internal/client/client.go` | Modify | Rename to `TcpClient`, implement `Client` interface |
| `cmd/parley/main.go` | Modify | Slim down to root command, `init()`, shared helpers only |
| `cmd/parley/host.go` | Create | `runHost`, `RoomAdapter`, host flags |
| `cmd/parley/join.go` | Create | `runJoin`, join flags |
| `cmd/parley/export.go` | Create | `runExport`, export flags |

---

### Task 1: Add roomID and topic to `room.State`

**Files:**
- Modify: `internal/room/state.go`
- Modify: `internal/room/state_test.go`
- Modify: `internal/room/dispatch.go`

`room.State` gains `roomID` and `topic` fields, populated when the `room.state` message arrives. It implements `GetID()`, `GetTopic()`, `GetParticipants()`, and `GetMessageCount()` from `command.RoomQuerier`. This lets `RoomAdapter` in main.go become a thin wrapper that only adds `GetPort()`.

- [ ] **Step 1: Write the failing tests**

Add to `internal/room/state_test.go`:

```go
func TestState_RoomQuerier_AfterState(t *testing.T) {
	rs := New(nil, command.Context{})

	if rs.GetID() != "" {
		t.Errorf("expected empty ID before state, got %q", rs.GetID())
	}
	if rs.GetTopic() != "" {
		t.Errorf("expected empty topic before state, got %q", rs.GetTopic())
	}
	if rs.GetMessageCount() != 0 {
		t.Errorf("expected 0 messages, got %d", rs.GetMessageCount())
	}
	if rs.GetParticipants() != nil {
		t.Errorf("expected nil participants, got %v", rs.GetParticipants())
	}

	stateJSON, _ := json.Marshal(protocol.RoomStateParams{
		RoomID: "room-123",
		Topic:  "test topic",
		Participants: []protocol.Participant{
			{Name: "alice", Role: "human", Online: true},
		},
		Messages: []protocol.MessageParams{
			{ID: "msg-1", From: "alice", Content: []protocol.Content{{Type: "text", Text: "hi"}}},
		},
	})
	rs.HandleServerMessage(&protocol.RawMessage{
		Method: protocol.MethodState,
		Params: stateJSON,
	})

	if rs.GetID() != "room-123" {
		t.Errorf("GetID() = %q, want %q", rs.GetID(), "room-123")
	}
	if rs.GetTopic() != "test topic" {
		t.Errorf("GetTopic() = %q, want %q", rs.GetTopic(), "test topic")
	}
	if rs.GetMessageCount() != 1 {
		t.Errorf("GetMessageCount() = %d, want 1", rs.GetMessageCount())
	}
	participants := rs.GetParticipants()
	if len(participants) != 1 || participants[0].Name != "alice" {
		t.Errorf("GetParticipants() = %v, want [alice]", participants)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/room/ -run TestState_RoomQuerier -v`
Expected: FAIL — `GetID` not defined.

- [ ] **Step 3: Add fields and methods to state.go**

Add `roomID` and `topic` fields to the `State` struct:

```go
type State struct {
	roomID       string
	topic        string
	participants []protocol.Participant
	// ... rest unchanged
}
```

Add the `RoomQuerier` methods:

```go
func (s *State) GetID() string                            { return s.roomID }
func (s *State) GetTopic() string                         { return s.topic }
func (s *State) GetParticipants() []protocol.Participant  { return s.Participants() }
func (s *State) GetMessageCount() int                     { return len(s.messages) }
```

- [ ] **Step 4: Populate roomID and topic in dispatch.go**

In `HandleServerMessage`, inside the `MethodState` case, add after the existing parsing:

```go
s.roomID = params.RoomID
s.topic = params.Topic
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/room/ -v -race`
Expected: All pass.

- [ ] **Step 6: Commit**

```bash
git add internal/room/state.go internal/room/state_test.go internal/room/dispatch.go
git commit -m "feat(room): add roomID/topic to State, implement RoomQuerier methods"
```

---

### Task 2: Create DebounceRouter

**Files:**
- Create: `internal/room/router.go`
- Create: `internal/room/router_test.go`

The `DebounceRouter` subscribes to `room.State` events via `Start(events)`, listens for `MessageReceived`, and routes messages to the agent driver with debounce logic. This replaces the routing logic inside `bridgeNetworkToAgent` (`cmd/parley/main.go:554-606`).

- [ ] **Step 1: Write the failing tests**

Create `internal/room/router_test.go`:

```go
package room

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/khaiql/parley/internal/protocol"
)

type collector struct {
	mu   sync.Mutex
	msgs []string
}

func (c *collector) send(text string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.msgs = append(c.msgs, text)
}

func (c *collector) get() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.msgs))
	copy(out, c.msgs)
	return out
}

func makeMsg(from, text string, mentions []string) protocol.MessageParams {
	return protocol.MessageParams{
		From:     from,
		Source:   "human",
		Role:     "human",
		Content:  []protocol.Content{{Type: "text", Text: text}},
		Mentions: mentions,
	}
}

func TestDebounceRouter_IgnoresOwnMessages(t *testing.T) {
	col := &collector{}
	r := NewDebounceRouter("bot", 50*time.Millisecond, col.send)

	events := make(chan Event, 8)
	r.Start(events)

	events <- MessageReceived{Message: makeMsg("bot", "my own message", nil)}
	close(events)
	r.Close()

	if msgs := col.get(); len(msgs) != 0 {
		t.Errorf("expected no messages, got %v", msgs)
	}
}

func TestDebounceRouter_MentionDeliversImmediately(t *testing.T) {
	col := &collector{}
	r := NewDebounceRouter("bot", 50*time.Millisecond, col.send)

	events := make(chan Event, 8)
	r.Start(events)

	events <- MessageReceived{Message: makeMsg("alice", "@bot help me", []string{"bot"})}
	time.Sleep(10 * time.Millisecond)

	msgs := col.get()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if !strings.Contains(msgs[0], "alice:") {
		t.Errorf("expected formatted as 'alice: ...', got %q", msgs[0])
	}

	close(events)
	r.Close()
}

func TestDebounceRouter_NonMentionBatchesWithDelay(t *testing.T) {
	col := &collector{}
	r := NewDebounceRouter("bot", 50*time.Millisecond, col.send)

	events := make(chan Event, 8)
	r.Start(events)

	events <- MessageReceived{Message: makeMsg("alice", "hello", nil)}

	time.Sleep(10 * time.Millisecond)
	if msgs := col.get(); len(msgs) != 0 {
		t.Errorf("expected 0 messages before debounce, got %d", len(msgs))
	}

	time.Sleep(60 * time.Millisecond)
	if msgs := col.get(); len(msgs) != 1 {
		t.Errorf("expected 1 message after debounce, got %d", len(msgs))
	}

	close(events)
	r.Close()
}

func TestDebounceRouter_MentionFlushesPendingBatch(t *testing.T) {
	col := &collector{}
	r := NewDebounceRouter("bot", 200*time.Millisecond, col.send)

	events := make(chan Event, 8)
	r.Start(events)

	events <- MessageReceived{Message: makeMsg("alice", "first", nil)}
	time.Sleep(10 * time.Millisecond)

	events <- MessageReceived{Message: makeMsg("bob", "@bot second", []string{"bot"})}
	time.Sleep(10 * time.Millisecond)

	msgs := col.get()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d: %v", len(msgs), msgs)
	}
	if !strings.Contains(msgs[0], "alice:") {
		t.Errorf("first should be alice's batch, got %q", msgs[0])
	}
	if !strings.Contains(msgs[1], "bob:") {
		t.Errorf("second should be bob's mention, got %q", msgs[1])
	}

	close(events)
	r.Close()
}

func TestDebounceRouter_CloseFlushes(t *testing.T) {
	col := &collector{}
	r := NewDebounceRouter("bot", 5*time.Second, col.send)

	events := make(chan Event, 8)
	r.Start(events)

	events <- MessageReceived{Message: makeMsg("alice", "pending", nil)}
	time.Sleep(10 * time.Millisecond)

	close(events)
	r.Close()

	msgs := col.get()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 flushed message, got %d", len(msgs))
	}
}

func TestDebounceRouter_FormatsNameColonText(t *testing.T) {
	col := &collector{}
	r := NewDebounceRouter("bot", 50*time.Millisecond, col.send)

	events := make(chan Event, 8)
	r.Start(events)

	events <- MessageReceived{Message: makeMsg("alice", "@bot do this", []string{"bot"})}
	time.Sleep(10 * time.Millisecond)

	msgs := col.get()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	want := "alice: @bot do this"
	if msgs[0] != want {
		t.Errorf("got %q, want %q", msgs[0], want)
	}

	close(events)
	r.Close()
}

func TestDebounceRouter_DebounceResetsOnNewMessage(t *testing.T) {
	col := &collector{}
	r := NewDebounceRouter("bot", 50*time.Millisecond, col.send)

	events := make(chan Event, 8)
	r.Start(events)

	events <- MessageReceived{Message: makeMsg("alice", "msg1", nil)}
	time.Sleep(30 * time.Millisecond)

	events <- MessageReceived{Message: makeMsg("bob", "msg2", nil)}
	time.Sleep(30 * time.Millisecond)

	if msgs := col.get(); len(msgs) != 0 {
		t.Errorf("expected 0 messages (timer reset), got %d", len(msgs))
	}

	time.Sleep(40 * time.Millisecond)
	msgs := col.get()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 batched message, got %d", len(msgs))
	}
	if !strings.Contains(msgs[0], "alice:") || !strings.Contains(msgs[0], "bob:") {
		t.Errorf("expected both senders in batch, got %q", msgs[0])
	}

	close(events)
	r.Close()
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/room/ -run TestDebounceRouter -v`
Expected: FAIL — `NewDebounceRouter` not defined.

- [ ] **Step 3: Write the implementation**

Create `internal/room/router.go`:

```go
package room

import (
	"fmt"
	"sync"
	"time"

	"github.com/khaiql/parley/internal/protocol"
)

// MessageRouter consumes room events and routes chat messages to a destination.
type MessageRouter interface {
	Start(events <-chan Event)
	Close()
}

// DebounceRouter routes incoming chat messages to an agent driver.
// @mentioned messages are delivered immediately; non-mentioned messages are
// batched with a debounce delay to avoid flooding the agent.
type DebounceRouter struct {
	agentName string
	delay     time.Duration
	send      func(string)

	mu           sync.Mutex
	pendingMsg   string
	pendingTimer *time.Timer
	done         chan struct{}
}

func NewDebounceRouter(agentName string, delay time.Duration, send func(string)) *DebounceRouter {
	return &DebounceRouter{
		agentName: agentName,
		delay:     delay,
		send:      send,
	}
}

func (r *DebounceRouter) Start(events <-chan Event) {
	r.done = make(chan struct{})
	go func() {
		defer close(r.done)
		for evt := range events {
			if msg, ok := evt.(MessageReceived); ok {
				r.route(msg.Message)
			}
		}
		r.flush()
	}()
}

func (r *DebounceRouter) Close() {
	if r.done != nil {
		<-r.done
	}
}

func (r *DebounceRouter) route(msg protocol.MessageParams) {
	if msg.From == r.agentName {
		return
	}

	text := ""
	if len(msg.Content) > 0 {
		text = msg.Content[0].Text
	}
	formatted := fmt.Sprintf("%s: %s", msg.From, text)
	mentioned := isMentioned(msg.Mentions, r.agentName)

	r.mu.Lock()
	defer r.mu.Unlock()

	if mentioned {
		if r.pendingTimer != nil {
			r.pendingTimer.Stop()
			r.pendingTimer = nil
		}
		if r.pendingMsg != "" {
			r.send(r.pendingMsg)
			r.pendingMsg = ""
		}
		r.send(formatted)
		return
	}

	if r.pendingMsg != "" {
		r.pendingMsg += "\n" + formatted
	} else {
		r.pendingMsg = formatted
	}

	if r.pendingTimer == nil {
		r.pendingTimer = time.AfterFunc(r.delay, func() {
			r.flush()
		})
	} else {
		r.pendingTimer.Reset(r.delay)
	}
}

func (r *DebounceRouter) flush() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pendingTimer != nil {
		r.pendingTimer.Stop()
		r.pendingTimer = nil
	}
	if r.pendingMsg != "" {
		r.send(r.pendingMsg)
		r.pendingMsg = ""
	}
}

func isMentioned(mentions []string, name string) bool {
	for _, m := range mentions {
		if m == name {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/room/ -run TestDebounceRouter -v -race`
Expected: All 7 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/room/router.go internal/room/router_test.go
git commit -m "feat(room): add DebounceRouter — event-driven message routing"
```

---

### Task 3: Split `cmd/parley/main.go` into Focused Files

**Files:**
- Modify: `cmd/parley/main.go`
- Create: `cmd/parley/host.go`
- Create: `cmd/parley/join.go`
- Create: `cmd/parley/export.go`

Split before wiring the router so subsequent tasks edit the right file.

- [ ] **Step 1: Create `cmd/parley/host.go`**

Move from main.go to host.go:
- `hostTopic`, `hostPort`, `hostResume`, `hostYolo` vars
- `hostCmd` var
- `runHost` function
- `RoomAdapter` struct and all its methods

The `init()` block for `hostCmd` flags stays in main.go (or moves to host.go — either is fine as long as `init()` calls `rootCmd.AddCommand(hostCmd)`).

- [ ] **Step 2: Create `cmd/parley/join.go`**

Move from main.go to join.go:
- `joinPort`, `joinName`, `joinRole`, `joinResume` vars
- `joinCmd` var
- `runJoin` function
- `randomName` function
- `isMentioned` function (will be deleted in Task 4 anyway)
- `contentText` function (will be deleted in Task 4 anyway)
- `bridgeNetworkToAgent` function (will be deleted in Task 4 anyway)

- [ ] **Step 3: Create `cmd/parley/export.go`**

Move from main.go to export.go:
- `exportOutput` var
- `exportCmd` var
- `runExport` function

- [ ] **Step 4: Slim down `main.go`**

What remains in main.go:
- `package main`
- `main()` function
- `rootCmd` var
- `init()` — registers flags for all commands, calls `rootCmd.AddCommand(hostCmd, joinCmd, exportCmd)`
- `detectRepo()` helper (used by both host and join)

Alternatively, move each command's `init()` into its own file using Go's multiple `init()` feature — each file registers its own flags and calls `rootCmd.AddCommand()`.

- [ ] **Step 5: Verify it compiles and tests pass**

Run: `go build ./... && go test ./... -timeout 30s -race`
Expected: All pass — this is a pure code move, no behavior change.

- [ ] **Step 6: Commit**

```bash
git add cmd/parley/main.go cmd/parley/host.go cmd/parley/join.go cmd/parley/export.go
git commit -m "refactor: split cmd/parley/main.go into host.go, join.go, export.go"
```

---

### Task 4: Wire DebounceRouter and Slim Down RoomAdapter

**Files:**
- Modify: `cmd/parley/join.go`
- Modify: `cmd/parley/host.go`

Replace `bridgeNetworkToAgent` with the DebounceRouter. Slim down `RoomAdapter` to delegate to `room.State` and only provide `GetPort()`. Delete dead code.

- [ ] **Step 1: Update `RoomAdapter` in host.go**

Replace the current `RoomAdapter` with:

```go
// RoomAdapter wraps *room.State to implement command.RoomQuerier,
// adding the transport-level port that room.State doesn't know about.
type RoomAdapter struct {
	state *room.State
	port  int
}

func (a *RoomAdapter) GetID() string                           { return a.state.GetID() }
func (a *RoomAdapter) GetTopic() string                        { return a.state.GetTopic() }
func (a *RoomAdapter) GetPort() int                            { return a.port }
func (a *RoomAdapter) GetParticipants() []protocol.Participant { return a.state.GetParticipants() }
func (a *RoomAdapter) GetMessageCount() int                    { return a.state.GetMessageCount() }
```

- [ ] **Step 2: Update `runHost` to use new RoomAdapter**

In `runHost`, reorder so `roomState` is created before `cmdCtx`:

```go
roomState := room.New(reg, cmdCtx)
```

Wait — `cmdCtx` needs `RoomAdapter` which needs `roomState`. Circular. Break the cycle:

1. Create `roomState` first with nil `cmdCtx`
2. Create `RoomAdapter` wrapping `roomState`
3. Create `cmdCtx` with the adapter
4. Set the command registry and context on `roomState`

```go
roomState := room.New(nil, command.Context{})
roomState.SetSendFn(sendFn)
if hostYolo {
	roomState.SetAutoApprove(true)
}

cmdCtx := command.Context{
	Room: &RoomAdapter{state: roomState, port: port},
	SaveFn: func() error {
		return server.SaveRoom(roomDir, srv.Room())
	},
	SendFn: func(to, text string) {
		_ = c.Send(protocol.Content{Type: "text", Text: fmt.Sprintf("@%s %s", to, text)}, []string{to})
	},
}

reg := command.NewRegistry()
reg.Register(command.InfoCommand)
reg.Register(command.SaveCommand)
reg.Register(command.SendCommandCommand)

roomState.SetCommands(reg, cmdCtx)
app := tui.NewApp(hostTopic, port, tui.InputModeHuman, name, sendFn)
app.SetCommandRegistry(reg, cmdCtx)
app.SetRoomState(roomState)
```

Note: `room.State` needs a `SetCommands(reg, ctx)` method. Add it in state.go:

```go
func (s *State) SetCommands(reg *command.Registry, ctx command.Context) {
	s.commands = reg
	s.cmdCtx = ctx
}
```

- [ ] **Step 3: Replace bridgeNetworkToAgent in join.go**

Replace:

```go
go bridgeNetworkToAgent(c, rs, d, p, joinName)
```

With:

```go
router := room.NewDebounceRouter(joinName, 2*time.Second, func(text string) {
	_ = d.Send(text)
})
router.Start(rs.Subscribe())

go func() {
	for msg := range c.Incoming() {
		rs.HandleServerMessage(msg)
	}
	p.Send(tui.ServerDisconnectedMsg{})
}()
```

- [ ] **Step 4: Delete dead code from join.go**

Remove:
- `bridgeNetworkToAgent` function
- `isMentioned` function
- `contentText` function

- [ ] **Step 5: Verify it compiles and tests pass**

Run: `go build ./... && go test ./... -timeout 30s -race`
Expected: All pass.

- [ ] **Step 6: Commit**

```bash
git add cmd/parley/host.go cmd/parley/join.go internal/room/state.go
git commit -m "refactor: wire DebounceRouter, slim down RoomAdapter, delete bridgeNetworkToAgent"
```

---

### Task 5: Extract `Server` Interface, Rename to `TcpServer`

**Files:**
- Create: `internal/server/interfaces.go`
- Modify: `internal/server/server.go`
- Modify: `internal/server/server_test.go`
- Modify: `cmd/parley/host.go`

- [ ] **Step 1: Create the interface file**

Create `internal/server/interfaces.go`:

```go
package server

// Server is the interface for a Parley chat server.
type Server interface {
	Addr() string
	Port() int
	Room() *Room
	Serve()
	Close() error
}

var _ Server = (*TcpServer)(nil)
```

- [ ] **Step 2: Rename `Server` to `TcpServer` in server.go**

- Rename struct `Server` → `TcpServer`
- Update `New` and `NewWithRoom` return types to `*TcpServer`
- Update all method receivers `(s *Server)` → `(s *TcpServer)`

- [ ] **Step 3: Update server_test.go**

Replace any explicit `*Server` type references with `*TcpServer`.

- [ ] **Step 4: Update host.go**

Update the variable declaration in `runHost`:

```go
var srv *server.TcpServer
```

- [ ] **Step 5: Verify it compiles and tests pass**

Run: `go build ./... && go test ./... -timeout 30s -race`
Expected: All pass.

- [ ] **Step 6: Commit**

```bash
git add internal/server/interfaces.go internal/server/server.go internal/server/server_test.go cmd/parley/host.go
git commit -m "refactor(server): extract Server interface, rename to TcpServer"
```

---

### Task 6: Extract `Client` Interface, Rename to `TcpClient`

**Files:**
- Create: `internal/client/interfaces.go`
- Modify: `internal/client/client.go`

- [ ] **Step 1: Create the interface file**

Create `internal/client/interfaces.go`:

```go
package client

import "github.com/khaiql/parley/internal/protocol"

// Client is the interface for connecting to a Parley server.
type Client interface {
	Incoming() <-chan *protocol.RawMessage
	Join(params protocol.JoinParams) error
	Send(content protocol.Content, mentions []string) error
	SendStatus(name, status string) error
	Close() error
}

var _ Client = (*TcpClient)(nil)
```

- [ ] **Step 2: Rename `Client` to `TcpClient` in client.go**

- Rename struct `Client` → `TcpClient`
- Update `New` return type to `*TcpClient`
- Update all method receivers `(c *Client)` → `(c *TcpClient)`

- [ ] **Step 3: Verify it compiles and all tests pass**

`cmd/parley/host.go` and `join.go` use `client.New()` via `:=` — Go infers the concrete type. All method calls are on the interface. No changes needed.

Run: `go build ./... && go test ./... -timeout 30s -race`
Expected: All pass.

- [ ] **Step 4: Commit**

```bash
git add internal/client/interfaces.go internal/client/client.go
git commit -m "refactor(client): extract Client interface, rename to TcpClient"
```

---

### Task 7: Final Verification

- [ ] **Step 1: Run full test suite with race detector**

```bash
go test ./... -timeout 30s -race
```

Expected: All pass.

- [ ] **Step 2: Run linter**

```bash
go run github.com/golangci/golangci-lint/cmd/golangci-lint@latest run ./... --timeout=5m
```

Expected: Clean.

- [ ] **Step 3: Build**

```bash
go build -o parley ./cmd/parley
```

Expected: Success.

- [ ] **Step 4: Verify dead code is gone**

```bash
grep -rn 'bridgeNetworkToAgent\|func isMentioned\|func contentText' cmd/parley/
```

Expected: No output.

- [ ] **Step 5: Verify interface compliance**

```bash
grep -rn 'var _ Server\|var _ Client' internal/
```

Expected: Two lines — one in `server/interfaces.go`, one in `client/interfaces.go`.

- [ ] **Step 6: Run e2e verification**

Use the `agent-tui` skill to run end-to-end smoke tests:

1. Build the binary: `go build -o parley ./cmd/parley`
2. Host a room: `agent-tui run ./parley host --topic "e2e test"`
3. Wait for the TUI to render, verify the room starts
4. In a separate session, join an agent: `agent-tui run ./parley join --port <port> -- echo "hello"`
5. Verify the agent joins and messages flow between host and agent
6. Kill both sessions: `agent-tui kill`

This confirms the router wiring, server/client communication, and TUI rendering all work end-to-end after the refactor.

---

## What Gets Cleaned Up

| Before | After |
|--------|-------|
| `bridgeNetworkToAgent` — 50-line function mixing state dispatch + message routing + debounce | Deleted entirely |
| `isMentioned` in main.go | Deleted (lives in room package) |
| `contentText` in main.go | Deleted (not needed) |
| `RoomAdapter` — wraps `*server.Room`, 12 methods | Wraps `*room.State` + port, 5 one-liner methods |
| `cmd/parley/main.go` — 620 lines doing everything | Split into main.go (~50 lines), host.go, join.go, export.go |
| Join incoming loop — interleaves state dispatch with agent routing | 3 lines: just feeds `room.State` |
| `server.Server` concrete type | `Server` interface + `TcpServer` implementation |
| `client.Client` concrete type | `Client` interface + `TcpClient` implementation |
| `room.State` has no room metadata | `room.State` tracks roomID and topic |
