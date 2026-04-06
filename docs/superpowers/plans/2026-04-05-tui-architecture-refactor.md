# TUI Architecture Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract room business logic from the monolithic `internal/tui/app.go` into a pure `internal/room/` package with channel-based event pub/sub, then convert the TUI into a thin shell that builds its own state from events.

**Architecture:** Core/Shell split. `internal/room/` owns room state and emits typed events over Go channels. `internal/tui/` subscribes to events via `program.Send()`, builds its own local state, and renders from it. Input modes managed by `qmuntal/stateless` FSM. Key routing uses three-layer consumed-bool pattern.

**Tech Stack:** Go, Bubble Tea, Lipgloss, `github.com/qmuntal/stateless`

**Spec:** `docs/superpowers/specs/2026-04-05-tui-architecture-refactor-design.md`

---

## File Structure

### New files

| File | Responsibility |
|---|---|
| `internal/room/state.go` | Room state struct, constructor, query methods |
| `internal/room/events.go` | Event types, Activity enum, channel pub/sub |
| `internal/room/dispatch.go` | `HandleServerMessage` — translates protocol messages to events |
| `internal/room/commands.go` | `ExecuteCommand` — command registry integration |
| `internal/room/state_test.go` | Event contract tests: subscribe, feed messages, assert events |
| `internal/room/dispatch_test.go` | Server message dispatch tests |
| `internal/room/commands_test.go` | Command execution tests |
| `internal/tui/inputfsm.go` | Input FSM with `qmuntal/stateless` |
| `internal/tui/inputfsm_test.go` | FSM state transition tests |

### Modified files

| File | Change |
|---|---|
| `internal/tui/app.go` | Replace component-owned state with TUI-local state built from events. Replace handleServerMsg with event handlers. Replace suggestion logic with FSM. Add three-layer key routing. |
| `internal/tui/app_test.go` | Update tests to use room.State events instead of direct ServerMsg |
| `internal/tui/sidebar.go` | Remove participant/status data ownership. Accept data as View params. |
| `internal/tui/sidebar_test.go` | Update to pass data to View instead of calling setters |
| `internal/tui/chat.go` | Remove message ownership. Accept messages as View params. |
| `internal/tui/chat_test.go` | Update accordingly |
| `internal/tui/statusbar.go` | Remove yolo ownership. Accept as View param. |
| `internal/tui/topbar.go` | Remove topic/agent ownership. Accept as View params. |
| `cmd/parley/main.go` | Create `room.State`, subscribe TUI to events, wire event bridge |
| `go.mod` | Add `github.com/qmuntal/stateless` |

---

## Task 1: Create `internal/room/events.go` — Event types and pub/sub

This task establishes the event system foundation. No behavioral logic yet — just types, the Activity enum, and the Subscribe/emit mechanism.

**Files:**
- Create: `internal/room/events.go`
- Create: `internal/room/events_test.go`

- [ ] **Step 1: Write the failing test for Subscribe and emit**

```go
// internal/room/events_test.go
package room

import (
	"testing"
	"time"
)

func TestSubscribe_ReceivesEmittedEvents(t *testing.T) {
	s := &State{}
	ch := s.Subscribe()

	s.emit(ParticipantsChanged{
		Participants: nil,
	})

	select {
	case e := <-ch:
		if _, ok := e.(ParticipantsChanged); !ok {
			t.Fatalf("expected ParticipantsChanged, got %T", e)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for event")
	}
}

func TestSubscribe_MultipleSubscribers(t *testing.T) {
	s := &State{}
	ch1 := s.Subscribe()
	ch2 := s.Subscribe()

	s.emit(MessageReceived{})

	for _, ch := range []<-chan Event{ch1, ch2} {
		select {
		case e := <-ch:
			if _, ok := e.(MessageReceived); !ok {
				t.Fatalf("expected MessageReceived, got %T", e)
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("timed out waiting for event")
		}
	}
}

func TestEmit_DropsWhenChannelFull(t *testing.T) {
	s := &State{}
	ch := s.Subscribe()

	// Fill the channel buffer (64)
	for i := 0; i < 64; i++ {
		s.emit(MessageReceived{})
	}
	// This should not block — drops with log warning
	s.emit(MessageReceived{})

	// Drain and count
	count := 0
	for {
		select {
		case <-ch:
			count++
		default:
			goto done
		}
	}
done:
	if count != 64 {
		t.Fatalf("expected 64 events in channel, got %d", count)
	}
}

func TestActivity_Constants(t *testing.T) {
	if ActivityListening != 0 {
		t.Fatal("ActivityListening should be 0")
	}
	if ActivityGenerating != 2 {
		t.Fatal("ActivityGenerating should be 2")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/room/ -run TestSubscribe -v`
Expected: FAIL — package does not exist yet

- [ ] **Step 3: Write events.go with types and pub/sub**

```go
// internal/room/events.go
package room

import (
	"log"

	"github.com/khaiql/parley/internal/protocol"
)

// Event is the interface for all room events.
type Event interface{}

// Activity represents a participant's current activity state.
type Activity int

const (
	ActivityListening  Activity = iota
	ActivityThinking
	ActivityGenerating
	ActivityUsingTool
)

// ParticipantsChanged is emitted when the participant list changes (join/leave).
type ParticipantsChanged struct {
	Participants []protocol.Participant
}

// MessageReceived is emitted for a single new live message.
type MessageReceived struct {
	Message protocol.MessageParams
}

// HistoryLoaded is emitted once on join with the full room state.
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

// PermissionRequested is emitted when an agent requests permission.
type PermissionRequested struct {
	Request PermissionRequest
}

// PermissionResolved is emitted when a permission request is resolved.
type PermissionResolved struct {
	RequestID string
	Approved  bool
}

// ErrorOccurred is emitted when the core encounters a non-fatal error.
type ErrorOccurred struct {
	Error error
}

// PermissionRequest holds details of a pending permission request.
type PermissionRequest struct {
	ID        string
	AgentName string
	Tool      string
	Args      string
}

// Subscribe returns a buffered channel that receives all events emitted by this State.
func (s *State) Subscribe() <-chan Event {
	ch := make(chan Event, 64)
	s.subscribers = append(s.subscribers, ch)
	return ch
}

// emit sends events to all subscribers. Never blocks — drops with log warning if full.
func (s *State) emit(events ...Event) {
	for _, e := range events {
		for _, ch := range s.subscribers {
			select {
			case ch <- e:
			default:
				log.Println("WARN: subscriber channel full, dropping event")
			}
		}
	}
}
```

