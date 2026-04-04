package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/khaiql/parley/internal/protocol"
)

const updateGolden = false // set to true to regenerate golden files

func assertGolden(t *testing.T, name string, actual string) {
	t.Helper()
	goldenPath := filepath.Join("testdata", name+".golden")

	if updateGolden {
		os.MkdirAll("testdata", 0755)
		os.WriteFile(goldenPath, []byte(actual), 0644)
		t.Logf("Updated golden file: %s", goldenPath)
		return
	}

	expected, err := os.ReadFile(goldenPath)
	if err != nil {
		// Golden doesn't exist yet — create it
		os.MkdirAll("testdata", 0755)
		os.WriteFile(goldenPath, []byte(actual), 0644)
		t.Logf("Created golden file: %s (review and commit)", goldenPath)
		return
	}

	if string(expected) != actual {
		t.Errorf("Visual regression in %s.\nRun with updateGolden=true to update.\n\nExpected:\n%s\n\nGot:\n%s", name, string(expected), actual)
	}
}

// buildTestApp constructs an App with known data for visual regression testing.
func buildTestApp(t *testing.T, width, height int) App {
	t.Helper()

	app := NewApp("test topic", 1234, InputModeHuman, "sle", nil)

	// Add participants
	app.sidebar.AddParticipant(protocol.Participant{
		Name:   "sle",
		Role:   "human",
		Source: "human",
		Online: true,
	})
	app.sidebar.AddParticipant(protocol.Participant{
		Name:      "Alice",
		Role:      "backend",
		Directory: "/home/alice/project",
		AgentType: "claude",
		Online:    true,
	})

	// Fixed timestamp for determinism
	ts := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)

	// System message: Alice has joined
	app.chat.AddMessage(protocol.MessageParams{
		ID:        "msg-1",
		Seq:       1,
		From:      "system",
		Source:    "system",
		Role:      "system",
		Timestamp: ts,
		Content: []protocol.Content{
			{Type: "text", Text: "Alice has joined — backend"},
		},
	})

	// Human message from sle
	app.chat.AddMessage(protocol.MessageParams{
		ID:        "msg-2",
		Seq:       2,
		From:      "sle",
		Source:    "human",
		Role:      "human",
		Timestamp: ts.Add(time.Minute),
		Content: []protocol.Content{
			{Type: "text", Text: "Hello everyone"},
		},
	})

	// Apply window size
	model, _ := app.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return model.(App)
}

func TestVisualLayout80x24(t *testing.T) {
	app := buildTestApp(t, 80, 24)
	output := app.View()
	assertGolden(t, "layout_80x24", output)
}

func TestVisualLayout120x40(t *testing.T) {
	app := buildTestApp(t, 120, 40)
	output := app.View()
	assertGolden(t, "layout_120x40", output)
}

func TestVisualLayoutSmall40x10(t *testing.T) {
	app := buildTestApp(t, 40, 10)
	output := app.View()
	assertGolden(t, "layout_40x10", output)
}
