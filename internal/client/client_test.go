package client_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/khaiql/parley/internal/client"
	"github.com/khaiql/parley/internal/command"
	"github.com/khaiql/parley/internal/protocol"
	"github.com/khaiql/parley/internal/room"
	"github.com/khaiql/parley/internal/server"
)

// drain reads up to n messages from ch within a timeout, returning what it collected.
func drain(ch <-chan *protocol.RawMessage, n int) []*protocol.RawMessage {
	var msgs []*protocol.RawMessage
	deadline := time.After(3 * time.Second)
	for len(msgs) < n {
		select {
		case m := <-ch:
			msgs = append(msgs, m)
		case <-deadline:
			return msgs
		}
	}
	return msgs
}

// drainPending reads all currently buffered messages with a short timeout.
func drainPending(ch <-chan *protocol.RawMessage) {
	time.Sleep(100 * time.Millisecond)
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

func newClientTestServer(t *testing.T) *server.TCPServer {
	t.Helper()
	state := room.New(nil, command.Context{})
	state.Restore(state.GetID(), "test-room", nil, nil, false)
	srv, err := server.New("127.0.0.1:0", state)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	go srv.Serve()
	t.Cleanup(func() { srv.Close() })
	return srv
}

func TestClientConnectsAndJoins(t *testing.T) {
	srv := newClientTestServer(t)

	// Connect a client.
	c, err := client.New(srv.Addr())
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	defer c.Close()

	// Send a join.
	err = c.Join(protocol.JoinParams{Name: "alice", Role: "human"})
	if err != nil {
		t.Fatalf("Join: %v", err)
	}

	// Expect at least one message on Incoming() — should be room.state.
	msgs := drain(c.Incoming(), 1)
	if len(msgs) == 0 {
		t.Fatal("expected at least 1 message on Incoming(), got 0")
	}

	found := false
	for _, m := range msgs {
		if m.Method == protocol.MethodState {
			found = true
			var params protocol.RoomStateParams
			if err := json.Unmarshal(m.Params, &params); err != nil {
				t.Fatalf("unmarshal room.state params: %v", err)
			}
			if len(params.Participants) == 0 {
				t.Error("room.state has no participants")
			}
			break
		}
	}
	if !found {
		t.Errorf("no room.state message received; got methods: %v", methodsOf(msgs))
	}
}

func TestClientSendsAndReceives(t *testing.T) {
	srv := newClientTestServer(t)
	addr := srv.Addr()

	// Connect first client (alice).
	alice, err := client.New(addr)
	if err != nil {
		t.Fatalf("client.New alice: %v", err)
	}
	defer alice.Close()

	if err := alice.Join(protocol.JoinParams{Name: "alice", Role: "human"}); err != nil {
		t.Fatalf("alice.Join: %v", err)
	}
	// Drain alice's initial room.state.
	drain(alice.Incoming(), 1)

	// Connect second client (bob).
	bob, err := client.New(addr)
	if err != nil {
		t.Fatalf("client.New bob: %v", err)
	}
	defer bob.Close()

	if err := bob.Join(protocol.JoinParams{Name: "bob", Role: "human"}); err != nil {
		t.Fatalf("bob.Join: %v", err)
	}
	// Drain bob's room.state and any join/system notifications.
	drainPending(bob.Incoming())

	// Alice sends a message.
	content := protocol.Content{Type: "text", Text: "hello from alice"}
	if err := alice.Send(content, nil); err != nil {
		t.Fatalf("alice.Send: %v", err)
	}

	// Bob should receive a room.message from alice.
	msgs := drain(bob.Incoming(), 1)
	found := false
	for _, m := range msgs {
		if m.Method == protocol.MethodMessage {
			var params protocol.MessageParams
			if err := json.Unmarshal(m.Params, &params); err != nil {
				t.Fatalf("unmarshal room.message params: %v", err)
			}
			if params.From == "alice" && len(params.Content) > 0 && params.Content[0].Text == "hello from alice" {
				found = true
				break
			}
		}
	}
	if !found {
		t.Errorf("bob did not receive alice's message; got: %v", methodsOf(msgs))
	}
}

// TestClientSendStatus verifies that sending a room.status notification results
// in other participants receiving it.
func TestClientSendStatus(t *testing.T) {
	srv := newClientTestServer(t)
	addr := srv.Addr()

	// Connect alice.
	alice, err := client.New(addr)
	if err != nil {
		t.Fatalf("client.New alice: %v", err)
	}
	defer alice.Close()
	if err := alice.Join(protocol.JoinParams{Name: "alice", Role: "human"}); err != nil {
		t.Fatalf("alice.Join: %v", err)
	}
	drainPending(alice.Incoming()) // consume room.state

	// Connect bot.
	bot, err := client.New(addr)
	if err != nil {
		t.Fatalf("client.New bot: %v", err)
	}
	defer bot.Close()
	if err := bot.Join(protocol.JoinParams{Name: "bot", Role: "agent", AgentType: "claude"}); err != nil {
		t.Fatalf("bot.Join: %v", err)
	}
	drainPending(bot.Incoming()) // consume room.state + join notifications

	// Drain alice's join/system notifications from bot joining.
	drainPending(alice.Incoming())

	// Bot sends a status update.
	if err := bot.SendStatus("bot", "thinking…"); err != nil {
		t.Fatalf("bot.SendStatus: %v", err)
	}

	// Alice should receive a room.status notification.
	msgs := drain(alice.Incoming(), 1)
	found := false
	for _, m := range msgs {
		if m.Method == protocol.MethodStatus {
			var params protocol.StatusParams
			if err := json.Unmarshal(m.Params, &params); err != nil {
				t.Fatalf("unmarshal room.status params: %v", err)
			}
			if params.Name == "bot" && params.Status == "thinking…" {
				found = true
				break
			}
		}
	}
	if !found {
		t.Errorf("alice did not receive bot's status; got methods: %v", methodsOf(msgs))
	}
}

// TestClientConcurrentClose verifies that calling Close() from two goroutines
// simultaneously does not panic.
func TestClientConcurrentClose(t *testing.T) {
	srv := newClientTestServer(t)

	c, err := client.New(srv.Addr())
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}

	// Call Close() concurrently from two goroutines — must not panic.
	done := make(chan struct{})
	go func() {
		c.Close()
		close(done)
	}()
	c.Close()
	<-done
}

// TestClientIncomingClosesAfterDisconnect verifies that ranging over Incoming()
// terminates (does not hang) after Close() is called.
func TestClientIncomingClosesAfterDisconnect(t *testing.T) {
	srv := newClientTestServer(t)

	c, err := client.New(srv.Addr())
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}

	rangeFinished := make(chan struct{})
	go func() {
		// range should unblock once incoming is closed.
		for range c.Incoming() {
		}
		close(rangeFinished)
	}()

	c.Close()

	select {
	case <-rangeFinished:
		// success — range terminated
	case <-time.After(3 * time.Second):
		t.Fatal("range c.Incoming() did not terminate after Close()")
	}
}

// methodsOf extracts method names from a slice of RawMessages for error output.
func methodsOf(msgs []*protocol.RawMessage) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Method
	}
	return out
}