- [ ] **Step 4: Create minimal state.go so the package compiles**

```go
// internal/room/state.go
package room

// State holds all room data. Mutations emit events through channels.
type State struct {
	subscribers []chan Event
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/room/ -v`
Expected: PASS — all 4 tests pass

- [ ] **Step 6: Commit**

```bash
git add internal/room/events.go internal/room/events_test.go internal/room/state.go
git commit -m "feat(room): add event types, Activity enum, and channel pub/sub"
```

---

## Task 2: Create `internal/room/state.go` — State struct, constructor, and query methods

Build the full State struct with fields matching the spec. Add query methods. No mutation logic yet — that comes in Task 3.

**Files:**
- Modify: `internal/room/state.go`
- Create: `internal/room/state_test.go`

- [ ] **Step 1: Write failing tests for State constructor and query methods**

```go
// internal/room/state_test.go
package room

import (
	"testing"

	"github.com/khaiql/parley/internal/protocol"
)

func TestNew_ReturnsEmptyState(t *testing.T) {
	s := New(nil, nil)
	if len(s.Participants()) != 0 {
		t.Fatal("expected no participants")
	}
	if len(s.Messages()) != 0 {
		t.Fatal("expected no messages")
	}
	if s.IsAnyoneGenerating() {
		t.Fatal("expected no one generating")
	}
	if s.AutoApprove() {
		t.Fatal("expected autoApprove false")
	}
}

func TestIsAnyoneGenerating_TrueWhenGenerating(t *testing.T) {
	s := New(nil, nil)
	s.activities["agent-1"] = ActivityGenerating
	if !s.IsAnyoneGenerating() {
		t.Fatal("expected IsAnyoneGenerating to be true")
	}
}

func TestIsAnyoneGenerating_FalseWhenThinking(t *testing.T) {
	s := New(nil, nil)
	s.activities["agent-1"] = ActivityThinking
	if s.IsAnyoneGenerating() {
		t.Fatal("expected IsAnyoneGenerating to be false")
	}
}

func TestParticipantActivity_ReturnsListeningByDefault(t *testing.T) {
	s := New(nil, nil)
	if s.ParticipantActivity("unknown") != ActivityListening {
		t.Fatal("expected ActivityListening for unknown participant")
	}
}

func TestParticipants_ReturnsCopy(t *testing.T) {
	s := New(nil, nil)
	s.participants = []protocol.Participant{
		{Name: "alice", Online: true},
	}
	ps := s.Participants()
	ps[0].Name = "modified"
	if s.participants[0].Name != "alice" {
		t.Fatal("Participants() should return a copy, not a reference")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/room/ -run TestNew -v`
Expected: FAIL — `New` not defined

- [ ] **Step 3: Implement State struct and query methods**

```go
// internal/room/state.go
package room

import (
	"github.com/khaiql/parley/internal/command"
	"github.com/khaiql/parley/internal/protocol"
)

// State holds all room data. Mutations emit events through channels.
type State struct {
	participants []protocol.Participant
	activities   map[string]Activity
	messages     []protocol.MessageParams
	permissions  []PermissionRequest
	commands     *command.Registry
	cmdCtx       command.Context
	autoApprove  bool
	sendFn       func(string, []string)

	subscribers []chan Event
}

// New creates a new State with the given command registry and context.
func New(registry *command.Registry, ctx command.Context) *State {
	return &State{
		activities: make(map[string]Activity),
		commands:   registry,
		cmdCtx:     ctx,
	}
}

// SetSendFn sets the callback used to send messages over the network.
func (s *State) SetSendFn(fn func(string, []string)) {
	s.sendFn = fn
}

// SetAutoApprove sets the auto-approve mode.
func (s *State) SetAutoApprove(v bool) {
	s.autoApprove = v
}

// Participants returns a copy of the current participant list.
func (s *State) Participants() []protocol.Participant {
	cp := make([]protocol.Participant, len(s.participants))
	copy(cp, s.participants)
	return cp
}

// ParticipantActivity returns the activity for a named participant.
// Returns ActivityListening if the participant is not found.
func (s *State) ParticipantActivity(name string) Activity {
	if a, ok := s.activities[name]; ok {
		return a
	}
	return ActivityListening
}

// IsAnyoneGenerating returns true if any participant has ActivityGenerating.
func (s *State) IsAnyoneGenerating() bool {
	for _, a := range s.activities {
		if a == ActivityGenerating {
			return true
		}
	}
	return false
}

// Messages returns a copy of the message history.
func (s *State) Messages() []protocol.MessageParams {
	cp := make([]protocol.MessageParams, len(s.messages))
	copy(cp, s.messages)
	return cp
}

// AvailableCommands returns command info for the registered commands.
func (s *State) AvailableCommands() []command.Command {
	if s.commands == nil {
		return nil
	}
	return s.commands.Commands()
}

// PendingPermissions returns the current pending permission requests.
func (s *State) PendingPermissions() []PermissionRequest {
	cp := make([]PermissionRequest, len(s.permissions))
	copy(cp, s.permissions)
	return cp
}

// AutoApprove returns whether auto-approve mode is enabled.
func (s *State) AutoApprove() bool {
	return s.autoApprove
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/room/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/room/state.go internal/room/state_test.go
git commit -m "feat(room): add State struct with constructor and query methods"
```

---

## Task 3: Create `internal/room/dispatch.go` — HandleServerMessage with event contract tests

This is the core business logic extraction. Move the dispatch logic from `internal/tui/app.go:handleServerMsg` (lines 421-468) into `room.State.HandleServerMessage`. Each protocol method emits the corresponding typed event.

**Files:**
- Create: `internal/room/dispatch.go`
- Create: `internal/room/dispatch_test.go`

- [ ] **Step 1: Write failing event contract tests**

These tests subscribe to State, feed it raw protocol messages, and assert on the events emitted. This is the regression safety net for the entire refactor.

