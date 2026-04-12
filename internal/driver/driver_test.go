package driver

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/khaiql/parley/internal/protocol"
)

// ---------------------------------------------------------------------------
// TestBuildSystemPrompt
// ---------------------------------------------------------------------------

func TestBuildSystemPrompt_ContainsTopic(t *testing.T) {
	cfg := AgentConfig{
		Name:      "Alice",
		Role:      "backend engineer",
		Directory: "/home/alice/repo",
		Topic:     "refactor the auth module",
		Participants: []protocol.Participant{
			{Name: "Alice", Role: "backend engineer", Directory: "/home/alice/repo"},
			{Name: "Bob", Role: "frontend engineer", Directory: "/home/bob/repo"},
		},
	}
	prompt := BuildSystemPrompt(cfg)
	if !strings.Contains(prompt, "refactor the auth module") {
		t.Errorf("expected system prompt to contain topic, got:\n%s", prompt)
	}
}

func TestBuildSystemPrompt_ContainsParticipantNames(t *testing.T) {
	cfg := AgentConfig{
		Name:      "Alice",
		Role:      "backend engineer",
		Directory: "/home/alice/repo",
		Topic:     "test topic",
		Participants: []protocol.Participant{
			{Name: "Alice", Role: "backend engineer", Directory: "/home/alice/repo"},
			{Name: "Bob", Role: "frontend engineer", Directory: "/home/bob/repo"},
			{Name: "Carol", Role: "QA", Directory: "/home/carol/repo"},
		},
	}
	prompt := BuildSystemPrompt(cfg)
	for _, name := range []string{"Alice", "Bob", "Carol"} {
		if !strings.Contains(prompt, name) {
			t.Errorf("expected system prompt to contain participant %q, got:\n%s", name, prompt)
		}
	}
}

