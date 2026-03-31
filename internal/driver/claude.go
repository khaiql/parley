package driver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
)

// ---------------------------------------------------------------------------
// ClaudeDriver — AgentDriver implementation for Claude Code
// ---------------------------------------------------------------------------

// ClaudeDriver drives a long-lived `claude` subprocess using stream-json I/O.
type ClaudeDriver struct {
	mu        sync.Mutex
	wg        sync.WaitGroup
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	events    chan AgentEvent
	cancel    context.CancelFunc
	sessionID string
}

// Start spawns the claude subprocess and begins streaming events.
func (d *ClaudeDriver) Start(ctx context.Context, config AgentConfig) error {
	ctx, cancel := context.WithCancel(ctx)
	d.cancel = cancel

	if config.SystemPrompt == "" {
		config.SystemPrompt = BuildSystemPrompt(config)
	}

	command := config.Command
	if command == "" {
		command = "claude"
	}

	args := BuildArgs(config)
	d.cmd = exec.CommandContext(ctx, command, args...)

	stdin, err := d.cmd.StdinPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("driver: stdin pipe: %w", err)
	}
	d.stdin = stdin

	stdout, err := d.cmd.StdoutPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("driver: stdout pipe: %w", err)
	}

	d.events = make(chan AgentEvent, 64)

	if err := d.cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("driver: start process: %w", err)
	}

	d.wg.Add(1)
	go d.readLoop(stdout)

	// Send initial message to prompt the agent to introduce itself.
	intro := fmt.Sprintf("You have joined a parley chat room. The topic is: %s. Briefly introduce yourself and ask how you can help.", config.Topic)
	if err := d.Send(intro); err != nil {
		return fmt.Errorf("driver: send initial prompt: %w", err)
	}

	return nil
}

// Send writes a chat message to the agent's stdin.
func (d *ClaudeDriver) Send(text string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stdin == nil {
		return fmt.Errorf("driver: not started")
	}
	msg := BuildInputMessage(text)
	_, err := d.stdin.Write(msg)
	return err
}

// Events returns the channel on which AgentEvents are delivered.
func (d *ClaudeDriver) Events() <-chan AgentEvent {
	return d.events
}

// Stop terminates the agent process and waits for readLoop to finish.
// It closes stdin first to signal the subprocess to finish, then cancels
// the context (which sends SIGKILL via CommandContext), and finally waits
// for readLoop to exit so callers know the events channel is closed.
func (d *ClaudeDriver) Stop() error {
	// Close stdin to signal the subprocess that no more input is coming.
	if d.stdin != nil {
		_ = d.stdin.Close()
	}
	// Cancel context — kills the process via CommandContext.
	if d.cancel != nil {
		d.cancel()
	}
	// Wait for readLoop to finish. This guarantees the events channel is
	// closed before Stop returns, eliminating the race condition.
	d.wg.Wait()
	return nil
}

// readLoop reads stdout line by line and emits AgentEvents.
// The scanner buffer is set to 1 MB to match the server limit and to handle
// large tool results that would otherwise silently truncate the event stream.
func (d *ClaudeDriver) readLoop(r io.Reader) {
	defer d.wg.Done()
	defer close(d.events)
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		d.parseAndEmitLine(line, d.events)
	}
}

// parseAndEmitLine parses a single NDJSON line and sends an event if applicable.
// It is exported for test access (lower-case within package).
func (d *ClaudeDriver) parseAndEmitLine(line []byte, ch chan<- AgentEvent) {
	event, ok := parseLine(line)
	if !ok {
		// Check if it's a result event to capture session_id even when we skip.
		// parseLine already handles this for EventDone, but we also need to
		// capture session_id here.
		var raw claudeRawEvent
		if err := json.Unmarshal(line, &raw); err == nil && raw.Type == "result" {
			d.mu.Lock()
			d.sessionID = raw.SessionID
			d.mu.Unlock()
			// Still emit the done event.
			ch <- AgentEvent{Type: EventDone}
		}
		return
	}
	// Capture session_id from result events.
	if event.Type == EventDone {
		var raw claudeRawEvent
		if err := json.Unmarshal(line, &raw); err == nil {
			d.mu.Lock()
			d.sessionID = raw.SessionID
			d.mu.Unlock()
		}
	}
	ch <- event
}

// SessionID returns the most recently captured session_id (from result events).
func (d *ClaudeDriver) SessionID() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.sessionID
}

// ---------------------------------------------------------------------------
// Parsing helpers
// ---------------------------------------------------------------------------

// claudeRawEvent is the minimal shape of any Claude Code stream-json line.
type claudeRawEvent struct {
	Type      string          `json:"type"`
	Subtype   string          `json:"subtype,omitempty"`
	SessionID string          `json:"session_id,omitempty"`
	Message   json.RawMessage `json:"message,omitempty"`
	Result    string          `json:"result,omitempty"`
	Event     json.RawMessage `json:"event,omitempty"` // for stream_event
}

// claudeContentItem represents one item in message.content[].
type claudeContentItem struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Thinking string `json:"thinking,omitempty"`
	Name     string `json:"name,omitempty"` // for tool_use
	ID       string `json:"id,omitempty"`
}

// claudeMessage is the shape of the "message" field in assistant events.
type claudeMessage struct {
	Role    string              `json:"role"`
	Content []claudeContentItem `json:"content"`
}

