package client_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/khaiql/parley/internal/client"
	"github.com/khaiql/parley/internal/protocol"
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

func TestClientConnectsAndJoins(t *testing.T) {
	// Start a real server on a random port.
	srv, err := server.New("127.0.0.1:0", "test-room")
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	defer srv.Close()
	go srv.Serve()

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
		if m.Method == "room.state" {
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
	// Start a real server on a random port.
	srv, err := server.New("127.0.0.1:0", "test-room")
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	defer srv.Close()
	go srv.Serve()

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
	// Drain bob's initial room.state.
	drain(bob.Incoming(), 1)

	// Alice sends a message.
	content := protocol.Content{Type: "text", Text: "hello from alice"}
	if err := alice.Send(content, nil); err != nil {
		t.Fatalf("alice.Send: %v", err)
	}

	// Bob should receive a room.message.
	msgs := drain(bob.Incoming(), 3) // allow for system messages too
	found := false
	for _, m := range msgs {
		if m.Method == "room.message" {
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

// methodsOf extracts method names from a slice of RawMessages for error output.
func methodsOf(msgs []*protocol.RawMessage) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Method
	}
	return out
}
