package driver

import (
	"fmt"

	"github.com/khaiql/parley/internal/protocol"
)

// NewDriver creates the appropriate AgentDriver for the given agent type.
func NewDriver(agentType string) (AgentDriver, error) {
	switch protocol.NormalizeAgentType(agentType) {
	case protocol.AgentTypeClaude:
		return &ClaudeDriver{}, nil
	case protocol.AgentTypeGemini:
		return &GeminiDriver{}, nil
	case protocol.AgentTypeRovodev:
		return &RovodevDriver{}, nil
	default:
		return nil, fmt.Errorf("unsupported agent type: %q (supported: claude, gemini, rovodev)", agentType)
	}
}

// ConsumesInitialMessage reports whether the given agent type handles its
// initial message inside Start() rather than needing an explicit Send().
func ConsumesInitialMessage(agentType string) bool {
	switch protocol.NormalizeAgentType(agentType) {
	case protocol.AgentTypeGemini, protocol.AgentTypeRovodev:
		return true
	default:
		return false
	}
}