// claudeStreamEvent represents a stream_event from --include-partial-messages.
type claudeStreamEvent struct {
	Type         string          `json:"type"` // message_start, content_block_start, content_block_delta, content_block_stop, message_stop
	ContentBlock json.RawMessage `json:"content_block,omitempty"`
	Delta        json.RawMessage `json:"delta,omitempty"`
}

type claudeDelta struct {
	Type     string `json:"type"` // text_delta, thinking_delta
	Text     string `json:"text,omitempty"`
	Thinking string `json:"thinking,omitempty"`
}

type claudeContentBlock struct {
	Type string `json:"type"` // thinking, text, tool_use
	Name string `json:"name,omitempty"`
}

// parseLine parses one NDJSON line from Claude Code's stdout and returns an
// AgentEvent. Returns (zero, false) when the line should be silently skipped.
func parseLine(line []byte) (AgentEvent, bool) {
	var raw claudeRawEvent
	if err := json.Unmarshal(line, &raw); err != nil {
		return AgentEvent{}, false
	}

	switch raw.Type {
	case "stream_event":
		return parseStreamEvent(raw)
	case "assistant":
		// With --include-partial-messages, we get both stream_event (token-level)
		// and assistant (full text). Skip assistant to avoid double-rendering.
		// The stream_event deltas already provide the text incrementally.
		return AgentEvent{}, false
	case "result":
		return AgentEvent{Type: EventDone}, true
	default:
		return AgentEvent{}, false
	}
}

// parseStreamEvent handles token-level streaming events from --include-partial-messages.
func parseStreamEvent(raw claudeRawEvent) (AgentEvent, bool) {
	if raw.Event == nil {
		return AgentEvent{}, false
	}
	var se claudeStreamEvent
	if err := json.Unmarshal(raw.Event, &se); err != nil {
		return AgentEvent{}, false
	}

	switch se.Type {
	case "content_block_start":
		if se.ContentBlock != nil {
			var cb claudeContentBlock
			if err := json.Unmarshal(se.ContentBlock, &cb); err == nil {
				switch cb.Type {
				case "thinking":
					return AgentEvent{Type: EventThinking}, true
				case "tool_use":
					return AgentEvent{Type: EventToolUse, ToolName: cb.Name}, true
				}
			}
		}
		return AgentEvent{}, false

	case "content_block_delta":
		if se.Delta != nil {
			var d claudeDelta
			if err := json.Unmarshal(se.Delta, &d); err == nil {
				switch d.Type {
				case "text_delta":
					if d.Text != "" {
						return AgentEvent{Type: EventText, Text: d.Text}, true
					}
				case "thinking_delta":
					// Thinking deltas — we already showed "thinking..." on block start
					return AgentEvent{}, false
				}
			}
		}
		return AgentEvent{}, false

	default:
		return AgentEvent{}, false
	}
}

// ---------------------------------------------------------------------------
// BuildSystemPrompt
// ---------------------------------------------------------------------------

// BuildSystemPrompt generates the --append-system-prompt value for the agent.
func BuildSystemPrompt(config AgentConfig) string {
	var sb strings.Builder

	sb.WriteString("You are participating in a group chat room called \"parley\". ")
	sb.WriteString("You are one of several participants — some human, some AI coding agents — collaborating as peers.\n\n")

	sb.WriteString(fmt.Sprintf("ROOM: %s\n", config.Topic))
	sb.WriteString("PARTICIPANTS:\n")
	for _, p := range config.Participants {
		sb.WriteString(fmt.Sprintf("- %s (%s), working in %s\n", p.Name, p.Role, p.Directory))
	}
	sb.WriteString("\n")

	sb.WriteString(fmt.Sprintf("YOU ARE: %s, %s, working in %s\n\n", config.Name, config.Role, config.Directory))

	sb.WriteString(`RESPONSE GUIDELINES:
- ALWAYS respond when someone @-mentions you by name
- Respond when the discussion is directly relevant to your role/expertise
- Do NOT respond when another participant is better suited to answer
- Do NOT respond just to agree — only add substance
- If unsure whether to respond, default to staying silent
- Keep responses focused and concise — this is a chat, not a monologue
- You can @-mention other participants to ask them questions

When you respond, just write your message directly. Do not prefix it with your name.`)

	return sb.String()
}

// ---------------------------------------------------------------------------
// BuildInputMessage
// ---------------------------------------------------------------------------

// BuildInputMessage builds one NDJSON line for Claude Code's stdin.
// The result is always newline-terminated.
func BuildInputMessage(text string) []byte {
	type contentItem struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type message struct {
		Role    string        `json:"role"`
		Content []contentItem `json:"content"`
	}
	type envelope struct {
		Type    string  `json:"type"`
		Message message `json:"message"`
	}

	env := envelope{
		Type: "user",
		Message: message{
			Role: "user",
			Content: []contentItem{
				{Type: "text", Text: text},
			},
		},
	}

	data, _ := json.Marshal(env)
	return append(data, '\n')
}

// ---------------------------------------------------------------------------
// BuildArgs
// ---------------------------------------------------------------------------

// BuildArgs constructs the argument slice for the claude subprocess.
func BuildArgs(config AgentConfig) []string {
	args := []string{
		"-p",
		"--verbose",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--include-partial-messages",
		"--append-system-prompt", config.SystemPrompt,
	}
	args = append(args, config.Args...)
	return args
}
