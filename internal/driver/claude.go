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

	go d.readLoop(stdout)

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

// Stop terminates the agent process.
func (d *ClaudeDriver) Stop() error {
	if d.cancel != nil {
		d.cancel()
	}
	if d.stdin != nil {
		_ = d.stdin.Close()
	}
	if d.cmd != nil && d.cmd.Process != nil {
		return d.cmd.Process.Kill()
	}
	return nil
}

// readLoop reads stdout line by line and emits AgentEvents.
func (d *ClaudeDriver) readLoop(r io.Reader) {
	defer close(d.events)
	scanner := bufio.NewScanner(r)
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
}

// claudeContentItem represents one item in message.content[].
type claudeContentItem struct {
	Type  string `json:"type"`
	Text  string `json:"text,omitempty"`
	Name  string `json:"name,omitempty"` // for tool_use
	ID    string `json:"id,omitempty"`
}

// claudeMessage is the shape of the "message" field in assistant events.
type claudeMessage struct {
	Role    string              `json:"role"`
	Content []claudeContentItem `json:"content"`
}

// parseLine parses one NDJSON line from Claude Code's stdout and returns an
// AgentEvent. Returns (zero, false) when the line should be silently skipped.
func parseLine(line []byte) (AgentEvent, bool) {
	var raw claudeRawEvent
	if err := json.Unmarshal(line, &raw); err != nil {
		return AgentEvent{}, false
	}

	switch raw.Type {
	case "assistant":
		return parseAssistantEvent(raw)
	case "result":
		return AgentEvent{Type: EventDone}, true
	default:
		// system, rate_limit_event, and anything else — skip silently.
		return AgentEvent{}, false
	}
}

// parseAssistantEvent extracts text and tool-use content from an assistant event.
func parseAssistantEvent(raw claudeRawEvent) (AgentEvent, bool) {
	if raw.Message == nil {
		return AgentEvent{}, false
	}
	var msg claudeMessage
	if err := json.Unmarshal(raw.Message, &msg); err != nil {
		return AgentEvent{}, false
	}

	var texts []string
	for _, item := range msg.Content {
		switch item.Type {
		case "text":
			if item.Text != "" {
				texts = append(texts, item.Text)
			}
		case "tool_use":
			return AgentEvent{Type: EventToolUse, ToolName: item.Name}, true
		}
	}

	if len(texts) > 0 {
		return AgentEvent{Type: EventText, Text: strings.Join(texts, "")}, true
	}
	return AgentEvent{}, false
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
		"--append-system-prompt", config.SystemPrompt,
	}
	args = append(args, config.Args...)
	return args
}
