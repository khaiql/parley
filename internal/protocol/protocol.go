// Package protocol defines JSON-RPC 2.0 types and NDJSON helpers for the
// Parley chat protocol.
package protocol

import (
	"encoding/json"
	"strings"
	"time"
)

// ---- Protocol method names ---------------------------------------------------

const (
	MethodJoin    = "room.join"    // client → server: join a room
	MethodSend    = "room.send"    // client → server: send a chat message
	MethodStatus  = "room.status"  // bidirectional: participant activity status
	MethodState   = "room.state"   // server → client: full room state on join
	MethodMessage = "room.message" // server → client: broadcast chat message
	MethodJoined  = "room.joined"  // server → client: participant joined
	MethodLeft    = "room.left"    // server → client: participant left
)

// ---- JSON-RPC 2.0 base types ------------------------------------------------

// Notification is a JSON-RPC 2.0 notification (no id, no reply expected).
type Notification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Request is a JSON-RPC 2.0 request (has id, expects a response).
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is a JSON-RPC 2.0 response.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError holds a JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// RawMessage is a generic decoded message used before the type is known.
// It captures all possible fields; callers inspect Method/ID to determine
// whether the message is a notification, request, or response.
type RawMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// ---- Domain param types -----------------------------------------------------

// Content is a single piece of message content.
type Content struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// MessageParams is the params payload for a "chat/message" notification.
type MessageParams struct {
	ID        string    `json:"id"`
	Seq       int       `json:"seq"`
	From      string    `json:"from"`
	Source    string    `json:"source,omitempty"`
	Role      string    `json:"role"`
	Timestamp time.Time `json:"timestamp,omitempty"`
	Mentions  []string  `json:"mentions,omitempty"`
	Content   []Content `json:"content"`
}

// SendParams is the params payload for a "chat/send" request.
type SendParams struct {
	Content  []Content `json:"content"`
	Mentions []string  `json:"mentions,omitempty"`
}

// Known agent types.
const (
	AgentTypeClaude = "claude"
	AgentTypeGemini = "gemini"
	AgentTypeCodex  = "codex"
)

// JoinParams is the params payload for a "room/join" notification.
type JoinParams struct {
	Name      string `json:"name"`
	Role      string `json:"role"`
	Directory string `json:"directory,omitempty"`
	Repo      string `json:"repo,omitempty"`
	AgentType string `json:"agent_type,omitempty"`
}

// JoinedParams is the server-side confirmation payload for "room/joined".
type JoinedParams struct {
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	Directory string    `json:"directory,omitempty"`
	Repo      string    `json:"repo,omitempty"`
	AgentType string    `json:"agent_type,omitempty"`
	JoinedAt  time.Time `json:"joined_at"`
}

// LeftParams is the params payload for a "room/left" notification.
type LeftParams struct {
	Name string `json:"name"`
}

// ---- Status constants -------------------------------------------------------

const (
	StatusIdle       = ""           // participant is idle
	StatusThinking   = "thinking"   // processing input
	StatusGenerating = "generating" // producing output
	StatusListening  = "listening"  // passively observing
)

// StatusUsingTool returns a status string for tool use, optionally with name.
func StatusUsingTool(toolName string) string {
	if toolName != "" {
		return "using: " + toolName
	}
	return "using tool"
}

// IsUsingTool reports whether status indicates tool use.
func IsUsingTool(status string) bool {
	return status == "using tool" || len(status) > 7 && status[:7] == "using: "
}

// StatusParams is the params payload for a "room.status" notification.
// Status is a short description of what the participant is doing, e.g.
// "thinking", "using: Read", or "" to indicate idle.
type StatusParams struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// Participant describes a single participant in a room.
type Participant struct {
	Name      string `json:"name"`
	Role      string `json:"role"`
	Directory string `json:"directory,omitempty"`
	Repo      string `json:"repo,omitempty"`
	AgentType string `json:"agent_type,omitempty"`
	Source    string `json:"source,omitempty"`
	Online    bool   `json:"online"`
}

