# Agent Skill Pivot Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace Parley's TUI/subprocess-agent model with a headless, JSON-only, skill-first room server plus participant adapter workflow.

**Architecture:** Build the new v1 surface beside the old code until the new server, adapter, and CLI commands are covered, then delete the old TUI/driver/export surfaces in one cleanup task. The new model is event-log-first: the server assigns sequence numbers and broadcasts events, participant adapters keep local mirrors and expose Unix control sockets, and short CLI commands return JSON for agent skills.

**Tech Stack:** Go 1.26, Cobra, TCP with line-delimited JSON, Unix domain sockets, JSONL event logs, GoReleaser, shell scripts for skill bootstrap.

---

## File Structure

| File | Action | Responsibility |
|------|--------|----------------|
| `internal/model/event.go` | Create | Event envelope, participant, room metadata, payload structs |
| `internal/model/event_test.go` | Create | Event validation and transcript filtering tests |
| `internal/descriptor/descriptor.go` | Create | Parse/format `parley://host:port/room-id` descriptors |
| `internal/descriptor/descriptor_test.go` | Create | Descriptor grammar, IPv6, invalid form tests |
| `internal/paths/paths.go` | Create | Per-user Parley paths, room dirs, active pointer, permissions |
| `internal/paths/paths_test.go` | Create | Path and permission tests |
| `internal/jsonout/jsonout.go` | Create | JSON success/error response helpers |
| `internal/jsonout/jsonout_test.go` | Create | Error envelope tests |
| `internal/eventlog/log.go` | Create | Append/read/query JSONL event log |
| `internal/eventlog/log_test.go` | Create | Sequence, filtering, corrupt-line behavior |
| `internal/protocol/protocol.go` | Rewrite | V1 request/response/event wire types and NDJSON helpers |
| `internal/protocol/protocol_test.go` | Rewrite | Codec and request/response tests |
| `internal/server/server.go` | Rewrite | Headless room TCP server and local control socket |
| `internal/server/server_test.go` | Rewrite | Join/send/history/leave/stop integration tests |
| `internal/adapter/adapter.go` | Create | Participant adapter TCP connection and local event mirror |
| `internal/adapter/control.go` | Create | Participant Unix socket control API |
| `internal/adapter/adapter_test.go` | Create | Inbox, wait, send, leave, terminal-state tests |
| `cmd/parley/main.go` | Rewrite | Root command, shared flags, version command |
| `cmd/parley/start.go` | Create | `parley start`, hidden server/adapter child launch |
| `cmd/parley/join.go` | Rewrite | `parley join <descriptor>` starts participant adapter |
| `cmd/parley/invite.go` | Create | `parley invite` JSON handoff output |
| `cmd/parley/participant_commands.go` | Create | `info`, `status`, `inbox`, `history`, `wait`, `send`, `leave`, `stop` |
| `cmd/parley/*_test.go` | Rewrite/Create | CLI JSON shape and command validation tests |
| `skills/parley/SKILL.md` | Create | Agent skill instructions |
| `skills/parley/scripts/ensure-parley` | Create | Binary bootstrap helper |
| `install.sh` | Create | Human/user-local installer |
| `.goreleaser.yaml` | Modify | Darwin/Linux releases only; installer-friendly archives |
| `README.md` | Rewrite | New headless skill-first usage |
| `CLAUDE.md` | Modify | Remove TUI-era architecture references |
| `cmd/parley/host.go` | Delete | Old TUI host command |
| `cmd/parley/export.go` | Delete | Old HTML export command |
| `internal/client/` | Delete | Old TCP client API tied to JSON-RPC room protocol |
| `internal/room/` | Delete | Old in-process room state and TUI event model |
| `internal/persistence/` | Delete | Old `agents.json` session persistence |
| `internal/tui/` | Delete | Old Bubble Tea UI |
| `internal/driver/` | Delete | Old agent subprocess drivers |
| `internal/dispatcher/` | Delete | Old driver routing |
| `internal/command/` | Delete | Old slash-command package |
| `internal/web/` | Delete | Old HTML export |

---

## Clarifications Folded Into This Plan

- `parley wait` blocks until an unseen message from another participant exists, then returns the full unseen inbox batch through that message. This preserves a single `last_seen_seq` while avoiding lost join/leave events.
- `parley wait` returns terminal JSON states for `timeout`, `room_closed`, and `adapter_disconnected`; those terminal states are not "message wakeups".
- `parley join` requires `--name`; `--role` defaults to `"participant"` when omitted.
- Room directories are created with `0700`, files with `0600`, and Unix sockets live under those protected directories. V1 trusts the same local OS user.
- Descriptor grammar is `parley://host:port/room-id`; query strings and fragments are rejected in v1. IPv6 uses standard URL bracket notation such as `parley://[::1]:49231/01j...`.

---

### Task 1: Core Model, Descriptor, JSON Output, And Paths

**Files:**
- Create: `internal/model/event.go`
- Create: `internal/model/event_test.go`
- Create: `internal/descriptor/descriptor.go`
- Create: `internal/descriptor/descriptor_test.go`
- Create: `internal/paths/paths.go`
- Create: `internal/paths/paths_test.go`
- Create: `internal/jsonout/jsonout.go`
- Create: `internal/jsonout/jsonout_test.go`

- [ ] **Step 1: Write model tests**

Create `internal/model/event_test.go`:

```go
package model

import "testing"

func TestEventIsTranscript(t *testing.T) {
	tests := []struct {
		typ  EventType
		want bool
	}{
		{EventRoomStarted, true},
		{EventRoomStopped, true},
		{EventParticipantJoined, true},
		{EventParticipantLeft, true},
		{EventMessage, true},
		{EventType("unknown"), false},
	}
	for _, tt := range tests {
		if got := tt.typ.IsTranscript(); got != tt.want {
			t.Errorf("%s IsTranscript = %v, want %v", tt.typ, got, tt.want)
		}
	}
}

func TestMessagePayloadMentions(t *testing.T) {
	payload := MessagePayload{Text: "@codex please inspect", Mentions: []string{"codex"}}
	if payload.Text == "" {
		t.Fatal("expected text to be stored")
	}
	if len(payload.Mentions) != 1 || payload.Mentions[0] != "codex" {
		t.Fatalf("mentions = %#v, want [codex]", payload.Mentions)
	}
}
```

- [ ] **Step 2: Run model tests to verify they fail**

Run: `go test ./internal/model -run TestEvent -v`
Expected: FAIL with `stat ... internal/model: directory not found` or undefined model symbols.

- [ ] **Step 3: Implement model types**

Create `internal/model/event.go`:

```go
package model

import "time"

type EventType string

const (
	EventRoomStarted       EventType = "room.started"
	EventRoomStopped       EventType = "room.stopped"
	EventParticipantJoined EventType = "participant.joined"
	EventParticipantLeft   EventType = "participant.left"
	EventMessage           EventType = "message"
)

func (t EventType) IsTranscript() bool {
	switch t {
	case EventRoomStarted, EventRoomStopped, EventParticipantJoined, EventParticipantLeft, EventMessage:
		return true
	default:
		return false
	}
}

type Event struct {
	Seq       int64       `json:"seq"`
	Type      EventType   `json:"type"`
	Timestamp time.Time   `json:"timestamp"`
	RoomID    string      `json:"room_id"`
	Actor     string      `json:"actor"`
	Payload   interface{} `json:"payload,omitempty"`
}

type Participant struct {
	Name      string `json:"name"`
	Role      string `json:"role"`
	Directory string `json:"directory,omitempty"`
	Repo      string `json:"repo,omitempty"`
	Online    bool   `json:"online"`
}

type RoomMetadata struct {
	RoomID    string `json:"room_id"`
	Topic     string `json:"topic"`
	LocalHost string `json:"local_host,omitempty"`
	LocalPort int    `json:"local_port,omitempty"`
}

type MessagePayload struct {
	Text     string   `json:"text"`
	Mentions []string `json:"mentions,omitempty"`
}

type ParticipantPayload struct {
	Role      string `json:"role"`
	Directory string `json:"directory,omitempty"`
	Repo      string `json:"repo,omitempty"`
}

type RoomStoppedPayload struct {
	Reason string `json:"reason"`
}
```

- [ ] **Step 4: Run model tests**

Run: `go test ./internal/model -v`
Expected: PASS.

- [ ] **Step 5: Write descriptor tests**

Create `internal/descriptor/descriptor_test.go`:

```go
package descriptor

import "testing"

func TestParseDescriptor(t *testing.T) {
	d, err := Parse("parley://127.0.0.1:49231/01jabc")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if d.Host != "127.0.0.1" || d.Port != 49231 || d.RoomID != "01jabc" {
		t.Fatalf("descriptor = %#v", d)
	}
	if got := d.String(); got != "parley://127.0.0.1:49231/01jabc" {
		t.Fatalf("String = %q", got)
	}
}

func TestParseDescriptorIPv6(t *testing.T) {
	d, err := Parse("parley://[::1]:49231/01jabc")
	if err != nil {
		t.Fatalf("Parse IPv6: %v", err)
	}
	if d.Host != "::1" || d.Port != 49231 {
		t.Fatalf("descriptor = %#v", d)
	}
}

func TestParseDescriptorRejectsQuery(t *testing.T) {
	if _, err := Parse("parley://127.0.0.1:49231/01jabc?token=x"); err == nil {
		t.Fatal("expected query string to be rejected")
	}
}
```

- [ ] **Step 6: Run descriptor tests to verify they fail**

Run: `go test ./internal/descriptor -run TestParseDescriptor -v`
Expected: FAIL with missing package or undefined `Parse`.

- [ ] **Step 7: Implement descriptor parser**

Create `internal/descriptor/descriptor.go`:

```go
package descriptor

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

type Descriptor struct {
	Host   string `json:"host"`
	Port   int    `json:"port"`
	RoomID string `json:"room_id"`
}

func Parse(raw string) (Descriptor, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return Descriptor{}, fmt.Errorf("parse descriptor: %w", err)
	}
	if u.Scheme != "parley" {
		return Descriptor{}, fmt.Errorf("descriptor scheme must be parley")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return Descriptor{}, fmt.Errorf("descriptor query and fragment are not supported")
	}
	host := u.Hostname()
	portText := u.Port()
	if host == "" || portText == "" {
		return Descriptor{}, fmt.Errorf("descriptor requires host and port")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 || port > 65535 {
		return Descriptor{}, fmt.Errorf("descriptor port is invalid")
	}
	roomID := strings.TrimPrefix(u.EscapedPath(), "/")
	if roomID == "" || strings.Contains(roomID, "/") {
		return Descriptor{}, fmt.Errorf("descriptor requires exactly one room id path segment")
	}
	unescapedRoomID, err := url.PathUnescape(roomID)
	if err != nil {
		return Descriptor{}, fmt.Errorf("descriptor room id is invalid: %w", err)
	}
	return Descriptor{Host: host, Port: port, RoomID: unescapedRoomID}, nil
}

func (d Descriptor) Addr() string {
	return net.JoinHostPort(d.Host, strconv.Itoa(d.Port))
}

func (d Descriptor) String() string {
	host := d.Host
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("parley://%s:%d/%s", host, d.Port, url.PathEscape(d.RoomID))
}
```

