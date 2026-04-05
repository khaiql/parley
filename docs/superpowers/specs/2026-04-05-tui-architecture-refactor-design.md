# TUI Architecture Refactor — Core/Shell Split

## Problem

`internal/tui/app.go` is a monolithic 481-line file managing 9 distinct concerns through a single `App` struct with 19 fields. The `Update` method handles 8 message types with nested switches. Business logic (room state, participant tracking, command execution) is tangled with UI concerns (key routing, suggestions, modals, spinner animation).

This creates three problems:

1. **Testability** — testing business logic requires constructing a full Bubble Tea App with fake rooms and terminal sizing.
2. **Scalability** — each new feature (image upload #47, permission proxy #50) adds more branches to the same switch statement.
3. **Reusability** — the web server mode (#51) needs room state management without any Bubble Tea dependency. Today that logic lives inside the TUI.

### Specific bugs caused by current design

The spinner animation has a race condition: `spinnerActive` flag can get out of sync with actual participant status. If a `SpinnerTickMsg` fires between status changes, the spinner stops and never restarts because `maybeStartSpinner()` only runs on `ServerMsg`, not on status changes. This category of bug exists because App tracks both the business question ("is anyone generating?") and the animation concern ("is my ticker running?") in the same place.

## Design

### Principle: Core/Shell separation with channel-based events

Separate business logic from rendering. The core (`internal/room/`) is pure Go with no TUI dependency. The shell (`internal/tui/`) is a thin Bubble Tea adapter that reacts to state changes. Communication flows through Go channels.

```
TCP Server (broadcasts protocol messages)
    │
    ├── TUI Client
    │     └── room.State ──▶ chan Event ──▶ Bubble Tea views
    │
    └── Web Server (parley serve, also a TCP client)
          └── room.State ──▶ chan Event ──▶ WebSocket ──▶ Browser
```

### Layer 1: `internal/room/` — Business Logic

A pure Go package. No Bubble Tea imports. No rendering. Fully testable with standard Go tests.

#### `room.State`

Holds all room data. Mutations emit events through channels.

```go
package room

type State struct {
    participants []protocol.Participant
    statuses     map[string]string
    messages     []protocol.MessageParams
    permissions  []PermissionRequest
    commands     *command.Registry
    cmdCtx       command.Context
    autoApprove  bool

    subscribers []chan Event
}

func New(registry *command.Registry, ctx command.Context) *State
```

#### Events

Events carry data so remote consumers (web) can render without querying back.

```go
type Event interface{}

type ParticipantsChanged struct {
    Participants []protocol.Participant
}

type MessageReceived struct {
    Message protocol.MessageParams
}

type HistoryLoaded struct {
    Messages []protocol.MessageParams
}

type PermissionRequested struct {
    Request PermissionRequest
}

type PermissionResolved struct {
    RequestID string
    Approved  bool
}
```

Events only represent room-level concerns. No UI events (no overlay, suggestion, or input mode events).

#### Channel-based pub/sub

```go
func (s *State) Subscribe() <-chan Event {
    ch := make(chan Event, 64)
    s.subscribers = append(s.subscribers, ch)
    return ch
}

func (s *State) emit(events ...Event) {
    for _, e := range events {
        for _, ch := range s.subscribers {
            ch <- e
        }
    }
}
```

#### Mutation methods

Each method updates internal state and emits relevant events.

```go
func (s *State) HandleServerMessage(raw *protocol.RawMessage)
func (s *State) SendMessage(text string, mentions []string)
func (s *State) ExecuteCommand(text string) CommandResult
func (s *State) RespondPermission(id string, approved bool)
```

#### Query methods

Read-only access for renderers. No events, no side effects.

```go
func (s *State) Participants() []protocol.Participant
func (s *State) ParticipantStatus(name string) string
func (s *State) IsAnyoneGenerating() bool
func (s *State) Messages() []protocol.MessageParams
func (s *State) AvailableCommands() []CommandInfo
func (s *State) PendingPermissions() []PermissionRequest
func (s *State) AutoApprove() bool
```

#### CommandResult

Commands return a result. The UI layer decides how to present it.

```go
type CommandResult struct {
    Content string
    Error   error
}
```

### Layer 2: `internal/tui/` — TUI Shell

The TUI is a thin adapter that translates between Bubble Tea and `room.State`.

#### Event bridge

TUI subscribes to `room.State` events and injects them into the Bubble Tea event loop via `tea.Program.Send()`.

```go
events := state.Subscribe()
go func() {
    for e := range events {
        program.Send(e)
    }
}()
```

In `App.Update`, room events are handled like any other `tea.Msg`:

```go
case room.ParticipantsChanged:
    // sidebar re-renders from state
    if a.state.IsAnyoneGenerating() {
        return a, spinnerTick()
    }

case room.MessageReceived:
    a.chat.AddMessage(m.Message)

case room.HistoryLoaded:
    a.chat.LoadMessages(m.Messages)
```

#### Input FSM

Uses `qmuntal/stateless` for explicit state transitions. The FSM manages input interaction modes. It does not hold references to room state or view components — behavior is injected via callbacks.

```
    ┌──────────┐  '/' or '@'  ┌────────────┐
    │  Normal  │─────────────▶│ Completing  │
    │          │◀─────────────│             │
    └──────────┘  Tab/Esc/Enter └───────────┘
```

States:
- **Normal** — keystrokes go to textarea
- **Completing** — suggestions visible, Up/Down/Tab/Esc intercepted

Triggers: `TriggerSlash`, `TriggerMention`, `TriggerAccept`, `TriggerDismiss`, `TriggerSubmit`

```go
func NewInputFSM(
    onEnterCompleting func(trigger InputTrigger),
    onExitCompleting func(),
) *InputFSM
```

App wires callbacks at construction:

```go
inputFSM := NewInputFSM(
    func(trigger InputTrigger) {
        switch trigger {
        case TriggerSlash:
            suggestions.SetItems(commandsToItems(state.AvailableCommands()))
        case TriggerMention:
            suggestions.SetItems(participantsToItems(state.Participants()))
        }
    },
    func() {
        suggestions.Hide()
    },
)
```

#### Three-layer key routing

```
Keypress
    │
    ▼
Overlay active? (modal from /info, etc.)
    │ Consumes ALL keys. Esc dismisses.
    │ Purely TUI concern — room.State doesn't know about overlays.
    │ no
    ▼
Permission pending?
    │ y/n consumed, calls state.RespondPermission(). Everything else falls through.
    │ no / passed through
    ▼
Input FSM (Normal or Completing)
```

- **Overlay** is a TUI-only concept. A `CommandResult` with content triggers a modal in TUI, a panel in web — each UI decides independently.
- **Permissions** are a room concept (the queue), but the key mapping (y/n) and rendering (notification bar) are TUI concerns.
- **Input FSM** is TUI-only. Web would implement its own autocomplete UX.

#### Spinner — reactive, no flag

No `spinnerActive` bookkeeping. The rule is simple:

```go
case SpinnerTickMsg:
    a.sidebar.AdvanceFrame()
    if a.state.IsAnyoneGenerating() {
        return a, spinnerTick()
    }
    // stops naturally
```

Every `ParticipantsChanged` event handler checks `IsAnyoneGenerating()` and returns `spinnerTick()` if true. The spinner starts and stops reactively. The race condition in the current design is eliminated.

#### View components — stateless renderers

Components hold only rendering state (spinner frame, viewport scroll, textarea cursor). All room data comes from `room.State` queries.

```go
Sidebar.View(state *room.State) string
Chat.View(state *room.State) string
StatusBar.View(state *room.State) string
TopBar.View(state *room.State) string
```

### Layer 3: Web Server (future, #51)

Same `room.State`, different consumer. The web server subscribes to events and pushes them over WebSocket.

```go
events := state.Subscribe()
go func() {
    for e := range events {
        ws.WriteJSON(toWebEvent(e))
    }
}()
```

Browser receives typed events and renders its own UI. Input actions come back over WebSocket and call the same `room.State` mutation methods (ExecuteCommand, SendMessage, RespondPermission).

### Ownership summary

| Concern | Owner |
|---|---|
| Participants, messages, permissions | `room.State` |
| Command execution and results | `room.State` |
| "Is anyone generating?" | `room.State` |
| Event pub/sub (channels) | `room.State` |
| Input FSM (Normal/Completing) | TUI `InputFSM` |
| Suggestion population (via FSM callbacks) | TUI App |
| Suggestion rendering | TUI `Suggestions` view |
| Overlay (modal from command results) | TUI App |
| Permission key handling (y/n) | TUI App |
| Spinner animation frame | TUI `Sidebar` view |
| Layout calculation | TUI App |
| Three-layer key routing | TUI App |

### Dependencies

New external dependency:
- `github.com/qmuntal/stateless` — typed finite state machine for Input FSM

### Migration path

This refactor can be done incrementally:

1. Extract `internal/room/` with `State`, events, and channel pub/sub
2. Move participant, message, and permission logic from App/Sidebar into `room.State`
3. Add Input FSM alongside existing suggestion code, then swap
4. Convert view components to read from `room.State` instead of owning data
5. Simplify App.Update to three-layer key routing + event handling

Each step is independently shippable and testable. The existing test suite validates behavior at each step.

## Related issues

- #47 — Image upload: extends `Content` types in `room.State`, TUI and web render independently
- #50 — Permission proxy: permission queue lives in `room.State`, TUI adds y/n key layer, web adds approve/deny buttons
- #51 — Web server: subscribes to same `room.State` events via channels, renders via WebSocket
