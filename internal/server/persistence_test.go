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

func TestLoadRoomRestoresTopicAndMessages(t *testing.T) {
	dir := t.TempDir()

	room := NewRoom("discussion")
	cc := &ClientConn{Name: "alice", Role: "human", Source: "human", Send: make(chan []byte, 8), Done: make(chan struct{})}
	room.Participants["alice"] = cc

	room.Broadcast("alice", "human", "human", protocol.Content{Type: "text", Text: "hello"}, nil)
	room.Broadcast("alice", "human", "human", protocol.Content{Type: "text", Text: "world"}, nil)

	if err := SaveRoom(dir, room); err != nil {
		t.Fatalf("SaveRoom: %v", err)
	}

	loaded, err := LoadRoom(dir)
	if err != nil {
		t.Fatalf("LoadRoom: %v", err)
	}

	if loaded.Topic != "discussion" {
		t.Errorf("topic: got %q, want %q", loaded.Topic, "discussion")
	}
	msgs := loaded.GetMessages()
	if len(msgs) != 2 {
		t.Fatalf("messages: got %d, want 2", len(msgs))
	}
	if msgs[0].Content[0].Text != "hello" {
		t.Errorf("first message text: got %q, want %q", msgs[0].Content[0].Text, "hello")
	}
	if msgs[1].Content[0].Text != "world" {
		t.Errorf("second message text: got %q, want %q", msgs[1].Content[0].Text, "world")
	}
	// seq should be restored so next message gets seq 3
	if loaded.seq != 2 {
		t.Errorf("seq: got %d, want 2", loaded.seq)
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

// ---------------------------------------------------------------------------
// Session ID persistence tests
// ---------------------------------------------------------------------------

func TestParticipantDataSessionIDField(t *testing.T) {
	dir := t.TempDir()
	agents := []ParticipantData{
		{Name: "alice", Role: "agent", Source: "agent", SessionID: "sess-abc-123"},
		{Name: "bob", Role: "agent", Source: "agent", SessionID: ""},
	}

	if err := SaveAgents(dir, agents); err != nil {
		t.Fatalf("SaveAgents: %v", err)
	}

	loaded, err := LoadAgents(dir)
	if err != nil {
		t.Fatalf("LoadAgents: %v", err)
	}

	if len(loaded) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(loaded))
	}
	if loaded[0].SessionID != "sess-abc-123" {
		t.Errorf("alice SessionID: got %q, want %q", loaded[0].SessionID, "sess-abc-123")
	}
	if loaded[1].SessionID != "" {
		t.Errorf("bob SessionID: got %q, want empty", loaded[1].SessionID)
	}
}

func TestLoadAgents_NonExistentFileReturnsNil(t *testing.T) {
	dir := t.TempDir()
	agents, err := LoadAgents(dir)
	if err != nil {
		t.Fatalf("expected no error for missing agents.json, got: %v", err)
	}
	if agents != nil {
		t.Errorf("expected nil for missing file, got: %v", agents)
	}
}

func TestFindAgentSessionID_Found(t *testing.T) {
	dir := t.TempDir()
	agents := []ParticipantData{
		{Name: "alice", SessionID: "session-xyz"},
		{Name: "bob", SessionID: "session-abc"},
	}
	if err := SaveAgents(dir, agents); err != nil {
		t.Fatalf("SaveAgents: %v", err)
	}

	sid, err := FindAgentSessionID(dir, "alice")
	if err != nil {
		t.Fatalf("FindAgentSessionID: %v", err)
	}
	if sid != "session-xyz" {
		t.Errorf("got session ID %q, want %q", sid, "session-xyz")
	}
}

func TestFindAgentSessionID_NotFound(t *testing.T) {
	dir := t.TempDir()
	agents := []ParticipantData{
		{Name: "alice", SessionID: "session-xyz"},
	}
	if err := SaveAgents(dir, agents); err != nil {
		t.Fatalf("SaveAgents: %v", err)
	}

	sid, err := FindAgentSessionID(dir, "charlie")
	if err != nil {
		t.Fatalf("FindAgentSessionID: %v", err)
	}
	if sid != "" {
		t.Errorf("expected empty session ID for unknown agent, got %q", sid)
	}
}

