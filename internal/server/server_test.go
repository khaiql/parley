package server_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/khaiql/parley/internal/protocol"
	"github.com/khaiql/parley/internal/server"
)

const bufSize = 1024 * 1024 // 1 MB

func newTestServer(t *testing.T) *server.Server {
	t.Helper()
	s, err := server.New("127.0.0.1:0", "test-topic")
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	go s.Serve()
	t.Cleanup(func() { s.Close() })
	return s
}

func dialServer(t *testing.T, addr string) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func sendLine(t *testing.T, conn net.Conn, v interface{}) {
	t.Helper()
	data, err := protocol.EncodeLine(v)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if _, err := conn.Write(data); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func readLine(t *testing.T, scanner *bufio.Scanner) []byte {
	t.Helper()
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			t.Fatalf("scanner error: %v", err)
		}
		t.Fatal("connection closed before response")
	}
	return scanner.Bytes()
}

func newScanner(conn net.Conn) *bufio.Scanner {
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, bufSize), bufSize)
	return sc
}

// TestServerAcceptsConnection verifies the server accepts a TCP connection.
func TestServerAcceptsConnection(t *testing.T) {
	s := newTestServer(t)
	conn, err := net.DialTimeout("tcp", s.Addr(), 2*time.Second)
	if err != nil {
		t.Fatalf("expected connection to succeed, got: %v", err)
	}
	conn.Close()
}

// TestJoinAndReceiveState sends room.join and verifies a room.state response
// with the correct topic is returned.
func TestJoinAndReceiveState(t *testing.T) {
	s := newTestServer(t)
	conn := dialServer(t, s.Addr())
	sc := newScanner(conn)

	join := protocol.NewNotification(protocol.MethodJoin, protocol.JoinParams{
		Name: "alice",
		Role: "user",
	})
	sendLine(t, conn, join)

	line := readLine(t, sc)

	var raw protocol.RawMessage
	if err := json.Unmarshal(line, &raw); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if raw.Method != protocol.MethodState {
		t.Fatalf("expected method room.state, got %q", raw.Method)
	}

	var state protocol.RoomStateParams
	if err := json.Unmarshal(raw.Params, &state); err != nil {
		t.Fatalf("unmarshal state params: %v", err)
	}
	if state.Topic != "test-topic" {
		t.Errorf("expected topic %q, got %q", "test-topic", state.Topic)
	}
}

// TestBroadcastMessage verifies that when one client sends a message, the
// other client receives it as a room.message notification.
func TestBroadcastMessage(t *testing.T) {
	s := newTestServer(t)

	// Connect alice
	connAlice := dialServer(t, s.Addr())
	scAlice := newScanner(connAlice)

	sendLine(t, connAlice, protocol.NewNotification(protocol.MethodJoin, protocol.JoinParams{
		Name: "alice",
		Role: "user",
	}))
	readLine(t, scAlice) // consume room.state

	// Connect bob
	connBob := dialServer(t, s.Addr())
	scBob := newScanner(connBob)

	sendLine(t, connBob, protocol.NewNotification(protocol.MethodJoin, protocol.JoinParams{
		Name: "bob",
		Role: "user",
	}))
	readLine(t, scBob) // consume room.state

	// Bob sends a message
	sendLine(t, connBob, protocol.NewNotification(protocol.MethodSend, protocol.SendParams{
		Content: []protocol.Content{{Type: "text", Text: "hello alice"}},
	}))

	// Alice will receive some combination of: room.joined (bob), system "bob joined",
	// and then the room.message from bob. Read until we find room.message from bob.
	deadline := time.Now().Add(2 * time.Second)
	var foundMsg *protocol.MessageParams
	for time.Now().Before(deadline) {
		connAlice.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		if !scAlice.Scan() {
			break
		}
		connAlice.SetReadDeadline(time.Time{})

		var raw protocol.RawMessage
		if err := json.Unmarshal(scAlice.Bytes(), &raw); err != nil {
			continue
		}
		if raw.Method != protocol.MethodMessage {
			continue
		}
		var msg protocol.MessageParams
		if err := json.Unmarshal(raw.Params, &msg); err != nil {
			continue
		}
		if msg.From == "bob" {
			foundMsg = &msg
			break
		}
	}
	connAlice.SetReadDeadline(time.Time{})

	if foundMsg == nil {
		t.Fatal("alice never received room.message from bob")
	}
	if len(foundMsg.Content) == 0 || foundMsg.Content[0].Text != "hello alice" {
		t.Errorf("unexpected content: %+v", foundMsg.Content)
	}
}