```go
// internal/room/dispatch_test.go
package room

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/khaiql/parley/internal/protocol"
)

func rawMsg(t *testing.T, method string, params interface{}) *protocol.RawMessage {
	t.Helper()
	p, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	return &protocol.RawMessage{Method: method, Params: p}
}

func nextEvent(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for event")
		return nil
	}
}

func noMoreEvents(t *testing.T, ch <-chan Event) {
	t.Helper()
	select {
	case e := <-ch:
		t.Fatalf("unexpected event: %T", e)
	case <-time.After(20 * time.Millisecond):
		// good — no events
	}
}

func TestHandleServerMessage_RoomMessage_EmitsMessageReceived(t *testing.T) {
	s := New(nil, nil)
	ch := s.Subscribe()

	msg := protocol.MessageParams{
		From:    "alice",
		Content: []protocol.Content{{Type: "text", Text: "hello"}},
	}
	s.HandleServerMessage(rawMsg(t, "room.message", msg))

	e := nextEvent(t, ch)
	mr, ok := e.(MessageReceived)
	if !ok {
		t.Fatalf("expected MessageReceived, got %T", e)
	}
	if mr.Message.From != "alice" {
		t.Fatalf("expected from alice, got %s", mr.Message.From)
	}
	// Verify message is stored internally
	if len(s.messages) != 1 {
		t.Fatal("expected 1 stored message")
	}
}

func TestHandleServerMessage_RoomJoined_EmitsParticipantsChanged(t *testing.T) {
	s := New(nil, nil)
	ch := s.Subscribe()

	s.HandleServerMessage(rawMsg(t, "room.joined", protocol.JoinedParams{
		Name: "bob", Role: "agent", AgentType: "claude",
	}))

	e := nextEvent(t, ch)
	pc, ok := e.(ParticipantsChanged)
	if !ok {
		t.Fatalf("expected ParticipantsChanged, got %T", e)
	}
	if len(pc.Participants) != 1 || pc.Participants[0].Name != "bob" {
		t.Fatal("expected bob in participants")
	}
	if !pc.Participants[0].Online {
		t.Fatal("expected bob to be online")
	}
}

func TestHandleServerMessage_RoomLeft_EmitsParticipantsChanged(t *testing.T) {
	s := New(nil, nil)
	s.participants = []protocol.Participant{{Name: "bob", Online: true}}
	ch := s.Subscribe()

	s.HandleServerMessage(rawMsg(t, "room.left", protocol.LeftParams{Name: "bob"}))

	e := nextEvent(t, ch)
	pc, ok := e.(ParticipantsChanged)
	if !ok {
		t.Fatalf("expected ParticipantsChanged, got %T", e)
	}
	if pc.Participants[0].Online {
		t.Fatal("expected bob to be offline")
	}
}

func TestHandleServerMessage_RoomStatus_EmitsParticipantActivityChanged(t *testing.T) {
	s := New(nil, nil)
	ch := s.Subscribe()

	s.HandleServerMessage(rawMsg(t, "room.status", protocol.StatusParams{
		Name: "claude", Status: "generating",
	}))

	e := nextEvent(t, ch)
	ac, ok := e.(ParticipantActivityChanged)
	if !ok {
		t.Fatalf("expected ParticipantActivityChanged, got %T", e)
	}
	if ac.Name != "claude" {
		t.Fatalf("expected claude, got %s", ac.Name)
	}
	if ac.Activity != ActivityGenerating {
		t.Fatalf("expected ActivityGenerating, got %d", ac.Activity)
	}
}

func TestHandleServerMessage_RoomState_EmitsHistoryLoaded(t *testing.T) {
	s := New(nil, nil)
	ch := s.Subscribe()

	s.HandleServerMessage(rawMsg(t, "room.state", protocol.RoomStateParams{
		Participants: []protocol.Participant{{Name: "alice", Online: true}},
		Messages: []protocol.MessageParams{
			{From: "alice", Content: []protocol.Content{{Type: "text", Text: "hi"}}},
		},
		AutoApprove: true,
	}))

	e := nextEvent(t, ch)
	hl, ok := e.(HistoryLoaded)
	if !ok {
		t.Fatalf("expected HistoryLoaded, got %T", e)
	}
	if len(hl.Messages) != 1 {
		t.Fatal("expected 1 message in history")
	}
	if len(hl.Participants) != 1 {
		t.Fatal("expected 1 participant")
	}
	if s.autoApprove != true {
		t.Fatal("expected autoApprove to be set")
	}
	noMoreEvents(t, ch)
}

func TestHandleServerMessage_Ordering_ParticipantBeforeMessage(t *testing.T) {
	s := New(nil, nil)
	ch := s.Subscribe()

	// Simulate room.joined then room.message from the same person
	s.HandleServerMessage(rawMsg(t, "room.joined", protocol.JoinedParams{Name: "new-agent"}))
	s.HandleServerMessage(rawMsg(t, "room.message", protocol.MessageParams{From: "new-agent"}))

	e1 := nextEvent(t, ch)
	if _, ok := e1.(ParticipantsChanged); !ok {
		t.Fatalf("expected ParticipantsChanged first, got %T", e1)
	}
	e2 := nextEvent(t, ch)
	if _, ok := e2.(MessageReceived); !ok {
		t.Fatalf("expected MessageReceived second, got %T", e2)
	}
}

func TestHandleServerMessage_NilRaw_NoOp(t *testing.T) {
	s := New(nil, nil)
	ch := s.Subscribe()
	s.HandleServerMessage(nil)
	noMoreEvents(t, ch)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/room/ -run TestHandleServerMessage -v`
Expected: FAIL — `HandleServerMessage` not defined

- [ ] **Step 3: Implement dispatch.go**

