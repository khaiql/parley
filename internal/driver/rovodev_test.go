package driver

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/khaiql/parley/internal/protocol"
)

func TestHandleSSEEvent_PartStart_Thinking(t *testing.T) {
	d := &RovodevDriver{events: make(chan AgentEvent, 10)}

	data := `{"part":{"part_kind":"thinking","content":""}}`
	d.handleSSEEvent("part_start", data)

	select {
	case e := <-d.events:
		if e.Type != EventThinking {
			t.Errorf("expected EventThinking, got %v", e.Type)
		}
	default:
		t.Fatal("expected an event, got none")
	}
}

func TestHandleSSEEvent_PartStart_Text(t *testing.T) {
	d := &RovodevDriver{events: make(chan AgentEvent, 10)}

	data := `{"part":{"part_kind":"text","content":"hello world"}}`
	d.handleSSEEvent("part_start", data)

	select {
	case e := <-d.events:
		if e.Type != EventText {
			t.Errorf("expected EventText, got %v", e.Type)
		}
		if e.Text != "hello world" {
			t.Errorf("expected text 'hello world', got %q", e.Text)
		}
	default:
		t.Fatal("expected an event, got none")
	}
}

func TestHandleSSEEvent_PartStart_ToolCall(t *testing.T) {
	d := &RovodevDriver{events: make(chan AgentEvent, 10)}

	data := `{"part":{"part_kind":"tool-call","tool_name":"bash"}}`
	d.handleSSEEvent("part_start", data)

	select {
	case e := <-d.events:
		if e.Type != EventToolUse {
			t.Errorf("expected EventToolUse, got %v", e.Type)
		}
		if e.ToolName != "bash" {
			t.Errorf("expected tool_name 'bash', got %q", e.ToolName)
		}
	default:
		t.Fatal("expected an event, got none")
	}
}

func TestHandleSSEEvent_PartDelta_Text(t *testing.T) {
	d := &RovodevDriver{events: make(chan AgentEvent, 10)}

	data := `{"delta":{"part_delta_kind":"text","content_delta":"chunk"}}`
	d.handleSSEEvent("part_delta", data)

	select {
	case e := <-d.events:
		if e.Type != EventText {
			t.Errorf("expected EventText, got %v", e.Type)
		}
		if e.Text != "chunk" {
			t.Errorf("expected text 'chunk', got %q", e.Text)
		}
	default:
		t.Fatal("expected an event, got none")
	}
}

func TestHandleSSEEvent_PartDelta_EmptyIgnored(t *testing.T) {
	d := &RovodevDriver{events: make(chan AgentEvent, 10)}

	data := `{"delta":{"part_delta_kind":"text","content_delta":""}}`
	d.handleSSEEvent("part_delta", data)

	select {
	case e := <-d.events:
		t.Errorf("expected no event for empty delta, got %v", e)
	default:
	}
}

func TestHandleSSEEvent_Close(t *testing.T) {
	d := &RovodevDriver{events: make(chan AgentEvent, 10)}

	d.handleSSEEvent("close", "")

	select {
	case e := <-d.events:
		if e.Type != EventDone {
			t.Errorf("expected EventDone, got %v", e.Type)
		}
	default:
		t.Fatal("expected an event, got none")
	}
}

func TestHandleSSEEvent_Exception(t *testing.T) {
	d := &RovodevDriver{events: make(chan AgentEvent, 10)}

	d.handleSSEEvent("exception", `{"error":"something broke"}`)

	select {
	case e := <-d.events:
		if e.Type != EventError {
			t.Errorf("expected EventError, got %v", e.Type)
		}
		if e.Text == "" {
			t.Error("expected non-empty error text")
		}
	default:
		t.Fatal("expected an event, got none")
	}
}

func TestHandleSSEEvent_OnCallToolsStart(t *testing.T) {
	d := &RovodevDriver{events: make(chan AgentEvent, 10)}

	data := `{"tool_calls":[{"tool_name":"create_file"},{"tool_name":"bash"}]}`
	d.handleSSEEvent("on_call_tools_start", data)

	tools := []string{"create_file", "bash"}
	for _, want := range tools {
		select {
		case e := <-d.events:
			if e.Type != EventToolUse {
				t.Errorf("expected EventToolUse, got %v", e.Type)
			}
			if e.ToolName != want {
				t.Errorf("expected tool_name %q, got %q", want, e.ToolName)
			}
		default:
			t.Fatalf("expected event for tool %q, got none", want)
		}
	}
}

func TestHandleSSEEvent_UnknownIgnored(t *testing.T) {
	d := &RovodevDriver{events: make(chan AgentEvent, 10)}

	d.handleSSEEvent("user-prompt", `{}`)
	d.handleSSEEvent("request-usage", `{}`)

	select {
	case e := <-d.events:
		t.Errorf("expected no event for unknown SSE types, got %v", e)
	default:
	}
}