- [ ] **Step 8: Run descriptor tests**

Run: `go test ./internal/descriptor -v`
Expected: PASS.

- [ ] **Step 9: Write path and JSON output tests**

Create `internal/paths/paths_test.go`:

```go
package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureRoomDirPermissions(t *testing.T) {
	root := t.TempDir()
	p := New(root)
	dir, err := p.EnsureRoomDir("room-1")
	if err != nil {
		t.Fatalf("EnsureRoomDir: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("mode = %o, want 700", got)
	}
	if filepath.Base(dir) != "room-1" {
		t.Fatalf("dir = %s", dir)
	}
}
```

Create `internal/jsonout/jsonout_test.go`:

```go
package jsonout

import (
	"encoding/json"
	"testing"
)

func TestErrorEnvelope(t *testing.T) {
	data, err := MarshalError("adapter_not_running", "No adapter is running")
	if err != nil {
		t.Fatalf("MarshalError: %v", err)
	}
	var out struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Error.Code != "adapter_not_running" {
		t.Fatalf("code = %q", out.Error.Code)
	}
}
```

- [ ] **Step 10: Implement paths and JSON output helpers**

Create `internal/paths/paths.go`:

```go
package paths

import (
	"os"
	"path/filepath"
)

type Paths struct {
	Root string
}

func New(root string) Paths {
	return Paths{Root: root}
}

func DefaultRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, ".parley")
}

func (p Paths) RoomsDir() string {
	return filepath.Join(p.Root, "rooms")
}

func (p Paths) RoomDir(roomID string) string {
	return filepath.Join(p.RoomsDir(), roomID)
}

func (p Paths) EnsureRoomDir(roomID string) (string, error) {
	dir := p.RoomDir(roomID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, os.Chmod(dir, 0o700)
}

func (p Paths) ActivePath() string {
	return filepath.Join(p.Root, "active.json")
}
```

Create `internal/jsonout/jsonout.go`:

```go
package jsonout

import "encoding/json"

type ErrorEnvelope struct {
	Error ErrorBody `json:"error"`
}

type ErrorBody struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
}

func Marshal(v interface{}) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

func MarshalError(code, message string) ([]byte, error) {
	return Marshal(ErrorEnvelope{Error: ErrorBody{Code: code, Message: message}})
}
```

- [ ] **Step 11: Run package tests**

Run: `go test ./internal/model ./internal/descriptor ./internal/paths ./internal/jsonout -v`
Expected: PASS.

- [ ] **Step 12: Commit**

```bash
git add internal/model internal/descriptor internal/paths internal/jsonout
git commit -m "feat: add headless core model helpers"
```

---

### Task 2: Event Log Persistence

**Files:**
- Create: `internal/eventlog/log.go`
- Create: `internal/eventlog/log_test.go`

- [ ] **Step 1: Write event log tests**

Create `internal/eventlog/log_test.go`:

```go
package eventlog

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/khaiql/parley/internal/model"
)

func TestLogAppendAssignsSequence(t *testing.T) {
	log := New(filepath.Join(t.TempDir(), "events.jsonl"))
	ev, err := log.Append(model.Event{
		Type:      model.EventMessage,
		Timestamp: time.Now().UTC(),
		RoomID:    "room-1",
		Actor:     "alice",
		Payload:   model.MessagePayload{Text: "hello"},
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if ev.Seq != 1 {
		t.Fatalf("seq = %d, want 1", ev.Seq)
	}
	events, err := log.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(events) != 1 || events[0].Seq != 1 {
		t.Fatalf("events = %#v", events)
	}
}

func TestLogAfterSeq(t *testing.T) {
	log := New(filepath.Join(t.TempDir(), "events.jsonl"))
	for i := 0; i < 3; i++ {
		if _, err := log.Append(model.Event{Type: model.EventMessage, RoomID: "r", Actor: "a"}); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	events, err := log.AfterSeq(1, 10)
	if err != nil {
		t.Fatalf("AfterSeq: %v", err)
	}
	if len(events) != 2 || events[0].Seq != 2 || events[1].Seq != 3 {
		t.Fatalf("events = %#v", events)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/eventlog -run TestLog -v`
Expected: FAIL with missing package or undefined `New`.

- [ ] **Step 3: Implement append/read log**

Create `internal/eventlog/log.go`:

```go
package eventlog

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/khaiql/parley/internal/model"
)

type Log struct {
	path string
	mu   sync.Mutex
}

func New(path string) *Log {
	return &Log{path: path}
}

func (l *Log) Append(ev model.Event) (model.Event, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	events, err := l.readAllLocked()
	if err != nil {
		return model.Event{}, err
	}
	var last int64
	if len(events) > 0 {
		last = events[len(events)-1].Seq
	}
	ev.Seq = last + 1
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now().UTC()
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return model.Event{}, err
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return model.Event{}, err
	}
	defer f.Close()
	data, err := json.Marshal(ev)
	if err != nil {
		return model.Event{}, err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		return model.Event{}, err
	}
	_ = os.Chmod(l.path, 0o600)
	return ev, nil
}

func (l *Log) ReadAll() ([]model.Event, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.readAllLocked()
}

func (l *Log) AfterSeq(seq int64, limit int) ([]model.Event, error) {
	all, err := l.ReadAll()
	if err != nil {
		return nil, err
	}
	out := make([]model.Event, 0)
	for _, ev := range all {
		if ev.Seq > seq {
			out = append(out, ev)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (l *Log) readAllLocked() ([]model.Event, error) {
	f, err := os.Open(l.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var events []model.Event
	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		var ev model.Event
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", l.path, line, err)
		}
		events = append(events, ev)
	}
	return events, sc.Err()
}
```

- [ ] **Step 4: Run event log tests**

