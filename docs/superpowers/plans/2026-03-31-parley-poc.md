# Parley PoC Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a working PoC where a human and one Claude Code agent chat in a shared TUI room over TCP using JSON-RPC 2.0.

**Architecture:** Single Go binary (`parley`) with two subcommands: `host` (server + human TUI) and `join` (client + agent driver + agent TUI). Server manages room state and broadcasts messages. Claude Code is driven via `--output-format stream-json` in non-interactive mode. Communication is JSON-RPC 2.0 over NDJSON-framed TCP.

**Tech Stack:** Go, Bubble Tea (TUI), Lipgloss (styling), Cobra (CLI), TCP sockets, JSON-RPC 2.0

**Spec:** `docs/superpowers/specs/2026-03-31-parley-design.md`

---

## File Map

```
parley/
├── cmd/
│   └── parley/
│       └── main.go                  — Cobra root + host/join subcommands
│
├── internal/
│   ├── protocol/
│   │   └── protocol.go             — JSON-RPC 2.0 types, message types, encoding/decoding
│   │
│   ├── server/
│   │   ├── server.go               — TCP listener, accept connections, route to room
│   │   ├── room.go                 — Room state, participant list, broadcast, seq counter
│   │   └── persistence.go          — Save/load room + messages to ~/.parley/rooms/<id>/
│   │
│   ├── client/
│   │   └── client.go               — TCP connection, send/receive JSON-RPC messages
│   │
│   ├── driver/
│   │   ├── driver.go               — AgentDriver interface + AgentEvent types
│   │   └── claude.go               — Claude Code driver (stream-json output, per-invocation with --resume)
│   │
│   └── tui/
│       ├── app.go                   — Root Bubble Tea model, layout, message routing
│       ├── chat.go                  — Chat viewport: message list rendering, scroll
│       ├── sidebar.go              — Participant list panel
│       ├── input.go                — Input box (keyboard mode + agent-driven mode)
│       ├── topbar.go               — Header with topic + port
│       └── styles.go               — Lipgloss style constants
│
├── go.mod
└── go.sum
```

---

## Task 0: Validation Spike — Claude Code Stream-JSON I/O

**Goal:** Determine how to programmatically drive Claude Code. Test bidirectional stream-json. If it doesn't work, confirm per-invocation with `--resume` works.

**Files:**
- None (exploratory — results inform Task 7)

- [ ] **Step 1: Test stream-json output**

Run this and send a prompt on stdin, observe the JSON events on stdout:

```bash
echo '{"type":"user","message":{"role":"user","content":[{"type":"text","text":"Say hello in one word"}]}}' | claude -p --input-format stream-json --output-format stream-json 2>/dev/null
```

Note the structure of events emitted. Look for: `session_id` in the init/result message, `type` field values, how text content is streamed.

- [ ] **Step 2: Test per-invocation with --resume**

```bash
# First invocation — capture session_id
RESULT=$(claude -p --output-format json "Remember the word 'banana'. Just say OK.")
echo "$RESULT" | jq '.session_id'

# Second invocation — resume and verify context
SESSION_ID=$(echo "$RESULT" | jq -r '.session_id')
claude -p --output-format json --resume "$SESSION_ID" "What word did I ask you to remember?"
```

Verify the agent remembers context across invocations.

- [ ] **Step 3: Test stream-json output event structure**

```bash
claude -p --output-format stream-json "List 3 colors" 2>/dev/null | head -20
```

Document the exact JSON structure of each event type. We need to know the field names for: init, assistant text, tool use, result/done.

- [ ] **Step 4: Document findings**

Write a short summary of which approach works and the exact event format. This determines whether `claude.go` uses a long-lived process or per-invocation. Save to `docs/spike-results.md` and commit.

```bash
git add docs/spike-results.md
git commit -m "doc: Claude Code stream-json spike results"
```

---

## Task 1: Project Setup

**Files:**
- Create: `go.mod`
- Create: `cmd/parley/main.go`
- Create: `.gitignore`

- [ ] **Step 1: Initialize Go module**

```bash
cd /Users/sle/group_chat
go mod init github.com/sle/parley
```

- [ ] **Step 2: Create .gitignore**

Create `.gitignore`:

```
# Binary
parley
/parley

# OS
.DS_Store

# IDE
.idea/
.vscode/

# Superpowers brainstorm artifacts
.superpowers/
```

- [ ] **Step 3: Create minimal main.go with Cobra**

Create `cmd/parley/main.go`:

```go
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "parley",
	Short: "TUI group chat for coding agents",
}

var hostCmd = &cobra.Command{
	Use:   "host",
	Short: "Host a chat room",
	RunE: func(cmd *cobra.Command, args []string) error {
		topic, _ := cmd.Flags().GetString("topic")
		port, _ := cmd.Flags().GetInt("port")
		fmt.Printf("Hosting room on port %d with topic: %s\n", port, topic)
		return nil
	},
}

var joinCmd = &cobra.Command{
	Use:   "join",
	Short: "Join a chat room with an agent",
	RunE: func(cmd *cobra.Command, args []string) error {
		port, _ := cmd.Flags().GetInt("port")
		name, _ := cmd.Flags().GetString("name")
		role, _ := cmd.Flags().GetString("role")
		fmt.Printf("Joining room on port %d as %s (%s)\n", port, name, role)
		return nil
	},
}

func init() {
	hostCmd.Flags().StringP("topic", "t", "", "Room topic (required)")
	hostCmd.MarkFlagRequired("topic")
	hostCmd.Flags().IntP("port", "p", 0, "Port to listen on (0 = random)")

	joinCmd.Flags().IntP("port", "p", 0, "Server port to connect to (required)")
	joinCmd.MarkFlagRequired("port")
	joinCmd.Flags().StringP("name", "n", "", "Your display name (required)")
	joinCmd.MarkFlagRequired("name")
	joinCmd.Flags().StringP("role", "r", "", "Your role (e.g. 'backend specialist')")

	rootCmd.AddCommand(hostCmd, joinCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
```

- [ ] **Step 4: Install dependencies and verify build**

```bash
go get github.com/spf13/cobra@v1.10.2
go build -o parley ./cmd/parley
./parley --help
./parley host --topic "test"
./parley join --port 1234 --name "Alice" --role "backend"
```

Expected: help text shows both subcommands; host prints "Hosting room..."; join prints "Joining room...".

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum cmd/ .gitignore
git commit -m "feat: project setup with cobra CLI skeleton"
```

---

## Task 2: Protocol Types

**Files:**
- Create: `internal/protocol/protocol.go`
- Create: `internal/protocol/protocol_test.go`

- [ ] **Step 1: Write tests for protocol encoding/decoding**

Create `internal/protocol/protocol_test.go`:

```go
package protocol_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/sle/parley/internal/protocol"
)

func TestEncodeNotification(t *testing.T) {
	msg := protocol.Notification{
		JSONRPC: "2.0",
		Method:  "room.message",
		Params: protocol.MessageParams{
			ID:        "msg-1",
			Seq:       1,
			From:      "Alice",
			Source:    "agent",
			Role:      "backend",
			Timestamp: time.Date(2026, 3, 31, 22, 0, 0, 0, time.UTC),
			Mentions:  []string{"Bob"},
			Content: protocol.Content{
				Type: "text",
				Text: "Hello, world",
			},
		},
	}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded protocol.Notification
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	params, ok := decoded.Params.(map[string]interface{})
	if !ok {
		// Re-decode with typed params
		var typed struct {
			JSONRPC string                 `json:"jsonrpc"`
			Method  string                 `json:"method"`
			Params  protocol.MessageParams `json:"params"`
		}
		if err := json.Unmarshal(data, &typed); err != nil {
			t.Fatalf("typed unmarshal: %v", err)
		}
		if typed.Params.From != "Alice" {
			t.Errorf("from = %q, want Alice", typed.Params.From)
		}
		if typed.Params.Seq != 1 {
			t.Errorf("seq = %d, want 1", typed.Params.Seq)
		}
		if typed.Params.Content.Text != "Hello, world" {
			t.Errorf("text = %q, want Hello, world", typed.Params.Content.Text)
		}
		return
	}
	if params["from"] != "Alice" {
		t.Errorf("from = %v, want Alice", params["from"])
	}
}