func TestBuildSystemPrompt_ContainsRoleAndDirectory(t *testing.T) {
	cfg := AgentConfig{
		Name:      "Alice",
		Role:      "backend engineer",
		Directory: "/home/alice/special-dir",
		Topic:     "test topic",
		Participants: []protocol.Participant{
			{Name: "Alice", Role: "backend engineer", Directory: "/home/alice/special-dir"},
		},
	}
	prompt := BuildSystemPrompt(cfg)
	if !strings.Contains(prompt, "backend engineer") {
		t.Errorf("expected system prompt to contain role, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "/home/alice/special-dir") {
		t.Errorf("expected system prompt to contain directory, got:\n%s", prompt)
	}
}

func TestBuildSystemPrompt_ContainsGuidelines(t *testing.T) {
	cfg := AgentConfig{
		Name:      "Alice",
		Role:      "engineer",
		Directory: "/tmp",
		Topic:     "topic",
		Participants: []protocol.Participant{
			{Name: "Alice", Role: "engineer", Directory: "/tmp"},
		},
	}
	prompt := BuildSystemPrompt(cfg)
	guidelines := []string{
		"@-mentions",
		"parley",
		"ALWAYS respond",
		"Do NOT respond",
	}
	for _, g := range guidelines {
		if !strings.Contains(prompt, g) {
			t.Errorf("expected system prompt to contain guideline %q, got:\n%s", g, prompt)
		}
	}
}

func TestBuildSystemPrompt_IdentifiesAgent(t *testing.T) {
	cfg := AgentConfig{
		Name:      "DeepThought",
		Role:      "philosopher",
		Directory: "/tmp/dt",
		Topic:     "meaning of life",
		Participants: []protocol.Participant{
			{Name: "DeepThought", Role: "philosopher", Directory: "/tmp/dt"},
		},
	}
	prompt := BuildSystemPrompt(cfg)
	// The prompt must tell the agent who it is.
	if !strings.Contains(prompt, "YOU ARE") {
		t.Errorf("expected system prompt to identify the agent with 'YOU ARE', got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "DeepThought") {
		t.Errorf("expected system prompt to contain agent name 'DeepThought', got:\n%s", prompt)
	}
}

// ---------------------------------------------------------------------------
// TestBuildInputMessage
// ---------------------------------------------------------------------------

func TestBuildInputMessage_ValidJSON(t *testing.T) {
	msg := BuildInputMessage("hello world")
	var v interface{}
	if err := json.Unmarshal(msg, &v); err != nil {
		t.Fatalf("BuildInputMessage did not produce valid JSON: %v\nraw: %s", err, msg)
	}
}

func TestBuildInputMessage_NewlineTerminated(t *testing.T) {
	msg := BuildInputMessage("hello")
	if len(msg) == 0 || msg[len(msg)-1] != '\n' {
		t.Errorf("BuildInputMessage must end with newline, got: %q", msg)
	}
}

func TestBuildInputMessage_ContainsText(t *testing.T) {
	text := "this is the message content"
	msg := BuildInputMessage(text)
	if !strings.Contains(string(msg), text) {
		t.Errorf("BuildInputMessage must contain the text %q, got: %s", text, msg)
	}
}

func TestBuildInputMessage_Structure(t *testing.T) {
	msg := BuildInputMessage("ping")
	// Trim the trailing newline before unmarshalling into a strict struct.
	trimmed := strings.TrimRight(string(msg), "\n")

	type contentItem struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type innerMsg struct {
		Role    string        `json:"role"`
		Content []contentItem `json:"content"`
	}
	type envelope struct {
		Type    string   `json:"type"`
		Message innerMsg `json:"message"`
	}

	var env envelope
	if err := json.Unmarshal([]byte(trimmed), &env); err != nil {
		t.Fatalf("failed to unmarshal BuildInputMessage output: %v", err)
	}
	if env.Type != "user" {
		t.Errorf("expected type=user, got %q", env.Type)
	}
	if env.Message.Role != "user" {
		t.Errorf("expected message.role=user, got %q", env.Message.Role)
	}
	if len(env.Message.Content) == 0 {
		t.Fatal("expected at least one content item")
	}
	item := env.Message.Content[0]
	if item.Type != "text" {
		t.Errorf("expected content[0].type=text, got %q", item.Type)
	}
	if item.Text != "ping" {
		t.Errorf("expected content[0].text=ping, got %q", item.Text)
	}
}

// ---------------------------------------------------------------------------
// TestBuildArgs
// ---------------------------------------------------------------------------

func TestBuildArgs_ContainsRequiredFlags(t *testing.T) {
	cfg := AgentConfig{
		Name:         "Alice",
		Role:         "engineer",
		Directory:    "/tmp",
		Topic:        "topic",
		SystemPrompt: "you are helpful",
	}
	args := BuildArgs(cfg)

	required := []string{
		"-p",
		"--verbose",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--append-system-prompt",
	}

	for i := 0; i < len(required); i++ {
		flag := required[i]
		found := false
		for j, a := range args {
			if a == flag {
				// If next element is a value token, advance required pointer.
				if i+1 < len(required) && !strings.HasPrefix(required[i+1], "-") {
					if j+1 < len(args) && args[j+1] == required[i+1] {
						found = true
						i++ // consume the value from required too
						break
					}
				} else {
					found = true
					break
				}
			}
		}
		if !found {
			t.Errorf("expected args to contain %q; got: %v", flag, args)
		}
	}
}

func TestBuildArgs_SystemPromptFollowsFlag(t *testing.T) {
	cfg := AgentConfig{
		SystemPrompt: "MY CUSTOM PROMPT",
	}
	args := BuildArgs(cfg)
	for i, a := range args {
		if a == "--append-system-prompt" {
			if i+1 >= len(args) {
				t.Fatal("--append-system-prompt has no following value")
			}
			if args[i+1] != "MY CUSTOM PROMPT" {
				t.Errorf("expected system prompt value %q, got %q", "MY CUSTOM PROMPT", args[i+1])
			}
			return
		}
	}
	t.Error("--append-system-prompt flag not found in args")
}

func TestBuildArgs_ExtraArgsAppended(t *testing.T) {
	cfg := AgentConfig{
		Args:         []string{"--worktree", "--permission-mode", "acceptEdits"},
		SystemPrompt: "prompt",
	}
	args := BuildArgs(cfg)
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

func TestBuildArgs_ResumeSessionIDIncluded(t *testing.T) {
	cfg := AgentConfig{
		SystemPrompt:    "prompt",
		ResumeSessionID: "abc-session-123",
	}
	args := BuildArgs(cfg)

	// Find --resume flag followed by the session ID.
	found := false
	for i, a := range args {
		if a == "--resume" {
			if i+1 >= len(args) {
				t.Fatal("--resume flag has no following value")
			}
			if args[i+1] != "abc-session-123" {
				t.Errorf("expected --resume value %q, got %q", "abc-session-123", args[i+1])
			}
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected --resume flag in args: %v", args)
	}
}

func TestBuildArgs_NoResumeWhenEmpty(t *testing.T) {
	cfg := AgentConfig{
		SystemPrompt:    "prompt",
		ResumeSessionID: "",
	}
	args := BuildArgs(cfg)
	for _, a := range args {
		if a == "--resume" {
			t.Errorf("expected no --resume flag when ResumeSessionID is empty, got args: %v", args)
		}
	}
}

// ---------------------------------------------------------------------------
// TestParseAssistantEvent
// ---------------------------------------------------------------------------

func TestParseAssistantEvent_SkippedWithPartialMessages(t *testing.T) {
	// With --include-partial-messages, assistant events are skipped to avoid
	// double-rendering (stream_event deltas already provide the text).
	line := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Hello from agent"}]}}`
	_, ok := parseLine([]byte(line))
	if ok {
		t.Error("expected parseLine to skip assistant events (handled by stream_event instead)")
	}
}

// ---------------------------------------------------------------------------
// TestParseResultEvent
// ---------------------------------------------------------------------------

func TestParseResultEvent_EmitsDone(t *testing.T) {
	line := `{"type":"result","subtype":"success","session_id":"sess-123","result":"final text","num_turns":4,"total_cost_usd":0.01}`
	event, ok := parseLine([]byte(line))
	if !ok {
		t.Fatal("expected parseLine to return ok=true for result event")
	}
	if event.Type != EventDone {
		t.Errorf("expected EventDone, got %v", event.Type)
	}
}

func TestParseResultEvent_SessionIDExtracted(t *testing.T) {
	line := `{"type":"result","subtype":"success","session_id":"abc-456","result":"done","num_turns":1,"total_cost_usd":0.0}`
	d := &ClaudeDriver{}
	d.parseAndEmitLine([]byte(line), make(chan AgentEvent, 1))
	if d.sessionID != "abc-456" {
		t.Errorf("expected sessionID 'abc-456', got %q", d.sessionID)
	}
}

// ---------------------------------------------------------------------------
// TestParseSystemEvent
// ---------------------------------------------------------------------------

func TestParseSystemEvent_InitSkipped(t *testing.T) {
	line := `{"type":"system","subtype":"init","session_id":"init-session","tools":[],"model":"claude-opus-4-5"}`
	_, ok := parseLine([]byte(line))
	if ok {
		t.Error("expected parseLine to return ok=false for system init event (should be skipped)")
	}
}

func TestParseSystemEvent_HookSkipped(t *testing.T) {
	line := `{"type":"system","subtype":"hook_started","hook_type":"PreToolUse"}`
	_, ok := parseLine([]byte(line))
	if ok {
		t.Error("expected parseLine to return ok=false for system hook event")
	}
}

func TestParseSystemEvent_RateLimitSkipped(t *testing.T) {
	line := `{"type":"rate_limit_event","remaining_tokens":1000}`
	_, ok := parseLine([]byte(line))
	if ok {
		t.Error("expected parseLine to return ok=false for rate_limit_event")
	}
}

// ---------------------------------------------------------------------------
// TestParseStreamEvent (--include-partial-messages)
// ---------------------------------------------------------------------------

func TestParseStreamEvent_TextDelta(t *testing.T) {
	line := `{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}},"session_id":"s1"}`
	event, ok := parseLine([]byte(line))
	if !ok {
		t.Fatal("expected parseLine to return ok=true for text_delta")
	}
	if event.Type != EventText {
		t.Errorf("expected EventText, got %v", event.Type)
	}
	if event.Text != "Hello" {
		t.Errorf("expected text 'Hello', got %q", event.Text)
	}
}

func TestParseStreamEvent_ThinkingBlockStart(t *testing.T) {
	line := `{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}},"session_id":"s1"}`
	event, ok := parseLine([]byte(line))
	if !ok {
		t.Fatal("expected parseLine to return ok=true for thinking block start")
	}
	if event.Type != EventThinking {
		t.Errorf("expected EventThinking, got %v", event.Type)
	}
}

func TestParseStreamEvent_ToolUseBlockStart(t *testing.T) {
	line := `{"type":"stream_event","event":{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","name":"Bash"}},"session_id":"s1"}`
	event, ok := parseLine([]byte(line))
	if !ok {
		t.Fatal("expected parseLine to return ok=true for tool_use block start")
	}
	if event.Type != EventToolUse {
		t.Errorf("expected EventToolUse, got %v", event.Type)
	}
	if event.ToolName != "Bash" {
		t.Errorf("expected ToolName 'Bash', got %q", event.ToolName)
	}
}

func TestParseStreamEvent_ThinkingDeltaSkipped(t *testing.T) {
	line := `{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"let me think"}},"session_id":"s1"}`
	_, ok := parseLine([]byte(line))
	if ok {
		t.Error("expected parseLine to return ok=false for thinking_delta (already showed thinking status)")
	}
}

func TestParseStreamEvent_MessageStartSkipped(t *testing.T) {
	line := `{"type":"stream_event","event":{"type":"message_start","message":{"model":"claude","id":"msg1","type":"message","role":"assistant"}},"session_id":"s1"}`
	_, ok := parseLine([]byte(line))
	if ok {
		t.Error("expected parseLine to return ok=false for message_start")
	}
}

// ---------------------------------------------------------------------------
// TestStop_WaitsForReadLoop — Stop() must wait for readLoop to finish
// ---------------------------------------------------------------------------

// TestStop_WaitsForReadLoop starts a driver backed by a `cat` subprocess,
// calls Stop(), and verifies that the events channel is closed before Stop
// returns (i.e. readLoop has finished and there is no race).
func TestStop_WaitsForReadLoop(t *testing.T) {
	// Use a pipe-based driver rather than a real subprocess so the test is
	// hermetic: we control when stdout closes.
	pr, pw := io.Pipe()

	d := &ClaudeDriver{}
	d.events = make(chan AgentEvent, 64)

	// Simulate a started driver: launch readLoop manually.
	d.wg.Add(1)
	go d.readLoop(pr)

	// Writing a few bytes to pw then closing it mimics a process exiting.
	// Stop() should close pw (via stdin) and cancel, but here we just
	// exercise the wg.Wait() path directly by closing the pipe.
	pw.Close()

	// Stop() must not return until readLoop has finished.
	if err := d.Stop(); err != nil {
		t.Fatalf("Stop() returned error: %v", err)
	}

	// After Stop returns, the events channel MUST be closed (readLoop done).
	select {
	case _, ok := <-d.Events():
		if ok {
			t.Error("expected events channel to be closed after Stop(), but received a value")
		}
		// ok==false means channel is closed — correct
	default:
		t.Error("events channel is not closed after Stop() returned — readLoop is still running")
	}
}

// ---------------------------------------------------------------------------
// TestReadLoop — scanner buffer size
// ---------------------------------------------------------------------------

// makeResultLine returns a valid NDJSON "result" line whose total length
// exceeds targetLen bytes by padding the result field with extra data.
func makeResultLine(targetLen int) []byte {
	// Build a line of the form:
	//   {"type":"result","result":"<padding...>"}
	prefix := `{"type":"result","result":"`
	suffix := `"}`
	// How many padding bytes do we need?
	padding := targetLen - len(prefix) - len(suffix)
	if padding < 0 {
		padding = 0
	}
	buf := make([]byte, len(prefix)+padding+len(suffix))
	copy(buf, prefix)
	for i := len(prefix); i < len(prefix)+padding; i++ {
		buf[i] = 'x'
	}
	copy(buf[len(prefix)+padding:], suffix)
	return append(buf, '\n')
}

// TestReadLoop_DefaultBufferFailsOnLargeLine verifies that the *default*
// bufio.Scanner buffer (64 KB) cannot handle a line larger than 64 KB.
// This documents the bug we are fixing.
func TestReadLoop_DefaultBufferFailsOnLargeLine(t *testing.T) {
	const lineSize = 128 * 1024 // 128 KB — larger than default 64 KB scanner buffer

	pr, pw := io.Pipe()

	// Write one oversized line then close the writer.
	go func() {
		pw.Write(makeResultLine(lineSize))
		pw.Close()
	}()

	// Use a default-buffer scanner (no Buffer call) to confirm it fails.
	scanner := bufio.NewScanner(pr)
	// Read lines; on an oversized line the scanner reports an error.
	scanned := false
	for scanner.Scan() {
		scanned = true
	}
	err := scanner.Err()
	if scanned && err == nil {
		// If it somehow succeeded with the default buffer, skip rather than fail:
		// the test is documenting expected failure, not asserting a hard invariant
		// of the standard library.
		t.Skip("default scanner unexpectedly succeeded — skipping documentation test")
	}
	// Expected: either err != nil (token too long) or scanned == false.
	// Either outcome confirms the default buffer is insufficient.
	if err == nil && !scanned {
		t.Log("default scanner produced no error but also scanned nothing — line was silently dropped")
	}
}

// ---------------------------------------------------------------------------
// TestBuildSystemPrompt_ContainsPassInstruction
// ---------------------------------------------------------------------------

func TestBuildSystemPrompt_ContainsPassInstruction(t *testing.T) {
	cfg := AgentConfig{
		Name:      "Alice",
		Role:      "engineer",
		Directory: "/tmp",
		Topic:     "topic",
		Participants: []protocol.Participant{
			{Name: "Alice", Role: "engineer", Directory: "/tmp"},
		},
	}
	prompt := BuildSystemPrompt(cfg)
	if !strings.Contains(prompt, "[PASS]") {
		t.Errorf("expected system prompt to contain '[PASS]' instruction, got:\n%s", prompt)
	}
}

// ---------------------------------------------------------------------------
// TestReadLoop_OneMBBufferHandlesLargeLine verifies that readLoop with a 1 MB
// buffer correctly processes a >64 KB NDJSON line and emits an EventDone.
func TestReadLoop_OneMBBufferHandlesLargeLine(t *testing.T) {
	const lineSize = 128 * 1024 // 128 KB

	pr, pw := io.Pipe()

	d := &ClaudeDriver{}
	d.events = make(chan AgentEvent, 8)
	d.wg.Add(1) // required: readLoop calls wg.Done()

	go func() {
		pw.Write(makeResultLine(lineSize))
		pw.Close()
	}()

	// readLoop closes d.events when done; drain it.
	d.readLoop(pr)

	var got []AgentEvent
	for e := range d.events {
		got = append(got, e)
	}

	if len(got) == 0 {
		t.Fatal("expected at least one event from readLoop for a 128 KB result line, got none")
	}
	if got[0].Type != EventDone {
		t.Errorf("expected EventDone, got %v", got[0].Type)
	}
}

// ---------------------------------------------------------------------------
// TestFormatHistory
// ---------------------------------------------------------------------------

func makeMsg(from, source, text string) protocol.MessageParams {
	return protocol.MessageParams{
		From:   from,
		Source: source,
		Content: []protocol.Content{
			{Type: "text", Text: text},
		},
		Timestamp: time.Now(),
	}
}

func TestFormatHistory_EmptyForNoMessages(t *testing.T) {
	result := FormatHistory(nil)
	if result != "" {
		t.Errorf("expected empty string for nil messages, got %q", result)
	}
	result = FormatHistory([]protocol.MessageParams{})
	if result != "" {
		t.Errorf("expected empty string for empty messages, got %q", result)
	}
}

func TestFormatHistory_BasicFormat(t *testing.T) {
	msgs := []protocol.MessageParams{
		makeMsg("sle", "human", "I think we need a message queue"),
		makeMsg("Alice", "agent", "Agreed, Redis Streams would work"),
		makeMsg("sle", "human", "What about NATS?"),
	}
	result := FormatHistory(msgs)

	if !strings.Contains(result, "Here is the conversation so far:") {
		t.Errorf("expected header line, got:\n%s", result)
	}
	if !strings.Contains(result, "[sle]: I think we need a message queue") {
		t.Errorf("expected sle's first message, got:\n%s", result)
	}
	if !strings.Contains(result, "[Alice]: Agreed, Redis Streams would work") {
		t.Errorf("expected Alice's message, got:\n%s", result)
	}
	if !strings.Contains(result, "[sle]: What about NATS?") {
		t.Errorf("expected sle's second message, got:\n%s", result)
	}
	if !strings.Contains(result, "You are joining this conversation now.") {
		t.Errorf("expected closing context line, got:\n%s", result)
	}
}

func TestFormatHistory_SkipsSystemMessages(t *testing.T) {
	msgs := []protocol.MessageParams{
		makeMsg("sle", "human", "Hello everyone"),
		makeMsg("system", "system", "Alice has joined the room"),
		makeMsg("Alice", "agent", "Hi!"),
	}
	result := FormatHistory(msgs)

	if strings.Contains(result, "[system]") {
		t.Errorf("expected system messages to be skipped, got:\n%s", result)
	}
	if strings.Contains(result, "Alice has joined the room") {
		t.Errorf("expected system message text to be skipped, got:\n%s", result)
	}
	if !strings.Contains(result, "[sle]: Hello everyone") {
		t.Errorf("expected human message to be present, got:\n%s", result)
	}
	if !strings.Contains(result, "[Alice]: Hi!") {
		t.Errorf("expected agent message to be present, got:\n%s", result)
	}
}

func TestFormatHistory_LimitsToLast20Messages(t *testing.T) {
	// Create 25 messages
	msgs := make([]protocol.MessageParams, 25)
	for i := 0; i < 25; i++ {
		msgs[i] = makeMsg("user", "human", strings.Repeat("x", i+1))
	}
	result := FormatHistory(msgs)

	// The first 5 messages should NOT appear (messages 0-4 have text "x", "xx", ..., "xxxxx")
	// The last 20 messages (indices 5-24) should appear
	// Message at index 4 has text "xxxxx" (5 x's)
	if strings.Contains(result, "[user]: xxxxx\n") {
		// Check it's not in there as a standalone line (the 5th msg, index 4)
		// But "xxxxx" is a prefix of longer ones, so we check precisely
		lines := strings.Split(result, "\n")
		for _, line := range lines {
			if line == "[user]: xxxxx" {
				t.Errorf("expected message index 4 (5 x's) to be excluded as it's outside last 20, got line: %q", line)
			}
		}
	}

	// Message at index 5 has 6 x's — should be present
	if !strings.Contains(result, "[user]: xxxxxx") {
		t.Errorf("expected message index 5 (6 x's) to be present as it's within last 20, got:\n%s", result)
	}

	// Count non-header/footer lines to verify we have at most 20
	lines := strings.Split(strings.TrimSpace(result), "\n")
	msgLines := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "[") {
			msgLines++
		}
	}
	if msgLines > 20 {
		t.Errorf("expected at most 20 message lines, got %d", msgLines)
	}
}

func TestFormatHistory_SeparatorPresent(t *testing.T) {
	msgs := []protocol.MessageParams{
		makeMsg("Alice", "agent", "Hello"),
	}
	result := FormatHistory(msgs)
	if !strings.Contains(result, "---") {
		t.Errorf("expected separator '---' in output, got:\n%s", result)
	}
}

func TestBuildArgs_AutoApproveAddsFlag(t *testing.T) {
	cfg := AgentConfig{
		SystemPrompt: "prompt",
		AutoApprove:  true,
	}
	args := BuildArgs(cfg)

	found := false
	for _, a := range args {
		if a == "--dangerously-skip-permissions" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected --dangerously-skip-permissions when AutoApprove is true, got: %v", args)
	}
}

func TestBuildArgs_NoAutoApproveByDefault(t *testing.T) {
	cfg := AgentConfig{
		SystemPrompt: "prompt",
		AutoApprove:  false,
	}
	args := BuildArgs(cfg)

	for _, a := range args {
		if a == "--dangerously-skip-permissions" {
			t.Errorf("expected no --dangerously-skip-permissions when AutoApprove is false, got: %v", args)
		}
	}
}
