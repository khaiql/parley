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

Separate business logic from rendering. The core (`internal/room/`) is pure Go with no TUI dependency. The shell (`internal/tui/`) is a thin Bubble Tea adapter that builds its own local state from events. Communication flows through Go channels.

```
TCP Server (broadcasts protocol messages)
    │
    ├── TUI Client
    │     └── room.State ──▶ chan Event ──▶ Bubble Tea Update ──▶ TUI-local state ──▶ View
    │
    └── Web Server (parley serve, also a TCP client)
          └── room.State ──▶ chan Event ──▶ WebSocket ──▶ Browser
```

The TUI never reads from `*room.State` during rendering. It builds and owns its own local state incrementally from events. This eliminates shared mutable state between core and shell — no mutexes needed in the render path. Bubble Tea's `Update`/`View` run on the same goroutine in `eventLoop`, so the TUI side is naturally single-threaded.

### Layer 1: `internal/room/` — Business Logic

A pure Go package. No Bubble Tea imports. No rendering. Fully testable with standard Go tests.

#### `room.State`

Holds all room data. Mutations emit events through channels. Consumers never read from `room.State` during rendering — they build their own local state from events.

```go
package room

type State struct {
    participants []protocol.Participant
    activities   map[string]Activity
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

Events carry data so consumers can build local state without querying back. Each event type maps to a specific state mutation — no monolithic "state updated" event. Granular types let each `Update` case respond to exactly what changed (e.g., `MessageReceived` triggers scroll-to-bottom, `ParticipantsChanged` refreshes the sidebar).

```go
type Event interface{}

type ParticipantsChanged struct {
    Participants []protocol.Participant
}

type MessageReceived struct {
    Message protocol.MessageParams
}

type HistoryLoaded struct {
    Messages     []protocol.MessageParams
    Participants []protocol.Participant
    Activities   map[string]Activity
}

type Activity int

const (
    ActivityListening   Activity = iota
    ActivityThinking
    ActivityGenerating
    ActivityUsingTool
)

type ParticipantActivityChanged struct {
    Name     string
    Activity Activity
}

type PermissionRequested struct {
    Request PermissionRequest
}

type PermissionResolved struct {
    RequestID string
    Approved  bool
}

type ErrorOccurred struct {
    Error error
}
```

Events only represent room-level concerns. No UI events (no overlay, suggestion, or input mode events).

**Event ordering contract**: The core guarantees that participant events emit before their messages. A participant's `ParticipantsChanged` event always arrives before any `MessageReceived` from that participant. This is enforced in `HandleServerMessage()` — one invariant, one place. Consumers should still render defensively (fallback for unknown senders) as a safety net, but should never hit that path in normal operation.

**Semantic distinction**: `HistoryLoaded` (bulk init on join) and `MessageReceived` (live tail) are separate types not just for performance — they carry different semantics. History replay should not trigger scroll animations, notification sounds, or unread indicators. Having separate event types makes that behavior difference explicit in the code.

#### Protocol-to-event translation table

| Server Method | Room Event |
|---|---|
| `room.state` | `HistoryLoaded` (carries messages, participants, activities) |
| `room.message` | `MessageReceived` |
| `room.joined` / `room.left` | `ParticipantsChanged` |
| `room.status` | `ParticipantActivityChanged` |

#### Channel-based pub/sub

Single buffered channel per subscriber with typed events. Buffer size 64. Fan-out is built in — each subscriber (TUI, web server for #51) gets its own channel. A slow WebSocket client can't block the TUI or the core.

```go
func (s *State) Subscribe() <-chan Event {
    ch := make(chan Event, 64)
    s.subscribers = append(s.subscribers, ch)
    return ch
}

