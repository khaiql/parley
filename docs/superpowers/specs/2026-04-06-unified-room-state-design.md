# Unified Room State Design

## Goal

Eliminate the duplication between `server.Room` and `room.State`. `room.State` becomes the single source of truth for room data (participants, messages, metadata). `server.Room` is replaced by a `ConnectionManager` that only tracks TCP connections. Persistence moves to its own package behind an interface.

## Problem

`server.Room` and `room.State` both track participants and messages independently. `server.Room` is the server-side authority (with connections, broadcasting, persistence). `room.State` is the client-side projection (with events, TUI queries). The overlap leads to confusing ownership, duplicated logic, and tight coupling between the server and persistence.

## Architecture

### Before

```
server.Room                           room.State
├── Participants (with connections)    ├── participants (metadata only)
├── Messages                          ├── messages
├── Topic, ID, AutoApprove            ├── roomID, topic, autoApprove
├── Join/Leave                        ├── HandleServerMessage
├── Broadcast*/extractMentions        ├── Subscribe/emit
├── GetMessages/GetParticipants       ├── GetID/GetTopic/...
└── recentMessages/snapshot           └── SendMessage/ExecuteCommand
```

### After

```
room.State (single source of truth)        server.ConnectionManager
├── participants []protocol.Participant     ├── conns map[string]*ClientConn
├── messages []protocol.MessageParams       ├── mu sync.RWMutex
├── roomID, topic, autoApprove              │
├── seq int                                 ├── Add(name, cc)
├── activities map[string]Activity          ├── Remove(name)
├── subscribers []chan Event                 ├── Broadcast(data)
│                                           └── BroadcastExcept(name, data)
├── Join(name, role, ...) → RoomStateParams
├── Leave(name)
├── AddMessage(...) → MessageParams
├── AddSystemMessage(text) → MessageParams
├── UpdateStatus(name, status)
├── RecentMessages(n) []MessageParams
├── HandleServerMessage(raw)  [client-side]
├── Subscribe/emit
└── GetID/GetTopic/GetParticipants/...

persistence.Store (interface)
├── Save(RoomSnapshot) error
├── Load(roomID string) (RoomSnapshot, error)
├── SaveAgentSession(roomID, agentName, sessionID string) error
└── FindAgentSession(roomID, agentName string) (string, error)

persistence.JSONStore (implementation)
└── basePath string  (e.g. ~/.parley/rooms)
```

## Components

### room.State — Server-Side Mutations

`room.State` gains methods for the server to call directly. These replace the business logic in `server.Room`:

- `Join(name, role, dir, repo, agentType, source string) (protocol.RoomStateParams, error)` — adds participant (or reconnects offline one), returns state snapshot with recent messages
- `Leave(name string)` — marks participant offline
- `AddMessage(from, source, role string, content protocol.Content, mentions []string) protocol.MessageParams` — assigns seq/ID, stores message, emits `MessageReceived`, returns the message for broadcasting
- `AddSystemMessage(text string) protocol.MessageParams` — convenience wrapper
- `UpdateStatus(name, status string)` — updates activity, emits `ParticipantActivityChanged`
- `RecentMessages(n int) []protocol.MessageParams` — returns up to n recent non-system messages (logic from `server.Room.recentMessages`)

`room.State` remains **single-threaded**. The server protects it with its own mutex.

### server.ConnectionManager

Extracted from `server.Room`. Only manages connections:

```go
type ClientConn struct {
    Name string
    Send chan []byte
    Done chan struct{}
}

type ConnectionManager struct {
    mu    sync.RWMutex
    conns map[string]*ClientConn
}

func (cm *ConnectionManager) Add(name string, cc *ClientConn)
func (cm *ConnectionManager) Remove(name string)      // closes Done, deletes from map
func (cm *ConnectionManager) Broadcast(data []byte)
func (cm *ConnectionManager) BroadcastExcept(name string, data []byte)
```

`ClientConn` loses all metadata fields (`Role`, `Directory`, `Repo`, `AgentType`, `Source`, `Online`). That data lives in `room.State`'s participant list.

### server.TCPServer

Owns `*room.State` (borrowed, not created), `*ConnectionManager`, and a mutex:

```go
type TCPServer struct {
    listener net.Listener
    state    *room.State
    conns    *ConnectionManager
    mu       sync.Mutex  // protects state from concurrent handleConn goroutines
}

func New(addr string, state *room.State) (*TCPServer, error)
```

The caller (host) creates `room.State` and passes it in. The server doesn't own or create the state.

`handleConn` flow:

```
MethodJoin:
    mu.Lock → state.Join(name, ...) → mu.Unlock
    conns.Add(name, cc)
    send room.state to client
    conns.BroadcastExcept(name, joined notification)

MethodSend:
    mu.Lock → state.AddMessage(name, ...) → mu.Unlock
    conns.Broadcast(message notification)

MethodStatus:
    mu.Lock → state.UpdateStatus(name, status) → mu.Unlock
    conns.BroadcastExcept(name, status notification)

Disconnect:
    mu.Lock → state.Leave(name) → mu.Unlock
    conns.Remove(name)
    conns.Broadcast(left notification)
```