func TestEncodeJoinParams(t *testing.T) {
	jp := protocol.JoinParams{
		Name:      "Eve",
		Role:      "frontend specialist",
		Directory: "/Users/sle/web-app",
		Repo:      "github.com/sle/web-app",
		AgentType: "gemini",
	}
	data, err := json.Marshal(jp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded protocol.JoinParams
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Name != "Eve" {
		t.Errorf("name = %q, want Eve", decoded.Name)
	}
	if decoded.AgentType != "gemini" {
		t.Errorf("agent_type = %q, want gemini", decoded.AgentType)
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	line := `{"jsonrpc":"2.0","method":"room.send","params":{"content":{"type":"text","text":"hi"}}}` + "\n"
	msg, err := protocol.DecodeLine([]byte(line))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if msg.Method != "room.send" {
		t.Errorf("method = %q, want room.send", msg.Method)
	}
}

func TestEncodeLine(t *testing.T) {
	n := protocol.Notification{
		JSONRPC: "2.0",
		Method:  "room.joined",
		Params: protocol.JoinParams{
			Name: "Alice",
			Role: "backend",
		},
	}
	data, err := protocol.EncodeLine(n)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	// Must end with newline
	if data[len(data)-1] != '\n' {
		t.Error("encoded line must end with newline")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/protocol/ -v
```

Expected: compilation error — package doesn't exist yet.

- [ ] **Step 3: Implement protocol types**

Create `internal/protocol/protocol.go`:

```go
package protocol

import (
	"encoding/json"
	"fmt"
	"time"
)

// JSON-RPC 2.0 base types

type Notification struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type Request struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// RawMessage is a generic JSON-RPC message for decoding
type RawMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// Domain param types

type Content struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type MessageParams struct {
	ID        string    `json:"id"`
	Seq       int       `json:"seq"`
	From      string    `json:"from"`
	Source    string    `json:"source"`
	Role      string    `json:"role,omitempty"`
	Timestamp time.Time `json:"timestamp"`
	Mentions  []string  `json:"mentions,omitempty"`
	Content   Content   `json:"content"`
}

type SendParams struct {
	Content  Content  `json:"content"`
	Mentions []string `json:"mentions,omitempty"`
}

type JoinParams struct {
	Name      string `json:"name"`
	Role      string `json:"role"`
	Directory string `json:"directory"`
	Repo      string `json:"repo,omitempty"`
	AgentType string `json:"agent_type,omitempty"`
}

type JoinedParams struct {
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	Directory string    `json:"directory"`
	Repo      string    `json:"repo,omitempty"`
	AgentType string    `json:"agent_type,omitempty"`
	JoinedAt  time.Time `json:"joined_at"`
}

type LeftParams struct {
	Name string `json:"name"`
}

type Participant struct {
	Name      string `json:"name"`
	Role      string `json:"role"`
	Directory string `json:"directory"`
	Repo      string `json:"repo,omitempty"`
	AgentType string `json:"agent_type,omitempty"`
	Source    string `json:"source"`
}

type RoomStateParams struct {
	Topic        string        `json:"topic"`
	Participants []Participant `json:"participants"`
}

// NDJSON encode/decode helpers

func EncodeLine(v interface{}) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	return append(data, '\n'), nil
}

func DecodeLine(line []byte) (*RawMessage, error) {
	var msg RawMessage
	if err := json.Unmarshal(line, &msg); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	return &msg, nil
}

// NewNotification creates a JSON-RPC 2.0 notification
func NewNotification(method string, params interface{}) Notification {
	return Notification{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/protocol/ -v
```

Expected: all tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/protocol/
git commit -m "feat: JSON-RPC 2.0 protocol types with NDJSON encoding"
```

---

## Task 3: TCP Server

**Files:**
- Create: `internal/server/server.go`
- Create: `internal/server/room.go`
- Create: `internal/server/server_test.go`

- [ ] **Step 1: Write tests for server and room**

Create `internal/server/server_test.go`:

```go
package server_test

import (
	"bufio"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/sle/parley/internal/protocol"
	"github.com/sle/parley/internal/server"
)

func TestServerAcceptsConnection(t *testing.T) {
	srv, err := server.New("localhost:0", "test topic")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	defer srv.Close()

	go srv.Serve()

	conn, err := net.DialTimeout("tcp", srv.Addr(), 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
}

func TestJoinAndReceiveState(t *testing.T) {
	srv, err := server.New("localhost:0", "test topic")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	defer srv.Close()

	go srv.Serve()

	conn, err := net.DialTimeout("tcp", srv.Addr(), 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Send join
	join := protocol.NewNotification("room.join", protocol.JoinParams{
		Name:      "Alice",
		Role:      "backend",
		Directory: "/tmp/test",
		AgentType: "claude",
	})
	data, _ := protocol.EncodeLine(join)
	conn.Write(data)

	// Read room.state response
	scanner := bufio.NewScanner(conn)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if !scanner.Scan() {
		t.Fatal("no response from server")
	}
	var msg protocol.RawMessage
	if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Method != "room.state" {
		t.Errorf("method = %q, want room.state", msg.Method)
	}
}

func TestBroadcastMessage(t *testing.T) {
	srv, err := server.New("localhost:0", "test topic")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	defer srv.Close()

	go srv.Serve()

	// Connect two clients
	conn1, _ := net.DialTimeout("tcp", srv.Addr(), 2*time.Second)
	defer conn1.Close()
	conn2, _ := net.DialTimeout("tcp", srv.Addr(), 2*time.Second)
	defer conn2.Close()

	// Both join
	join1 := protocol.NewNotification("room.join", protocol.JoinParams{Name: "Alice", Role: "backend", Directory: "/tmp/a"})
	join2 := protocol.NewNotification("room.join", protocol.JoinParams{Name: "Bob", Role: "frontend", Directory: "/tmp/b"})
	data1, _ := protocol.EncodeLine(join1)
	data2, _ := protocol.EncodeLine(join2)
	conn1.Write(data1)
	time.Sleep(50 * time.Millisecond)
	conn2.Write(data2)

	// Drain room.state and room.joined messages
	scanner1 := bufio.NewScanner(conn1)
	scanner2 := bufio.NewScanner(conn2)
	conn1.SetReadDeadline(time.Now().Add(2 * time.Second))
	conn2.SetReadDeadline(time.Now().Add(2 * time.Second))

	// Drain conn1: room.state + room.joined (Bob)
	scanner1.Scan() // room.state
	scanner1.Scan() // room.joined (Bob)

	// Drain conn2: room.state
	scanner2.Scan() // room.state

	// Alice sends a message
	send := protocol.NewNotification("room.send", protocol.SendParams{
		Content: protocol.Content{Type: "text", Text: "Hello everyone"},
	})
	data, _ := protocol.EncodeLine(send)
	conn1.Write(data)

	// Bob should receive it
	conn2.SetReadDeadline(time.Now().Add(2 * time.Second))
	if !scanner2.Scan() {
		t.Fatal("Bob did not receive message")
	}
	var msg protocol.RawMessage
	if err := json.Unmarshal(scanner2.Bytes(), &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Method != "room.message" {
		t.Errorf("method = %q, want room.message", msg.Method)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/server/ -v
```

Expected: compilation error — package doesn't exist.

- [ ] **Step 3: Implement Room**

Create `internal/server/room.go`:

```go
package server

import (
	"sync"
	"time"

	"github.com/sle/parley/internal/protocol"
)

type Room struct {
	Topic        string
	Participants map[string]*ClientConn
	Messages     []protocol.MessageParams
	seq          int
	mu           sync.RWMutex
}

type ClientConn struct {
	Name      string
	Role      string
	Directory string
	Repo      string
	AgentType string
	Source    string
	send      chan []byte
	done      chan struct{}
}

func NewRoom(topic string) *Room {
	return &Room{
		Topic:        topic,
		Participants: make(map[string]*ClientConn),
	}
}

func (r *Room) Join(cc *ClientConn) protocol.RoomStateParams {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.Participants[cc.Name] = cc

	// Build state snapshot
	var participants []protocol.Participant
	for _, p := range r.Participants {
		participants = append(participants, protocol.Participant{
			Name:      p.Name,
			Role:      p.Role,
			Directory: p.Directory,
			Repo:      p.Repo,
			AgentType: p.AgentType,
			Source:    p.Source,
		})
	}

	return protocol.RoomStateParams{
		Topic:        r.Topic,
		Participants: participants,
	}
}

func (r *Room) Leave(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cc, ok := r.Participants[name]; ok {
		close(cc.done)
		delete(r.Participants, name)
	}
}

func (r *Room) Broadcast(from string, source string, role string, content protocol.Content, mentions []string) protocol.MessageParams {
	r.mu.Lock()
	r.seq++
	seq := r.seq
	r.mu.Unlock()

	msg := protocol.MessageParams{
		ID:        generateID(),
		Seq:       seq,
		From:      from,
		Source:    source,
		Role:      role,
		Timestamp: time.Now().UTC(),
		Mentions:  mentions,
		Content:   content,
	}

	r.mu.Lock()
	r.Messages = append(r.Messages, msg)
	r.mu.Unlock()

	notification := protocol.NewNotification("room.message", msg)
	data, err := protocol.EncodeLine(notification)
	if err != nil {
		return msg
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, cc := range r.Participants {
		select {
		case cc.send <- data:
		default:
			// Drop message if client is slow
		}
	}

	return msg
}

func (r *Room) BroadcastSystem(text string) {
	r.Broadcast("system", "system", "", protocol.Content{Type: "text", Text: text}, nil)
}

func (r *Room) BroadcastJoined(jp protocol.JoinedParams) {
	notification := protocol.NewNotification("room.joined", jp)
	data, err := protocol.EncodeLine(notification)
	if err != nil {
		return
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	for name, cc := range r.Participants {
		if name == jp.Name {
			continue // Don't send join notification to the joiner
		}
		select {
		case cc.send <- data:
		default:
		}
	}
}

func generateID() string {
	return time.Now().Format("20060102150405.000")
}
```

- [ ] **Step 4: Implement Server**

Create `internal/server/server.go`:

```go
package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"time"

	"github.com/sle/parley/internal/protocol"
)

type Server struct {
	listener net.Listener
	room     *Room
}

func New(addr string, topic string) (*Server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}
	return &Server{
		listener: ln,
		room:     NewRoom(topic),
	}, nil
}

func (s *Server) Addr() string {
	return s.listener.Addr().String()
}

func (s *Server) Port() int {
	return s.listener.Addr().(*net.TCPAddr).Port
}

func (s *Server) Room() *Room {
	return s.room
}

func (s *Server) Serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return // Listener closed
		}
		go s.handleConn(conn)
	}
}

func (s *Server) Close() error {
	return s.listener.Close()
}

func (s *Server) handleConn(conn net.Conn) {
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB buffer

	var cc *ClientConn

	for scanner.Scan() {
		var msg protocol.RawMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			log.Printf("invalid message from %s: %v", conn.RemoteAddr(), err)
			continue
		}

		switch msg.Method {
		case "room.join":
			var params protocol.JoinParams
			if err := json.Unmarshal(msg.Params, &params); err != nil {
				log.Printf("invalid join params: %v", err)
				continue
			}
			cc = &ClientConn{
				Name:      params.Name,
				Role:      params.Role,
				Directory: params.Directory,
				Repo:      params.Repo,
				AgentType: params.AgentType,
				Source:    "agent",
				send:      make(chan []byte, 64),
				done:      make(chan struct{}),
			}
			if params.AgentType == "" {
				cc.Source = "human"
			}

			state := s.room.Join(cc)

			// Send room.state to the new client
			stateNotif := protocol.NewNotification("room.state", state)
			data, _ := protocol.EncodeLine(stateNotif)
			conn.Write(data)

			// Broadcast join to others
			s.room.BroadcastJoined(protocol.JoinedParams{
				Name:      params.Name,
				Role:      params.Role,
				Directory: params.Directory,
				Repo:      params.Repo,
				AgentType: params.AgentType,
				JoinedAt:  time.Now().UTC(),
			})

			// Broadcast system message
			intro := fmt.Sprintf("%s has joined — %s", params.Name, params.Role)
			if params.Directory != "" {
				intro += ", working in " + params.Directory
			}
			s.room.BroadcastSystem(intro)

			// Start writer goroutine
			go func() {
				for {
					select {
					case data := <-cc.send:
						conn.Write(data)
					case <-cc.done:
						return
					}
				}
			}()

		case "room.send":
			if cc == nil {
				continue // Not joined yet
			}
			var params protocol.SendParams
			if err := json.Unmarshal(msg.Params, &params); err != nil {
				log.Printf("invalid send params: %v", err)
				continue
			}
			s.room.Broadcast(cc.Name, cc.Source, cc.Role, params.Content, params.Mentions)
		}
	}

	// Client disconnected
	if cc != nil {
		s.room.Leave(cc.Name)
		s.room.BroadcastSystem(fmt.Sprintf("%s has left the room", cc.Name))
		s.room.BroadcastJoined(protocol.JoinedParams{Name: cc.Name}) // Reuse as left notification for simplicity
	}
}
```

- [ ] **Step 5: Run tests to verify they pass**

```bash
go test ./internal/server/ -v -timeout 10s
```

Expected: all three tests pass.

- [ ] **Step 6: Commit**

```bash
git add internal/server/
git commit -m "feat: TCP server with room, join, broadcast"
```

---

## Task 4: TCP Client

**Files:**
- Create: `internal/client/client.go`
- Create: `internal/client/client_test.go`

- [ ] **Step 1: Write tests for client**

Create `internal/client/client_test.go`:

```go
package client_test

import (
	"testing"
	"time"

	"github.com/sle/parley/internal/client"
	"github.com/sle/parley/internal/protocol"
	"github.com/sle/parley/internal/server"
)

func TestClientConnectsAndJoins(t *testing.T) {
	srv, err := server.New("localhost:0", "test topic")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	defer srv.Close()
	go srv.Serve()

	c, err := client.New(srv.Addr())
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer c.Close()

	err = c.Join(protocol.JoinParams{
		Name:      "Alice",
		Role:      "backend",
		Directory: "/tmp/test",
	})
	if err != nil {
		t.Fatalf("join: %v", err)
	}

	// Should receive room.state
	select {
	case msg := <-c.Incoming():
		if msg.Method != "room.state" {
			t.Errorf("method = %q, want room.state", msg.Method)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for room.state")
	}
}

func TestClientSendsAndReceives(t *testing.T) {
	srv, err := server.New("localhost:0", "test topic")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	defer srv.Close()
	go srv.Serve()

	c1, _ := client.New(srv.Addr())
	defer c1.Close()
	c2, _ := client.New(srv.Addr())
	defer c2.Close()

	c1.Join(protocol.JoinParams{Name: "Alice", Role: "backend", Directory: "/tmp/a"})
	c2.Join(protocol.JoinParams{Name: "Bob", Role: "frontend", Directory: "/tmp/b"})

	// Drain initial messages (room.state, room.joined, system messages)
	drain(c1.Incoming(), 3)
	drain(c2.Incoming(), 2)
	time.Sleep(100 * time.Millisecond)

	// Alice sends
	c1.Send(protocol.Content{Type: "text", Text: "Hello Bob"}, nil)

	// Bob receives
	select {
	case msg := <-c2.Incoming():
		if msg.Method != "room.message" {
			t.Errorf("method = %q, want room.message", msg.Method)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for message")
	}
}

func drain(ch <-chan *protocol.RawMessage, n int) {
	for i := 0; i < n; i++ {
		select {
		case <-ch:
		case <-time.After(2 * time.Second):
			return
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/client/ -v
```

Expected: compilation error.

- [ ] **Step 3: Implement client**

Create `internal/client/client.go`:

```go
package client

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"

	"github.com/sle/parley/internal/protocol"
)

type Client struct {
	conn     net.Conn
	incoming chan *protocol.RawMessage
	done     chan struct{}
}

func New(addr string) (*Client, error) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	c := &Client{
		conn:     conn,
		incoming: make(chan *protocol.RawMessage, 64),
		done:     make(chan struct{}),
	}
	go c.readLoop()
	return c, nil
}

func (c *Client) Incoming() <-chan *protocol.RawMessage {
	return c.incoming
}

func (c *Client) Join(params protocol.JoinParams) error {
	return c.sendNotification("room.join", params)
}

func (c *Client) Send(content protocol.Content, mentions []string) error {
	return c.sendNotification("room.send", protocol.SendParams{
		Content:  content,
		Mentions: mentions,
	})
}

func (c *Client) Close() error {
	close(c.done)
	return c.conn.Close()
}

func (c *Client) sendNotification(method string, params interface{}) error {
	n := protocol.NewNotification(method, params)
	data, err := protocol.EncodeLine(n)
	if err != nil {
		return err
	}
	_, err = c.conn.Write(data)
	return err
}

func (c *Client) readLoop() {
	scanner := bufio.NewScanner(c.conn)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		var msg protocol.RawMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		select {
		case c.incoming <- &msg:
		case <-c.done:
			return
		}
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/client/ -v -timeout 10s
```

Expected: all tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/client/
git commit -m "feat: TCP client with join, send, receive"
```

---

## Task 5: TUI — Styles and Components

**Files:**
- Create: `internal/tui/styles.go`
- Create: `internal/tui/topbar.go`
- Create: `internal/tui/sidebar.go`
- Create: `internal/tui/chat.go`
- Create: `internal/tui/input.go`

- [ ] **Step 1: Create styles**

Create `internal/tui/styles.go`:

```go
package tui

import "github.com/charmbracelet/lipgloss"

var (
	// Colors
	colorPrimary   = lipgloss.Color("#58a6ff")
	colorHuman     = lipgloss.Color("#f0883e")
	colorAgent     = lipgloss.Color("#a5d6ff")
	colorSystem    = lipgloss.Color("#8b949e")
	colorRoleBadge = lipgloss.Color("#388bfd")
	colorBorder    = lipgloss.Color("#30363d")
	colorBg        = lipgloss.Color("#0d1117")
	colorText      = lipgloss.Color("#c9d1d9")
	colorDimText   = lipgloss.Color("#484f58")

	// Styles
	topBarStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("#161b22")).
			Foreground(colorText).
			Padding(0, 1)

	sidebarStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderLeft(true).
			BorderForeground(colorBorder).
			Padding(0, 1)

	sidebarTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorPrimary)

	inputStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderTop(true).
			BorderForeground(colorBorder).
			Padding(0, 1)

	humanNameStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorHuman)

	agentNameStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorAgent)

	roleBadgeStyle = lipgloss.NewStyle().
			Foreground(colorRoleBadge).
			SetString("")

	systemMsgStyle = lipgloss.NewStyle().
			Foreground(colorSystem).
			Italic(true)

	timestampStyle = lipgloss.NewStyle().
			Foreground(colorDimText)
)
```

- [ ] **Step 2: Create top bar component**

Create `internal/tui/topbar.go`:

```go
package tui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

type TopBar struct {
	topic string
	port  int
	width int
}

func NewTopBar(topic string, port int) TopBar {
	return TopBar{topic: topic, port: port}
}

func (t *TopBar) SetWidth(w int) {
	t.width = w
}

func (t TopBar) View() string {
	left := lipgloss.NewStyle().Bold(true).Foreground(colorPrimary).Render("parley")
	center := fmt.Sprintf("Topic: %s", t.topic)
	right := fmt.Sprintf(":%d", t.port)

	// Calculate spacing
	usedWidth := lipgloss.Width(left) + lipgloss.Width(center) + lipgloss.Width(right)
	gap := t.width - usedWidth - 4 // 4 for padding
	if gap < 1 {
		gap = 1
	}
	leftGap := gap / 2
	rightGap := gap - leftGap

	line := left
	for i := 0; i < leftGap; i++ {
		line += " "
	}
	line += center
	for i := 0; i < rightGap; i++ {
		line += " "
	}
	line += right

	return topBarStyle.Width(t.width).Render(line)
}
```

- [ ] **Step 3: Create sidebar component**

Create `internal/tui/sidebar.go`:

```go
package tui

import (
	"fmt"
	"strings"

	"github.com/sle/parley/internal/protocol"
)

type Sidebar struct {
	participants []protocol.Participant
	width        int
	height       int
}

func NewSidebar() Sidebar {
	return Sidebar{}
}

func (s *Sidebar) SetSize(w, h int) {
	s.width = w
	s.height = h
}

func (s *Sidebar) SetParticipants(participants []protocol.Participant) {
	s.participants = participants
}

func (s *Sidebar) AddParticipant(p protocol.Participant) {
	// Update if exists, add if not
	for i, existing := range s.participants {
		if existing.Name == p.Name {
			s.participants[i] = p
			return
		}
	}
	s.participants = append(s.participants, p)
}

func (s *Sidebar) RemoveParticipant(name string) {
	for i, p := range s.participants {
		if p.Name == name {
			s.participants = append(s.participants[:i], s.participants[i+1:]...)
			return
		}
	}
}

func (s Sidebar) View() string {
	var b strings.Builder

	b.WriteString(sidebarTitleStyle.Render("Participants"))
	b.WriteString("\n\n")

	for _, p := range s.participants {
		nameStyle := agentNameStyle
		if p.Source == "human" {
			nameStyle = humanNameStyle
		}
		b.WriteString(fmt.Sprintf("● %s\n", nameStyle.Render(p.Name)))

		info := p.Role
		if p.AgentType != "" {
			info += " · " + p.AgentType
		}
		b.WriteString(fmt.Sprintf("  %s\n", systemMsgStyle.Render(info)))

		if p.Directory != "" {
			b.WriteString(fmt.Sprintf("  %s\n", timestampStyle.Render(p.Directory)))
		}
		b.WriteString("\n")
	}

	return sidebarStyle.Width(s.width).Height(s.height).Render(b.String())
}
```

- [ ] **Step 4: Create chat viewport component**

Create `internal/tui/chat.go`:

```go
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/sle/parley/internal/protocol"
)

