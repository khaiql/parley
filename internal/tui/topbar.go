package tui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

// TopBar renders the header bar showing the app name, topic, and port.
type TopBar struct {
	topic string
	port  int
	width int
}

// NewTopBar creates a TopBar with the given topic and port.
func NewTopBar(topic string, port int) TopBar {
	return TopBar{topic: topic, port: port}
}

// SetWidth updates the available width for rendering.
func (t *TopBar) SetWidth(w int) {
	t.width = w
}

// View renders the top bar as a string.
func (t TopBar) View() string {
	appName := lipgloss.NewStyle().Bold(true).Foreground(colorPrimary).Render("parley")

	right := ""
	if t.port > 0 {
		right = lipgloss.NewStyle().Foreground(colorText).Render(fmt.Sprintf(":%d", t.port))
	}

	middle := ""
	if t.topic != "" {
		middle = lipgloss.NewStyle().Foreground(colorText).Render(t.topic)
	}

	// Calculate spacing to spread content across the width.
	// topBarStyle has Padding(0,1) which adds 2 chars per side (left+right = 2 total padding).
	innerWidth := t.width - 2
	if innerWidth < 0 {
		innerWidth = 0
	}

	leftLen := lipgloss.Width(appName)
	rightLen := lipgloss.Width(right)
	middleLen := lipgloss.Width(middle)

	// Place appName on the left, right portion on the right, middle centered.
	spacerRight := innerWidth - leftLen - middleLen - rightLen
	if spacerRight < 0 {
		spacerRight = 0
	}
	// Distribute space: gap between left and middle, gap between middle and right.
	leftGap := (spacerRight) / 2
	rightGap := spacerRight - leftGap

	line := appName +
		lipgloss.NewStyle().Width(leftGap).Render("") +
		middle +
		lipgloss.NewStyle().Width(rightGap).Render("") +
		right

	return topBarStyle.Width(t.width).Render(line)
}