```go
// internal/room/dispatch.go
package room

import (
	"encoding/json"

	"github.com/khaiql/parley/internal/protocol"
)

// ParseActivity converts a protocol status string to an Activity enum.
func ParseActivity(status string) Activity {
	switch status {
	case "generating":
		return ActivityGenerating
	case "thinking":
		return ActivityThinking
	case "using_tool":
		return ActivityUsingTool
	default:
		return ActivityListening
	}
}

// HandleServerMessage dispatches an incoming RawMessage to the appropriate
// handler and emits events for state changes.
func (s *State) HandleServerMessage(raw *protocol.RawMessage) {
	if raw == nil {
		return
	}
	switch raw.Method {
	case "room.state":
		s.handleRoomState(raw.Params)
	case "room.message":
		s.handleRoomMessage(raw.Params)
	case "room.joined":
		s.handleRoomJoined(raw.Params)
	case "room.left":
		s.handleRoomLeft(raw.Params)
	case "room.status":
		s.handleRoomStatus(raw.Params)
	}
}

func (s *State) handleRoomState(data json.RawMessage) {
	var params protocol.RoomStateParams
	if err := json.Unmarshal(data, &params); err != nil {
		s.emit(ErrorOccurred{Error: err})
		return
	}
	s.participants = params.Participants
	s.autoApprove = params.AutoApprove

	// Build fresh copies for the consumer to take ownership of
	msgs := make([]protocol.MessageParams, len(params.Messages))
	copy(msgs, params.Messages)

	ps := make([]protocol.Participant, len(s.participants))
	copy(ps, s.participants)

	acts := make(map[string]Activity, len(s.activities))
	for k, v := range s.activities {
		acts[k] = v
	}

	s.emit(HistoryLoaded{
		Messages:     msgs,
		Participants: ps,
		Activities:   acts,
	})
}

func (s *State) handleRoomMessage(data json.RawMessage) {
	var params protocol.MessageParams
	if err := json.Unmarshal(data, &params); err != nil {
		s.emit(ErrorOccurred{Error: err})
		return
	}
	s.messages = append(s.messages, params)
	s.emit(MessageReceived{Message: params})
}

func (s *State) handleRoomJoined(data json.RawMessage) {
	var params protocol.JoinedParams
	if err := json.Unmarshal(data, &params); err != nil {
		s.emit(ErrorOccurred{Error: err})
		return
	}
	p := protocol.Participant{
		Name:      params.Name,
		Role:      params.Role,
		Directory: params.Directory,
		Repo:      params.Repo,
		AgentType: params.AgentType,
		Online:    true,
	}
	// Replace existing or append
	found := false
	for i, existing := range s.participants {
		if existing.Name == p.Name {
			s.participants[i] = p
			found = true
			break
		}
	}
	if !found {
		s.participants = append(s.participants, p)
	}

	ps := make([]protocol.Participant, len(s.participants))
	copy(ps, s.participants)
	s.emit(ParticipantsChanged{Participants: ps})
}

func (s *State) handleRoomLeft(data json.RawMessage) {
	var params protocol.LeftParams
	if err := json.Unmarshal(data, &params); err != nil {
		s.emit(ErrorOccurred{Error: err})
		return
	}
	for i, p := range s.participants {
		if p.Name == params.Name {
			s.participants[i].Online = false
			break
		}
	}

	ps := make([]protocol.Participant, len(s.participants))
	copy(ps, s.participants)
	s.emit(ParticipantsChanged{Participants: ps})
}

func (s *State) handleRoomStatus(data json.RawMessage) {
	var params protocol.StatusParams
	if err := json.Unmarshal(data, &params); err != nil {
		s.emit(ErrorOccurred{Error: err})
		return
	}
	activity := ParseActivity(params.Status)
	s.activities[params.Name] = activity
	s.emit(ParticipantActivityChanged{
		Name:     params.Name,
		Activity: activity,
	})
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/room/ -v`
Expected: PASS — all event contract tests pass

- [ ] **Step 5: Run full test suite to verify no regressions**

Run: `go test ./... -timeout 30s`
Expected: All packages pass

- [ ] **Step 6: Commit**

```bash
git add internal/room/dispatch.go internal/room/dispatch_test.go
git commit -m "feat(room): add HandleServerMessage with event contract tests"
```

---

## Task 4: Create `internal/room/commands.go` — Command execution

Extract command execution from App.Update (lines 184-195 of app.go) into `room.State.ExecuteCommand`.

**Files:**
- Create: `internal/room/commands.go`
- Create: `internal/room/commands_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/room/commands_test.go
package room

import (
	"testing"

	"github.com/khaiql/parley/internal/command"
)

func TestExecuteCommand_ReturnsContent(t *testing.T) {
	reg := command.NewRegistry()
	reg.Register(command.Command{
		Name:        "test",
		Description: "a test command",
		Execute: func(ctx command.Context, args string) command.Result {
			return command.Result{
				Modal: &command.ModalContent{Title: "Test", Body: "body"},
			}
		},
	})
	s := New(reg, command.Context{})

	result := s.ExecuteCommand("/test")
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Modal == nil {
		t.Fatal("expected modal content")
	}
	if result.Modal.Title != "Test" {
		t.Fatalf("expected title Test, got %s", result.Modal.Title)
	}
}

func TestExecuteCommand_UnknownCommand(t *testing.T) {
	reg := command.NewRegistry()
	s := New(reg, command.Context{})

	result := s.ExecuteCommand("/unknown")
	if result.Error == nil {
		t.Fatal("expected error for unknown command")
	}
}

func TestExecuteCommand_NilRegistry(t *testing.T) {
	s := New(nil, command.Context{})
	result := s.ExecuteCommand("/test")
	if result.Error == nil {
		t.Fatal("expected error when no registry")
	}
}

func TestSendMessage_CallsSendFn(t *testing.T) {
	s := New(nil, command.Context{})
	var sentText string
	var sentMentions []string
	s.SetSendFn(func(text string, mentions []string) {
		sentText = text
		sentMentions = mentions
	})

	s.SendMessage("hello @bob", []string{"bob"})
	if sentText != "hello @bob" {
		t.Fatalf("expected 'hello @bob', got %q", sentText)
	}
	if len(sentMentions) != 1 || sentMentions[0] != "bob" {
		t.Fatalf("expected [bob], got %v", sentMentions)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/room/ -run TestExecuteCommand -v`
Expected: FAIL

- [ ] **Step 3: Implement commands.go**

```go
// internal/room/commands.go
package room

import (
	"errors"

	"github.com/khaiql/parley/internal/command"
)

// ExecuteCommand runs a slash command and returns the result.
func (s *State) ExecuteCommand(text string) command.Result {
	if s.commands == nil {
		return command.Result{Error: errors.New("no command registry configured")}
	}
	return s.commands.Execute(s.cmdCtx, text)
}

// SendMessage sends a message with mentions over the network via sendFn.
func (s *State) SendMessage(text string, mentions []string) {
	if s.sendFn != nil {
		s.sendFn(text, mentions)
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/room/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/room/commands.go internal/room/commands_test.go
git commit -m "feat(room): add ExecuteCommand and SendMessage"
```

---

## Task 5: Add `qmuntal/stateless` dependency and create Input FSM

**Files:**
- Modify: `go.mod`
- Create: `internal/tui/inputfsm.go`
- Create: `internal/tui/inputfsm_test.go`

- [ ] **Step 1: Add dependency**

```bash
go get github.com/qmuntal/stateless
```

- [ ] **Step 2: Write failing tests for the Input FSM**

