package tui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

// TopBar renders the header bar showing the app name, topic, and port.
type TopBar struct {
	topic string
	port  int
	name  string // agent name (empty for host)
	role  string // agent role (empty for host)
	width int
}

// NewTopBar creates a TopBar with the given topic and port.
func NewTopBar(topic string, port int) TopBar {
	return TopBar{topic: topic, port: port}
}

// SetAgent sets the agent name and role displayed on the left of the topbar.
func (t *TopBar) SetAgent(name, role string) {
	t.name = name
	t.role = role
}

// SetWidth updates the available width for rendering.
func (t *TopBar) SetWidth(w int) {
	t.width = w
}

// View renders the top bar as a string.
func (t TopBar) View() string {
	// Left: app name, plus agent name/role if set.
	left := lipgloss.NewStyle().Bold(true).Foreground(colorPrimary).Render("parley")
	if t.name != "" {
		agentInfo := lipgloss.NewStyle().Foreground(colorText).Render(" · " + t.name)
		if t.role != "" {
			agentInfo += lipgloss.NewStyle().Foreground(colorDimText).Render(" (" + t.role + ")")
		}
		left += agentInfo
	}

	// Right: port in primary color (bright and visible)
	right := ""
	if t.port > 0 {
		right = lipgloss.NewStyle().Foreground(colorPrimary).Render(fmt.Sprintf(":%d", t.port))
	}

	// Center: "Topic:" label + topic text
	middle := ""
	if t.topic != "" {
		label := lipgloss.NewStyle().Foreground(colorText).Render("Topic:")
		text := lipgloss.NewStyle().Foreground(colorText).Render(t.topic)
		middle = label + " " + text
	}

	// topBarStyle has Padding(0,1) which adds 1 char on each side = 2 total.
	innerWidth := t.width - 2
	if innerWidth < 0 {
		innerWidth = 0
	}

	leftLen := lipgloss.Width(left)
	rightLen := lipgloss.Width(right)
	middleLen := lipgloss.Width(middle)

	// Distribute remaining space: left-gap and right-gap around the center.
	remaining := innerWidth - leftLen - middleLen - rightLen
	if remaining < 0 {
		remaining = 0
	}
	leftGap := remaining / 2
	rightGap := remaining - leftGap

	line := left +
		lipgloss.NewStyle().Width(leftGap).Render("") +
		middle +
		lipgloss.NewStyle().Width(rightGap).Render("") +
		right

	return topBarStyle.Width(t.width).Render(line)
}