// TestDuplicateNameRejected verifies that when B tries to join with the same
// name as A, B receives an error response and the connection is closed, while A
// remains unaffected in the room.
func TestDuplicateNameRejected(t *testing.T) {
	s := newTestServer(t)

	// Connect Alice.
	connAlice := dialServer(t, s.Addr())
	scAlice := newScanner(connAlice)

	sendLine(t, connAlice, protocol.NewNotification(protocol.MethodJoin, protocol.JoinParams{
		Name: "Alice",
		Role: "user",
	}))
	readLine(t, scAlice) // consume room.state

	// Connect a second client that also tries to join as "Alice".
	connAlice2 := dialServer(t, s.Addr())
	scAlice2 := newScanner(connAlice2)

	sendLine(t, connAlice2, protocol.NewNotification(protocol.MethodJoin, protocol.JoinParams{
		Name: "Alice",
		Role: "user",
	}))

	// The second client should receive an error response and the connection
	// should be closed by the server shortly after.
	connAlice2.SetReadDeadline(time.Now().Add(2 * time.Second))
	line := readLine(t, scAlice2)
	connAlice2.SetReadDeadline(time.Time{})

	var raw protocol.RawMessage
	if err := json.Unmarshal(line, &raw); err != nil {
		t.Fatalf("unmarshal response from duplicate join: %v", err)
	}
	if raw.Error == nil {
		t.Fatalf("expected error response for duplicate name, got method=%q", raw.Method)
	}
	if !strings.Contains(raw.Error.Message, "name already taken") {
		t.Errorf("unexpected error message: %q", raw.Error.Message)
	}

	// Wait for server to close the second connection.
	connAlice2.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	if scAlice2.Scan() {
		t.Error("expected connection to be closed after duplicate-name rejection")
	}
	connAlice2.SetReadDeadline(time.Time{})

	// Original Alice must still be in the room.
	time.Sleep(100 * time.Millisecond)
	parts := s.Room().GetParticipants()
	if len(parts) != 1 {
		t.Fatalf("expected 1 participant remaining, got %d", len(parts))
	}
	if parts[0].Name != "Alice" {
		t.Errorf("expected participant 'Alice', got %q", parts[0].Name)
	}
}

// TestServerBroadcastsRoomStatus verifies that when one client sends room.status,
// the other client receives a room.status notification.
func TestServerBroadcastsRoomStatus(t *testing.T) {
	s := newTestServer(t)

	// Connect alice
	connAlice := dialServer(t, s.Addr())
	scAlice := newScanner(connAlice)

	sendLine(t, connAlice, protocol.NewNotification(protocol.MethodJoin, protocol.JoinParams{
		Name: "alice",
		Role: "user",
	}))
	readLine(t, scAlice) // consume room.state

	// Connect bot1
	connBot := dialServer(t, s.Addr())
	scBot := newScanner(connBot)

	sendLine(t, connBot, protocol.NewNotification(protocol.MethodJoin, protocol.JoinParams{
		Name:      "bot1",
		Role:      "agent",
		AgentType: "claude",
	}))
	readLine(t, scBot) // consume room.state for bot1

	// Give the server time to deliver join notifications to alice.
	time.Sleep(50 * time.Millisecond)

	// Bot sends room.status
	sendLine(t, connBot, protocol.NewNotification(protocol.MethodStatus, protocol.StatusParams{
		Name:   "bot1",
		Status: "thinking…",
	}))

	// Alice should eventually receive a room.status notification.
	// She may first receive room.joined and system messages from bot joining.
	deadline := time.Now().Add(2 * time.Second)
	var foundStatus *protocol.StatusParams
	for time.Now().Before(deadline) {
		connAlice.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		if !scAlice.Scan() {
			break
		}
		connAlice.SetReadDeadline(time.Time{})

		var raw protocol.RawMessage
		if err := json.Unmarshal(scAlice.Bytes(), &raw); err != nil {
			continue
		}
		if raw.Method != protocol.MethodStatus {
			continue
		}
		var sp protocol.StatusParams
		if err := json.Unmarshal(raw.Params, &sp); err != nil {
			continue
		}
		foundStatus = &sp
		break
	}
	connAlice.SetReadDeadline(time.Time{})

	if foundStatus == nil {
		t.Fatal("alice never received room.status notification from bot1")
	}
	if foundStatus.Name != "bot1" {
		t.Errorf("expected status Name %q, got %q", "bot1", foundStatus.Name)
	}
	if foundStatus.Status != "thinking…" {
		t.Errorf("expected status %q, got %q", "thinking…", foundStatus.Status)
	}
}

