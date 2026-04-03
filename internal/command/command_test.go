package command

import (
	"errors"
	"strings"
	"testing"
)

// mockRoom implements RoomQuerier for testing.
type mockRoom struct {
	id           string
	topic        string
	port         int
	participants []ParticipantInfo
	messageCount int
}

func (m *mockRoom) GetID() string                        { return m.id }
func (m *mockRoom) GetTopic() string                     { return m.topic }
func (m *mockRoom) GetPort() int                         { return m.port }
func (m *mockRoom) GetParticipantSnapshot() []ParticipantInfo { return m.participants }
func (m *mockRoom) GetMessageCount() int                 { return m.messageCount }

func newTestRoom() *mockRoom {
	return &mockRoom{
		id:    "room-abc",
		topic: "test-topic",
		port:  9000,
		participants: []ParticipantInfo{
			{Name: "host-user", Role: "human", Directory: "/home/user"},
			{Name: "atlas", Role: "agent", Directory: "/tmp/atlas", AgentType: "claude"},
		},
		messageCount: 42,
	}
}

func TestRegistryDispatch(t *testing.T) {
	reg := NewRegistry()
	reg.Register(InfoCommand)
	reg.Register(SaveCommand)

	ctx := Context{Room: newTestRoom()}
	result := reg.Execute(ctx, "/info")
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.LocalMessage == "" {
		t.Fatal("expected non-empty local message from /info")
	}
}

func TestRegistryUnknownCommand(t *testing.T) {
	reg := NewRegistry()
	reg.Register(InfoCommand)
	reg.Register(SaveCommand)

	ctx := Context{Room: newTestRoom()}
	result := reg.Execute(ctx, "/foo")
	if result.Error == nil {
		t.Fatal("expected error for unknown command")
	}
	errMsg := result.Error.Error()
	if !strings.Contains(errMsg, "/foo") {
		t.Errorf("error should mention /foo, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "/info") || !strings.Contains(errMsg, "/save") {
		t.Errorf("error should list available commands, got: %s", errMsg)
	}
}

func TestRegistryNotACommand(t *testing.T) {
	reg := NewRegistry()
	result := reg.Execute(Context{}, "hello world")
	if result.Error == nil {
		t.Fatal("expected error for non-command input")
	}
}

func TestIsCommand(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"/info", true},
		{"/save", true},
		{"  /info  ", true},
		{"/", false},
		{"hello", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsCommand(tt.input); got != tt.want {
			t.Errorf("IsCommand(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestInfoCommand(t *testing.T) {
	ctx := Context{Room: newTestRoom()}
	result := InfoCommand.Execute(ctx, "")
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	msg := result.LocalMessage
	for _, want := range []string{"room-abc", "test-topic", "9000", "42", "host-user", "atlas"} {
		if !strings.Contains(msg, want) {
			t.Errorf("info output should contain %q, got:\n%s", want, msg)
		}
	}
}

func TestSaveCommandSuccess(t *testing.T) {
	saved := false
	ctx := Context{
		Room:   newTestRoom(),
		SaveFn: func() error { saved = true; return nil },
	}
	result := SaveCommand.Execute(ctx, "")
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if !saved {
		t.Fatal("SaveFn was not called")
	}
	if !strings.Contains(result.LocalMessage, "saved") {
		t.Errorf("expected success message, got: %s", result.LocalMessage)
	}
}

func TestSaveCommandFailure(t *testing.T) {
	ctx := Context{
		Room:   newTestRoom(),
		SaveFn: func() error { return errors.New("disk full") },
	}
	result := SaveCommand.Execute(ctx, "")
	if result.Error == nil {
		t.Fatal("expected error on save failure")
	}
	if !strings.Contains(result.Error.Error(), "disk full") {
		t.Errorf("expected wrapped error, got: %v", result.Error)
	}
}

func TestSaveCommandNilFn(t *testing.T) {
	ctx := Context{Room: newTestRoom()}
	result := SaveCommand.Execute(ctx, "")
	if result.Error == nil {
		t.Fatal("expected error when SaveFn is nil")
	}
}

func TestSendCommandSuccess(t *testing.T) {
	var sentTo, sentText string
	ctx := Context{
		Room: newTestRoom(),
		SendFn: func(to, text string) {
			sentTo = to
			sentText = text
		},
	}
	result := SendCommandCommand.Execute(ctx, "@atlas /prune")
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if sentTo != "atlas" {
		t.Errorf("expected sentTo=atlas, got %q", sentTo)
	}
	if sentText != "/prune" {
		t.Errorf("expected sentText=/prune, got %q", sentText)
	}
	if !strings.Contains(result.LocalMessage, "atlas") {
		t.Errorf("expected local message to mention atlas, got: %s", result.LocalMessage)
	}
}

func TestSendCommandMissingArgs(t *testing.T) {
	ctx := Context{Room: newTestRoom()}
	result := SendCommandCommand.Execute(ctx, "")
	if result.Error == nil {
		t.Fatal("expected error for empty args")
	}
}

func TestSendCommandNoAt(t *testing.T) {
	ctx := Context{Room: newTestRoom()}
	result := SendCommandCommand.Execute(ctx, "atlas /prune")
	if result.Error == nil {
		t.Fatal("expected error when agent name doesn't start with @")
	}
}

func TestSendCommandUnknownAgent(t *testing.T) {
	ctx := Context{Room: newTestRoom()}
	result := SendCommandCommand.Execute(ctx, "@unknown /prune")
	if result.Error == nil {
		t.Fatal("expected error for unknown agent")
	}
	if !strings.Contains(result.Error.Error(), "unknown") {
		t.Errorf("error should mention agent name, got: %v", result.Error)
	}
}

func TestSendCommandNoSubCommand(t *testing.T) {
	ctx := Context{Room: newTestRoom()}
	result := SendCommandCommand.Execute(ctx, "@atlas")
	if result.Error == nil {
		t.Fatal("expected error when no sub-command specified")
	}
}
