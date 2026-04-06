package main

import (
	"bufio"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/khaiql/parley/internal/command"
	"github.com/khaiql/parley/internal/persistence"
	"github.com/khaiql/parley/internal/protocol"
	"github.com/khaiql/parley/internal/room"
	"github.com/khaiql/parley/internal/server"
)

// TestHostResumeLoadsHistory verifies that when a room is saved and then a
// server is started with a restored room.State (the path taken by --resume),
// a joining client receives the historical messages in room.state.
func TestHostResumeLoadsHistory(t *testing.T) {
	dir := t.TempDir()

	// Create a room.State, add messages, and save via persistence.JSONStore.
	originalState := room.New(nil, command.Context{})
	originalState.Restore(originalState.GetID(), "resume-topic", nil, nil, false)
	originalState.AddMessage("alice", "human", "human",
		protocol.Content{Type: "text", Text: "message one"})
	originalState.AddMessage("alice", "human", "human",
		protocol.Content{Type: "text", Text: "message two"})

	roomID := originalState.GetID()

	store := persistence.NewJSONStore(dir)
	if err := store.Save(protocol.RoomSnapshot{
		RoomID:       roomID,
		Topic:        "resume-topic",
		Participants: originalState.GetParticipants(),
		Messages:     originalState.Messages(),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Simulate --resume: load snapshot and restore into a new room.State.
	snapshot, err := store.Load(roomID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if snapshot.Topic != "resume-topic" {
		t.Errorf("expected topic %q, got %q", "resume-topic", snapshot.Topic)
	}
	if len(snapshot.Messages) != 2 {
		t.Errorf("expected 2 messages after load, got %d", len(snapshot.Messages))
	}

	resumedState := room.New(nil, command.Context{})
	resumedState.Restore(snapshot.RoomID, snapshot.Topic, snapshot.Participants, snapshot.Messages, snapshot.AutoApprove)

	srv, err := server.New("127.0.0.1:0", resumedState)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	go srv.Serve()
	t.Cleanup(func() { srv.Close() })

	// Connect bob and check he receives the historical messages in room.state.
	conn, dialErr := net.DialTimeout("tcp", srv.Addr(), 2*time.Second)
	if dialErr != nil {
		t.Fatalf("dial: %v", dialErr)
	}
	t.Cleanup(func() { conn.Close() })

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)

	join := protocol.NewNotification(protocol.MethodJoin, protocol.JoinParams{
		Name: "bob",
		Role: "user",
	})
	data, _ := protocol.EncodeLine(join)
	if _, err := conn.Write(data); err != nil {
		t.Fatalf("write join: %v", err)
	}

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if !sc.Scan() {
		t.Fatalf("no response from server")
	}
	conn.SetReadDeadline(time.Time{})

	var raw protocol.RawMessage
	if err := json.Unmarshal(sc.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if raw.Method != protocol.MethodState {
		t.Fatalf("expected room.state, got %q", raw.Method)
	}

	var state protocol.RoomStateParams
	if err := json.Unmarshal(raw.Params, &state); err != nil {
		t.Fatalf("unmarshal state: %v", err)
	}

	if state.Topic != "resume-topic" {
		t.Errorf("expected topic %q, got %q", "resume-topic", state.Topic)
	}

	var aliceMsgs []protocol.MessageParams
	for _, m := range state.Messages {
		if m.From == "alice" {
			aliceMsgs = append(aliceMsgs, m)
		}
	}
	if len(aliceMsgs) != 2 {
		t.Errorf("expected 2 alice messages in resumed room.state, got %d (total: %d)",
			len(aliceMsgs), len(state.Messages))
	}

}

func TestRandomNameDistribution(t *testing.T) {
	// Call randomName many times and check we get more than one unique name.
	// A properly seeded RNG should produce variety over 100 calls.
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		seen[randomName()] = true
	}
	if len(seen) < 2 {
		t.Errorf("randomName() produced only %d unique name(s) over 100 calls; expected variety", len(seen))
	}
}