```go
// internal/tui/inputfsm_test.go
package tui

import (
	"testing"
)

func TestInputFSM_StartsInNormal(t *testing.T) {
	fsm := NewInputFSM(func(InputTrigger) {}, func() {})
	if fsm.Current() != StateNormal {
		t.Fatalf("expected StateNormal, got %v", fsm.Current())
	}
}

func TestInputFSM_SlashTransitionsToCompleting(t *testing.T) {
	fsm := NewInputFSM(func(InputTrigger) {}, func() {})
	if err := fsm.Fire(TriggerSlash); err != nil {
		t.Fatal(err)
	}
	if fsm.Current() != StateCompleting {
		t.Fatalf("expected StateCompleting, got %v", fsm.Current())
	}
}

func TestInputFSM_MentionTransitionsToCompleting(t *testing.T) {
	fsm := NewInputFSM(func(InputTrigger) {}, func() {})
	if err := fsm.Fire(TriggerMention); err != nil {
		t.Fatal(err)
	}
	if fsm.Current() != StateCompleting {
		t.Fatalf("expected StateCompleting, got %v", fsm.Current())
	}
}

func TestInputFSM_AcceptReturnsToNormal(t *testing.T) {
	fsm := NewInputFSM(func(InputTrigger) {}, func() {})
	fsm.Fire(TriggerSlash)
	if err := fsm.Fire(TriggerAccept); err != nil {
		t.Fatal(err)
	}
	if fsm.Current() != StateNormal {
		t.Fatalf("expected StateNormal, got %v", fsm.Current())
	}
}

func TestInputFSM_DismissReturnsToNormal(t *testing.T) {
	fsm := NewInputFSM(func(InputTrigger) {}, func() {})
	fsm.Fire(TriggerSlash)
	if err := fsm.Fire(TriggerDismiss); err != nil {
		t.Fatal(err)
	}
	if fsm.Current() != StateNormal {
		t.Fatalf("expected StateNormal, got %v", fsm.Current())
	}
}

func TestInputFSM_OnEnterCalledWithTrigger(t *testing.T) {
	var gotTrigger InputTrigger
	fsm := NewInputFSM(func(trigger InputTrigger) {
		gotTrigger = trigger
	}, func() {})

	fsm.Fire(TriggerSlash)
	if gotTrigger != TriggerSlash {
		t.Fatalf("expected TriggerSlash, got %v", gotTrigger)
	}
}

func TestInputFSM_OnExitCalledOnDismiss(t *testing.T) {
	exitCalled := false
	fsm := NewInputFSM(func(InputTrigger) {}, func() {
		exitCalled = true
	})

	fsm.Fire(TriggerSlash)
	fsm.Fire(TriggerDismiss)
	if !exitCalled {
		t.Fatal("expected onExitCompleting to be called")
	}
}

func TestInputFSM_InvalidTransitionFromNormal(t *testing.T) {
	fsm := NewInputFSM(func(InputTrigger) {}, func() {})
	err := fsm.Fire(TriggerAccept) // can't accept in Normal state
	if err == nil {
		t.Fatal("expected error for invalid transition")
	}
	if fsm.Current() != StateNormal {
		t.Fatal("state should not change on invalid transition")
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/tui/ -run TestInputFSM -v`
Expected: FAIL

- [ ] **Step 4: Implement inputfsm.go**

```go
// internal/tui/inputfsm.go
package tui

import (
	"context"

	"github.com/qmuntal/stateless"
)

// InputState represents the current state of the input FSM.
type InputState int

const (
	StateNormal    InputState = iota
	StateCompleting
)

// InputTrigger represents events that cause state transitions.
type InputTrigger int

const (
	TriggerSlash   InputTrigger = iota // '/' typed at position 0
	TriggerMention                     // '@' typed after whitespace
	TriggerAccept                      // Tab pressed
	TriggerDismiss                     // Esc pressed
	TriggerSubmit                      // Enter pressed
)

// InputFSM manages input interaction modes using a formal state machine.
type InputFSM struct {
	machine *stateless.StateMachine
}

// NewInputFSM creates an InputFSM with injected callbacks.
// onEnterCompleting is called when transitioning to Completing state.
// onExitCompleting is called when leaving Completing state.
func NewInputFSM(
	onEnterCompleting func(trigger InputTrigger),
	onExitCompleting func(),
) *InputFSM {
	sm := stateless.NewStateMachine(StateNormal)

	sm.Configure(StateNormal).
		Permit(TriggerSlash, StateCompleting).
		Permit(TriggerMention, StateCompleting)

	sm.Configure(StateCompleting).
		Permit(TriggerAccept, StateNormal).
		Permit(TriggerDismiss, StateNormal).
		Permit(TriggerSubmit, StateNormal).
		OnEntry(func(_ context.Context, args ...any) error {
			if len(args) > 0 {
				if trigger, ok := args[0].(InputTrigger); ok {
					onEnterCompleting(trigger)
				}
			}
			return nil
		}).
		OnExit(func(_ context.Context, _ ...any) error {
			onExitCompleting()
			return nil
		})

	return &InputFSM{machine: sm}
}

// Current returns the current input state.
func (f *InputFSM) Current() InputState {
	return f.machine.MustState().(InputState)
}

// Fire triggers a state transition. Returns an error if the transition is invalid.
func (f *InputFSM) Fire(trigger InputTrigger) error {
	return f.machine.Fire(trigger, trigger)
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/tui/ -run TestInputFSM -v`
Expected: PASS

- [ ] **Step 6: Run full test suite**