Run: `go test ./internal/eventlog -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/eventlog
git commit -m "feat: add event log persistence"
```

---

### Task 3: V1 Protocol Types And Codec

**Files:**
- Rewrite: `internal/protocol/protocol.go`
- Rewrite: `internal/protocol/protocol_test.go`

- [ ] **Step 1: Replace protocol tests**

Replace `internal/protocol/protocol_test.go` with tests for the v1 protocol:

```go
package protocol

import (
	"encoding/json"
	"testing"

	"github.com/khaiql/parley/internal/model"
)

func TestEncodeDecodeRequest(t *testing.T) {
	req := Request{
		Type: RequestJoin,
		Join: &JoinRequest{
			RoomID: "room-1",
			Name:   "codex",
			Role:   "reviewer",
		},
	}
	data, err := EncodeLine(req)
	if err != nil {
		t.Fatalf("EncodeLine: %v", err)
	}
	var decoded Request
	if err := DecodeLine(data, &decoded); err != nil {
		t.Fatalf("DecodeLine: %v", err)
	}
	if decoded.Type != RequestJoin || decoded.Join.Name != "codex" {
		t.Fatalf("decoded = %#v", decoded)
	}
}

func TestResponseCarriesEvent(t *testing.T) {
	resp := Response{
		OK:    true,
		Event: &model.Event{Seq: 1, Type: model.EventMessage, RoomID: "room-1"},
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected JSON")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/protocol -run 'TestEncodeDecodeRequest|TestResponseCarriesEvent' -v`
Expected: FAIL because old JSON-RPC types do not match.

- [ ] **Step 3: Rewrite protocol types**

Replace `internal/protocol/protocol.go` with v1 protocol definitions:

```go
package protocol

import (
	"bytes"
	"encoding/json"

	"github.com/khaiql/parley/internal/model"
)

type RequestType string

const (
	RequestJoin    RequestType = "join"
	RequestSend    RequestType = "send"
	RequestHistory RequestType = "history"
	RequestLeave   RequestType = "leave"
)

type Request struct {
	Type    RequestType     `json:"type"`
	Join    *JoinRequest    `json:"join,omitempty"`
	Send    *SendRequest    `json:"send,omitempty"`
	History *HistoryRequest `json:"history,omitempty"`
	Leave   *LeaveRequest   `json:"leave,omitempty"`
}

type JoinRequest struct {
	RoomID    string `json:"room_id"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	Directory string `json:"directory,omitempty"`
	Repo      string `json:"repo,omitempty"`
}

type SendRequest struct {
	Text string `json:"text"`
}

type HistoryRequest struct {
	AfterSeq int64 `json:"after_seq,omitempty"`
	Limit    int   `json:"limit,omitempty"`
	All      bool  `json:"all,omitempty"`
}

type LeaveRequest struct {
	Name string `json:"name,omitempty"`
}