func TestHandleSSEEvent_MalformedJSON(t *testing.T) {
	d := &RovodevDriver{events: make(chan AgentEvent, 10)}

	d.handleSSEEvent("part_start", "not json")
	d.handleSSEEvent("part_delta", "{broken")

	select {
	case e := <-d.events:
		t.Errorf("expected no event for malformed JSON, got %v", e)
	default:
	}
}

func TestReadSSE_DoesNotCloseChannelOnStreamEnd(t *testing.T) {
	d := &RovodevDriver{events: make(chan AgentEvent, 10)}
	d.wg.Add(1)

	stream := "event: part_start\ndata: {\"part\":{\"part_kind\":\"text\",\"content\":\"hi\"}}\n\n"
	body := io.NopCloser(strings.NewReader(stream))

	go d.readSSE(body)
	d.wg.Wait()

	select {
	case _, ok := <-d.events:
		if !ok {
			t.Fatal("events channel should remain open until Stop()")
		}
	default:
	}

	if err := d.Stop(); err != nil {
		t.Fatalf("Stop() returned error: %v", err)
	}

	select {
	case _, ok := <-d.events:
		if ok {
			t.Fatal("events channel should be closed after Stop()")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("events channel was not closed by Stop()")
	}
}

func TestStop_ClosesChannelWithBufferedEvents(t *testing.T) {
	d := &RovodevDriver{events: make(chan AgentEvent, 64)}

	d.events <- AgentEvent{Type: EventText, Text: "a"}
	d.events <- AgentEvent{Type: EventText, Text: "b"}
	d.events <- AgentEvent{Type: EventText, Text: "c"}

	if err := d.Stop(); err != nil {
		t.Fatalf("Stop() returned error: %v", err)
	}

	var collected []AgentEvent
	for e := range d.events {
		collected = append(collected, e)
	}
	if len(collected) != 3 {
		t.Errorf("expected 3 buffered events, got %d", len(collected))
	}
}

func TestCloseEvents_Idempotent(t *testing.T) {
	d := &RovodevDriver{events: make(chan AgentEvent, 10)}

	d.closeEvents()
	d.closeEvents()

	select {
	case _, ok := <-d.events:
		if ok {
			t.Error("expected channel to be closed")
		}
	default:
		t.Error("expected channel to be closed and readable, not blocking")
	}
}

func TestDefaultCommand(t *testing.T) {
	tests := []struct {
		agentType string
		want      string
	}{
		{"claude", "claude"},
		{"gemini", "gemini"},
		{"rovodev", "acli"},
		{"codex", "claude"},
		{"", "claude"},
	}
	for _, tt := range tests {
		if got := protocol.DefaultCommand(tt.agentType); got != tt.want {
			t.Errorf("DefaultCommand(%q) = %q, want %q", tt.agentType, got, tt.want)
		}
	}
}

func TestDefaultArgs(t *testing.T) {
	rovodevArgs := protocol.DefaultArgs("rovodev")
	if len(rovodevArgs) != 0 {
		t.Errorf("DefaultArgs(rovodev) = %v, want []", rovodevArgs)
	}

	mixedCaseArgs := protocol.DefaultArgs("RoVoDeV")
	if len(mixedCaseArgs) != 0 {
		t.Errorf("DefaultArgs(RoVoDeV) = %v, want []", mixedCaseArgs)
	}

	claudeArgs := protocol.DefaultArgs("claude")
	if len(claudeArgs) != 0 {
		t.Errorf("DefaultArgs(claude) = %v, want []", claudeArgs)
	}
}

func TestFreePort(t *testing.T) {
	port, err := freePort()
	if err != nil {
		t.Fatalf("freePort() returned error: %v", err)
	}
	if port <= 0 {
		t.Errorf("freePort() returned invalid port: %d", port)
	}
}

func TestRovodevPartStructParsing(t *testing.T) {
	data := `{"part":{"part_kind":"text","content":"hi","tool_name":""}}`
	var p rovodevPart
	if err := json.Unmarshal([]byte(data), &p); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if p.Part.PartKind != "text" {
		t.Errorf("expected part_kind 'text', got %q", p.Part.PartKind)
	}
	if p.Part.Content != "hi" {
		t.Errorf("expected content 'hi', got %q", p.Part.Content)
	}
}

func TestRovodevDeltaStructParsing(t *testing.T) {
	data := `{"delta":{"part_delta_kind":"text","content_delta":"hello"}}`
	var d rovodevDelta
	if err := json.Unmarshal([]byte(data), &d); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if d.Delta.PartDeltaKind != "text" {
		t.Errorf("expected part_delta_kind 'text', got %q", d.Delta.PartDeltaKind)
	}
	if d.Delta.ContentDelta != "hello" {
		t.Errorf("expected content_delta 'hello', got %q", d.Delta.ContentDelta)
	}
}
