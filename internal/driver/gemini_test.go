package driver

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// TestBuildGeminiArgs
// ---------------------------------------------------------------------------

func TestBuildGeminiArgs_ContainsRequiredFlags(t *testing.T) {
	cfg := AgentConfig{
		Name:         "Gemini",
		Role:         "engineer",
		Directory:    "/tmp",
		Topic:        "topic",
		SystemPrompt: "you are helpful",
	}
	args := BuildGeminiArgs(cfg, "hello")

	// Check that -o stream-json and --yolo are present.
	requiredFlags := []string{"-o", "--yolo"}
	for _, flag := range requiredFlags {
		found := false
		for _, a := range args {
			if a == flag {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected args to contain %q; got: %v", flag, args)
		}
	}

	// Check -o stream-json value pair.
	for i, a := range args {
		if a == "-o" {
			if i+1 >= len(args) || args[i+1] != "stream-json" {
				t.Errorf("expected -o to be followed by stream-json, got: %v", args)
			}
			break
		}
	}

	// -p must be present and its value must contain the message.
	found := false
	for i, a := range args {
		if a == "-p" {
			found = true
			if i+1 >= len(args) {
				t.Fatal("-p flag has no value")
			}
			if !strings.Contains(args[i+1], "hello") {
				t.Errorf("expected -p value to contain 'hello', got: %q", args[i+1])
			}
			break
		}
	}
	if !found {
		t.Errorf("expected args to contain -p; got: %v", args)
	}
}

func TestBuildGeminiArgs_MessageIsPromptValue(t *testing.T) {
	cfg := AgentConfig{SystemPrompt: "sys"}
	args := BuildGeminiArgs(cfg, "do the thing")
	for i, a := range args {
		if a == "-p" {
			if i+1 >= len(args) {
				t.Fatal("-p flag has no value")
			}
			if !strings.Contains(args[i+1], "do the thing") {
				t.Errorf("expected -p value to contain message, got: %q", args[i+1])
			}
			return
		}
	}
	t.Error("-p flag not found in args")
}

func TestBuildGeminiArgs_SystemPromptPrependedToMessage(t *testing.T) {
	cfg := AgentConfig{SystemPrompt: "YOU ARE A ROBOT"}
	args := BuildGeminiArgs(cfg, "say hi")
	for i, a := range args {
		if a == "-p" {
			if i+1 >= len(args) {
				t.Fatal("-p flag has no value")
			}
			val := args[i+1]
			if !strings.Contains(val, "YOU ARE A ROBOT") {
				t.Errorf("expected -p value to contain system prompt, got: %q", val)
			}
			if !strings.Contains(val, "say hi") {
				t.Errorf("expected -p value to contain message, got: %q", val)
			}
			return
		}
	}
	t.Error("-p flag not found in args")
}

func TestBuildGeminiArgs_NoSystemPrompt(t *testing.T) {
	cfg := AgentConfig{}
	args := BuildGeminiArgs(cfg, "ping")
	for i, a := range args {
		if a == "-p" {
			if i+1 >= len(args) {
				t.Fatal("-p flag has no value")
			}
			if args[i+1] != "ping" {
				t.Errorf("expected -p value to be exactly %q, got %q", "ping", args[i+1])
			}
			return
		}
	}
	t.Error("-p flag not found in args")
}

func TestBuildGeminiArgs_ExtraArgsAppended(t *testing.T) {
	cfg := AgentConfig{
		Args: []string{"--model", "gemini-pro"},
	}
	args := BuildGeminiArgs(cfg, "test")
	for _, extra := range cfg.Args {
		found := false
		for _, a := range args {
			if a == extra {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected extra arg %q to be present in args: %v", extra, args)
		}
	}
}

func TestBuildGeminiArgs_ResumeFlag(t *testing.T) {
	cfg := AgentConfig{}
	args := BuildGeminiArgsWithResume(cfg, "follow-up", "5")
	found := false
	for i, a := range args {
		if a == "--resume" {
			found = true
			if i+1 >= len(args) {
				t.Fatal("--resume flag has no value")
			}
			if args[i+1] != "5" {
				t.Errorf("expected --resume value '5', got %q", args[i+1])
			}
			break
		}
	}
	if !found {
		t.Errorf("expected --resume flag in args: %v", args)
	}
}

func TestBuildGeminiArgs_NoResumeFlagOnFirstMessage(t *testing.T) {
	cfg := AgentConfig{}
	args := BuildGeminiArgs(cfg, "first message")
	for _, a := range args {
		if a == "--resume" {
			t.Errorf("expected no --resume flag in initial args: %v", args)
		}
	}
}

// ---------------------------------------------------------------------------
// TestParseGeminiLine
// ---------------------------------------------------------------------------

func TestParseGeminiLine_InitSkipped(t *testing.T) {
	line := `{"type":"init","timestamp":"2026-03-31T14:37:05.616Z","session_id":"abc-123","model":"gemini-3"}`
	event, ok := parseGeminiLine([]byte(line))
	if ok {
		t.Errorf("expected init event to be skipped, got event: %+v", event)
	}
}

func TestParseGeminiLine_UserMessageSkipped(t *testing.T) {
	line := `{"type":"message","timestamp":"2026-03-31T14:37:05.617Z","role":"user","content":"say hi"}`
	_, ok := parseGeminiLine([]byte(line))
	if ok {
		t.Error("expected user message to be skipped")
	}
}

func TestParseGeminiLine_AssistantTextDelta(t *testing.T) {
	line := `{"type":"message","timestamp":"2026-03-31T14:37:54.906Z","role":"assistant","content":"Hello World","delta":true}`
	event, ok := parseGeminiLine([]byte(line))
	if !ok {
		t.Fatal("expected assistant delta to produce an event")
	}
	if event.Type != EventText {
		t.Errorf("expected EventText, got %v", event.Type)
	}
	if event.Text != "Hello World" {
		t.Errorf("expected text 'Hello World', got %q", event.Text)
	}
}

func TestParseGeminiLine_AssistantFullMessage(t *testing.T) {
	// Non-delta assistant messages should also produce text.
	line := `{"type":"message","role":"assistant","content":"Full response"}`
	event, ok := parseGeminiLine([]byte(line))
	if !ok {
		t.Fatal("expected assistant full message to produce an event")
	}
	if event.Type != EventText {
		t.Errorf("expected EventText, got %v", event.Type)
	}
	if event.Text != "Full response" {
		t.Errorf("expected text 'Full response', got %q", event.Text)
	}
}

func TestParseGeminiLine_ToolUse(t *testing.T) {
	line := `{"type":"tool_use","timestamp":"2026-03-31T14:36:19.507Z","tool_name":"activate_skill","tool_id":"act_1","parameters":{"name":"using-superpowers"}}`
	event, ok := parseGeminiLine([]byte(line))
	if !ok {
		t.Fatal("expected tool_use to produce an event")
	}
	if event.Type != EventToolUse {
		t.Errorf("expected EventToolUse, got %v", event.Type)
	}
	if event.ToolName != "activate_skill" {
		t.Errorf("expected ToolName 'activate_skill', got %q", event.ToolName)
	}
}

func TestParseGeminiLine_ToolResultSkipped(t *testing.T) {
	line := `{"type":"tool_result","timestamp":"2026-03-31T14:36:19.552Z","tool_id":"act_1","status":"success","output":"done"}`
	_, ok := parseGeminiLine([]byte(line))
	if ok {
		t.Error("expected tool_result to be skipped")
	}
}

func TestParseGeminiLine_ResultEmitsDone(t *testing.T) {
	line := `{"type":"result","timestamp":"2026-03-31T14:37:54.959Z","status":"success","stats":{"total_tokens":12098}}`
	event, ok := parseGeminiLine([]byte(line))
	if !ok {
		t.Fatal("expected result event to produce an event")
	}
	if event.Type != EventDone {
		t.Errorf("expected EventDone, got %v", event.Type)
	}
}

func TestParseGeminiLine_InvalidJSONSkipped(t *testing.T) {
	line := `not valid json`
	_, ok := parseGeminiLine([]byte(line))
	if ok {
		t.Error("expected invalid JSON to be skipped")
	}
}

func TestParseGeminiLine_EmptyLineSkipped(t *testing.T) {
	_, ok := parseGeminiLine([]byte(""))
	if ok {
		t.Error("expected empty line to be skipped")
	}
}

// ---------------------------------------------------------------------------
// TestExtractGeminiSessionIndex
// ---------------------------------------------------------------------------

func TestExtractGeminiSessionIndex_FromInitEvent(t *testing.T) {
	// Simulate parsing the init event to extract session index.
	// The session_id from --list-sessions is an index; we capture it from
	// the init event's session_id field.
	line := `{"type":"init","session_id":"abc-123","model":"gemini-3"}`
	sessionID := extractGeminiSessionID([]byte(line))
	if sessionID != "abc-123" {
		t.Errorf("expected session ID 'abc-123', got %q", sessionID)
	}
}

func TestExtractGeminiSessionIndex_FromNonInitEvent(t *testing.T) {
	line := `{"type":"result","status":"success"}`
	sessionID := extractGeminiSessionID([]byte(line))
	if sessionID != "" {
		t.Errorf("expected empty session ID from non-init event, got %q", sessionID)
	}
}

// ---------------------------------------------------------------------------
// TestGeminiDriver Start/Send lifecycle
// ---------------------------------------------------------------------------

func TestGeminiDriver_StartWaitsForSessionID(t *testing.T) {
	// Create a script that emits init (with session_id) then a result event.
	// This simulates what the real gemini CLI does.
	script := `#!/bin/sh
echo '{"type":"init","session_id":"test-session-42","model":"gemini-3"}'
echo '{"type":"message","role":"assistant","content":"hello","delta":true}'
echo '{"type":"result","status":"success","stats":{}}'
`
	scriptPath := t.TempDir() + "/fake-gemini.sh"
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	d := &GeminiDriver{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := d.Start(ctx, AgentConfig{
		Command: scriptPath,
		Name:    "Test",
		Role:    "tester",
		Topic:   "test",
	})
	if err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer d.Stop()

	// After Start returns, session ID must be set.
	sid := d.SessionID()
	if sid != "test-session-42" {
		t.Errorf("expected session ID 'test-session-42', got %q", sid)
	}
}

func TestGeminiDriver_SendAfterStart(t *testing.T) {
	// Script that handles both initial and resumed invocations.
	// It emits an init event with session_id, then responds.
	script := `#!/bin/sh
echo '{"type":"init","session_id":"sess-1","model":"gemini-3"}'
echo '{"type":"message","role":"assistant","content":"response","delta":true}'
echo '{"type":"result","status":"success","stats":{}}'
`
	scriptPath := t.TempDir() + "/fake-gemini.sh"
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	d := &GeminiDriver{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := d.Start(ctx, AgentConfig{
		Command: scriptPath,
		Name:    "Test",
		Role:    "tester",
		Topic:   "test",
	})
	if err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer d.Stop()

	// Drain events from Start's initial invocation.
	drainEvents(d.Events(), 3, time.Second)

	// Send should not error since session is established.
	err = d.Send("hello")
	if err != nil {
		t.Fatalf("Send() failed: %v", err)
	}
}

func drainEvents(ch <-chan AgentEvent, max int, timeout time.Duration) {
	deadline := time.After(timeout)
	for i := 0; i < max; i++ {
		select {
		case <-ch:
		case <-deadline:
			return
		}
	}
}
