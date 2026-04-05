// Package driver defines the AgentDriver interface and associated event types
// for spawning and communicating with AI coding agent subprocesses.
package driver

import "context"

// EventType identifies the kind of event emitted by an AgentDriver.
type EventType int

const (
	// EventText is emitted when the agent produces text output.
	EventText EventType = iota
	// EventThinking is emitted when the agent produces internal thinking.
	EventThinking
	// EventToolUse is emitted when the agent invokes a tool.
	EventToolUse
	// EventToolResult is emitted when a tool returns a result.
	EventToolResult
	// EventDone is emitted when the agent finishes a turn.
	EventDone
	// EventError is emitted when an unrecoverable error occurs.
	EventError
)

// AgentEvent carries a single event from a running agent.
type AgentEvent struct {
	Type     EventType
	Text     string
	ToolName string
}

// DebugLogger is the interface for debug WAL logging.
// It is satisfied by *wal.Writer but defined here to avoid a circular import.
type DebugLogger interface {
	Log(direction string, raw []byte) error
}

// AgentConfig holds all configuration needed to start an agent process.
type AgentConfig struct {
	Command         string   // e.g., "claude"
	Args            []string // extra args, e.g. ["--worktree"]
	Name            string
	Role            string
	Directory       string
	Repo            string
	Topic           string
	Participants    []ParticipantInfo
	SystemPrompt    string
	InitialMessage  string // if set, used as the first prompt (for drivers that need one in Start)
	ResumeSessionID string // if set, pass --resume <id> to the driver
	AutoApprove     bool        // if set, append driver-specific auto-approve flag
	DebugWriter     DebugLogger // if set, log all raw I/O to this writer
}

// ParticipantInfo describes one participant in the room (used for system prompt).
type ParticipantInfo struct {
	Name      string
	Role      string
	Directory string
}

// AgentDriver is the interface for spawning and communicating with an agent subprocess.
type AgentDriver interface {
	// Start spawns the agent process and begins streaming events.
	Start(ctx context.Context, config AgentConfig) error
	// Send delivers a chat message to the running agent.
	Send(text string) error
	// Events returns the channel on which AgentEvents are delivered.
	Events() <-chan AgentEvent
	// Stop terminates the agent process.
	Stop() error
	// SessionID returns the most recently captured session ID from the agent.
	// Returns empty string if no session has been established yet.
	SessionID() string
}
