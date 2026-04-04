package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// StatusBar renders the bottom status line.
type StatusBar struct {
	connected bool
	width     int
}

// NewStatusBar creates a StatusBar with default values.
func NewStatusBar() StatusBar {
	return StatusBar{connected: true}
}

// SetWidth sets the available width for rendering.
func (s *StatusBar) SetWidth(w int) {
	s.width = w
}

// SetConnected sets the connection status.
func (s *StatusBar) SetConnected(c bool) {
	s.connected = c
}

// View renders the status bar as a single line.
func (s StatusBar) View() string {
	barBg := lipgloss.NewStyle().Background(colorSidebarBg)

	helpStyle := lipgloss.NewStyle().
		Bold(true).
		Background(colorStatusBarBg).
		Foreground(colorText).
		Padding(0, 1)

	// Left: just help.
	left := helpStyle.Render("? help")

	// Right: connection status.
	var right string
	if s.connected {
		connStyle := barBg.Foreground(colorConnected).Padding(0, 1)
		right = connStyle.Render("● connected")
	} else {
		disconnStyle := barBg.Foreground(lipgloss.Color("#f85149")).Padding(0, 1)
		right = disconnStyle.Render("● disconnected")
	}

	// Fill gap.
	leftWidth := lipgloss.Width(left)
	rightWidth := lipgloss.Width(right)
	gap := s.width - leftWidth - rightWidth
	if gap < 0 {
		gap = 0
	}
	fill := barBg.Render(strings.Repeat(" ", gap))

	return left + fill + right
}