Run: `go test ./... -timeout 30s`
Expected: All pass (existing TUI tests unaffected)

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum internal/tui/inputfsm.go internal/tui/inputfsm_test.go
git commit -m "feat(tui): add Input FSM using qmuntal/stateless"
```

---

## Task 6: Wire `room.State` into `cmd/parley/main.go`

Create `room.State` in the host and join commands. For now, both the old `handleServerMsg` path and the new `room.State` receive messages — the old path still drives the TUI. This is the bridge step that lets us incrementally migrate the TUI to event-based state in subsequent tasks.

**Files:**
- Modify: `cmd/parley/main.go`
- Modify: `internal/tui/app.go` (add `RoomState` field and setter)

- [ ] **Step 1: Add a RoomState field to App**

Add to `internal/tui/app.go`, in the App struct:

```go
roomState *room.State // room business logic (nil during migration)
```

Add setter method:

```go
// SetRoomState connects the App to a room.State for event-based updates.
func (a *App) SetRoomState(s *room.State) {
	a.roomState = s
}
```

- [ ] **Step 2: Create room.State in runHost**

In `cmd/parley/main.go`, in the `runHost` function, after the command registry is created but before `tui.NewApp`:

```go
roomState := room.New(reg, cmdCtx)
roomState.SetSendFn(sendFn)
```

After creating the App:

```go
a.SetRoomState(roomState)
```

In the existing goroutine that bridges `c.Incoming()` → `p.Send(tui.ServerMsg{Raw: msg})`, also feed the message to room.State:

```go
go func() {
	for msg := range c.Incoming() {
		roomState.HandleServerMessage(msg)
		p.Send(tui.ServerMsg{Raw: msg})
	}
	p.Send(tui.ServerDisconnectedMsg{})
}()
```

- [ ] **Step 3: Create room.State in runJoin**

Same pattern in `runJoin`. The join path doesn't have a command registry, so pass nil:

```go
roomState := room.New(nil, command.Context{})
```

Wire it the same way — `HandleServerMessage` in the incoming message loop.

- [ ] **Step 4: Run full test suite**

Run: `go test ./... -timeout 30s`
Expected: All pass — room.State runs in parallel with old code, no behavioral changes

- [ ] **Step 5: Commit**

```bash
git add cmd/parley/main.go internal/tui/app.go
git commit -m "feat: wire room.State into host and join commands (parallel with old path)"
```

---

## Task 7: Migrate App to event-sourced state — replace handleServerMsg

Replace `App.handleServerMsg` with room event handlers. App builds its own local state from events instead of managing components directly. The old `ServerMsg` case delegates to `room.State`, and new event cases update TUI-local state.

**Files:**
- Modify: `internal/tui/app.go`
- Modify: `internal/tui/app_test.go`

- [ ] **Step 1: Add TUI-local state fields to App**

Replace the old fields that were managed through components with TUI-owned copies. Add these fields to the App struct:

```go
// TUI-owned state, built from room events
localMessages     []protocol.MessageParams
localParticipants []protocol.Participant
localActivities   map[string]room.Activity
```

Initialize `localActivities` in `NewApp`:

```go
a.localActivities = make(map[string]room.Activity)
```

- [ ] **Step 2: Add room event handlers in Update**

Add cases in the `Update` switch for room events. These replace the old `ServerMsg` → `handleServerMsg` path:

```go
case room.HistoryLoaded:
	a.localMessages = m.Messages
	a.localParticipants = m.Participants
	a.localActivities = m.Activities
	a.sidebar.SetParticipants(m.Participants)
	a.chat.LoadMessages(m.Messages)
	a.statusbar.SetYolo(a.roomState.AutoApprove())
	return a, nil

case room.MessageReceived:
	a.localMessages = append(a.localMessages, m.Message)
	a.chat.AddMessage(m.Message)
	return a, nil

case room.ParticipantsChanged:
	a.localParticipants = m.Participants
	a.sidebar.SetParticipants(m.Participants)
	if isAnyGenerating(a.localActivities) {
		return a, a.maybeStartSpinnerFromActivities()
	}
	return a, nil

case room.ParticipantActivityChanged:
	a.localActivities[m.Name] = m.Activity
	a.sidebar.SetParticipantStatus(m.Name, activityToString(m.Activity))
	if m.Activity == room.ActivityGenerating {
		return a, a.maybeStartSpinnerFromActivities()
	}
	return a, nil

case room.ErrorOccurred:
	a.chat.AddMessage(systemMessage(m.Error.Error()))
	return a, nil
```

Add helper functions:

```go
func isAnyGenerating(activities map[string]room.Activity) bool {
	for _, a := range activities {
		if a == room.ActivityGenerating {
			return true
		}
	}
	return false
}

func activityToString(a room.Activity) string {
	switch a {
	case room.ActivityGenerating:
		return "generating"
	case room.ActivityThinking:
		return "thinking"
	case room.ActivityUsingTool:
		return "using tool"
	default:
		return ""
	}
}

func (a *App) maybeStartSpinnerFromActivities() tea.Cmd {
	if a.spinnerActive {
		return nil
	}
	if isAnyGenerating(a.localActivities) {
		a.spinnerActive = true
		return spinnerTick()
	}
	return nil
}
```

- [ ] **Step 3: Remove the old ServerMsg handler and handleServerMsg method**

In `Update`, change the `ServerMsg` case to delegate to `room.State` only (if wired):

```go
case ServerMsg:
	if a.roomState != nil {
		// room.State handles it and emits events — events arrive via program.Send
		return a, nil
	}
	// Fallback for tests that don't use room.State yet
	a.handleServerMsg(m.Raw)
	return a, a.maybeStartSpinner()
```

- [ ] **Step 4: Set up event bridge in main.go**

In `cmd/parley/main.go`, after creating the tea.Program, start the event bridge goroutine:

```go
// Bridge room events into Bubble Tea
events := roomState.Subscribe()
go func() {
	for e := range events {
		p.Send(e)
	}
}()
```

Remove the `roomState.HandleServerMessage(msg)` call from the existing incoming message goroutine — it's already there from Task 6. Keep only the `p.Send(tui.ServerMsg{Raw: msg})` for backward compat during migration, but the `ServerMsg` case in Update now just returns nil when roomState is set.

Actually, **keep both**: `roomState.HandleServerMessage(msg)` feeds the core, which emits events that the bridge goroutine picks up and sends to the TUI. The old `p.Send(tui.ServerMsg{Raw: msg})` can be removed since the TUI now handles room events directly.

- [ ] **Step 5: Update tests**

Update `app_test.go` to test the new event-based flow. Tests that previously used `ServerMsg` + `handleServerMsg` should now create a `room.State`, subscribe, and verify TUI state after room events:

```go
func TestApp_RoomMessageReceived_AddsToChat(t *testing.T) {
	a := makeApp()
	a.localActivities = make(map[string]room.Activity)

	msg := protocol.MessageParams{
		From:    "alice",
		Content: []protocol.Content{{Type: "text", Text: "hello"}},
	}
	a.Update(room.MessageReceived{Message: msg})

	if len(a.localMessages) != 1 {
		t.Fatalf("expected 1 local message, got %d", len(a.localMessages))
	}
}
```

- [ ] **Step 6: Run full test suite**

Run: `go test ./... -timeout 30s`
Expected: All pass

- [ ] **Step 7: Commit**

```bash
git add internal/tui/app.go internal/tui/app_test.go cmd/parley/main.go
git commit -m "feat(tui): migrate to event-sourced state from room.State"
```

---

## Task 8: Replace suggestion system with Input FSM

Replace the ad-hoc suggestion tracking (completionTrigger, completionStart, checkSuggestionTrigger, updateSuggestionFilter, acceptSuggestion, dismissSuggestions) with the InputFSM from Task 5.

**Files:**
- Modify: `internal/tui/app.go`
- Modify: `internal/tui/app_test.go`

- [ ] **Step 1: Replace suggestion fields with InputFSM in App struct**

Remove these fields from App:
```go
completionTrigger rune
completionStart   int
```

Add:
```go
inputFSM *InputFSM
```

- [ ] **Step 2: Initialize InputFSM in NewApp**

In `NewApp`, after creating the App, wire up the FSM:

```go
a.inputFSM = NewInputFSM(
	func(trigger InputTrigger) {
		switch trigger {
		case TriggerSlash:
			if a.registry != nil {
				items := make([]SuggestionItem, 0)
				for _, cmd := range a.registry.Commands() {
					items = append(items, SuggestionItem{
						Label:       "/" + cmd.Name,
						Description: cmd.Description,
					})
				}
				a.suggestions.SetItems(items)
			}
		case TriggerMention:
			items := make([]SuggestionItem, 0)
			for _, p := range a.localParticipants {
				if p.Online {
					items = append(items, SuggestionItem{
						Label:       "@" + p.Name,
						Description: p.Role,
					})
				}
			}
			a.suggestions.SetItems(items)
		}
	},
	func() {
		a.suggestions.Hide()
	},
)
```

- [ ] **Step 3: Replace key handling for suggestions in Update**

Replace the old `KeyUp`/`KeyDown`/`KeyTab`/`KeyEsc` suggestion checks with FSM-based routing:

```go
case tea.KeyUp:
	if a.inputFSM.Current() == StateCompleting {
		a.suggestions.MoveUp()
		return a, nil
	}
