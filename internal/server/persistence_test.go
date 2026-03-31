package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/khaiql/parley/internal/protocol"
)

func TestSaveAndLoadRoom(t *testing.T) {
	dir := t.TempDir()

	// Create a room and add a participant.
	room := NewRoom("test topic")
	cc := &ClientConn{
		Name:      "alice",
		Role:      "human",
		Directory: "/tmp/alice",
		Repo:      "https://github.com/example/repo",
		AgentType: "",
		Source:    "human",
	}
	if _, err := room.Join(cc); err != nil {
		t.Fatalf("room.Join: %v", err)
	}

	// Broadcast a message.
	room.Broadcast("alice", "human", "human", protocol.Content{Type: "text", Text: "hello world"}, nil)

	// Save the room.
	if err := SaveRoom(dir, room); err != nil {
		t.Fatalf("SaveRoom: %v", err)
	}

	// Load it back.
	loaded, err := LoadRoom(dir)
	if err != nil {
		t.Fatalf("LoadRoom: %v", err)
	}

	// Verify topic.
	if loaded.Topic != room.Topic {
		t.Errorf("topic: got %q, want %q", loaded.Topic, room.Topic)
	}

	// Verify messages.
	origMsgs := room.GetMessages()
	loadedMsgs := loaded.GetMessages()
	if len(loadedMsgs) != len(origMsgs) {
		t.Fatalf("message count: got %d, want %d", len(loadedMsgs), len(origMsgs))
	}
	if loadedMsgs[0].From != origMsgs[0].From {
		t.Errorf("message from: got %q, want %q", loadedMsgs[0].From, origMsgs[0].From)
	}
	if len(loadedMsgs[0].Content) == 0 || loadedMsgs[0].Content[0].Text != "hello world" {
		t.Errorf("message content: got %+v, want text 'hello world'", loadedMsgs[0].Content)
	}
}

func TestSaveCreatesDirectory(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "new", "nested", "dir")

	// Directory must not exist yet.
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("expected dir to not exist before SaveRoom")
	}

	room := NewRoom("topic")
	if err := SaveRoom(dir, room); err != nil {
		t.Fatalf("SaveRoom: %v", err)
	}

	// Directory should exist now.
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("dir not created: %v", err)
	}
}

func TestRoomDir(t *testing.T) {
	dir := RoomDir("abc123")
	if !strings.Contains(dir, ".parley") {
		t.Errorf("expected '.parley' in path, got %q", dir)
	}
	if !strings.HasSuffix(dir, "/rooms/abc123") {
		t.Errorf("expected path to end with /rooms/abc123, got %q", dir)
	}
}

func TestSaveLoadRoomPreservesID(t *testing.T) {
	dir := t.TempDir()

	room := NewRoom("id-topic")
	originalID := room.ID
	if originalID == "" {
		t.Fatal("NewRoom() must set a non-empty ID before this test is meaningful")
	}

	if err := SaveRoom(dir, room); err != nil {
		t.Fatalf("SaveRoom: %v", err)
	}

	loaded, err := LoadRoom(dir)
	if err != nil {
		t.Fatalf("LoadRoom: %v", err)
	}

	if loaded.ID != originalID {
		t.Errorf("LoadRoom restored ID %q, want %q", loaded.ID, originalID)
	}
}

func TestSaveRoomUsesRoomID(t *testing.T) {
	dir := t.TempDir()
	room := NewRoom("topic")

	if err := SaveRoom(dir, room); err != nil {
		t.Fatalf("SaveRoom: %v", err)
	}

	// Read the saved room.json and verify the id field matches room.ID.
	var rd RoomData
	if err := readJSON(filepath.Join(dir, "room.json"), &rd); err != nil {
		t.Fatalf("readJSON: %v", err)
	}

	if rd.ID != room.ID {
		t.Errorf("room.json ID = %q, want room.ID = %q", rd.ID, room.ID)
	}
}