func TestFindAgentSessionID_MissingFile(t *testing.T) {
	dir := t.TempDir()
	sid, err := FindAgentSessionID(dir, "alice")
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if sid != "" {
		t.Errorf("expected empty session ID for missing file, got %q", sid)
	}
}

func TestUpdateAgentSessionID_UpdatesExisting(t *testing.T) {
	dir := t.TempDir()
	agents := []ParticipantData{
		{Name: "alice", SessionID: "old-session"},
	}
	if err := SaveAgents(dir, agents); err != nil {
		t.Fatalf("SaveAgents: %v", err)
	}

	if err := UpdateAgentSessionID(dir, "alice", "new-session-456"); err != nil {
		t.Fatalf("UpdateAgentSessionID: %v", err)
	}

	sid, err := FindAgentSessionID(dir, "alice")
	if err != nil {
		t.Fatalf("FindAgentSessionID after update: %v", err)
	}
	if sid != "new-session-456" {
		t.Errorf("got %q, want %q", sid, "new-session-456")
	}
}

func TestUpdateAgentSessionID_AppendsNew(t *testing.T) {
	dir := t.TempDir()
	// Start with an empty but existing file.
	if err := SaveAgents(dir, []ParticipantData{}); err != nil {
		t.Fatalf("SaveAgents: %v", err)
	}

	if err := UpdateAgentSessionID(dir, "newagent", "fresh-session"); err != nil {
		t.Fatalf("UpdateAgentSessionID: %v", err)
	}

	sid, err := FindAgentSessionID(dir, "newagent")
	if err != nil {
		t.Fatalf("FindAgentSessionID: %v", err)
	}
	if sid != "fresh-session" {
		t.Errorf("got %q, want %q", sid, "fresh-session")
	}
}

func TestUpdateAgentSessionID_CreatesFileWhenMissing(t *testing.T) {
	dir := t.TempDir()
	// No agents.json exists yet.

	if err := UpdateAgentSessionID(dir, "alice", "brand-new-session"); err != nil {
		t.Fatalf("UpdateAgentSessionID: %v", err)
	}

	sid, err := FindAgentSessionID(dir, "alice")
	if err != nil {
		t.Fatalf("FindAgentSessionID: %v", err)
	}
	if sid != "brand-new-session" {
		t.Errorf("got %q, want %q", sid, "brand-new-session")
	}
}

func TestSaveLoadRoom_AutoApprove(t *testing.T) {
	dir := t.TempDir()

	room := NewRoom("yolo-topic")
	room.AutoApprove = true

	if err := SaveRoom(dir, room); err != nil {
		t.Fatalf("SaveRoom: %v", err)
	}

	loaded, err := LoadRoom(dir)
	if err != nil {
		t.Fatalf("LoadRoom: %v", err)
	}

	if !loaded.AutoApprove {
		t.Error("expected AutoApprove to be true after load")
	}
}

func TestSaveLoadRoom_AutoApproveFalse(t *testing.T) {
	dir := t.TempDir()

	room := NewRoom("normal-topic")

	if err := SaveRoom(dir, room); err != nil {
		t.Fatalf("SaveRoom: %v", err)
	}

	loaded, err := LoadRoom(dir)
	if err != nil {
		t.Fatalf("LoadRoom: %v", err)
	}

	if loaded.AutoApprove {
		t.Error("expected AutoApprove to be false after load")
	}
}