case tea.KeyDown:
	if a.inputFSM.Current() == StateCompleting {
		a.suggestions.MoveDown()
		return a, nil
	}
case tea.KeyTab:
	if a.inputFSM.Current() == StateCompleting {
		sel := a.suggestions.Selected()
		if sel.Label != "" {
			end := len([]rune(a.input.Value()))
			a.input.ReplaceRange(a.completionStartPos, end, sel.Label+" ")
		}
		a.inputFSM.Fire(TriggerAccept)
		a.layout()
		return a, nil
	}
case tea.KeyEsc:
	if a.inputFSM.Current() == StateCompleting {
		a.inputFSM.Fire(TriggerDismiss)
		a.layout()
		return a, nil
	}
```

- [ ] **Step 4: Replace suggestion trigger detection**

Replace `checkSuggestionTrigger` with FSM-based detection. After forwarding key events to input, check for triggers:

```go
if _, ok := msg.(tea.KeyMsg); ok && a.input.mode == InputModeHuman {
	if a.inputFSM.Current() == StateCompleting {
		// Update filter based on current input
		val := a.input.Value()
		runes := []rune(val)
		if len(runes) <= a.completionStartPos {
			a.inputFSM.Fire(TriggerDismiss)
		} else {
			query := string(runes[a.completionStartPos+1:])
			a.suggestions.Filter(query)
		}
	} else {
		// Check for new triggers
		val := a.input.Value()
		if val != "" {
			runes := []rune(val)
			last := runes[len(runes)-1]
			switch last {
			case '/':
				if len(runes) == 1 && a.registry != nil {
					a.completionStartPos = 0
					a.inputFSM.Fire(TriggerSlash)
				}
			case '@':
				pos := len(runes) - 1
				if pos == 0 || runes[pos-1] == ' ' || runes[pos-1] == '\n' {
					a.completionStartPos = pos
					a.inputFSM.Fire(TriggerMention)
				}
			}
		}
	}
}
```

Note: We keep `completionStartPos` (renamed from `completionStart`) since it's needed for cursor position tracking, which is a TUI concern.

- [ ] **Step 5: Remove old suggestion methods**

Delete these methods from app.go:
- `checkSuggestionTrigger` (lines 336-380)
- `updateSuggestionFilter` (lines 383-398)
- `acceptSuggestion` (lines 401-410)
- `dismissSuggestions` (lines 413-417)

- [ ] **Step 6: Update tests**

Update suggestion tests in `app_test.go` to verify FSM-based behavior. The test assertions should remain the same (suggestions visible, filtered, accepted) but the mechanism changes.

- [ ] **Step 7: Run full test suite**

Run: `go test ./... -timeout 30s`
Expected: All pass

- [ ] **Step 8: Commit**

```bash
git add internal/tui/app.go internal/tui/app_test.go
git commit -m "feat(tui): replace ad-hoc suggestion tracking with Input FSM"
```

---

## Task 9: Implement three-layer key routing

Replace the nested key handling in Update with the Overlay → Permission → Input FSM pattern.

**Files:**
- Modify: `internal/tui/app.go`
- Modify: `internal/tui/modal.go` (add HandleKey method)

- [ ] **Step 1: Add HandleKey to Modal**

```go
// HandleKey processes a key event in the modal overlay.
// Returns (cmd, consumed). Esc and q dismiss the modal.
func (m *Modal) HandleKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch {
	case msg.Type == tea.KeyEsc, msg.String() == "q":
		return nil, true // consumed — caller should set modal to nil
	default:
		cmd := m.Update(msg)
		return cmd, true // modal consumes all keys
	}
}
```

- [ ] **Step 2: Refactor Update's KeyMsg handling to three layers**

Replace the current nested KeyMsg switch with:

```go
case tea.KeyMsg:
	// Layer 1: Overlay (modal) intercepts ALL keys
	if a.modal != nil {
		cmd, _ := a.modal.HandleKey(m)
		if m.Type == tea.KeyEsc || m.String() == "q" {
			a.modal = nil
		}
		return a, cmd
	}

	// Layer 2: Permission (future — placeholder for #50)
	// if len(a.pendingPermissions) > 0 { ... }

	// Layer 3: Global keys
	if m.Type == tea.KeyCtrlC {
		return a, tea.Quit
	}

	// Layer 4: Input FSM routing
	switch a.inputFSM.Current() {
	case StateCompleting:
		switch m.Type {
		case tea.KeyUp:
			a.suggestions.MoveUp()
			return a, nil
		case tea.KeyDown:
			a.suggestions.MoveDown()
			return a, nil
		case tea.KeyTab:
			// accept suggestion
			sel := a.suggestions.Selected()
			if sel.Label != "" {
				end := len([]rune(a.input.Value()))
				a.input.ReplaceRange(a.completionStartPos, end, sel.Label+" ")
			}
			a.inputFSM.Fire(TriggerAccept)
			a.layout()
			return a, nil
		case tea.KeyEsc:
			a.inputFSM.Fire(TriggerDismiss)
			a.layout()
			return a, nil
		case tea.KeyEnter:
			a.inputFSM.Fire(TriggerSubmit)
			a.layout()
			// fall through to normal Enter handling below
		}
	case StateNormal:
		// Normal input handling — Enter submits
	}

	// Enter handling (both states can reach here)
	if m.Type == tea.KeyEnter && a.input.mode == InputModeHuman {
		text := a.input.Value()
		if newText, consumed := handleBackslashNewline(text); consumed {
			a.input.ta.SetValue(newText)
			return a, nil
		}
		text = strings.TrimSpace(text)
		if text != "" {
			a.input.Reset()
			if a.registry != nil && command.IsCommand(text) {
				result := a.roomState.ExecuteCommand(text)
				if result.Error != nil {
					a.chat.AddMessage(systemMessage(result.Error.Error()))
				} else if result.Modal != nil {
					modal := NewModal(result.Modal, a.width, a.height)
					a.modal = &modal
				} else if result.LocalMessage != "" {
					a.chat.AddMessage(systemMessage(result.LocalMessage))
				}
				return a, nil
			}
			mentions := protocol.ParseMentions(text)
			a.roomState.SendMessage(text, mentions)
		}
		return a, nil
	}