func (s *State) emit(events ...Event) {
    for _, e := range events {
        for _, ch := range s.subscribers {
            select {
            case ch <- e:
            default:
                log.Warn("subscriber channel full, dropping event")
            }
        }
    }
}
```

**Backpressure**: Log warning when channel is full; never block the core. Dropped events cause permanent state divergence in the consumer. Recovery path: re-request `HistoryLoaded` to rebuild consumer state from scratch. The architecture supports this since `HistoryLoaded` does a full state replace. Not built initially — the buffer of 64 is sufficient for chat-speed events — but the resync mechanism should be the first thing built if drops are ever observed.

**Slice ownership**: For `HistoryLoaded`, the core allocates fresh slices/maps and does not retain references. The consumer takes full ownership — zero-copy assignment in Go (just a slice header), safe to append indefinitely.

#### Mutation methods

Each method updates internal state and emits relevant events.

```go
func (s *State) HandleServerMessage(raw *protocol.RawMessage)
func (s *State) SendMessage(text string, mentions []string)
func (s *State) ExecuteCommand(text string) CommandResult
func (s *State) RespondPermission(id string, approved bool)
```

#### Query methods

Read-only access for initialization and non-render-path use. Not called during `View()`.

```go
func (s *State) Participants() []protocol.Participant
func (s *State) ParticipantActivity(name string) Activity
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

The TUI is a thin adapter that translates between Bubble Tea and `room.State`. It owns its own local state built incrementally from events — it never reads from `*room.State` during rendering.

#### Event bridge

TUI subscribes to `room.State` events and injects them into the Bubble Tea event loop via `tea.Program.Send()`. This is thread-safe and idiomatic (see bubbletea `examples/send-msg/main.go`).

```go
events := state.Subscribe()
go func() {
    for e := range events {
        program.Send(e)
    }
}()
```

#### Event-sourced incremental state

The TUI model holds its own copies of room data, built from events. This eliminates shared mutable state between core and shell.

```go
type App struct {
    // TUI-owned state, built from events
    messages     []protocol.MessageParams
    participants []protocol.Participant
    activities   map[string]Activity

    // TUI-only concerns
    overlay     *Modal
    inputFSM    *InputFSM
    suggestions Suggestions
    // ...
}
```

In `App.Update`, room events apply deltas to the TUI's local state:

```go
case room.HistoryLoaded:
    // Bulk replace — once on join. No scroll animation, no notifications.
    m.messages = msg.Messages       // takes ownership of core's fresh slice
    m.participants = msg.Participants
    m.activities = msg.Activities
    m.chat.GotoBottom()
    if isAnyoneGenerating(m.activities) {
        return m, spinnerTick()
    }
    return m, nil

case room.MessageReceived:
    // Append single message — live tail. Full side effects.
    m.messages = append(m.messages, msg.Message)
    m.chat.GotoBottom()
    return m, nil

case room.ParticipantsChanged:
    // Replace participant list (small, cheap copy)
    m.participants = msg.Participants
    if isAnyoneGenerating(m.activities) {
        return m, spinnerTick()
    }

case room.ParticipantActivityChanged:
    // Update one entry in map
    m.activities[msg.Name] = msg.Activity
    if msg.Activity == room.ActivityGenerating {
        return m, spinnerTick()
    }

case room.ErrorOccurred:
    // Display in status bar or toast
    m.chat.AddMessage(systemMessage(msg.Error.Error()))
```

Key property: after `HistoryLoaded` on join, no event ever copies the full message history. `MessageReceived` appends a single message — O(1) amortized. This scales to arbitrarily large rooms.

#### Input FSM

Uses `qmuntal/stateless` for explicit state transitions. The FSM manages input interaction modes. It does not hold references to room state or view components — behavior is injected via callbacks. Side effects are returned as `tea.Cmd`, not fired directly, keeping them testable and within Bubble Tea's control flow.

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

