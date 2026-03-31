package server_test

import (
	"bufio"
	"encoding/json"
	"net"
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

	join := protocol.NewNotification("room.join", protocol.JoinParams{
		Name: "alice",
		Role: "user",
	})
	sendLine(t, conn, join)

	line := readLine(t, sc)

	var raw protocol.RawMessage
	if err := json.Unmarshal(line, &raw); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if raw.Method != "room.state" {
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

	sendLine(t, connAlice, protocol.NewNotification("room.join", protocol.JoinParams{
		Name: "alice",
		Role: "user",
	}))
	readLine(t, scAlice) // consume room.state

	// Connect bob
	connBob := dialServer(t, s.Addr())
	scBob := newScanner(connBob)

	sendLine(t, connBob, protocol.NewNotification("room.join", protocol.JoinParams{
		Name: "bob",
		Role: "user",
	}))
	readLine(t, scBob) // consume room.state

	// Bob sends a message
	sendLine(t, connBob, protocol.NewNotification("room.send", protocol.SendParams{
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
		if raw.Method != "room.message" {
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

	sendLine(t, connAlice, protocol.NewNotification("room.join", protocol.JoinParams{
		Name: "Alice",
		Role: "user",
	}))
	readLine(t, scAlice) // consume room.state

	// Connect a second client that also tries to join as "Alice".
	connAlice2 := dialServer(t, s.Addr())
	scAlice2 := newScanner(connAlice2)

	sendLine(t, connAlice2, protocol.NewNotification("room.join", protocol.JoinParams{
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
	if raw.Error.Message != "name already taken" {
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

// TestRoomLeftBroadcast verifies that when B disconnects, A receives a
// room.left notification with B's name.
func TestRoomLeftBroadcast(t *testing.T) {
	s := newTestServer(t)

	// Connect alice
	connAlice := dialServer(t, s.Addr())
	scAlice := newScanner(connAlice)

	sendLine(t, connAlice, protocol.NewNotification("room.join", protocol.JoinParams{
		Name: "alice",
		Role: "user",
	}))
	readLine(t, scAlice) // consume room.state

	// Connect bob
	connBob := dialServer(t, s.Addr())

	sendLine(t, connBob, protocol.NewNotification("room.join", protocol.JoinParams{
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
		if raw.Method != "room.left" {
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