func TestSaveRoom_PreservesSavedAgentsWhenNoParticipants(t *testing.T) {
	dir := t.TempDir()

	// Simulate a previous session: agents.json has agent data with session IDs.
	prev := []ParticipantData{
		{Name: "claude", Role: "agent", AgentType: "claude", Source: "agent", SessionID: "sess-abc"},
		{Name: "gemini", Role: "agent", AgentType: "gemini", Source: "agent", SessionID: "sess-xyz"},
	}
	if err := SaveAgents(dir, prev); err != nil {
		t.Fatalf("SaveAgents: %v", err)
	}

	// Resume: Add them back as offline participants representing previously saved agents.
	room := NewRoom("topic")
	for _, a := range prev {
		room.Participants[a.Name] = &ClientConn{
			Name:      a.Name,
			Role:      a.Role,
			AgentType: a.AgentType,
			Source:    a.Source,
			Online:    false,
		}
	}

	// SaveRoom should NOT destroy the saved agents even though no one is connected.
	if err := SaveRoom(dir, room); err != nil {
		t.Fatalf("SaveRoom: %v", err)
	}

	agents, err := LoadAgents(dir)
	if err != nil {
		t.Fatalf("LoadAgents: %v", err)
	}

	if len(agents) != 2 {
		t.Fatalf("expected 2 agents in agents.json, got %d", len(agents))
	}

	// Verify session IDs are preserved.
	byName := make(map[string]ParticipantData)
	for _, a := range agents {
		byName[a.Name] = a
	}
	if byName["claude"].SessionID != "sess-abc" {
		t.Errorf("claude session ID: got %q, want %q", byName["claude"].SessionID, "sess-abc")
	}
	if byName["gemini"].SessionID != "sess-xyz" {
		t.Errorf("gemini session ID: got %q, want %q", byName["gemini"].SessionID, "sess-xyz")
	}
}

func TestSaveRoom_PreservesPartialReconnect(t *testing.T) {
	dir := t.TempDir()

	prev := []ParticipantData{
		{Name: "claude", Role: "agent", AgentType: "claude", Source: "agent", SessionID: "sess-abc"},
		{Name: "gemini", Role: "agent", AgentType: "gemini", Source: "agent", SessionID: "sess-xyz"},
	}
	if err := SaveAgents(dir, prev); err != nil {
		t.Fatalf("SaveAgents: %v", err)
	}

	room := NewRoom("topic")
	for _, a := range prev {
		room.Participants[a.Name] = &ClientConn{
			Name:      a.Name,
			Role:      a.Role,
			AgentType: a.AgentType,
			Source:    a.Source,
			Online:    false,
		}
	}

	// Only claude reconnects.
	cc := &ClientConn{Name: "claude", Role: "agent", AgentType: "claude", Source: "agent"}
	if _, err := room.Join(cc); err != nil {
		t.Fatalf("Join: %v", err)
	}

	if err := SaveRoom(dir, room); err != nil {
		t.Fatalf("SaveRoom: %v", err)
	}

	agents, err := LoadAgents(dir)
	if err != nil {
		t.Fatalf("LoadAgents: %v", err)
	}

	// Both agents should be preserved, no duplicates.
	if len(agents) != 2 {
		t.Fatalf("expected 2 agents, got %d: %v", len(agents), agents)
	}

	byName := make(map[string]ParticipantData)
	for _, a := range agents {
		byName[a.Name] = a
	}
	if byName["claude"].SessionID != "sess-abc" {
		t.Errorf("claude session ID: got %q, want %q", byName["claude"].SessionID, "sess-abc")
	}
	if byName["gemini"].SessionID != "sess-xyz" {
		t.Errorf("gemini session ID: got %q, want %q", byName["gemini"].SessionID, "sess-xyz")
	}
}

func TestSaveRoom_AgentsIncludeSessionID(t *testing.T) {
	dir := t.TempDir()

	room := NewRoom("topic")
	cc := &ClientConn{
		Name:      "agent1",
		Role:      "agent",
		Source:    "agent",
		AgentType: "claude",
		Send:      make(chan []byte, 8),
		Done:      make(chan struct{}),
	}
	if _, err := room.Join(cc); err != nil {
		t.Fatalf("room.Join: %v", err)
	}

	if err := SaveRoom(dir, room); err != nil {
		t.Fatalf("SaveRoom: %v", err)
	}

	// agents.json should exist.
	var agents []ParticipantData
	if err := readJSON(filepath.Join(dir, AgentsFile), &agents); err != nil {
		t.Fatalf("read agents.json: %v", err)
	}

	if len(agents) != 1 {
		t.Fatalf("expected 1 agent in agents.json, got %d", len(agents))
	}
	if agents[0].Name != "agent1" {
		t.Errorf("agent name: got %q, want %q", agents[0].Name, "agent1")
	}
}