// IsHuman returns true if this participant is a human (not an AI agent).
// The canonical check is Role == "human" — only the host sets this value.
// Source is unreliable because agents joining via `parley join` get Source="human".
func (p Participant) IsHuman() bool {
	return p.Role == "human"
}

// IsHuman returns true if the message was sent by a human participant.
func (m MessageParams) IsHuman() bool {
	return m.Role == "human"
}

// IsSystem returns true if the message is a system-generated notification.
func (m MessageParams) IsSystem() bool {
	return m.Source == "system" || (m.Source == "" && m.Role == "system")
}

// RoomStateParams is the params payload for a "room/state" notification.
type RoomStateParams struct {
	RoomID       string          `json:"room_id,omitempty"`
	Topic        string          `json:"topic,omitempty"`
	AutoApprove  bool            `json:"auto_approve,omitempty"`
	Participants []Participant   `json:"participants"`
	Messages     []MessageParams `json:"messages,omitempty"`
}

// RoomSnapshot is a plain data container for persisting and restoring room state.
type RoomSnapshot struct {
	RoomID       string          `json:"room_id"`
	Topic        string          `json:"topic"`
	AutoApprove  bool            `json:"auto_approve,omitempty"`
	Participants []Participant   `json:"participants"`
	Messages     []MessageParams `json:"messages"`
}

// ---- Helper functions -------------------------------------------------------

// ParseMentions extracts @mention tokens from a message string.
// A mention is any whitespace-delimited token that starts with "@" and has at
// least one additional character. The returned slice contains the names without
// the leading "@".
func ParseMentions(text string) []string {
	var mentions []string
	for _, word := range strings.Fields(text) {
		if strings.HasPrefix(word, "@") && len(word) > 1 {
			mentions = append(mentions, word[1:])
		}
	}
	return mentions
}

// MatchMentions matches @tokens in text against a list of known names.
// Returns matched names in order of appearance. Case-insensitive.
// Handles punctuation after names (e.g. "@bob's", "@alice!").
func MatchMentions(text string, names []string) []string {
	var mentions []string
	seen := make(map[string]bool)
	for _, word := range strings.Fields(text) {
		if !strings.HasPrefix(word, "@") || len(word) < 2 {
			continue
		}
		token := word[1:]
		for _, name := range names {
			lower := strings.ToLower(token)
			lowerName := strings.ToLower(name)
			if strings.EqualFold(token, name) ||
				(strings.HasPrefix(lower, lowerName) && len(token) > len(name) && !isNameChar(token[len(name)])) {
				if !seen[name] {
					mentions = append(mentions, name)
					seen[name] = true
				}
			}
		}
	}
	return mentions
}

// isNameChar returns true if c could be part of a participant name.
func isNameChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-'
}

// EncodeLine marshals v to JSON and appends a newline, returning the result.
// This produces one NDJSON line suitable for writing to a TCP stream.
func EncodeLine(v interface{}) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// DecodeLine unmarshals a single JSON line (with or without trailing newline)
// into a RawMessage so the caller can inspect the type before further decoding.
func DecodeLine(line []byte) (*RawMessage, error) {
	// Trim trailing whitespace/newline so json.Unmarshal doesn't complain.
	trimmed := line
	for len(trimmed) > 0 && (trimmed[len(trimmed)-1] == '\n' || trimmed[len(trimmed)-1] == '\r') {
		trimmed = trimmed[:len(trimmed)-1]
	}
	var msg RawMessage
	if err := json.Unmarshal(trimmed, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

// NewNotification is a convenience constructor that creates a Notification with
// jsonrpc set to "2.0" and params marshalled from the provided value.
func NewNotification(method string, params interface{}) Notification {
	raw, _ := json.Marshal(params)
	return Notification{
		JSONRPC: "2.0",
		Method:  method,
		Params:  json.RawMessage(raw),
	}
}
