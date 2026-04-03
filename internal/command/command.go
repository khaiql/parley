// Package command provides a registry-based slash command system for the
// Parley host TUI. Each command is self-contained; adding a new one requires
// only a new file and a single Register call.
package command

// RoomQuerier abstracts the read-only room data that commands need.
// This avoids coupling commands directly to *server.Room.
type RoomQuerier interface {
	GetID() string
	GetTopic() string
	GetPort() int
	GetParticipantSnapshot() []ParticipantInfo
	GetMessageCount() int
}

// ParticipantInfo is a simplified view of a room participant for command output.
type ParticipantInfo struct {
	Name      string
	Role      string
	Directory string
	AgentType string
}

// Context carries everything a command needs to execute.
type Context struct {
	Room   RoomQuerier
	SaveFn func() error          // triggers an immediate room save
	SendFn func(to, text string) // sends a message to a specific agent
}

// Result is what a command returns to the TUI.
type Result struct {
	LocalMessage string // displayed as a local system message
	Error        error
}

// Command describes a single slash command.
type Command struct {
	Name        string                          // e.g. "info"
	Usage       string                          // e.g. "/info"
	Description string                          // short help text
	Execute     func(ctx Context, args string) Result
}
