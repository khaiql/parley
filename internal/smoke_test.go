package integration_test

import (
	"bufio"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/khaiql/parley/internal/protocol"
	"github.com/khaiql/parley/internal/server"
)

// TestSmokeFullFlow exercises the core product flow end-to-end:
// host creates room → agent joins → messages exchanged → agent leaves → sidebar updates
func TestSmokeFullFlow(t *testing.T) {
	// 1. Start server
	srv, err := server.New("127.0.0.1:0", "Smoke test: Go best practices")
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	defer srv.Close()
	go srv.Serve()

	// 2. Human joins
	humanConn := dial(t, srv.Addr())
	defer humanConn.Close()
	human := newTestClient(humanConn)
	human.sendJoin(t, "sle", "human", "/Users/sle/project", "")

	// Should get room.state
	msg := human.readMethod(t, "room.state", 2*time.Second)
	var state protocol.RoomStateParams
	json.Unmarshal(msg.Params, &state)
	if state.Topic != "Smoke test: Go best practices" {
		t.Errorf("topic = %q, want expected topic", state.Topic)
	}
	if len(state.Participants) != 1 {
		t.Errorf("participants = %d, want 1", len(state.Participants))
	}

	// 3. Agent joins
	agentConn := dial(t, srv.Addr())
	defer agentConn.Close()
	agent := newTestClient(agentConn)
	agent.sendJoin(t, "GoExpert", "Go specialist", "/Users/sle/project", "claude")

	// Agent gets room.state with 2 participants
	agentStateMsg := agent.readMethod(t, "room.state", 2*time.Second)
	var aState protocol.RoomStateParams
	json.Unmarshal(agentStateMsg.Params, &aState)
	if len(aState.Participants) != 2 {
		t.Errorf("agent sees %d participants, want 2", len(aState.Participants))
	}

	// Human should get room.joined
	msg = human.readMethod(t, "room.joined", 2*time.Second)
	var joined protocol.JoinedParams
	json.Unmarshal(msg.Params, &joined)
	if joined.Name != "GoExpert" {
		t.Errorf("joined name = %q, want GoExpert", joined.Name)
	}

	// 4. Human sends a message with @-mention
	human.sendMsg(t, "Hey @GoExpert, what do you think about error handling?", []string{"GoExpert"})

	// Agent receives the message — skip system messages, find one from "sle"
	msg = agent.readMessageFrom(t, "sle", 2*time.Second)
	var chatMsg protocol.MessageParams
	json.Unmarshal(msg.Params, &chatMsg)
	if chatMsg.Source != "human" {
		t.Errorf("source = %q, want human", chatMsg.Source)
	}
	if len(chatMsg.Mentions) == 0 || chatMsg.Mentions[0] != "GoExpert" {
		t.Errorf("mentions = %v, want [GoExpert]", chatMsg.Mentions)
	}
	// Verify atomic ID format
	if !strings.HasPrefix(chatMsg.ID, "msg-") {
		t.Errorf("id = %q, want msg-N format", chatMsg.ID)
	}

	// 5. Agent sends a response
	agent.sendMsg(t, "Always use error wrapping with fmt.Errorf!", nil)

	// Human receives it — find message from "GoExpert"
	msg = human.readMessageFrom(t, "GoExpert", 2*time.Second)
	json.Unmarshal(msg.Params, &chatMsg)
	if chatMsg.Source != "agent" {
		t.Errorf("source = %q, want agent", chatMsg.Source)
	}

	// 6. Agent disconnects — human should get room.left
	agentConn.Close()
	msg = human.readMethod(t, "room.left", 2*time.Second)
	var left protocol.LeftParams
	json.Unmarshal(msg.Params, &left)
	if left.Name != "GoExpert" {
		t.Errorf("left name = %q, want GoExpert", left.Name)
	}

	// 7. Verify persistence
	dir := t.TempDir()
	if err := server.SaveRoom(dir, srv.Room()); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := server.LoadRoom(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Topic != "Smoke test: Go best practices" {
		t.Errorf("loaded topic = %q", loaded.Topic)
	}
	if len(loaded.Messages) < 3 {
		t.Errorf("loaded %d messages, want >= 3", len(loaded.Messages))
	}

	t.Log("Smoke test passed — full flow verified")
}

// --- testClient wraps a persistent scanner over a TCP connection ---

type testClient struct {
	conn    net.Conn
	scanner *bufio.Scanner
}

func newTestClient(conn net.Conn) *testClient {
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	return &testClient{conn: conn, scanner: sc}
}

func (tc *testClient) sendJoin(t *testing.T, name, role, dir, agentType string) {
	t.Helper()
	n := protocol.NewNotification("room.join", protocol.JoinParams{
		Name: name, Role: role, Directory: dir, AgentType: agentType,
	})
	data, _ := protocol.EncodeLine(n)
	tc.conn.Write(data)
}

func (tc *testClient) sendMsg(t *testing.T, text string, mentions []string) {
	t.Helper()
	n := protocol.NewNotification("room.send", protocol.SendParams{
		Content:  []protocol.Content{{Type: "text", Text: text}},
		Mentions: mentions,
	})
	data, _ := protocol.EncodeLine(n)
	tc.conn.Write(data)
}

func (tc *testClient) readMethod(t *testing.T, method string, timeout time.Duration) *protocol.RawMessage {
	t.Helper()
	tc.conn.SetReadDeadline(time.Now().Add(timeout))
	for tc.scanner.Scan() {
		var msg protocol.RawMessage
		if err := json.Unmarshal(tc.scanner.Bytes(), &msg); err != nil {
			continue
		}
		if msg.Method == method {
			return &msg
		}
	}
	t.Fatalf("timeout waiting for %s", method)
	return nil
}

func (tc *testClient) readMessageFrom(t *testing.T, from string, timeout time.Duration) *protocol.RawMessage {
	t.Helper()
	tc.conn.SetReadDeadline(time.Now().Add(timeout))
	for tc.scanner.Scan() {
		var msg protocol.RawMessage
		if err := json.Unmarshal(tc.scanner.Bytes(), &msg); err != nil {
			continue
		}
		if msg.Method == "room.message" {
			var params protocol.MessageParams
			if err := json.Unmarshal(msg.Params, &params); err == nil && params.From == from {
				return &msg
			}
		}
	}
	t.Fatalf("timeout waiting for message from %s", from)
	return nil
}

func (tc *testClient) drainUntilQuiet(quiet time.Duration) {
	tc.conn.SetReadDeadline(time.Now().Add(quiet))
	for tc.scanner.Scan() {
		// consume
		tc.conn.SetReadDeadline(time.Now().Add(quiet))
	}
}

func dial(t *testing.T, addr string) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	return conn
}
