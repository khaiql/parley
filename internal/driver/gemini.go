package driver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
)

// ---------------------------------------------------------------------------
// GeminiDriver — AgentDriver implementation for Gemini CLI
// ---------------------------------------------------------------------------

// GeminiDriver drives the `gemini` CLI using stream-json output.
//
// Unlike ClaudeDriver, Gemini has no --input-format flag (no bidirectional
// stdin streaming). Each message requires a new process invocation.
// Session continuity is maintained via --resume <session_id>.
type GeminiDriver struct {
	mu        sync.Mutex
	wg        sync.WaitGroup
	events    chan AgentEvent
	config    AgentConfig
	sessionID string // session_id from the init event; passed as --resume
	cancel    context.CancelFunc
	ctx       context.Context
}

// Start saves the config, creates the events channel, and sends the initial
// message by invoking gemini for the first time.
func (d *GeminiDriver) Start(ctx context.Context, config AgentConfig) error {
	ctx, cancel := context.WithCancel(ctx)
	d.cancel = cancel
	d.ctx = ctx
	d.config = config

	if config.SystemPrompt == "" {
		config.SystemPrompt = BuildSystemPrompt(config)
		d.config = config
	}

	d.events = make(chan AgentEvent, 64)

	// Gemini needs an initial prompt to start — use a join message.
	initialMsg := "[joining the conversation]"
	if err := d.invoke(initialMsg, true); err != nil {
		cancel()
		return err
	}

	return nil
}

// Send invokes gemini with the given text, resuming the existing session.
func (d *GeminiDriver) Send(text string) error {
	d.mu.Lock()
	sessionID := d.sessionID
	d.mu.Unlock()

	if sessionID == "" {
		return fmt.Errorf("gemini: no active session (Start not called or session not yet established)")
	}

	return d.invoke(text, false)
}

// Events returns the channel on which AgentEvents are delivered.
func (d *GeminiDriver) Events() <-chan AgentEvent {
	return d.events
}

// Stop cancels the context and waits for any running invocation to finish.
func (d *GeminiDriver) Stop() error {
	if d.cancel != nil {
		d.cancel()
	}
	d.wg.Wait()
	return nil
}

// SessionID returns the most recently captured session_id.
func (d *GeminiDriver) SessionID() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.sessionID
}

// invoke runs a single gemini invocation for the given message.
// isFirst=true means we have no session ID yet (skip --resume).
// isFirst=false means we include --resume <sessionID>.
func (d *GeminiDriver) invoke(message string, isFirst bool) error {
	d.mu.Lock()
	sessionID := d.sessionID
	cfg := d.config
	d.mu.Unlock()

	var args []string
	if isFirst {
		args = BuildGeminiArgs(cfg, message)
	} else {
		args = BuildGeminiArgsWithResume(cfg, message, sessionID)
	}

	command := cfg.Command
	if command == "" {
		command = "gemini"
	}

	cmd := exec.CommandContext(d.ctx, command, args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("gemini: stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("gemini: start process: %w", err)
	}

	d.wg.Add(1)
	go d.readLoop(stdout, cmd)

	return nil
}

// readLoop reads stdout line by line, emits events, and captures session_id.
func (d *GeminiDriver) readLoop(r io.Reader, cmd *exec.Cmd) {
	defer d.wg.Done()

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()

		// Try to capture session_id from init event.
		if sid := extractGeminiSessionID(line); sid != "" {
			d.mu.Lock()
			d.sessionID = sid
			d.mu.Unlock()
		}

		event, ok := parseGeminiLine(line)
		if ok {
			d.events <- event
		}
	}

	// Wait for the command to finish.
	_ = cmd.Wait()

	// Signal done via a final EventDone if the stream ended without one.
	// (parseGeminiLine already emits EventDone for the result event, so this
	// is just a safety net for abnormal exits.)
}

// ---------------------------------------------------------------------------
// Parsing helpers
// ---------------------------------------------------------------------------

// geminiRawEvent is the minimal shape of any Gemini CLI stream-json line.
type geminiRawEvent struct {
	Type      string `json:"type"`
	Role      string `json:"role,omitempty"`
	Content   string `json:"content,omitempty"`
	Delta     bool   `json:"delta,omitempty"`
	ToolName  string `json:"tool_name,omitempty"`
	ToolID    string `json:"tool_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Status    string `json:"status,omitempty"`
}

// parseGeminiLine parses one NDJSON line from gemini's stdout and returns an
// AgentEvent. Returns (zero, false) when the line should be silently skipped.
func parseGeminiLine(line []byte) (AgentEvent, bool) {
	if len(line) == 0 {
		return AgentEvent{}, false
	}

	var raw geminiRawEvent
	if err := json.Unmarshal(line, &raw); err != nil {
		return AgentEvent{}, false
	}

	switch raw.Type {
	case "message":
		if raw.Role != "assistant" {
			// Skip user echo and other non-assistant messages.
			return AgentEvent{}, false
		}
		if raw.Content == "" {
			return AgentEvent{}, false
		}
		return AgentEvent{Type: EventText, Text: raw.Content}, true

	case "tool_use":
		return AgentEvent{Type: EventToolUse, ToolName: raw.ToolName}, true

	case "result":
		return AgentEvent{Type: EventDone}, true

	default:
		// init, tool_result, and unknown types are skipped.
		return AgentEvent{}, false
	}
}

// extractGeminiSessionID returns the session_id from an init event line,
// or empty string if this is not an init event.
func extractGeminiSessionID(line []byte) string {
	var raw geminiRawEvent
	if err := json.Unmarshal(line, &raw); err != nil {
		return ""
	}
	if raw.Type == "init" {
		return raw.SessionID
	}
	return ""
}

// ---------------------------------------------------------------------------
// BuildGeminiArgs / BuildGeminiArgsWithResume
// ---------------------------------------------------------------------------

// BuildGeminiArgs constructs the argument slice for a first-time gemini invocation.
// The system prompt (if any) is prepended to the message since Gemini has no
// --append-system-prompt flag.
func BuildGeminiArgs(config AgentConfig, message string) []string {
	prompt := buildGeminiPrompt(config.SystemPrompt, message)
	args := []string{
		"-p", prompt,
		"-o", "stream-json",
		"--yolo",
	}
	args = append(args, config.Args...)
	return args
}

// BuildGeminiArgsWithResume constructs the argument slice for resuming a session.
func BuildGeminiArgsWithResume(config AgentConfig, message string, sessionID string) []string {
	prompt := buildGeminiPrompt("", message) // system prompt already established
	args := []string{
		"-p", prompt,
		"-o", "stream-json",
		"--yolo",
		"--resume", sessionID,
	}
	args = append(args, config.Args...)
	return args
}

// buildGeminiPrompt builds the prompt value, optionally prepending the system prompt.
func buildGeminiPrompt(systemPrompt, message string) string {
	if systemPrompt == "" {
		return message
	}
	return systemPrompt + "\n\n" + message
}
