package main

import (
	"bufio"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/khaiql/parley/internal/protocol"
	"github.com/khaiql/parley/internal/server"
)

// TestHostResumeLoadsHistory verifies that when a room is saved and then a
// server is started with NewWithRoom (the path taken by --resume), a joining
// client receives the historical messages in room.state.
func TestHostResumeLoadsHistory(t *testing.T) {
	dir := t.TempDir()

	// Create a room, add messages, and save it.
	originalRoom := server.NewRoom("resume-topic")
	originalRoom.Broadcast("alice", "human", "human",
		protocol.Content{Type: "text", Text: "message one"}, nil)
	originalRoom.Broadcast("alice", "human", "human",
		protocol.Content{Type: "text", Text: "message two"}, nil)

	roomID := originalRoom.ID

	if err := server.SaveRoom(filepath.Join(dir, roomID), originalRoom); err != nil {
		t.Fatalf("SaveRoom: %v", err)
	}

	// Simulate --resume: load the room and start a new server.
	loadDir := filepath.Join(dir, roomID)
	loaded, err := server.LoadRoom(loadDir)
	if err != nil {
		t.Fatalf("LoadRoom: %v", err)
	}
	if loaded.Topic != "resume-topic" {
		t.Errorf("expected topic %q, got %q", "resume-topic", loaded.Topic)
	}
	if len(loaded.GetMessages()) != 2 {
		t.Errorf("expected 2 messages after load, got %d", len(loaded.GetMessages()))
	}

	srv, err := server.NewWithRoom("127.0.0.1:0", loaded)
	if err != nil {
		t.Fatalf("NewWithRoom: %v", err)
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

func TestIsMentioned(t *testing.T) {
	tests := []struct {
		name     string
		mentions []string
		agent    string
		want     bool
	}{
		{
			name:     "mentioned",
			mentions: []string{"alice", "bob"},
			agent:    "bob",
			want:     true,
		},
		{
			name:     "not mentioned",
			mentions: []string{"alice", "charlie"},
			agent:    "bob",
			want:     false,
		},
		{
			name:     "empty mentions",
			mentions: []string{},
			agent:    "bob",
			want:     false,
		},
		{
			name:     "nil mentions",
			mentions: nil,
			agent:    "bob",
			want:     false,
		},
		{
			name:     "exact match required",
			mentions: []string{"bobby"},
			agent:    "bob",
			want:     false,
		},
		{
			name:     "single match",
			mentions: []string{"bob"},
			agent:    "bob",
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isMentioned(tt.mentions, tt.agent)
			if got != tt.want {
				t.Errorf("isMentioned(%v, %q) = %v, want %v", tt.mentions, tt.agent, got, tt.want)
			}
		})
	}
}