```

- [ ] **Step 3: Run full test suite**

Run: `go test ./... -timeout 30s`
Expected: All pass

- [ ] **Step 4: Commit**

```bash
git add internal/tui/app.go internal/tui/modal.go
git commit -m "feat(tui): implement three-layer key routing (overlay → permission → FSM)"
```

---

## Task 10: Make spinner reactive — remove spinnerActive flag

Replace the `spinnerActive` flag and `maybeStartSpinner` with the reactive self-terminating tick pattern.

**Files:**
- Modify: `internal/tui/app.go`

- [ ] **Step 1: Remove spinnerActive field from App struct**

Remove:
```go
spinnerActive bool
```

- [ ] **Step 2: Remove maybeStartSpinner method**

Delete the `maybeStartSpinner` method (lines 321-332).

- [ ] **Step 3: Update SpinnerTickMsg handler**

Replace:
```go
case SpinnerTickMsg:
	if a.sidebar.TickSpinner() {
		return a, spinnerTick()
	}
	a.spinnerActive = false
	return a, nil
```

With:
```go
case SpinnerTickMsg:
	a.sidebar.TickSpinner()
	if isAnyGenerating(a.localActivities) {
		return a, spinnerTick()
	}
	return a, nil
```

- [ ] **Step 4: Ensure event handlers start the spinner**

The `ParticipantActivityChanged` handler from Task 7 already returns `spinnerTick()` when activity is `ActivityGenerating`. Verify that `HistoryLoaded` also starts the spinner if needed (already does from Task 7).

- [ ] **Step 5: Run full test suite**

Run: `go test ./... -timeout 30s`
Expected: All pass

- [ ] **Step 6: Commit**

```bash
git add internal/tui/app.go
git commit -m "fix(tui): replace spinnerActive flag with reactive self-terminating tick"
```

---

## Task 11: Clean up — remove old handleServerMsg and dead code

Remove the old `handleServerMsg` method and any remaining dead code from the migration.

**Files:**
- Modify: `internal/tui/app.go`
- Modify: `internal/tui/app_test.go`

- [ ] **Step 1: Remove handleServerMsg method**

Delete the entire `handleServerMsg` method (was at lines 421-468).

- [ ] **Step 2: Remove the ServerMsg fallback in Update**

Change:
```go
case ServerMsg:
	if a.roomState != nil {
		return a, nil
	}
	a.handleServerMsg(m.Raw)
	return a, a.maybeStartSpinner()
```

To:
```go
case ServerMsg:
	// ServerMsg is now handled by room.State which emits typed events
	return a, nil
```

- [ ] **Step 3: Remove pendingHistory field and HistoryLoadedMsg type**

The old `pendingHistory` field and `HistoryLoadedMsg` TUI type are replaced by `room.HistoryLoaded`. Remove:
- `pendingHistory []protocol.MessageParams` from App struct
- The old `HistoryLoadedMsg` type definition
- The old `case HistoryLoadedMsg:` handler

- [ ] **Step 4: Remove old maybeStartSpinner references**

Delete `maybeStartSpinnerFromActivities` if it duplicates the reactive pattern (which it does after Task 10).

- [ ] **Step 5: Update tests to remove old ServerMsg-based tests**

Tests that send `ServerMsg` + `handleServerMsg` should now use room events directly. Remove or update:
- `TestHandleServerMsg_*` tests — these are replaced by event contract tests in `internal/room/dispatch_test.go`

Keep tests that verify TUI behavior (modal, suggestions, keyboard) but update them to use room events as input.

- [ ] **Step 6: Run full test suite and linter**

```bash
go test ./... -timeout 30s -race
go run github.com/golangci/golangci-lint/cmd/golangci-lint@latest run ./... --timeout=5m
```

Expected: All pass, no lint errors

- [ ] **Step 7: Commit**

```bash
git add internal/tui/app.go internal/tui/app_test.go
git commit -m "refactor(tui): remove old handleServerMsg and dead migration code"
```

---

## Task 12: Final verification — CI quality gates

Run all CI quality gates to ensure the refactored code is production-ready.

**Files:** None (verification only)

- [ ] **Step 1: Build**

```bash
go build ./...
```

Expected: Clean build

- [ ] **Step 2: Lint**

```bash
go run github.com/golangci/golangci-lint/cmd/golangci-lint@latest run ./... --timeout=5m
```

Expected: No issues

- [ ] **Step 3: Test with race detector**

```bash
go test ./... -timeout 30s -race
```

Expected: All pass, no races

- [ ] **Step 4: Verify room package is TUI-free**

```bash
grep -r "bubbletea\|lipgloss\|bubbles" internal/room/
```

Expected: No matches — room package has zero TUI dependencies

- [ ] **Step 5: Commit any final fixes and push**

```bash
git push -u origin tui-architecture-refactor
```

---

Plan complete and saved to `docs/superpowers/plans/2026-04-05-tui-architecture-refactor.md`. Two execution options:

**1. Subagent-Driven (recommended)** - I dispatch a fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** - Execute tasks in this session using executing-plans, batch execution with checkpoints

Which approach?