// TestJoinReceivesMessageHistory verifies that when a client joins after
// messages have been sent, the room.state includes those messages.
func TestJoinReceivesMessageHistory(t *testing.T) {
	s := newTestServer(t)

	// Connect alice and send 5 messages.
	connAlice := dialServer(t, s.Addr())
	scAlice := newScanner(connAlice)

	sendLine(t, connAlice, protocol.NewNotification(protocol.MethodJoin, protocol.JoinParams{
		Name: "alice",
		Role: "user",
	}))
	readLine(t, scAlice) // consume room.state

	for i := 0; i < 5; i++ {
		sendLine(t, connAlice, protocol.NewNotification(protocol.MethodSend, protocol.SendParams{
			Content: []protocol.Content{{Type: "text", Text: fmt.Sprintf("message %d", i+1)}},
		}))
	}

	// Give the server time to process the messages.
	time.Sleep(100 * time.Millisecond)

	// Connect bob (new joiner).
	connBob := dialServer(t, s.Addr())
	scBob := newScanner(connBob)

	sendLine(t, connBob, protocol.NewNotification(protocol.MethodJoin, protocol.JoinParams{
		Name: "bob",
		Role: "user",
	}))

	// Bob should receive room.state with the message history.
	connBob.SetReadDeadline(time.Now().Add(2 * time.Second))
	line := readLine(t, scBob)
	connBob.SetReadDeadline(time.Time{})

	var raw protocol.RawMessage
	if err := json.Unmarshal(line, &raw); err != nil {
		t.Fatalf("unmarshal room.state: %v", err)
	}
	if raw.Method != protocol.MethodState {
		t.Fatalf("expected room.state, got %q", raw.Method)
	}

	var state protocol.RoomStateParams
	if err := json.Unmarshal(raw.Params, &state); err != nil {
		t.Fatalf("unmarshal state params: %v", err)
	}

	// The room broadcasts system messages too (alice joined), so we need at
	// least 5 user messages among the history. Filter to alice's messages.
	var aliceMsgs []protocol.MessageParams
	for _, m := range state.Messages {
		if m.From == "alice" {
			aliceMsgs = append(aliceMsgs, m)
		}
	}
	if len(aliceMsgs) != 5 {
		t.Fatalf("expected 5 messages from alice in history, got %d (total: %d)", len(aliceMsgs), len(state.Messages))
	}
}

// TestJoinReceivesAtMost50Messages verifies that when a room has more than 50
// messages, only the last 50 are included in the room.state sent to a new joiner.
func TestJoinReceivesAtMost50Messages(t *testing.T) {
	s := newTestServer(t)

	// Connect alice and send 60 messages.
	connAlice := dialServer(t, s.Addr())
	scAlice := newScanner(connAlice)

	sendLine(t, connAlice, protocol.NewNotification(protocol.MethodJoin, protocol.JoinParams{
		Name: "alice",
		Role: "user",
	}))
	readLine(t, scAlice) // consume room.state

	for i := 0; i < 60; i++ {
		sendLine(t, connAlice, protocol.NewNotification(protocol.MethodSend, protocol.SendParams{
			Content: []protocol.Content{{Type: "text", Text: fmt.Sprintf("message %d", i+1)}},
		}))
	}

	// Give the server time to process all messages.
	time.Sleep(200 * time.Millisecond)

	// Connect bob (new joiner).
	connBob := dialServer(t, s.Addr())
	scBob := newScanner(connBob)

	sendLine(t, connBob, protocol.NewNotification(protocol.MethodJoin, protocol.JoinParams{
		Name: "bob",
		Role: "user",
	}))

	connBob.SetReadDeadline(time.Now().Add(2 * time.Second))
	line := readLine(t, scBob)
	connBob.SetReadDeadline(time.Time{})

	var raw protocol.RawMessage
	if err := json.Unmarshal(line, &raw); err != nil {
		t.Fatalf("unmarshal room.state: %v", err)
	}
	if raw.Method != protocol.MethodState {
		t.Fatalf("expected room.state, got %q", raw.Method)
	}

	var state protocol.RoomStateParams
	if err := json.Unmarshal(raw.Params, &state); err != nil {
		t.Fatalf("unmarshal state params: %v", err)
	}

	if len(state.Messages) > 50 {
		t.Fatalf("expected at most 50 messages in history, got %d", len(state.Messages))
	}
	if len(state.Messages) == 0 {
		t.Fatal("expected messages in history, got 0")
	}
}

