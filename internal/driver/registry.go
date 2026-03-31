package driver

import (
	"fmt"
	"path/filepath"
	"strings"
)

// NewDriver creates the appropriate AgentDriver for the given command name.
// Use filepath.Base so paths like /usr/bin/claude still match.
func NewDriver(command string) (AgentDriver, error) {
	switch strings.ToLower(filepath.Base(command)) {
	case "claude":
		return &ClaudeDriver{}, nil
	case "gemini":
		return &GeminiDriver{}, nil
	default:
		return nil, fmt.Errorf("unsupported agent command: %q (supported: claude, gemini)", command)
	}
}