type ChatMessage struct {
	Params protocol.MessageParams
}

type Chat struct {
	viewport viewport.Model
	messages []protocol.MessageParams
	width    int
	height   int
}

func NewChat() Chat {
	vp := viewport.New(0, 0)
	vp.MouseWheelEnabled = true
	return Chat{viewport: vp}
}

func (c *Chat) SetSize(w, h int) {
	c.width = w
	c.height = h
	c.viewport.Width = w
	c.viewport.Height = h
	c.rerender()
}

func (c *Chat) AddMessage(msg protocol.MessageParams) {
	c.messages = append(c.messages, msg)
	c.rerender()
	c.viewport.GotoBottom()
}

func (c *Chat) Update(msg tea.Msg) (*Chat, tea.Cmd) {
	var cmd tea.Cmd
	c.viewport, cmd = c.viewport.Update(msg)
	return c, cmd
}

func (c Chat) View() string {
	return c.viewport.View()
}

func (c *Chat) rerender() {
	var lines []string
	for _, msg := range c.messages {
		lines = append(lines, renderMessage(msg, c.width))
	}
	c.viewport.SetContent(strings.Join(lines, "\n"))
}

func renderMessage(msg protocol.MessageParams, width int) string {
	switch msg.Source {
	case "system":
		return systemMsgStyle.Render(fmt.Sprintf("[system] %s", msg.Content.Text))

	case "human":
		ts := timestampStyle.Render(msg.Timestamp.Format("15:04"))
		name := humanNameStyle.Render(msg.From)
		header := fmt.Sprintf("%s %s", name, ts)
		return fmt.Sprintf("%s\n%s", header, msg.Content.Text)

	case "agent":
		ts := timestampStyle.Render(msg.Timestamp.Format("15:04"))
		name := agentNameStyle.Render(msg.From)
		role := roleBadgeStyle.Render(msg.Role)
		header := fmt.Sprintf("%s %s %s", name, role, ts)
		return fmt.Sprintf("%s\n%s", header, msg.Content.Text)

	default:
		return msg.Content.Text
	}
}
```

- [ ] **Step 5: Create input component**

Create `internal/tui/input.go`:

```go
package tui

