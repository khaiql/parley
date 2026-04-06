package integration_test

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/khaiql/parley/internal/client"
	"github.com/khaiql/parley/internal/protocol"
	"github.com/khaiql/parley/internal/server"
)

// readMsg reads the next message from the client's incoming channel, failing
// the test if nothing arrives within the timeout.
func readMsg(t *testing.T, c *client.TCPClient, timeout time.Duration) *protocol.RawMessage {
	t.Helper()
	select {
	case msg := <-c.Incoming():
		return msg
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for message")
		return nil
	}
}

// readMsgWithMethod reads messages from the client until one with the expected
// method is found, discarding any that don't match.
func readMsgWithMethod(t *testing.T, c *client.TCPClient, method string, timeout time.Duration) *protocol.RawMessage {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		select {
		case msg := <-c.Incoming():
			if msg.Method == method {
				return msg
			}
		case <-time.After(remaining):
			t.Fatalf("timed out waiting for method %q", method)
			return nil
		}
	}
	t.Fatalf("timed out waiting for method %q", method)
	return nil
}

func TestEndToEndHostAndJoin(t *testing.T) {
	const timeout = 2 * time.Second

	// 1. Start a server with a topic.
	srv, err := server.New("127.0.0.1:0", "Design the persistence layer")
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer srv.Close()
	go srv.Serve()

	addr := srv.Addr()

	// 2. Connect human client, join as human (no agent_type).
	humanClient, err := client.New(addr)
	if err != nil {
		t.Fatalf("connect human client: %v", err)
	}
	defer humanClient.Close()

	if err := humanClient.Join(protocol.JoinParams{
		Name: "Alice",
		Role: "human",
	}); err != nil {
		t.Fatalf("human join: %v", err)
	}

	// 3. Verify human receives room.state with correct topic.
	stateMsg := readMsgWithMethod(t, humanClient, protocol.MethodState, timeout)
	var humanState protocol.RoomStateParams
	if err := json.Unmarshal(stateMsg.Params, &humanState); err != nil {
		t.Fatalf("unmarshal room.state: %v", err)
	}
	if humanState.Topic != "Design the persistence layer" {
		t.Errorf("expected topic %q, got %q", "Design the persistence layer", humanState.Topic)
	}
	if len(humanState.Participants) != 1 {
		t.Errorf("expected 1 participant, got %d", len(humanState.Participants))
	}

	// Drain the system message ("Alice joined") that the human receives about themselves.
	readMsgWithMethod(t, humanClient, protocol.MethodMessage, timeout)

	// 4. Connect agent client, join as agent (with agent_type "claude").
	agentClient, err := client.New(addr)
	if err != nil {
		t.Fatalf("connect agent client: %v", err)
	}
	defer agentClient.Close()

	if err := agentClient.Join(protocol.JoinParams{
		Name:      "GoExpert",
		Role:      "assistant",
		AgentType: "claude",
	}); err != nil {
		t.Fatalf("agent join: %v", err)
	}

	// 5. Verify human receives room.joined notification for the agent.
	joinedMsg := readMsgWithMethod(t, humanClient, protocol.MethodJoined, timeout)
	var joined protocol.JoinedParams
	if err := json.Unmarshal(joinedMsg.Params, &joined); err != nil {
		t.Fatalf("unmarshal room.joined: %v", err)
	}
	if joined.Name != "GoExpert" {
		t.Errorf("expected joined name %q, got %q", "GoExpert", joined.Name)
	}
	if joined.AgentType != "claude" {
		t.Errorf("expected agent_type %q, got %q", "claude", joined.AgentType)
	}

	// 6. Verify agent receives room.state with both participants.
	agentStateMsg := readMsgWithMethod(t, agentClient, protocol.MethodState, timeout)
	var agentState protocol.RoomStateParams
	if err := json.Unmarshal(agentStateMsg.Params, &agentState); err != nil {
		t.Fatalf("unmarshal agent room.state: %v", err)
	}
	if len(agentState.Participants) != 2 {
		t.Errorf("expected 2 participants in agent state, got %d", len(agentState.Participants))
	}

	// Drain the "GoExpert joined" system message that goes to human.
	readMsgWithMethod(t, humanClient, protocol.MethodMessage, timeout)
	// Drain the "GoExpert joined" system message that goes to agent too.
	readMsgWithMethod(t, agentClient, protocol.MethodMessage, timeout)

	// 7. Human sends a message with a mention.
	if err := humanClient.Send(
		protocol.Content{Type: "text", Text: "Hello @GoExpert, what do you think?"},
		[]string{"GoExpert"},
	); err != nil {
		t.Fatalf("human send: %v", err)
	}

	// 8. Verify agent receives room.message with correct content, source="human", mentions=["GoExpert"].
	agentMsg := readMsgWithMethod(t, agentClient, protocol.MethodMessage, timeout)
	var agentRecv protocol.MessageParams
	if err := json.Unmarshal(agentMsg.Params, &agentRecv); err != nil {
		t.Fatalf("unmarshal room.message (agent recv): %v", err)
	}
	if agentRecv.Source != "human" {
		t.Errorf("expected source %q, got %q", "human", agentRecv.Source)
	}
	if len(agentRecv.Content) == 0 || agentRecv.Content[0].Text != "Hello @GoExpert, what do you think?" {
		t.Errorf("unexpected content: %+v", agentRecv.Content)
	}
	if len(agentRecv.Mentions) != 1 || agentRecv.Mentions[0] != "GoExpert" {
		t.Errorf("expected mentions [GoExpert], got %v", agentRecv.Mentions)
	}

	// Human also receives its own broadcast.
	readMsgWithMethod(t, humanClient, protocol.MethodMessage, timeout)

	// 9. Agent sends a message back.
	if err := agentClient.Send(
		protocol.Content{Type: "text", Text: "I think we should use goroutines"},
		nil,
	); err != nil {
		t.Fatalf("agent send: %v", err)
	}

	// 10. Verify human receives room.message with correct content, source="agent".
	humanMsg := readMsgWithMethod(t, humanClient, protocol.MethodMessage, timeout)
	var humanRecv protocol.MessageParams
	if err := json.Unmarshal(humanMsg.Params, &humanRecv); err != nil {
		t.Fatalf("unmarshal room.message (human recv): %v", err)
	}
	if humanRecv.Source != "agent" {
		t.Errorf("expected source %q, got %q", "agent", humanRecv.Source)
	}
	if len(humanRecv.Content) == 0 || humanRecv.Content[0].Text != "I think we should use goroutines" {
		t.Errorf("unexpected content: %+v", humanRecv.Content)
	}

	// Agent also receives its own broadcast.
	readMsgWithMethod(t, agentClient, protocol.MethodMessage, timeout)

	// 11. Disconnect agent.
	agentClient.Close()

	// 12. Verify human receives system message about agent leaving.
	leaveMsg := readMsgWithMethod(t, humanClient, protocol.MethodMessage, timeout)
	var leaveParams protocol.MessageParams
	if err := json.Unmarshal(leaveMsg.Params, &leaveParams); err != nil {
		t.Fatalf("unmarshal leave system message: %v", err)
	}
	if leaveParams.Source != "system" {
		t.Errorf("expected system source for leave message, got %q", leaveParams.Source)
	}
	if len(leaveParams.Content) == 0 {
		t.Errorf("expected non-empty content in leave message")
	}

	// 13. Clean up human client.
	humanClient.Close()

	// --- Persistence tests ---

	// 14. After the conversation, call SaveRoom to a temp dir.
	tmpDir, err := os.MkdirTemp("", "parley-integration-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := server.SaveRoom(tmpDir, srv.Room()); err != nil {
		t.Fatalf("SaveRoom: %v", err)
	}

	// 15. Call LoadRoom and verify topic and messages were persisted.
	loaded, err := server.LoadRoom(tmpDir)
	if err != nil {
		t.Fatalf("LoadRoom: %v", err)
	}
	if loaded.Topic != "Design the persistence layer" {
		t.Errorf("persisted topic: expected %q, got %q", "Design the persistence layer", loaded.Topic)
	}
	msgs := loaded.GetMessages()
	// We expect at least the two chat messages (human and agent) plus system messages.
	// The human chat message and the agent chat message are the key ones.
	foundHuman := false
	foundAgent := false
	for _, m := range msgs {
		if m.Source == "human" && len(m.Content) > 0 && m.Content[0].Text == "Hello @GoExpert, what do you think?" {
			foundHuman = true
		}
		if m.Source == "agent" && len(m.Content) > 0 && m.Content[0].Text == "I think we should use goroutines" {
			foundAgent = true
		}
	}
	if !foundHuman {
		t.Errorf("human message not found in persisted messages (got %d messages)", len(msgs))
	}
	if !foundAgent {
		t.Errorf("agent message not found in persisted messages (got %d messages)", len(msgs))
	}
}
