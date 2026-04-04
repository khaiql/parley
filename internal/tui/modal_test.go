package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/khaiql/parley/internal/command"
)

func TestModal_View_ContainsTitleAndBody(t *testing.T) {
	content := &command.ModalContent{Title: "Room Info", Body: "Port: 9000\nTopic: test"}
	m := NewModal(content, 80, 24)
	view := m.View()

	if !strings.Contains(view, "Room Info") {
		t.Errorf("expected title 'Room Info' in view, got:\n%s", view)
	}
	if !strings.Contains(view, "Port: 9000") {
		t.Errorf("expected body content in view, got:\n%s", view)
	}
}

func TestModal_View_ContainsDismissHint(t *testing.T) {
	content := &command.ModalContent{Title: "Info", Body: "body"}
	m := NewModal(content, 80, 24)
	view := m.View()

	if !strings.Contains(view, "esc") && !strings.Contains(view, "Esc") {
		t.Errorf("expected dismiss hint in view, got:\n%s", view)
	}
}

func TestModal_Update_ScrollingDoesNotPanic(t *testing.T) {
	content := &command.ModalContent{Title: "Info", Body: "line1\nline2\nline3"}
	m := NewModal(content, 80, 24)

	// Sending a PageDown should not panic.
	m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
}

func TestModal_Resize_UpdatesDimensions(t *testing.T) {
	content := &command.ModalContent{Title: "Info", Body: "body"}
	m := NewModal(content, 80, 24)
	m.Resize(120, 40)

	if m.termWidth != 120 || m.termHeight != 40 {
		t.Errorf("expected 120x40 after resize, got %dx%d", m.termWidth, m.termHeight)
	}
}
