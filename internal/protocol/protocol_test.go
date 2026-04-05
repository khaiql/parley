package protocol_test

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/khaiql/parley/internal/protocol"
)

func TestEncodeLineEndsWithNewline(t *testing.T) {
	n := protocol.NewNotification("chat/message", protocol.MessageParams{
		ID:   "msg-1",
		Seq:  1,
		From: "alice",
		Role: "user",
		Content: []protocol.Content{
			{Type: "text", Text: "hello"},
		},
	})

	data, err := protocol.EncodeLine(n)
	if err != nil {
		t.Fatalf("EncodeLine error: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("EncodeLine returned empty data")
	}
	if data[len(data)-1] != '\n' {
		t.Errorf("EncodeLine output does not end with newline; got %q", data[len(data)-1])
	}
}

func TestDecodeLineParses(t *testing.T) {
	raw := []byte(`{"jsonrpc":"2.0","method":"chat/message","params":{"id":"m1","seq":1,"from":"bob","role":"user","content":[]}}` + "\n")

	msg, err := protocol.DecodeLine(raw)
	if err != nil {
		t.Fatalf("DecodeLine error: %v", err)
	}
	if msg.JSONRPC != "2.0" {
		t.Errorf("expected jsonrpc 2.0, got %q", msg.JSONRPC)
	}
	if msg.Method != "chat/message" {
		t.Errorf("expected method chat/message, got %q", msg.Method)
	}
	if msg.Params == nil {
		t.Error("expected params to be non-nil")
	}
}

func TestNotificationRoundTrip(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	params := protocol.MessageParams{
		ID:        "msg-42",
		Seq:       42,
		From:      "agent-1",
		Source:    "agent",
		Role:      "assistant",
		Timestamp: now,
		Mentions:  []string{"agent-2"},
		Content: []protocol.Content{
			{Type: "text", Text: "hello world"},
		},
	}

	n := protocol.NewNotification("chat/message", params)
	if n.JSONRPC != "2.0" {
		t.Errorf("expected jsonrpc 2.0, got %q", n.JSONRPC)
	}
	if n.Method != "chat/message" {
		t.Errorf("expected method chat/message, got %q", n.Method)
	}

	data, err := protocol.EncodeLine(n)
	if err != nil {
		t.Fatalf("EncodeLine error: %v", err)
	}

	// strip trailing newline for unmarshal
	line := bytes.TrimRight(data, "\n")
	var decoded protocol.Notification
	if err := json.Unmarshal(line, &decoded); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if decoded.Method != "chat/message" {
		t.Errorf("round-trip method mismatch: got %q", decoded.Method)
	}

	// Decode the params
	var decodedParams protocol.MessageParams
	if err := json.Unmarshal(decoded.Params, &decodedParams); err != nil {
		t.Fatalf("Unmarshal params error: %v", err)
	}
	if decodedParams.ID != "msg-42" {
		t.Errorf("params ID mismatch: got %q", decodedParams.ID)
	}
	if decodedParams.Seq != 42 {
		t.Errorf("params Seq mismatch: got %d", decodedParams.Seq)
	}
	if decodedParams.From != "agent-1" {
		t.Errorf("params From mismatch: got %q", decodedParams.From)
	}
	if len(decodedParams.Content) != 1 || decodedParams.Content[0].Text != "hello world" {
		t.Errorf("params Content mismatch: %+v", decodedParams.Content)
	}
	if len(decodedParams.Mentions) != 1 || decodedParams.Mentions[0] != "agent-2" {
		t.Errorf("params Mentions mismatch: %+v", decodedParams.Mentions)
	}
}

// ---------------------------------------------------------------------------
// TestParseMentions
// ---------------------------------------------------------------------------

func TestParseMentions_ExtractsAtMentions(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"no mentions", "hello world", nil},
		{"single mention", "@alice hello", []string{"alice"}},
		{"multiple mentions", "@alice and @bob should look", []string{"alice", "bob"}},
		{"bare at sign skipped", "@ hello", nil},
		{"mention with punctuation attached", "@alice: hello", []string{"alice:"}},
		{"empty string", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := protocol.ParseMentions(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("ParseMentions(%q) = %v, want %v", tt.input, got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("ParseMentions(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestStatusParamsEncodeDecodeRoundTrip(t *testing.T) {
	params := protocol.StatusParams{
		Name:   "bot1",
		Status: "thinking…",
	}

	n := protocol.NewNotification("room.status", params)
	data, err := protocol.EncodeLine(n)
	if err != nil {
		t.Fatalf("EncodeLine error: %v", err)
	}

	msg, err := protocol.DecodeLine(data)
	if err != nil {
		t.Fatalf("DecodeLine error: %v", err)
	}
	if msg.Method != "room.status" {
		t.Errorf("method mismatch: got %q", msg.Method)
	}

	var decoded protocol.StatusParams
	if err := json.Unmarshal(msg.Params, &decoded); err != nil {
		t.Fatalf("Unmarshal StatusParams error: %v", err)
	}
	if decoded.Name != "bot1" {
		t.Errorf("Name mismatch: got %q", decoded.Name)
	}
	if decoded.Status != "thinking…" {
		t.Errorf("Status mismatch: got %q", decoded.Status)
	}
}

func TestStatusParamsEmptyStatus(t *testing.T) {
	params := protocol.StatusParams{Name: "bot1", Status: ""}
	n := protocol.NewNotification("room.status", params)
	data, err := protocol.EncodeLine(n)
	if err != nil {
		t.Fatalf("EncodeLine error: %v", err)
	}
	msg, err := protocol.DecodeLine(data)
	if err != nil {
		t.Fatalf("DecodeLine error: %v", err)
	}
	var decoded protocol.StatusParams
	if err := json.Unmarshal(msg.Params, &decoded); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if decoded.Status != "" {
		t.Errorf("expected empty status, got %q", decoded.Status)
	}
}

func TestParticipantColorIndexRoundTrip(t *testing.T) {
	p := protocol.Participant{Name: "cosmo", ColorIndex: 5}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got protocol.Participant
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ColorIndex != 5 {
		t.Errorf("ColorIndex = %d, want 5", got.ColorIndex)
	}
}

func TestJoinedParamsColorIndexRoundTrip(t *testing.T) {
	jp := protocol.JoinedParams{Name: "cosmo", ColorIndex: 3}
	b, err := json.Marshal(jp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got protocol.JoinedParams
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ColorIndex != 3 {
		t.Errorf("ColorIndex = %d, want 3", got.ColorIndex)
	}
}

func TestJoinParamsEncodeDecodeRoundTrip(t *testing.T) {
	params := protocol.JoinParams{
		Name:      "agent-x",
		Role:      "assistant",
		Directory: "/workspace/project",
		Repo:      "github.com/example/repo",
		AgentType: "claude",
	}

	n := protocol.NewNotification("room/join", params)
	data, err := protocol.EncodeLine(n)
	if err != nil {
		t.Fatalf("EncodeLine error: %v", err)
	}

	msg, err := protocol.DecodeLine(data)
	if err != nil {
		t.Fatalf("DecodeLine error: %v", err)
	}
	if msg.Method != "room/join" {
		t.Errorf("method mismatch: got %q", msg.Method)
	}

	var decoded protocol.JoinParams
	if err := json.Unmarshal(msg.Params, &decoded); err != nil {
		t.Fatalf("Unmarshal JoinParams error: %v", err)
	}
	if decoded.Name != "agent-x" {
		t.Errorf("Name mismatch: got %q", decoded.Name)
	}
	if decoded.Role != "assistant" {
		t.Errorf("Role mismatch: got %q", decoded.Role)
	}
	if decoded.Directory != "/workspace/project" {
		t.Errorf("Directory mismatch: got %q", decoded.Directory)
	}
	if decoded.Repo != "github.com/example/repo" {
		t.Errorf("Repo mismatch: got %q", decoded.Repo)
	}
	if decoded.AgentType != "claude" {
		t.Errorf("AgentType mismatch: got %q", decoded.AgentType)
	}
}