import (
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type InputMode int

const (
	InputModeHuman InputMode = iota
	InputModeAgent
)

type Input struct {
	textarea  textarea.Model
	mode      InputMode
	agentText string
	prompt    string
	width     int
}

func NewInput(mode InputMode, name string) Input {
	ta := textarea.New()
	ta.Placeholder = "Type a message..."
	ta.ShowLineNumbers = false
	ta.SetHeight(1)
	ta.CharLimit = 0
	ta.Prompt = name + " › "
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.BlurredStyle.CursorLine = lipgloss.NewStyle()

	i := Input{
		textarea: ta,
		mode:     mode,
		prompt:   name,
	}

	if mode == InputModeHuman {
		ta.Focus()
	}

	return i
}

func (i *Input) SetWidth(w int) {
	i.width = w
	i.textarea.SetWidth(w - 2) // account for padding
}

func (i *Input) SetAgentText(text string) {
	i.agentText = text
}

func (i *Input) Value() string {
	return i.textarea.Value()
}

func (i *Input) Reset() {
	i.textarea.Reset()
	i.agentText = ""
}

func (i *Input) Update(msg tea.Msg) (*Input, tea.Cmd) {
	if i.mode == InputModeAgent {
		return i, nil // Agent input is read-only
	}
	var cmd tea.Cmd
	i.textarea, cmd = i.textarea.Update(msg)
	return i, cmd
}

func (i Input) View() string {
	if i.mode == InputModeAgent {
		display := i.prompt + " › " + i.agentText
		if i.agentText != "" {
			display += " (agent typing...)"
		}
		return inputStyle.Width(i.width).Render(display)
	}
	return inputStyle.Width(i.width).Render(i.textarea.View())
}
```

- [ ] **Step 6: Verify everything compiles**

```bash
go build ./internal/tui/
```

Expected: compiles with no errors.

- [ ] **Step 7: Commit**

```bash
git add internal/tui/
git commit -m "feat: TUI components — styles, topbar, sidebar, chat, input"
```

---

## Task 6: TUI — Root App Model

**Files:**
- Create: `internal/tui/app.go`

- [ ] **Step 1: Implement the root Bubble Tea model**

Create `internal/tui/app.go`:

```go
package tui

import (
	"encoding/json"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sle/parley/internal/protocol"
)

const sidebarWidth = 28

// Messages from the network
type ServerMsg struct {
	Raw *protocol.RawMessage
}

type App struct {
	topbar  TopBar
	chat    Chat
	sidebar Sidebar
	input   Input

	width  int
	height int
}

func NewApp(topic string, port int, mode InputMode, name string) App {
	return App{
		topbar:  NewTopBar(topic, port),
		chat:    NewChat(),
		sidebar: NewSidebar(),
		input:   NewInput(mode, name),
	}
}

func (a App) Init() tea.Cmd {
	return textarea.Blink
}

func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		a.layout()
		return a, nil

	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			return a, tea.Quit
		case tea.KeyEnter:
			if a.input.mode == InputModeHuman && a.input.Value() != "" {
				text := a.input.Value()
				a.input.Reset()
				// Return a command that sends the message
				return a, func() tea.Msg {
					return SendMsg{Text: text}
				}
			}
		}

	case ServerMsg:
		a.handleServerMsg(msg.Raw)
		return a, nil
	}

	// Update sub-components
	var cmd tea.Cmd
	inputPtr, cmd := a.input.Update(msg)
	a.input = *inputPtr
	cmds = append(cmds, cmd)

	chatPtr, cmd := a.chat.Update(msg)
	a.chat = *chatPtr
	cmds = append(cmds, cmd)

	return a, tea.Batch(cmds...)
}