// TestNewWithRoom verifies that NewWithRoom creates a server backed by the
// provided room, preserving its topic and message history.
func TestNewWithRoom(t *testing.T) {
	// Build a room with some history.
	room := server.NewRoom("resume-topic")
	room.Broadcast("alice", "human", "human", protocol.Content{Type: "text", Text: "first message"}, nil)
	room.Broadcast("alice", "human", "human", protocol.Content{Type: "text", Text: "second message"}, nil)

	s, err := server.NewWithRoom("127.0.0.1:0", room)
	if err != nil {
		t.Fatalf("NewWithRoom: %v", err)
	}
	go s.Serve()
	t.Cleanup(func() { s.Close() })

	if s.Room().Topic != "resume-topic" {
		t.Errorf("expected topic %q, got %q", "resume-topic", s.Room().Topic)
	}
	if len(s.Room().GetMessages()) != 2 {
		t.Errorf("expected 2 messages, got %d", len(s.Room().GetMessages()))
	}

	// Connect bob and verify room.state includes history.
	conn := dialServer(t, s.Addr())
	sc := newScanner(conn)
	sendLine(t, conn, protocol.NewNotification(protocol.MethodJoin, protocol.JoinParams{
		Name: "bob",
		Role: "user",
	}))

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	line := readLine(t, sc)
	conn.SetReadDeadline(time.Time{})

	var raw protocol.RawMessage
	if err := json.Unmarshal(line, &raw); err != nil {
		t.Fatalf("unmarshal room.state: %v", err)
	}
	if raw.Method != protocol.MethodState {
		t.Fatalf("expected room.state, got %q", raw.Method)
	}

	var state protocol.RoomStateParams
	if err := json.Unmarshal(raw.Params, &state); err != nil {
		t.Fatalf("unmarshal state params: %v", err)
	}
	if state.Topic != "resume-topic" {
		t.Errorf("expected topic %q, got %q", "resume-topic", state.Topic)
	}
	// History should include the 2 alice messages.
	var aliceMsgs []protocol.MessageParams
	for _, m := range state.Messages {
		if m.From == "alice" {
			aliceMsgs = append(aliceMsgs, m)
		}
	}
	if len(aliceMsgs) != 2 {
		t.Errorf("expected 2 alice messages in history, got %d (total: %d)", len(aliceMsgs), len(state.Messages))
	}
}

// TestRoomLeftBroadcast verifies that when B disconnects, A receives a
// room.left notification with B's name.
func TestRoomLeftBroadcast(t *testing.T) {
	s := newTestServer(t)

	// Connect alice
	connAlice := dialServer(t, s.Addr())
	scAlice := newScanner(connAlice)

	sendLine(t, connAlice, protocol.NewNotification(protocol.MethodJoin, protocol.JoinParams{
		Name: "alice",
		Role: "user",
	}))
	readLine(t, scAlice) // consume room.state

	// Connect bob
	connBob := dialServer(t, s.Addr())

	sendLine(t, connBob, protocol.NewNotification(protocol.MethodJoin, protocol.JoinParams{
		Name: "bob",
		Role: "user",
	}))
	// Give the server time to process bob's join and deliver notifications to alice.
	time.Sleep(100 * time.Millisecond)

	// Bob disconnects.
	connBob.Close()

	// Alice should receive a room.left notification with bob's name.
	// She may also receive room.joined + system messages from bob's join first.
	deadline := time.Now().Add(2 * time.Second)
	var foundLeft *protocol.LeftParams
	for time.Now().Before(deadline) {
		connAlice.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		if !scAlice.Scan() {
			break
		}
		connAlice.SetReadDeadline(time.Time{})

		var raw protocol.RawMessage
		if err := json.Unmarshal(scAlice.Bytes(), &raw); err != nil {
			continue
		}
		if raw.Method != protocol.MethodLeft {
			continue
		}
		var lp protocol.LeftParams
		if err := json.Unmarshal(raw.Params, &lp); err != nil {
			continue
		}
		if lp.Name == "bob" {
			foundLeft = &lp
			break
		}
	}
	connAlice.SetReadDeadline(time.Time{})

	if foundLeft == nil {
		t.Fatal("alice never received room.left notification for bob")
	}
}
