package room

import (
	"testing"

	"github.com/khaiql/parley/internal/command"
)

func TestExecuteCommand_ReturnsContent(t *testing.T) {
	reg := command.NewRegistry()
	reg.Register(&command.Command{
		Name:        "test",
		Usage:       "/test",
		Description: "A test command",
		Execute: func(_ command.Context, _ string) command.Result {
			return command.Result{
				Modal: &command.ModalContent{
					Title: "Test Modal",
					Body:  "Hello from test",
				},
			}
		},
	})
	s := New(reg, command.Context{})

	result := s.ExecuteCommand("/test")
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Modal == nil {
		t.Fatal("expected modal content, got nil")
	}
	if result.Modal.Title != "Test Modal" {
		t.Errorf("expected title %q, got %q", "Test Modal", result.Modal.Title)
	}
	if result.Modal.Body != "Hello from test" {
		t.Errorf("expected body %q, got %q", "Hello from test", result.Modal.Body)
	}
}

func TestExecuteCommand_UnknownCommand(t *testing.T) {
	reg := command.NewRegistry()
	s := New(reg, command.Context{})

	result := s.ExecuteCommand("/nonexistent")
	if result.Error == nil {
		t.Fatal("expected error for unknown command, got nil")
	}
}

func TestExecuteCommand_NilRegistry(t *testing.T) {
	s := New(nil, command.Context{})

	result := s.ExecuteCommand("/anything")
	if result.Error == nil {
		t.Fatal("expected error when registry is nil, got nil")
	}
}

func TestSendMessage_CallsSendFn(t *testing.T) {
	s := New(nil, command.Context{})

	var gotText string
	var gotMentions []string
	s.SetSendFn(func(text string, mentions []string) {
		gotText = text
		gotMentions = mentions
	})

	s.SendMessage("hello world", []string{"alice", "bob"})

	if gotText != "hello world" {
		t.Errorf("expected text %q, got %q", "hello world", gotText)
	}
	if len(gotMentions) != 2 || gotMentions[0] != "alice" || gotMentions[1] != "bob" {
		t.Errorf("expected mentions [alice bob], got %v", gotMentions)
	}
}

func TestSendMessage_NilSendFn(t *testing.T) {
	s := New(nil, command.Context{})

	// Should not panic.
	s.SendMessage("hello", nil)
}