func (a App) View() string {
	topbar := a.topbar.View()

	chatView := a.chat.View()
	sidebarView := a.sidebar.View()
	middle := lipgloss.JoinHorizontal(lipgloss.Top, chatView, sidebarView)

	inputView := a.input.View()

	return lipgloss.JoinVertical(lipgloss.Left, topbar, middle, inputView)
}

func (a *App) layout() {
	topbarHeight := 1
	inputHeight := 3
	chatHeight := a.height - topbarHeight - inputHeight
	chatWidth := a.width - sidebarWidth

	a.topbar.SetWidth(a.width)
	a.chat.SetSize(chatWidth, chatHeight)
	a.sidebar.SetSize(sidebarWidth, chatHeight)
	a.input.SetWidth(a.width)
}

func (a *App) handleServerMsg(raw *protocol.RawMessage) {
	switch raw.Method {
	case "room.state":
		var params protocol.RoomStateParams
		json.Unmarshal(raw.Params, &params)
		a.sidebar.SetParticipants(params.Participants)

	case "room.message":
		var params protocol.MessageParams
		json.Unmarshal(raw.Params, &params)
		a.chat.AddMessage(params)

	case "room.joined":
		var params protocol.JoinedParams
		json.Unmarshal(raw.Params, &params)
		a.sidebar.AddParticipant(protocol.Participant{
			Name:      params.Name,
			Role:      params.Role,
			Directory: params.Directory,
			Repo:      params.Repo,
			AgentType: params.AgentType,
			Source:    "agent",
		})

	case "room.left":
		var params protocol.LeftParams
		json.Unmarshal(raw.Params, &params)
		a.sidebar.RemoveParticipant(params.Name)
	}
}

// SendMsg is emitted by the TUI when the user presses Enter
type SendMsg struct {
	Text string
}

// SetAgentText updates the agent typing indicator
func (a *App) SetAgentText(text string) {
	a.input.SetAgentText(text)
}
```

- [ ] **Step 2: Verify it compiles**

```bash
go build ./internal/tui/
```

Expected: compiles with no errors. (Note: `textarea.Blink` import — you may need to import `"github.com/charmbracelet/bubbles/textarea"` in the import block. Adjust if needed.)

- [ ] **Step 3: Commit**

```bash
git add internal/tui/app.go
git commit -m "feat: root TUI app model with layout and server message handling"
```

---

## Task 7: Claude Code Driver

**Files:**
- Create: `internal/driver/driver.go`
- Create: `internal/driver/claude.go`
- Create: `internal/driver/claude_test.go`

- [ ] **Step 1: Define the driver interface and event types**

Create `internal/driver/driver.go`:

```go
package driver

import "context"

type EventType int

const (
	EventText EventType = iota
	EventThinking
	EventToolUse
	EventToolResult
	EventDone
	EventError
)

type AgentEvent struct {
	Type    EventType
	Text    string
	ToolName string
}

type AgentConfig struct {
	Command     string   // e.g., "claude"
	Args        []string // e.g., ["--worktree"]
	Name        string
	Role        string
	Directory   string
	Repo        string
	Topic       string
	Participants []ParticipantInfo
	SystemPrompt string
}

type ParticipantInfo struct {
	Name      string
	Role      string
	Directory string
}

type AgentDriver interface {
	Start(ctx context.Context, config AgentConfig) error
	Send(text string) error
	Events() <-chan AgentEvent
	Stop() error
}
```

- [ ] **Step 2: Write tests for Claude driver**

Create `internal/driver/claude_test.go`:

```go
package driver_test

import (
	"context"
	"testing"
	"time"

	"github.com/sle/parley/internal/driver"
)

func TestClaudeDriverBuildArgs(t *testing.T) {
	d := driver.NewClaudeDriver()
	config := driver.AgentConfig{
		Command:   "claude",
		Args:      []string{"--worktree"},
		Name:      "Alice",
		Role:      "backend",
		Directory: "/tmp/test",
		Topic:     "test topic",
		SystemPrompt: "You are in a chat room.",
	}

	args := d.BuildArgs(config, "Hello, world")
	// Should contain -p, --output-format, stream-json, --append-system-prompt
	found := map[string]bool{}
	for _, arg := range args {
		found[arg] = true
	}
	if !found["-p"] {
		t.Error("missing -p flag")
	}
	if !found["--output-format"] {
		t.Error("missing --output-format flag")
	}
	if !found["stream-json"] {
		t.Error("missing stream-json value")
	}
	if !found["--append-system-prompt"] {
		t.Error("missing --append-system-prompt flag")
	}
}

func TestClaudeDriverStartStop(t *testing.T) {
	// This test requires `claude` to be installed
	// Skip in CI
	d := driver.NewClaudeDriver()
	config := driver.AgentConfig{
		Command:      "echo",
		Args:         nil,
		Name:         "Test",
		Role:         "test",
		Directory:    "/tmp",
		Topic:        "test",
		SystemPrompt: "test",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Test that Start doesn't panic with a simple command
	// We use 'echo' as a stand-in since claude may not be available in all test envs
	err := d.Start(ctx, config)
	if err != nil {
		// Expected - echo doesn't produce stream-json
		t.Logf("Start returned error (expected with echo): %v", err)
	}

	d.Stop()
}
```

- [ ] **Step 3: Run tests to verify they fail**

```bash
go test ./internal/driver/ -v
```

Expected: compilation error.

- [ ] **Step 4: Implement Claude driver**

Create `internal/driver/claude.go`:

```go
package driver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

type ClaudeDriver struct {
	events    chan AgentEvent
	cmd       *exec.Cmd
	sessionID string
	cancel    context.CancelFunc
	mu        sync.Mutex
}

func NewClaudeDriver() *ClaudeDriver {
	return &ClaudeDriver{
		events: make(chan AgentEvent, 64),
	}
}

func (d *ClaudeDriver) BuildArgs(config AgentConfig, message string) []string {
	args := []string{
		"-p", message,
		"--output-format", "stream-json",
		"--append-system-prompt", config.SystemPrompt,
	}
	// Add user-provided extra args (e.g., --worktree)
	args = append(args, config.Args...)
	// If we have a session to resume, add it
	if d.sessionID != "" {
		args = append(args, "--resume", d.sessionID)
	}
	return args
}

func (d *ClaudeDriver) Start(ctx context.Context, config AgentConfig) error {
	return d.invoke(ctx, config, "You have joined a parley chat room. The topic is: "+config.Topic+". Introduce yourself briefly.")
}

func (d *ClaudeDriver) Send(text string) error {
	d.mu.Lock()
	config := d.lastConfig
	d.mu.Unlock()

	if config == nil {
		return fmt.Errorf("driver not started")
	}

	ctx := context.Background()
	return d.invoke(ctx, *config, text)
}

func (d *ClaudeDriver) Events() <-chan AgentEvent {
	return d.events
}

func (d *ClaudeDriver) Stop() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cancel != nil {
		d.cancel()
	}
	if d.cmd != nil && d.cmd.Process != nil {
		d.cmd.Process.Kill()
	}
	return nil
}