**FSM command queue**: Since `qmuntal/stateless` callbacks are void (they don't return values), the FSM adapter holds a `pendingCmds []tea.Cmd` field. `OnEntry`/`OnExit` callbacks append commands to this queue. After firing a trigger, `Update` drains the queue into a `tea.Batch` and returns it.

```go
type InputFSM struct {
    machine     *stateless.StateMachine
    pendingCmds []tea.Cmd
}

// After transition in Update:
fsm.machine.Fire(trigger)
cmds := fsm.pendingCmds
fsm.pendingCmds = nil
return tea.Batch(cmds...), true
```

App wires callbacks at construction:

```go
inputFSM := NewInputFSM(
    func(trigger InputTrigger) {
        switch trigger {
        case TriggerSlash:
            suggestions.SetItems(commandsToItems(state.AvailableCommands()))
        case TriggerMention:
            suggestions.SetItems(participantsToItems(m.participants))
        }
    },
    func() {
        suggestions.Hide()
    },
)
```

#### Three-layer key routing

Strict early-return with `consumed bool`. First layer that matches consumes the key — no double-handling.

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

Implementation:

```go
if a.overlay != nil {
    cmd, consumed := a.overlay.HandleKey(msg)
    if consumed {
        return a, cmd
    }
}
if len(a.pendingPermissions) > 0 {
    cmd, consumed := a.handlePermissionKey(msg)
    if consumed {
        return a, cmd
    }
}
return a.inputFSM.HandleKey(msg)
```

- **Overlay** is a TUI-only concept. A `CommandResult` with content triggers a modal in TUI, a panel in web — each UI decides independently.
- **Permissions** are a room concept (the queue), but the key mapping (y/n) and rendering (notification bar) are TUI concerns.
- **Input FSM** is TUI-only. Web would implement its own autocomplete UX.

#### Spinner — reactive, no flag

No `spinnerActive` bookkeeping. Derive from state, self-terminating tick pattern. Start the tick when any `ParticipantActivityChanged` or `ParticipantsChanged` indicates someone is generating. Let it self-terminate when no one is.

```go
case SpinnerTickMsg:
    a.sidebar.AdvanceFrame()
    if isAnyoneGenerating(a.activities) {
        return a, spinnerTick()
    }
    // stops naturally — no flag to track
```

The spinner visibility derives from the TUI-local `activities` map on each **tick** (not each `View()` call). `View()` is passive and just returns a string. The tick is a `tea.Cmd` that fires at a fixed interval (100ms) and triggers `Update`, which decides whether to re-queue.

#### View components — stateless pure functions

Components hold only rendering state (spinner frame, viewport scroll, textarea cursor). All room data comes from the TUI's local state, not from `room.State`.

```go
func (a App) sidebarView() string    // reads a.participants, a.activities
func (a App) chatView() string       // reads a.messages
func (a App) statusBarView() string  // reads a.activities
func (a App) topBarView() string     // reads a.topic
```

Pure functions of TUI-local state. No owned room data, no side effects. Trivially testable — pass in state, assert on output string. `View()` never panics regardless of state (defensive fallback for unknown participants as a safety net).

### Layer 3: Web Server (future, #51)

Same `room.State`, different consumer. The web server subscribes to events via its own buffered channel and pushes them over WebSocket. A slow WebSocket client can't block the TUI or core — each subscriber has independent backpressure.

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
| Participants, messages, permissions | `room.State` (authoritative) |
| Command execution and results | `room.State` |
| Event pub/sub (channels) | `room.State` |
| TUI-local messages, participants, activities | TUI `App` (built from events) |
| "Is anyone generating?" | Derived from TUI-local `activities` map |
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

## Design Decisions

These decisions were refined through cross-team review with maintainers of bubbletea (dingo, gemini), claude-tmux (cosmo), and opencode (loki).

| Decision | Detail |
|---|---|
| Core/Shell split | `room.State` in `internal/room/`, pure Go, no TUI deps |
| State ownership | TUI builds its own local state from events, no shared `*room.State` pointer in views |
| View components | Stateless pure functions: receive TUI-local state, return strings. No owned room data, no side effects |
| Event delivery | Single buffered channel per subscriber, typed events injected via `program.Send()` |
| Event granularity | Separate types: `ParticipantsChanged`, `MessageReceived`, `ParticipantActivityChanged`, `HistoryLoaded`, `ErrorOccurred`, `PermissionRequested`, `PermissionResolved` |
| Init vs. live | `HistoryLoaded` (bulk, single event, carries messages + participants + activities) for join — no scroll animation, no notifications. `MessageReceived` (individual) for live tail — with full side effects |
| Event ordering | Core guarantees participant events emit before their messages. TUI renders defensively as safety net |
| Concurrency | No mutexes in render path. `Update`/`View` serialization in Bubble Tea's `eventLoop` gives single-threaded safety |
| Backpressure | Buffered channel (64), log warning on full, never block core. Dropped events → state divergence, recovered via `HistoryLoaded` resync |
| Spinner | Reactive: derive from activities map on each tick, self-terminating tick pattern, no `spinnerActive` flag |
| Key routing | Three-layer with `consumed bool` early-return: Overlay → Permission → Input FSM |
| Input FSM | `qmuntal/stateless` for explicit state transitions. Side effects via `pendingCmds` queue, drained as `tea.Cmd` after transitions |
| Fan-out for #51 | One goroutine per subscriber channel. Web server gets its own buffered channel, slow consumers don't block TUI or core |

### Key rationale

**Why TUI-owned state (not shared pointer)**: Every reviewed codebase (claude-tmux's `GitContext` snapshots, opencode's `sync` store, bubbletea's value-type models) independently arrived at the same pattern — the rendering layer owns its state, built from events. Eliminates data races without mutexes and makes `View()` lock-free.

**Why event-sourced incremental state (not full snapshots)**: Message history grows unboundedly. Copying the full history on every event doesn't scale. Instead, `HistoryLoaded` does one bulk transfer on join, then `MessageReceived` appends a single message — O(1) amortized. Small state (participants, activities) can be replaced wholesale cheaply. This matches opencode's delta-based `sync` store and claude-tmux's snapshot-on-init pattern.

**Why granular event types (not `RoomUpdatedMsg`)**: A single update event forces consumers to diff old vs. new state to determine side effects. Granular types let each `Update` case respond to exactly what changed — e.g., `MessageReceived` triggers scroll-to-bottom, `ParticipantsChanged` refreshes the sidebar. Also enables the semantic distinction between `HistoryLoaded` (no animations) and `MessageReceived` (full side effects).

**Why core-guaranteed event ordering (with defensive TUI)**: Both approaches work (core-enforced ordering vs. resilient consumer with fallbacks). We chose core-enforced because parley's core controls emission order in a single method (`HandleServerMessage`), making the guarantee cheap to enforce. Belt and suspenders: enforce ordering at the core, render defensively in `View()` as a safety net. View() must never panic regardless of state.

**Why `qmuntal/stateless` for Input FSM**: claude-tmux uses a `Mode` enum in Rust, opencode uses dialog stack checks, bubbletea's `list` component uses `FilterState()`. All work, but parley's input modes will grow (permissions #50, image upload #47). A formal FSM makes invalid transitions impossible at the library level and provides introspectable state for debugging.

### Migration path

This refactor can be done incrementally:

1. Extract `internal/room/` with `State`, events, and channel pub/sub. **Include event contract tests** — subscribe to `room.State`, feed it server messages, assert on events emitted (types, ordering, data). This becomes the regression safety net for all subsequent steps.
2. Move participant, message, and permission logic from App/Sidebar into `room.State`
3. Add Input FSM alongside existing suggestion code, then swap
4. Convert view components to stateless pure functions reading from TUI-local state
5. Simplify App.Update to three-layer key routing + event handling

Each step is independently shippable and testable. The existing test suite validates behavior at each step.

## Related issues

- #47 — Image upload: extends `Content` types in `room.State`, TUI and web render independently
- #50 — Permission proxy: permission queue lives in `room.State`, TUI adds y/n key layer, web adds approve/deny buttons
- #51 — Web server: subscribes to same `room.State` events via channels, renders via WebSocket