type Response struct {
	OK           bool                `json:"ok"`
	Error        *Error              `json:"error,omitempty"`
	Room         *model.RoomMetadata `json:"room,omitempty"`
	Participants []model.Participant `json:"participants,omitempty"`
	Events       []model.Event       `json:"events,omitempty"`
	Event        *model.Event        `json:"event,omitempty"`
	LatestSeq    int64              `json:"latest_seq,omitempty"`
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func EncodeLine(v interface{}) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func DecodeLine(data []byte, v interface{}) error {
	return json.NewDecoder(bytes.NewReader(bytes.TrimSpace(data))).Decode(v)
}
```

- [ ] **Step 4: Run protocol tests**

Run: `go test ./internal/protocol -v`
Expected: PASS for the rewritten tests.

- [ ] **Step 5: Commit**

```bash
git add internal/protocol
git commit -m "feat: define v1 line protocol"
```

---

### Task 4: Headless Room Server

**Files:**
- Rewrite: `internal/server/server.go`
- Rewrite: `internal/server/server_test.go`
- Delete in this task: `internal/server/connmanager.go` if the v1 server no longer needs the old connection manager

- [ ] **Step 1: Write server integration tests**

Replace `internal/server/server_test.go` with v1 tests that start a real server on `127.0.0.1:0`:

```go
package server_test

import (
	"bufio"
	"net"
	"testing"
	"time"

	"github.com/khaiql/parley/internal/eventlog"
	"github.com/khaiql/parley/internal/model"
	"github.com/khaiql/parley/internal/protocol"
	"github.com/khaiql/parley/internal/server"
)

func TestServerJoinSendHistory(t *testing.T) {
	dir := t.TempDir()
	log := eventlog.New(dir + "/events.jsonl")
	srv, err := server.New("127.0.0.1:0", server.Config{
		RoomID: "room-1",
		Topic:  "test topic",
		Log:    log,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	go srv.Serve()
	t.Cleanup(func() { _ = srv.Close() })

	conn := dialAndJoin(t, srv.Addr(), "room-1", "alice")
	defer conn.Close()

	sendReq(t, conn, protocol.Request{Type: protocol.RequestSend, Send: &protocol.SendRequest{Text: "hello"}})
	resp := readResp(t, conn)
	if !resp.OK || resp.Event == nil || resp.Event.Type != model.EventMessage {
		t.Fatalf("send response = %#v", resp)
	}

	sendReq(t, conn, protocol.Request{Type: protocol.RequestHistory, History: &protocol.HistoryRequest{Limit: 10}})
	resp = readResp(t, conn)
	if !resp.OK || len(resp.Events) == 0 {
		t.Fatalf("history response = %#v", resp)
	}
}

func TestServerRejectsWrongRoomID(t *testing.T) {
	log := eventlog.New(t.TempDir() + "/events.jsonl")
	srv, err := server.New("127.0.0.1:0", server.Config{RoomID: "room-1", Topic: "test", Log: log})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	go srv.Serve()
	t.Cleanup(func() { _ = srv.Close() })

	conn, err := net.Dial("tcp", srv.Addr())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	sendReq(t, conn, protocol.Request{Type: protocol.RequestJoin, Join: &protocol.JoinRequest{RoomID: "wrong", Name: "alice", Role: "participant"}})
	resp := readResp(t, conn)
	if resp.OK || resp.Error == nil || resp.Error.Code != "room_mismatch" {
		t.Fatalf("response = %#v", resp)
	}
}

func dialAndJoin(t *testing.T, addr, roomID, name string) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	sendReq(t, conn, protocol.Request{Type: protocol.RequestJoin, Join: &protocol.JoinRequest{RoomID: roomID, Name: name, Role: "participant"}})
	resp := readResp(t, conn)
	if !resp.OK {
		t.Fatalf("join response = %#v", resp)
	}
	return conn
}

func sendReq(t *testing.T, conn net.Conn, req protocol.Request) {
	t.Helper()
	data, err := protocol.EncodeLine(req)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if _, err := conn.Write(data); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func readResp(t *testing.T, conn net.Conn) protocol.Response {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	var resp protocol.Response
	if err := protocol.DecodeLine(line, &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}
```

- [ ] **Step 2: Run server tests to verify they fail**

Run: `go test ./internal/server -run 'TestServerJoinSendHistory|TestServerRejectsWrongRoomID' -v`
Expected: FAIL because server API still uses old `room.State`.

- [ ] **Step 3: Implement v1 server API**

Rewrite `internal/server/server.go` around this public surface:

```go
type Config struct {
	RoomID string
	Topic  string
	Log    *eventlog.Log
}

type Server struct {
	// listener, config, event log, participant map, connection map, mutex,
	// closed channel, and wait group
}

func New(addr string, cfg Config) (*Server, error)
func (s *Server) Addr() string
func (s *Server) Port() int
func (s *Server) Serve()
func (s *Server) Close() error
```

Implement these request handlers:

```go
func (s *Server) handleJoin(conn net.Conn, req protocol.JoinRequest) protocol.Response
func (s *Server) handleSend(name string, req protocol.SendRequest) protocol.Response
func (s *Server) handleHistory(req protocol.HistoryRequest) protocol.Response
func (s *Server) handleLeave(name string) protocol.Response
```

Behavior:
- Reject join when `req.RoomID != cfg.RoomID` with `room_mismatch`.
- Reject duplicate online names with `name_taken`.
- Append `participant.joined` on successful join.
- Append `message` on send, with mentions parsed against current participant names.
- Broadcast committed events to all connected clients.
- `history` returns transcript events from the event log.
- `Close` stops listener and waits for handlers.

- [ ] **Step 4: Run server tests**

Run: `go test ./internal/server -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/server
git commit -m "feat: add headless room server"
```

---

### Task 5: Participant Adapter And Control Socket

**Files:**
- Create: `internal/adapter/adapter.go`
- Create: `internal/adapter/control.go`
- Create: `internal/adapter/adapter_test.go`

- [ ] **Step 1: Write adapter tests**

Create `internal/adapter/adapter_test.go` with focused local-state tests:

```go
package adapter

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/khaiql/parley/internal/model"
)

func TestInboxPeekDoesNotAdvanceCursor(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "participant.json"), filepath.Join(t.TempDir(), "events.jsonl"))
	if err := store.AppendLocal(model.Event{Seq: 1, Type: model.EventMessage, RoomID: "r", Actor: "alice"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	events, err := store.Inbox(true)
	if err != nil {
		t.Fatalf("peek inbox: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %#v", events)
	}
	meta, err := store.LoadMeta()
	if err != nil {
		t.Fatalf("meta: %v", err)
	}
	if meta.LastSeenSeq != 0 {
		t.Fatalf("LastSeenSeq = %d, want 0", meta.LastSeenSeq)
	}
}

func TestWaitBatchReturnsInterveningEventsThroughMessage(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "participant.json"), filepath.Join(t.TempDir(), "events.jsonl"))
	_ = store.AppendLocal(model.Event{Seq: 1, Type: model.EventParticipantJoined, RoomID: "r", Actor: "bob"})
	_ = store.AppendLocal(model.Event{Seq: 2, Type: model.EventMessage, RoomID: "r", Actor: "alice"})
	events, err := store.WaitReadyBatch("me")
	if err != nil {
		t.Fatalf("WaitReadyBatch: %v", err)
	}
	if len(events) != 2 || events[0].Seq != 1 || events[1].Seq != 2 {
		t.Fatalf("events = %#v", events)
	}
}

func TestWaitTimeoutShape(t *testing.T) {
	result := WaitResult{Status: "timeout", Events: nil}
	if result.Status != "timeout" {
		t.Fatalf("status = %q", result.Status)
	}
	_ = time.Second
}
```

- [ ] **Step 2: Run adapter tests to verify they fail**

Run: `go test ./internal/adapter -run 'TestInbox|TestWait' -v`
Expected: FAIL with missing package or undefined symbols.

- [ ] **Step 3: Implement adapter store types**

Create `internal/adapter/adapter.go` with:

```go
type Meta struct {
	RoomID          string `json:"room_id"`
	Name            string `json:"name"`
	Role            string `json:"role"`
	Descriptor      string `json:"descriptor"`
	Status          string `json:"status"`
	LastReceivedSeq int64  `json:"last_received_seq"`
	LastSeenSeq     int64  `json:"last_seen_seq"`
}

type Store struct {
	MetaPath  string
	EventsPath string
}

func NewStore(metaPath, eventsPath string) *Store
func (s *Store) LoadMeta() (Meta, error)
func (s *Store) SaveMeta(meta Meta) error
func (s *Store) AppendLocal(ev model.Event) error
func (s *Store) Inbox(peek bool) ([]model.Event, error)
func (s *Store) WaitReadyBatch(self string) ([]model.Event, error)
```

Rules:
- `Inbox(false)` advances `LastSeenSeq` to the highest returned event sequence.
- `Inbox(true)` does not update meta.
- `WaitReadyBatch(self)` returns all unseen events through the first unseen `message` whose actor is not `self`.
- If no matching message exists, return an empty slice and nil error.

- [ ] **Step 4: Implement control socket request/response types**

Create `internal/adapter/control.go` with:

```go
type ControlRequest struct {
	Type    string `json:"type"`
	Text    string `json:"text,omitempty"`
	Peek    bool   `json:"peek,omitempty"`
	Limit   int    `json:"limit,omitempty"`
	Timeout string `json:"timeout,omitempty"`
}

type ControlResponse struct {
	OK     bool          `json:"ok"`
	Status string        `json:"status,omitempty"`
	Events []model.Event `json:"events,omitempty"`
	Error  string        `json:"error,omitempty"`
}
```

Implement `ServeControl(socketPath string, handler func(ControlRequest) ControlResponse) error` and `CallControl(socketPath string, req ControlRequest) (ControlResponse, error)` using Unix domain sockets.

- [ ] **Step 5: Run adapter tests**

Run: `go test ./internal/adapter -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/adapter
git commit -m "feat: add participant adapter store and control API"
```

---

### Task 6: CLI Command Skeleton And JSON Contracts

**Files:**
- Rewrite: `cmd/parley/main.go`
- Create: `cmd/parley/start.go`
- Rewrite: `cmd/parley/join.go`
- Create: `cmd/parley/invite.go`
- Create: `cmd/parley/participant_commands.go`
- Rewrite/Create: `cmd/parley/*_test.go`

- [ ] **Step 1: Write command validation tests**

Create `cmd/parley/main_test.go`:

```go
package main

import (
	"encoding/json"
	"testing"
)

func TestVersionJSON(t *testing.T) {
	out, err := executeForTest("version")
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	var body struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if body.Version == "" {
		t.Fatal("version should not be empty")
	}
}

func TestJoinRequiresName(t *testing.T) {
	_, err := executeForTest("join", "parley://127.0.0.1:1234/room-1")
	if err == nil {
		t.Fatal("expected join without --name to fail")
	}
}
```

Add test helper in the same file:

```go
func executeForTest(args ...string) ([]byte, error) {
	buf := new(bytes.Buffer)
	cmd := newRootCmd()
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.Bytes(), err
}
```

Include imports for `bytes`, `encoding/json`, and `testing`.

- [ ] **Step 2: Run command tests to verify they fail**

Run: `go test ./cmd/parley -run 'TestVersionJSON|TestJoinRequiresName' -v`
Expected: FAIL because `newRootCmd` and v1 commands do not exist.

- [ ] **Step 3: Rewrite root command**

Rewrite `cmd/parley/main.go` so `main()` calls `newRootCmd().Execute()` and `newRootCmd` registers:

```go
startCmd()
joinCmd()
inviteCmd()
infoCmd()
statusCmd()
inboxCmd()
historyCmd()
waitCmd()
sendCmd()
leaveCmd()
stopCmd()
versionCmd()
```

`versionCmd` returns:

```json
{
  "version": "dev",
  "protocol_version": "v1"
}
```

- [ ] **Step 4: Implement command stubs with JSON errors**

Create the command files with Cobra commands that validate arguments and return JSON errors for unimplemented runtime paths. At the end of this task:
- `version` works.
- `join` validates descriptor and requires `--name`.
- `start`, `invite`, `info`, `status`, `inbox`, `history`, `wait`, `send`, `leave`, and `stop` are registered and return a temporary `not_implemented` JSON error until Task 7 replaces each stub with runtime behavior.

- [ ] **Step 5: Run command tests**

Run: `go test ./cmd/parley -v`
Expected: PASS for command registration and validation tests.

- [ ] **Step 6: Commit**

```bash
git add cmd/parley
git commit -m "feat: add v1 JSON CLI skeleton"
```

---

### Task 7: Wire Runtime Commands

**Files:**
- Modify: `cmd/parley/start.go`
- Modify: `cmd/parley/join.go`
- Modify: `cmd/parley/invite.go`
- Modify: `cmd/parley/participant_commands.go`
- Modify: `internal/adapter/adapter.go`
- Modify: `internal/server/server.go`
- Create: `internal/runtime/runtime.go`
- Create: `internal/runtime/runtime_test.go`

- [ ] **Step 1: Write runtime tests**

Create `internal/runtime/runtime_test.go`:

```go
package runtime

import (
	"testing"

	"github.com/khaiql/parley/internal/paths"
)

func TestInviteFromRuntimeMetadata(t *testing.T) {
	p := paths.New(t.TempDir())
	meta := RoomRuntime{
		RoomID:    "room-1",
		LocalHost: "127.0.0.1",
		LocalPort: 49231,
	}
	if err := SaveRoomRuntime(p, meta); err != nil {
		t.Fatalf("SaveRoomRuntime: %v", err)
	}
	invite, err := Invite(p, "room-1")
	if err != nil {
		t.Fatalf("Invite: %v", err)
	}
	if invite.Descriptor != "parley://127.0.0.1:49231/room-1" {
		t.Fatalf("descriptor = %q", invite.Descriptor)
	}
}
```

- [ ] **Step 2: Implement runtime metadata**

Create `internal/runtime/runtime.go`:

```go
type RoomRuntime struct {
	RoomID    string `json:"room_id"`
	Topic     string `json:"topic,omitempty"`
	LocalHost string `json:"local_host"`
	LocalPort int    `json:"local_port"`
	ServerPID int    `json:"server_pid,omitempty"`
}

type InviteResponse struct {
	RoomID              string `json:"room_id"`
	Descriptor          string `json:"descriptor"`
	LocalHost           string `json:"local_host"`
	LocalPort           int    `json:"local_port"`
	JoinCommandTemplate string `json:"join_command_template"`
	AgentInstruction    string `json:"agent_instruction"`
}
```

Implement `SaveRoomRuntime`, `LoadRoomRuntime`, and `Invite`.

- [ ] **Step 3: Wire `parley invite` and `parley info`**

`parley invite` resolves active room unless `--room` is passed and returns `runtime.InviteResponse`.

`parley info` returns room id, descriptor, local host/port, active participant, and known local status.

- [ ] **Step 4: Wire `parley inbox`, `history`, and `status` file-backed behavior**

Implement:
- `inbox`: read active participant store and return unseen events; honor `--peek`.
- `history`: read local `events.jsonl`, filter transcript events, honor `--limit` and `--all`.
- `status`: return participant meta and socket/server availability where known.

- [ ] **Step 5: Wire `send`, `wait`, `leave`, and `stop` socket behavior**

Implement:
- `send`: call adapter control socket with text.
- `wait`: call adapter control socket with parsed timeout.
- `leave`: call adapter control socket.
- `stop`: call server local control socket.

If a required socket is absent, return JSON error `adapter_not_running` or `server_not_running`.

- [ ] **Step 6: Run runtime and command tests**

Run: `go test ./internal/runtime ./cmd/parley -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/runtime cmd/parley internal/adapter internal/server
git commit -m "feat: wire v1 runtime commands"
```

---

### Task 8: End-To-End Headless Flow

**Files:**
- Create: `internal/e2e/headless_test.go`
- Modify: `cmd/parley/start.go`
- Modify: `cmd/parley/join.go`
- Modify: `internal/adapter/adapter.go`
- Modify: `internal/server/server.go`

- [ ] **Step 1: Write e2e test**

Create `internal/e2e/headless_test.go`:

```go
package e2e

import (
	"testing"
	"time"
)

func TestHeadlessRoomTwoParticipants(t *testing.T) {
	root := t.TempDir()
	host := StartServerForTest(t, root, "topic", "host", "host")
	agent := JoinForTest(t, root, host.Descriptor, "agent", "reviewer")

	host.Send(t, "@agent please respond")
	got := agent.Wait(t, 2*time.Second)
	if len(got.Events) == 0 {
		t.Fatal("agent did not receive message")
	}

	agent.Send(t, "I am here")
	got = host.Wait(t, 2*time.Second)
	if len(got.Events) == 0 || got.Events[0].Actor != "agent" {
		t.Fatalf("host wait = %#v", got)
	}
}
```

- [ ] **Step 2: Add in-process test hooks**

Add package-level test helpers to start a server and two adapters in-process without daemonizing:

```go
func StartServerForTest(t testing.TB, root, topic, name, role string) ServerHandle
func JoinForTest(t testing.TB, root, descriptor, name, role string) AdapterHandle
```

The first test run should fail to compile until these helpers and handle methods exist. Implement the helpers in non-test runtime packages if the CLI needs the same start/join orchestration, or in `internal/e2e` if they are pure test harness glue.

- [ ] **Step 3: Run e2e test**

Run: `go test ./internal/e2e -run TestHeadlessRoomTwoParticipants -v`
Expected: PASS.

- [ ] **Step 4: Run broad tests**

Run: `go test ./internal/... ./cmd/parley -timeout 30s`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/e2e internal/adapter internal/server cmd/parley
git commit -m "test: add headless room e2e flow"
```

---

### Task 9: Remove Old TUI, Client, Room, Driver, Dispatcher, Command, Persistence, And Export Surfaces

**Files:**
- Delete: `cmd/parley/host.go`
- Delete: `cmd/parley/export.go`
- Delete: `cmd/parley/join_host_test.go`
- Delete: `cmd/parley/name_test.go`
- Delete: `internal/client/`
- Delete: `internal/room/`
- Delete: `internal/persistence/`
- Delete: `internal/tui/`
- Delete: `internal/driver/`
- Delete: `internal/dispatcher/`
- Delete: `internal/command/`
- Delete: `internal/web/`
- Modify: `go.mod`
- Modify: `go.sum`
- Modify: `README.md`
- Modify: `CLAUDE.md`

- [ ] **Step 1: Delete old source directories and command files**

Remove old files/directories:

```bash
rm -rf internal/client internal/room internal/persistence internal/tui internal/driver internal/dispatcher internal/command internal/web
rm -f cmd/parley/host.go cmd/parley/export.go cmd/parley/join_host_test.go cmd/parley/name_test.go
```

- [ ] **Step 2: Run tests to expose stale imports**

Run: `go test ./... -timeout 30s`
Expected: FAIL if any old imports remain.

- [ ] **Step 3: Remove stale imports and old tests**

Fix or delete tests that refer to removed packages:
- `internal/integration_test.go`
- `internal/smoke_test.go`
- old `cmd/parley` tests that assert TUI-era behavior
- old `internal/protocol` tests for agent types/status

Keep only tests that validate the v1 headless behavior.

- [ ] **Step 4: Tidy dependencies**

Run: `go mod tidy`
Expected: Bubble Tea, Lipgloss, Glamour, Bubbles, and stateless dependencies are removed if unused.

- [ ] **Step 5: Update README and CLAUDE architecture**

Rewrite README quick start around:

```bash
parley start --topic "debug parser" --name codex --role host
parley invite
parley join "parley://127.0.0.1:49231/01j..." --name codex-auth --role "auth reviewer"
parley wait --timeout 10m
parley send "I found the issue"
```

Update `CLAUDE.md` package map to remove TUI/driver/dispatcher/export references and add model/eventlog/adapter/runtime.

- [ ] **Step 6: Run full tests**

Run: `go test ./... -timeout 30s`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add .
git commit -m "refactor: remove old tui and subprocess surfaces"
```

---

### Task 10: Skill, Installer, And Release Configuration

**Files:**
- Create: `skills/parley/SKILL.md`
- Create: `skills/parley/scripts/ensure-parley`
- Create: `install.sh`
- Modify: `.goreleaser.yaml`
- Modify: `.github/workflows/release.yml` if release assets need installer upload
- Modify: `README.md`

- [ ] **Step 1: Write skill file**

Create `skills/parley/SKILL.md`:

```markdown
---
name: parley
description: Join and host Parley headless collaboration rooms with other coding agents.
---

Before running any Parley command, run `scripts/ensure-parley` from this skill and use the returned binary path.

Use Parley when the user asks to collaborate with another agent, host a room, join a room, wait for another agent, or exchange messages with agents through a `parley://` room descriptor.

Core workflow:

1. To host: run `parley start --topic "<topic>" --name "<your-name>" --role "<your-role>"`.
2. To invite: run `parley invite` and show the user the `agent_instruction` field.
3. To join: run `parley join "<descriptor>" --name "<your-name>" --role "<your-role>"`.
4. To check messages: run `parley inbox`.
5. To wait for replies: run `parley wait --timeout 10m`.
6. To send: run `parley send "<message>"`.
7. To leave: run `parley leave`.

Parley outputs JSON. Read the JSON fields directly instead of scraping prose.
```

- [ ] **Step 2: Write ensure-parley script**

Create executable `skills/parley/scripts/ensure-parley`:

```sh
#!/bin/sh
set -eu

if [ "${PARLEY_BIN:-}" != "" ] && [ -x "$PARLEY_BIN" ]; then
  printf '%s\n' "$PARLEY_BIN"
  exit 0
fi

if command -v parley >/dev/null 2>&1; then
  command -v parley
  exit 0
fi

target="${HOME}/.parley/bin/parley"
if [ -x "$target" ]; then
  printf '%s\n' "$target"
  exit 0
fi

mkdir -p "${HOME}/.parley/bin"

if command -v brew >/dev/null 2>&1; then
  brew install khaiql/parley/parley
  if command -v parley >/dev/null 2>&1; then
    command -v parley
    exit 0
  fi
fi

if command -v curl >/dev/null 2>&1; then
  curl -fsSL https://raw.githubusercontent.com/khaiql/parley/main/install.sh | sh
  if [ -x "$target" ]; then
    printf '%s\n' "$target"
    exit 0
  fi
fi

if command -v go >/dev/null 2>&1; then
  GOBIN="${HOME}/.parley/bin" go install github.com/khaiql/parley/cmd/parley@latest
  printf '%s\n' "$target"
  exit 0
fi

printf '%s\n' "parley not installed and no installer path succeeded" >&2
exit 1
```

- [ ] **Step 3: Write installer script**

Create executable `install.sh` that:
- detects `uname -s` and `uname -m`
- maps to GoReleaser archive names for Darwin/Linux arm64/x86_64
- downloads latest GitHub release archive
- installs `parley` into `~/.parley/bin/parley`
- prints the installed path

- [ ] **Step 4: Update GoReleaser**

Modify `.goreleaser.yaml`:
- remove Windows builds
- keep darwin/linux amd64/arm64
- keep tar.gz archives
- keep checksums

- [ ] **Step 5: Run script shell checks**

Run:

```bash
sh -n install.sh
sh -n skills/parley/scripts/ensure-parley
```

Expected: both commands exit 0.

- [ ] **Step 6: Commit**

```bash
git add skills/parley install.sh .goreleaser.yaml .github/workflows/release.yml README.md
git commit -m "feat: ship parley agent skill and installer"
```

---

### Task 11: Final Verification

**Files:**
- Modify the exact files identified by failing verification commands, then rerun the failed command before proceeding.

- [ ] **Step 1: Run full build**

Run: `go build ./...`
Expected: PASS.

- [ ] **Step 2: Run full tests**

Run: `go test ./... -timeout 30s`
Expected: PASS.

- [ ] **Step 3: Run race tests**

Run: `go test ./... -timeout 30s -race`
Expected: PASS.

- [ ] **Step 4: Run lint**

Run: `go run github.com/golangci/golangci-lint/cmd/golangci-lint@latest run ./... --timeout=5m`
Expected: PASS.

- [ ] **Step 5: Run manual local smoke**

Build:

```bash
go build -o /tmp/parley ./cmd/parley
```

Start:

```bash
/tmp/parley start --topic "smoke" --name host --role host
```

Expected: JSON contains `room_id`, `descriptor`, `local_port`, and `host_participant`.

Invite:

```bash
/tmp/parley invite
```

Expected: JSON contains `agent_instruction`.

Join from another shell:

```bash
/tmp/parley join "parley://127.0.0.1:<port>/<room-id>" --name agent --role reviewer
```

Send/wait:

```bash
/tmp/parley send "@agent hello"
/tmp/parley wait --timeout 5s
```

Expected: waiting participant receives a message event.

- [ ] **Step 6: Commit verification fixes**

If any verification-only fixes were needed:

```bash
git add .
git commit -m "fix: pass headless pivot verification"
```

If no fixes were needed, do not create an empty commit.