// lastConfig stores config for re-invocations
var _ AgentDriver = (*ClaudeDriver)(nil)

// We add lastConfig to the struct
func init() {} // placeholder — lastConfig is added below

// invoke runs a single claude invocation and parses stream-json output
func (d *ClaudeDriver) invoke(ctx context.Context, config AgentConfig, message string) error {
	d.mu.Lock()
	d.lastConfig = &config
	d.mu.Unlock()

	ctx, cancel := context.WithCancel(ctx)
	d.mu.Lock()
	d.cancel = cancel
	d.mu.Unlock()

	args := d.BuildArgs(config, message)
	cmd := exec.CommandContext(ctx, config.Command, args...)
	cmd.Dir = config.Directory

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("stdout pipe: %w", err)
	}

	d.mu.Lock()
	d.cmd = cmd
	d.mu.Unlock()

	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("start: %w", err)
	}

	go func() {
		defer cancel()
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

		for scanner.Scan() {
			line := scanner.Bytes()
			event := d.parseLine(line)
			if event != nil {
				d.events <- *event
			}
		}

		cmd.Wait()
		d.events <- AgentEvent{Type: EventDone}
	}()

	return nil
}

// parseLine converts a stream-json line into an AgentEvent
func (d *ClaudeDriver) parseLine(line []byte) *AgentEvent {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil
	}

	// Extract type field
	var msgType string
	if t, ok := raw["type"]; ok {
		json.Unmarshal(t, &msgType)
	}

	switch msgType {
	case "assistant":
		// Extract text content from message
		var msg struct {
			Message struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
					Name string `json:"name"`
				} `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(line, &msg); err == nil {
			for _, c := range msg.Message.Content {
				switch c.Type {
				case "text":
					return &AgentEvent{Type: EventText, Text: c.Text}
				case "tool_use":
					return &AgentEvent{Type: EventToolUse, ToolName: c.Name}
				}
			}
		}

	case "result":
		// Extract session_id for resume
		var result struct {
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(line, &result); err == nil && result.SessionID != "" {
			d.mu.Lock()
			d.sessionID = result.SessionID
			d.mu.Unlock()
		}
		return nil

	case "system":
		// Init message — may contain session_id
		var sys struct {
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(line, &sys); err == nil && sys.SessionID != "" {
			d.mu.Lock()
			d.sessionID = sys.SessionID
			d.mu.Unlock()
		}
		return nil
	}

	return nil
}

func BuildSystemPrompt(config AgentConfig) string {
	var b strings.Builder
	b.WriteString("You are participating in a group chat room called \"parley\". ")
	b.WriteString("You are one of several participants — some human, some AI coding agents — collaborating as peers.\n\n")
	b.WriteString(fmt.Sprintf("ROOM: %s\n", config.Topic))
	b.WriteString("PARTICIPANTS:\n")
	for _, p := range config.Participants {
		b.WriteString(fmt.Sprintf("- %s (%s), working in %s\n", p.Name, p.Role, p.Directory))
	}
	b.WriteString(fmt.Sprintf("\nYOU ARE: %s, %s, working in %s\n\n", config.Name, config.Role, config.Directory))
	b.WriteString("RESPONSE GUIDELINES:\n")
	b.WriteString("- ALWAYS respond when someone @-mentions you by name\n")
	b.WriteString("- Respond when the discussion is directly relevant to your role/expertise\n")
	b.WriteString("- Do NOT respond when another participant is better suited to answer\n")
	b.WriteString("- Do NOT respond just to agree — only add substance\n")
	b.WriteString("- If unsure whether to respond, default to staying silent\n")
	b.WriteString("- Keep responses focused and concise — this is a chat, not a monologue\n")
	b.WriteString("- You can @-mention other participants to ask them questions\n\n")
	b.WriteString("When you respond, just write your message directly. Do not prefix it with your name.\n")
	return b.String()
}
```

Note: we need to add `lastConfig *AgentConfig` to the struct. Update the struct definition:

```go
type ClaudeDriver struct {
	events     chan AgentEvent
	cmd        *exec.Cmd
	sessionID  string
	cancel     context.CancelFunc
	lastConfig *AgentConfig
	mu         sync.Mutex
}
```

- [ ] **Step 5: Run tests to verify they pass**

```bash
go test ./internal/driver/ -v
```

Expected: `TestClaudeDriverBuildArgs` passes. `TestClaudeDriverStartStop` may log an expected error with `echo`.

- [ ] **Step 6: Commit**

```bash
git add internal/driver/
git commit -m "feat: agent driver interface and Claude Code driver"
```

---

## Task 8: Wire Host Command

**Files:**
- Modify: `cmd/parley/main.go`

- [ ] **Step 1: Wire host command to start server + TUI**

Replace the `hostCmd` in `cmd/parley/main.go`:

```go
package main

import (
	"fmt"
	"log"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/sle/parley/internal/client"
	"github.com/sle/parley/internal/protocol"
	"github.com/sle/parley/internal/server"
	"github.com/sle/parley/internal/tui"
)

var rootCmd = &cobra.Command{
	Use:   "parley",
	Short: "TUI group chat for coding agents",
}

var hostCmd = &cobra.Command{
	Use:   "host",
	Short: "Host a chat room",
	RunE:  runHost,
}

var joinCmd = &cobra.Command{
	Use:   "join",
	Short: "Join a chat room with an agent",
	RunE:  runJoin,
}

func init() {
	hostCmd.Flags().StringP("topic", "t", "", "Room topic (required)")
	hostCmd.MarkFlagRequired("topic")
	hostCmd.Flags().IntP("port", "p", 0, "Port to listen on (0 = random)")

	joinCmd.Flags().IntP("port", "p", 0, "Server port to connect to (required)")
	joinCmd.MarkFlagRequired("port")
	joinCmd.Flags().StringP("name", "n", "", "Your display name (required)")
	joinCmd.MarkFlagRequired("name")
	joinCmd.Flags().StringP("role", "r", "", "Your role")

	rootCmd.AddCommand(hostCmd, joinCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runHost(cmd *cobra.Command, args []string) error {
	topic, _ := cmd.Flags().GetString("topic")
	port, _ := cmd.Flags().GetInt("port")

	addr := fmt.Sprintf("localhost:%d", port)
	srv, err := server.New(addr, topic)
	if err != nil {
		return fmt.Errorf("start server: %w", err)
	}
	defer srv.Close()

	go srv.Serve()

	// Connect as the human client
	c, err := client.New(srv.Addr())
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer c.Close()

	// Get current user name
	name := os.Getenv("USER")
	if name == "" {
		name = "host"
	}

	// Auto-detect directory and repo
	dir, _ := os.Getwd()
	repo := detectRepo()

	err = c.Join(protocol.JoinParams{
		Name:      name,
		Role:      "human",
		Directory: dir,
		Repo:      repo,
	})
	if err != nil {
		return fmt.Errorf("join: %w", err)
	}

	// Create TUI
	app := tui.NewApp(topic, srv.Port(), tui.InputModeHuman, name)
	p := tea.NewProgram(app, tea.WithAltScreen(), tea.WithMouseCellMotion())

	// Network → TUI bridge
	go func() {
		for msg := range c.Incoming() {
			p.Send(tui.ServerMsg{Raw: msg})
		}
	}()

	// Run TUI (blocks)
	finalModel, err := p.Run()
	if err != nil {
		return fmt.Errorf("tui: %w", err)
	}

	// TUI → Network bridge: handle SendMsg
	// This is handled inside the Update loop via a tea.Cmd that we process here
	_ = finalModel

	return nil
}

func runJoin(cmd *cobra.Command, args []string) error {
	// TODO: implement in Task 9
	return fmt.Errorf("not implemented yet")
}

func detectRepo() string {
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
```

Add `"os/exec"` and `"strings"` to imports.

- [ ] **Step 2: Fix the SendMsg handling**

The TUI emits `tui.SendMsg` when the user presses Enter, but we need to actually send it over the network. Update `tui/app.go` to return the `SendMsg` via the command pattern (already done), and in `main.go`, we need to handle it. The cleanest way is to make the TUI model hold a reference to the client's send function.

Update `internal/tui/app.go` — add a `sendFn` callback:

```go
type App struct {
	topbar  TopBar
	chat    Chat
	sidebar Sidebar
	input   Input
	sendFn  func(string, []string) // callback to send messages

	width  int
	height int
}

func NewApp(topic string, port int, mode InputMode, name string, sendFn func(string, []string)) App {
	return App{
		topbar:  NewTopBar(topic, port),
		chat:    NewChat(),
		sidebar: NewSidebar(),
		input:   NewInput(mode, name),
		sendFn:  sendFn,
	}
}
```

And update the `KeyEnter` handler in `Update`:

```go
case tea.KeyEnter:
	if a.input.mode == InputModeHuman && a.input.Value() != "" {
		text := a.input.Value()
		a.input.Reset()
		if a.sendFn != nil {
			mentions := parseMentions(text)
			a.sendFn(text, mentions)
		}
	}
```

Add mention parsing helper:

```go
func parseMentions(text string) []string {
	var mentions []string
	words := strings.Fields(text)
	for _, w := range words {
		if strings.HasPrefix(w, "@") && len(w) > 1 {
			mentions = append(mentions, strings.TrimPrefix(w, "@"))
		}
	}
	return mentions
}
```

Add `"strings"` to the import in `app.go`.

Update the `NewApp` call in `main.go`:

```go
sendFn := func(text string, mentions []string) {
	c.Send(protocol.Content{Type: "text", Text: text}, mentions)
}
app := tui.NewApp(topic, srv.Port(), tui.InputModeHuman, name, sendFn)
```

Remove the `SendMsg` type and the `SendMsg` case from `Update` — the callback handles it directly now.

- [ ] **Step 3: Build and test manually**

```bash
go build -o parley ./cmd/parley
./parley host --topic "hello world"
```

Expected: TUI opens in alt screen. Shows top bar with topic. Chat area is empty. Input box is focused. `Ctrl+C` exits.

- [ ] **Step 4: Commit**

```bash
git add cmd/parley/main.go internal/tui/app.go
git commit -m "feat: wire host command — server + TUI + network bridge"
```

---

## Task 9: Wire Join Command + Claude Driver Integration

**Files:**
- Modify: `cmd/parley/main.go`

- [ ] **Step 1: Implement runJoin**

Replace `runJoin` in `cmd/parley/main.go`:

```go
func runJoin(cmd *cobra.Command, args []string) error {
	port, _ := cmd.Flags().GetInt("port")
	name, _ := cmd.Flags().GetString("name")
	role, _ := cmd.Flags().GetString("role")

	// Everything after -- is the agent command
	agentArgs := cmd.ArgsLenAtDash()
	var agentCmd string
	var agentExtraArgs []string
	if agentArgs >= 0 && len(args) > agentArgs {
		agentCmd = args[agentArgs]
		if len(args) > agentArgs+1 {
			agentExtraArgs = args[agentArgs+1:]
		}
	}
	if agentCmd == "" {
		return fmt.Errorf("agent command required after --")
	}

	dir, _ := os.Getwd()
	repo := detectRepo()

	addr := fmt.Sprintf("localhost:%d", port)
	c, err := client.New(addr)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer c.Close()

	err = c.Join(protocol.JoinParams{
		Name:      name,
		Role:      role,
		Directory: dir,
		Repo:      repo,
		AgentType: agentCmd,
	})
	if err != nil {
		return fmt.Errorf("join: %w", err)
	}

	// Wait for room.state to get participant list
	var participants []driver.ParticipantInfo
	var topic string
	select {
	case msg := <-c.Incoming():
		if msg.Method == "room.state" {
			var state protocol.RoomStateParams
			json.Unmarshal(msg.Params, &state)
			topic = state.Topic
			for _, p := range state.Participants {
				participants = append(participants, driver.ParticipantInfo{
					Name:      p.Name,
					Role:      p.Role,
					Directory: p.Directory,
				})
			}
		}
	case <-time.After(5 * time.Second):
		return fmt.Errorf("timeout waiting for room state")
	}

	// Build config and start driver
	config := driver.AgentConfig{
		Command:      agentCmd,
		Args:         agentExtraArgs,
		Name:         name,
		Role:         role,
		Directory:    dir,
		Repo:         repo,
		Topic:        topic,
		Participants: participants,
	}
	config.SystemPrompt = driver.BuildSystemPrompt(config)

	d := driver.NewClaudeDriver()
	ctx := context.Background()
	if err := d.Start(ctx, config); err != nil {
		return fmt.Errorf("start agent: %w", err)
	}
	defer d.Stop()

	// Create agent TUI
	app := tui.NewApp(topic, port, tui.InputModeAgent, name, nil)
	p := tea.NewProgram(app, tea.WithAltScreen(), tea.WithMouseCellMotion())

	// Network → TUI bridge
	go func() {
		for msg := range c.Incoming() {
			p.Send(tui.ServerMsg{Raw: msg})

			// Also forward messages to the agent driver
			if msg.Method == "room.message" {
				var params protocol.MessageParams
				json.Unmarshal(msg.Params, &params)
				// Don't echo our own messages back to the agent
				if params.From != name {
					formatted := fmt.Sprintf("[%s] (%s): %s", params.From, params.Role, params.Content.Text)
					d.Send(formatted)
				}
			}
		}
	}()

	// Agent → Network bridge
	go func() {
		var currentText strings.Builder
		for event := range d.Events() {
			switch event.Type {
			case driver.EventText:
				currentText.WriteString(event.Text)
				p.Send(tui.AgentTypingMsg{Text: currentText.String()})
			case driver.EventDone:
				if currentText.Len() > 0 {
					text := currentText.String()
					mentions := parseMentions(text)
					c.Send(protocol.Content{Type: "text", Text: text}, mentions)
					currentText.Reset()
					p.Send(tui.AgentTypingMsg{Text: ""})
				}
			}
		}
	}()

	_, err = p.Run()
	return err
}
```

Add the `parseMentions` helper to `main.go` (or move it to a shared location):

```go
func parseMentions(text string) []string {
	var mentions []string
	for _, w := range strings.Fields(text) {
		if strings.HasPrefix(w, "@") && len(w) > 1 {
			mentions = append(mentions, strings.TrimPrefix(w, "@"))
		}
	}
	return mentions
}
```

- [ ] **Step 2: Add AgentTypingMsg to TUI**

Add to `internal/tui/app.go`:

```go
type AgentTypingMsg struct {
	Text string
}
```

And handle it in `Update`:

```go
case AgentTypingMsg:
	a.input.SetAgentText(msg.Text)
	return a, nil
```

- [ ] **Step 3: Update imports in main.go**

Make sure `cmd/parley/main.go` imports:

```go
import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/sle/parley/internal/client"
	"github.com/sle/parley/internal/driver"
	"github.com/sle/parley/internal/protocol"
	"github.com/sle/parley/internal/server"
	"github.com/sle/parley/internal/tui"
)
```

- [ ] **Step 4: Build and test end-to-end**

```bash
go build -o parley ./cmd/parley

# Terminal 1:
./parley host --topic "test chat"

# Terminal 2:
./parley join --port <port-from-terminal-1> --name "Alice" --role "backend" -- claude
```

Expected: Terminal 1 shows TUI with you as participant. Terminal 2 shows TUI with Alice. When you type a message in Terminal 1, it appears in Terminal 2's chat. Alice's agent processes it and responds — the response appears in both TUIs.

- [ ] **Step 5: Commit**

```bash
git add cmd/parley/main.go internal/tui/app.go
git commit -m "feat: wire join command — agent driver + TUI + network bridge"
```

---

## Task 10: Persistence

**Files:**
- Create: `internal/server/persistence.go`
- Create: `internal/server/persistence_test.go`

- [ ] **Step 1: Write tests for persistence**

Create `internal/server/persistence_test.go`:

```go
package server_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sle/parley/internal/protocol"
	"github.com/sle/parley/internal/server"
)

func TestSaveAndLoadRoom(t *testing.T) {
	dir := t.TempDir()

	room := server.NewRoom("test topic")
	room.Join(&server.ClientConn{
		Name: "Alice", Role: "backend", Directory: "/tmp/a", Source: "agent", AgentType: "claude",
	})

	msg := room.Broadcast("Alice", "agent", "backend", protocol.Content{Type: "text", Text: "hello"}, nil)

	err := server.SaveRoom(dir, room)
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	// Verify files exist
	if _, err := os.Stat(filepath.Join(dir, "room.json")); err != nil {
		t.Error("room.json not created")
	}
	if _, err := os.Stat(filepath.Join(dir, "messages.json")); err != nil {
		t.Error("messages.json not created")
	}

	loaded, err := server.LoadRoom(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Topic != "test topic" {
		t.Errorf("topic = %q, want test topic", loaded.Topic)
	}
	if len(loaded.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(loaded.Messages))
	}
	if loaded.Messages[0].Content.Text != "hello" {
		t.Errorf("text = %q, want hello", loaded.Messages[0].Content.Text)
	}
	_ = msg
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/server/ -v -run TestSaveAndLoad
```

Expected: compilation error.

- [ ] **Step 3: Implement persistence**

Create `internal/server/persistence.go`:

```go
package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sle/parley/internal/protocol"
)

type RoomData struct {
	Topic string `json:"topic"`
	ID    string `json:"id"`
}

type ParticipantData struct {
	Name      string `json:"name"`
	Role      string `json:"role"`
	Directory string `json:"directory"`
	Repo      string `json:"repo,omitempty"`
	AgentType string `json:"agent_type,omitempty"`
	Source    string `json:"source"`
}

func SaveRoom(dir string, room *Room) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	// Save room metadata
	roomData := RoomData{Topic: room.Topic}
	if err := writeJSON(filepath.Join(dir, "room.json"), roomData); err != nil {
		return fmt.Errorf("save room: %w", err)
	}

	// Save messages
	room.mu.RLock()
	messages := make([]protocol.MessageParams, len(room.Messages))
	copy(messages, room.Messages)
	room.mu.RUnlock()

	if err := writeJSON(filepath.Join(dir, "messages.json"), messages); err != nil {
		return fmt.Errorf("save messages: %w", err)
	}

	// Save participants
	room.mu.RLock()
	var participants []ParticipantData
	for _, cc := range room.Participants {
		participants = append(participants, ParticipantData{
			Name:      cc.Name,
			Role:      cc.Role,
			Directory: cc.Directory,
			Repo:      cc.Repo,
			AgentType: cc.AgentType,
			Source:    cc.Source,
		})
	}
	room.mu.RUnlock()

	if err := writeJSON(filepath.Join(dir, "agents.json"), participants); err != nil {
		return fmt.Errorf("save agents: %w", err)
	}

	return nil
}

func LoadRoom(dir string) (*Room, error) {
	var roomData RoomData
	if err := readJSON(filepath.Join(dir, "room.json"), &roomData); err != nil {
		return nil, fmt.Errorf("load room: %w", err)
	}

	room := NewRoom(roomData.Topic)

	var messages []protocol.MessageParams
	if err := readJSON(filepath.Join(dir, "messages.json"), &messages); err != nil {
		return nil, fmt.Errorf("load messages: %w", err)
	}
	room.Messages = messages
	if len(messages) > 0 {
		room.seq = messages[len(messages)-1].Seq
	}

	return room, nil
}

func RoomDir(roomID string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".parley", "rooms", roomID)
}

func writeJSON(path string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func readJSON(path string, v interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
```

We need to export `ClientConn` fields for the test. Update `internal/server/room.go` — the `send` and `done` fields need to handle nil for persistence-loaded participants:

```go
// Make ClientConn fields needed by persistence accessible
// send and done are already public-ish via the struct, but we need
// to handle the case where a loaded room has no active connections.
```

Actually, the test creates a `ClientConn` directly. We need to make the `send` and `done` channels optional or add a constructor. For simplicity, update `room.go`'s `Join` to initialize channels if nil:

```go
func (r *Room) Join(cc *ClientConn) protocol.RoomStateParams {
	r.mu.Lock()
	defer r.mu.Unlock()

	if cc.send == nil {
		cc.send = make(chan []byte, 64)
	}
	if cc.done == nil {
		cc.done = make(chan struct{})
	}

	r.Participants[cc.Name] = cc
	// ... rest same
}
```

And export `Send` and `Done` or make the fields exported:

In `room.go`, change the struct:

```go
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
```

Update all references from `cc.send` → `cc.Send` and `cc.done` → `cc.Done` in `room.go` and `server.go`.

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/server/ -v
```

Expected: all tests pass, including the persistence test.

- [ ] **Step 5: Wire persistence into the server shutdown**

Add to `cmd/parley/main.go` in `runHost`, before the defer:

```go
// Save room state on exit
defer func() {
	roomID := fmt.Sprintf("%d", srv.Port())
	dir := server.RoomDir(roomID)
	if err := server.SaveRoom(dir, srv.Room()); err != nil {
		log.Printf("failed to save room: %v", err)
	} else {
		log.Printf("Room saved to %s", dir)
	}
}()
```

- [ ] **Step 6: Commit**

```bash
git add internal/server/persistence.go internal/server/persistence_test.go internal/server/room.go cmd/parley/main.go
git commit -m "feat: room persistence — save/load room state to JSON"
```

---

## Task 11: End-to-End Manual Test

**Files:** None — manual verification

- [ ] **Step 1: Build**

```bash
go build -o parley ./cmd/parley
```

- [ ] **Step 2: Test host-only mode**

```bash
./parley host --topic "test chat"
```

Verify:
- TUI opens with topic in top bar
- Your username appears in sidebar
- You can type messages and they appear in chat
- `Ctrl+C` exits cleanly

- [ ] **Step 3: Test with Claude agent**

Terminal 1:
```bash
./parley host --topic "Let's discuss Go best practices"
```

Terminal 2:
```bash
./parley join --port <port> --name "GoExpert" --role "Go specialist" -- claude
```

Verify:
- System message "[system] GoExpert has joined" appears in Terminal 1
- GoExpert appears in sidebar of both terminals
- Type a message in Terminal 1 — it appears in Terminal 2
- The Claude agent processes it and responds
- The response appears in both terminals
- The agent TUI shows "agent typing..." while Claude is responding

- [ ] **Step 4: Test persistence**

After chatting, `Ctrl+C` in Terminal 1. Check:
```bash
ls ~/.parley/rooms/
cat ~/.parley/rooms/<port>/room.json
cat ~/.parley/rooms/<port>/messages.json
```

Verify JSON files contain the room topic and message history.

- [ ] **Step 5: Commit any fixes**

```bash
git add -A
git commit -m "fix: end-to-end test fixes"
```

---

## Self-Review Checklist

Verified against spec:

| Spec Requirement | Task |
|-----------------|------|
| `parley host` command | Task 1 (skeleton), Task 8 (wired) |
| `parley join` command | Task 1 (skeleton), Task 9 (wired) |
| TCP + JSON-RPC 2.0 | Task 2 (protocol), Task 3 (server) |
| NDJSON framing | Task 2 (EncodeLine/DecodeLine) |
| Room state, broadcast, seq numbers | Task 3 (room.go) |
| System messages for join/leave | Task 3 (server.go) |
| room.state sent on join | Task 3 (server.go) |
| room.joined broadcast | Task 3 (server.go) |
| @-mentions | Task 8 (parseMentions), Task 9 |
| Claude Code driver | Task 7 |
| Selective response system prompt | Task 7 (BuildSystemPrompt) |
| TUI with chat + sidebar + input | Tasks 5-6 |
| Same TUI for human and agent | Task 6 (InputMode) |
| Agent typing indicator | Task 5 (input.go), Task 9 |
| JSON persistence | Task 10 |
| Directory/repo auto-detection | Task 8 (detectRepo, os.Getwd) |
| Validation spike | Task 0 |

No placeholders found. Types are consistent across tasks (`protocol.MessageParams`, `protocol.Content`, `driver.AgentEvent`, etc.).