### Server Interface

```go
type Server interface {
    Addr() string
    Port() int
    Serve()
    Close() error
}
```

`Room() *Room` is removed. The host already has a reference to `room.State` (it created it).

### protocol.MatchMentions

Pure function extracted from `server.Room.extractMentions`:

```go
func MatchMentions(text string, names []string) []string
```

Takes message text and known participant names, returns matched mentions. No dependency on any struct.

### persistence.Store

```go
type RoomSnapshot struct {
    RoomID       string
    Topic        string
    AutoApprove  bool
    Participants []protocol.Participant
    Messages     []protocol.MessageParams
}

type Store interface {
    Save(snapshot RoomSnapshot) error
    Load(roomID string) (RoomSnapshot, error)
    SaveAgentSession(roomID, agentName, sessionID string) error
    FindAgentSession(roomID, agentName string) (string, error)
}
```

`JSONStore` is the first implementation. Same file format as today (`room.json`, `messages.json`, `agents.json`). Base path configurable (default `~/.parley/rooms`).

`Load` returns a `RoomSnapshot` — plain data. The caller hydrates `room.State` from it. This keeps persistence decoupled from `room.State` internals.

### room.State Hydration

`room.State` needs a way to restore from a snapshot (for resume):

```go
func (s *State) Restore(snap persistence.RoomSnapshot)
```

Sets roomID, topic, participants, messages, seq, autoApprove from the snapshot. Called by the host on resume before starting the server.

Note: this creates a circular import (`room` → `persistence` → `room`). To avoid it, `Restore` takes the same fields directly:

```go
func (s *State) Restore(roomID, topic string, participants []protocol.Participant, messages []protocol.MessageParams, autoApprove bool)
```

Or `RoomSnapshot` lives in `protocol` (it's just data types from there anyway).

**Decision:** `RoomSnapshot` moves to `internal/protocol/` since it's composed entirely of protocol types. Both `room` and `persistence` depend on `protocol`, no cycle.

## Concurrency Model

`room.State` is single-threaded. It does not contain a mutex.

- **Client-side** (host TUI, join TUI): one goroutine calls `HandleServerMessage` from the incoming loop. Safe.
- **Server-side**: multiple `handleConn` goroutines call `Join`/`Leave`/`AddMessage`/`UpdateStatus`. The server's `mu sync.Mutex` serializes these calls. `room.State` doesn't know about concurrency.
- **ConnectionManager**: has its own `sync.RWMutex` for connection map access. Independent from state mutex.

## Migration Path (Strangler)

Each step is independently shippable:

1. **Extract `ConnectionManager`** from `server.Room`. Both `ConnectionManager` and `Room` exist. `Room` delegates broadcast to `ConnectionManager`.
2. **Add server-side mutations to `room.State`** (`Join`, `Leave`, `AddMessage`, `RecentMessages`, etc.). Add `protocol.MatchMentions`. Tests only — not wired yet.
3. **Move persistence** to `internal/persistence/` with `Store` interface and `JSONStore`. Move `RoomSnapshot` to `protocol`. Add `room.State.Restore`. Old persistence functions become thin wrappers.
4. **Rewire `TCPServer`** to use `room.State` + `ConnectionManager` instead of `server.Room`. Server constructor takes `*room.State`. `handleConn` calls state mutations under mutex.
5. **Rewire host** — host creates `room.State`, passes to server. Persistence uses new `Store`. Remove `RoomAdapter` (use `room.State` directly for `RoomQuerier`, pass port separately).
6. **Delete `server.Room`** and old persistence wrappers. Clean up imports.

## What Gets Deleted

| Deleted | Replaced by |
|---------|------------|
| `server.Room` struct | `room.State` + `server.ConnectionManager` |
| `server.Room.Join/Leave` | `room.State.Join/Leave` |
| `server.Room.Broadcast*` | `ConnectionManager.Broadcast/BroadcastExcept` + server wiring |
| `server.Room.extractMentions` | `protocol.MatchMentions` |
| `server.Room.recentMessages` | `room.State.RecentMessages` |
| `server.Room.snapshot/Get*` | Already on `room.State` |
| `server.NewRoom/newUUID` | `room.State` constructor |
| `server.SaveRoom/LoadRoom` | `persistence.JSONStore.Save/Load` |
| `server.RoomDir` | `persistence.JSONStore` internal |
| `server.RoomData/ParticipantData` | `protocol.RoomSnapshot` + internal types |
| `ClientConn` metadata fields | `room.State` participant tracking |
| `RoomAdapter` in host.go | `room.State` implements `RoomQuerier` directly |
