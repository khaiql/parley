package room

import (
	"errors"

	"github.com/khaiql/parley/internal/command"
)

// ExecuteCommand runs a slash command and returns the result.
func (s *State) ExecuteCommand(text string) command.Result {
	if s.commands == nil {
		return command.Result{Error: errors.New("no command registry configured")}
	}
	return s.commands.Execute(s.cmdCtx, text)
}

// SendMessage sends a message with mentions over the network via sendFn.
func (s *State) SendMessage(text string, mentions []string) {
	if s.sendFn != nil {
		s.sendFn(text, mentions)
	}
}
